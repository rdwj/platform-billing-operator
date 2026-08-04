package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type invoiceHandler struct {
	store *ledger.Store
}

type invoiceResponse struct {
	InvoiceID   string `json:"invoice_id"`
	TenantID    string `json:"tenant_id"`
	Period      string `json:"period"`
	Status      string `json:"status"`
	Total       string `json:"total"`
	PSPRef      string `json:"psp_ref,omitempty"`
	FinalizedAt string `json:"finalized_at,omitempty"`
}

func (h *invoiceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/invoices")
	if path != "" && path != "/" {
		invoiceID := strings.TrimPrefix(path, "/")
		h.handleGet(w, r, invoiceID)
		return
	}
	h.handleList(w, r)
}

func (h *invoiceHandler) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	invoices, err := h.store.QueryInvoices(r.Context(), tenant, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]invoiceResponse, 0, len(invoices))
	for _, inv := range invoices {
		resp = append(resp, toInvoiceResponse(inv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": resp})
}

func (h *invoiceHandler) handleGet(w http.ResponseWriter, r *http.Request, invoiceID string) {
	inv, err := h.store.GetInvoiceWithLines(r.Context(), invoiceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inv == nil {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func toInvoiceResponse(inv ledger.Invoice) invoiceResponse {
	resp := invoiceResponse{
		InvoiceID: inv.InvoiceID,
		TenantID:  inv.TenantID,
		Period:    inv.Period,
		Status:    inv.Status,
		Total:     inv.Total,
		PSPRef:    inv.PSPRef,
	}
	if inv.FinalizedAt != nil {
		resp.FinalizedAt = inv.FinalizedAt.UTC().Format(time.RFC3339)
	}
	return resp
}
