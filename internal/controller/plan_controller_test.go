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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// testTargetRef returns a standard targetRef for test Plans.
func testTargetRef() gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName {
	return gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayapiv1alpha2.LocalPolicyTargetReference{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  "test-gateway",
		},
	}
}

// createTestNamespace creates a unique namespace for test isolation.
func createTestNamespace(ctx context.Context, prefix string) string {
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, ns)).To(Succeed())
	return name
}

// int64Ptr returns a pointer to the given int64 value.
func int64Ptr(v int64) *int64 { return &v }

// int32Ptr returns a pointer to the given int32 value.
func int32Ptr(v int32) *int32 { return &v }

// ---------------------------------------------------------------------------
// Integration tests (envtest)
// ---------------------------------------------------------------------------

var _ = Describe("PlanReconciler", func() {
	Context("when Kuadrant CRDs are not installed", func() {
		It("should set the KuadrantNotInstalled condition", func() {
			ns := createTestNamespace(ctx, "kuadrant-missing")
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-no-kuadrant",
					Namespace: ns,
				},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						TokensPerMinute: int64Ptr(1000),
					},
				},
			}
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.Plan
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      plan.Name,
					Namespace: ns,
				}, &fetched)).To(Succeed())
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionKuadrantNotInstalled)
				g.Expect(cond).NotTo(BeNil(), "expected KuadrantNotInstalled condition to be set")
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal("CRDsMissing"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("when a Plan has maxConcurrency set (unit test)", func() {
		It("should set the UnsupportedField condition via setUnsupportedFieldConditions", func() {
			reconciler := &PlanReconciler{}
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-maxconc", Namespace: "default", Generation: 1},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits:    billingv1alpha1.PlanLimits{MaxConcurrency: int32Ptr(10)},
				},
			}
			reconciler.setUnsupportedFieldConditions(plan)
			cond := apimeta.FindStatusCondition(plan.Status.Conditions, billingv1alpha1.ConditionUnsupportedField)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("maxConcurrency"))
		})
	})

	Context("when a Plan has non-wildcard geoScopes (unit test)", func() {
		It("should set the UnsupportedField condition via setUnsupportedFieldConditions", func() {
			reconciler := &PlanReconciler{}
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-geo", Namespace: "default", Generation: 1},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					GeoScopes: []string{"us-east-1"},
					Limits:    billingv1alpha1.PlanLimits{TokensPerMinute: int64Ptr(500)},
				},
			}
			reconciler.setUnsupportedFieldConditions(plan)
			cond := apimeta.FindStatusCondition(plan.Status.Conditions, billingv1alpha1.ConditionUnsupportedField)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("geoScopes"))
		})
	})
})

// ---------------------------------------------------------------------------
// PriceBook webhook validation (direct validator calls)
// ---------------------------------------------------------------------------

var _ = Describe("PriceBookValidator", func() {
	var validator *billingv1alpha1.PriceBookValidator

	BeforeEach(func() {
		validator = &billingv1alpha1.PriceBookValidator{}
	})

	Context("ValidateCreate", func() {
		It("should accept a valid PriceBook with effectiveFrom far in the future", func() {
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{Name: "valid-pb", Namespace: "default"},
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
			warnings, err := validator.ValidateCreate(context.Background(), pb)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should reject effectiveFrom less than noticeDays in the future", func() {
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{Name: "too-soon-pb", Namespace: "default"},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().Add(24 * time.Hour)), // 1 day from now
					NoticeDays:    30,                                             // requires 30 days
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "2.50"},
					}},
				},
			}
			_, err := validator.ValidateCreate(context.Background(), pb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("effectiveFrom"))
		})

		It("should reject invalid decimal rates", func() {
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-rates-pb", Namespace: "default"},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
					NoticeDays:    0,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "abc"},
					}},
				},
			}
			_, err := validator.ValidateCreate(context.Background(), pb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("non-negative decimal string"))
		})

		It("should reject rates with too many fractional digits", func() {
			pb := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{Name: "too-precise-pb", Namespace: "default"},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().AddDate(1, 0, 0)),
					NoticeDays:    0,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "1.1234567"},
					}},
				},
			}
			_, err := validator.ValidateCreate(context.Background(), pb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("non-negative decimal string"))
		})
	})

	Context("ValidateUpdate", func() {
		It("should reject SKU edits after effectiveFrom has passed", func() {
			oldPB := &billingv1alpha1.PriceBook{
				ObjectMeta: metav1.ObjectMeta{Name: "immutable-pb", Namespace: "default"},
				Spec: billingv1alpha1.PriceBookSpec{
					Currency:      "USD",
					EffectiveFrom: metav1.NewTime(time.Now().Add(-24 * time.Hour)), // in the past
					NoticeDays:    0,
					SKUs: []billingv1alpha1.SKU{{
						SKUName: "gpt-4o",
						Unit:    "per-1M-tokens",
						Rates:   map[string]string{"prompt_tokens": "2.50"},
					}},
				},
			}
			newPB := oldPB.DeepCopy()
			newPB.Spec.SKUs[0].Rates["prompt_tokens"] = "5.00"

			_, err := validator.ValidateUpdate(context.Background(), oldPB, newPB)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable after effectiveFrom"))
		})
	})
})

// ---------------------------------------------------------------------------
// Builder unit tests (no envtest client needed)
// ---------------------------------------------------------------------------

var _ = Describe("PolicyCompiler builders", func() {
	var reconciler *PlanReconciler

	BeforeEach(func() {
		reconciler = &PlanReconciler{}
	})

	Context("buildDesiredTRLP", func() {
		It("should produce a TRLP with both token limits", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-trlp", Namespace: "test-ns"},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						TokensPerMinute: int64Ptr(5000),
						TokensPerDay:    int64Ptr(100000),
					},
				},
			}
			trlp := reconciler.buildDesiredTRLP(plan)
			Expect(trlp).NotTo(BeNil())
			Expect(trlp.Name).To(Equal("plan-trlp-trlp"))
			Expect(trlp.Namespace).To(Equal("test-ns"))

			Expect(trlp.Spec.Limits).To(HaveLen(2))
			Expect(trlp.Spec.Limits).To(HaveKey("tokens-per-minute"))
			Expect(trlp.Spec.Limits).To(HaveKey("tokens-per-day"))

			tpm := trlp.Spec.Limits["tokens-per-minute"]
			Expect(tpm.Rates).To(HaveLen(1))
			Expect(tpm.Rates[0].Limit).To(Equal(5000))
			Expect(string(tpm.Rates[0].Window)).To(Equal("1m"))

			tpd := trlp.Spec.Limits["tokens-per-day"]
			Expect(tpd.Rates).To(HaveLen(1))
			Expect(tpd.Rates[0].Limit).To(Equal(100000))
			Expect(string(tpd.Rates[0].Window)).To(Equal("24h"))
		})

		It("should return nil when no token limits are configured", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-no-tokens", Namespace: "test-ns"},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						RequestsPerMinute: int32Ptr(100),
					},
				},
			}
			Expect(reconciler.buildDesiredTRLP(plan)).To(BeNil())
		})
	})

	Context("buildDesiredRLP", func() {
		It("should produce an RLP with request limits", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-rlp", Namespace: "test-ns"},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						RequestsPerMinute: int32Ptr(60),
					},
				},
			}
			rlp := reconciler.buildDesiredRLP(plan)
			Expect(rlp).NotTo(BeNil())
			Expect(rlp.Name).To(Equal("plan-rlp-rlp"))
			Expect(rlp.Namespace).To(Equal("test-ns"))

			Expect(rlp.Spec.Limits).To(HaveLen(1))
			Expect(rlp.Spec.Limits).To(HaveKey("requests-per-minute"))

			rpm := rlp.Spec.Limits["requests-per-minute"]
			Expect(rpm.Rates).To(HaveLen(1))
			Expect(rpm.Rates[0].Limit).To(Equal(60))
			Expect(string(rpm.Rates[0].Window)).To(Equal("1m"))
		})

		It("should return nil when no request limits are configured", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-no-requests", Namespace: "test-ns"},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						TokensPerMinute: int64Ptr(1000),
					},
				},
			}
			Expect(reconciler.buildDesiredRLP(plan)).To(BeNil())
		})
	})

	Context("buildDesiredAuthPolicy", func() {
		It("should produce an AuthPolicy with model scope predicates", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "plan-auth",
					Namespace: "test-ns",
					UID:       "test-uid-123",
				},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef:   testTargetRef(),
					ModelScopes: []string{"llama-4-*", "granite-*"},
					Limits: billingv1alpha1.PlanLimits{
						TokensPerMinute: int64Ptr(1000),
					},
				},
			}
			authPolicy := reconciler.buildDesiredAuthPolicy(plan)
			Expect(authPolicy).NotTo(BeNil())
			Expect(authPolicy.GetName()).To(Equal("plan-auth-auth"))
			Expect(authPolicy.GetNamespace()).To(Equal("test-ns"))

			// Verify the when predicates contain model-scope expressions.
			spec, found, err := unstructuredNestedField(authPolicy.Object, "spec", "when")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			whenList, ok := spec.([]interface{})
			Expect(ok).To(BeTrue())
			Expect(whenList).To(HaveLen(1))

			predMap, ok := whenList[0].(map[string]interface{})
			Expect(ok).To(BeTrue())
			predicate, ok := predMap["predicate"].(string)
			Expect(ok).To(BeTrue())
			Expect(predicate).To(ContainSubstring("llama"))
			Expect(predicate).To(ContainSubstring("granite"))
		})

		It("should return nil when no model scopes are configured", func() {
			plan := &billingv1alpha1.Plan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-no-models", Namespace: "test-ns"},
				Spec: billingv1alpha1.PlanSpec{
					TargetRef: testTargetRef(),
					Limits: billingv1alpha1.PlanLimits{
						TokensPerMinute: int64Ptr(1000),
					},
				},
			}
			Expect(reconciler.buildDesiredAuthPolicy(plan)).To(BeNil())
		})
	})
})

// ---------------------------------------------------------------------------
// globToRegex unit tests (table-driven)
// ---------------------------------------------------------------------------

var _ = Describe("globToRegex", func() {
	DescribeTable("converts glob patterns to anchored regexes",
		func(glob, expected string) {
			Expect(globToRegex(glob)).To(Equal(expected))
		},
		Entry("trailing wildcard", "llama-4-*", `^llama-4-.*$`),
		Entry("prefix wildcard", "granite-*", `^granite-.*$`),
		Entry("path wildcard", "pass-through/*", `^pass-through/.*$`),
		Entry("exact match (no glob)", "gpt-4o", `^gpt-4o$`),
		Entry("double wildcard", "model-*-v*", `^model-.*-v.*$`),
		Entry("question mark", "llama-?", `^llama-.$`),
		Entry("dot in name", "model.v2", `^model\.v2$`),
		Entry("bare wildcard", "*", `^.*$`),
	)
})

// unstructuredNestedField is a helper to walk nested maps inside an
// unstructured object's data. It returns the value at the given path,
// whether it was found, and any error.
func unstructuredNestedField(obj map[string]interface{}, fields ...string) (interface{}, bool, error) {
	var current interface{} = obj
	for _, f := range fields {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, fmt.Errorf("expected map at %q, got %T", f, current)
		}
		val, exists := m[f]
		if !exists {
			return nil, false, nil
		}
		current = val
	}
	return current, true, nil
}
