package enforcement

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// ComputeAllowance converts a monetary balance into a token allowance using the
// given per-token price estimate. The result is clamped to [0, planTPM] and
// truncated to a whole number of tokens.
//
// Assumes pricePerTokenEstimate is already normalized to per-token (the caller
// handles unit conversion from PriceBook SKU units like "per-1M-tokens").
func ComputeAllowance(balance, pricePerTokenEstimate string, planTPM *int64) (string, error) {
	bal, err := decimal.NewFromString(balance)
	if err != nil {
		return "", fmt.Errorf("invalid balance %q: %w", balance, err)
	}
	price, err := decimal.NewFromString(pricePerTokenEstimate)
	if err != nil {
		return "", fmt.Errorf("invalid pricePerTokenEstimate %q: %w", pricePerTokenEstimate, err)
	}

	if price.LessThanOrEqual(decimal.Zero) {
		return "", fmt.Errorf("pricePerTokenEstimate must be positive, got %s", price)
	}

	if bal.LessThanOrEqual(decimal.Zero) {
		return "0", nil
	}

	raw := bal.Div(price).IntPart()
	if raw < 0 {
		raw = 0
	}

	if planTPM != nil && raw > *planTPM {
		raw = *planTPM
	}

	return fmt.Sprintf("%d", raw), nil
}

// EstimatePricePerToken computes the simple average of prompt and completion
// per-token rates. This gives a rough per-token cost used by ComputeAllowance
// when the exact prompt/completion split is unknown.
func EstimatePricePerToken(promptRate, completionRate string) (string, error) {
	pr, err := decimal.NewFromString(promptRate)
	if err != nil {
		return "", fmt.Errorf("invalid promptRate %q: %w", promptRate, err)
	}
	cr, err := decimal.NewFromString(completionRate)
	if err != nil {
		return "", fmt.Errorf("invalid completionRate %q: %w", completionRate, err)
	}

	two := decimal.NewFromInt(2)
	estimate := pr.Add(cr).Div(two)
	return estimate.String(), nil
}
