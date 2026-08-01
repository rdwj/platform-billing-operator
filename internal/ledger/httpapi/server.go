package httpapi

import (
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

// NewServer returns an http.Handler with billing-api routes:
// /v1/ingest, /v1/usage, /v1/balance, /v1/adjustments, /v1/pricing, /healthz.
func NewServer(ingester *ledger.Ingester, store *ledger.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", &ingestHandler{ingester: ingester})
	mux.Handle("/v1/usage", &usageHandler{store: store})
	mux.Handle("/v1/balance", &balanceHandler{store: store})
	mux.Handle("/v1/adjustments", &adjustmentHandler{store: store})
	mux.Handle("/v1/pricing", &pricingHandler{})
	mux.HandleFunc("/healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n")) //nolint:errcheck
}
