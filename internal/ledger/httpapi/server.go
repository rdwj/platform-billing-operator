package httpapi

import (
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

// NewServer returns an http.Handler with /v1/ingest and /healthz routes.
func NewServer(ingester *ledger.Ingester) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", &ingestHandler{ingester: ingester})
	mux.HandleFunc("/healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n")) //nolint:errcheck
}
