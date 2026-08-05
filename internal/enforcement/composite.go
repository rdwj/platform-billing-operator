package enforcement

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// Product profile constants identify which enforcement backend(s) a tenant
// requires based on the features configured in their Plan.
const (
	ProductMaaS   = "maas"
	ProductGPUaaS = "gpuaas"
)

// CompositeBackend routes enforcement calls to the appropriate backend(s)
// based on the products a tenant uses. A Plan with TokensPerMinute set routes
// to the MaaS backend; a Plan with GPUQuota set routes to the GPUaaS backend.
// Tenants using both products get enforcement applied to both backends.
type CompositeBackend struct {
	client   client.Client
	backends map[string]EnforcementBackend
}

var _ EnforcementBackend = (*CompositeBackend)(nil)

// NewCompositeBackend returns a CompositeBackend with no registered backends.
// Use Register to wire product-specific backends before use.
func NewCompositeBackend(c client.Client) *CompositeBackend {
	return &CompositeBackend{
		client:   c,
		backends: make(map[string]EnforcementBackend),
	}
}

// Register associates an EnforcementBackend with a product profile.
func (cb *CompositeBackend) Register(product string, backend EnforcementBackend) {
	cb.backends[product] = backend
}

// Apply updates rate limits on all backends matching the tenant's active
// products. Returns the first error encountered (short-circuit), since a
// partial Apply leaves the tenant in an inconsistent enforcement state that
// the next reconcile will retry.
func (cb *CompositeBackend) Apply(ctx context.Context, acct *billingv1alpha1.TenantAccount, allowance string) error {
	products, err := cb.resolveProducts(ctx, acct)
	if err != nil {
		return err
	}
	for _, p := range products {
		if b, ok := cb.backends[p]; ok {
			if err := b.Apply(ctx, acct, allowance); err != nil {
				return fmt.Errorf("apply %s backend: %w", p, err)
			}
		}
	}
	return nil
}

// Freeze denies all requests for the tenant across every matching backend.
// Unlike Apply, Freeze calls all matching backends even if one fails: partial
// freeze is recoverable via retry and each backend is idempotent. Errors are
// collected with errors.Join.
func (cb *CompositeBackend) Freeze(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	products, err := cb.resolveProducts(ctx, acct)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range products {
		if b, ok := cb.backends[p]; ok {
			if err := b.Freeze(ctx, acct); err != nil {
				errs = append(errs, fmt.Errorf("freeze %s backend: %w", p, err))
			}
		}
	}
	return errors.Join(errs...)
}

// Thaw restores access for the tenant across every matching backend.
// Like Freeze, calls all backends and collects errors.
func (cb *CompositeBackend) Thaw(ctx context.Context, acct *billingv1alpha1.TenantAccount) error {
	products, err := cb.resolveProducts(ctx, acct)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range products {
		if b, ok := cb.backends[p]; ok {
			if err := b.Thaw(ctx, acct); err != nil {
				errs = append(errs, fmt.Errorf("thaw %s backend: %w", p, err))
			}
		}
	}
	return errors.Join(errs...)
}

// resolveProducts looks up the tenant's Plan and determines which product
// profiles are active based on the configured limits.
func (cb *CompositeBackend) resolveProducts(ctx context.Context, acct *billingv1alpha1.TenantAccount) ([]string, error) {
	var plan billingv1alpha1.Plan
	if err := cb.client.Get(ctx, types.NamespacedName{
		Name:      acct.Spec.PlanRef.Name,
		Namespace: acct.Namespace,
	}, &plan); err != nil {
		return nil, fmt.Errorf("resolving plan %q for tenant %s: %w",
			acct.Spec.PlanRef.Name, acct.Name, err)
	}

	var products []string
	if plan.Spec.Limits.TokensPerMinute != nil {
		products = append(products, ProductMaaS)
	}
	if plan.Spec.Limits.GPUQuota != nil {
		products = append(products, ProductGPUaaS)
	}
	return products, nil
}
