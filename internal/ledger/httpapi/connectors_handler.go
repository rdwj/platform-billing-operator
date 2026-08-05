package httpapi

import (
	"fmt"
	"net/http"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type connectorsHandler struct {
	reader    client.Reader
	defaultNS string
}

type connectorsResponse struct {
	Connectors []connectorSummary `json:"connectors"`
}

type connectorSummary struct {
	Name         string                 `json:"name"`
	Namespace    string                 `json:"namespace"`
	Type         string                 `json:"type"`
	Sync         *connectorSyncResponse `json:"sync,omitempty"`
	Connected    bool                   `json:"connected"`
	LastSync     *connectorLastSync     `json:"lastSync,omitempty"`
	WebhookStats *connectorWebhookStats `json:"webhookStats,omitempty"`
	Conditions   []connectorCondition   `json:"conditions"`
}

type connectorSyncResponse struct {
	UsageRecordsInterval string `json:"usageRecordsInterval"`
	InvoiceInterval      string `json:"invoiceInterval"`
}

type connectorLastSync struct {
	At            string `json:"at"`
	PushedRecords int64  `json:"pushedRecords"`
	Failed        int64  `json:"failed"`
}

type connectorWebhookStats struct {
	Received24h       int64 `json:"received24h"`
	SignatureFailures int64 `json:"signatureFailures"`
}

type connectorCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (h *connectorsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "connectors listing requires Kubernetes access")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var list billingv1alpha1.BillingConnectorList
	if err := h.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	connectors := make([]connectorSummary, 0, len(list.Items))
	for i := range list.Items {
		bc := &list.Items[i]

		cs := connectorSummary{
			Name:      bc.Name,
			Namespace: bc.Namespace,
			Type:      bc.Spec.Type,
			Connected: bc.Status.Connected,
		}

		if bc.Spec.Sync != nil {
			cs.Sync = &connectorSyncResponse{
				UsageRecordsInterval: bc.Spec.Sync.UsageRecordsInterval,
				InvoiceInterval:      bc.Spec.Sync.InvoiceInterval,
			}
		}

		if bc.Status.LastSync != nil {
			ls := &connectorLastSync{
				PushedRecords: bc.Status.LastSync.PushedRecords,
				Failed:        bc.Status.LastSync.Failed,
			}
			if bc.Status.LastSync.At != nil {
				ls.At = bc.Status.LastSync.At.UTC().Format(time.RFC3339)
			}
			cs.LastSync = ls
		}

		if bc.Status.WebhookStats != nil {
			cs.WebhookStats = &connectorWebhookStats{
				Received24h:       bc.Status.WebhookStats.Received24h,
				SignatureFailures: bc.Status.WebhookStats.SignatureFailures,
			}
		}

		conditions := make([]connectorCondition, 0, len(bc.Status.Conditions))
		for _, c := range bc.Status.Conditions {
			conditions = append(conditions, connectorCondition{
				Type:   c.Type,
				Status: string(c.Status),
				Reason: c.Reason,
			})
		}
		cs.Conditions = conditions

		connectors = append(connectors, cs)
	}

	writeJSON(w, http.StatusOK, connectorsResponse{Connectors: connectors})
}
