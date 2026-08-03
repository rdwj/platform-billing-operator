package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type driftHandler struct {
	store *ledger.Store
}

type driftReportResponse struct {
	ID           int64  `json:"id"`
	TenantID     string `json:"tenant_id"`
	WindowStart  string `json:"window_start"`
	WindowEnd    string `json:"window_end"`
	LedgerTokens int64  `json:"ledger_tokens"`
	PromTokens   int64  `json:"prom_tokens"`
	DriftPct     string `json:"drift_pct"`
	TolerancePct string `json:"tolerance_pct"`
	Exceeded     bool   `json:"exceeded"`
	Signature    string `json:"signature"`
	CreatedAt    string `json:"created_at"`
}

func (h *driftHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	// Dispatch: /v1/drift/latest vs /v1/drift
	path := strings.TrimPrefix(r.URL.Path, "/v1/drift")
	if path == "/latest" {
		h.handleLatest(w, r, tenant)
		return
	}
	h.handleList(w, r, tenant)
}

func (h *driftHandler) handleList(w http.ResponseWriter, r *http.Request, tenant string) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	reports, err := h.store.QueryDriftReports(r.Context(), tenant, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]driftReportResponse, 0, len(reports))
	for _, rpt := range reports {
		resp = append(resp, toDriftResponse(rpt))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": resp})
}

func (h *driftHandler) handleLatest(w http.ResponseWriter, r *http.Request, tenant string) {
	rpt, err := h.store.QueryLatestDriftReport(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rpt == nil {
		writeError(w, http.StatusNotFound, "no drift reports for tenant")
		return
	}
	writeJSON(w, http.StatusOK, toDriftResponse(*rpt))
}

func toDriftResponse(r ledger.DriftReport) driftReportResponse {
	return driftReportResponse{
		ID:           r.ID,
		TenantID:     r.TenantID,
		WindowStart:  r.WindowStart.UTC().Format(time.RFC3339),
		WindowEnd:    r.WindowEnd.UTC().Format(time.RFC3339),
		LedgerTokens: r.LedgerTokens,
		PromTokens:   r.PromTokens,
		DriftPct:     r.DriftPct,
		TolerancePct: r.TolerancePct,
		Exceeded:     r.Exceeded,
		Signature:    r.Signature,
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339),
	}
}
