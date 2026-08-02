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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// stubBalanceQuerier is a test double that returns configurable balance values.
// It defaults to "0"/zero-time if no values are set.
type stubBalanceQuerier struct {
	mu       sync.Mutex
	balances map[string]string
	spends   map[string]string
	wallets  map[string]string
	errors   map[string]error // tenant -> error to return
}

func (s *stubBalanceQuerier) QueryTenantBalance(_ context.Context, tenantID string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errors != nil {
		if e, ok := s.errors[tenantID]; ok {
			return "", time.Time{}, e
		}
	}
	if s.balances != nil {
		if b, ok := s.balances[tenantID]; ok {
			return b, time.Now(), nil
		}
	}
	return "0", time.Time{}, nil
}

func (s *stubBalanceQuerier) QueryMTDSpend(_ context.Context, tenantID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spends != nil {
		if sp, ok := s.spends[tenantID]; ok {
			return sp, nil
		}
	}
	return "0", nil
}

func (s *stubBalanceQuerier) UpdateWallet(_ context.Context, tenantID, balance string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallets == nil {
		s.wallets = make(map[string]string)
	}
	s.wallets[tenantID] = balance
	return nil
}

func setBalance(tenantName, balance string) {
	testLedger.mu.Lock()
	if testLedger.balances == nil {
		testLedger.balances = make(map[string]string)
	}
	testLedger.balances[tenantName] = balance
	testLedger.mu.Unlock()
}

func setLedgerError(tenantName string, err error) {
	testLedger.mu.Lock()
	if testLedger.errors == nil {
		testLedger.errors = make(map[string]error)
	}
	if err == nil {
		delete(testLedger.errors, tenantName)
	} else {
		testLedger.errors[tenantName] = err
	}
	testLedger.mu.Unlock()
}

func clearLedgerError(tenantName string) {
	setLedgerError(tenantName, nil)
}

var _ = Describe("TenantAccountReconciler", func() {
	Context("ShowbackOnly tenant with non-zero balance", func() {
		It("should mirror the non-zero balance from ledger into status", func() {
			ns := createTestNamespace(ctx, "showback-bal")
			tenantName := "ten-showback-bal"

			testLedger.mu.Lock()
			if testLedger.balances == nil {
				testLedger.balances = make(map[string]string)
			}
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.balances[tenantName] = "12345.670000"
			testLedger.spends[tenantName] = "654.330000"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Balance Test Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "ShowbackOnly",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      tenantName,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
				g.Expect(fetched.Status.Balance).To(Equal("12345.670000"),
					"Balance should match the ledger value")
				g.Expect(fetched.Status.MtdSpend).To(Equal("654.330000"),
					"MTD spend should match the ledger value")
				g.Expect(fetched.Status.BalanceObservedAt).NotTo(BeNil(),
					"BalanceObservedAt should be set")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("ShowbackOnly tenant", func() {
		It("should set state to Active and mirror balance from ledger", func() {
			ns := createTestNamespace(ctx, "showback")
			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ten-showback",
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Showback Test Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "ShowbackOnly",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      acct.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"),
					"ShowbackOnly tenants should always be Active")
				g.Expect(fetched.Status.Balance).To(Equal("0"),
					"Balance should be mirrored from ledger (stub returns 0)")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionBalanceHealthy)
				g.Expect(cond).NotTo(BeNil(),
					"BalanceHealthy condition should be set")
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Prepaid tenant with no prior state", func() {
		It("should set initial state to Pending", func() {
			ns := createTestNamespace(ctx, "prepaid")
			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ten-prepaid",
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:          "Prepaid Test Co",
					PlanRef:              corev1.LocalObjectReference{Name: "priority"},
					BillingMode:          "Prepaid",
					PSPCustomerSecretRef: &corev1.LocalObjectReference{Name: "stripe-secret"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000",
						ReloadAmount:     "25000",
						MonthlyCapAmount: "60000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      acct.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Pending"),
					"Prepaid tenant with no prior state should be Pending")
				g.Expect(fetched.Status.Balance).NotTo(BeEmpty(),
					"Balance should be populated")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("computeState unit tests", func() {
		var reconciler *TenantAccountReconciler

		BeforeEach(func() {
			reconciler = &TenantAccountReconciler{}
		})

		DescribeTable("state computation",
			func(acct *billingv1alpha1.TenantAccount, balance, expectedState string) {
				Expect(reconciler.computeState(acct, balance)).To(Equal(expectedState))
			},
			Entry("ShowbackOnly always Active regardless of balance",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{BillingMode: "ShowbackOnly"},
				}, "-999.00", "Active"),

			Entry("ShowbackOnly overrides existing Frozen state",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "ShowbackOnly"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Frozen"},
				}, "0", "Active"),

			Entry("Prepaid positive balance above warning threshold → Active",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "500.000000", "Active"),

			Entry("Prepaid balance below warningThreshold → Warning",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "75.000000", "Warning"),

			Entry("Prepaid balance at zero with graceAmount > 0 → Grace",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "0.000000", "Grace"),

			Entry("Prepaid balance at zero with graceAmount = 0 → Frozen",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount: "0.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "0.000000", "Frozen"),

			Entry("Prepaid balance negative within grace → Grace",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount: "50.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "-30.000000", "Grace"),

			Entry("Prepaid balance below -graceAmount → Frozen",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Grace"},
				}, "-60.000000", "Frozen"),

			Entry("Prepaid Frozen with positive balance above threshold → Active (thaw)",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Frozen"},
				}, "200.000000", "Active"),

			Entry("Prepaid Frozen with positive balance below threshold → Warning (thaw)",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "50.000000",
							WarningThreshold: "100.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Frozen"},
				}, "75.000000", "Warning"),

			Entry("Prepaid no prior state with zero balance → Pending",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount: "0.000000",
						},
					},
				}, "0.000000", "Pending"),

			Entry("Prepaid no prior state with positive balance → Active",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
					},
				}, "500.000000", "Active"),

			Entry("Prepaid no prior state with positive balance below warning → Warning",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "0.000000",
							WarningThreshold: "1000.000000",
						},
					},
				}, "500.000000", "Warning"),

			Entry("Prepaid Pending state with zero balance stays Pending",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Pending"},
				}, "0.000000", "Pending"),

			Entry("Prepaid nil GracePolicy balance at zero → Pending (no prior state)",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
					},
				}, "0.000000", "Pending"),

			Entry("Prepaid nil GracePolicy balance at zero with existing state → Frozen",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "0.000000", "Frozen"),

			Entry("Invoiced with no prior state → Pending",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Invoiced",
					},
				}, "0.000000", "Pending"),

			Entry("Invoiced positive balance above threshold → Active",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Invoiced",
						GracePolicy: &billingv1alpha1.GracePolicy{
							GraceAmount:      "100.000000",
							WarningThreshold: "50.000000",
						},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active"},
				}, "200.000000", "Active"),

			Entry("Unparseable balance with no prior state → Pending",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
					},
				}, "not-a-number", "Pending"),

			Entry("Unparseable balance preserves existing state",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Grace"},
				}, "not-a-number", "Grace"),
		)
	})

	Context("Full lifecycle: Active → Warning → Grace → Frozen → Active", func() {
		It("should transition through all enforcement states", func() {
			ns := createTestNamespace(ctx, "lifecycle")
			tenantName := "ten-lifecycle"

			setBalance(tenantName, "500.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Lifecycle Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount:      "20.000000",
						WarningThreshold: "100.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Should activate (balance $500 > warningThreshold $100)
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drop balance below warningThreshold → Warning
			setBalance(tenantName, "50.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Warning"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drop balance to zero → Grace (graceAmount is $20)
			setBalance(tenantName, "0.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Grace"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drop below -graceAmount → Frozen
			setBalance(tenantName, "-25.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Top up → Active
			setBalance(tenantName, "200.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("graceAmount=0 freezes immediately at zero", func() {
		It("should skip Grace and go straight to Frozen", func() {
			ns := createTestNamespace(ctx, "nograce")
			tenantName := "ten-nograce"

			setBalance(tenantName, "100.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "No Grace Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount: "0.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Should activate
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drop to zero → Frozen (no Grace period)
			setBalance(tenantName, "0.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("ShowbackOnly immunity", func() {
		It("should stay Active regardless of negative balance", func() {
			ns := createTestNamespace(ctx, "showback-immune")
			tenantName := "ten-showback-immune"

			setBalance(tenantName, "-500.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "500.000000"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Immune Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "ShowbackOnly",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"),
					"ShowbackOnly must stay Active even with negative balance")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Ledger unreachable degradation", func() {
		It("should hold state within maxStaleness then set BalanceStale", func() {
			ns := createTestNamespace(ctx, "ledger-stale")
			tenantName := "ten-ledger-stale"

			setBalance(tenantName, "500.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Stale Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount:      "10.000000",
						WarningThreshold: "100.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for initial activation
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
				g.Expect(fetched.Status.BalanceObservedAt).NotTo(BeNil())
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Inject ledger error
			setLedgerError(tenantName, fmt.Errorf("connection refused"))

			// Manually set BalanceObservedAt to >10 minutes ago to simulate staleness
			// (we can't wait 10 real minutes in a test)
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			staleTime := metav1.NewTime(time.Now().Add(-11 * time.Minute))
			fetched.Status.BalanceObservedAt = &staleTime
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			// Should set BalanceStale condition, keep state as Active (never goes unlimited)
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				// Critical: state must NOT change to something that removes limits
				g.Expect(f.Status.State).To(Equal("Active"),
					"State must be held during ledger unavailability — never unlimited")
				staleCond := apimeta.FindStatusCondition(f.Status.Conditions, billingv1alpha1.ConditionBalanceStale)
				g.Expect(staleCond).NotTo(BeNil(), "BalanceStale condition should be set")
				g.Expect(staleCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(staleCond.Reason).To(Equal("LedgerUnavailable"))
				healthyCond := apimeta.FindStatusCondition(f.Status.Conditions, billingv1alpha1.ConditionBalanceHealthy)
				g.Expect(healthyCond).NotTo(BeNil())
				g.Expect(healthyCond.Status).To(Equal(metav1.ConditionFalse))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Clear ledger error: BalanceStale should be cleared on recovery
			clearLedgerError(tenantName)
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				staleCond := apimeta.FindStatusCondition(f.Status.Conditions, billingv1alpha1.ConditionBalanceStale)
				g.Expect(staleCond).To(BeNil(), "BalanceStale condition should be cleared after recovery")
				healthyCond := apimeta.FindStatusCondition(f.Status.Conditions, billingv1alpha1.ConditionBalanceHealthy)
				g.Expect(healthyCond).NotTo(BeNil())
				g.Expect(healthyCond.Status).To(Equal(metav1.ConditionTrue))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})

		It("should never set state to unlimited or empty during degradation", func() {
			ns := createTestNamespace(ctx, "never-unlimited")
			tenantName := "ten-never-unlimited"

			// Activate first with positive balance, then drain to Frozen
			setBalance(tenantName, "100.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Never Unlimited Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount: "0.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for Active state
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drain balance to trigger Frozen
			setBalance(tenantName, "-100.000000")

			// Wait for Frozen state
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Inject ledger error + make observation stale
			setLedgerError(tenantName, fmt.Errorf("ledger down"))
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			staleTime := metav1.NewTime(time.Now().Add(-15 * time.Minute))
			fetched.Status.BalanceObservedAt = &staleTime
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			// Verify: state must stay Frozen (not Active, not empty, not unlimited)
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.State).To(Equal("Frozen"),
					"Frozen tenant must stay Frozen during ledger outage — never falls to unlimited")
				staleCond := apimeta.FindStatusCondition(f.Status.Conditions, billingv1alpha1.ConditionBalanceStale)
				g.Expect(staleCond).NotTo(BeNil())
				g.Expect(staleCond.Status).To(Equal(metav1.ConditionTrue))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Clean up: remove ledger error for other tests
			clearLedgerError(tenantName)
		})
	})
})
