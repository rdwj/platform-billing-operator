package httpapi

import (
	"encoding/csv"
	"net/http"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type exportHandler struct {
	store *ledger.Store
}

type exportResponse struct {
	Rows []ledger.FOCUSRow `json:"rows"`
}

func (h *exportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	fromStr := r.URL.Query().Get("from")
	if fromStr == "" {
		writeError(w, http.StatusBadRequest, "from is required")
		return
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from: invalid RFC3339 timestamp")
		return
	}

	toStr := r.URL.Query().Get("to")
	if toStr == "" {
		writeError(w, http.StatusBadRequest, "to is required")
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to: invalid RFC3339 timestamp")
		return
	}

	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, "from must be before to")
		return
	}

	rows, err := h.store.QueryExportData(r.Context(), tenant, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if wantCSV(r) {
		writeCSV(w, rows)
		return
	}

	writeJSON(w, http.StatusOK, exportResponse{Rows: rows})
}

// wantCSV returns true when the caller requested CSV output. The format query
// parameter takes precedence; if absent, the Accept header is checked.
func wantCSV(r *http.Request) bool {
	if f := r.URL.Query().Get("format"); f != "" {
		return f == "csv"
	}
	return r.Header.Get("Accept") == "text/csv"
}

func writeCSV(w http.ResponseWriter, rows []ledger.FOCUSRow) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="focus-export.csv"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	cw.Write(ledger.FOCUSCSVHeader()) //nolint:errcheck
	for i := range rows {
		cw.Write(rows[i].CSVRecord()) //nolint:errcheck
	}
	cw.Flush()
}
