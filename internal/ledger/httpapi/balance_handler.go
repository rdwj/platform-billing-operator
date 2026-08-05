package httpapi

import (
	"net/http"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/shopspring/decimal"
)

type balanceHandler struct {
	store *ledger.Store
}

type balanceResponse struct {
	Tenant   string `json:"tenant"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
	State    string `json:"state"`
	AsOf     string `json:"as_of"`
}

func (h *balanceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	balance, latestAt, err := h.store.QueryTenantBalance(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	bal, _ := decimal.NewFromString(balance)
	asOf := latestAt
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}

	writeJSON(w, http.StatusOK, balanceResponse{
		Tenant:   tenant,
		Currency: "USD",
		Balance:  bal.StringFixed(6),
		State:    billingv1alpha1.StateActive,
		AsOf:     asOf.UTC().Format(time.RFC3339),
	})
}
