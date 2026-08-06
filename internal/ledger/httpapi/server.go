package httpapi

import (
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewServer returns an http.Handler with billing-api routes.
//
// k8sClient is a Kubernetes client.Client used by handlers that resolve or
// mutate CRs. Handlers that only need read access use the embedded Reader
// interface. It may be nil if no kubeconfig is available; in that case those
// endpoints return 503. defaultNS is the namespace used when the caller does
// not supply a namespace query parameter.
func NewServer(ingester *ledger.Ingester, store *ledger.Store, k8sClient client.Client, defaultNS string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", &ingestHandler{ingester: ingester})
	mux.Handle("/v1/usage", &usageHandler{store: store})
	mux.Handle("/v1/balance", &balanceHandler{store: store})
	mux.Handle("/v1/adjustments", &adjustmentHandler{store: store})
	mux.Handle("/v1/pricing", &pricingHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/tenants", &tenantsHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/ledger", &ledgerHandler{store: store})
	mux.Handle("/v1/budgets", &budgetsHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/plans", &plansHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/pricebooks", &pricebooksHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/drift", &driftHandler{store: store})
	mux.Handle("/v1/drift/latest", &driftHandler{store: store})
	mux.Handle("/v1/export", &exportHandler{store: store})
	mux.Handle("/v1/invoices", &invoiceHandler{store: store})
	mux.Handle("/v1/invoices/", &invoiceHandler{store: store})
	mux.Handle("/v1/keys", &keysHandler{store: store})
	mux.Handle("/v1/keys/disable", &keysHandler{store: store})
	mux.Handle("/v1/keys/enable", &keysHandler{store: store})
	mux.Handle("/v1/connectors", &connectorsHandler{reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/webhooks", &webhooksHandler{store: store})
	mux.Handle("/v1/overview", &overviewHandler{store: store, reader: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/reservations", &reservationsHandler{client: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/reservations/", &reservationsHandler{client: k8sClient, defaultNS: defaultNS})
	mux.Handle("/v1/health", &healthHandler{store: store})
	mux.HandleFunc("/healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n")) //nolint:errcheck
}
