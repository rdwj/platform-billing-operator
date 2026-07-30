-- +goose Up
CREATE TABLE usage_hourly (
  tenant_id     text NOT NULL,
  key_id        text,
  sku           text NOT NULL,
  geo           text,
  quantity_type text NOT NULL,
  hour          timestamptz NOT NULL,
  quantity      numeric NOT NULL,
  amount        numeric(18,6) NOT NULL,
  PRIMARY KEY (tenant_id, sku, quantity_type, hour, key_id, geo)
);
CREATE TABLE ledger_meta (
  key           text PRIMARY KEY,   -- e.g. 'watermark_ts'
  value         text NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE ledger_meta;
DROP TABLE usage_hourly;
