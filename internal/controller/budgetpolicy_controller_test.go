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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var _ = Describe("BudgetPolicy controller", func() {
	var ns string

	BeforeEach(func() {
		ns = createTestNamespace(ctx, "budget")
		testBackend.reset()
	})

	createTenant := func(name string) {
		acct := &billingv1alpha1.TenantAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: billingv1alpha1.TenantAccountSpec{
				DisplayName: name,
				PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
				BillingMode: billingv1alpha1.BillingModeShowbackOnly,
			},
		}
		Expect(k8sClient.Create(ctx, acct)).To(Succeed())
	}

	createBudgetPolicy := func(name, tenantName string, caps []billingv1alpha1.BudgetCap) {
		bp := &billingv1alpha1.BudgetPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: billingv1alpha1.BudgetPolicySpec{
				TenantRef: corev1.LocalObjectReference{Name: tenantName},
				Caps:      caps,
			},
		}
		Expect(k8sClient.Create(ctx, bp)).To(Succeed())
	}

	fetchBudgetPolicy := func(name string) billingv1alpha1.BudgetPolicy {
		var bp billingv1alpha1.BudgetPolicy
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{
			Name: name, Namespace: ns,
		}, &bp)).To(Succeed())
		return bp
	}

	Context("basic threshold evaluation", func() {
		It("sets WithinBudget when spend is below all thresholds", func() {
			createTenant("ten-low")
			setPeriodSpend("ten-low", "500.000000")
			setBalance("ten-low", "10000")

			createBudgetPolicy("bp-low", "ten-low", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
					},
				},
			})

			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-low")
				cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionWithinBudget)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			}, "10s", "250ms").Should(Succeed())
		})

		It("fires Notify threshold at the correct percentage", func() {
			createTenant("ten-notify")
			setPeriodSpend("ten-notify", "8500.000000")
			setBalance("ten-notify", "10000")

			createBudgetPolicy("bp-notify", "ten-notify", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
					},
				},
			})

			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-notify")
				cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionThresholdFired)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(bp.Status.FiredThresholds).To(HaveLen(1))
				g.Expect(bp.Status.FiredThresholds[0].Pct).To(Equal(int32(80)))
				g.Expect(bp.Status.FiredThresholds[0].CapIndex).To(Equal(int32(0)))
			}, "10s", "250ms").Should(Succeed())
		})

		It("fires Freeze threshold and calls enforcement", func() {
			createTenant("ten-freeze")
			setPeriodSpend("ten-freeze", "10500.000000")
			setBalance("ten-freeze", "10000")

			createBudgetPolicy("bp-freeze", "ten-freeze", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 100, Action: billingv1alpha1.ThresholdActionFreeze},
					},
				},
			})

			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-freeze")
				cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionThresholdFired)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(bp.Status.FiredThresholds).To(HaveLen(1))
			}, "10s", "250ms").Should(Succeed())

			Eventually(func(g Gomega) {
				calls := testBackend.callsForTenant("ten-freeze")
				freezeCalls := 0
				for _, c := range calls {
					if c.method == "Freeze" {
						freezeCalls++
					}
				}
				g.Expect(freezeCalls).To(BeNumerically(">=", 1))
			}, "10s", "250ms").Should(Succeed())
		})
	})

	Context("idempotency", func() {
		It("does not duplicate notifications on re-reconcile", func() {
			createTenant("ten-idem")
			setPeriodSpend("ten-idem", "9000.000000")
			setBalance("ten-idem", "10000")

			createBudgetPolicy("bp-idem", "ten-idem", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
					},
				},
			})

			// Wait for the threshold to fire.
			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-idem")
				g.Expect(bp.Status.FiredThresholds).To(HaveLen(1))
			}, "10s", "250ms").Should(Succeed())

			// Allow several re-reconciles and verify firedThresholds stays at length 1.
			Consistently(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-idem")
				g.Expect(bp.Status.FiredThresholds).To(HaveLen(1))
			}, "2s", "200ms").Should(Succeed())
		})
	})

	Context("missing TenantAccount", func() {
		It("sets Evaluating=false when tenant is not found", func() {
			bp := &billingv1alpha1.BudgetPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bp-orphan",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BudgetPolicySpec{
					TenantRef: corev1.LocalObjectReference{Name: "nonexistent"},
					Caps: []billingv1alpha1.BudgetCap{
						{
							Amount: "1000",
							Period: billingv1alpha1.PeriodMonthly,
							Thresholds: []billingv1alpha1.BudgetThreshold{
								{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, bp)).To(Succeed())

			Eventually(func(g Gomega) {
				fetched := fetchBudgetPolicy("bp-orphan")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionEvaluating)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("TenantNotFound"))
			}, "10s", "250ms").Should(Succeed())
		})
	})

	Context("multiple thresholds", func() {
		It("fires multiple thresholds when spend crosses them", func() {
			createTenant("ten-multi")
			setPeriodSpend("ten-multi", "10500.000000")
			setBalance("ten-multi", "10000")

			createBudgetPolicy("bp-multi", "ten-multi", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
						{Pct: 100, Action: billingv1alpha1.ThresholdActionFreeze},
					},
				},
			})

			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-multi")
				g.Expect(bp.Status.FiredThresholds).To(HaveLen(2))
			}, "10s", "250ms").Should(Succeed())
		})
	})

	Context("period rollover thaw", func() {
		It("thaws enforcement when a frozen cap's period rolls over", func() {
			createTenant("ten-thaw")
			setPeriodSpend("ten-thaw", "11000.000000")
			setBalance("ten-thaw", "10000")

			createBudgetPolicy("bp-thaw", "ten-thaw", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodDaily,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 100, Action: billingv1alpha1.ThresholdActionFreeze},
					},
				},
			})

			// Wait for freeze to fire.
			Eventually(func(g Gomega) {
				calls := testBackend.callsForTenant("ten-thaw")
				freezeCalls := 0
				for _, c := range calls {
					if c.method == "Freeze" {
						freezeCalls++
					}
				}
				g.Expect(freezeCalls).To(BeNumerically(">=", 1))
			}, "10s", "250ms").Should(Succeed())

			// Reset backend and lower spend so freeze doesn't immediately re-fire.
			testBackend.reset()
			setPeriodSpend("ten-thaw", "0.000000")

			// Simulate period rollover by backdating CapPeriodEnds.
			Eventually(func(g Gomega) {
				var bp billingv1alpha1.BudgetPolicy
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: "bp-thaw", Namespace: ns,
				}, &bp)).To(Succeed())

				yesterday := metav1.NewTime(time.Now().UTC().AddDate(0, 0, -1))
				bp.Status.CapPeriodEnds = []billingv1alpha1.CapPeriodEnd{{
					CapIndex:  0,
					PeriodEnd: yesterday,
				}}
				g.Expect(k8sClient.Status().Update(ctx, &bp)).To(Succeed())
			}, "5s", "250ms").Should(Succeed())

			// Verify Thaw was called.
			Eventually(func(g Gomega) {
				calls := testBackend.callsForTenant("ten-thaw")
				thawCalls := 0
				for _, c := range calls {
					if c.method == "Thaw" {
						thawCalls++
					}
				}
				g.Expect(thawCalls).To(BeNumerically(">=", 1))
			}, "10s", "250ms").Should(Succeed())

			// FiredThresholds should be cleared after rollover.
			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-thaw")
				g.Expect(bp.Status.FiredThresholds).To(BeEmpty())
			}, "5s", "250ms").Should(Succeed())
		})
	})

	Context("spend tracking", func() {
		It("records currentSpend in status", func() {
			createTenant("ten-spend")
			setPeriodSpend("ten-spend", "4200.500000")
			setBalance("ten-spend", "10000")

			createBudgetPolicy("bp-spend", "ten-spend", []billingv1alpha1.BudgetCap{
				{
					Amount: "10000",
					Period: billingv1alpha1.PeriodMonthly,
					Thresholds: []billingv1alpha1.BudgetThreshold{
						{Pct: 80, Action: billingv1alpha1.ThresholdActionNotify},
					},
				},
			})

			Eventually(func(g Gomega) {
				bp := fetchBudgetPolicy("bp-spend")
				g.Expect(bp.Status.CurrentSpend).To(Equal("4200.500000"))
				g.Expect(bp.Status.CapPeriodEnds).To(HaveLen(1))
			}, "10s", "250ms").Should(Succeed())
		})
	})
})
