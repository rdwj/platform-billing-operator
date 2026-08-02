package rater

import (
	"encoding/json"
	"testing"

	v1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func TestConvertMaaSPriceBook(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency:               "USD",
		ProviderFaultPolicy:    "zero_charge",
		ClientDisconnectPolicy: "bill_partial",
		SKUs: []v1alpha1.SKU{
			{
				SKUName: "llama-3-70b",
				Unit:    "per-1M-tokens",
				Rates: map[string]string{
					"prompt_tokens":     "2.500000",
					"completion_tokens": "10.000000",
					"reasoning_tokens":  "10.000000",
					"cached_tokens":     "0.625000",
				},
			},
		},
	}

	pb, err := PriceBookSpecToRaterPriceBook("pb-test", 1, spec)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if pb.Name != "pb-test" {
		t.Errorf("name = %q, want pb-test", pb.Name)
	}
	if pb.Currency != "USD" {
		t.Errorf("currency = %q, want USD", pb.Currency)
	}
	if pb.ProviderFaultPolicy != "zero_charge" {
		t.Errorf("provider_fault_policy = %q, want zero_charge", pb.ProviderFaultPolicy)
	}

	sku, ok := pb.SKUs["llama-3-70b"]
	if !ok {
		t.Fatal("missing SKU llama-3-70b")
	}
	if len(sku.Components) != 4 {
		t.Errorf("component count = %d, want 4", len(sku.Components))
	}
	prompt := sku.Components["prompt_tokens"]
	if prompt.RatePerMillion != "2.500000" {
		t.Errorf("prompt rate_per_million = %q, want 2.500000", prompt.RatePerMillion)
	}
	if prompt.RatePerUnit != "" {
		t.Errorf("prompt rate_per_unit should be empty, got %q", prompt.RatePerUnit)
	}
}

func TestConvertGPUaaSSKU(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency: "USD",
		SKUs: []v1alpha1.SKU{
			{
				SKUName: "h200-mig-3g.40gb",
				Unit:    "per-gpu-second",
				Rates:   map[string]string{"gpu_seconds": "0.001250"},
			},
		},
	}

	pb, err := PriceBookSpecToRaterPriceBook("pb-gpu", 2, spec)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	sku := pb.SKUs["h200-mig-3g.40gb"]
	comp := sku.Components["gpu_seconds"]
	if comp.RatePerUnit != "0.001250" {
		t.Errorf("gpu rate_per_unit = %q, want 0.001250", comp.RatePerUnit)
	}
	if comp.RatePerMillion != "" {
		t.Errorf("gpu rate_per_million should be empty, got %q", comp.RatePerMillion)
	}
}

func TestConvertFullGPUSKU(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency: "USD",
		SKUs: []v1alpha1.SKU{
			{
				SKUName: "h200",
				Unit:    "per-gpu-second",
				Rates:   map[string]string{"gpu_seconds": "0.002917"},
			},
		},
	}

	pb, err := PriceBookSpecToRaterPriceBook("pb-gpu", 1, spec)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	sku := pb.SKUs["h200"]
	comp := sku.Components["gpu_seconds"]
	if comp.RatePerUnit != "0.002917" {
		t.Errorf("gpu rate_per_unit = %q, want 0.002917", comp.RatePerUnit)
	}
	if comp.RatePerMillion != "" {
		t.Errorf("gpu rate_per_million should be empty, got %q", comp.RatePerMillion)
	}
}

func TestConvertPolicyDefaults(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency: "USD",
		SKUs: []v1alpha1.SKU{
			{SKUName: "test-sku", Unit: "per-1M-tokens", Rates: map[string]string{"tokens": "1.0"}},
		},
	}

	pb, err := PriceBookSpecToRaterPriceBook("pb-defaults", 1, spec)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if pb.ProviderFaultPolicy != "zero_charge" {
		t.Errorf("default provider_fault_policy = %q, want zero_charge", pb.ProviderFaultPolicy)
	}
	if pb.ClientDisconnectPolicy != "bill_partial" {
		t.Errorf("default client_disconnect_policy = %q, want bill_partial", pb.ClientDisconnectPolicy)
	}
}

func TestConvertDuplicateSKUErrors(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency: "USD",
		SKUs: []v1alpha1.SKU{
			{SKUName: "same-sku", Unit: "per-1M-tokens", Rates: map[string]string{"tokens": "1.0"}},
			{SKUName: "same-sku", Unit: "per-1M-tokens", Rates: map[string]string{"tokens": "2.0"}},
		},
	}

	_, err := PriceBookSpecToRaterPriceBook("pb-dup", 1, spec)
	if err == nil {
		t.Fatal("expected error for duplicate SKU")
	}
}

// TestConvertedPriceBookRatesCorrectly verifies the converted PriceBook
// produces the same golden vector output as the hand-crafted test fixture.
func TestConvertedPriceBookRatesCorrectly(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency:               "USD",
		ProviderFaultPolicy:    "zero_charge",
		ClientDisconnectPolicy: "bill_partial",
		SKUs: []v1alpha1.SKU{
			{
				SKUName: "llama-3-70b",
				Unit:    "per-1M-tokens",
				Rates: map[string]string{
					"prompt_tokens":     "2.500000",
					"completion_tokens": "10.000000",
					"reasoning_tokens":  "10.000000",
					"cached_tokens":     "0.625000",
				},
			},
			{
				SKUName: "h200-mig-3g.40gb",
				Unit:    "per-gpu-second",
				Rates:   map[string]string{"gpu_seconds": "0.001250"},
			},
			{
				SKUName: "h200",
				Unit:    "per-gpu-second",
				Rates:   map[string]string{"gpu_seconds": "0.002917"},
			},
		},
	}

	pb, err := PriceBookSpecToRaterPriceBook("pb-standard", 3, spec)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	ev := loadEvent(t, "examples/maas-streaming-ok.json")
	ch, err := Rate(ev, pb)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if ch.Total != "0.013670" {
		t.Errorf("total = %q, want 0.013670", ch.Total)
	}

	gpuEv := loadEvent(t, "examples/gpuaas-interval.json")
	gpuCh, err := Rate(gpuEv, pb)
	if err != nil {
		t.Fatalf("Rate GPU: %v", err)
	}
	if gpuCh.Total != "4.500000" {
		t.Errorf("gpu total = %q, want 4.500000", gpuCh.Total)
	}

	fullGPUEv := loadEvent(t, "examples/gpuaas-interval-full-gpu.json")
	fullGPUCh, err := Rate(fullGPUEv, pb)
	if err != nil {
		t.Fatalf("Rate full GPU: %v", err)
	}
	if fullGPUCh.Total != "10.501200" {
		t.Errorf("full gpu total = %q, want 10.501200", fullGPUCh.Total)
	}
}

// TestConvertedPriceBookDeterminism verifies that converting and rating
// produces deterministic results (no map iteration ordering issues in convert).
func TestConvertedPriceBookDeterminism(t *testing.T) {
	spec := v1alpha1.PriceBookSpec{
		Currency:               "USD",
		ProviderFaultPolicy:    "zero_charge",
		ClientDisconnectPolicy: "bill_partial",
		SKUs: []v1alpha1.SKU{
			{
				SKUName: "llama-3-70b",
				Unit:    "per-1M-tokens",
				Rates: map[string]string{
					"prompt_tokens":     "2.500000",
					"completion_tokens": "10.000000",
					"reasoning_tokens":  "10.000000",
					"cached_tokens":     "0.625000",
				},
			},
		},
	}

	ev := loadEvent(t, "examples/maas-streaming-ok.json")
	var prev string
	for i := 0; i < 10; i++ {
		pb, err := PriceBookSpecToRaterPriceBook("pb-det", 1, spec)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := Rate(ev, pb)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(ch)
		cur := string(b)
		if prev != "" && cur != prev {
			t.Fatalf("iteration %d: determinism failure\nprev: %s\n cur: %s", i, prev, cur)
		}
		prev = cur
	}
}
