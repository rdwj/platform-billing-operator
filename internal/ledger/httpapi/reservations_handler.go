package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type reservationsHandler struct {
	client    client.Client
	defaultNS string
}

type reservationResponse struct {
	Name                       string                    `json:"name"`
	Tenant                     string                    `json:"tenant"`
	SKU                        string                    `json:"sku"`
	Count                      int32                     `json:"count"`
	Geo                        string                    `json:"geo"`
	Class                      string                    `json:"class"`
	MinAllocationSeconds       *int32                    `json:"minAllocationSeconds,omitempty"`
	EvictionCreditPerGpuSecond *string                   `json:"evictionCreditPerGpuSecond,omitempty"`
	Window                     reservationWindowResponse `json:"window"`
	MonthlyPrice               string                    `json:"monthlyPrice"`
	State                      string                    `json:"state"`
	CancelledAt                string                    `json:"cancelledAt,omitempty"`
	CapacityCheck              *capacityCheckResponse    `json:"capacityCheck,omitempty"`
}

type reservationWindowResponse struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type capacityCheckResponse struct {
	Available bool   `json:"available"`
	Pool      string `json:"pool,omitempty"`
}

type createReservationRequest struct {
	Tenant                     string  `json:"tenant"`
	SKU                        string  `json:"sku"`
	Count                      int32   `json:"count"`
	Geo                        string  `json:"geo"`
	Class                      string  `json:"class"`
	MinAllocationSeconds       *int32  `json:"minAllocationSeconds,omitempty"`
	EvictionCreditPerGpuSecond *string `json:"evictionCreditPerGpuSecond,omitempty"`
	Start                      string  `json:"start"`
	End                        string  `json:"end"`
	MonthlyPrice               string  `json:"monthlyPrice"`
}

func (h *reservationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "reservations require Kubernetes access")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/reservations")

	// POST /v1/reservations/{name}/cancel
	if strings.HasSuffix(path, "/cancel") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/cancel")
		h.handleCancel(w, r, name)
		return
	}

	// GET or POST /v1/reservations (no sub-path)
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			h.handleList(w, r)
		case http.MethodPost:
			h.handleCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// GET /v1/reservations/{name}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path, "/")
	h.handleGet(w, r, name)
}

func (h *reservationsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var list billingv1alpha1.ReservationList
	if err := h.client.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	tenant := r.URL.Query().Get("tenant")
	items := make([]reservationResponse, 0, len(list.Items))
	for i := range list.Items {
		res := &list.Items[i]
		if tenant != "" && res.Spec.TenantRef != tenant {
			continue
		}
		items = append(items, toReservationResponse(res))
	}

	writeJSON(w, http.StatusOK, map[string]any{"reservations": items})
}

func (h *reservationsHandler) handleGet(w http.ResponseWriter, r *http.Request, name string) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var res billingv1alpha1.Reservation
	if err := h.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &res); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("reservation %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, toReservationResponse(&res))
}

func (h *reservationsHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}

	if req.Tenant == "" || req.SKU == "" || req.Geo == "" || req.Class == "" || req.MonthlyPrice == "" {
		writeError(w, http.StatusBadRequest, "tenant, sku, geo, class, and monthlyPrice are required")
		return
	}
	if req.Count < 1 {
		writeError(w, http.StatusBadRequest, "count must be at least 1")
		return
	}

	startTime, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid start time: %v", err))
		return
	}
	endTime, err := time.Parse(time.RFC3339, req.End)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid end time: %v", err))
		return
	}

	res := &billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("res-%s-", req.Tenant),
			Namespace:    h.defaultNS,
		},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:                  req.Tenant,
			SKU:                        req.SKU,
			Count:                      req.Count,
			Geo:                        req.Geo,
			Class:                      req.Class,
			MinAllocationSeconds:       req.MinAllocationSeconds,
			EvictionCreditPerGpuSecond: req.EvictionCreditPerGpuSecond,
			MonthlyPrice:               req.MonthlyPrice,
			Window: billingv1alpha1.ReservationWindow{
				Start: metav1.NewTime(startTime),
				End:   metav1.NewTime(endTime),
			},
		},
	}

	if err := h.client.Create(r.Context(), res); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create reservation: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, toReservationResponse(res))
}

func (h *reservationsHandler) handleCancel(w http.ResponseWriter, r *http.Request, name string) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var res billingv1alpha1.Reservation
	if err := h.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &res); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("reservation %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	state := res.Status.State
	if state != billingv1alpha1.ReservationStateActive && state != billingv1alpha1.ReservationStateConfirmed {
		writeError(w, http.StatusConflict, fmt.Sprintf("reservation is %s, only Active or Confirmed reservations can be cancelled", state))
		return
	}

	patch := client.MergeFrom(res.DeepCopy())
	now := metav1.Now()
	res.Status.State = billingv1alpha1.ReservationStateCancelled
	res.Status.CancelledAt = &now

	if err := h.client.Status().Patch(r.Context(), &res, patch); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to cancel reservation: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, toReservationResponse(&res))
}

func toReservationResponse(res *billingv1alpha1.Reservation) reservationResponse {
	resp := reservationResponse{
		Name:                       res.Name,
		Tenant:                     res.Spec.TenantRef,
		SKU:                        res.Spec.SKU,
		Count:                      res.Spec.Count,
		Geo:                        res.Spec.Geo,
		Class:                      res.Spec.Class,
		MinAllocationSeconds:       res.Spec.MinAllocationSeconds,
		EvictionCreditPerGpuSecond: res.Spec.EvictionCreditPerGpuSecond,
		MonthlyPrice:               res.Spec.MonthlyPrice,
		State:                      res.Status.State,
		Window: reservationWindowResponse{
			Start: res.Spec.Window.Start.UTC().Format(time.RFC3339),
			End:   res.Spec.Window.End.UTC().Format(time.RFC3339),
		},
	}
	if res.Status.CancelledAt != nil {
		resp.CancelledAt = res.Status.CancelledAt.UTC().Format(time.RFC3339)
	}
	if res.Status.CapacityCheck != nil {
		resp.CapacityCheck = &capacityCheckResponse{
			Available: res.Status.CapacityCheck.Available,
			Pool:      res.Status.CapacityCheck.Pool,
		}
	}
	return resp
}
