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
	"fmt"
	"testing"
	"time"

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

func testResourceFlavor(name, gpuProduct string) *kueuev1beta1.ResourceFlavor { //nolint:unparam
	return &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{
				labelGPUProduct: gpuProduct,
			},
		},
	}
}

func testReservation(name, ns, tenant, sku, geo, class, state string, count int32) *billingv1alpha1.Reservation { //nolint:unparam
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
	// BorrowingLimit is omitted (standalone CQ can't borrow; Kueue rejects
	// BorrowingLimit on CQs without a cohort). Verify it is nil.
	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			for _, r := range f.Resources {
				if r.Name == gpuResourceKey && r.BorrowingLimit != nil {
					t.Errorf("BorrowingLimit = %d, want nil (no cohort)", r.BorrowingLimit.Value())
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

func preemptibleCQFixture(stopPolicy *kueuev1beta1.StopPolicy) *kueuev1beta1.ClusterQueue {
	return &kueuev1beta1.ClusterQueue{
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
			StopPolicy: stopPolicy,
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
}

func fakeWorkload(name, ns, cqName string, admittedAt time.Time) *kueuev1beta1.Workload { //nolint:unparam
	active := true
	return &kueuev1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: kueuev1beta1.WorkloadSpec{
			Active: &active,
		},
		Status: kueuev1beta1.WorkloadStatus{
			Admission: &kueuev1beta1.Admission{
				ClusterQueue: kueuev1beta1.ClusterQueueReference(cqName),
			},
			Conditions: []metav1.Condition{{
				Type:               kueuev1beta1.WorkloadAdmitted,
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(admittedAt),
			}},
		},
	}
}

func getWorkload(t *testing.T, c client.Client, ns, name string) *kueuev1beta1.Workload { //nolint:unparam
	t.Helper()
	var wl kueuev1beta1.Workload
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &wl); err != nil {
		t.Fatalf("getting workload %s/%s: %v", ns, name, err)
	}
	return &wl
}

func TestTriggerPreemption_HoldWhenGuaranteedExceedsCapacity(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)
	c := newCompilerClient(t, cq)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", time.Now()); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	updated := getCQ(t, c, "tenant-a-preemptible")
	if updated.Spec.StopPolicy == nil {
		t.Fatal("StopPolicy is nil, want Hold")
	}
	if *updated.Spec.StopPolicy != kueuev1beta1.Hold {
		t.Errorf("StopPolicy = %q, want Hold", *updated.Spec.StopPolicy)
	}
}

func TestTriggerPreemption_ThawWhenCapacityAvailable(t *testing.T) {
	holdPolicy := kueuev1beta1.Hold
	cq := preemptibleCQFixture(&holdPolicy)
	c := newCompilerClient(t, cq)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 8, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 2),
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", time.Now()); err != nil {
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

func TestTriggerPreemption_EvictsWorkloadPastFloor(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	admittedAt := time.Now().Add(-5 * time.Hour) // admitted 5h ago
	wl := fakeWorkload("wl-old", "default", cq.Name, admittedAt)

	c := newCompilerClient(t, cq, wl)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	preemptRes := fakeReservation("res-p1", "tenant-a", "h200", "default",
		billingv1alpha1.ReservationClassPreemptible,
		billingv1alpha1.ReservationStateActive, 2)
	preemptRes.Spec.MinAllocationSeconds = int32Ptr(14400) // 4h floor
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		preemptRes,
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", time.Now()); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	evicted := getWorkload(t, c, "default", "wl-old")
	if evicted.Spec.Active == nil || *evicted.Spec.Active {
		t.Error("workload past floor should have Active=false, got true or nil")
	}
}

func TestTriggerPreemption_ProtectsWorkloadWithinFloor(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	admittedAt := time.Now().Add(-1 * time.Hour) // admitted 1h ago
	wl := fakeWorkload("wl-young", "default", cq.Name, admittedAt)

	c := newCompilerClient(t, cq, wl)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	preemptRes := fakeReservation("res-p1", "tenant-a", "h200", "default",
		billingv1alpha1.ReservationClassPreemptible,
		billingv1alpha1.ReservationStateActive, 2)
	preemptRes.Spec.MinAllocationSeconds = int32Ptr(14400) // 4h floor
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		preemptRes,
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", time.Now()); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	protected := getWorkload(t, c, "default", "wl-young")
	if protected.Spec.Active != nil && !*protected.Spec.Active {
		t.Error("workload within floor should NOT be evicted, got Active=false")
	}
}

func TestTriggerPreemption_SelectiveEviction(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	now := time.Now()
	wlOld := fakeWorkload("wl-old", "default", cq.Name, now.Add(-5*time.Hour))
	wlYoung := fakeWorkload("wl-young", "default", cq.Name, now.Add(-1*time.Hour))

	c := newCompilerClient(t, cq, wlOld, wlYoung)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	preemptRes := fakeReservation("res-p1", "tenant-a", "h200", "default",
		billingv1alpha1.ReservationClassPreemptible,
		billingv1alpha1.ReservationStateActive, 2)
	preemptRes.Spec.MinAllocationSeconds = int32Ptr(14400) // 4h floor
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		preemptRes,
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", now); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	evicted := getWorkload(t, c, "default", "wl-old")
	if evicted.Spec.Active == nil || *evicted.Spec.Active {
		t.Error("old workload (past floor) should be evicted")
	}

	protected := getWorkload(t, c, "default", "wl-young")
	if protected.Spec.Active != nil && !*protected.Spec.Active {
		t.Error("young workload (within floor) should NOT be evicted")
	}
}

func TestTriggerPreemption_DefaultMinAllocation(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	// Admitted 3h ago — within default 4h floor
	wl := fakeWorkload("wl-1", "default", cq.Name, time.Now().Add(-3*time.Hour))

	c := newCompilerClient(t, cq, wl)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	// Preemptible reservation WITHOUT explicit minAllocationSeconds (uses default)
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		fakeReservation("res-p1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassPreemptible,
			billingv1alpha1.ReservationStateActive, 2),
	}

	if _, err := triggerPreemption(context.Background(), c, nodes, reservations, "", time.Now()); err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	protected := getWorkload(t, c, "default", "wl-1")
	if protected.Spec.Active != nil && !*protected.Spec.Active {
		t.Error("workload within default 4h floor should NOT be evicted")
	}
}

// ---------------------------------------------------------------------------
// Eviction credits
// ---------------------------------------------------------------------------

func stringPtr(s string) *string { return &s }

func fakeWorkloadWithGPU(name, ns, cqName string, admittedAt time.Time, gpuCount int64) *kueuev1beta1.Workload {
	wl := fakeWorkload(name, ns, cqName, admittedAt)
	wl.Spec.PodSets = []kueuev1beta1.PodSet{{
		Count: 1,
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "gpu-worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							gpuResourceKey: resource.MustParse(fmt.Sprintf("%d", gpuCount)),
						},
					},
				}},
			},
		},
	}}
	return wl
}

func TestTriggerPreemption_EvictionCreditWithNonZeroRate(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	now := time.Now()
	admittedAt := now.Add(-5 * time.Hour) // admitted 5h ago, past 4h floor
	wl := fakeWorkloadWithGPU("wl-evict", "default", cq.Name, admittedAt, 2)

	c := newCompilerClient(t, cq, wl)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	preemptRes := fakeReservation("res-p1", "tenant-a", "h200", "default",
		billingv1alpha1.ReservationClassPreemptible,
		billingv1alpha1.ReservationStateActive, 2)
	preemptRes.Spec.MinAllocationSeconds = int32Ptr(14400)
	preemptRes.Spec.EvictionCreditPerGpuSecond = stringPtr("0.001000")
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		preemptRes,
	}

	credits, err := triggerPreemption(context.Background(), c, nodes, reservations, "", now)
	if err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	// Workload should be evicted
	evicted := getWorkload(t, c, "default", "wl-evict")
	if evicted.Spec.Active == nil || *evicted.Spec.Active {
		t.Error("workload should be evicted")
	}

	// Annotation should be set
	if evicted.Annotations[AnnotationEvictionReason] != "capacity-preemption" {
		t.Errorf("eviction reason annotation = %q, want capacity-preemption",
			evicted.Annotations[AnnotationEvictionReason])
	}

	// Should have exactly 1 credit
	if len(credits) != 1 {
		t.Fatalf("expected 1 credit, got %d", len(credits))
	}

	cr := credits[0]
	if cr.TenantID != "tenant-a" {
		t.Errorf("credit tenant = %q, want tenant-a", cr.TenantID)
	}
	// Amount = 0.001000 * 2 GPUs * ~18000 seconds (5h)
	// Should be a positive decimal string
	if cr.Amount == "" || cr.Amount == "0.000000" {
		t.Errorf("credit amount should be non-zero positive, got %q", cr.Amount)
	}
}

func TestTriggerPreemption_NoCreditWhenRateZero(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := preemptibleCQFixture(&nonePolicy)

	now := time.Now()
	admittedAt := now.Add(-5 * time.Hour)
	wl := fakeWorkloadWithGPU("wl-evict", "default", cq.Name, admittedAt, 1)

	c := newCompilerClient(t, cq, wl)

	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "default", 4, nil),
	}
	preemptRes := fakeReservation("res-p1", "tenant-a", "h200", "default",
		billingv1alpha1.ReservationClassPreemptible,
		billingv1alpha1.ReservationStateActive, 2)
	preemptRes.Spec.MinAllocationSeconds = int32Ptr(14400)
	// No EvictionCreditPerGpuSecond set — defaults to zero
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-g1", "tenant-a", "h200", "default",
			billingv1alpha1.ReservationClassGuaranteed,
			billingv1alpha1.ReservationStateActive, 4),
		preemptRes,
	}

	credits, err := triggerPreemption(context.Background(), c, nodes, reservations, "", now)
	if err != nil {
		t.Fatalf("triggerPreemption: %v", err)
	}

	// Workload should still be evicted
	evicted := getWorkload(t, c, "default", "wl-evict")
	if evicted.Spec.Active == nil || *evicted.Spec.Active {
		t.Error("workload should be evicted even without credit")
	}

	// No credits when rate is zero
	if len(credits) != 0 {
		t.Errorf("expected 0 credits (zero rate), got %d", len(credits))
	}
}

func TestEvictPastFloor_AnnotatesWorkload(t *testing.T) {
	admittedAt := time.Now().Add(-5 * time.Hour)
	wl := fakeWorkload("wl-1", "default", "test-cq", admittedAt)

	c := newCompilerClient(t, wl)

	now := time.Now()
	evictions, err := evictPastFloor(context.Background(), c, "test-cq", 14400, now)
	if err != nil {
		t.Fatalf("evictPastFloor: %v", err)
	}

	if len(evictions) != 1 {
		t.Fatalf("expected 1 eviction, got %d", len(evictions))
	}

	wlUpdated := getWorkload(t, c, "default", "wl-1")
	if wlUpdated.Annotations[AnnotationEvictionReason] != "capacity-preemption" {
		t.Errorf("annotation = %q, want capacity-preemption",
			wlUpdated.Annotations[AnnotationEvictionReason])
	}

	if evictions[0].RuntimeSecs <= 0 {
		t.Errorf("runtime should be positive, got %f", evictions[0].RuntimeSecs)
	}
}

func TestWorkloadGPUCount(t *testing.T) {
	tests := []struct {
		name    string
		wl      *kueuev1beta1.Workload
		wantGPU int64
	}{
		{
			name: "single container with 2 GPUs",
			wl: &kueuev1beta1.Workload{
				Spec: kueuev1beta1.WorkloadSpec{
					PodSets: []kueuev1beta1.PodSet{{
						Count: 1,
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											gpuResourceKey: resource.MustParse("2"),
										},
									},
								}},
							},
						},
					}},
				},
			},
			wantGPU: 2,
		},
		{
			name: "no GPU request defaults to 1",
			wl: &kueuev1beta1.Workload{
				Spec: kueuev1beta1.WorkloadSpec{
					PodSets: []kueuev1beta1.PodSet{{
						Count: 1,
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{
									Resources: corev1.ResourceRequirements{},
								}},
							},
						},
					}},
				},
			},
			wantGPU: 1,
		},
		{
			name: "multi-pod set scales",
			wl: &kueuev1beta1.Workload{
				Spec: kueuev1beta1.WorkloadSpec{
					PodSets: []kueuev1beta1.PodSet{{
						Count: 3,
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											gpuResourceKey: resource.MustParse("1"),
										},
									},
								}},
							},
						},
					}},
				},
			},
			wantGPU: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workloadGPUCount(tt.wl)
			if got != tt.wantGPU {
				t.Errorf("workloadGPUCount = %d, want %d", got, tt.wantGPU)
			}
		})
	}
}
