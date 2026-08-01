package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/shopspring/decimal"
)

type adjustmentHandler struct {
	store *ledger.Store
}

type adjustmentRequest struct {
	Tenant         string `json:"tenant"`
	Amount         string `json:"amount"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type adjustmentResponse struct {
	ID             string `json:"id"`
	Tenant         string `json:"tenant"`
	Amount         string `json:"amount"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
	CreatedAt      string `json:"created_at"`
}

func (h *adjustmentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req adjustmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Tenant == "" || req.Amount == "" || req.Kind == "" || req.Reason == "" || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "tenant, amount, kind, reason, and idempotency_key are required")
		return
	}

	if len(req.IdempotencyKey) < 8 {
		writeError(w, http.StatusBadRequest, "idempotency_key must be at least 8 characters")
		return
	}

	if _, err := decimal.NewFromString(req.Amount); err != nil {
		writeError(w, http.StatusBadRequest, "amount must be a valid decimal string")
		return
	}

	switch req.Kind {
	case "manual_credit", "manual_debit", "balance_carry_in", "writeoff", "unmetered_gap":
	default:
		writeError(w, http.StatusBadRequest,
			"kind must be one of: manual_credit, manual_debit, balance_carry_in, writeoff, unmetered_gap")
		return
	}

	row, err := h.store.InsertAdjustment(r.Context(), ledger.AdjustmentInput{
		TenantID:       req.Tenant,
		Amount:         req.Amount,
		Kind:           req.Kind,
		Reason:         req.Reason,
		Actor:          "billctl",
		IdempotencyKey: req.IdempotencyKey,
	})
	if errors.Is(err, ledger.ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "idempotency key replay with different content")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	amt, _ := decimal.NewFromString(row.Amount)
	writeJSON(w, http.StatusCreated, adjustmentResponse{
		ID:             row.ID,
		Tenant:         row.TenantID,
		Amount:         amt.StringFixed(6),
		Kind:           row.Kind,
		Reason:         row.Reason,
		IdempotencyKey: row.IdempotencyKey,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339),
	})
}
