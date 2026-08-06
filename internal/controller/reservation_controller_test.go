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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var _ = Describe("Reservation controller", func() {
	var ns string

	BeforeEach(func() {
		ns = createTestNamespace(ctx, "reservation")
	})

	createReservation := func(name, tenant, sku, geo, class, price string, count int32, start, end time.Time) {
		r := &billingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: billingv1alpha1.ReservationSpec{
				TenantRef: tenant,
				SKU:       sku,
				Count:     count,
				Geo:       geo,
				Window: billingv1alpha1.ReservationWindow{
					Start: metav1.NewTime(start),
					End:   metav1.NewTime(end),
				},
				Class:        class,
				MonthlyPrice: price,
			},
		}
		Expect(k8sClient.Create(ctx, r)).To(Succeed())
	}

	fetchReservation := func(name string) billingv1alpha1.Reservation {
		var r billingv1alpha1.Reservation
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{
			Name: name, Namespace: ns,
		}, &r)).To(Succeed())
		return r
	}

	ensureGPUNode := func(name, sku, geo string, gpuCount int) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					labelGPUProduct: sku,
					defaultGeoLabel: geo,
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceName(gpuResource): resource.MustParse(fmt.Sprintf("%d", gpuCount)),
				},
			},
		}
		err := k8sClient.Create(ctx, node)
		if err != nil {
			// Node already exists (cluster-scoped, shared across tests).
			return
		}
	}

	Context("state machine transitions", func() {
		It("transitions Requested → Confirmed when capacity is available", func() {
			ensureGPUNode("gpu-node-confirm-1", "h200", "us-east", 8)

			future := time.Now().UTC().Add(1 * time.Hour)
			farFuture := future.Add(24 * time.Hour)
			createReservation("res-confirm", "tenant-a", "h200", "us-east", "Guaranteed", "5000.000000", 4, future, farFuture)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-confirm")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateConfirmed))
				cond := apimeta.FindStatusCondition(r.Status.Conditions, billingv1alpha1.ConditionCapacityConfirmed)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal("CapacityAvailable"))
			}, "10s", "250ms").Should(Succeed())
		})

		It("stays Requested when capacity is insufficient", func() {
			future := time.Now().UTC().Add(1 * time.Hour)
			farFuture := future.Add(24 * time.Hour)
			createReservation("res-no-cap", "tenant-nocap", "v100-rare", "moon-base", "Guaranteed", "5000.000000", 100, future, farFuture)

			// Wait for the controller to set the CapacityInsufficient condition.
			Eventually(func(g Gomega) {
				r := fetchReservation("res-no-cap")
				cond := apimeta.FindStatusCondition(r.Status.Conditions, billingv1alpha1.ConditionCapacityConfirmed)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal("CapacityInsufficient"))
			}, "10s", "250ms").Should(Succeed())

			// Verify it stays Requested and does not transition.
			Consistently(func(g Gomega) {
				r := fetchReservation("res-no-cap")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateRequested))
			}, "2s", "500ms").Should(Succeed())
		})

		It("populates CapacityCheckResult on confirmation", func() {
			ensureGPUNode("gpu-node-capcheck", "h200", "us-east", 8)

			future := time.Now().UTC().Add(1 * time.Hour)
			farFuture := future.Add(24 * time.Hour)
			createReservation("res-capcheck", "tenant-cap", "h200", "us-east", "Guaranteed", "5000.000000", 2, future, farFuture)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-capcheck")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateConfirmed))
				g.Expect(r.Status.CapacityCheck).NotTo(BeNil())
				g.Expect(r.Status.CapacityCheck.Available).To(BeTrue())
				g.Expect(r.Status.CapacityCheck.Pool).To(Equal("h200/us-east"))
			}, "10s", "250ms").Should(Succeed())
		})

		It("transitions Confirmed → Active when window.start is reached", func() {
			ensureGPUNode("gpu-node-active", "h200", "us-west", 8)

			past := time.Now().UTC().Add(-1 * time.Hour)
			farFuture := time.Now().UTC().Add(24 * time.Hour)
			createReservation("res-active", "tenant-b", "h200", "us-west", "Preemptible", "2500.000000", 2, past, farFuture)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-active")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateActive))
			}, "10s", "250ms").Should(Succeed())
		})

		It("transitions Active → Expired when window.end is reached", func() {
			ensureGPUNode("gpu-node-expired", "h200", "eu-west", 16)

			past := time.Now().UTC().Add(-2 * time.Hour)
			recentPast := time.Now().UTC().Add(-1 * time.Hour)
			createReservation("res-expired", "tenant-c", "h200", "eu-west", "Guaranteed", "8000.000000", 8, past, recentPast)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-expired")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateExpired))
			}, "10s", "250ms").Should(Succeed())
		})

		It("sets ObservedGeneration on status", func() {
			ensureGPUNode("gpu-node-gen", "h200", "us-east", 8)

			future := time.Now().UTC().Add(1 * time.Hour)
			farFuture := future.Add(24 * time.Hour)
			createReservation("res-gen", "tenant-d", "h200", "us-east", "Guaranteed", "5000.000000", 4, future, farFuture)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-gen")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateConfirmed))
				g.Expect(r.Status.ObservedGeneration).To(Equal(r.Generation))
			}, "10s", "250ms").Should(Succeed())
		})
	})

	Context("Cancelled state", func() {
		It("treats Cancelled as a terminal state (no further transitions)", func() {
			ensureGPUNode("gpu-node-cancel", "h200", "us-east", 8)

			// Create a reservation with window in the past so the controller
			// would normally drive it through to Active then Expired.
			past := time.Now().UTC().Add(-2 * time.Hour)
			recentPast := time.Now().UTC().Add(-1 * time.Hour)
			createReservation("res-cancel", "tenant-cancel", "h200", "us-east", "Guaranteed", "5000.000000", 2, past, recentPast)

			// Wait for the controller to process it (it will reach Expired).
			Eventually(func(g Gomega) {
				r := fetchReservation("res-cancel")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateExpired))
			}, "10s", "250ms").Should(Succeed())

			// Simulate an out-of-band cancel by patching status directly.
			r := fetchReservation("res-cancel")
			cancelledAt := metav1.Now()
			r.Status.State = billingv1alpha1.ReservationStateCancelled
			r.Status.CancelledAt = &cancelledAt
			Expect(k8sClient.Status().Update(ctx, &r)).To(Succeed())

			// Verify the controller does not override Cancelled.
			Consistently(func(g Gomega) {
				r := fetchReservation("res-cancel")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateCancelled))
				g.Expect(r.Status.CancelledAt).NotTo(BeNil())
			}, "3s", "500ms").Should(Succeed())
		})
	})

	Context("Preemptible class", func() {
		It("follows the same state machine for Preemptible reservations", func() {
			ensureGPUNode("gpu-node-preempt", "h200-mig-3g.40gb", "us-east", 4)

			future := time.Now().UTC().Add(1 * time.Hour)
			farFuture := future.Add(7 * 24 * time.Hour)
			createReservation("res-preempt", "tenant-e", "h200-mig-3g.40gb", "us-east", "Preemptible", "1200.000000", 2, future, farFuture)

			Eventually(func(g Gomega) {
				r := fetchReservation("res-preempt")
				g.Expect(r.Status.State).To(Equal(billingv1alpha1.ReservationStateConfirmed))
			}, "10s", "250ms").Should(Succeed())
		})
	})
})
