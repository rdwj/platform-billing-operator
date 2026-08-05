package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type ledgerHandler struct {
	store *ledger.Store
}

type ledgerEntryResponse struct {
	Timestamp    string `json:"timestamp"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	Amount       string `json:"amount"`
	BalanceAfter string `json:"balance_after"`
	RefID        string `json:"ref_id"`
}

type ledgerResponse struct {
	Tenant  string                `json:"tenant"`
	Entries []ledgerEntryResponse `json:"entries"`
}

func (h *ledgerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := h.store.QueryLedger(r.Context(), tenant, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]ledgerEntryResponse, 0, len(entries))
	for _, e := range entries {
		resp = append(resp, ledgerEntryResponse{
			Timestamp:    e.Timestamp.UTC().Format(time.RFC3339),
			Type:         e.Type,
			Description:  e.Description,
			Amount:       e.Amount,
			BalanceAfter: e.BalanceAfter,
			RefID:        e.RefID,
		})
	}

	writeJSON(w, http.StatusOK, ledgerResponse{
		Tenant:  tenant,
		Entries: resp,
	})
}
