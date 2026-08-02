package enforcement

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	kuadrantv1alpha1 "github.com/kuadrant/kuadrant-operator/api/v1alpha1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const labelManagedBy = "billing.opendatahub.io/managed-by"

// GVKs for Kuadrant CRs managed by the enforcement backend. Redefined here
// (rather than imported from the controller package) to avoid a circular
// dependency.
var authPolicyGVK = schema.GroupVersionKind{Group: "kuadrant.io", Version: "v1", Kind: "AuthPolicy"}

// MaaSBackend implements EnforcementBackend by writing Kuadrant
// TokenRateLimitPolicy and AuthPolicy CRs for MaaS tenants.
//
// Uses controllerutil.CreateOrUpdate rather than server-side apply because the
// controller-runtime fake client does not support SSA (ApplyPatchType). This is
// acceptable because the enforcement controller owns the entire object, not
// individual fields.
type MaaSBackend struct {
	client   client.Client
	scheme   *runtime.Scheme
	keys     KeyManager
	lastTRLP sync.Map // acct.Name -> last applied token limit (int64)
}

var _ EnforcementBackend = (*MaaSBackend)(nil)

// NewMaaSBackend returns a MaaSBackend wired to the given client and scheme.
func NewMaaSBackend(c client.Client, s *runtime.Scheme, keys KeyManager) *MaaSBackend {
	return &MaaSBackend{
		client: c,
		scheme: s,
		keys:   keys,
	}
}

// Apply updates the tenant's TRLP to reflect the current token allowance.
// A hysteresis check skips the update when the change is <=10% of the previous
// value to avoid thrashing Kuadrant reconciliation.
func (m *MaaSBackend) Apply(ctx context.Context, acct *billingv1alpha1.TenantAccount, allowance string) error {
	tokens, err := strconv.ParseInt(allowance, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing allowance %q: %w", allowance, err)
	}

	// Hysteresis: skip if the change is <=10% of the previous value.
	if prev, ok := m.lastTRLP.Load(acct.Name); ok {
		prevTokens := prev.(int64)
		if prevTokens > 0 {
			delta := tokens - prevTokens
			if delta < 0 {
				delta = -delta
			}
			if delta <= prevTokens/10 {
				return nil
			}
		}
	}

	plan, err := m.resolvePlan(ctx, acct)
	if err != nil {
		return fmt.Errorf("resolving plan for tenant %s: %w", acct.Name, err)
	}

	trlp := &kuadrantv1alpha1.TokenRateLimitPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      enforcementTRLPName(acct),
			Namespace: acct.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, m.client, trlp, func() error {
		trlp.Labels = map[string]string{
			labelManagedBy: acct.Name,
		}
		trlp.OwnerReferences = []metav1.OwnerReference{tenantOwnerReference(acct)}
		trlp.Spec = kuadrantv1alpha1.TokenRateLimitPolicySpec{
			TargetRef: plan.Spec.TargetRef,
			TokenRateLimitPolicySpecProper: kuadrantv1alpha1.TokenRateLimitPolicySpecProper{
				Limits: map[string]kuadrantv1alpha1.TokenLimit{
					"balance-allowance": {
						Rates: []kuadrantv1.Rate{{
							Limit:  int(tokens),
							Window: kuadrantv1.Duration("1m"),
						}},
						Counters: []kuadrantv1.Counter{{
							Expression: kuadrantv1.Expression("auth.identity.sub"),
						}},
					},
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("applying TRLP for tenant %s: %w", acct.Name, err)
	}

	m.lastTRLP.Store(acct.Name, tokens)
	return nil
}

// Freeze creates (or updates) a deny AuthPolicy that blocks all requests for
// the given tenant. Idempotent.
func (m *MaaSBackend) Freeze(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	plan, err := m.resolvePlan(ctx, acct)
	if err != nil {
		return fmt.Errorf("resolving plan for tenant %s: %w", acct.Name, err)
	}

	desired := m.buildDenyAuthPolicy(acct, plan)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(authPolicyGVK)
	err = m.client.Get(ctx, types.NamespacedName{
		Name:      enforcementDenyName(acct),
		Namespace: acct.Namespace,
	}, existing)

	if apierrors.IsNotFound(err) {
		if createErr := m.client.Create(ctx, desired); createErr != nil {
			return createErr
		}
	} else if err != nil {
		return fmt.Errorf("getting deny AuthPolicy for tenant %s: %w", acct.Name, err)
	} else {
		desired.SetResourceVersion(existing.GetResourceVersion())
		if updateErr := m.client.Update(ctx, desired); updateErr != nil {
			return updateErr
		}
	}

	if m.keys != nil {
		if keyErr := m.keys.DisableAllKeys(ctx, acct.Name); keyErr != nil {
			return fmt.Errorf("disabling keys for tenant %s: %w", acct.Name, keyErr)
		}
	}

	return nil
}

// Thaw removes the deny AuthPolicy, restoring the tenant's access. Idempotent:
// returns nil if no deny policy exists.
func (m *MaaSBackend) Thaw(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(authPolicyGVK)
	obj.SetName(enforcementDenyName(acct))
	obj.SetNamespace(acct.Namespace)

	if err := m.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting deny AuthPolicy for tenant %s: %w", acct.Name, err)
	}

	if m.keys != nil {
		if keyErr := m.keys.EnableAllKeys(ctx, acct.Name); keyErr != nil {
			return fmt.Errorf("enabling keys for tenant %s: %w", acct.Name, keyErr)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolvePlan reads the Plan CR referenced by the TenantAccount's PlanRef.
func (m *MaaSBackend) resolvePlan(ctx context.Context, acct *billingv1alpha1.TenantAccount) (*billingv1alpha1.Plan, error) {
	var plan billingv1alpha1.Plan
	if err := m.client.Get(ctx, types.NamespacedName{
		Name:      acct.Spec.PlanRef.Name,
		Namespace: acct.Namespace,
	}, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// buildDenyAuthPolicy constructs an unstructured AuthPolicy that denies all
// requests matching this tenant's identity. Uses unstructured construction
// because the Authorino types used by Kuadrant's AuthPolicy are complex to
// construct from outside the Kuadrant module (same rationale as
// plan_controller.go's buildDesiredAuthPolicy).
func (m *MaaSBackend) buildDenyAuthPolicy(acct *billingv1alpha1.TenantAccount, plan *billingv1alpha1.Plan) *unstructured.Unstructured {
	targetRef := map[string]interface{}{
		"group": string(plan.Spec.TargetRef.Group),
		"kind":  string(plan.Spec.TargetRef.Kind),
		"name":  string(plan.Spec.TargetRef.Name),
	}
	if plan.Spec.TargetRef.SectionName != nil {
		targetRef["sectionName"] = string(*plan.Spec.TargetRef.SectionName)
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": authPolicyGVK.GroupVersion().String(),
			"kind":       authPolicyGVK.Kind,
			"metadata": map[string]interface{}{
				"name":      enforcementDenyName(acct),
				"namespace": acct.Namespace,
				"labels": map[string]interface{}{
					labelManagedBy: acct.Name,
				},
				"ownerReferences": []interface{}{
					tenantOwnerReferenceMap(acct),
				},
			},
			"spec": map[string]interface{}{
				"targetRef": targetRef,
				"when": []interface{}{
					map[string]interface{}{
						"predicate": fmt.Sprintf("auth.identity.sub == '%s'", acct.Name),
					},
				},
				"rules": map[string]interface{}{
					"authorization": map[string]interface{}{
						"deny-frozen": map[string]interface{}{
							"opa": map[string]interface{}{
								"rego": "allow = false",
							},
						},
					},
				},
			},
		},
	}
}

// tenantOwnerReference returns a typed OwnerReference pointing to the given
// TenantAccount with controller=true and blockOwnerDeletion=true.
func tenantOwnerReference(acct *billingv1alpha1.TenantAccount) metav1.OwnerReference {
	controller := true
	blockOwnerDeletion := true
	return metav1.OwnerReference{
		APIVersion:         billingv1alpha1.GroupVersion.String(),
		Kind:               "TenantAccount",
		Name:               acct.Name,
		UID:                acct.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
}

// tenantOwnerReferenceMap returns an owner reference as map[string]interface{}
// for use in unstructured CRs.
func tenantOwnerReferenceMap(acct *billingv1alpha1.TenantAccount) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion":         billingv1alpha1.GroupVersion.String(),
		"kind":               "TenantAccount",
		"name":               acct.Name,
		"uid":                string(acct.UID),
		"controller":         true,
		"blockOwnerDeletion": true,
	}
}

func enforcementTRLPName(acct *billingv1alpha1.TenantAccount) string {
	return acct.Name + "-enforcement-trlp"
}

func enforcementDenyName(acct *billingv1alpha1.TenantAccount) string {
	return acct.Name + "-enforcement-deny"
}
