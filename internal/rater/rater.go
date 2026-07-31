// Package rater is the reference implementation of the platform-billing-operator
// rating function (operator-plan/controllers.md §5): a PURE function from
// (usage.v1 event, PriceBook revision) to a charge. No I/O, no clock, no
// randomness — replaying identical inputs MUST produce byte-identical charges.
// That property is enforced by TestDeterminism and by the golden vectors in
// contracts/rating-vectors.v1.json, which this package must pass byte-exactly.
//
// Money rules (repo CLAUDE.md, non-negotiable): all amounts are
// shopspring/decimal internally and decimal STRINGS with exactly 6 fractional
// digits externally (NUMERIC(18,6)). Component amounts are rounded half-away-
// from-zero at 6 decimal places individually; the total is the sum of the
// ROUNDED components. Floats never touch money: quantities are parsed as
// json.Number and converted via decimal.NewFromString.
//
// Token semantics encoded here (documented in rating-vectors.v1.json):
//   - cached_tokens are billed INSTEAD of prompt_tokens for the cached portion:
//     billable prompt = max(0, prompt_tokens - cached_tokens) when the
//     PriceBook prices cached_tokens; cached_tokens bill at their own rate.
//   - reasoning_tokens are a subset of completion_tokens and are billed as a
//     DIFFERENTIAL against the completion rate: amount = qty × (reasoning_rate
//     − completion_rate). When the differential is zero the component is
//     informational (quantity 0, amount 0). completion_tokens always bill in
//     full.
//   - total_tokens is derived and never rated.
//   - a quantity key with no PriceBook component yields a warning
//     ("unrated_quantity:<key>") and no charge — forward-compatible with new
//     token types (gap-analysis §4), which land in the ledger before pricing.
//
// Status policies (open-questions Q6/Q15): status=upstream_error rates to $0
// under provider_fault_policy=zero_charge (the event still produces a charge
// row, for reconciliation); status=client_disconnect bills the partial usage
// under client_disconnect_policy=bill_partial. The inverse settings are also
// implemented so a PriceBook can choose either policy explicitly.
package rater

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// ---- Inputs ----

// Event is the rating-relevant subset of a usage.v1 envelope
// (contracts/usage.v1.schema.json). Quantities use json.Number so integers
// survive without float round-trips.
type Event struct {
	SchemaVersion string                 `json:"schema_version"`
	RecordID      string                 `json:"record_id"`
	TenantID      string                 `json:"tenant_id"`
	SKU           string                 `json:"sku"`
	Geo           string                 `json:"geo,omitempty"`
	Quantities    map[string]json.Number `json:"quantities"`
	Status        string                 `json:"status"`
	Partial       bool                   `json:"partial,omitempty"`
}

// PriceBook mirrors the fixture shape in contracts/rating-vectors.v1.json and
// the PriceBook CRD spec (operator-plan/crds.md). All rates are decimal strings.
type PriceBook struct {
	Name                   string         `json:"name"`
	Revision               int            `json:"revision"`
	Currency               string         `json:"currency"`
	SKUs                   map[string]SKU `json:"skus"`
	ProviderFaultPolicy    string         `json:"provider_fault_policy"`    // zero_charge | bill_partial
	ClientDisconnectPolicy string         `json:"client_disconnect_policy"` // bill_partial | zero_charge
}

type SKU struct {
	Unit       string                   `json:"unit"`
	Components map[string]ComponentRate `json:"components"`
}

type ComponentRate struct {
	RatePerMillion string `json:"rate_per_million,omitempty"`
	RatePerUnit    string `json:"rate_per_unit,omitempty"`
}

// ---- Output ----

type Charge struct {
	RecordID   string                     `json:"record_id"`
	TenantID   string                     `json:"tenant_id"`
	SKU        string                     `json:"sku"`
	PriceBook  string                     `json:"pricebook"`
	Revision   int                        `json:"revision"`
	Currency   string                     `json:"currency"`
	Components map[string]ChargeComponent `json:"components"`
	Total      string                     `json:"total"`
	Warnings   []string                   `json:"warnings,omitempty"`
}

type ChargeComponent struct {
	BillableQuantity int64  `json:"billable_quantity"`
	Amount           string `json:"amount"`
}

const (
	moneyPlaces = 6

	schemaVersionV1   = "usage.v1"
	keyPromptTokens   = "prompt_tokens"
	keyCachedTokens   = "cached_tokens"
	keyReasonTokens   = "reasoning_tokens"
	keyCompTokens     = "completion_tokens"
	keyTotalTokens    = "total_tokens"
	policyZeroCharge  = "zero_charge"
	policyBillPartial = "bill_partial"
)

var million = decimal.NewFromInt(1_000_000)

// Rate is the pure rating function. Errors are contract violations (unknown
// SKU, unparseable quantity, missing rate) — the rating job routes those to
// the DLQ; a returned Charge is always fully priced.
func Rate(ev Event, pb PriceBook) (Charge, error) {
	if ev.SchemaVersion != "" && ev.SchemaVersion != schemaVersionV1 {
		return Charge{}, fmt.Errorf("unsupported schema_version %q", ev.SchemaVersion)
	}
	sku, ok := pb.SKUs[ev.SKU]
	if !ok {
		return Charge{}, fmt.Errorf("unknown_sku: %q not in pricebook %s rev %d", ev.SKU, pb.Name, pb.Revision)
	}

	charge := Charge{
		RecordID:   ev.RecordID,
		TenantID:   ev.TenantID,
		SKU:        ev.SKU,
		PriceBook:  pb.Name,
		Revision:   pb.Revision,
		Currency:   pb.Currency,
		Components: map[string]ChargeComponent{},
	}

	// Status policies: a zero-charge outcome still returns a charge row so the
	// event is visibly settled (reconciliation + dispute traceability).
	if zeroCharge(ev.Status, pb) {
		charge.Total = decimal.Zero.StringFixed(moneyPlaces)
		return charge, nil
	}

	qty, err := parseQuantities(ev.Quantities)
	if err != nil {
		return Charge{}, err
	}

	total := decimal.Zero
	for _, key := range sortedKeys(qty) {
		if key == keyTotalTokens { // derived, never rated
			continue
		}
		rate, priced := sku.Components[key]
		if !priced {
			charge.Warnings = append(charge.Warnings, "unrated_quantity:"+key)
			continue
		}
		billable, effRate, err := effective(key, qty, sku)
		if err != nil {
			return Charge{}, err
		}
		amount := componentAmount(billable, effRate, rate.RatePerMillion != "")
		charge.Components[key] = ChargeComponent{
			BillableQuantity: billable.IntPart(),
			Amount:           amount.StringFixed(moneyPlaces),
		}
		total = total.Add(amount)
	}
	charge.Total = total.StringFixed(moneyPlaces)
	return charge, nil
}

func zeroCharge(status string, pb PriceBook) bool {
	switch status {
	case "upstream_error":
		return pb.ProviderFaultPolicy != policyBillPartial // default zero_charge (Q15)
	case "client_disconnect":
		return pb.ClientDisconnectPolicy == policyZeroCharge // default bill_partial (Q6)
	default:
		return false
	}
}

// effective resolves the billable quantity and rate for a quantity key,
// applying the cached-offset and reasoning-differential token semantics.
func effective(key string, qty map[string]decimal.Decimal, sku SKU) (decimal.Decimal, decimal.Decimal, error) {
	q := qty[key]
	rate, err := rateOf(sku.Components[key])
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("component %s: %w", key, err)
	}
	switch key {
	case keyPromptTokens:
		if cached, ok := qty[keyCachedTokens]; ok {
			if _, priced := sku.Components[keyCachedTokens]; priced {
				q = decimal.Max(decimal.Zero, q.Sub(cached))
			}
		}
		return q, rate, nil
	case keyReasonTokens:
		compRate, err := rateOf(sku.Components[keyCompTokens])
		if err != nil {
			return decimal.Zero, decimal.Zero, fmt.Errorf("reasoning differential needs completion_tokens rate: %w", err)
		}
		diff := rate.Sub(compRate)
		if diff.IsZero() {
			return decimal.Zero, decimal.Zero, nil // informational: same rate as completion
		}
		return q, diff, nil
	default:
		return q, rate, nil
	}
}

func rateOf(r ComponentRate) (decimal.Decimal, error) {
	s := r.RatePerMillion
	if s == "" {
		s = r.RatePerUnit
	}
	if s == "" {
		return decimal.Zero, fmt.Errorf("no rate_per_million or rate_per_unit")
	}
	return decimal.NewFromString(s)
}

// componentAmount = qty × rate [÷ 1e6], rounded half away from zero at 6 dp.
func componentAmount(qty, rate decimal.Decimal, perMillion bool) decimal.Decimal {
	amt := qty.Mul(rate)
	if perMillion {
		amt = amt.Div(million)
	}
	return amt.Round(moneyPlaces)
}

func parseQuantities(in map[string]json.Number) (map[string]decimal.Decimal, error) {
	out := make(map[string]decimal.Decimal, len(in))
	for k, v := range in {
		d, err := decimal.NewFromString(v.String())
		if err != nil {
			return nil, fmt.Errorf("quantity %s=%q: %w", k, v, err)
		}
		if d.IsNegative() {
			return nil, fmt.Errorf("quantity %s is negative", k)
		}
		out[k] = d
	}
	return out, nil
}

func sortedKeys(m map[string]decimal.Decimal) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
