package httpapi

import (
	"fmt"
	"net/http"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/shopspring/decimal"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type budgetsHandler struct {
	reader    client.Reader
	defaultNS string
}

type budgetThresholdResponse struct {
	Pct    int32  `json:"pct"`
	Action string `json:"action"`
}

type budgetCapResponse struct {
	Amount     string                    `json:"amount"`
	Period     string                    `json:"period"`
	Thresholds []budgetThresholdResponse `json:"thresholds"`
}

type firedThresholdResponse struct {
	CapIndex int32  `json:"cap_index"`
	Pct      int32  `json:"pct"`
	FiredAt  string `json:"fired_at"`
}

type budgetResponse struct {
	Name            string                   `json:"name"`
	Caps            []budgetCapResponse      `json:"caps"`
	CurrentSpend    string                   `json:"current_spend"`
	FiredThresholds []firedThresholdResponse `json:"fired_thresholds"`
	Selector        *budgetSelectorResponse  `json:"selector"`
}

type budgetSelectorResponse struct {
	KeyLabels map[string]string `json:"keyLabels,omitempty"`
}

type budgetsListResponse struct {
	Tenant  string           `json:"tenant"`
	Budgets []budgetResponse `json:"budgets"`
}

func (h *budgetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "budgets listing requires Kubernetes access")
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var list billingv1alpha1.BudgetPolicyList
	if err := h.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	budgets := make([]budgetResponse, 0)
	for i := range list.Items {
		bp := &list.Items[i]
		if bp.Spec.TenantRef.Name != tenant {
			continue
		}

		caps := make([]budgetCapResponse, 0, len(bp.Spec.Caps))
		for _, cap := range bp.Spec.Caps {
			amt, _ := decimal.NewFromString(cap.Amount)
			thresholds := make([]budgetThresholdResponse, 0, len(cap.Thresholds))
			for _, t := range cap.Thresholds {
				thresholds = append(thresholds, budgetThresholdResponse{
					Pct:    t.Pct,
					Action: t.Action,
				})
			}
			caps = append(caps, budgetCapResponse{
				Amount:     amt.StringFixed(6),
				Period:     cap.Period,
				Thresholds: thresholds,
			})
		}

		fired := make([]firedThresholdResponse, 0, len(bp.Status.FiredThresholds))
		for _, ft := range bp.Status.FiredThresholds {
			fired = append(fired, firedThresholdResponse{
				CapIndex: ft.CapIndex,
				Pct:      ft.Pct,
				FiredAt:  ft.FiredAt.UTC().Format(time.RFC3339),
			})
		}

		spend := bp.Status.CurrentSpend
		if spend != "" {
			s, _ := decimal.NewFromString(spend)
			spend = s.StringFixed(6)
		}

		br := budgetResponse{
			Name:            bp.Name,
			Caps:            caps,
			CurrentSpend:    spend,
			FiredThresholds: fired,
		}

		if bp.Spec.Selector != nil {
			br.Selector = &budgetSelectorResponse{
				KeyLabels: bp.Spec.Selector.KeyLabels,
			}
		}

		budgets = append(budgets, br)
	}

	writeJSON(w, http.StatusOK, budgetsListResponse{
		Tenant:  tenant,
		Budgets: budgets,
	})
}
