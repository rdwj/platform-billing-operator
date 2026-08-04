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
