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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var _ = Describe("BillingConsole Controller", Serial, Ordered, func() {
	const timeout = 10 * time.Second
	const interval = 250 * time.Millisecond

	depKey := types.NamespacedName{Name: "billing-console", Namespace: "default"}
	svcKey := types.NamespacedName{Name: "billing-console", Namespace: "default"}
	crKey := types.NamespacedName{Name: "default"}

	saKey := types.NamespacedName{Name: "billing-console", Namespace: "default"}
	cookieKey := types.NamespacedName{Name: "billing-console-oauth", Namespace: "default"}

	cleanupConsole := func() {
		cr := &billingv1alpha1.BillingConsole{}
		if err := k8sClient.Get(ctx, crKey, cr); err == nil {
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, crKey, cr))
			}, timeout, interval).Should(BeTrue())
		}
		dep := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, depKey, dep); err == nil {
			Expect(k8sClient.Delete(ctx, dep)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, depKey, dep))
			}, timeout, interval).Should(BeTrue())
		}
		svc := &corev1.Service{}
		if err := k8sClient.Get(ctx, svcKey, svc); err == nil {
			Expect(k8sClient.Delete(ctx, svc)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, svcKey, svc))
			}, timeout, interval).Should(BeTrue())
		}
		sa := &corev1.ServiceAccount{}
		if err := k8sClient.Get(ctx, saKey, sa); err == nil {
			k8sClient.Delete(ctx, sa) //nolint:errcheck
		}
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, cookieKey, secret); err == nil {
			k8sClient.Delete(ctx, secret) //nolint:errcheck
		}
	}

	AfterEach(func() {
		cleanupConsole()
	})

	It("should create a Deployment and Service with correct configuration", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled:  ptr.To(true),
				Image:    "quay.io/test/console:v1",
				Replicas: ptr.To(int32(2)),
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())

			g.Expect(*dep.Spec.Replicas).To(Equal(int32(2)))
			g.Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))

			c := dep.Spec.Template.Spec.Containers[0]
			g.Expect(c.Image).To(Equal("quay.io/test/console:v1"))

			envMap := envVarMap(c.Env)
			g.Expect(envMap).To(HaveKeyWithValue("LEDGER_URL",
				"http://ledger-svc.default.svc.cluster.local:8080"))
			g.Expect(envMap).To(HaveKeyWithValue("LISTEN_ADDR", ":9090"))

			g.Expect(c.LivenessProbe).NotTo(BeNil())
			g.Expect(c.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
			g.Expect(c.ReadinessProbe).NotTo(BeNil())
			g.Expect(c.ReadinessProbe.HTTPGet.Path).To(Equal("/healthz"))

			g.Expect(dep.OwnerReferences).To(HaveLen(1))
			g.Expect(dep.OwnerReferences[0].Kind).To(Equal("BillingConsole"))
			g.Expect(dep.OwnerReferences[0].Name).To(Equal("default"))
			g.Expect(*dep.OwnerReferences[0].Controller).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var svc corev1.Service
			g.Expect(k8sClient.Get(ctx, svcKey, &svc)).To(Succeed())

			g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			g.Expect(svc.Spec.Ports).To(HaveLen(1))
			g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(9090)))
			g.Expect(svc.Spec.Selector).To(HaveKeyWithValue(
				"app.kubernetes.io/name", "billing-console"))

			g.Expect(svc.OwnerReferences).To(HaveLen(1))
			g.Expect(svc.OwnerReferences[0].Kind).To(Equal("BillingConsole"))
			g.Expect(*svc.OwnerReferences[0].Controller).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("should update the Deployment when image changes", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			g.Expect(dep.Spec.Template.Spec.Containers[0].Image).To(
				Equal("quay.io/test/console:v1"))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())
			fetched.Spec.Image = "quay.io/test/console:v2"
			g.Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			g.Expect(dep.Spec.Template.Spec.Containers[0].Image).To(
				Equal("quay.io/test/console:v2"))
		}, timeout, interval).Should(Succeed())
	})

	It("should set InvalidName condition for non-default name", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "not-default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, console)).To(Succeed())
		})

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "not-default",
			}, &fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionConsoleDeployed)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("InvalidName"))
		}, timeout, interval).Should(Succeed())
	})

	It("should add oauth-proxy sidecar for OpenShiftOAuth mode", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
				Auth: &billingv1alpha1.ConsoleAuthConfig{
					Mode: billingv1alpha1.AuthModeOpenShiftOAuth,
					Roles: &billingv1alpha1.ConsoleRoles{
						Admin: []string{"billing-admins"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			g.Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(2))

			var consoleC, proxyC corev1.Container
			for _, c := range dep.Spec.Template.Spec.Containers {
				switch c.Name {
				case "console":
					consoleC = c
				case "oauth-proxy":
					proxyC = c
				}
			}

			g.Expect(proxyC.Image).To(Equal("registry.redhat.io/openshift4/ose-oauth-proxy:latest"))
			g.Expect(proxyC.Ports).To(HaveLen(1))
			g.Expect(proxyC.Ports[0].ContainerPort).To(Equal(int32(4180)))

			envMap := envVarMap(consoleC.Env)
			g.Expect(envMap).To(HaveKeyWithValue("AUTH_MODE", "openshift-oauth"))
			g.Expect(envMap).To(HaveKeyWithValue("CONSOLE_ROLES_ADMIN", "billing-admins"))

			g.Expect(dep.Spec.Template.Spec.ServiceAccountName).To(Equal("billing-console"))
			g.Expect(dep.Spec.Template.Spec.Volumes).To(HaveLen(1))
			g.Expect(dep.Spec.Template.Spec.Volumes[0].Name).To(Equal("oauth-cookie"))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var sa corev1.ServiceAccount
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "billing-console", Namespace: "default",
			}, &sa)).To(Succeed())
			g.Expect(sa.Annotations).To(HaveKey("serviceaccounts.openshift.io/oauth-redirectreference.primary"))
			g.Expect(sa.OwnerReferences).To(HaveLen(1))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var secret corev1.Secret
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "billing-console-oauth", Namespace: "default",
			}, &secret)).To(Succeed())
			g.Expect(secret.Data).To(HaveKey("cookie-secret"))
			g.Expect(secret.OwnerReferences).To(HaveLen(1))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var svc corev1.Service
			g.Expect(k8sClient.Get(ctx, svcKey, &svc)).To(Succeed())
			g.Expect(svc.Spec.Ports[0].TargetPort.String()).To(Equal("oauth-proxy"))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionAuthConfigured)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("Configured"))
		}, timeout, interval).Should(Succeed())
	})

	It("should inject OIDC env vars for OIDC mode", func() {
		oidcSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-oidc-creds", Namespace: "default"},
			Data: map[string][]byte{
				"client-id":     []byte("test-client"),
				"client-secret": []byte("test-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, oidcSecret)).To(Succeed())
		DeferCleanup(func() {
			k8sClient.Delete(ctx, oidcSecret) //nolint:errcheck
		})

		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
				Auth: &billingv1alpha1.ConsoleAuthConfig{
					Mode: billingv1alpha1.AuthModeOIDC,
					OIDC: &billingv1alpha1.OIDCConfig{
						IssuerURL:       "https://idp.example.com",
						ClientSecretRef: corev1.LocalObjectReference{Name: "my-oidc-creds"},
						GroupsClaim:     "roles",
					},
					Roles: &billingv1alpha1.ConsoleRoles{
						Admin:    []string{"admins"},
						Operator: []string{"ops"},
						Viewer:   []string{"readers"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			g.Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))

			c := dep.Spec.Template.Spec.Containers[0]
			envMap := envVarMap(c.Env)
			g.Expect(envMap).To(HaveKeyWithValue("AUTH_MODE", "oidc"))
			g.Expect(envMap).To(HaveKeyWithValue("OIDC_ISSUER_URL", "https://idp.example.com"))
			g.Expect(envMap).To(HaveKeyWithValue("OIDC_GROUPS_CLAIM", "roles"))
			g.Expect(envMap).To(HaveKeyWithValue("CONSOLE_ROLES_ADMIN", "admins"))
			g.Expect(envMap).To(HaveKeyWithValue("CONSOLE_ROLES_OPERATOR", "ops"))
			g.Expect(envMap).To(HaveKeyWithValue("CONSOLE_ROLES_VIEWER", "readers"))

			// OIDC_CLIENT_ID should use SecretKeyRef, not a plain value
			var clientIDEnv *corev1.EnvVar
			for i := range c.Env {
				if c.Env[i].Name == "OIDC_CLIENT_ID" {
					clientIDEnv = &c.Env[i]
					break
				}
			}
			g.Expect(clientIDEnv).NotTo(BeNil())
			g.Expect(clientIDEnv.ValueFrom).NotTo(BeNil())
			g.Expect(clientIDEnv.ValueFrom.SecretKeyRef.Name).To(Equal("my-oidc-creds"))
			g.Expect(clientIDEnv.ValueFrom.SecretKeyRef.Key).To(Equal("client-id"))

			g.Expect(dep.Spec.Template.Spec.ServiceAccountName).To(Equal("billing-console"))
			g.Expect(dep.Spec.Template.Spec.Volumes).To(BeEmpty())
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionAuthConfigured)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("Configured"))
		}, timeout, interval).Should(Succeed())
	})

	It("should set AuthConfigured=False when OIDC Secret is missing", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Auth: &billingv1alpha1.ConsoleAuthConfig{
					Mode: billingv1alpha1.AuthModeOIDC,
					OIDC: &billingv1alpha1.OIDCConfig{
						IssuerURL:       "https://idp.example.com",
						ClientSecretRef: corev1.LocalObjectReference{Name: "nonexistent-secret"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionAuthConfigured)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("SecretNotFound"))
		}, timeout, interval).Should(Succeed())

		// Deployment should serve unauthenticated when auth config is incomplete
		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			envMap := envVarMap(dep.Spec.Template.Spec.Containers[0].Env)
			g.Expect(envMap).NotTo(HaveKey("AUTH_MODE"))
		}, timeout, interval).Should(Succeed())
	})

	It("should set AuthConfigured=False with NotConfigured when no auth is set", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionAuthConfigured)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotConfigured"))
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			c := dep.Spec.Template.Spec.Containers[0]
			envMap := envVarMap(c.Env)
			g.Expect(envMap).NotTo(HaveKey("AUTH_MODE"))
		}, timeout, interval).Should(Succeed())
	})

	It("should inject OPERATOR_NAMESPACE and TENANT_PORTAL_ENABLED env vars", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			c := dep.Spec.Template.Spec.Containers[0]
			envMap := envVarMap(c.Env)
			g.Expect(envMap).To(HaveKeyWithValue("OPERATOR_NAMESPACE", "default"))
			g.Expect(envMap).To(HaveKeyWithValue("TENANT_PORTAL_ENABLED", "true"))
		}, timeout, interval).Should(Succeed())
	})

	It("should set TENANT_PORTAL_ENABLED=false when tenantPortal is disabled", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(true),
				Image:   "quay.io/test/console:v1",
				TenantPortal: &billingv1alpha1.TenantPortalConfig{
					Enabled: ptr.To(false),
				},
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var dep appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			c := dep.Spec.Template.Spec.Containers[0]
			envMap := envVarMap(c.Env)
			g.Expect(envMap).To(HaveKeyWithValue("TENANT_PORTAL_ENABLED", "false"))
			g.Expect(envMap).To(HaveKeyWithValue("OPERATOR_NAMESPACE", "default"))
		}, timeout, interval).Should(Succeed())
	})

	It("should set Disabled condition when enabled=false", func() {
		console := &billingv1alpha1.BillingConsole{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: billingv1alpha1.BillingConsoleSpec{
				Enabled: ptr.To(false),
			},
		}
		Expect(k8sClient.Create(ctx, console)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingConsole
			g.Expect(k8sClient.Get(ctx, crKey, &fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions,
				billingv1alpha1.ConditionConsoleDeployed)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("Disabled"))
			g.Expect(fetched.Status.URL).To(BeEmpty())
		}, timeout, interval).Should(Succeed())
	})
})

func envVarMap(envs []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(envs))
	for _, e := range envs {
		m[e.Name] = e.Value
	}
	return m
}
