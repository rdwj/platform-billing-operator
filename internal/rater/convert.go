package rater

import (
	"fmt"
	"strings"

	v1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// PriceBookSpecToRaterPriceBook converts a PriceBook CRD spec into the rater's
// PriceBook type. The name parameter is the CR's metadata.name and revision is
// typically metadata.generation.
func PriceBookSpecToRaterPriceBook(name string, revision int, spec v1alpha1.PriceBookSpec) (PriceBook, error) {
	pb := PriceBook{
		Name:                   name,
		Revision:               revision,
		Currency:               spec.Currency,
		SKUs:                   make(map[string]SKU, len(spec.SKUs)),
		ProviderFaultPolicy:    policyOrDefault(spec.ProviderFaultPolicy, policyZeroCharge),
		ClientDisconnectPolicy: policyOrDefault(spec.ClientDisconnectPolicy, policyBillPartial),
	}

	for _, crdSKU := range spec.SKUs {
		if _, dup := pb.SKUs[crdSKU.SKUName]; dup {
			return PriceBook{}, fmt.Errorf("duplicate SKU %q in PriceBook %s", crdSKU.SKUName, name)
		}
		perMillion := isPerMillionUnit(crdSKU.Unit)
		raterSKU := SKU{
			Unit:       crdSKU.Unit,
			Components: make(map[string]ComponentRate, len(crdSKU.Rates)),
		}
		for qtyKey, rateStr := range crdSKU.Rates {
			cr := ComponentRate{}
			if perMillion {
				cr.RatePerMillion = rateStr
			} else {
				cr.RatePerUnit = rateStr
			}
			raterSKU.Components[qtyKey] = cr
		}
		pb.SKUs[crdSKU.SKUName] = raterSKU
	}

	return pb, nil
}

func policyOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

// isPerMillionUnit returns true when the CRD unit indicates rates are
// quoted per million (e.g. "per-1M-tokens", "tokens"). GPU-time and
// other non-token units are per-unit.
func isPerMillionUnit(unit string) bool {
	u := strings.ToLower(unit)
	return strings.Contains(u, "token")
}
