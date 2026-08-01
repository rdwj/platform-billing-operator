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
}

func (s *stubBalanceQuerier) QueryTenantBalance(_ context.Context, tenantID string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

		DescribeTable("state computation by billing mode",
			func(mode, existingState, expectedState string) {
				acct := &billingv1alpha1.TenantAccount{
					Spec: billingv1alpha1.TenantAccountSpec{
						BillingMode: mode,
					},
					Status: billingv1alpha1.TenantAccountStatus{
						State: existingState,
					},
				}
				Expect(reconciler.computeState(acct, "100.00")).To(Equal(expectedState))
			},
			Entry("ShowbackOnly always Active", "ShowbackOnly", "", "Active"),
			Entry("ShowbackOnly overrides existing state", "ShowbackOnly", "Frozen", "Active"),
			Entry("Prepaid with no prior state → Pending", "Prepaid", "", "Pending"),
			Entry("Prepaid preserves existing state", "Prepaid", "Active", "Active"),
			Entry("Invoiced with no prior state → Pending", "Invoiced", "", "Pending"),
			Entry("Invoiced preserves existing state", "Invoiced", "Grace", "Grace"),
		)
	})
})
