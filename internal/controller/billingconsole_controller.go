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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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
	oauthProxyImage     = "registry.redhat.io/openshift4/ose-oauth-proxy:latest"
	oauthProxyPort      = 4180
	consoleSAName       = "billing-console"
	consoleCookieSecret = "billing-console-oauth"
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
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

	// --- Auth mode ---
	authMode := ""
	if console.Spec.Auth != nil {
		authMode = console.Spec.Auth.Mode
	}

	if console.Spec.Auth != nil && console.Spec.Auth.Broker != nil && console.Spec.Auth.Broker.Deploy {
		logger.Info("spec.auth.broker.deploy is not supported in v1; use an external identity broker via spec.auth.broker.existingIssuerURL")
	}

	enabled := console.Spec.Enabled == nil || *console.Spec.Enabled

	if !enabled {
		r.deleteIfExists(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: consoleResourceName, Namespace: r.OperatorNamespace,
		}})
		r.deleteIfExists(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: consoleResourceName, Namespace: r.OperatorNamespace,
		}})
		r.deleteIfExists(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name: consoleSAName, Namespace: r.OperatorNamespace,
		}})
		r.deleteIfExists(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: consoleCookieSecret, Namespace: r.OperatorNamespace,
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

	// --- OpenShiftOAuth resources (SA + cookie Secret) ---
	if authMode == billingv1alpha1.AuthModeOpenShiftOAuth {
		if err := r.reconcileServiceAccount(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.reconcileCookieSecret(ctx, &console); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		r.deleteIfExists(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name: consoleSAName, Namespace: r.OperatorNamespace,
		}})
		r.deleteIfExists(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: consoleCookieSecret, Namespace: r.OperatorNamespace,
		}})
	}

	// --- Auth validation ---
	r.setAuthCondition(ctx, &console, authMode)

	// If auth is configured but invalid, serve unauthenticated rather
	// than injecting partial env vars that crash-loop the BFF.
	effectiveAuthMode := authMode
	authCond := apimeta.FindStatusCondition(console.Status.Conditions,
		billingv1alpha1.ConditionAuthConfigured)
	if authCond != nil && authCond.Status == metav1.ConditionFalse && authMode != "" {
		logger.Info("auth config incomplete, serving unauthenticated until resolved",
			"reason", authCond.Reason, "message", authCond.Message)
		effectiveAuthMode = ""
	}

	// --- Deployment ---
	if err := r.reconcileDeployment(ctx, &console, effectiveAuthMode); err != nil {
		return ctrl.Result{}, err
	}

	// --- Service ---
	if err := r.reconcileService(ctx, &console, effectiveAuthMode); err != nil {
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

func (r *BillingConsoleReconciler) reconcileDeployment(ctx context.Context, console *billingv1alpha1.BillingConsole, authMode string) error {
	replicas := int32(1)
	if console.Spec.Replicas != nil {
		replicas = *console.Spec.Replicas
	}

	image := console.Spec.Image
	if image == "" {
		image = "controller:latest"
	}

	ledgerURL := fmt.Sprintf("http://ledger-svc.%s.svc.cluster.local:8080", r.OperatorNamespace)

	envVars := []corev1.EnvVar{
		{Name: "LEDGER_URL", Value: ledgerURL},
		{Name: "LISTEN_ADDR", Value: ":9090"},
	}
	envVars = append(envVars, r.authEnvVars(console, authMode)...)

	consoleContainer := corev1.Container{
		Name:  "console",
		Image: image,
		Env:   envVars,
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
	}

	containers := []corev1.Container{consoleContainer}
	var volumes []corev1.Volume
	saName := ""

	if authMode == billingv1alpha1.AuthModeOpenShiftOAuth {
		containers = append(containers, corev1.Container{
			Name:  "oauth-proxy",
			Image: oauthProxyImage,
			Args: []string{
				"--https-address=",
				fmt.Sprintf("--http-address=:%d", oauthProxyPort),
				"--upstream=http://localhost:9090",
				"--provider=openshift",
				fmt.Sprintf("--openshift-service-account=%s", consoleSAName),
				"--cookie-secret-file=/etc/oauth/cookie-secret",
				"--pass-user-headers=true",
				"--email-domain=*",
			},
			Ports: []corev1.ContainerPort{
				{Name: "oauth-proxy", ContainerPort: int32(oauthProxyPort), Protocol: corev1.ProtocolTCP},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "oauth-cookie", MountPath: "/etc/oauth", ReadOnly: true},
			},
		})
		volumes = []corev1.Volume{
			{
				Name: "oauth-cookie",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: consoleCookieSecret,
					},
				},
			},
		}
		saName = consoleSAName
	}

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
				Containers:         containers,
				Volumes:            volumes,
				ServiceAccountName: saName,
			},
		}
		return nil
	})
	return err
}

func (r *BillingConsoleReconciler) authEnvVars(console *billingv1alpha1.BillingConsole, authMode string) []corev1.EnvVar {
	if authMode == "" {
		return nil
	}

	var envVars []corev1.EnvVar

	switch authMode {
	case billingv1alpha1.AuthModeOpenShiftOAuth:
		envVars = append(envVars, corev1.EnvVar{Name: "AUTH_MODE", Value: "openshift-oauth"})

	case billingv1alpha1.AuthModeOIDC, billingv1alpha1.AuthModeBrokered:
		issuerURL := ""
		if authMode == billingv1alpha1.AuthModeOIDC && console.Spec.Auth.OIDC != nil {
			issuerURL = console.Spec.Auth.OIDC.IssuerURL
		} else if authMode == billingv1alpha1.AuthModeBrokered && console.Spec.Auth.Broker != nil {
			issuerURL = console.Spec.Auth.Broker.ExistingIssuerURL
		}

		groupsClaim := "groups"
		if console.Spec.Auth.OIDC != nil && console.Spec.Auth.OIDC.GroupsClaim != "" {
			groupsClaim = console.Spec.Auth.OIDC.GroupsClaim
		}

		envVars = append(envVars,
			corev1.EnvVar{Name: "AUTH_MODE", Value: "oidc"},
			corev1.EnvVar{Name: "OIDC_ISSUER_URL", Value: issuerURL},
			corev1.EnvVar{Name: "OIDC_GROUPS_CLAIM", Value: groupsClaim},
		)

		if console.Spec.Auth.OIDC != nil && console.Spec.Auth.OIDC.ClientSecretRef.Name != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name: "OIDC_CLIENT_ID",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: console.Spec.Auth.OIDC.ClientSecretRef.Name,
						},
						Key: "client-id",
					},
				},
			})
		}
	}

	envVars = append(envVars, r.roleEnvVars(console)...)
	return envVars
}

func (r *BillingConsoleReconciler) roleEnvVars(console *billingv1alpha1.BillingConsole) []corev1.EnvVar {
	if console.Spec.Auth == nil || console.Spec.Auth.Roles == nil {
		return nil
	}
	var envVars []corev1.EnvVar
	roles := console.Spec.Auth.Roles
	if len(roles.Admin) > 0 {
		envVars = append(envVars, corev1.EnvVar{Name: "CONSOLE_ROLES_ADMIN", Value: strings.Join(roles.Admin, ",")})
	}
	if len(roles.Operator) > 0 {
		envVars = append(envVars, corev1.EnvVar{Name: "CONSOLE_ROLES_OPERATOR", Value: strings.Join(roles.Operator, ",")})
	}
	if len(roles.Viewer) > 0 {
		envVars = append(envVars, corev1.EnvVar{Name: "CONSOLE_ROLES_VIEWER", Value: strings.Join(roles.Viewer, ",")})
	}
	return envVars
}

func (r *BillingConsoleReconciler) reconcileService(ctx context.Context, console *billingv1alpha1.BillingConsole, authMode string) error {
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

		targetPort := intstr.FromString("http")
		if authMode == billingv1alpha1.AuthModeOpenShiftOAuth {
			targetPort = intstr.FromString("oauth-proxy")
		}

		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "http",
				Port:       int32(consoleListenPort),
				TargetPort: targetPort,
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

func (r *BillingConsoleReconciler) reconcileServiceAccount(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consoleSAName,
			Namespace: r.OperatorNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if err := controllerutil.SetControllerReference(console, sa, r.Scheme); err != nil {
			return err
		}
		sa.Labels = consoleLabels()
		redirectRef := fmt.Sprintf(`{"kind":"OAuthRedirectReference","apiVersion":"v1","reference":{"kind":"Route","name":"%s"}}`, consoleResourceName)
		if sa.Annotations == nil {
			sa.Annotations = map[string]string{}
		}
		sa.Annotations["serviceaccounts.openshift.io/oauth-redirectreference.primary"] = redirectRef
		return nil
	})
	return err
}

func (r *BillingConsoleReconciler) reconcileCookieSecret(ctx context.Context, console *billingv1alpha1.BillingConsole) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consoleCookieSecret,
			Namespace: r.OperatorNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(console, secret, r.Scheme); err != nil {
			return err
		}
		secret.Labels = consoleLabels()
		if secret.Data == nil || len(secret.Data["cookie-secret"]) == 0 {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return fmt.Errorf("generating cookie secret: %w", err)
			}
			secret.Data = map[string][]byte{
				"cookie-secret": []byte(base64.StdEncoding.EncodeToString(b)),
			}
		}
		return nil
	})
	return err
}

func (r *BillingConsoleReconciler) setAuthCondition(ctx context.Context, console *billingv1alpha1.BillingConsole, authMode string) {
	cond := metav1.Condition{
		Type:               billingv1alpha1.ConditionAuthConfigured,
		ObservedGeneration: console.Generation,
	}

	switch authMode {
	case "":
		cond.Status = metav1.ConditionFalse
		cond.Reason = "NotConfigured"
		cond.Message = "No authentication configured; console serves unauthenticated"

	case billingv1alpha1.AuthModeOpenShiftOAuth:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Configured"
		cond.Message = "OpenShift OAuth proxy configured"

	case billingv1alpha1.AuthModeOIDC:
		if console.Spec.Auth.OIDC == nil || console.Spec.Auth.OIDC.IssuerURL == "" {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "IncompleteConfig"
			cond.Message = "spec.auth.oidc.issuerURL is required for OIDC mode"
		} else if console.Spec.Auth.OIDC.ClientSecretRef.Name == "" {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "IncompleteConfig"
			cond.Message = "spec.auth.oidc.clientSecretRef is required for OIDC mode"
		} else if !r.secretExists(ctx, console.Spec.Auth.OIDC.ClientSecretRef.Name) {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "SecretNotFound"
			cond.Message = fmt.Sprintf("Secret %q not found", console.Spec.Auth.OIDC.ClientSecretRef.Name)
		} else {
			cond.Status = metav1.ConditionTrue
			cond.Reason = "Configured"
			cond.Message = fmt.Sprintf("OIDC configured with issuer %s", console.Spec.Auth.OIDC.IssuerURL)
		}

	case billingv1alpha1.AuthModeBrokered:
		if console.Spec.Auth.Broker == nil || console.Spec.Auth.Broker.ExistingIssuerURL == "" {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "IncompleteConfig"
			cond.Message = "spec.auth.broker.existingIssuerURL is required for Brokered mode"
		} else if console.Spec.Auth.OIDC == nil || console.Spec.Auth.OIDC.ClientSecretRef.Name == "" {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "IncompleteConfig"
			cond.Message = "spec.auth.oidc.clientSecretRef is required for Brokered mode (reuses OIDC client credentials)"
		} else if !r.secretExists(ctx, console.Spec.Auth.OIDC.ClientSecretRef.Name) {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "SecretNotFound"
			cond.Message = fmt.Sprintf("Secret %q not found", console.Spec.Auth.OIDC.ClientSecretRef.Name)
		} else {
			cond.Status = metav1.ConditionTrue
			cond.Reason = "Configured"
			cond.Message = fmt.Sprintf("Brokered auth configured with issuer %s", console.Spec.Auth.Broker.ExistingIssuerURL)
		}
	}

	apimeta.SetStatusCondition(&console.Status.Conditions, cond)
}

func (r *BillingConsoleReconciler) secretExists(ctx context.Context, name string) bool {
	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: r.OperatorNamespace}, &secret)
	return err == nil
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
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Secret{}).
		Named("billingconsole").
		Complete(r)
}
