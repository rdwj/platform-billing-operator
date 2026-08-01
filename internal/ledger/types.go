package ledger

import "time"

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
