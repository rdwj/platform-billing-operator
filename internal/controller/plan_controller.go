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
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	kuadrantv1alpha1 "github.com/kuadrant/kuadrant-operator/api/v1alpha1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const (
	finalizerName        = "billing.opendatahub.io/policy-compiler"
	fieldManager         = "platform-billing-operator"
	labelManagedBy       = "billing.opendatahub.io/managed-by"
	annotationQueueClass = "billing.opendatahub.io/queue-class"

	requeueNotReady = 5 * time.Minute
	requeueResync   = 10 * time.Minute
)

var (
	trlpGVK       = schema.GroupVersionKind{Group: "kuadrant.io", Version: "v1alpha1", Kind: "TokenRateLimitPolicy"}
	rlpGVK        = schema.GroupVersionKind{Group: "kuadrant.io", Version: "v1", Kind: "RateLimitPolicy"}
	authPolicyGVK = schema.GroupVersionKind{Group: "kuadrant.io", Version: "v1", Kind: "AuthPolicy"}
)

// PlanReconciler reconciles Plan CRs by compiling them into Kuadrant enforcement
// CRs (TokenRateLimitPolicy, RateLimitPolicy, AuthPolicy) via server-side apply.
//
// NOTE: The Kuadrant type schemes (kuadrantv1.AddToScheme, kuadrantv1alpha1.AddToScheme)
// must be registered in the manager's scheme for typed SSA to work. All runtime
// operations (apply, drift detection, adoption check) use unstructured objects to
// avoid a hard dependency on scheme registration for the Kuadrant types at the
// controller level.
type PlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=plans,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=plans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=plans/finalizers,verbs=update
// +kubebuilder:rbac:groups=kuadrant.io,resources=tokenratelimitpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=ratelimitpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch

// Reconcile compiles a Plan into Kuadrant enforcement CRs and keeps them in sync.
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// --- Fetch the Plan ---
	var plan billingv1alpha1.Plan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// --- Handle deletion ---
	if !plan.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &plan)
	}

	// --- Ensure finalizer ---
	if !controllerutil.ContainsFinalizer(&plan, finalizerName) {
		controllerutil.AddFinalizer(&plan, finalizerName)
		if err := r.Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
	}

	// --- Check Kuadrant CRD installation ---
	if !r.kuadrantCRDsInstalled() {
		apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionKuadrantNotInstalled,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: plan.Generation,
			Reason:             "CRDsMissing",
			Message:            "TokenRateLimitPolicy CRD not found; install Kuadrant operator",
		})
		plan.Status.ObservedGeneration = plan.Generation
		if err := r.Status().Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueNotReady}, nil
	}
	apimeta.RemoveStatusCondition(&plan.Status.Conditions, billingv1alpha1.ConditionKuadrantNotInstalled)

	// --- Check gateway existence ---
	if !r.gatewayExists(ctx, &plan) {
		apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionGatewayNotFound,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: plan.Generation,
			Reason:             "TargetNotFound",
			Message:            fmt.Sprintf("Gateway %s not found in namespace %s", plan.Spec.TargetRef.Name, plan.Namespace),
		})
		plan.Status.ObservedGeneration = plan.Generation
		if err := r.Status().Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueNotReady}, nil
	}
	apimeta.RemoveStatusCondition(&plan.Status.Conditions, billingv1alpha1.ConditionGatewayNotFound)

	// --- Set advisory conditions for unsupported fields ---
	r.setUnsupportedFieldConditions(&plan)

	// --- Compile and apply enforcement CRs ---
	var (
		compiledPolicies []billingv1alpha1.CompiledPolicyRef
		driftMessages    []string
	)

	// TRLP: token-based rate limits.
	if trlp := r.buildDesiredTRLP(&plan); trlp != nil {
		u, err := typedToUnstructured(trlp, trlpGVK)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("converting TRLP to unstructured: %w", err)
		}
		ref, driftMsg, err := r.reconcileCompiledCR(ctx, &plan, u, "limits", "targetRef")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling TRLP: %w", err)
		}
		if ref == nil {
			// Adoption conflict; condition already set. Requeue.
			plan.Status.ObservedGeneration = plan.Generation
			_ = r.Status().Update(ctx, &plan)
			return ctrl.Result{RequeueAfter: requeueNotReady}, nil
		}
		compiledPolicies = append(compiledPolicies, *ref)
		if driftMsg != "" {
			driftMessages = append(driftMessages, driftMsg)
		}
	} else {
		if err := r.deleteIfExists(ctx, plan.Name+"-trlp", plan.Namespace, trlpGVK); err != nil {
			return ctrl.Result{}, err
		}
	}

	// RLP: request-based rate limits.
	if rlp := r.buildDesiredRLP(&plan); rlp != nil {
		u, err := typedToUnstructured(rlp, rlpGVK)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("converting RLP to unstructured: %w", err)
		}
		ref, driftMsg, err := r.reconcileCompiledCR(ctx, &plan, u, "limits", "targetRef")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling RLP: %w", err)
		}
		if ref == nil {
			plan.Status.ObservedGeneration = plan.Generation
			_ = r.Status().Update(ctx, &plan)
			return ctrl.Result{RequeueAfter: requeueNotReady}, nil
		}
		compiledPolicies = append(compiledPolicies, *ref)
		if driftMsg != "" {
			driftMessages = append(driftMessages, driftMsg)
		}
	} else {
		if err := r.deleteIfExists(ctx, plan.Name+"-rlp", plan.Namespace, rlpGVK); err != nil {
			return ctrl.Result{}, err
		}
	}

	// AuthPolicy: model-scope predicates.
	// Built as unstructured directly because the Authorino types used by Kuadrant's
	// AuthPolicy are complex to construct from outside the Kuadrant module. A typed
	// builder can be introduced when a live auth pipeline is available for testing.
	if authPolicy := r.buildDesiredAuthPolicy(&plan); authPolicy != nil {
		ref, driftMsg, err := r.reconcileCompiledCR(ctx, &plan, authPolicy, "targetRef", "when", "rules")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling AuthPolicy: %w", err)
		}
		if ref == nil {
			plan.Status.ObservedGeneration = plan.Generation
			_ = r.Status().Update(ctx, &plan)
			return ctrl.Result{RequeueAfter: requeueNotReady}, nil
		}
		compiledPolicies = append(compiledPolicies, *ref)
		if driftMsg != "" {
			driftMessages = append(driftMessages, driftMsg)
		}
	} else {
		if err := r.deleteIfExists(ctx, plan.Name+"-auth", plan.Namespace, authPolicyGVK); err != nil {
			return ctrl.Result{}, err
		}
	}

	// If we reached here without conflicts, clear any stale AdoptionConflict condition.
	apimeta.RemoveStatusCondition(&plan.Status.Conditions, billingv1alpha1.ConditionAdoptionConflict)

	// --- Drift condition ---
	if len(driftMessages) > 0 {
		apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionDrifted,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: plan.Generation,
			Reason:             "DriftDetected",
			Message:            strings.Join(driftMessages, "; "),
		})
	} else {
		apimeta.RemoveStatusCondition(&plan.Status.Conditions, billingv1alpha1.ConditionDrifted)
	}

	// --- Compiled condition ---
	apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionCompiled,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: plan.Generation,
		Reason:             "Compiled",
		Message:            fmt.Sprintf("Compiled %d enforcement policies", len(compiledPolicies)),
	})

	// --- Final status update ---
	plan.Status.CompiledPolicies = compiledPolicies
	plan.Status.ObservedGeneration = plan.Generation
	if err := r.Status().Update(ctx, &plan); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Plan reconciled", "compiledPolicies", len(compiledPolicies))
	return ctrl.Result{RequeueAfter: requeueResync}, nil
}

// handleDeletion removes managed enforcement CRs and strips the finalizer.
func (r *PlanReconciler) handleDeletion(ctx context.Context, plan *billingv1alpha1.Plan) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(plan, finalizerName) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("Cleaning up managed enforcement CRs")

	// Delete all possible managed CRs (belt-and-suspenders with ownerRef cascade).
	for _, cr := range []struct {
		name string
		gvk  schema.GroupVersionKind
	}{
		{plan.Name + "-trlp", trlpGVK},
		{plan.Name + "-rlp", rlpGVK},
		{plan.Name + "-auth", authPolicyGVK},
	} {
		if err := r.deleteIfExists(ctx, cr.name, plan.Namespace, cr.gvk); err != nil {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(plan, finalizerName)
	if err := r.Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileCompiledCR checks for adoption conflicts, applies via SSA, and detects
// drift on a single compiled CR. Returns nil ref (without error) on adoption conflict.
func (r *PlanReconciler) reconcileCompiledCR(
	ctx context.Context,
	plan *billingv1alpha1.Plan,
	desired *unstructured.Unstructured,
	driftKeys ...string,
) (*billingv1alpha1.CompiledPolicyRef, string, error) {
	logger := log.FromContext(ctx)

	// Adoption conflict check: reject if a same-name CR exists without our label.
	conflict, err := r.checkAdoptionConflict(ctx, desired, plan.Name)
	if err != nil {
		return nil, "", err
	}
	if conflict {
		apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionAdoptionConflict,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: plan.Generation,
			Reason:             "ConflictingResource",
			Message: fmt.Sprintf("%s %s/%s exists but is not managed by Plan %s",
				desired.GetKind(), desired.GetNamespace(), desired.GetName(), plan.Name),
		})
		return nil, "", nil
	}

	// Apply via server-side apply.
	if err := r.applySSA(ctx, desired); err != nil {
		return nil, "", fmt.Errorf("SSA apply %s/%s: %w", desired.GetKind(), desired.GetName(), err)
	}

	// Drift detection: read back the live CR and compare managed spec fields.
	drifted, driftMsg := r.detectDrift(ctx, desired, driftKeys...)
	if drifted {
		logger.Info("Drift detected, re-stamping", "cr", desired.GetName(), "drift", driftMsg)
		if err := r.applySSA(ctx, desired); err != nil {
			logger.Error(err, "Re-stamp failed", "cr", desired.GetName())
		}
	}

	gvk := desired.GroupVersionKind()
	return &billingv1alpha1.CompiledPolicyRef{
		Group:     gvk.Group,
		Kind:      gvk.Kind,
		Name:      desired.GetName(),
		Namespace: desired.GetNamespace(),
		UpToDate:  !drifted,
	}, driftMsg, nil
}

// ---------------------------------------------------------------------------
// Compilation: typed builders
// ---------------------------------------------------------------------------

// buildDesiredTRLP produces a TokenRateLimitPolicy from Plan token limits.
// Returns nil if no token limits are configured.
func (r *PlanReconciler) buildDesiredTRLP(plan *billingv1alpha1.Plan) *kuadrantv1alpha1.TokenRateLimitPolicy {
	limits := make(map[string]kuadrantv1alpha1.TokenLimit)

	if plan.Spec.Limits.TokensPerMinute != nil && *plan.Spec.Limits.TokensPerMinute > 0 {
		limits["tokens-per-minute"] = kuadrantv1alpha1.TokenLimit{
			Rates: []kuadrantv1.Rate{{
				Limit:  int(*plan.Spec.Limits.TokensPerMinute),
				Window: kuadrantv1.Duration("1m"),
			}},
			Counters: []kuadrantv1.Counter{{
				Expression: kuadrantv1.Expression("auth.identity.sub"),
			}},
		}
	}

	if plan.Spec.Limits.TokensPerDay != nil && *plan.Spec.Limits.TokensPerDay > 0 {
		limits["tokens-per-day"] = kuadrantv1alpha1.TokenLimit{
			Rates: []kuadrantv1.Rate{{
				Limit:  int(*plan.Spec.Limits.TokensPerDay),
				Window: kuadrantv1.Duration("24h"),
			}},
			Counters: []kuadrantv1.Counter{{
				Expression: kuadrantv1.Expression("auth.identity.sub"),
			}},
		}
	}

	if len(limits) == 0 {
		return nil
	}

	trlp := &kuadrantv1alpha1.TokenRateLimitPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kuadrantv1alpha1.GroupVersion.String(),
			Kind:       "TokenRateLimitPolicy",
		},
		ObjectMeta: commonObjectMeta(plan, plan.Name+"-trlp"),
		Spec: kuadrantv1alpha1.TokenRateLimitPolicySpec{
			TargetRef: plan.Spec.TargetRef,
			TokenRateLimitPolicySpecProper: kuadrantv1alpha1.TokenRateLimitPolicySpecProper{
				Limits: limits,
			},
		},
	}
	return trlp
}

// buildDesiredRLP produces a RateLimitPolicy from Plan request limits.
// Returns nil if requestsPerMinute is not configured.
func (r *PlanReconciler) buildDesiredRLP(plan *billingv1alpha1.Plan) *kuadrantv1.RateLimitPolicy {
	if plan.Spec.Limits.RequestsPerMinute == nil || *plan.Spec.Limits.RequestsPerMinute <= 0 {
		return nil
	}

	rlp := &kuadrantv1.RateLimitPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kuadrantv1.GroupVersion.String(),
			Kind:       "RateLimitPolicy",
		},
		ObjectMeta: commonObjectMeta(plan, plan.Name+"-rlp"),
		Spec: kuadrantv1.RateLimitPolicySpec{
			TargetRef: plan.Spec.TargetRef,
			RateLimitPolicySpecProper: kuadrantv1.RateLimitPolicySpecProper{
				Limits: map[string]kuadrantv1.Limit{
					"requests-per-minute": {
						Rates: []kuadrantv1.Rate{{
							Limit:  int(*plan.Spec.Limits.RequestsPerMinute),
							Window: kuadrantv1.Duration("1m"),
						}},
						Counters: []kuadrantv1.Counter{{
							Expression: kuadrantv1.Expression("auth.identity.sub"),
						}},
					},
				},
			},
		},
	}
	return rlp
}

// buildDesiredAuthPolicy produces an unstructured AuthPolicy from model scopes.
// Returns nil if modelScopes is empty.
//
// Uses unstructured construction because Kuadrant's AuthPolicy depends on
// Authorino v1beta3 types whose struct graph is complex to construct outside
// the Kuadrant module. A typed builder can replace this once a live auth
// pipeline is available for integration testing.
func (r *PlanReconciler) buildDesiredAuthPolicy(plan *billingv1alpha1.Plan) *unstructured.Unstructured {
	if len(plan.Spec.ModelScopes) == 0 {
		return nil
	}

	// Build a single CEL predicate that OR's all model-scope glob patterns.
	parts := make([]string, 0, len(plan.Spec.ModelScopes))
	for _, glob := range plan.Spec.ModelScopes {
		parts = append(parts, fmt.Sprintf("auth.identity.model.matches('%s')", globToRegex(glob)))
	}
	celPredicate := strings.Join(parts, " || ")

	targetRef := map[string]interface{}{
		"group": string(plan.Spec.TargetRef.Group),
		"kind":  string(plan.Spec.TargetRef.Kind),
		"name":  string(plan.Spec.TargetRef.Name),
	}
	if plan.Spec.TargetRef.SectionName != nil {
		targetRef["sectionName"] = string(*plan.Spec.TargetRef.SectionName)
	}

	labels := map[string]interface{}{
		labelManagedBy: plan.Name,
	}
	annotations := map[string]interface{}{}
	if plan.Spec.QueueClass != "" {
		annotations[annotationQueueClass] = plan.Spec.QueueClass
	}

	metadata := map[string]interface{}{
		"name":      plan.Name + "-auth",
		"namespace": plan.Namespace,
		"labels":    labels,
		"ownerReferences": []interface{}{
			ownerRefMap(plan),
		},
	}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kuadrant.io/v1",
			"kind":       "AuthPolicy",
			"metadata":   metadata,
			"spec": map[string]interface{}{
				"targetRef": targetRef,
				"when": []interface{}{
					map[string]interface{}{
						"predicate": celPredicate,
					},
				},
				// Kuadrant requires at least one auth rule. Use anonymous access
				// as a passthrough -- the real auth pipeline is configured separately.
				"rules": map[string]interface{}{
					"authentication": map[string]interface{}{
						"anonymous": map[string]interface{}{
							"anonymous": map[string]interface{}{},
						},
					},
				},
			},
		},
	}
	return obj
}

// ---------------------------------------------------------------------------
// Infrastructure checks
// ---------------------------------------------------------------------------

// kuadrantCRDsInstalled returns true if the TokenRateLimitPolicy CRD is registered
// in the cluster's API discovery. Uses the REST mapper (refreshed periodically by
// controller-runtime's DynamicRESTMapper).
func (r *PlanReconciler) kuadrantCRDsInstalled() bool {
	_, err := r.RESTMapper().RESTMapping(
		schema.GroupKind{Group: trlpGVK.Group, Kind: trlpGVK.Kind},
		trlpGVK.Version,
	)
	return err == nil
}

// gatewayExists returns true if the Gateway (or HTTPRoute) referenced by the Plan's
// targetRef can be found in the same namespace.
func (r *PlanReconciler) gatewayExists(ctx context.Context, plan *billingv1alpha1.Plan) bool {
	gk := schema.GroupKind{
		Group: string(plan.Spec.TargetRef.Group),
		Kind:  string(plan.Spec.TargetRef.Kind),
	}
	mapping, err := r.RESTMapper().RESTMapping(gk)
	if err != nil {
		return false
	}
	gw := &unstructured.Unstructured{}
	gw.SetGroupVersionKind(mapping.GroupVersionKind)
	err = r.Get(ctx, types.NamespacedName{
		Name:      string(plan.Spec.TargetRef.Name),
		Namespace: plan.Namespace,
	}, gw)
	return err == nil
}

// ---------------------------------------------------------------------------
// SSA, adoption, drift, cleanup
// ---------------------------------------------------------------------------

// applySSA applies the unstructured object via server-side apply with force ownership.
func (r *PlanReconciler) applySSA(ctx context.Context, obj *unstructured.Unstructured) error {
	return r.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership)
}

// checkAdoptionConflict returns true if a CR with the same name/namespace/GVK
// exists but does not carry the managed-by label for this Plan.
func (r *PlanReconciler) checkAdoptionConflict(ctx context.Context, desired *unstructured.Unstructured, planName string) (bool, error) {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err := r.Get(ctx, types.NamespacedName{
		Name:      desired.GetName(),
		Namespace: desired.GetNamespace(),
	}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	labels := existing.GetLabels()
	if labels == nil || labels[labelManagedBy] != planName {
		return true, nil
	}
	return false, nil
}

// deleteIfExists removes an unstructured object by GVK/name/namespace,
// ignoring NotFound errors.
func (r *PlanReconciler) deleteIfExists(ctx context.Context, name, namespace string, gvk schema.GroupVersionKind) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// detectDrift reads back the live CR after SSA and compares specific spec fields
// against the desired state. Returns true with a summary message if drift is found.
func (r *PlanReconciler) detectDrift(ctx context.Context, desired *unstructured.Unstructured, fields ...string) (bool, string) {
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(desired.GroupVersionKind())
	if err := r.Get(ctx, types.NamespacedName{
		Name:      desired.GetName(),
		Namespace: desired.GetNamespace(),
	}, live); err != nil {
		// Cannot read live state; skip drift detection this cycle.
		return false, ""
	}

	desiredSpec := nestedMap(desired.Object, "spec")
	liveSpec := nestedMap(live.Object, "spec")

	var drifted []string
	for _, field := range fields {
		if !reflect.DeepEqual(desiredSpec[field], liveSpec[field]) {
			drifted = append(drifted, fmt.Sprintf("spec.%s", field))
		}
	}
	if len(drifted) > 0 {
		return true, fmt.Sprintf("%s/%s: %s drifted", desired.GetKind(), desired.GetName(), strings.Join(drifted, ", "))
	}
	return false, ""
}

// ---------------------------------------------------------------------------
// Unsupported field conditions
// ---------------------------------------------------------------------------

// setUnsupportedFieldConditions sets or removes the UnsupportedField condition
// based on whether the Plan contains fields that cannot be compiled to Kuadrant CRs.
func (r *PlanReconciler) setUnsupportedFieldConditions(plan *billingv1alpha1.Plan) {
	var messages []string

	if plan.Spec.Limits.MaxConcurrency != nil && *plan.Spec.Limits.MaxConcurrency > 0 {
		messages = append(messages,
			"maxConcurrency: no Kuadrant concurrency primitive; field stored but not enforced")
	}

	if hasNonWildcardGeo(plan.Spec.GeoScopes) {
		messages = append(messages,
			"geoScopes: non-wildcard geo scoping not compilable; field stored but not enforced")
	}

	if len(messages) > 0 {
		apimeta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionUnsupportedField,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: plan.Generation,
			Reason:             "UnsupportedFields",
			Message:            strings.Join(messages, "; "),
		})
	} else {
		apimeta.RemoveStatusCondition(&plan.Status.Conditions, billingv1alpha1.ConditionUnsupportedField)
	}
}

func hasNonWildcardGeo(scopes []string) bool {
	for _, s := range scopes {
		if s != "*" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// typedToUnstructured converts a typed Kubernetes object to unstructured via
// JSON round-trip. The GVK is set explicitly to ensure SSA payloads always
// carry apiVersion/kind regardless of whether TypeMeta was populated.
func typedToUnstructured(obj interface{}, gvk schema.GroupVersionKind) (*unstructured.Unstructured, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var content map[string]interface{}
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("unmarshal to map: %w", err)
	}

	content["apiVersion"] = gvk.GroupVersion().String()
	content["kind"] = gvk.Kind

	// Strip zero-value creationTimestamp injected by metav1.ObjectMeta serialization.
	if metadata, ok := content["metadata"].(map[string]interface{}); ok {
		delete(metadata, "creationTimestamp")
	}
	// Remove empty status so SSA does not claim status fields.
	if status, ok := content["status"].(map[string]interface{}); ok && len(status) == 0 {
		delete(content, "status")
	}

	return &unstructured.Unstructured{Object: content}, nil
}

// commonObjectMeta builds the ObjectMeta shared by all compiled typed CRs.
func commonObjectMeta(plan *billingv1alpha1.Plan, name string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{
		Name:      name,
		Namespace: plan.Namespace,
		Labels: map[string]string{
			labelManagedBy: plan.Name,
		},
		OwnerReferences: []metav1.OwnerReference{planOwnerReference(plan)},
	}
	if plan.Spec.QueueClass != "" {
		meta.Annotations = map[string]string{
			annotationQueueClass: plan.Spec.QueueClass,
		}
	}
	return meta
}

// planOwnerReference returns an OwnerReference pointing to the given Plan
// with controller=true and blockOwnerDeletion=true.
func planOwnerReference(plan *billingv1alpha1.Plan) metav1.OwnerReference {
	controller := true
	blockOwnerDeletion := true
	return metav1.OwnerReference{
		APIVersion:         billingv1alpha1.GroupVersion.String(),
		Kind:               "Plan",
		Name:               plan.Name,
		UID:                plan.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
}

// ownerRefMap returns an ownerReference as map[string]interface{} for unstructured CRs.
func ownerRefMap(plan *billingv1alpha1.Plan) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion":         billingv1alpha1.GroupVersion.String(),
		"kind":               "Plan",
		"name":               plan.Name,
		"uid":                string(plan.UID),
		"controller":         true,
		"blockOwnerDeletion": true,
	}
}

// globToRegex converts a simple glob pattern to an anchored regex.
// Replaces * with .* and ? with . then wraps with ^...$
func globToRegex(pattern string) string {
	// Escape regex metacharacters except * and ?
	var b strings.Builder
	b.WriteByte('^')
	for _, ch := range pattern {
		switch ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		case '.', '+', '(', ')', '[', ']', '{', '}', '\\', '^', '$', '|':
			b.WriteByte('\\')
			b.WriteRune(ch)
		default:
			b.WriteRune(ch)
		}
	}
	b.WriteByte('$')
	return b.String()
}

// nestedMap extracts a nested map from an unstructured object's data.
func nestedMap(obj map[string]interface{}, key string) map[string]interface{} {
	val, ok := obj[key]
	if !ok {
		return nil
	}
	m, ok := val.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

// ---------------------------------------------------------------------------
// Controller setup
// ---------------------------------------------------------------------------

// SetupWithManager registers the PlanReconciler with the controller manager.
//
// Only watches Plan CRs directly. Kuadrant CRs created by compilation are NOT
// watched via Owns() because that would fail at startup if Kuadrant CRDs are not
// yet installed. Instead, drift is caught via periodic resync (requeueResync).
// Dynamic watches for owned CRs can be added once Kuadrant CRD presence is
// confirmed at runtime.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Plan{}).
		Named("plan-policycompiler").
		Complete(r)
}
