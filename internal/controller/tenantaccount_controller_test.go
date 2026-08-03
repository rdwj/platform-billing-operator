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
	mu               sync.Mutex
	balances         map[string]string
	spends           map[string]string
	periodSpends     map[string]string // "tenant:since" -> period spend
	wallets          map[string]string
	errors           map[string]error  // tenant -> error to return
	adjustmentTotals map[string]string // "tenant:kind" -> total amount
	adjustmentCounts map[string]int64  // "tenant:kind" -> count
	adjustmentErrors map[string]error  // "tenant:kind" -> error (for fail-closed tests)
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

func (s *stubBalanceQuerier) QueryPeriodSpend(_ context.Context, tenantID string, _ time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.periodSpends != nil {
		if sp, ok := s.periodSpends[tenantID]; ok {
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

func (s *stubBalanceQuerier) QueryAdjustmentTotal(_ context.Context, tenantID, kind string, _ time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + kind
	if s.adjustmentErrors != nil {
		if e, ok := s.adjustmentErrors[key]; ok {
			return "", e
		}
	}
	if s.adjustmentTotals != nil {
		if t, ok := s.adjustmentTotals[key]; ok {
			return t, nil
		}
	}
	return "0", nil
}

func (s *stubBalanceQuerier) QueryAdjustmentCount(_ context.Context, tenantID, kind string, _ time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + kind
	if s.adjustmentErrors != nil {
		if e, ok := s.adjustmentErrors[key]; ok {
			return 0, e
		}
	}
	if s.adjustmentCounts != nil {
		if c, ok := s.adjustmentCounts[key]; ok {
			return c, nil
		}
	}
	return 0, nil
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

func setPeriodSpend(tenantName, spend string) {
	testLedger.mu.Lock()
	if testLedger.periodSpends == nil {
		testLedger.periodSpends = make(map[string]string)
	}
	testLedger.periodSpends[tenantName] = spend
	testLedger.mu.Unlock()
}

func setPaymentCreditTotal(tenantName, total string) {
	testLedger.mu.Lock()
	if testLedger.adjustmentTotals == nil {
		testLedger.adjustmentTotals = make(map[string]string)
	}
	testLedger.adjustmentTotals[tenantName+":payment_credit"] = total
	testLedger.mu.Unlock()
}

func setPaymentCreditCount(tenantName string, count int64) {
	testLedger.mu.Lock()
	if testLedger.adjustmentCounts == nil {
		testLedger.adjustmentCounts = make(map[string]int64)
	}
	testLedger.adjustmentCounts[tenantName+":payment_credit"] = count
	testLedger.mu.Unlock()
}

func setPaymentCreditError(tenantName string, err error) {
	testLedger.mu.Lock()
	if testLedger.adjustmentErrors == nil {
		testLedger.adjustmentErrors = make(map[string]error)
	}
	if err == nil {
		delete(testLedger.adjustmentErrors, tenantName+":payment_credit")
	} else {
		testLedger.adjustmentErrors[tenantName+":payment_credit"] = err
	}
	testLedger.mu.Unlock()
}

// enforcementCall records a single call to the recording enforcement backend.
type enforcementCall struct {
	method    string // "Apply", "Freeze", "Thaw"
	tenant    string // acct.Name
	allowance string // for Apply only
}

// recordingBackend is an EnforcementBackend that records calls for test assertions.
type recordingBackend struct {
	mu    sync.Mutex
	calls []enforcementCall
}

func (r *recordingBackend) Apply(_ context.Context, acct *billingv1alpha1.TenantAccount, allowance string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, enforcementCall{method: "Apply", tenant: acct.Name, allowance: allowance})
	return nil
}

func (r *recordingBackend) Freeze(_ context.Context, acct *billingv1alpha1.TenantAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, enforcementCall{method: "Freeze", tenant: acct.Name})
	return nil
}

func (r *recordingBackend) Thaw(_ context.Context, acct *billingv1alpha1.TenantAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, enforcementCall{method: "Thaw", tenant: acct.Name})
	return nil
}

func (r *recordingBackend) getCalls() []enforcementCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]enforcementCall, len(r.calls))
	copy(result, r.calls)
	return result
}

func (r *recordingBackend) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

func (r *recordingBackend) callsForTenant(tenant string) []enforcementCall {
	all := r.getCalls()
	var result []enforcementCall
	for _, c := range all {
		if c.tenant == tenant {
			result = append(result, c)
		}
	}
	return result
}

// createEnforcementFixtures creates a Plan and PriceBook in the given namespace
// for enforcement integration tests. The Plan has TPM=5,000,000 and references
// the PriceBook. With rates $3.00/$15.00 per 1M tokens, the blended per-token
// price is $0.000009.
func createEnforcementFixtures(ctx context.Context, ns string) {
	plan := &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: ns,
		},
		Spec: billingv1alpha1.PlanSpec{
			Limits: billingv1alpha1.PlanLimits{
				TokensPerMinute: int64Ptr(5000000),
			},
			TargetRef:    testTargetRef(),
			PriceBookRef: "test-pricebook",
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, plan)).To(Succeed())

	pb := &billingv1alpha1.PriceBook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pricebook",
			Namespace: ns,
		},
		Spec: billingv1alpha1.PriceBookSpec{
			Currency:      "USD",
			EffectiveFrom: metav1.Now(),
			NoticeDays:    30,
			SKUs: []billingv1alpha1.SKU{{
				SKUName: "test-model",
				Unit:    "per-1M-tokens",
				Rates: map[string]string{
					"prompt_tokens":     "3.00",
					"completion_tokens": "15.00",
				},
			}},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, pb)).To(Succeed())
	pb.Status.State = "Active"
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, pb)).To(Succeed())
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
					BillingConnectorRef:  &corev1.LocalObjectReference{Name: "test-connector"},
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

			// Dunning state machine entries (event-gated, M=3)
			Entry("1 payment failure → Dunning",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active", ConsecutivePaymentFailures: 1},
				}, "500.000000", "Dunning"),

			Entry("2 payment failures → Dunning",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active", ConsecutivePaymentFailures: 2},
				}, "500.000000", "Dunning"),

			Entry("3 payment failures → Frozen",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Dunning", ConsecutivePaymentFailures: 3},
				}, "500.000000", "Frozen"),

			Entry("5 payment failures → Frozen",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Dunning", ConsecutivePaymentFailures: 5},
				}, "500.000000", "Frozen"),

			Entry("Payment failure recovery (counter reset) → Active",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "Prepaid"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Dunning", ConsecutivePaymentFailures: 0},
				}, "500.000000", "Active"),

			Entry("1 failure + zero balance → Dunning (failures take precedence over balance freeze)",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{GraceAmount: "0.000000"},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active", ConsecutivePaymentFailures: 1},
				}, "0.000000", "Dunning"),

			Entry("ShowbackOnly ignores payment failures",
				&billingv1alpha1.TenantAccount{
					Spec:   billingv1alpha1.TenantAccountSpec{BillingMode: "ShowbackOnly"},
					Status: billingv1alpha1.TenantAccountStatus{State: "Active", ConsecutivePaymentFailures: 5},
				}, "500.000000", "Active"),

			Entry("Payment failures take precedence over balance-based Grace",
				&billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: "Prepaid",
						GracePolicy: &billingv1alpha1.GracePolicy{GraceAmount: "50.000000"},
					},
					Status: billingv1alpha1.TenantAccountStatus{State: "Grace", ConsecutivePaymentFailures: 2},
				}, "-10.000000", "Dunning"),
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

	Context("Enforcement: Prepaid tenant Apply is called with correct allowance", func() {
		It("should call Apply with clamped allowance and populate AllowanceTokens", func() {
			ns := createTestNamespace(ctx, "enforce-apply")
			tenantName := "ten-enforce-apply"
			createEnforcementFixtures(ctx, ns)

			setBalance(tenantName, "100.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			testBackend.reset()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "Enforce Apply Co",
					PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
					BillingMode: "Prepaid",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for Active state and AllowanceTokens to be populated.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
				g.Expect(fetched.Status.AllowanceTokens).To(Equal("5000000"),
					"Allowance should be clamped to plan TPM (balance/price=11M > TPM=5M)")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Assert recording backend captured Apply with correct allowance.
			Eventually(func() bool {
				for _, c := range testBackend.callsForTenant(tenantName) {
					if c.method == "Apply" && c.allowance == "5000000" {
						return true
					}
				}
				return false
			}, 5*time.Second, 250*time.Millisecond).Should(BeTrue(),
				"Expected at least one Apply call with allowance 5000000")
		})
	})

	Context("Enforcement: Frozen transition calls Freeze", func() {
		It("should call Freeze when transitioning to Frozen", func() {
			ns := createTestNamespace(ctx, "enforce-freeze")
			tenantName := "ten-enforce-freeze"
			createEnforcementFixtures(ctx, ns)

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
					DisplayName: "Freeze Co",
					PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount: "0.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for Active state.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drain balance -> Frozen (graceAmount=0 means zero balance = Frozen).
			testBackend.reset()
			setBalance(tenantName, "-100.000000")

			// Wait for Frozen state.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Assert Freeze was called.
			Eventually(func() bool {
				for _, c := range testBackend.callsForTenant(tenantName) {
					if c.method == "Freeze" {
						return true
					}
				}
				return false
			}, 5*time.Second, 250*time.Millisecond).Should(BeTrue(),
				"Expected at least one Freeze call for the tenant")
		})
	})

	Context("Enforcement: Thaw transition calls Thaw then Apply", func() {
		It("should call Thaw and Apply when transitioning from Frozen to Active", func() {
			ns := createTestNamespace(ctx, "enforce-thaw")
			tenantName := "ten-enforce-thaw"
			createEnforcementFixtures(ctx, ns)

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
					DisplayName: "Thaw Co",
					PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
					BillingMode: "Prepaid",
					GracePolicy: &billingv1alpha1.GracePolicy{
						GraceAmount: "0.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Activate.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Freeze.
			setBalance(tenantName, "-100.000000")
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Top up -> should trigger Thaw then Apply.
			testBackend.reset()
			setBalance(tenantName, "200.000000")

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Assert both Thaw and Apply were called.
			Eventually(func(g Gomega) {
				calls := testBackend.callsForTenant(tenantName)
				hasThaw := false
				hasApply := false
				for _, c := range calls {
					if c.method == "Thaw" {
						hasThaw = true
					}
					if c.method == "Apply" {
						hasApply = true
					}
				}
				g.Expect(hasThaw).To(BeTrue(), "Expected Thaw call after unfreezing")
				g.Expect(hasApply).To(BeTrue(), "Expected Apply call after unfreezing")
			}, 5*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Enforcement: ShowbackOnly tenant never gets Apply", func() {
		It("should not call Apply for ShowbackOnly tenants", func() {
			ns := createTestNamespace(ctx, "enforce-showback")
			tenantName := "ten-enforce-showback"
			createEnforcementFixtures(ctx, ns)

			setBalance(tenantName, "100.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			testBackend.reset()

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName: "ShowbackOnly Co",
					PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
					BillingMode: "ShowbackOnly",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for Active state to confirm reconciliation ran.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Assert no enforcement calls were made for this tenant over a sustained period.
			Consistently(func() []enforcementCall {
				return testBackend.callsForTenant(tenantName)
			}, 2*time.Second, 200*time.Millisecond).Should(BeEmpty(),
				"ShowbackOnly tenants should never trigger enforcement calls")
		})
	})

	Context("Dunning lifecycle: Active → Dunning → Frozen via payment failures", func() {
		It("should transition through Dunning states based on ConsecutivePaymentFailures", func() {
			ns := createTestNamespace(ctx, "dunning-lifecycle")
			tenantName := "ten-dunning-lifecycle"

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
					DisplayName: "Dunning Co",
					PlanRef:     corev1.LocalObjectReference{Name: "basic"},
					BillingMode: "Prepaid",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Should activate with positive balance
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Simulate 1 payment failure → Dunning
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.ConsecutivePaymentFailures = 1
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.State).To(Equal("Dunning"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Escalate to 3 failures → Frozen
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.ConsecutivePaymentFailures = 3
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Recovery: clear failures → Active (balance still positive)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.ConsecutivePaymentFailures = 0
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Reload: creates exactly one PaymentIntent per threshold crossing", func() {
		It("should create one intent and not duplicate on subsequent reconciles", func() {
			ns := createTestNamespace(ctx, "reload-idem")
			tenantName := "ten-reload-idem"

			initialIntentCount := len(testPSP.GetIntents())

			setBalance(tenantName, "3000.000000")
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
					DisplayName:         "Reload Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Balance $3000 < threshold $5000 → should trigger reload.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.PendingReloadIntentID).NotTo(BeEmpty(),
					"Should have a pending reload intent")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Exactly one new intent should exist for this tenant.
			newIntents := testPSP.GetIntents()[initialIntentCount:]
			Expect(newIntents).To(HaveLen(1),
				fmt.Sprintf("Expected exactly 1 new intent, got %d", len(newIntents)))
			Expect(newIntents[0].Amount).To(Equal("25000.000000"))
			Expect(newIntents[0].TenantName).To(Equal(tenantName))

			// Subsequent reconciles should not create duplicates.
			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(initialIntentCount+1),
				"No duplicate intents after initial reload")
		})
	})

	Context("Reload: balance above threshold does not trigger reload", func() {
		It("should not create a PaymentIntent when balance >= threshold", func() {
			ns := createTestNamespace(ctx, "reload-above")
			tenantName := "ten-reload-above"

			setBalance(tenantName, "10000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			initialIntentCount := len(testPSP.GetIntents())

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Above Threshold Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for reconcile to run (state should be set).
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Balance).To(Equal("10000.000000"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// No new intents should have been created.
			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(initialIntentCount),
				"No reload intent should be created when balance >= threshold")
		})
	})

	Context("Reload: Frozen tenant does not trigger reload", func() {
		It("should not create a PaymentIntent for Frozen tenants", func() {
			ns := createTestNamespace(ctx, "reload-frozen")
			tenantName := "ten-reload-frozen"

			// Start with balance above threshold to prevent reload on first reconcile.
			setBalance(tenantName, "10000.000000")
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
					DisplayName:         "Frozen Reload Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for Active state (balance $10k > threshold $5k, no reload).
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Drop balance below threshold AND set 3 payment failures → Frozen.
			setBalance(tenantName, "1000.000000")
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.ConsecutivePaymentFailures = 3
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			// Wait for Frozen state.
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.State).To(Equal("Frozen"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Snapshot intent count after Frozen is confirmed.
			frozenIntentCount := len(testPSP.GetIntents())

			// Verify no new intents created while Frozen.
			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(frozenIntentCount),
				"No reload intent should be created for Frozen tenants")
		})
	})

	Context("Enforcement: Allowance populates status.AllowanceTokens", func() {
		It("should compute and populate AllowanceTokens from balance and pricing", func() {
			ns := createTestNamespace(ctx, "enforce-allowance")
			tenantName := "ten-enforce-allowance"
			createEnforcementFixtures(ctx, ns)

			// $50 / $0.000009 per token = 5,555,555 tokens, clamped to TPM 5,000,000.
			setBalance(tenantName, "50.000000")
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
					DisplayName: "Allowance Co",
					PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
					BillingMode: "Prepaid",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: tenantName, Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
				g.Expect(fetched.Status.AllowanceTokens).NotTo(BeEmpty(),
					"AllowanceTokens should be populated")
				g.Expect(fetched.Status.AllowanceTokens).To(Equal("5000000"),
					"Expected $50 / $0.000009 = 5,555,555 clamped to TPM 5,000,000")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Fraud: monthly cap blocks reload when exceeded", func() {
		It("should not create a PaymentIntent when monthly total + reload > cap", func() {
			ns := createTestNamespace(ctx, "fraud-mcap")
			tenantName := "ten-fraud-mcap"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			// Monthly total $50k + reload $25k = $75k > cap $60k → should block.
			setPaymentCreditTotal(tenantName, "50000.000000")

			initialIntentCount := len(testPSP.GetIntents())

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Cap Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for reconcile to run.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Balance).To(Equal("3000.000000"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// No new intents — monthly cap exceeded.
			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(initialIntentCount),
				"No reload intent when monthly cap would be exceeded")
		})
	})

	Context("Fraud: monthly cap allows reload within limit", func() {
		It("should create a PaymentIntent when monthly total + reload <= cap", func() {
			ns := createTestNamespace(ctx, "fraud-mcap-ok")
			tenantName := "ten-fraud-mcap-ok"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			// Monthly total $10k + reload $25k = $35k <= cap $60k → should allow.
			setPaymentCreditTotal(tenantName, "10000.000000")

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Cap Ok Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.PendingReloadIntentID).NotTo(BeEmpty(),
					"Should have a pending reload intent when within monthly cap")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Fraud: velocity limit blocks reload when 24h count met", func() {
		It("should not create a PaymentIntent when daily reload count meets limit", func() {
			ns := createTestNamespace(ctx, "fraud-vel")
			tenantName := "ten-fraud-vel"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			// Monthly cap is fine but velocity limit hit: 3 reloads in 24h, limit is 3.
			setPaymentCreditTotal(tenantName, "10000.000000")
			setPaymentCreditCount(tenantName, 3)

			initialIntentCount := len(testPSP.GetIntents())

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Vel Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
						MaxReloadsPerDay: 3,
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Balance).To(Equal("3000.000000"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(initialIntentCount),
				"No reload intent when velocity limit is reached")
		})
	})

	Context("Fraud: velocity limit allows reload when under limit", func() {
		It("should create a PaymentIntent when daily count is below limit", func() {
			ns := createTestNamespace(ctx, "fraud-vel-ok")
			tenantName := "ten-fraud-vel-ok"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			// 2 reloads in 24h, limit is 3 → OK.
			setPaymentCreditTotal(tenantName, "10000.000000")
			setPaymentCreditCount(tenantName, 2)

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Vel Ok Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
						MaxReloadsPerDay: 3,
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.PendingReloadIntentID).NotTo(BeEmpty(),
					"Should create intent when under velocity limit")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Fraud: payment method cooldown blocks reload", func() {
		It("should not create a PaymentIntent during cooldown period", func() {
			ns := createTestNamespace(ctx, "fraud-cool")
			tenantName := "ten-fraud-cool"

			// Start with balance ABOVE threshold to prevent reload on first reconcile.
			setBalance(tenantName, "10000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			setPaymentCreditTotal(tenantName, "0")

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Cool Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for initial reconcile (balance above threshold, no reload).
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Inject cooldown via status update.
			recentChange := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.PaymentMethodChangedAt = &recentChange
			fetched.Status.LastPSPCustomerSecretRef = "some-old-secret"
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			// Now drop balance below threshold → reload would trigger but cooldown blocks it.
			setBalance(tenantName, "3000.000000")
			cooldownIntentCount := len(testPSP.GetIntents())

			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 3*time.Second, 200*time.Millisecond).Should(Equal(cooldownIntentCount),
				"No reload intent during payment method cooldown")
		})
	})

	Context("Fraud: expired cooldown allows reload", func() {
		It("should create a PaymentIntent after cooldown expires", func() {
			ns := createTestNamespace(ctx, "fraud-cool-exp")
			tenantName := "ten-fraud-cool-exp"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			setPaymentCreditTotal(tenantName, "0")

			// Set PaymentMethodChangedAt to 25 hours ago → cooldown expired.
			expiredChange := metav1.NewTime(time.Now().Add(-25 * time.Hour))
			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:          "Fraud Cool Exp Co",
					PlanRef:              corev1.LocalObjectReference{Name: "basic"},
					BillingMode:          "Prepaid",
					PSPCustomerSecretRef: &corev1.LocalObjectReference{Name: "stripe-secret"},
					BillingConnectorRef:  &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for initial reconcile to set LastPSPCustomerSecretRef.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.LastPSPCustomerSecretRef).To(Equal("stripe-secret"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Inject expired cooldown via status update. LastPSPCustomerSecretRef
			// must match spec to prevent change detection from setting a fresh cooldown.
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			fetched.Status.PaymentMethodChangedAt = &expiredChange
			fetched.Status.LastPSPCustomerSecretRef = "stripe-secret"
			fetched.Status.PendingReloadIntentID = ""
			Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())

			// Cooldown expired → reload should proceed.
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.PendingReloadIntentID).NotTo(BeEmpty(),
					"Should create intent after cooldown expires")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("Fraud: fail-closed on ledger query error", func() {
		It("should not create a PaymentIntent when monthly cap query fails", func() {
			ns := createTestNamespace(ctx, "fraud-failcl")
			tenantName := "ten-fraud-failcl"

			setBalance(tenantName, "3000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			// Inject error on adjustment query.
			setPaymentCreditError(tenantName, fmt.Errorf("connection refused"))

			initialIntentCount := len(testPSP.GetIntents())

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:         "Fraud Failcl Co",
					PlanRef:             corev1.LocalObjectReference{Name: "basic"},
					BillingMode:         "Prepaid",
					BillingConnectorRef: &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for reconcile to run.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Balance).To(Equal("3000.000000"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Fail-closed: no intent created when ledger query fails.
			Consistently(func() int {
				return len(testPSP.GetIntents())
			}, 2*time.Second, 200*time.Millisecond).Should(Equal(initialIntentCount),
				"No reload intent when ledger query fails (fail-closed)")

			// Cleanup: clear the error so it doesn't affect other tests.
			setPaymentCreditError(tenantName, nil)
		})
	})

	Context("Fraud: payment method change sets cooldown timestamp", func() {
		It("should set PaymentMethodChangedAt when PSPCustomerSecretRef changes", func() {
			ns := createTestNamespace(ctx, "fraud-pmchg")
			tenantName := "ten-fraud-pmchg"

			setBalance(tenantName, "10000.000000")
			testLedger.mu.Lock()
			if testLedger.spends == nil {
				testLedger.spends = make(map[string]string)
			}
			testLedger.spends[tenantName] = "0"
			testLedger.mu.Unlock()

			setPaymentCreditTotal(tenantName, "0")

			acct := &billingv1alpha1.TenantAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: ns,
				},
				Spec: billingv1alpha1.TenantAccountSpec{
					DisplayName:          "PM Change Co",
					PlanRef:              corev1.LocalObjectReference{Name: "basic"},
					BillingMode:          "Prepaid",
					PSPCustomerSecretRef: &corev1.LocalObjectReference{Name: "stripe-secret-v1"},
					BillingConnectorRef:  &corev1.LocalObjectReference{Name: "test-connector"},
					ReloadPolicy: &billingv1alpha1.ReloadPolicy{
						Enabled:          true,
						ThresholdAmount:  "5000.000000",
						ReloadAmount:     "25000.000000",
						MonthlyCapAmount: "60000.000000",
					},
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			// Wait for initial reconcile to set LastPSPCustomerSecretRef.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.LastPSPCustomerSecretRef).To(Equal("stripe-secret-v1"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Initially no cooldown (first setup, not a change).
			var fetched billingv1alpha1.TenantAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &fetched)).To(Succeed())
			Expect(fetched.Status.PaymentMethodChangedAt).To(BeNil(),
				"No cooldown on initial setup")

			// Change the PSP secret ref.
			fetched.Spec.PSPCustomerSecretRef = &corev1.LocalObjectReference{Name: "stripe-secret-v2"}
			Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())

			// Reconciler should detect the change and set PaymentMethodChangedAt.
			Eventually(func(g Gomega) {
				var f billingv1alpha1.TenantAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: ns}, &f)).To(Succeed())
				g.Expect(f.Status.LastPSPCustomerSecretRef).To(Equal("stripe-secret-v2"))
				g.Expect(f.Status.PaymentMethodChangedAt).NotTo(BeNil(),
					"PaymentMethodChangedAt should be set after secret ref change")
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})
})
