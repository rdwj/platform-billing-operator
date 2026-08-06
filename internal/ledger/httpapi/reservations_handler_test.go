package httpapi

import (
	"bytes"
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
)

func newTestReservation(name, tenant, state string) *billingv1alpha1.Reservation {
	return &billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "billing-demo"},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:    tenant,
			SKU:          "h200",
			Count:        4,
			Geo:          "us-east-1",
			Class:        billingv1alpha1.ReservationClassGuaranteed,
			MonthlyPrice: "12000.000000",
			Window: billingv1alpha1.ReservationWindow{
				Start: metav1.NewTime(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)),
				End:   metav1.NewTime(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
			},
		},
		Status: billingv1alpha1.ReservationStatus{
			State: state,
		},
	}
}

func TestReservationsHandler_List(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		objects    []runtime.Object
		nilClient  bool
		wantStatus int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "nil client returns 503",
			query:      "/v1/reservations",
			nilClient:  true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "empty list",
			query:      "/v1/reservations",
			objects:    nil,
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp map[string][]reservationResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Empty(t, resp["reservations"])
			},
		},
		{
			name:  "list all reservations",
			query: "/v1/reservations",
			objects: []runtime.Object{
				newTestReservation("res-acme-1", "acme", billingv1alpha1.ReservationStateActive),
				newTestReservation("res-bigco-1", "bigco", billingv1alpha1.ReservationStateConfirmed),
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp map[string][]reservationResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp["reservations"], 2)
			},
		},
		{
			name:  "filter by tenant",
			query: "/v1/reservations?tenant=acme",
			objects: []runtime.Object{
				newTestReservation("res-acme-1", "acme", billingv1alpha1.ReservationStateActive),
				newTestReservation("res-bigco-1", "bigco", billingv1alpha1.ReservationStateConfirmed),
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp map[string][]reservationResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp["reservations"], 1)
				require.Equal(t, "acme", resp["reservations"][0].Tenant)
				require.Equal(t, "res-acme-1", resp["reservations"][0].Name)
			},
		},
		{
			name:  "response fields populated correctly",
			query: "/v1/reservations",
			objects: []runtime.Object{
				newTestReservation("res-acme-1", "acme", billingv1alpha1.ReservationStateActive),
			},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp map[string][]reservationResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Len(t, resp["reservations"], 1)
				r := resp["reservations"][0]
				require.Equal(t, "res-acme-1", r.Name)
				require.Equal(t, "acme", r.Tenant)
				require.Equal(t, "h200", r.SKU)
				require.Equal(t, int32(4), r.Count)
				require.Equal(t, "us-east-1", r.Geo)
				require.Equal(t, "Guaranteed", r.Class)
				require.Equal(t, "12000.000000", r.MonthlyPrice)
				require.Equal(t, "Active", r.State)
				require.Equal(t, "2026-08-01T00:00:00Z", r.Window.Start)
				require.Equal(t, "2026-11-01T00:00:00Z", r.Window.End)
				require.Empty(t, r.CancelledAt)
				require.Nil(t, r.CapacityCheck)
			},
		},
		{
			name:       "method not allowed",
			query:      "/v1/reservations",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h *reservationsHandler
			if tt.nilClient {
				h = &reservationsHandler{client: nil, defaultNS: "billing-demo"}
			} else {
				scheme := newTestScheme(t)
				fb := fake.NewClientBuilder().WithScheme(scheme)
				if tt.objects != nil {
					fb = fb.WithRuntimeObjects(tt.objects...)
				}
				h = &reservationsHandler{client: fb.Build(), defaultNS: "billing-demo"}
			}

			method := http.MethodGet
			if tt.name == "method not allowed" {
				method = http.MethodDelete
			}
			req := httptest.NewRequest(method, tt.query, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, "body: %s", rec.Body.String())
			if tt.checkBody != nil {
				tt.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

func TestReservationsHandler_Get(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		objects    []runtime.Object
		wantStatus int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "found",
			path:       "/v1/reservations/res-acme-1",
			objects:    []runtime.Object{newTestReservation("res-acme-1", "acme", billingv1alpha1.ReservationStateActive)},
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				var resp reservationResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				require.Equal(t, "res-acme-1", resp.Name)
				require.Equal(t, "Active", resp.State)
			},
		},
		{
			name:       "not found",
			path:       "/v1/reservations/nonexistent",
			objects:    []runtime.Object{},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			fb := fake.NewClientBuilder().WithScheme(scheme)
			if tt.objects != nil {
				fb = fb.WithRuntimeObjects(tt.objects...)
			}
			h := &reservationsHandler{client: fb.Build(), defaultNS: "billing-demo"}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, "body: %s", rec.Body.String())
			if tt.checkBody != nil {
				tt.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

func TestReservationsHandler_Create(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		body := createReservationRequest{
			Tenant:       "acme",
			SKU:          "h200",
			Count:        2,
			Geo:          "us-east-1",
			Class:        "Guaranteed",
			Start:        "2026-09-01T00:00:00Z",
			End:          "2026-12-01T00:00:00Z",
			MonthlyPrice: "8000.000000",
		}
		b, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

		var resp reservationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, "acme", resp.Tenant)
		require.Equal(t, "h200", resp.SKU)
		require.Equal(t, int32(2), resp.Count)
		require.Equal(t, "Guaranteed", resp.Class)
		require.Equal(t, "8000.000000", resp.MonthlyPrice)
		require.Equal(t, "2026-09-01T00:00:00Z", resp.Window.Start)
		require.Equal(t, "2026-12-01T00:00:00Z", resp.Window.End)
	})

	t.Run("missing required fields", func(t *testing.T) {
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		body := createReservationRequest{SKU: "h200"} // missing tenant, geo, etc.
		b, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid start time", func(t *testing.T) {
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		body := createReservationRequest{
			Tenant:       "acme",
			SKU:          "h200",
			Count:        1,
			Geo:          "us-east-1",
			Class:        "Guaranteed",
			Start:        "not-a-time",
			End:          "2026-12-01T00:00:00Z",
			MonthlyPrice: "1000.000000",
		}
		b, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestReservationsHandler_Cancel(t *testing.T) {
	t.Run("active reservation cancelled", func(t *testing.T) {
		res := newTestReservation("res-acme-1", "acme", billingv1alpha1.ReservationStateActive)
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(res).
			WithStatusSubresource(&billingv1alpha1.Reservation{}).
			Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations/res-acme-1/cancel", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

		var resp reservationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, billingv1alpha1.ReservationStateCancelled, resp.State)
		require.NotEmpty(t, resp.CancelledAt)
	})

	t.Run("confirmed reservation cancelled", func(t *testing.T) {
		res := newTestReservation("res-acme-2", "acme", billingv1alpha1.ReservationStateConfirmed)
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(res).
			WithStatusSubresource(&billingv1alpha1.Reservation{}).
			Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations/res-acme-2/cancel", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("already cancelled returns 409", func(t *testing.T) {
		res := newTestReservation("res-acme-3", "acme", billingv1alpha1.ReservationStateCancelled)
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(res).
			WithStatusSubresource(&billingv1alpha1.Reservation{}).
			Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations/res-acme-3/cancel", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("expired returns 409", func(t *testing.T) {
		res := newTestReservation("res-acme-4", "acme", billingv1alpha1.ReservationStateExpired)
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(res).
			WithStatusSubresource(&billingv1alpha1.Reservation{}).
			Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations/res-acme-4/cancel", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("not found returns 404", func(t *testing.T) {
		scheme := newTestScheme(t)
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
		h := &reservationsHandler{client: fc, defaultNS: "billing-demo"}

		req := httptest.NewRequest(http.MethodPost, "/v1/reservations/nonexistent/cancel", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}
