package httpapi

import (
	"fmt"
	"net/http"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type pricebooksHandler struct {
	reader    client.Reader
	defaultNS string
}

type pricebooksResponse struct {
	PriceBooks []priceBookSummary `json:"pricebooks"`
}

type priceBookSummary struct {
	Name                   string                     `json:"name"`
	Namespace              string                     `json:"namespace"`
	Currency               string                     `json:"currency"`
	EffectiveFrom          string                     `json:"effectiveFrom"`
	SKUCount               int                        `json:"skuCount"`
	ProviderFaultPolicy    string                     `json:"providerFaultPolicy,omitempty"`
	ClientDisconnectPolicy string                     `json:"clientDisconnectPolicy,omitempty"`
	VolumeTiers            []volumeTierResponse       `json:"volumeTiers,omitempty"`
	CommittedUse           []committedUseTierResponse `json:"committedUse,omitempty"`
	SKUs                   []skuResponse              `json:"skus"`
	State                  string                     `json:"state"`
	ActiveTenants          int32                      `json:"activeTenants"`
}

type skuResponse struct {
	SKU   string            `json:"sku"`
	Unit  string            `json:"unit"`
	Geo   string            `json:"geo,omitempty"`
	Rates map[string]string `json:"rates"`
}

type volumeTierResponse struct {
	ThresholdTokens int64  `json:"thresholdTokens"`
	DiscountPct     string `json:"discountPct"`
}

type committedUseTierResponse struct {
	Name            string `json:"name"`
	TokensPerSecond int64  `json:"tokensPerSecond"`
	MonthlyPrice    string `json:"monthlyPrice"`
}

func (h *pricebooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "pricebooks listing requires Kubernetes access")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	var list billingv1alpha1.PriceBookList
	if err := h.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	pricebooks := make([]priceBookSummary, 0, len(list.Items))
	for i := range list.Items {
		pb := &list.Items[i]

		skus := make([]skuResponse, 0, len(pb.Spec.SKUs))
		for _, s := range pb.Spec.SKUs {
			skus = append(skus, skuResponse{
				SKU:   s.SKUName,
				Unit:  s.Unit,
				Geo:   s.Geo,
				Rates: s.Rates,
			})
		}

		var volTiers []volumeTierResponse
		for _, vt := range pb.Spec.VolumeTiers {
			volTiers = append(volTiers, volumeTierResponse{
				ThresholdTokens: vt.ThresholdTokens,
				DiscountPct:     vt.DiscountPct,
			})
		}

		var committed []committedUseTierResponse
		for _, cu := range pb.Spec.CommittedUse {
			committed = append(committed, committedUseTierResponse{
				Name:            cu.Name,
				TokensPerSecond: cu.TokensPerSecond,
				MonthlyPrice:    cu.MonthlyPrice,
			})
		}

		pricebooks = append(pricebooks, priceBookSummary{
			Name:                   pb.Name,
			Namespace:              pb.Namespace,
			Currency:               pb.Spec.Currency,
			EffectiveFrom:          pb.Spec.EffectiveFrom.UTC().Format(time.RFC3339),
			SKUCount:               len(pb.Spec.SKUs),
			ProviderFaultPolicy:    pb.Spec.ProviderFaultPolicy,
			ClientDisconnectPolicy: pb.Spec.ClientDisconnectPolicy,
			VolumeTiers:            volTiers,
			CommittedUse:           committed,
			SKUs:                   skus,
			State:                  pb.Status.State,
			ActiveTenants:          pb.Status.ActiveTenants,
		})
	}

	writeJSON(w, http.StatusOK, pricebooksResponse{PriceBooks: pricebooks})
}
