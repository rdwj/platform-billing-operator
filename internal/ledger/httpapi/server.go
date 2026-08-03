package httpapi

import (
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewServer returns an http.Handler with billing-api routes:
// /v1/ingest, /v1/usage, /v1/balance, /v1/adjustments, /v1/pricing,
// /v1/keys, /v1/keys/disable, /v1/keys/enable, /v1/drift, /v1/drift/latest,
// /healthz.
//
// k8sReader is a Kubernetes client.Reader used by the pricing handler to resolve
// PriceBook CRs. It may be nil if no kubeconfig is available; in that case
// /v1/pricing returns 503. defaultNS is the namespace used when the caller does
// not supply a namespace query parameter.
func NewServer(ingester *ledger.Ingester, store *ledger.Store, k8sReader client.Reader, defaultNS string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", &ingestHandler{ingester: ingester})
	mux.Handle("/v1/usage", &usageHandler{store: store})
	mux.Handle("/v1/balance", &balanceHandler{store: store})
	mux.Handle("/v1/adjustments", &adjustmentHandler{store: store})
	mux.Handle("/v1/pricing", &pricingHandler{reader: k8sReader, defaultNS: defaultNS})
	mux.Handle("/v1/drift", &driftHandler{store: store})
	mux.Handle("/v1/drift/latest", &driftHandler{store: store})
	mux.Handle("/v1/keys", &keysHandler{store: store})
	mux.Handle("/v1/keys/disable", &keysHandler{store: store})
	mux.Handle("/v1/keys/enable", &keysHandler{store: store})
	mux.HandleFunc("/healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n")) //nolint:errcheck
}
