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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var (
	routeGVK       = schema.GroupVersionKind{Group: "route.openshift.io", Version: "v1", Kind: "Route"}
	consoleLinkGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsoleLink"}
)

const (
	consoleResourceName = "billing-console"
	consoleListenPort   = 9090
)

// BillingConsoleReconciler reconciles a BillingConsole object.
type BillingConsoleReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=billingconsoles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=billingconsoles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consolelinks,verbs=get;list;watch;create;update;patch;delete

func (r *BillingConsoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var console billingv1alpha1.BillingConsole
	if err := r.Get(ctx, req.NamespacedName, &console); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if console.Name != "default" {
		apimeta.SetStatusCondition(&console.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionConsoleDeployed,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: console.Generation,
			Reason:             "InvalidName",
			Message:            `BillingConsole is a singleton; the resource must be named "default"`,
		})
		console.Status.ObservedGeneration = console.Generation
		if err := r.Status().Update(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	enabled := console.Spec.Enabled == nil || *console.Spec.Enabled

	if !enabled {
		r.deleteIfExists(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: consoleResourceName, Namespace: r.OperatorNamespace,
		}})
		r.deleteIfExists(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: consoleResourceName, Namespace: r.OperatorNamespace,
		}})
		if r.apiAvailable(routeGVK) {
			route := &unstructured.Unstructured{}
			route.SetGroupVersionKind(routeGVK)
			route.SetName(consoleResourceName)
			route.SetNamespace(r.OperatorNamespace)
			r.deleteIfExists(ctx, route)
		}
		if r.apiAvailable(consoleLinkGVK) {
			cl := &unstructured.Unstructured{}
			cl.SetGroupVersionKind(consoleLinkGVK)
			cl.SetName(consoleResourceName)
			r.deleteIfExists(ctx, cl)
		}

		apimeta.SetStatusCondition(&console.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionConsoleDeployed,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: console.Generation,
			Reason:             "Disabled",
			Message:            "Console is disabled via spec.enabled=false",
		})
		console.Status.URL = ""
		console.Status.ObservedGeneration = console.Generation
		if err := r.Status().Update(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// --- Deployment ---
	if err := r.reconcileDeployment(ctx, &console); err != nil {
		return ctrl.Result{}, err
	}

	// --- Service ---
	if err := r.reconcileService(ctx, &console); err != nil {
		return ctrl.Result{}, err
	}

	// --- Route (OpenShift only) ---
	if r.apiAvailable(routeGVK) {
		if err := r.reconcileRoute(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
	}

	// --- ConsoleLink (OpenShift only) ---
	if r.apiAvailable(consoleLinkGVK) && console.Status.URL != "" {
		if err := r.reconcileConsoleLink(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
	}

	// --- Update status ---
	deployedCond := r.checkDeploymentStatus(ctx)
	apimeta.SetStatusCondition(&console.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionConsoleDeployed,
		Status:             deployedCond.Status,
		ObservedGeneration: console.Generation,
		Reason:             deployedCond.Reason,
		Message:            deployedCond.Message,
	})

	console.Status.ObservedGeneration = console.Generation
	if err := r.Status().Update(ctx, &console); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("BillingConsole reconciled", "url", console.Status.URL)

	// Requeue if Route exists but hasn't been admitted yet so we pick up
	// the admitted host once the router processes it.
	if r.apiAvailable(routeGVK) {
		routeCond := apimeta.FindStatusCondition(console.Status.Conditions,
			billingv1alpha1.ConditionConsoleRouteAdmitted)
		if routeCond != nil && routeCond.Status == metav1.ConditionFalse {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *BillingConsoleReconciler) reconcileDeployment(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	replicas := int32(1)
	if console.Spec.Replicas != nil {
		replicas = *console.Spec.Replicas
	}

	image := console.Spec.Image
	if image == "" {
		image = "controller:latest"
	}

	ledgerURL := fmt.Sprintf("http://ledger-svc.%s.svc.cluster.local:8080", r.OperatorNamespace)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consoleResourceName,
			Namespace: r.OperatorNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := controllerutil.SetControllerReference(console, dep, r.Scheme); err != nil {
			return err
		}

		dep.Labels = consoleLabels()

		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: consoleSelectorLabels(),
		}
		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: consoleLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "console",
						Image: image,
						Env: []corev1.EnvVar{
							{Name: "LEDGER_URL", Value: ledgerURL},
							{Name: "LISTEN_ADDR", Value: ":9090"},
						},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: int32(consoleListenPort), Protocol: corev1.ProtocolTCP},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("http"),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       10,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("http"),
								},
							},
							InitialDelaySeconds: 3,
							PeriodSeconds:       5,
						},
					},
				},
			},
		}
		return nil
	})
	return err
}

func (r *BillingConsoleReconciler) reconcileService(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consoleResourceName,
			Namespace: r.OperatorNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(console, svc, r.Scheme); err != nil {
			return err
		}
		svc.Labels = consoleLabels()
		svc.Spec.Selector = consoleSelectorLabels()
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "http",
				Port:       int32(consoleListenPort),
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		return nil
	})
	return err
}

func (r *BillingConsoleReconciler) reconcileRoute(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	route.SetName(consoleResourceName)
	route.SetNamespace(r.OperatorNamespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if err := controllerutil.SetControllerReference(console, route, r.Scheme); err != nil {
			return err
		}
		route.SetLabels(consoleLabels())

		spec := map[string]interface{}{
			"to": map[string]interface{}{
				"kind":   "Service",
				"name":   consoleResourceName,
				"weight": int64(100),
			},
			"port": map[string]interface{}{
				"targetPort": "http",
			},
			"tls": map[string]interface{}{
				"termination":                   "edge",
				"insecureEdgeTerminationPolicy": "Redirect",
			},
		}

		if console.Spec.Route != nil && console.Spec.Route.Host != "" {
			spec["host"] = console.Spec.Route.Host
		}

		if err := unstructured.SetNestedMap(route.Object, spec, "spec"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Read admitted host from Route status to set console URL.
	ingress, found, _ := unstructured.NestedSlice(route.Object, "status", "ingress")
	if found && len(ingress) > 0 {
		if first, ok := ingress[0].(map[string]interface{}); ok {
			if host, ok := first["host"].(string); ok && host != "" {
				console.Status.URL = "https://" + host

				apimeta.SetStatusCondition(&console.Status.Conditions, metav1.Condition{
					Type:               billingv1alpha1.ConditionConsoleRouteAdmitted,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: console.Generation,
					Reason:             "Admitted",
					Message:            fmt.Sprintf("Route admitted at %s", host),
				})
				return nil
			}
		}
	}

	apimeta.SetStatusCondition(&console.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionConsoleRouteAdmitted,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: console.Generation,
		Reason:             "Pending",
		Message:            "Waiting for Route to be admitted",
	})
	return nil
}

func (r *BillingConsoleReconciler) reconcileConsoleLink(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	cl := &unstructured.Unstructured{}
	cl.SetGroupVersionKind(consoleLinkGVK)
	cl.SetName(consoleResourceName)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cl, func() error {
		if err := controllerutil.SetControllerReference(console, cl, r.Scheme); err != nil {
			return err
		}
		cl.SetLabels(consoleLabels())

		spec := map[string]interface{}{
			"href":     console.Status.URL,
			"location": "ApplicationMenu",
			"text":     "Billing Console",
			"applicationMenu": map[string]interface{}{
				"section":  "Platform Billing",
				"imageURL": "",
			},
		}
		if err := unstructured.SetNestedMap(cl.Object, spec, "spec"); err != nil {
			return err
		}
		return nil
	})
	return err
}

type conditionResult struct {
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

func (r *BillingConsoleReconciler) checkDeploymentStatus(ctx context.Context) conditionResult {
	var dep appsv1.Deployment
	key := client.ObjectKey{Name: consoleResourceName, Namespace: r.OperatorNamespace}
	if err := r.Get(ctx, key, &dep); err != nil {
		return conditionResult{
			Status:  metav1.ConditionFalse,
			Reason:  "DeploymentNotFound",
			Message: "Console Deployment not found",
		}
	}

	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
			return conditionResult{
				Status:  metav1.ConditionTrue,
				Reason:  "Available",
				Message: "Console Deployment is available",
			}
		}
	}

	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	return conditionResult{
		Status:  metav1.ConditionFalse,
		Reason:  "Progressing",
		Message: fmt.Sprintf("Console Deployment has %d/%d replicas available", dep.Status.AvailableReplicas, desired),
	}
}

func (r *BillingConsoleReconciler) deleteIfExists(ctx context.Context, obj client.Object) {
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		log.FromContext(ctx).Error(err, "failed to delete resource",
			"kind", obj.GetObjectKind().GroupVersionKind().Kind,
			"name", obj.GetName())
	}
}

func (r *BillingConsoleReconciler) apiAvailable(gvk schema.GroupVersionKind) bool {
	_, err := r.RESTMapper().RESTMapping(
		schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind},
		gvk.Version,
	)
	return err == nil
}

func consoleLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "billing-console",
		"app.kubernetes.io/component":  "console",
		"app.kubernetes.io/managed-by": "platform-billing-operator",
	}
}

func consoleSelectorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": "billing-console",
	}
}

// SetupWithManager registers the BillingConsoleReconciler with the controller manager.
func (r *BillingConsoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.BillingConsole{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("billingconsole").
		Complete(r)
}
