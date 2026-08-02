package httpapi

import (
	"context"
	"fmt"
	"net/http"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type pricingHandler struct {
	reader    client.Reader
	defaultNS string
}

// pricingResponse is the JSON shape returned by GET /v1/pricing.
type pricingResponse struct {
	Tenant                 string             `json:"tenant"`
	Namespace              string             `json:"namespace"`
	PriceBook              string             `json:"priceBook"`
	Currency               string             `json:"currency"`
	EffectiveFrom          string             `json:"effectiveFrom"`
	ProviderFaultPolicy    string             `json:"providerFaultPolicy"`
	ClientDisconnectPolicy string             `json:"clientDisconnectPolicy"`
	SKUs                   []pricingSKU       `json:"skus"`
	VolumeTiers            []pricingVolTier   `json:"volumeTiers"`
	CommittedUse           []pricingCommitted `json:"committedUse"`
}

type pricingSKU struct {
	SKU   string            `json:"sku"`
	Unit  string            `json:"unit"`
	Geo   string            `json:"geo"`
	Rates map[string]string `json:"rates"`
}

type pricingVolTier struct {
	ThresholdTokens int64  `json:"thresholdTokens"`
	DiscountPct     string `json:"discountPct"`
}

type pricingCommitted struct {
	Name            string `json:"name"`
	TokensPerSecond int64  `json:"tokensPerSecond"`
	MonthlyPrice    string `json:"monthlyPrice"`
}

func (h *pricingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant query parameter is required")
		return
	}

	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "pricing requires Kubernetes access")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = h.defaultNS
	}

	ctx := r.Context()

	pb, err := h.resolvePriceBook(ctx, tenant, ns)
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("kubernetes API error: %v", err))
		return
	}

	resp := buildPricingResponse(tenant, ns, pb)
	writeJSON(w, http.StatusOK, resp)
}

// resolvePriceBook looks up the TenantAccount and resolves to a PriceBook through
// the standard chain: PriceBookPin -> Plan.PriceBookRef -> Active PriceBook in namespace.
func (h *pricingHandler) resolvePriceBook(ctx context.Context, tenant, ns string) (*billingv1alpha1.PriceBook, error) {
	// 1. Look up TenantAccount.
	var ta billingv1alpha1.TenantAccount
	if err := h.reader.Get(ctx, types.NamespacedName{Name: tenant, Namespace: ns}, &ta); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(
				billingv1alpha1.GroupVersion.WithResource("tenantaccounts").GroupResource(),
				tenant,
			)
		}
		return nil, err
	}

	// 2a. Check PriceBookPin.
	if ta.Spec.PriceBookPin != "" {
		var pb billingv1alpha1.PriceBook
		if err := h.reader.Get(ctx, types.NamespacedName{Name: ta.Spec.PriceBookPin, Namespace: ns}, &pb); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, apierrors.NewNotFound(
					billingv1alpha1.GroupVersion.WithResource("pricebooks").GroupResource(),
					ta.Spec.PriceBookPin,
				)
			}
			return nil, err
		}
		return &pb, nil
	}

	// 2b. Resolve via Plan.PriceBookRef.
	if ta.Spec.PlanRef.Name != "" {
		var plan billingv1alpha1.Plan
		if err := h.reader.Get(ctx, types.NamespacedName{Name: ta.Spec.PlanRef.Name, Namespace: ns}, &plan); err != nil {
			if apierrors.IsNotFound(err) {
				// Plan not found is not fatal for pricing; fall through to active search.
			} else {
				return nil, err
			}
		} else if plan.Spec.PriceBookRef != "" {
			var pb billingv1alpha1.PriceBook
			if err := h.reader.Get(ctx, types.NamespacedName{Name: plan.Spec.PriceBookRef, Namespace: ns}, &pb); err != nil {
				if apierrors.IsNotFound(err) {
					return nil, apierrors.NewNotFound(
						billingv1alpha1.GroupVersion.WithResource("pricebooks").GroupResource(),
						plan.Spec.PriceBookRef,
					)
				}
				return nil, err
			}
			return &pb, nil
		}
	}

	// 2c. Fall back to the Active PriceBook in the namespace.
	var pbList billingv1alpha1.PriceBookList
	if err := h.reader.List(ctx, &pbList, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for i := range pbList.Items {
		if pbList.Items[i].Status.State == "Active" {
			return &pbList.Items[i], nil
		}
	}

	return nil, apierrors.NewNotFound(
		billingv1alpha1.GroupVersion.WithResource("pricebooks").GroupResource(),
		"(active)",
	)
}

func buildPricingResponse(tenant, ns string, pb *billingv1alpha1.PriceBook) pricingResponse {
	skus := make([]pricingSKU, 0, len(pb.Spec.SKUs))
	for _, s := range pb.Spec.SKUs {
		skus = append(skus, pricingSKU{
			SKU:   s.SKUName,
			Unit:  s.Unit,
			Geo:   s.Geo,
			Rates: s.Rates,
		})
	}

	volTiers := make([]pricingVolTier, 0, len(pb.Spec.VolumeTiers))
	for _, vt := range pb.Spec.VolumeTiers {
		volTiers = append(volTiers, pricingVolTier{
			ThresholdTokens: vt.ThresholdTokens,
			DiscountPct:     vt.DiscountPct,
		})
	}

	committed := make([]pricingCommitted, 0, len(pb.Spec.CommittedUse))
	for _, cu := range pb.Spec.CommittedUse {
		committed = append(committed, pricingCommitted{
			Name:            cu.Name,
			TokensPerSecond: cu.TokensPerSecond,
			MonthlyPrice:    cu.MonthlyPrice,
		})
	}

	provFault := pb.Spec.ProviderFaultPolicy
	if provFault == "" {
		provFault = "zero_charge"
	}
	clientDisc := pb.Spec.ClientDisconnectPolicy
	if clientDisc == "" {
		clientDisc = "bill_partial"
	}

	return pricingResponse{
		Tenant:                 tenant,
		Namespace:              ns,
		PriceBook:              pb.Name,
		Currency:               pb.Spec.Currency,
		EffectiveFrom:          pb.Spec.EffectiveFrom.UTC().Format("2006-01-02T15:04:05Z"),
		ProviderFaultPolicy:    provFault,
		ClientDisconnectPolicy: clientDisc,
		SKUs:                   skus,
		VolumeTiers:            volTiers,
		CommittedUse:           committed,
	}
}
