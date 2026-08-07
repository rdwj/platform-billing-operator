package usagesink_test

import (
	"testing"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/usagesink"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestConvertLedgerEvent(t *testing.T) {
	ts := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	stream := true
	latency := 42.5

	input := ledger.UsageEvent{
		RecordID:         "rec-001",
		TenantID:         "tenant-a",
		SKU:              "granite-3.3-8b",
		Profile:          "maas",
		Collector:        "capture-filter",
		PromptTokens:     350,
		CompletionTokens: 175,
		ReasoningTokens:  0,
		CachedTokens:     50,
		Quantities:       map[string]float64{"total_tokens": 525},
		Status:           "200",
		Stream:           &stream,
		LatencyMs:        &latency,
		Attributes:       map[string]string{"geo": "us-east-1", "key_id": "key-xyz"},
		EventTS:          ts,
	}

	got := usagesink.ConvertLedgerEvent(input)

	assert.Equal(t, "rec-001", got.RecordID)
	assert.Equal(t, "tenant-a", got.TenantID)
	assert.Equal(t, ts, got.Timestamp)
	assert.Equal(t, "inference", got.EventType, "maas profile should map to inference event type")
	assert.Equal(t, "granite-3.3-8b", got.Model)
	assert.True(t, decimal.NewFromInt(350).Equal(got.PromptTokens))
	assert.True(t, decimal.NewFromInt(175).Equal(got.CompletionTokens))
	assert.True(t, decimal.Zero.Equal(got.ReasoningTokens))
	assert.True(t, decimal.NewFromInt(50).Equal(got.CachedTokens))
	assert.Equal(t, "rec-001", got.RequestID)
	assert.Equal(t, "us-east-1", got.Metadata["geo"])
	assert.Equal(t, "key-xyz", got.Metadata["key_id"])
}

func TestConvertLedgerEvent_NilAttributes(t *testing.T) {
	got := usagesink.ConvertLedgerEvent(ledger.UsageEvent{
		RecordID: "rec-002",
		TenantID: "tenant-b",
	})

	assert.NotNil(t, got.Metadata)
	assert.Empty(t, got.Metadata)
}

func TestConvertLedgerEvents_Batch(t *testing.T) {
	events := []ledger.UsageEvent{
		{RecordID: "r1", TenantID: "t1", PromptTokens: 100},
		{RecordID: "r2", TenantID: "t2", PromptTokens: 200},
		{RecordID: "r3", TenantID: "t1", PromptTokens: 300},
	}

	got := usagesink.ConvertLedgerEvents(events)

	assert.Len(t, got, 3)
	assert.Equal(t, "r1", got[0].RecordID)
	assert.Equal(t, "r3", got[2].RecordID)
	assert.True(t, decimal.NewFromInt(200).Equal(got[1].PromptTokens))
}

func TestConvertLedgerEvent_AttributesCopied(t *testing.T) {
	attrs := map[string]string{"key": "value"}
	input := ledger.UsageEvent{
		RecordID:   "rec-003",
		Attributes: attrs,
	}

	got := usagesink.ConvertLedgerEvent(input)
	attrs["key"] = "mutated"

	assert.Equal(t, "value", got.Metadata["key"], "metadata should be a copy, not shared reference")
}
