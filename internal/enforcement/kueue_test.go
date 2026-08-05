package enforcement

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func kueueTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		billingv1alpha1.AddToScheme,
		kueuev1beta1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("registering scheme: %v", err)
		}
	}
	return s
}

func gpuPlan(gpuQuota *int32) *billingv1alpha1.Plan {
	return &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-plan",
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
				GPUQuota:        gpuQuota,
			},
		},
	}
}

func gpuTenantAccount(freezeMode string) *billingv1alpha1.TenantAccount {
	return &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-tenant",
			Namespace: testNamespace,
			UID:       "gpu-uid-12345",
		},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName:   "GPU Tenant",
			PlanRef:       corev1.LocalObjectReference{Name: "gpu-plan"},
			BillingMode:   billingv1alpha1.BillingModePrepaid,
			GPUFreezeMode: freezeMode,
		},
	}
}

func gpuClusterQueue(tenantName string, gpuQuota string, stopPolicy *kueuev1beta1.StopPolicy) *kueuev1beta1.ClusterQueue {
	return &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("cq-%s", tenantName),
			Labels: map[string]string{
				labelTenantID: tenantName,
			},
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{gpuResourceName},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "default",
					Resources: []kueuev1beta1.ResourceQuota{{
						Name:         gpuResourceName,
						NominalQuota: resource.MustParse(gpuQuota),
					}},
				}},
			}},
			StopPolicy: stopPolicy,
		},
	}
}

func newKueueTestBackend(t *testing.T, plan *billingv1alpha1.Plan, objs ...client.Object) (*KueueBackend, client.Client) {
	t.Helper()
	s := kueueTestScheme(t)
	allObjs := []client.Object{plan}
	allObjs = append(allObjs, objs...)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(allObjs...).
		Build()
	return NewKueueBackend(c, s), c
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

func TestKueueApplySetsNominalQuota(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := gpuClusterQueue("gpu-tenant", "2", &nonePolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, c := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Apply(context.Background(), acct, "ignored"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var updated kueuev1beta1.ClusterQueue
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cq), &updated); err != nil {
		t.Fatalf("getting CQ: %v", err)
	}

	got := updated.Spec.ResourceGroups[0].Flavors[0].Resources[0].NominalQuota
	want := resource.MustParse("4")
	if !got.Equal(want) {
		t.Errorf("NominalQuota = %s, want %s", got.String(), want.String())
	}
}

func TestKueueApplyIsIdempotent(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := gpuClusterQueue("gpu-tenant", "4", &nonePolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, c := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Apply(context.Background(), acct, "ignored"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify quota unchanged.
	var updated kueuev1beta1.ClusterQueue
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cq), &updated); err != nil {
		t.Fatalf("getting CQ: %v", err)
	}

	got := updated.Spec.ResourceGroups[0].Flavors[0].Resources[0].NominalQuota
	want := resource.MustParse("4")
	if !got.Equal(want) {
		t.Errorf("NominalQuota = %s, want %s (should be unchanged)", got.String(), want.String())
	}
}

func TestKueueApplyNoOpWhenNoCQ(t *testing.T) {
	plan := gpuPlan(int32Ptr(4))
	backend, _ := newKueueTestBackend(t, plan)

	acct := gpuTenantAccount("")
	if err := backend.Apply(context.Background(), acct, "ignored"); err != nil {
		t.Fatalf("Apply with no CQ should return nil, got: %v", err)
	}
}

func TestKueueApplyNoOpWhenNoGPUQuota(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := gpuClusterQueue("gpu-tenant", "2", &nonePolicy)
	plan := gpuPlan(nil) // no GPUQuota
	backend, _ := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Apply(context.Background(), acct, "ignored"); err != nil {
		t.Fatalf("Apply with no GPUQuota should return nil, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Freeze
// ---------------------------------------------------------------------------

func TestKueueFreezeSetsDrainMode(t *testing.T) {
	// GPUFreezeMode="" (empty, default=Drain) should map to Hold.
	for _, mode := range []string{"", billingv1alpha1.GPUFreezeModeDrain} {
		t.Run(fmt.Sprintf("mode=%q", mode), func(t *testing.T) {
			nonePolicy := kueuev1beta1.None
			cq := gpuClusterQueue("gpu-tenant", "4", &nonePolicy)
			plan := gpuPlan(int32Ptr(4))
			backend, c := newKueueTestBackend(t, plan, cq)

			acct := gpuTenantAccount(mode)
			if err := backend.Freeze(context.Background(), acct); err != nil {
				t.Fatalf("Freeze: %v", err)
			}

			var updated kueuev1beta1.ClusterQueue
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(cq), &updated); err != nil {
				t.Fatalf("getting CQ: %v", err)
			}

			if updated.Spec.StopPolicy == nil {
				t.Fatal("StopPolicy is nil after Freeze")
			}
			if *updated.Spec.StopPolicy != kueuev1beta1.Hold {
				t.Errorf("StopPolicy = %q, want %q", *updated.Spec.StopPolicy, kueuev1beta1.Hold)
			}
		})
	}
}

func TestKueueFreezeSetsPreemptMode(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := gpuClusterQueue("gpu-tenant", "4", &nonePolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, c := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount(billingv1alpha1.GPUFreezeModePreempt)
	if err := backend.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	var updated kueuev1beta1.ClusterQueue
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cq), &updated); err != nil {
		t.Fatalf("getting CQ: %v", err)
	}

	if updated.Spec.StopPolicy == nil {
		t.Fatal("StopPolicy is nil after Freeze")
	}
	if *updated.Spec.StopPolicy != kueuev1beta1.HoldAndDrain {
		t.Errorf("StopPolicy = %q, want %q", *updated.Spec.StopPolicy, kueuev1beta1.HoldAndDrain)
	}
}

func TestKueueFreezeIsIdempotent(t *testing.T) {
	holdPolicy := kueuev1beta1.Hold
	cq := gpuClusterQueue("gpu-tenant", "4", &holdPolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, _ := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze (idempotent): %v", err)
	}
}

func TestKueueFreezeNoOpWhenNoCQ(t *testing.T) {
	plan := gpuPlan(int32Ptr(4))
	backend, _ := newKueueTestBackend(t, plan)

	acct := gpuTenantAccount("")
	if err := backend.Freeze(context.Background(), acct); err != nil {
		t.Fatalf("Freeze with no CQ should return nil, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Thaw
// ---------------------------------------------------------------------------

func TestKueueThawRestoresNone(t *testing.T) {
	holdPolicy := kueuev1beta1.Hold
	cq := gpuClusterQueue("gpu-tenant", "4", &holdPolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, c := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw: %v", err)
	}

	var updated kueuev1beta1.ClusterQueue
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cq), &updated); err != nil {
		t.Fatalf("getting CQ: %v", err)
	}

	if updated.Spec.StopPolicy == nil {
		t.Fatal("StopPolicy is nil after Thaw")
	}
	if *updated.Spec.StopPolicy != kueuev1beta1.None {
		t.Errorf("StopPolicy = %q, want %q", *updated.Spec.StopPolicy, kueuev1beta1.None)
	}
}

func TestKueueThawIsIdempotent(t *testing.T) {
	nonePolicy := kueuev1beta1.None
	cq := gpuClusterQueue("gpu-tenant", "4", &nonePolicy)
	plan := gpuPlan(int32Ptr(4))
	backend, _ := newKueueTestBackend(t, plan, cq)

	acct := gpuTenantAccount("")
	if err := backend.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw (idempotent): %v", err)
	}
}

func TestKueueThawNoOpWhenNoCQ(t *testing.T) {
	plan := gpuPlan(int32Ptr(4))
	backend, _ := newKueueTestBackend(t, plan)

	acct := gpuTenantAccount("")
	if err := backend.Thaw(context.Background(), acct); err != nil {
		t.Fatalf("Thaw with no CQ should return nil, got: %v", err)
	}
}
