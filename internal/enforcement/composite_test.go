package enforcement

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func int32Ptr(v int32) *int32 { return &v }

// recordingBackend is a test double that records which methods were called.
type recordingBackend struct {
	applyCalls  []applyCall
	freezeCalls []string
	thawCalls   []string
	freezeErr   error
	thawErr     error
}

type applyCall struct {
	tenant    string
	allowance string
}

func (r *recordingBackend) Apply(_ context.Context, acct *billingv1alpha1.TenantAccount, allowance string) error {
	r.applyCalls = append(r.applyCalls, applyCall{tenant: acct.Name, allowance: allowance})
	return nil
}

func (r *recordingBackend) Freeze(_ context.Context, acct *billingv1alpha1.TenantAccount) error {
	r.freezeCalls = append(r.freezeCalls, acct.Name)
	return r.freezeErr
}

func (r *recordingBackend) Thaw(_ context.Context, acct *billingv1alpha1.TenantAccount) error {
	r.thawCalls = append(r.thawCalls, acct.Name)
	return r.thawErr
}

// compositeTestPlan builds a Plan with the specified limits for composite tests.
func compositeTestPlan(name string, tokensPerMinute *int64, gpuQuota *int32) *billingv1alpha1.Plan {
	return &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
				TokensPerMinute: tokensPerMinute,
				GPUQuota:        gpuQuota,
			},
		},
	}
}

// compositeTestAcct builds a TenantAccount pointing at the named Plan.
func compositeTestAcct(planName string) *billingv1alpha1.TenantAccount {
	return &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-composite",
			Namespace: testNamespace,
			UID:       "composite-uid-12345",
		},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Composite Test Tenant",
			PlanRef:     corev1.LocalObjectReference{Name: planName},
			BillingMode: billingv1alpha1.BillingModePrepaid,
		},
	}
}

// ---------------------------------------------------------------------------
// MaaS-only tenant
// ---------------------------------------------------------------------------

func TestCompositeApplyMaaSOnly(t *testing.T) {
	plan := compositeTestPlan("maas-plan", int64Ptr(5000000), nil)
	acct := compositeTestAcct("maas-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Apply(context.Background(), acct, "1000"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(maas.applyCalls) != 1 {
		t.Errorf("expected 1 MaaS Apply call, got %d", len(maas.applyCalls))
	}
	if len(gpu.applyCalls) != 0 {
		t.Errorf("expected 0 GPU Apply calls, got %d", len(gpu.applyCalls))
	}
}

func TestCompositeFreezeMaaSOnly(t *testing.T) {
	plan := compositeTestPlan("maas-plan", int64Ptr(5000000), nil)
	acct := compositeTestAcct("maas-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	if len(maas.freezeCalls) != 1 {
		t.Errorf("expected 1 MaaS Freeze call, got %d", len(maas.freezeCalls))
	}
	if len(gpu.freezeCalls) != 0 {
		t.Errorf("expected 0 GPU Freeze calls, got %d", len(gpu.freezeCalls))
	}
}

func TestCompositeThawMaaSOnly(t *testing.T) {
	plan := compositeTestPlan("maas-plan", int64Ptr(5000000), nil)
	acct := compositeTestAcct("maas-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	if len(maas.thawCalls) != 1 {
		t.Errorf("expected 1 MaaS Thaw call, got %d", len(maas.thawCalls))
	}
	if len(gpu.thawCalls) != 0 {
		t.Errorf("expected 0 GPU Thaw calls, got %d", len(gpu.thawCalls))
	}
}

// ---------------------------------------------------------------------------
// GPU-only tenant
// ---------------------------------------------------------------------------

func TestCompositeApplyGPUOnly(t *testing.T) {
	plan := compositeTestPlan("gpu-plan", nil, int32Ptr(4))
	acct := compositeTestAcct("gpu-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Apply(context.Background(), acct, "1000"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(maas.applyCalls) != 0 {
		t.Errorf("expected 0 MaaS Apply calls, got %d", len(maas.applyCalls))
	}
	if len(gpu.applyCalls) != 1 {
		t.Errorf("expected 1 GPU Apply call, got %d", len(gpu.applyCalls))
	}
}

func TestCompositeFreezeGPUOnly(t *testing.T) {
	plan := compositeTestPlan("gpu-plan", nil, int32Ptr(4))
	acct := compositeTestAcct("gpu-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	if len(maas.freezeCalls) != 0 {
		t.Errorf("expected 0 MaaS Freeze calls, got %d", len(maas.freezeCalls))
	}
	if len(gpu.freezeCalls) != 1 {
		t.Errorf("expected 1 GPU Freeze call, got %d", len(gpu.freezeCalls))
	}
}

func TestCompositeThawGPUOnly(t *testing.T) {
	plan := compositeTestPlan("gpu-plan", nil, int32Ptr(4))
	acct := compositeTestAcct("gpu-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	if len(maas.thawCalls) != 0 {
		t.Errorf("expected 0 MaaS Thaw calls, got %d", len(maas.thawCalls))
	}
	if len(gpu.thawCalls) != 1 {
		t.Errorf("expected 1 GPU Thaw call, got %d", len(gpu.thawCalls))
	}
}

// ---------------------------------------------------------------------------
// Dual-product tenant
// ---------------------------------------------------------------------------

func TestCompositeApplyDualProduct(t *testing.T) {
	plan := compositeTestPlan("dual-plan", int64Ptr(5000000), int32Ptr(4))
	acct := compositeTestAcct("dual-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Apply(context.Background(), acct, "1000"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(maas.applyCalls) != 1 {
		t.Errorf("expected 1 MaaS Apply call, got %d", len(maas.applyCalls))
	}
	if len(gpu.applyCalls) != 1 {
		t.Errorf("expected 1 GPU Apply call, got %d", len(gpu.applyCalls))
	}
}

func TestCompositeFreezeDualProduct(t *testing.T) {
	plan := compositeTestPlan("dual-plan", int64Ptr(5000000), int32Ptr(4))
	acct := compositeTestAcct("dual-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	if len(maas.freezeCalls) != 1 {
		t.Errorf("expected 1 MaaS Freeze call, got %d", len(maas.freezeCalls))
	}
	if len(gpu.freezeCalls) != 1 {
		t.Errorf("expected 1 GPU Freeze call, got %d", len(gpu.freezeCalls))
	}
}

func TestCompositeThawDualProduct(t *testing.T) {
	plan := compositeTestPlan("dual-plan", int64Ptr(5000000), int32Ptr(4))
	acct := compositeTestAcct("dual-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	if err := cb.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	if len(maas.thawCalls) != 1 {
		t.Errorf("expected 1 MaaS Thaw call, got %d", len(maas.thawCalls))
	}
	if len(gpu.thawCalls) != 1 {
		t.Errorf("expected 1 GPU Thaw call, got %d", len(gpu.thawCalls))
	}
}

// ---------------------------------------------------------------------------
// Partial failure: Freeze
// ---------------------------------------------------------------------------

func TestCompositeFreezePartialFailure(t *testing.T) {
	plan := compositeTestPlan("dual-plan", int64Ptr(5000000), int32Ptr(4))
	acct := compositeTestAcct("dual-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{freezeErr: fmt.Errorf("gpu freeze failed")}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	err := cb.Freeze(context.Background(), acct)
	if err == nil {
		t.Fatal("expected error from partial Freeze failure, got nil")
	}

	// MaaS backend should still have been called despite GPU failure.
	if len(maas.freezeCalls) != 1 {
		t.Errorf("expected MaaS Freeze to be called even when GPU fails, got %d calls",
			len(maas.freezeCalls))
	}
	if len(gpu.freezeCalls) != 1 {
		t.Errorf("expected GPU Freeze to be called, got %d calls", len(gpu.freezeCalls))
	}
}

// ---------------------------------------------------------------------------
// Partial failure: Thaw
// ---------------------------------------------------------------------------

func TestCompositeThawPartialFailure(t *testing.T) {
	plan := compositeTestPlan("dual-plan", int64Ptr(5000000), int32Ptr(4))
	acct := compositeTestAcct("dual-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{thawErr: fmt.Errorf("gpu thaw failed")}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	err := cb.Thaw(context.Background(), acct)
	if err == nil {
		t.Fatal("expected error from partial Thaw failure, got nil")
	}

	// MaaS backend should still have been called despite GPU failure.
	if len(maas.thawCalls) != 1 {
		t.Errorf("expected MaaS Thaw to be called even when GPU fails, got %d calls",
			len(maas.thawCalls))
	}
	if len(gpu.thawCalls) != 1 {
		t.Errorf("expected GPU Thaw to be called, got %d calls", len(gpu.thawCalls))
	}
}

// ---------------------------------------------------------------------------
// No matching backends: no-op
// ---------------------------------------------------------------------------

func TestCompositeNoMatchingBackends(t *testing.T) {
	// Plan has neither TokensPerMinute nor GPUQuota.
	plan := compositeTestPlan("empty-plan", nil, nil)
	acct := compositeTestAcct("empty-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plan).Build()

	maas := &recordingBackend{}
	gpu := &recordingBackend{}

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, maas)
	cb.Register(ProductGPUaaS, gpu)

	ctx := context.Background()

	if err := cb.Apply(ctx, acct, "1000"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := cb.Freeze(ctx, acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if err := cb.Thaw(ctx, acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	if len(maas.applyCalls)+len(maas.freezeCalls)+len(maas.thawCalls) != 0 {
		t.Error("expected no calls to MaaS backend for plan without TokensPerMinute")
	}
	if len(gpu.applyCalls)+len(gpu.freezeCalls)+len(gpu.thawCalls) != 0 {
		t.Error("expected no calls to GPU backend for plan without GPUQuota")
	}
}

// ---------------------------------------------------------------------------
// Error: Plan not found
// ---------------------------------------------------------------------------

func TestCompositePlanNotFound(t *testing.T) {
	acct := compositeTestAcct("nonexistent-plan")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build() // no Plan object

	cb := NewCompositeBackend(c)
	cb.Register(ProductMaaS, &recordingBackend{})

	ctx := context.Background()

	if err := cb.Apply(ctx, acct, "1000"); err == nil {
		t.Fatal("expected error when Plan not found, got nil")
	}
	if err := cb.Freeze(ctx, acct); err == nil {
		t.Fatal("expected error when Plan not found, got nil")
	}
	if err := cb.Thaw(ctx, acct); err == nil {
		t.Fatal("expected error when Plan not found, got nil")
	}
}
