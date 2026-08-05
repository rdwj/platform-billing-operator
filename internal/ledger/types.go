package ledger

import (
	"encoding/json"
	"time"
)

type IngestResult string

const (
	Inserted         IngestResult = "inserted"
	Duplicate        IngestResult = "duplicate"
	ConflictDLQ      IngestResult = "conflict_dlq"
	SchemaUnknownDLQ IngestResult = "schema_unknown_dlq"
	ValidationFailed IngestResult = "validation_failed"
)

type UsageEvent struct {
	RecordID         string             `json:"record_id"`
	SchemaVer        string             `json:"schema_version"`
	TenantID         string             `json:"tenant_id"`
	KeyID            string             `json:"key_id,omitempty"`
	SKU              string             `json:"sku"`
	Geo              string             `json:"geo,omitempty"`
	Profile          string             `json:"profile"`
	Collector        string             `json:"collector"`
	PromptTokens     int64              `json:"prompt_tokens"`
	CompletionTokens int64              `json:"completion_tokens"`
	ReasoningTokens  int64              `json:"reasoning_tokens"`
	CachedTokens     int64              `json:"cached_tokens"`
	Quantities       map[string]float64 `json:"quantities"`
	Status           string             `json:"status"`
	Partial          bool               `json:"partial"`
	Stream           *bool              `json:"stream,omitempty"`
	LatencyMs        *float64           `json:"latency_ms,omitempty"`
	Attributes       map[string]string  `json:"attributes,omitempty"`
	ContentHash      string             `json:"content_hash"`
	EventTS          time.Time          `json:"occurred_at"`
	IngestedAt       time.Time          `json:"ingested_at"`
}

type QueryFilter struct {
	TenantID  string
	SKU       string
	StartTime time.Time
	EndTime   time.Time
	Limit     int32
	PageToken string
}

type QueryResult struct {
	Events        []UsageEvent
	NextPageToken string
}

// rawEvent is the intermediate parsed form of a usage.v1 JSON event.
type rawEvent struct {
	SchemaVersion string             `json:"schema_version"`
	RecordID      string             `json:"record_id"`
	OccurredAt    string             `json:"occurred_at"`
	TenantID      string             `json:"tenant_id"`
	KeyID         string             `json:"key_id,omitempty"`
	Source        rawSource          `json:"source"`
	SKU           string             `json:"sku"`
	Geo           string             `json:"geo,omitempty"`
	Quantities    map[string]float64 `json:"quantities"`
	Status        string             `json:"status"`
	Partial       bool               `json:"partial"`
	Stream        *bool              `json:"stream,omitempty"`
	LatencyMs     *float64           `json:"latency_ms,omitempty"`
	Attributes    map[string]string  `json:"attributes,omitempty"`
}

type rawSource struct {
	Profile          string `json:"profile"`
	Collector        string `json:"collector"`
	CollectorVersion string `json:"collector_version,omitempty"`
}

type UsageBucket struct {
	Start      time.Time          `json:"start"`
	SKU        string             `json:"sku"`
	Quantities map[string]float64 `json:"quantities"`
	Charged    string             `json:"charged"`
	Events     int                `json:"events"`
}

type AdjustmentInput struct {
	TenantID       string `json:"tenant"`
	Amount         string `json:"amount"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"idempotency_key"`
}

type AdjustmentRow struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant"`
	Amount         string    `json:"amount"`
	Kind           string    `json:"kind"`
	Reason         string    `json:"reason"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

// LedgerEntry is a combined view of charges and adjustments for the tenant
// activity ledger. Charges appear as negative amounts; adjustments retain
// their original sign. BalanceAfter is the running sum computed chronologically.
type LedgerEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Type         string    `json:"type"`
	Description  string    `json:"description"`
	Amount       string    `json:"amount"`
	BalanceAfter string    `json:"balance_after"`
	RefID        string    `json:"ref_id"`
}

type DriftReport struct {
	ID           int64     `json:"id"`
	TenantID     string    `json:"tenant_id"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`
	LedgerTokens int64     `json:"ledger_tokens"`
	PromTokens   int64     `json:"prom_tokens"`
	DriftPct     string    `json:"drift_pct"`
	TolerancePct string    `json:"tolerance_pct"`
	Exceeded     bool      `json:"exceeded"`
	Signature    string    `json:"signature"`
	CreatedAt    time.Time `json:"created_at"`
}

type Invoice struct {
	InvoiceID   string     `json:"invoice_id"`
	TenantID    string     `json:"tenant_id"`
	Period      string     `json:"period"`
	Status      string     `json:"status"`
	Total       string     `json:"total"`
	PSPRef      string     `json:"psp_ref,omitempty"`
	FinalizedAt *time.Time `json:"finalized_at,omitempty"`
}

type InvoiceLine struct {
	InvoiceID    string          `json:"invoice_id"`
	LineNo       int             `json:"line_no"`
	Description  string          `json:"description"`
	SKU          string          `json:"sku,omitempty"`
	Geo          string          `json:"geo,omitempty"`
	QuantityType string          `json:"quantity_type,omitempty"`
	Quantity     int64           `json:"quantity"`
	Rate         string          `json:"rate,omitempty"`
	Amount       string          `json:"amount"`
	ChargeQuery  json.RawMessage `json:"charge_query"`
}

type InvoiceWithLines struct {
	Invoice
	Lines []InvoiceLine `json:"lines"`
}
