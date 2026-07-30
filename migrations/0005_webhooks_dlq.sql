-- +goose Up
CREATE TABLE webhook_deliveries (
  delivery_id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type    text NOT NULL,
  tenant_id     text,
  endpoint      text NOT NULL,
  payload_hash  text NOT NULL,
  status_code   int,
  attempts      int NOT NULL DEFAULT 0,
  next_retry_at timestamptz,
  delivered_at  timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE dlq (
  dlq_id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  raw           jsonb NOT NULL,
  reason        text NOT NULL,      -- 'parse_error' | 'schema_unknown' | 'dedup_conflict' | 'unknown_sku' | ...
  source        text NOT NULL,
  received_at   timestamptz NOT NULL DEFAULT now(),
  replayed_at   timestamptz
);
CREATE INDEX dlq_unreplayed ON dlq (received_at) WHERE replayed_at IS NULL;

-- +goose Down
DROP TABLE dlq;
DROP TABLE webhook_deliveries;
