package httpapi

import (
	"fmt"
	"net/http"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type tenantsHandler struct {
	reader    client.Reader
	defaultNS string
}

type tenantsResponse struct {
	Tenants []tenantSummary `json:"tenants"`
}

type tenantSummary struct {
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Plan      string `json:"plan"`
	Status    string `json:"status"`
}

func (h *tenantsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "tenants listing requires Kubernetes access")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var list billingv1alpha1.TenantAccountList
	if err := h.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	tenants := make([]tenantSummary, 0, len(list.Items))
	for i := range list.Items {
		ta := &list.Items[i]
		tenants = append(tenants, tenantSummary{
			TenantID:  ta.Name,
			Name:      ta.Spec.DisplayName,
			Namespace: ta.Namespace,
			Plan:      ta.Spec.PlanRef.Name,
			Status:    ta.Status.State,
		})
	}

	writeJSON(w, http.StatusOK, tenantsResponse{Tenants: tenants})
}
