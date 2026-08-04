package ledger

import "testing"

func TestFOCUSCSVHeaderLength(t *testing.T) {
	header := FOCUSCSVHeader()
	if got := len(header); got != 29 {
		t.Fatalf("FOCUSCSVHeader() returned %d elements, want 29", got)
	}
}

func TestFOCUSCSVRecordMatchesHeader(t *testing.T) {
	row := FOCUSRow{
		BilledCost:         "1.000000",
		BillingAccountId:   "tenant-1",
		BillingAccountName: "tenant-1",
		BillingCurrency:    "USD",
		BillingPeriodEnd:   "2026-09-01T00:00:00Z",
		BillingPeriodStart: "2026-08-01T00:00:00Z",
		ChargeCategory:     "Usage",
		ChargeClass:        "",
		ChargeDescription:  "gpt-4o prompt_tokens",
		ChargePeriodEnd:    "2026-08-01T11:00:00Z",
		ChargePeriodStart:  "2026-08-01T10:00:00Z",
		ContractedCost:     "1.000000",
		EffectiveCost:      "1.000000",
		ConsumedQuantity:   "500",
		ConsumedUnit:       FOCUSConsumedUnit,
		ListCost:           "1.000000",
		PricingQuantity:    "500",
		PricingUnit:        FOCUSConsumedUnit,
		Provider:           FOCUSProvider,
		Publisher:          FOCUSProvider,
		ResourceId:         "gpt-4o",
		ResourceName:       "gpt-4o",
		ServiceCategory:    "AI and Machine Learning",
		ServiceName:        "Models as a Service",
		SkuId:              "gpt-4o",
		SkuMeter:           "prompt_tokens",
		SubAccountId:       "tenant-1",
		SubAccountName:     "tenant-1",
		ChargeFrequency:    FOCUSChargeFrequency,
	}

	header := FOCUSCSVHeader()
	record := row.CSVRecord()

	if len(record) != len(header) {
		t.Fatalf("CSVRecord() length %d != FOCUSCSVHeader() length %d", len(record), len(header))
	}
}

func TestFOCUSCSVRecordOrder(t *testing.T) {
	header := FOCUSCSVHeader()

	// Verify boundary elements to catch ordering drift.
	checks := []struct {
		index int
		want  string
	}{
		{0, "BilledCost"},
		{1, "BillingAccountId"},
		{12, "EffectiveCost"},
		{24, "SkuId"},
		{25, "SkuMeter"},
		{28, "ChargeFrequency"},
	}
	for _, c := range checks {
		if header[c.index] != c.want {
			t.Errorf("header[%d] = %q, want %q", c.index, header[c.index], c.want)
		}
	}

	// Verify record values match in the same positions.
	row := FOCUSRow{
		BilledCost:      "0.005000",
		EffectiveCost:   "0.005000",
		SkuId:           "llama-3-70b",
		SkuMeter:        "completion_tokens",
		ChargeFrequency: FOCUSChargeFrequency,
	}
	record := row.CSVRecord()

	valueChecks := []struct {
		index int
		want  string
	}{
		{0, "0.005000"},
		{12, "0.005000"},
		{24, "llama-3-70b"},
		{25, "completion_tokens"},
		{28, FOCUSChargeFrequency},
	}
	for _, c := range valueChecks {
		if record[c.index] != c.want {
			t.Errorf("record[%d] = %q, want %q", c.index, record[c.index], c.want)
		}
	}
}
