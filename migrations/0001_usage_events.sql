-- +goose Up
-- usage_events: immutable, append + idempotent upsert (ledger-schema.md,
-- reconciled to contracts/usage.v1.schema.json which is law:
--   request_id → record_id, model → sku, status 'error' → 'upstream_error',
--   + partial flag, + quantities/attributes jsonb for full envelope fidelity.
-- Typed token columns remain the MaaS-profile projection for fast queries.
CREATE TABLE usage_events (
  record_id     text PRIMARY KEY,
  schema_ver    text NOT NULL DEFAULT 'usage.v1',
  tenant_id     text NOT NULL,
  key_id        text,                        -- NULL for gpuaas profile
  sku           text NOT NULL,
  geo           text,
  profile       text NOT NULL CHECK (profile IN ('maas','gpuaas')),
  collector     text NOT NULL,               -- source.collector from the envelope
  prompt_tokens      bigint NOT NULL DEFAULT 0,
  completion_tokens  bigint NOT NULL DEFAULT 0,
  reasoning_tokens   bigint NOT NULL DEFAULT 0,
  cached_tokens      bigint NOT NULL DEFAULT 0,
  quantities    jsonb NOT NULL,              -- full usage.v1 quantities map (source of truth)
  status        text NOT NULL CHECK (status IN ('ok','client_disconnect','upstream_error')),
  partial       boolean NOT NULL DEFAULT false,
  stream        boolean,
  estimated     boolean NOT NULL DEFAULT false,
  latency_ms    numeric,
  attributes    jsonb NOT NULL DEFAULT '{}'::jsonb,
  content_hash  text NOT NULL,               -- sha256 of canonical payload; dedup-conflict detection
  event_ts      timestamptz NOT NULL,
  ingested_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_events_tenant_ts   ON usage_events (tenant_id, event_ts);
CREATE INDEX usage_events_sku_geo_ts  ON usage_events (sku, geo, event_ts);

-- +goose Down
DROP TABLE usage_events;
