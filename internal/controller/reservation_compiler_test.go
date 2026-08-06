/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func compilerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		billingv1alpha1.AddToScheme,
		kueuev1beta1.AddToScheme,
		corev1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("registering scheme: %v", err)
		}
	}
	return s
}

func testResourceFlavor(name, gpuProduct string) *kueuev1beta1.ResourceFlavor {
	return &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{
				labelGPUProduct: gpuProduct,
			},
		},
	}
}

func testReservation(name, ns, tenant, sku, geo, class, state string, count int32) *billingv1alpha1.Reservation {
	return &billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:    tenant,
			SKU:          sku,
			Count:        count,
			Geo:          geo,
			Class:        class,
			MonthlyPrice: "1000.000000",
			Window: billingv1alpha1.ReservationWindow{
				Start: metav1.Now(),
				End:   metav1.Now(),
			},
		},
		Status: billingv1alpha1.ReservationStatus{State: state},
	}
}

func newCompilerClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(compilerTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.Reservation{}).
		Build()
}

func getCQ(t *testing.T, c client.Client, name string) *kueuev1beta1.ClusterQueue {
	t.Helper()
	var cq kueuev1beta1.ClusterQueue
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &cq); err != nil {
		t.Fatalf("getting CQ %s: %v", name, err)
	}
	return &cq
}

func cqGPUQuota(cq *kueuev1beta1.ClusterQueue) int64 {
	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			for _, r := range f.Resources {
				if r.Name == gpuResourceKey {
					return r.NominalQuota.Value()
				}
			}
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// compileKueueState
// ---------------------------------------------------------------------------

func TestCompileKueueState_GuaranteedCQCreated(t *testing.T) {
	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		testReservation("res-1", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateConfirmed, 4),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	cq := getCQ(t, c, "tenant-a-guaranteed")
	if got := cqGPUQuota(cq); got != 4 {
		t.Errorf("GPU quota = %d, want 4", got)
	}
	if cq.Labels[labelTenant] != "tenant-a" {
		t.Errorf("tenant label = %q, want tenant-a", cq.Labels[labelTenant])
	}
	if cq.Labels[labelReservationClass] != billingv1alpha1.ReservationClassGuaranteed {
		t.Errorf("class label = %q, want Guaranteed", cq.Labels[labelReservationClass])
	}
	if cq.Labels[labelManagedBy] != managedByReservation {
		t.Errorf("managed-by label = %q, want %q", cq.Labels[labelManagedBy], managedByReservation)
	}
}

func TestCompileKueueState_PreemptibleCQCreated(t *testing.T) {
	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		testReservation("res-1", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassPreemptible,
			billingv1alpha1.ReservationStateActive, 2),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	cq := getCQ(t, c, "tenant-a-preemptible")
	if got := cqGPUQuota(cq); got != 2 {
		t.Errorf("GPU quota = %d, want 2", got)
	}
	// Preemptible CQs should have BorrowingLimit = 0.
	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			for _, r := range f.Resources {
				if r.Name == gpuResourceKey {
					if r.BorrowingLimit == nil {
						t.Fatal("BorrowingLimit is nil, want 0")
					}
					if r.BorrowingLimit.Value() != 0 {
						t.Errorf("BorrowingLimit = %d, want 0", r.BorrowingLimit.Value())
					}
				}
			}
		}
	}
}

func TestCompileKueueState_MultipleReservationsAggregated(t *testing.T) {
	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		testReservation("res-1", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateConfirmed, 4),
		testReservation("res-2", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 2),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	cq := getCQ(t, c, "tenant-a-guaranteed")
	if got := cqGPUQuota(cq); got != 6 {
		t.Errorf("GPU quota = %d, want 6 (4 + 2)", got)
	}
}

func TestCompileKueueState_CQUpdatedOnChange(t *testing.T) {
	existingCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-guaranteed",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassGuaranteed,
				labelManagedBy:        managedByReservation,
			},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceKey},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "gpu-h200",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceKey,
						NominalQuota: resource.MustParse("4"),
					}},
				}},
			}},
		},
	}

	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		existingCQ,
		testReservation("res-1", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateConfirmed, 4),
		testReservation("res-2", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	cq := getCQ(t, c, "tenant-a-guaranteed")
	if got := cqGPUQuota(cq); got != 8 {
		t.Errorf("GPU quota = %d, want 8 (updated from 4)", got)
	}
}

func TestCompileKueueState_CQDeletedWhenNoReservations(t *testing.T) {
	existingCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-guaranteed",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassGuaranteed,
				labelManagedBy:        managedByReservation,
			},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceKey},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "gpu-h200",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceKey,
						NominalQuota: resource.MustParse("4"),
					}},
				}},
			}},
		},
	}

	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		existingCQ,
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	var cq kueuev1beta1.ClusterQueue
	err := c.Get(context.Background(), client.ObjectKey{Name: "tenant-a-guaranteed"}, &cq)
	if err == nil {
		t.Error("CQ should have been deleted, but still exists")
	}
}

func TestCompileKueueState_IgnoresRequestedAndExpired(t *testing.T) {
	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		testReservation("res-requested", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateRequested, 10),
		testReservation("res-expired", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateExpired, 10),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	// No CQ should be created since all reservations are in non-active states.
	var cq kueuev1beta1.ClusterQueue
	err := c.Get(context.Background(), client.ObjectKey{Name: "tenant-a-guaranteed"}, &cq)
	if err == nil {
		t.Error("CQ should not exist for Requested/Expired reservations")
	}
}

func TestCompileKueueState_CQDeletedOnCancel(t *testing.T) {
	existingCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-guaranteed",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassGuaranteed,
				labelManagedBy:        managedByReservation,
			},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceKey},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "gpu-h200",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceKey,
						NominalQuota: resource.MustParse("4"),
					}},
				}},
			}},
		},
	}

	c := newCompilerClient(t,
		testResourceFlavor("gpu-h200", "H200"),
		existingCQ,
		testReservation("res-cancelled", "ns-1", "tenant-a", "h200", "us-east",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateCancelled, 4),
	)

	if err := compileKueueState(context.Background(), c, "tenant-a"); err != nil {
		t.Fatalf("compileKueueState: %v", err)
	}

	var cq kueuev1beta1.ClusterQueue
	err := c.Get(context.Background(), client.ObjectKey{Name: "tenant-a-guaranteed"}, &cq)
	if err == nil {
		t.Error("CQ should have been deleted after reservation was cancelled")
	}
}

// ---------------------------------------------------------------------------
// triggerPreemption
// ---------------------------------------------------------------------------

func TestTriggerPreemption_DrainWhenGuaranteedExceedsCapacity(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	preemptibleCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-preemptible",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassPreemptible,
				labelManagedBy:        managedByReservation,
			},
			Annotations: map[string]string{annotationSKU: "h200", annotationGeo: "default"},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			StopPolicy: &nonePolicy,
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceKey},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "gpu-h200",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceKey,
						NominalQuota: resource.MustParse("2"),
					}},
				}},
			}},
		},
	}

	c := newCompilerClient(t, preemptibleCQ)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
	}

	if err := triggerPreemption(context.Background(), c, nodes, reservations, ""); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	updated := getCQ(t, c, "tenant-a-preemptible")
	if updated.Spec.StopPolicy == nil {
		t.Fatal("StopPolicy is nil, want HoldAndDrain")
	}
	if *updated.Spec.StopPolicy != kueuev1beta1.HoldAndDrain {
		t.Errorf("StopPolicy = %q, want HoldAndDrain", *updated.Spec.StopPolicy)
	}
}

func TestTriggerPreemption_ThawWhenCapacityAvailable(t *testing.T) {
	drainPolicy := kueuev1beta1.HoldAndDrain
	preemptibleCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-preemptible",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassPreemptible,
				labelManagedBy:        managedByReservation,
			},
			Annotations: map[string]string{annotationSKU: "h200", annotationGeo: "default"},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			StopPolicy: &drainPolicy,
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceKey},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "gpu-h200",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceKey,
						NominalQuota: resource.MustParse("2"),
					}},
				}},
			}},
		},
	}

	c := newCompilerClient(t, preemptibleCQ)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 8, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 2),
	}

	if err := triggerPreemption(context.Background(), c, nodes, reservations, ""); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	updated := getCQ(t, c, "tenant-a-preemptible")
	if updated.Spec.StopPolicy == nil {
		t.Fatal("StopPolicy is nil, want None")
	}
	if *updated.Spec.StopPolicy != kueuev1beta1.None {
		t.Errorf("StopPolicy = %q, want None", *updated.Spec.StopPolicy)
	}
}
