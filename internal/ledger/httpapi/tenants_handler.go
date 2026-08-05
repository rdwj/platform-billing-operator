package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

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
	TenantID        string `json:"tenant_id"`
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	Plan            string `json:"plan"`
	Status          string `json:"status"`
	Balance         string `json:"balance"`
	BillingMode     string `json:"billing_mode"`
	MtdSpend        string `json:"mtd_spend"`
	RunwayDays      string `json:"runway_days"`
	ReloadEnabled   bool   `json:"reload_enabled"`
	BurnPerDay      string `json:"burn_per_day"`
	ReloadThreshold string `json:"reload_threshold,omitempty"`
	ReloadAmount    string `json:"reload_amount,omitempty"`
	MonthlyCap      string `json:"monthly_cap,omitempty"`
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
		ts := tenantSummary{
			TenantID:    ta.Name,
			Name:        ta.Spec.DisplayName,
			Namespace:   ta.Namespace,
			Plan:        ta.Spec.PlanRef.Name,
			Status:      ta.Status.State,
			Balance:     ta.Status.Balance,
			BillingMode: ta.Spec.BillingMode,
			MtdSpend:    ta.Status.MtdSpend,
			RunwayDays:  ta.Status.RunwayDays,
		}

		// Compute burn_per_day from balance / runway_days.
		if ta.Status.Balance != "" && ta.Status.RunwayDays != "" {
			bal, errB := strconv.ParseFloat(ta.Status.Balance, 64)
			run, errR := strconv.ParseFloat(ta.Status.RunwayDays, 64)
			if errB == nil && errR == nil && run > 0 {
				ts.BurnPerDay = fmt.Sprintf("%.6f", bal/run)
			}
		}

		if rp := ta.Spec.ReloadPolicy; rp != nil {
			ts.ReloadEnabled = rp.Enabled
			ts.ReloadThreshold = rp.ThresholdAmount
			ts.ReloadAmount = rp.ReloadAmount
			ts.MonthlyCap = rp.MonthlyCapAmount
		}

		tenants = append(tenants, ts)
	}

	writeJSON(w, http.StatusOK, tenantsResponse{Tenants: tenants})
}
