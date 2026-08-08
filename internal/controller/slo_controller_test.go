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
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/rater"
)

type mockAdjustmentPoster struct {
	mu      sync.Mutex
	credits []postedCredit
}

type postedCredit struct {
	Tenant      string
	Amount      string
	WorkloadUID string
	Kind        string
}

func (m *mockAdjustmentPoster) PostEvictionCredit(_ context.Context, tenant, amount, workloadUID string, _ float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credits = append(m.credits, postedCredit{Tenant: tenant, Amount: amount, WorkloadUID: workloadUID, Kind: "eviction_credit"})
	return nil
}

func (m *mockAdjustmentPoster) PostSLACredit(_ context.Context, tenant, amount, workloadUID string, _ float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credits = append(m.credits, postedCredit{Tenant: tenant, Amount: amount, WorkloadUID: workloadUID, Kind: "sla_credit"})
	return nil
}

func (m *mockAdjustmentPoster) creditCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.credits)
}

func guaranteedCQFixture() *kueuev1beta1.ClusterQueue {
	return &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a-guaranteed",
			Labels: map[string]string{
				labelTenant:           "tenant-a",
				labelReservationClass: billingv1alpha1.ReservationClassGuaranteed,
				labelManagedBy:        managedByReservation,
			},
			Annotations: map[string]string{annotationSKU: "h200", annotationGeo: "default"},
		},
	}
}

func guaranteedReservation(tenant string, sloSeconds *int32) *billingv1alpha1.Reservation {
	return &billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-g1", Namespace: "default"},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:           tenant,
			SKU:                 "h200",
			Count:               2,
			Geo:                 "default",
			Class:               billingv1alpha1.ReservationClassGuaranteed,
			SloAdmissionSeconds: sloSeconds,
			MonthlyPrice:        "3000.000000",
		},
		Status: billingv1alpha1.ReservationStatus{
			State: billingv1alpha1.ReservationStateActive,
		},
	}
}

func guaranteedPriceBook() *billingv1alpha1.PriceBook {
	return &billingv1alpha1.PriceBook{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-book", Namespace: "default"},
		Spec: billingv1alpha1.PriceBookSpec{
			Currency:      "USD",
			EffectiveFrom: metav1.Now(),
			NoticeDays:    0,
			SKUs: []billingv1alpha1.SKU{
				{
					SKUName: "h200-guaranteed",
					Unit:    "per-gpu-second",
					Rates:   map[string]string{"gpu_seconds": "0.002917"},
				},
			},
		},
		Status: billingv1alpha1.PriceBookStatus{State: "Active"},
	}
}

func workloadWithLatency(name, ns, cqName string, latency time.Duration) *kueuev1beta1.Workload { //nolint:unparam
	created := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	admitted := created.Add(latency)
	active := true
	return &kueuev1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			CreationTimestamp: metav1.NewTime(created),
			UID:               "test-uid-001",
		},
		Spec: kueuev1beta1.WorkloadSpec{
			Active: &active,
		},
		Status: kueuev1beta1.WorkloadStatus{
			Admission: &kueuev1beta1.Admission{
				ClusterQueue: kueuev1beta1.ClusterQueueReference(cqName),
			},
			Conditions: []metav1.Condition{{
				Type:               "Admitted",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(admitted),
			}},
		},
	}
}

func TestSLOController_BreachPostsCredit(t *testing.T) {
	cq := guaranteedCQFixture()
	slo := int32(60)
	res := guaranteedReservation("tenant-a", &slo)
	pb := guaranteedPriceBook()
	wl := workloadWithLatency("wl-slow", "default", cq.Name, 5*time.Minute)

	c := newCompilerClient(t, cq, res, pb, wl)
	poster := &mockAdjustmentPoster{}
	resolver := rater.NewK8sResolver(c)

	sloCtrl := &SLOController{
		Client:           c,
		Log:              ctrl.Log.WithName("test-slo"),
		AdjustmentPoster: poster,
		Resolver:         resolver,
	}

	_, err := sloCtrl.Reconcile(context.Background(), reconcileRequest("default", "wl-slow"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if poster.creditCount() != 1 {
		t.Fatalf("expected 1 credit, got %d", poster.creditCount())
	}

	cr := poster.credits[0]
	if cr.Tenant != "tenant-a" {
		t.Errorf("tenant = %q, want tenant-a", cr.Tenant)
	}
	if cr.Kind != "sla_credit" {
		t.Errorf("kind = %q, want sla_credit", cr.Kind)
	}

	// Verify annotation was set
	updated := getWorkload(t, c, "default", "wl-slow")
	if updated.Annotations[annotationSLOChecked] != annotationValueTrue {
		t.Error("slo-checked annotation should be true")
	}
}

func TestSLOController_NoBreach(t *testing.T) {
	cq := guaranteedCQFixture()
	slo := int32(600)
	res := guaranteedReservation("tenant-a", &slo)
	wl := workloadWithLatency("wl-fast", "default", cq.Name, 30*time.Second)

	c := newCompilerClient(t, cq, res, wl)
	poster := &mockAdjustmentPoster{}

	sloCtrl := &SLOController{
		Client:           c,
		Log:              ctrl.Log.WithName("test-slo"),
		AdjustmentPoster: poster,
	}

	_, err := sloCtrl.Reconcile(context.Background(), reconcileRequest("default", "wl-fast"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if poster.creditCount() != 0 {
		t.Errorf("expected 0 credits (no breach), got %d", poster.creditCount())
	}

	updated := getWorkload(t, c, "default", "wl-fast")
	if updated.Annotations[annotationSLOChecked] != annotationValueTrue {
		t.Error("slo-checked annotation should be true even without breach")
	}
}

func TestSLOController_SkipsPreemptibleCQ(t *testing.T) {
	cq := preemptibleCQFixture(nil)
	wl := workloadWithLatency("wl-preempt", "default", cq.Name, 10*time.Minute)

	c := newCompilerClient(t, cq, wl)
	poster := &mockAdjustmentPoster{}

	sloCtrl := &SLOController{
		Client:           c,
		Log:              ctrl.Log.WithName("test-slo"),
		AdjustmentPoster: poster,
	}

	_, err := sloCtrl.Reconcile(context.Background(), reconcileRequest("default", "wl-preempt"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if poster.creditCount() != 0 {
		t.Errorf("expected 0 credits (Preemptible CQ), got %d", poster.creditCount())
	}

	updated := getWorkload(t, c, "default", "wl-preempt")
	if updated.Annotations[annotationSLOChecked] == annotationValueTrue {
		t.Error("Preemptible workloads should NOT get slo-checked annotation")
	}
}

func TestSLOController_SkipsAlreadyChecked(t *testing.T) {
	cq := guaranteedCQFixture()
	slo := int32(60)
	res := guaranteedReservation("tenant-a", &slo)
	wl := workloadWithLatency("wl-checked", "default", cq.Name, 5*time.Minute)
	wl.Annotations = map[string]string{annotationSLOChecked: annotationValueTrue}

	c := newCompilerClient(t, cq, res, wl)
	poster := &mockAdjustmentPoster{}

	sloCtrl := &SLOController{
		Client:           c,
		Log:              ctrl.Log.WithName("test-slo"),
		AdjustmentPoster: poster,
	}

	_, err := sloCtrl.Reconcile(context.Background(), reconcileRequest("default", "wl-checked"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if poster.creditCount() != 0 {
		t.Errorf("expected 0 credits (already checked), got %d", poster.creditCount())
	}
}

func TestSLOController_SkipsNotAdmitted(t *testing.T) {
	active := true
	wl := &kueuev1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{Name: "wl-pending", Namespace: "default"},
		Spec:       kueuev1beta1.WorkloadSpec{Active: &active},
	}

	c := newCompilerClient(t, wl)
	poster := &mockAdjustmentPoster{}

	sloCtrl := &SLOController{
		Client:           c,
		Log:              ctrl.Log.WithName("test-slo"),
		AdjustmentPoster: poster,
	}

	_, err := sloCtrl.Reconcile(context.Background(), reconcileRequest("default", "wl-pending"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if poster.creditCount() != 0 {
		t.Errorf("expected 0 credits (not admitted), got %d", poster.creditCount())
	}
}

func reconcileRequest(ns, name string) ctrl.Request { //nolint:unparam
	return ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: name}}
}
