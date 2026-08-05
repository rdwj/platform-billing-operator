package enforcement

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const (
	labelTenantID   = "billing.opendatahub.io/tenant-id"
	gpuResourceName = corev1.ResourceName("nvidia.com/gpu")
)

// KueueBackend implements EnforcementBackend by managing Kueue ClusterQueue
// quota and stop policies for GPU workloads. Apply sets the GPU nominalQuota
// from the Plan, Freeze pauses admissions via StopPolicy, and Thaw restores
// normal operation.
type KueueBackend struct {
	client client.Client
	scheme *runtime.Scheme
}

var _ EnforcementBackend = (*KueueBackend)(nil)

// NewKueueBackend returns a KueueBackend wired to the given client and scheme.
func NewKueueBackend(c client.Client, s *runtime.Scheme) *KueueBackend {
	return &KueueBackend{
		client: c,
		scheme: s,
	}
}

// Apply updates the tenant's ClusterQueue GPU nominalQuota to match the Plan's
// GPUQuota. The allowance parameter is ignored because GPU enforcement is
// state-driven (Active vs Frozen), not token-allowance-driven. Returns nil
// (no-op) when the Plan has no GPUQuota or no ClusterQueue exists for the
// tenant.
func (k *KueueBackend) Apply(ctx context.Context, acct *billingv1alpha1.TenantAccount, _ string) error {
	plan, err := k.resolvePlan(ctx, acct)
	if err != nil {
		return fmt.Errorf("resolving plan for tenant %s: %w", acct.Name, err)
	}
	if plan.Spec.Limits.GPUQuota == nil {
		return nil // not a GPU tenant
	}

	cq, err := k.findTenantClusterQueue(ctx, acct.Name)
	if err != nil {
		return fmt.Errorf("finding ClusterQueue for tenant %s: %w", acct.Name, err)
	}
	if cq == nil {
		return nil // no CQ configured for this tenant
	}

	desiredQuota := resource.MustParse(fmt.Sprintf("%d", *plan.Spec.Limits.GPUQuota))

	// Walk ResourceGroups looking for the nvidia.com/gpu resource.
	for i := range cq.Spec.ResourceGroups {
		for j := range cq.Spec.ResourceGroups[i].Flavors {
			for l := range cq.Spec.ResourceGroups[i].Flavors[j].Resources {
				r := &cq.Spec.ResourceGroups[i].Flavors[j].Resources[l]
				if r.Name == gpuResourceName {
					if r.NominalQuota.Equal(desiredQuota) {
						return nil // already correct, idempotent
					}
					r.NominalQuota = desiredQuota
					return k.client.Update(ctx, cq)
				}
			}
		}
	}

	return nil // nvidia.com/gpu resource not found in any flavor
}

// Freeze stops new workload admissions for the tenant's ClusterQueue. The
// StopPolicy is determined by the TenantAccount's GPUFreezeMode:
//   - Drain (default): Hold -- running workloads complete, no new admissions
//   - Preempt: HoldAndDrain -- running workloads are evicted
func (k *KueueBackend) Freeze(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	cq, err := k.findTenantClusterQueue(ctx, acct.Name)
	if err != nil {
		return fmt.Errorf("finding ClusterQueue for tenant %s: %w", acct.Name, err)
	}
	if cq == nil {
		return nil
	}

	var targetPolicy kueuev1beta1.StopPolicy
	switch acct.Spec.GPUFreezeMode {
	case billingv1alpha1.GPUFreezeModePreempt:
		targetPolicy = kueuev1beta1.HoldAndDrain
	default: // Drain or empty (Drain is the default)
		targetPolicy = kueuev1beta1.Hold
	}

	if cq.Spec.StopPolicy != nil && *cq.Spec.StopPolicy == targetPolicy {
		return nil // already set, idempotent
	}

	cq.Spec.StopPolicy = &targetPolicy
	return k.client.Update(ctx, cq)
}

// Thaw restores normal workload admissions by setting the tenant's
// ClusterQueue StopPolicy to None. Idempotent: returns nil if the queue is
// already accepting workloads.
func (k *KueueBackend) Thaw(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	cq, err := k.findTenantClusterQueue(ctx, acct.Name)
	if err != nil {
		return fmt.Errorf("finding ClusterQueue for tenant %s: %w", acct.Name, err)
	}
	if cq == nil {
		return nil
	}

	none := kueuev1beta1.None
	if cq.Spec.StopPolicy == nil || *cq.Spec.StopPolicy == none {
		return nil // already accepting workloads
	}

	cq.Spec.StopPolicy = &none
	return k.client.Update(ctx, cq)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolvePlan reads the Plan CR referenced by the TenantAccount's PlanRef.
func (k *KueueBackend) resolvePlan(ctx context.Context, acct *billingv1alpha1.TenantAccount) (*billingv1alpha1.Plan, error) {
	var plan billingv1alpha1.Plan
	if err := k.client.Get(ctx, types.NamespacedName{
		Name:      acct.Spec.PlanRef.Name,
		Namespace: acct.Namespace,
	}, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// findTenantClusterQueue lists ClusterQueues labeled with the given tenant
// name and returns the first match, or nil if none exist.
func (k *KueueBackend) findTenantClusterQueue(ctx context.Context, tenantName string) (*kueuev1beta1.ClusterQueue, error) {
	var list kueuev1beta1.ClusterQueueList
	if err := k.client.List(ctx, &list, client.MatchingLabels{
		labelTenantID: tenantName,
	}); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}
