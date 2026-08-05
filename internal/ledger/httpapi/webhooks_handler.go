package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type webhooksHandler struct {
	store *ledger.Store
}

type webhooksResponse struct {
	Webhooks []webhookDeliveryResponse `json:"webhooks"`
}

type webhookDeliveryResponse struct {
	DeliveryID  string `json:"delivery_id"`
	EventType   string `json:"event_type"`
	TenantID    string `json:"tenant_id,omitempty"`
	Endpoint    string `json:"endpoint"`
	StatusCode  *int   `json:"status_code,omitempty"`
	Attempts    int    `json:"attempts"`
	NextRetryAt string `json:"next_retry_at,omitempty"`
	DeliveredAt string `json:"delivered_at,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func (h *webhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	deliveries, err := h.store.QueryWebhookDeliveries(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]webhookDeliveryResponse, 0, len(deliveries))
	for _, d := range deliveries {
		wr := webhookDeliveryResponse{
			DeliveryID: d.DeliveryID,
			EventType:  d.EventType,
			TenantID:   d.TenantID,
			Endpoint:   d.Endpoint,
			StatusCode: d.StatusCode,
			Attempts:   d.Attempts,
			CreatedAt:  d.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d.NextRetryAt != nil {
			wr.NextRetryAt = d.NextRetryAt.UTC().Format(time.RFC3339)
		}
		if d.DeliveredAt != nil {
			wr.DeliveredAt = d.DeliveredAt.UTC().Format(time.RFC3339)
		}
		resp = append(resp, wr)
	}

	writeJSON(w, http.StatusOK, webhooksResponse{Webhooks: resp})
}
