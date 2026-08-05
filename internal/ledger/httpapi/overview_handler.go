package httpapi

import (
	"fmt"
	"net/http"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/shopspring/decimal"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type overviewHandler struct {
	store     *ledger.Store
	reader    client.Reader
	defaultNS string
}

type overviewResponse struct {
	RevenueKpis    revenueKpisResponse    `json:"revenueKpis"`
	TrailingWeeks  []weekResponse         `json:"trailingWeeks"`
	TokenMix       []tokenMixResponse     `json:"tokenMix"`
	TopTenants     []topTenantResponse    `json:"topTenants"`
	TopModels      []topModelResponse     `json:"topModels"`
	Alerts         alertsResponse         `json:"alerts"`
	MeteringStatus meteringStatusResponse `json:"meteringStatus"`
}

type revenueKpisResponse struct {
	MtdRevenue  string `json:"mtdRevenue"`
	Trailing30d string `json:"trailing30d"`
	PriorMonth  string `json:"priorMonth"`
	DeltaPct    string `json:"deltaPct"`
}

type weekResponse struct {
	WeekStart string `json:"weekStart"`
	Revenue   string `json:"revenue"`
}

type tokenMixResponse struct {
	QuantityType string `json:"quantityType"`
	Total        int64  `json:"total"`
}

type topTenantResponse struct {
	TenantID    string `json:"tenantId"`
	DisplayName string `json:"displayName"`
	Amount      string `json:"amount"`
}

type topModelResponse struct {
	SKU    string `json:"sku"`
	Amount string `json:"amount"`
}

type alertsResponse struct {
	TenantStates map[string]int `json:"tenantStates"`
	FiredBudgets int            `json:"firedBudgets"`
}

type meteringStatusResponse struct {
	DriftExceeded int64 `json:"driftExceeded"`
	DlqDepth      int64 `json:"dlqDepth"`
}

func (h *overviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	resp := overviewResponse{}

	mtd, prior, err := h.store.QueryMTDRevenue(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("revenue: %v", err))
		return
	}
	trailing30d, err := h.store.QueryTrailing30dRevenue(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("trailing30d: %v", err))
		return
	}

	mtdDec, _ := decimal.NewFromString(mtd)
	priorDec, _ := decimal.NewFromString(prior)
	trailing30dDec, _ := decimal.NewFromString(trailing30d)

	var deltaPct string
	if !priorDec.IsZero() {
		delta := mtdDec.Sub(priorDec).Div(priorDec).Mul(decimal.NewFromInt(100))
		deltaPct = delta.StringFixed(1)
	} else {
		deltaPct = "0.0"
	}

	resp.RevenueKpis = revenueKpisResponse{
		MtdRevenue:  mtdDec.StringFixed(6),
		Trailing30d: trailing30dDec.StringFixed(6),
		PriorMonth:  priorDec.StringFixed(6),
		DeltaPct:    deltaPct,
	}

	weeks, err := h.store.QueryWeeklyRevenue(ctx, 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("weekly: %v", err))
		return
	}
	resp.TrailingWeeks = make([]weekResponse, 0, len(weeks))
	for _, w := range weeks {
		rev, _ := decimal.NewFromString(w.Revenue)
		resp.TrailingWeeks = append(resp.TrailingWeeks, weekResponse{
			WeekStart: w.WeekStart.UTC().Format(time.RFC3339),
			Revenue:   rev.StringFixed(6),
		})
	}

	tokenMix, err := h.store.QueryTokenMix(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("token mix: %v", err))
		return
	}
	resp.TokenMix = make([]tokenMixResponse, 0, len(tokenMix))
	for _, t := range tokenMix {
		resp.TokenMix = append(resp.TokenMix, tokenMixResponse{
			QuantityType: t.QuantityType,
			Total:        t.Total,
		})
	}

	topTenants, err := h.store.QueryTopTenantsBySpend(ctx, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("top tenants: %v", err))
		return
	}

	tenantNames := make(map[string]string)
	if h.reader != nil {
		ns := h.defaultNS
		var taList billingv1alpha1.TenantAccountList
		if listErr := h.reader.List(ctx, &taList, client.InNamespace(ns)); listErr == nil {
			for i := range taList.Items {
				ta := &taList.Items[i]
				tenantNames[ta.Name] = ta.Spec.DisplayName
			}
		}
	}

	resp.TopTenants = make([]topTenantResponse, 0, len(topTenants))
	for _, t := range topTenants {
		amt, _ := decimal.NewFromString(t.Amount)
		name := tenantNames[t.TenantID]
		if name == "" {
			name = t.TenantID
		}
		resp.TopTenants = append(resp.TopTenants, topTenantResponse{
			TenantID:    t.TenantID,
			DisplayName: name,
			Amount:      amt.StringFixed(6),
		})
	}

	topModels, err := h.store.QueryTopModelsByRevenue(ctx, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("top models: %v", err))
		return
	}
	resp.TopModels = make([]topModelResponse, 0, len(topModels))
	for _, m := range topModels {
		amt, _ := decimal.NewFromString(m.Amount)
		resp.TopModels = append(resp.TopModels, topModelResponse{
			SKU:    m.SKU,
			Amount: amt.StringFixed(6),
		})
	}

	resp.Alerts = alertsResponse{
		TenantStates: make(map[string]int),
	}
	if h.reader != nil {
		ns := h.defaultNS
		var taList billingv1alpha1.TenantAccountList
		if listErr := h.reader.List(ctx, &taList, client.InNamespace(ns)); listErr == nil {
			for i := range taList.Items {
				st := taList.Items[i].Status.State
				if st == "Warning" || st == "Grace" || st == "Dunning" || st == "Frozen" {
					resp.Alerts.TenantStates[st]++
				}
			}
		}

		var bpList billingv1alpha1.BudgetPolicyList
		if listErr := h.reader.List(ctx, &bpList, client.InNamespace(ns)); listErr == nil {
			for i := range bpList.Items {
				resp.Alerts.FiredBudgets += len(bpList.Items[i].Status.FiredThresholds)
			}
		}
	}

	dlqDepth, err := h.store.QueryDLQDepth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("dlq: %v", err))
		return
	}
	driftExceeded, err := h.store.QueryDriftExceededCount(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("drift: %v", err))
		return
	}
	resp.MeteringStatus = meteringStatusResponse{
		DriftExceeded: driftExceeded,
		DlqDepth:      dlqDepth,
	}

	writeJSON(w, http.StatusOK, resp)
}
