package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type dlqHandler struct {
	store    *ledger.Store
	ingester *ledger.Ingester
}

type dlqReplayRequest struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

type dlqReplayError struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type dlqReplayResponse struct {
	Replayed int              `json:"replayed"`
	Failed   int              `json:"failed"`
	Errors   []dlqReplayError `json:"errors"`
}

func (h *dlqHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch {
	case path == "/v1/dlq" && r.Method == http.MethodGet:
		h.handleList(w, r)
	case path == "/v1/dlq/replay" && r.Method == http.MethodPost:
		h.handleReplay(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *dlqHandler) handleList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := h.store.ListDLQ(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []ledger.DLQEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *dlqHandler) handleReplay(w http.ResponseWriter, r *http.Request) {
	var req dlqReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if !req.All && len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "provide 'ids' array or set 'all' to true")
		return
	}

	ctx := r.Context()
	var entries []ledger.DLQEntry

	if req.All {
		listed, err := h.store.ListDLQ(ctx, 1000)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		entries = listed
	} else {
		for _, id := range req.IDs {
			entry, err := h.store.GetDLQEntry(ctx, id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if entry == nil {
				// Entry not found or already replayed -- report as error in summary
				entries = append(entries, ledger.DLQEntry{ID: id})
				continue
			}
			entries = append(entries, *entry)
		}
	}

	resp := dlqReplayResponse{
		Errors: []dlqReplayError{},
	}

	for _, entry := range entries {
		if entry.Raw == nil {
			// Entry was requested by ID but not found
			resp.Failed++
			resp.Errors = append(resp.Errors, dlqReplayError{
				ID:    entry.ID,
				Error: "entry not found or already replayed",
			})
			continue
		}

		_, err := h.ingester.IngestEvent(ctx, entry.Raw)
		if err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, dlqReplayError{
				ID:    entry.ID,
				Error: err.Error(),
			})
			if incErr := h.store.IncrementDLQAttempts(ctx, entry.ID, err.Error()); incErr != nil {
				resp.Errors = append(resp.Errors, dlqReplayError{
					ID:    entry.ID,
					Error: "failed to increment attempts: " + incErr.Error(),
				})
			}
			continue
		}

		if err := h.store.ReplayDLQEntry(ctx, entry.ID); err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, dlqReplayError{
				ID:    entry.ID,
				Error: "ingested but failed to mark replayed: " + err.Error(),
			})
			continue
		}
		resp.Replayed++
	}

	writeJSON(w, http.StatusOK, resp)
}
