package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestTenantsHandler(t *testing.T) {
	prepaidTenant := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-tenant", Namespace: "billing-demo"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Acme Corp",
			PlanRef:     corev1.LocalObjectReference{Name: "growth"},
			BillingMode: "Prepaid",
			ReloadPolicy: &billingv1alpha1.ReloadPolicy{
				Enabled:          true,
				ThresholdAmount:  "1000.000000",
				ReloadAmount:     "5000.000000",
				MonthlyCapAmount: "20000.000000",
			},
		},
		Status: billingv1alpha1.TenantAccountStatus{
			State:      "Active",
			Balance:    "5000.000000",
			MtdSpend:   "1200.000000",
			RunwayDays: "12.500000",
		},
	}

	invoicedTenant := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "bigco-tenant", Namespace: "billing-demo"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "BigCo Inc",
			PlanRef:     corev1.LocalObjectReference{Name: "enterprise"},
			BillingMode: "Invoiced",
		},
		Status: billingv1alpha1.TenantAccountStatus{
			State: "Active",
		},
	}

	tests := []struct {
		name       string
		method     string
		query      string
		objects    []runtime.Object
		nilReader  bool
		wantStatus int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "happy path — prepaid tenant with reload policy",
			method:     http.MethodGet,
			query:      "/v1/tenants?namespace=billing-demo",
			objects:    []runtime.Object{prepaidTenant},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp tenantsResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp.Tenants, 1)

				ts := resp.Tenants[0]
				require.Equal(t, "acme-tenant", ts.TenantID)
				require.Equal(t, "Acme Corp", ts.Name)
				require.Equal(t, "billing-demo", ts.Namespace)
				require.Equal(t, "growth", ts.Plan)
				require.Equal(t, "Active", ts.Status)
				require.Equal(t, "5000.000000", ts.Balance)
				require.Equal(t, "Prepaid", ts.BillingMode)
				require.Equal(t, "1200.000000", ts.MtdSpend)
				require.Equal(t, "12.500000", ts.RunwayDays)
				require.True(t, ts.ReloadEnabled)
				require.Equal(t, "400.000000", ts.BurnPerDay)
				require.Equal(t, "1000.000000", ts.ReloadThreshold)
				require.Equal(t, "5000.000000", ts.ReloadAmount)
				require.Equal(t, "20000.000000", ts.MonthlyCap)
			},
		},
		{
			name:       "invoiced tenant — no balance",
			method:     http.MethodGet,
			query:      "/v1/tenants?namespace=billing-demo",
			objects:    []runtime.Object{invoicedTenant},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp tenantsResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp.Tenants, 1)

				ts := resp.Tenants[0]
				require.Equal(t, "bigco-tenant", ts.TenantID)
				require.Equal(t, "Invoiced", ts.BillingMode)
				require.Empty(t, ts.Balance)
				require.False(t, ts.ReloadEnabled)
				require.Empty(t, ts.ReloadThreshold)
				require.Empty(t, ts.ReloadAmount)
				require.Empty(t, ts.MonthlyCap)
				require.Empty(t, ts.BurnPerDay)
			},
		},
		{
			name:       "nil reader returns 503",
			method:     http.MethodGet,
			query:      "/v1/tenants?namespace=billing-demo",
			nilReader:  true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "method not allowed",
			method:     http.MethodPost,
			query:      "/v1/tenants?namespace=billing-demo",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "empty namespace uses default",
			method:     http.MethodGet,
			query:      "/v1/tenants",
			objects:    []runtime.Object{prepaidTenant},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp tenantsResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				// prepaidTenant is in billing-demo, but query uses defaultNS
				// (also billing-demo), so we still find it.
				require.Len(t, resp.Tenants, 1)
				require.Equal(t, "acme-tenant", resp.Tenants[0].TenantID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h *tenantsHandler
			if tt.nilReader {
				h = &tenantsHandler{reader: nil, defaultNS: "billing-demo"}
			} else {
				scheme := newTestScheme(t)
				fb := fake.NewClientBuilder().WithScheme(scheme)
				if tt.objects != nil {
					fb = fb.WithRuntimeObjects(tt.objects...)
				}
				h = &tenantsHandler{reader: fb.Build(), defaultNS: "billing-demo"}
			}

			req := httptest.NewRequest(tt.method, tt.query, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, "body: %s", rec.Body.String())
			if tt.checkBody != nil {
				tt.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}
