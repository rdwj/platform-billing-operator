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

var _ = Describe("BillingConnector Controller", func() {
	Context("When a BillingConnector with type=stripe is created", func() {
		It("Should set Connected=true when credentials Secret is valid", func() {
			ns := createTestNamespace(ctx, "bc-stripe-valid")

			// Create a Secret with the required Stripe keys.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "stripe-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"secretKey":     []byte("sk_test_abc123"),
					"webhookSecret": []byte("whsec_test_xyz789"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create a BillingConnector referencing the Secret.
			bc := &billingv1alpha1.BillingConnector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-stripe-connector",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BillingConnectorSpec{
					Type: billingv1alpha1.ConnectorTypeStripe,
					CredentialsSecretRef: corev1.LocalObjectReference{
						Name: "stripe-creds",
					},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())

			// Verify status converges to Connected=true with CredentialsValid=True.
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingConnector
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-stripe-connector",
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Connected).To(BeTrue(),
					"Connected should be true when credentials are valid")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionCredentialsValid)
				g.Expect(cond).NotTo(BeNil(), "CredentialsValid condition should exist")
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal("Valid"))
				g.Expect(fetched.Status.ObservedGeneration).To(Equal(fetched.Generation))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

			// Verify the backend is registered in the registry.
			backend, ok := testRegistry.Get("test-stripe-connector")
			Expect(ok).To(BeTrue(), "Backend should be registered in the registry")
			Expect(backend).NotTo(BeNil())
		})
	})

	Context("When the credentials Secret is missing", func() {
		It("Should set Connected=false with SecretNotFound reason", func() {
			ns := createTestNamespace(ctx, "bc-secret-missing")

			// Create a BillingConnector pointing to a non-existent Secret.
			bc := &billingv1alpha1.BillingConnector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-missing-secret",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BillingConnectorSpec{
					Type: billingv1alpha1.ConnectorTypeStripe,
					CredentialsSecretRef: corev1.LocalObjectReference{
						Name: "does-not-exist",
					},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingConnector
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-missing-secret",
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Connected).To(BeFalse(),
					"Connected should be false when Secret is missing")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionCredentialsValid)
				g.Expect(cond).NotTo(BeNil(), "CredentialsValid condition should exist")
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("SecretNotFound"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("When the credentials Secret is missing required keys", func() {
		It("Should set Connected=false with MissingKeys reason", func() {
			ns := createTestNamespace(ctx, "bc-missing-keys")

			// Create a Secret with only one of the required keys.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "partial-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"secretKey": []byte("sk_test_abc123"),
					// "webhookSecret" intentionally absent
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			bc := &billingv1alpha1.BillingConnector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-partial-keys",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BillingConnectorSpec{
					Type: billingv1alpha1.ConnectorTypeStripe,
					CredentialsSecretRef: corev1.LocalObjectReference{
						Name: "partial-creds",
					},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingConnector
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-partial-keys",
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Connected).To(BeFalse(),
					"Connected should be false when required keys are missing")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionCredentialsValid)
				g.Expect(cond).NotTo(BeNil(), "CredentialsValid condition should exist")
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("MissingKeys"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})

		It("Should set MissingKeys when Secret keys have empty values", func() {
			ns := createTestNamespace(ctx, "bc-empty-keys")

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-vals-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"secretKey":     []byte("sk_test_abc123"),
					"webhookSecret": []byte(""), // present but empty
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			bc := &billingv1alpha1.BillingConnector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-empty-vals",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BillingConnectorSpec{
					Type: billingv1alpha1.ConnectorTypeStripe,
					CredentialsSecretRef: corev1.LocalObjectReference{
						Name: "empty-vals-creds",
					},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingConnector
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-empty-vals",
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Connected).To(BeFalse(),
					"Connected should be false when key values are empty")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionCredentialsValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("MissingKeys"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})

	Context("When type=webhook", func() {
		It("Should set ConditionCredentialsValid with NotImplemented reason", func() {
			ns := createTestNamespace(ctx, "bc-webhook")

			// Even though webhook isn't implemented, create a Secret so the test
			// focuses on the type check, not the missing-secret path.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "webhook-creds",
					Namespace: ns,
				},
				Data: map[string][]byte{
					"hmacSecret": []byte("test-hmac-secret"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			bc := &billingv1alpha1.BillingConnector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-webhook-connector",
					Namespace: ns,
				},
				Spec: billingv1alpha1.BillingConnectorSpec{
					Type: billingv1alpha1.ConnectorTypeWebhook,
					CredentialsSecretRef: corev1.LocalObjectReference{
						Name: "webhook-creds",
					},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingConnector
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-webhook-connector",
					Namespace: ns,
				}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Connected).To(BeFalse(),
					"Connected should be false for unimplemented webhook type")
				cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.ConditionCredentialsValid)
				g.Expect(cond).NotTo(BeNil(), "CredentialsValid condition should exist")
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("NotImplemented"))
			}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		})
	})
})
