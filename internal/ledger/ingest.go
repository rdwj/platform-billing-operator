package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Ingester struct {
	store  *Store
	schema *jsonschema.Schema
}

func NewIngester(store *Store) (*Ingester, error) {
	schema, err := loadSchema()
	if err != nil {
		return nil, fmt.Errorf("load usage.v1 schema: %w", err)
	}
	return &Ingester{store: store, schema: schema}, nil
}

// IngestEvent implements the same contract as 0007_ingest_fn.sql:
// idempotent upsert on record_id, content_hash for conflict detection,
// DLQ for unknown schemas and conflicting content.
func (ing *Ingester) IngestEvent(ctx context.Context, rawJSON json.RawMessage) (IngestResult, error) {
	if err := validateEvent(ing.schema, rawJSON); err != nil {
		return ValidationFailed, fmt.Errorf("schema validation: %w", err)
	}

	var ev rawEvent
	if err := json.Unmarshal(rawJSON, &ev); err != nil {
		return ValidationFailed, fmt.Errorf("parse event: %w", err)
	}

	contentHash := computeHash(rawJSON)
	collector := ev.Source.Collector
	if collector == "" {
		collector = "unknown"
	}

	if ev.SchemaVersion != "usage.v1" {
		if err := ing.store.InsertDLQ(ctx, rawJSON, "schema_unknown", collector); err != nil {
			return "", fmt.Errorf("insert DLQ (schema_unknown): %w", err)
		}
		return SchemaUnknownDLQ, nil
	}

	existingHash, found, err := ing.store.GetContentHash(ctx, ev.RecordID)
	if err != nil {
		return "", fmt.Errorf("check existing record: %w", err)
	}
	if found {
		if existingHash == contentHash {
			return Duplicate, nil
		}
		if err := ing.store.InsertDLQ(ctx, rawJSON, "dedup_conflict", collector); err != nil {
			return "", fmt.Errorf("insert DLQ (dedup_conflict): %w", err)
		}
		return ConflictDLQ, nil
	}

	eventTS, err := time.Parse(time.RFC3339, ev.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("parse occurred_at: %w", err)
	}

	toInsert := &UsageEvent{
		RecordID:         ev.RecordID,
		SchemaVer:        ev.SchemaVersion,
		TenantID:         ev.TenantID,
		KeyID:            ev.KeyID,
		SKU:              ev.SKU,
		Geo:              ev.Geo,
		Profile:          ev.Source.Profile,
		Collector:        ev.Source.Collector,
		PromptTokens:     int64(ev.Quantities["prompt_tokens"]),
		CompletionTokens: int64(ev.Quantities["completion_tokens"]),
		ReasoningTokens:  int64(ev.Quantities["reasoning_tokens"]),
		CachedTokens:     int64(ev.Quantities["cached_tokens"]),
		Quantities:       ev.Quantities,
		Status:           ev.Status,
		Partial:          ev.Partial,
		Stream:           ev.Stream,
		LatencyMs:        ev.LatencyMs,
		Attributes:       ev.Attributes,
		ContentHash:      contentHash,
		EventTS:          eventTS,
	}

	if err := ing.store.InsertEvent(ctx, toInsert); err != nil {
		return "", fmt.Errorf("insert event: %w", err)
	}

	if err := ing.store.AdvanceWatermark(ctx, eventTS); err != nil {
		return "", fmt.Errorf("advance watermark: %w", err)
	}

	return Inserted, nil
}

func computeHash(raw json.RawMessage) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
