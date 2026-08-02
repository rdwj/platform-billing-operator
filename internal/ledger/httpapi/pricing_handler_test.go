package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, billingv1alpha1.AddToScheme(s))
	return s
}

func TestPricingHandler(t *testing.T) {
	effectiveTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Shared test fixtures.
	activePB := &billingv1alpha1.PriceBook{
		ObjectMeta: metav1.ObjectMeta{Name: "standard-v1", Namespace: "billing-demo"},
		Spec: billingv1alpha1.PriceBookSpec{
			Currency:               "USD",
			EffectiveFrom:          metav1.NewTime(effectiveTime),
			NoticeDays:             30,
			ProviderFaultPolicy:    "zero_charge",
			ClientDisconnectPolicy: "bill_partial",
			SKUs: []billingv1alpha1.SKU{
				{
					SKUName: "gpt-4o",
					Unit:    "per-1M-tokens",
					Rates:   map[string]string{"prompt_tokens": "2.500000", "completion_tokens": "10.000000"},
				},
			},
		},
		Status: billingv1alpha1.PriceBookStatus{State: "Active"},
	}

	pinnedPB := &billingv1alpha1.PriceBook{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-v0", Namespace: "billing-demo"},
		Spec: billingv1alpha1.PriceBookSpec{
			Currency:      "EUR",
			EffectiveFrom: metav1.NewTime(effectiveTime),
			NoticeDays:    7,
			SKUs: []billingv1alpha1.SKU{
				{
					SKUName: "llama-3-70b",
					Unit:    "per-1M-tokens",
					Rates:   map[string]string{"prompt_tokens": "1.000000", "completion_tokens": "3.000000"},
				},
			},
		},
		Status: billingv1alpha1.PriceBookStatus{State: "Superseded"},
	}

	planWithRef := &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{Name: "growth", Namespace: "billing-demo"},
		Spec: billingv1alpha1.PlanSpec{
			PriceBookRef: "standard-v1",
			Limits:       billingv1alpha1.PlanLimits{},
			TargetRef: gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "ai-gateway",
				},
			},
		},
	}

	planNoRef := &billingv1alpha1.Plan{
		ObjectMeta: metav1.ObjectMeta{Name: "basic", Namespace: "billing-demo"},
		Spec: billingv1alpha1.PlanSpec{
			Limits: billingv1alpha1.PlanLimits{},
			TargetRef: gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gatewayapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "ai-gateway",
				},
			},
		},
	}

	tenantViaPlan := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "corvid-tenant", Namespace: "billing-demo"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Corvid AI Cloud",
			PlanRef:     corev1.LocalObjectReference{Name: "growth"},
			BillingMode: "Prepaid",
		},
	}

	tenantWithPin := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "pinned-tenant", Namespace: "billing-demo"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName:  "Pinned Corp",
			PlanRef:      corev1.LocalObjectReference{Name: "growth"},
			PriceBookPin: "legacy-v0",
			BillingMode:  "Prepaid",
		},
	}

	tenantNoPlanRef := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-tenant", Namespace: "billing-demo"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Fallback Corp",
			PlanRef:     corev1.LocalObjectReference{Name: "basic"},
			BillingMode: "ShowbackOnly",
		},
	}

	tests := []struct {
		name       string
		query      string
		objects    []runtime.Object
		nilReader  bool
		wantStatus int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "missing tenant param",
			query:      "/v1/pricing",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "nil reader returns 503",
			query:      "/v1/pricing?tenant=any",
			nilReader:  true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "tenant not found",
			query:      "/v1/pricing?tenant=nonexistent",
			objects:    []runtime.Object{},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "happy path via Plan.PriceBookRef",
			query:      "/v1/pricing?tenant=corvid-tenant&namespace=billing-demo",
			objects:    []runtime.Object{tenantViaPlan, planWithRef, activePB},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp pricingResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Equal(t, "corvid-tenant", resp.Tenant)
				require.Equal(t, "billing-demo", resp.Namespace)
				require.Equal(t, "standard-v1", resp.PriceBook)
				require.Equal(t, "USD", resp.Currency)
				require.Equal(t, "2026-01-01T00:00:00Z", resp.EffectiveFrom)
				require.Equal(t, "zero_charge", resp.ProviderFaultPolicy)
				require.Equal(t, "bill_partial", resp.ClientDisconnectPolicy)
				require.Len(t, resp.SKUs, 1)
				require.Equal(t, "gpt-4o", resp.SKUs[0].SKU)
				require.Equal(t, "per-1M-tokens", resp.SKUs[0].Unit)
				require.Equal(t, "2.500000", resp.SKUs[0].Rates["prompt_tokens"])
				require.Equal(t, "10.000000", resp.SKUs[0].Rates["completion_tokens"])
				require.Empty(t, resp.VolumeTiers)
				require.Empty(t, resp.CommittedUse)
			},
		},
		{
			name:       "PriceBookPin resolution",
			query:      "/v1/pricing?tenant=pinned-tenant&namespace=billing-demo",
			objects:    []runtime.Object{tenantWithPin, pinnedPB, planWithRef, activePB},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp pricingResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Equal(t, "legacy-v0", resp.PriceBook)
				require.Equal(t, "EUR", resp.Currency)
				require.Len(t, resp.SKUs, 1)
				require.Equal(t, "llama-3-70b", resp.SKUs[0].SKU)
			},
		},
		{
			name:       "fallback to Active PriceBook",
			query:      "/v1/pricing?tenant=fallback-tenant&namespace=billing-demo",
			objects:    []runtime.Object{tenantNoPlanRef, planNoRef, activePB},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp pricingResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Equal(t, "standard-v1", resp.PriceBook)
			},
		},
		{
			name:       "no active PriceBook returns 404",
			query:      "/v1/pricing?tenant=fallback-tenant&namespace=billing-demo",
			objects:    []runtime.Object{tenantNoPlanRef, planNoRef, pinnedPB}, // pinnedPB is Superseded, not Active
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h *pricingHandler
			if tt.nilReader {
				h = &pricingHandler{reader: nil, defaultNS: "billing-demo"}
			} else {
				scheme := newTestScheme(t)
				fb := fake.NewClientBuilder().WithScheme(scheme)
				if tt.objects != nil {
					fb = fb.WithRuntimeObjects(tt.objects...)
				}
				h = &pricingHandler{reader: fb.Build(), defaultNS: "billing-demo"}
			}

			req := httptest.NewRequest(http.MethodGet, tt.query, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, "body: %s", rec.Body.String())
			if tt.checkBody != nil {
				tt.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

func TestPricingHandlerMethodNotAllowed(t *testing.T) {
	h := &pricingHandler{defaultNS: "default"}
	req := httptest.NewRequest(http.MethodPost, "/v1/pricing?tenant=t", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
