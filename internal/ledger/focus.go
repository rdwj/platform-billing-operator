package ledger

// FOCUSRow represents a single row in a FOCUS 1.2-compliant cost and usage export.
type FOCUSRow struct {
	// Mandatory columns (FOCUS 1.2)
	BilledCost         string `json:"BilledCost"`
	BillingAccountId   string `json:"BillingAccountId"`
	BillingAccountName string `json:"BillingAccountName"`
	BillingCurrency    string `json:"BillingCurrency"`
	BillingPeriodEnd   string `json:"BillingPeriodEnd"`
	BillingPeriodStart string `json:"BillingPeriodStart"`
	ChargeCategory     string `json:"ChargeCategory"`
	ChargeClass        string `json:"ChargeClass"`
	ChargeDescription  string `json:"ChargeDescription"`
	ChargePeriodEnd    string `json:"ChargePeriodEnd"`
	ChargePeriodStart  string `json:"ChargePeriodStart"`
	ContractedCost     string `json:"ContractedCost"`
	EffectiveCost      string `json:"EffectiveCost"`

	// Conditional columns (MaaS-relevant subset)
	ConsumedQuantity string `json:"ConsumedQuantity,omitempty"`
	ConsumedUnit     string `json:"ConsumedUnit,omitempty"`
	ListCost         string `json:"ListCost,omitempty"`
	PricingQuantity  string `json:"PricingQuantity,omitempty"`
	PricingUnit      string `json:"PricingUnit,omitempty"`
	Provider         string `json:"Provider,omitempty"`
	Publisher        string `json:"Publisher,omitempty"`
	ResourceId       string `json:"ResourceId,omitempty"`
	ResourceName     string `json:"ResourceName,omitempty"`
	ServiceCategory  string `json:"ServiceCategory,omitempty"`
	ServiceName      string `json:"ServiceName,omitempty"`
	SkuId            string `json:"SkuId,omitempty"`
	SkuMeter         string `json:"SkuMeter,omitempty"`
	SubAccountId     string `json:"SubAccountId,omitempty"`
	SubAccountName   string `json:"SubAccountName,omitempty"`

	// Recommended
	ChargeFrequency string `json:"ChargeFrequency,omitempty"`
}

// FOCUSCSVHeader returns the FOCUS 1.2 column names in struct field order.
func FOCUSCSVHeader() []string {
	return []string{
		"BilledCost",
		"BillingAccountId",
		"BillingAccountName",
		"BillingCurrency",
		"BillingPeriodEnd",
		"BillingPeriodStart",
		"ChargeCategory",
		"ChargeClass",
		"ChargeDescription",
		"ChargePeriodEnd",
		"ChargePeriodStart",
		"ContractedCost",
		"EffectiveCost",
		"ConsumedQuantity",
		"ConsumedUnit",
		"ListCost",
		"PricingQuantity",
		"PricingUnit",
		"Provider",
		"Publisher",
		"ResourceId",
		"ResourceName",
		"ServiceCategory",
		"ServiceName",
		"SkuId",
		"SkuMeter",
		"SubAccountId",
		"SubAccountName",
		"ChargeFrequency",
	}
}

// CSVRecord returns the field values in the same order as FOCUSCSVHeader.
func (r *FOCUSRow) CSVRecord() []string {
	return []string{
		r.BilledCost,
		r.BillingAccountId,
		r.BillingAccountName,
		r.BillingCurrency,
		r.BillingPeriodEnd,
		r.BillingPeriodStart,
		r.ChargeCategory,
		r.ChargeClass,
		r.ChargeDescription,
		r.ChargePeriodEnd,
		r.ChargePeriodStart,
		r.ContractedCost,
		r.EffectiveCost,
		r.ConsumedQuantity,
		r.ConsumedUnit,
		r.ListCost,
		r.PricingQuantity,
		r.PricingUnit,
		r.Provider,
		r.Publisher,
		r.ResourceId,
		r.ResourceName,
		r.ServiceCategory,
		r.ServiceName,
		r.SkuId,
		r.SkuMeter,
		r.SubAccountId,
		r.SubAccountName,
		r.ChargeFrequency,
	}
}
