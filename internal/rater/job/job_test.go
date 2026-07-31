package job

import (
	"testing"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/rater"
)

func TestUsageEventToRaterEvent(t *testing.T) {
	tests := []struct {
		name    string
		qty     map[string]float64
		wantQty map[string]string
		status  string
	}{
		{
			name:    "integer token counts",
			qty:     map[string]float64{"prompt_tokens": 412, "completion_tokens": 1288},
			wantQty: map[string]string{"prompt_tokens": "412", "completion_tokens": "1288"},
			status:  "ok",
		},
		{
			name:    "zero quantities",
			qty:     map[string]float64{"prompt_tokens": 0},
			wantQty: map[string]string{"prompt_tokens": "0"},
			status:  "ok",
		},
		{
			name:    "gpu seconds",
			qty:     map[string]float64{"gpu_seconds": 3600},
			wantQty: map[string]string{"gpu_seconds": "3600"},
			status:  "ok",
		},
		{
			name:    "empty quantities",
			qty:     map[string]float64{},
			wantQty: map[string]string{},
			status:  "upstream_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ue := ledger.UsageEvent{
				RecordID:   "rec-test",
				TenantID:   "ten-test",
				SKU:        "llama-3-70b",
				Quantities: tt.qty,
				Status:     tt.status,
			}
			ev := usageEventToRaterEvent(ue)
			if len(ev.Quantities) != len(tt.wantQty) {
				t.Fatalf("quantity count = %d, want %d", len(ev.Quantities), len(tt.wantQty))
			}
			for k, want := range tt.wantQty {
				got := string(ev.Quantities[k])
				if got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if ev.Status != tt.status {
				t.Errorf("status = %q, want %q", ev.Status, tt.status)
			}
		})
	}
}

func TestUsageEventToRaterEventRatesCorrectly(t *testing.T) {
	ue := ledger.UsageEvent{
		RecordID: "rec-golden",
		TenantID: "ten-test",
		SKU:      "llama-3-70b",
		Quantities: map[string]float64{
			"prompt_tokens":     412,
			"completion_tokens": 1288,
			"reasoning_tokens":  256,
			"cached_tokens":     128,
			"total_tokens":      1700,
		},
		Status: "ok",
	}

	ev := usageEventToRaterEvent(ue)

	pb := rater.PriceBook{
		Name:     "pb-test",
		Revision: 1,
		Currency: "USD",
		SKUs: map[string]rater.SKU{
			"llama-3-70b": {
				Unit: "tokens",
				Components: map[string]rater.ComponentRate{
					"prompt_tokens":     {RatePerMillion: "2.500000"},
					"completion_tokens": {RatePerMillion: "10.000000"},
					"reasoning_tokens":  {RatePerMillion: "10.000000"},
					"cached_tokens":     {RatePerMillion: "0.625000"},
				},
			},
		},
		ProviderFaultPolicy:    "zero_charge",
		ClientDisconnectPolicy: "bill_partial",
	}

	charge, err := rater.Rate(ev, pb)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if charge.Total != "0.013670" {
		t.Errorf("total = %q, want 0.013670 (golden vector v1-maas-streaming-ok)", charge.Total)
	}
}

func TestUsageEventToRaterEventPreservesFields(t *testing.T) {
	ue := ledger.UsageEvent{
		RecordID:   "rec-123",
		SchemaVer:  "usage.v1",
		TenantID:   "ten-abc",
		SKU:        "test-model",
		Geo:        "us-east-1",
		Quantities: map[string]float64{"tokens": 100},
		Status:     "ok",
		Partial:    true,
	}

	ev := usageEventToRaterEvent(ue)
	if ev.RecordID != "rec-123" {
		t.Errorf("record_id = %q, want rec-123", ev.RecordID)
	}
	if ev.TenantID != "ten-abc" {
		t.Errorf("tenant_id = %q, want ten-abc", ev.TenantID)
	}
	if ev.SKU != "test-model" {
		t.Errorf("sku = %q, want test-model", ev.SKU)
	}
	if ev.Geo != "us-east-1" {
		t.Errorf("geo = %q, want us-east-1", ev.Geo)
	}
	if ev.SchemaVersion != "usage.v1" {
		t.Errorf("schema_version = %q, want usage.v1", ev.SchemaVersion)
	}
	if !ev.Partial {
		t.Error("partial should be true")
	}
}
