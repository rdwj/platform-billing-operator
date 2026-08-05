package httpapi

import (
	"fmt"
	"net/http"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type plansHandler struct {
	reader    client.Reader
	defaultNS string
}

type plansResponse struct {
	Plans []planSummary `json:"plans"`
}

type planSummary struct {
	Name             string                   `json:"name"`
	Namespace        string                   `json:"namespace"`
	Limits           planLimitsResponse       `json:"limits"`
	ModelScopes      []string                 `json:"modelScopes"`
	GeoScopes        []string                 `json:"geoScopes"`
	PriceBookRef     string                   `json:"priceBookRef"`
	CompiledPolicies []compiledPolicyResponse `json:"compiledPolicies"`
	TenantCount      int                      `json:"tenantCount"`
}

type planLimitsResponse struct {
	TokensPerMinute   *int64 `json:"tokensPerMinute,omitempty"`
	TokensPerDay      *int64 `json:"tokensPerDay,omitempty"`
	RequestsPerMinute *int32 `json:"requestsPerMinute,omitempty"`
	MaxConcurrency    *int32 `json:"maxConcurrency,omitempty"`
}

type compiledPolicyResponse struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UpToDate  bool   `json:"upToDate"`
}

func (h *plansHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "plans listing requires Kubernetes access")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var planList billingv1alpha1.PlanList
	if err := h.reader.List(r.Context(), &planList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	var taList billingv1alpha1.TenantAccountList
	if err := h.reader.List(r.Context(), &taList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error listing tenants: %v", err))
		return
	}

	tenantsByPlan := make(map[string]int, len(planList.Items))
	for i := range taList.Items {
		tenantsByPlan[taList.Items[i].Spec.PlanRef.Name]++
	}

	plans := make([]planSummary, 0, len(planList.Items))
	for i := range planList.Items {
		p := &planList.Items[i]

		policies := make([]compiledPolicyResponse, 0, len(p.Status.CompiledPolicies))
		for _, cp := range p.Status.CompiledPolicies {
			policies = append(policies, compiledPolicyResponse{
				Group:     cp.Group,
				Kind:      cp.Kind,
				Name:      cp.Name,
				Namespace: cp.Namespace,
				UpToDate:  cp.UpToDate,
			})
		}

		modelScopes := p.Spec.ModelScopes
		if modelScopes == nil {
			modelScopes = []string{}
		}
		geoScopes := p.Spec.GeoScopes
		if geoScopes == nil {
			geoScopes = []string{}
		}

		plans = append(plans, planSummary{
			Name:      p.Name,
			Namespace: p.Namespace,
			Limits: planLimitsResponse{
				TokensPerMinute:   p.Spec.Limits.TokensPerMinute,
				TokensPerDay:      p.Spec.Limits.TokensPerDay,
				RequestsPerMinute: p.Spec.Limits.RequestsPerMinute,
				MaxConcurrency:    p.Spec.Limits.MaxConcurrency,
			},
			ModelScopes:      modelScopes,
			GeoScopes:        geoScopes,
			PriceBookRef:     p.Spec.PriceBookRef,
			CompiledPolicies: policies,
			TenantCount:      tenantsByPlan[p.Name],
		})
	}

	writeJSON(w, http.StatusOK, plansResponse{Plans: plans})
}
