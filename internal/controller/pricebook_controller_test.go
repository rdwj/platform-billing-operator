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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var _ = Describe("PriceBookReconciler", func() {
	Context("valid PriceBook", func() {
		It("should transition to Active with Ready=True", func() {
			ns := createTestNamespace(ctx, "pb-valid")
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-pricebook",
					Namespace: ns,
				},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(0, 6, 0)),
					NoticeDays:    30,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "2.50", "completion_tokens": "10.0"},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pb)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.PriceBook
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      pb.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"),
					"expected state Active, got %q; conditions: %+v", fetched.Status.State, fetched.Status.Conditions)
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, conditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal("Valid"))
				g.Expect(fetched.Status.ObservedGeneration).To(Equal(fetched.Generation))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("invalid PriceBook with empty rates map", func() {
		It("should not become Active and set Ready=False", func() {
			ns := createTestNamespace(ctx, "pb-empty-rates")
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-rates-pricebook",
					Namespace: ns,
				},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
					NoticeDays:    0,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pb)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.PriceBook
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      pb.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(BeEmpty(),
					"expected empty state, got %q", fetched.Status.State)
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, conditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("Invalid"))
				g.Expect(cond.Message).To(ContainSubstring("at least 1 rate entry"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("invalid PriceBook with bad rate values", func() {
		It("should not become Active and explain the invalid rate", func() {
			ns := createTestNamespace(ctx, "pb-bad-rate")
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-rate-pricebook",
					Namespace: ns,
				},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
					NoticeDays:    0,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "not-a-number"},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pb)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.PriceBook
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      pb.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(BeEmpty(),
					"expected empty state, got %q", fetched.Status.State)
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, conditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring("not a valid decimal"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("updating a valid PriceBook to invalid", func() {
		It("should revert from Active", func() {
			ns := createTestNamespace(ctx, "pb-revert")
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "revert-pricebook",
					Namespace: ns,
				},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(0, 6, 0)),
					NoticeDays:    30,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "2.50"},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pb)).To(Succeed())

			// Wait for Active.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.PriceBook
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      pb.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(Equal("Active"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Update to invalid: replace the valid rate with a non-decimal value.
			var current billingv1alpha1.PriceBook
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      pb.Name,
				Namespace: ns,
			}, &current)).To(Succeed())
			current.Spec.SKUs[0].Rates["prompt_tokens"] = "oops"
			Expect(k8sClient.Update(ctx, &current)).To(Succeed())

			// Status should revert.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.PriceBook
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      pb.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.State).To(BeEmpty(),
					"expected state to revert to empty, got %q", fetched.Status.State)
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, conditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})
})

// ---------------------------------------------------------------------------
// validatePriceBookSpec unit tests (no envtest needed)
// ---------------------------------------------------------------------------

var _ = Describe("validatePriceBookSpec", func() {
	It("should return no errors for a valid spec", func() {
		spec := &billingv1alpha1.PriceBookSpec{
			Currency:      "USD",
			EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
			SKUs: []billingv1alpha1.SKU{{
				SKUName: "model-a",
				Unit:    "per-1M-tokens",
				Rates:   map[string]string{"prompt_tokens": "1.00", "completion_tokens": "3.00"},
			}},
		}
		Expect(validatePriceBookSpec(spec)).To(BeEmpty())
	})

	It("should reject a negative rate", func() {
		spec := &billingv1alpha1.PriceBookSpec{
			Currency:      "USD",
			EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
			SKUs: []billingv1alpha1.SKU{{
				SKUName: "model-a",
				Unit:    "per-1M-tokens",
				Rates:   map[string]string{"prompt_tokens": "-5.00"},
			}},
		}
		errs := validatePriceBookSpec(spec)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0]).To(ContainSubstring("must not be negative"))
	})

	It("should reject zero-length rates map", func() {
		spec := &billingv1alpha1.PriceBookSpec{
			Currency:      "USD",
			EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
			SKUs: []billingv1alpha1.SKU{{
				SKUName: "model-a",
				Unit:    "per-1M-tokens",
				Rates:   map[string]string{},
			}},
		}
		errs := validatePriceBookSpec(spec)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0]).To(ContainSubstring("at least 1 rate entry"))
	})

	It("should report zero effectiveFrom", func() {
		spec := &billingv1alpha1.PriceBookSpec{
			Currency: "USD",
			SKUs: []billingv1alpha1.SKU{{
				SKUName: "model-a",
				Unit:    "per-1M-tokens",
				Rates:   map[string]string{"prompt_tokens": "1.00"},
			}},
		}
		errs := validatePriceBookSpec(spec)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0]).To(ContainSubstring("effectiveFrom"))
	})
})
