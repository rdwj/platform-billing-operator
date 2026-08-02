package enforcement

import (
	"context"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// EnforcementBackend abstracts how enforcement actions (freeze, thaw, allowance
// updates) are applied to the data plane. Implementations target specific
// enforcement mechanisms (e.g., Kuadrant TokenRateLimitPolicy).
type EnforcementBackend interface {
	// Apply updates the tenant's rate limits based on the computed allowance.
	// allowance is a decimal string representing the token budget for the
	// current enforcement window. No-op until Phase 2 wires this to TRLP.
	Apply(ctx context.Context, acct *billingv1alpha1.TenantAccount, allowance string) error

	// Freeze denies all requests for the tenant. Called on Active/Grace → Frozen
	// transitions.
	Freeze(ctx context.Context, acct *billingv1alpha1.TenantAccount) error

	// Thaw restores the tenant's access after a freeze. Called on Frozen → Active
	// transitions (e.g., after a top-up).
	Thaw(ctx context.Context, acct *billingv1alpha1.TenantAccount) error
}

// NoopBackend is an EnforcementBackend that does nothing. Used for testing the
// state machine in isolation before the real Kuadrant backend is wired in.
type NoopBackend struct{}

var _ EnforcementBackend = (*NoopBackend)(nil)

func (n *NoopBackend) Apply(_ context.Context, _ *billingv1alpha1.TenantAccount, _ string) error {
	return nil
}

func (n *NoopBackend) Freeze(_ context.Context, _ *billingv1alpha1.TenantAccount) error {
	return nil
}

func (n *NoopBackend) Thaw(_ context.Context, _ *billingv1alpha1.TenantAccount) error {
	return nil
}
