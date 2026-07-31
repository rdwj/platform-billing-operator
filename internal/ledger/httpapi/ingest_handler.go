package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type ingestHandler struct {
	ingester *ledger.Ingester
}

type ingestRequest struct {
	EventJSON string `json:"event_json"`
}

type ingestResponse struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

func (h *ingestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ingestResponse{
			Result:  "error",
			Message: "invalid JSON body",
		})
		return
	}

	if req.EventJSON == "" {
		writeJSON(w, http.StatusBadRequest, ingestResponse{
			Result:  "error",
			Message: "event_json is required",
		})
		return
	}

	result, err := h.ingester.IngestEvent(r.Context(), json.RawMessage(req.EventJSON))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ingestResponse{
			Result:  "error",
			Message: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, ingestResponse{
		Result:  string(result),
		Message: string(result),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
