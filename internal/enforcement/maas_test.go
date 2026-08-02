package enforcement

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	kuadrantv1alpha1 "github.com/kuadrant/kuadrant-operator/api/v1alpha1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const testNamespace = "test-ns"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		billingv1alpha1.AddToScheme,
		kuadrantv1alpha1.AddToScheme,
		kuadrantv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("registering scheme: %v", err)
		}
	}
	return s
}

func testPlan() *billingv1alpha1.Plan {
	return &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: testNamespace,
		},
		Spec: billingv1alpha1.PlanSpec{
			TargetRef: gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-gateway",
				},
			},
			Limits: billingv1alpha1.PlanLimits{
				TokensPerMinute: int64Ptr(5000000),
			},
		},
	}
}

func testTenantAccount() *billingv1alpha1.TenantAccount {
	return &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-a",
			Namespace: testNamespace,
			UID:       "test-uid-12345",
		},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Tenant A",
			PlanRef:     corev1.LocalObjectReference{Name: "test-plan"},
			BillingMode: billingv1alpha1.BillingModePrepaid,
		},
	}
}

func newTestBackend(t *testing.T) (*MaaSBackend, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(testPlan()).
		Build()
	return NewMaaSBackend(c, s), c
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

func TestApplyCreatesTRLP(t *testing.T) {
	backend, c := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Apply(ctx, acct, "1000000"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var trlp kuadrantv1alpha1.TokenRateLimitPolicy
	if err := c.Get(ctx, types.NamespacedName{
		Name:      enforcementTRLPName(acct),
		Namespace: testNamespace,
	}, &trlp); err != nil {
		t.Fatalf("getting TRLP: %v", err)
	}

	if got := trlp.Labels[labelManagedBy]; got != acct.Name {
		t.Errorf("managed-by label = %q, want %q", got, acct.Name)
	}

	limit, ok := trlp.Spec.Limits["balance-allowance"]
	if !ok {
		t.Fatal("TRLP missing 'balance-allowance' limit")
	}
	if len(limit.Rates) != 1 {
		t.Fatalf("expected 1 rate, got %d", len(limit.Rates))
	}
	if limit.Rates[0].Limit != 1000000 {
		t.Errorf("rate limit = %d, want 1000000", limit.Rates[0].Limit)
	}
	if got := string(limit.Rates[0].Window); got != "1m" {
		t.Errorf("rate window = %q, want %q", got, "1m")
	}
	if len(limit.Counters) != 1 || string(limit.Counters[0].Expression) != "auth.identity.sub" {
		t.Errorf("unexpected counters: %+v", limit.Counters)
	}
}

func TestApplyHysteresisSkipsSmallChange(t *testing.T) {
	backend, c := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Apply(ctx, acct, "1000000"); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// 5% change: 50000 / 1000000 = 5% <= 10% threshold -> skip.
	if err := backend.Apply(ctx, acct, "1050000"); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	var trlp kuadrantv1alpha1.TokenRateLimitPolicy
	if err := c.Get(ctx, types.NamespacedName{
		Name:      enforcementTRLPName(acct),
		Namespace: testNamespace,
	}, &trlp); err != nil {
		t.Fatalf("getting TRLP: %v", err)
	}

	limit := trlp.Spec.Limits["balance-allowance"]
	if limit.Rates[0].Limit != 1000000 {
		t.Errorf("rate limit = %d, want 1000000 (hysteresis should skip 5%% change)",
			limit.Rates[0].Limit)
	}
}

func TestApplyHysteresisAppliesLargeChange(t *testing.T) {
	backend, c := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Apply(ctx, acct, "1000000"); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// 20% change: 200000 / 1000000 = 20% > 10% threshold -> apply.
	if err := backend.Apply(ctx, acct, "1200000"); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	var trlp kuadrantv1alpha1.TokenRateLimitPolicy
	if err := c.Get(ctx, types.NamespacedName{
		Name:      enforcementTRLPName(acct),
		Namespace: testNamespace,
	}, &trlp); err != nil {
		t.Fatalf("getting TRLP: %v", err)
	}

	limit := trlp.Spec.Limits["balance-allowance"]
	if limit.Rates[0].Limit != 1200000 {
		t.Errorf("rate limit = %d, want 1200000 (hysteresis should apply 20%% change)",
			limit.Rates[0].Limit)
	}
}

// ---------------------------------------------------------------------------
// Freeze
// ---------------------------------------------------------------------------

func TestFreezeCreatesAuthPolicy(t *testing.T) {
	backend, c := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Freeze(ctx, acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(authPolicyGVK)
	if err := c.Get(ctx, types.NamespacedName{
		Name:      enforcementDenyName(acct),
		Namespace: testNamespace,
	}, obj); err != nil {
		t.Fatalf("getting deny AuthPolicy: %v", err)
	}

	// Verify deny-frozen OPA rule.
	rego, found, err := unstructured.NestedString(
		obj.Object, "spec", "rules", "authorization", "deny-frozen", "opa", "rego",
	)
	if err != nil {
		t.Fatalf("extracting rego: %v", err)
	}
	if !found {
		t.Fatal("deny-frozen opa rego not found in AuthPolicy")
	}
	if rego != "allow = false" {
		t.Errorf("rego = %q, want %q", rego, "allow = false")
	}

	// Verify the when predicate targets this tenant.
	whenSlice, found, err := unstructured.NestedSlice(obj.Object, "spec", "when")
	if err != nil || !found {
		t.Fatalf("extracting when: found=%v err=%v", found, err)
	}
	if len(whenSlice) != 1 {
		t.Fatalf("expected 1 when predicate, got %d", len(whenSlice))
	}
	pred, ok := whenSlice[0].(map[string]interface{})
	if !ok {
		t.Fatal("when[0] is not a map")
	}
	wantPred := "auth.identity.sub == 'tenant-a'"
	if pred["predicate"] != wantPred {
		t.Errorf("predicate = %v, want %q", pred["predicate"], wantPred)
	}
}

func TestFreezeIsIdempotent(t *testing.T) {
	backend, _ := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Freeze(ctx, acct); err != nil {
		t.Fatalf("first Freeze: %v", err)
	}
	if err := backend.Freeze(ctx, acct); err != nil {
		t.Fatalf("second Freeze: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Thaw
// ---------------------------------------------------------------------------

func TestThawDeletesAuthPolicy(t *testing.T) {
	backend, c := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	if err := backend.Freeze(ctx, acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	if err := backend.Thaw(ctx, acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(authPolicyGVK)
	err := c.Get(ctx, types.NamespacedName{
		Name:      enforcementDenyName(acct),
		Namespace: testNamespace,
	}, obj)
	if err == nil {
		t.Fatal("expected deny AuthPolicy to be deleted, but it still exists")
	}
}

func TestThawIsIdempotent(t *testing.T) {
	backend, _ := newTestBackend(t)
	acct := testTenantAccount()
	ctx := context.Background()

	// Thaw without prior Freeze should be a no-op.
	if err := backend.Thaw(ctx, acct); err != nil {
		t.Fatalf("Thaw without prior Freeze: %v", err)
	}
}
