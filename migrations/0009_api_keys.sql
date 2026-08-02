-- +goose Up
CREATE TABLE api_keys (
  id          BIGSERIAL PRIMARY KEY,
  key_name    TEXT NOT NULL,
  tenant_id   TEXT NOT NULL,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  source      TEXT NOT NULL DEFAULT 'manual',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  disabled_at TIMESTAMPTZ,
  UNIQUE(tenant_id, key_name)
);

CREATE INDEX idx_api_keys_tenant ON api_keys (tenant_id);

-- +goose Down
DROP TABLE IF EXISTS api_keys;
