-- +goose Up
-- Reference implementation of the ingest semantics (controllers.md §3):
-- idempotent upsert on record_id; a re-send with IDENTICAL content is a
-- silent duplicate; a re-send with DIFFERENT content NEVER overwrites — it
-- lands in the DLQ as 'dedup_conflict'. ledger-svc (Go) implements the same
-- contract; this function makes the semantics executable + testable in SQL.
-- +goose StatementBegin
CREATE FUNCTION ledger_ingest_event(ev jsonb) RETURNS text LANGUAGE plpgsql AS $$
DECLARE
  hash text := encode(sha256(convert_to(ev::text, 'UTF8')), 'hex');
  existing_hash text;
BEGIN
  IF ev->>'schema_version' IS DISTINCT FROM 'usage.v1' THEN
    INSERT INTO dlq (raw, reason, source) VALUES (ev, 'schema_unknown', COALESCE(ev#>>'{source,collector}','unknown'));
    RETURN 'schema_unknown_dlq';
  END IF;

  SELECT content_hash INTO existing_hash FROM usage_events WHERE record_id = ev->>'record_id';
  IF FOUND THEN
    IF existing_hash = hash THEN
      RETURN 'duplicate';
    END IF;
    INSERT INTO dlq (raw, reason, source) VALUES (ev, 'dedup_conflict', COALESCE(ev#>>'{source,collector}','unknown'));
    RETURN 'conflict_dlq';
  END IF;

  INSERT INTO usage_events (
    record_id, tenant_id, key_id, sku, geo, profile, collector,
    prompt_tokens, completion_tokens, reasoning_tokens, cached_tokens,
    quantities, status, partial, stream, estimated, latency_ms, attributes,
    content_hash, event_ts)
  VALUES (
    ev->>'record_id', ev->>'tenant_id', ev->>'key_id', ev->>'sku', ev->>'geo',
    ev#>>'{source,profile}', ev#>>'{source,collector}',
    COALESCE((ev#>>'{quantities,prompt_tokens}')::bigint, 0),
    COALESCE((ev#>>'{quantities,completion_tokens}')::bigint, 0),
    COALESCE((ev#>>'{quantities,reasoning_tokens}')::bigint, 0),
    COALESCE((ev#>>'{quantities,cached_tokens}')::bigint, 0),
    ev->'quantities', ev->>'status',
    COALESCE((ev->>'partial')::boolean, false),
    (ev->>'stream')::boolean,
    false,
    (ev->>'latency_ms')::numeric,
    COALESCE(ev->'attributes', '{}'::jsonb),
    hash, (ev->>'occurred_at')::timestamptz)
  ON CONFLICT (record_id) DO NOTHING;   -- race-safe: concurrent identical insert
  RETURN 'inserted';
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION ledger_ingest_event;
