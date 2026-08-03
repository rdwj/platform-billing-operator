-- +goose Up
CREATE TABLE drift_reports (
  id             bigserial PRIMARY KEY,
  tenant_id      text NOT NULL,
  window_start   timestamptz NOT NULL,
  window_end     timestamptz NOT NULL,
  ledger_tokens  bigint NOT NULL,
  prom_tokens    bigint NOT NULL,
  drift_pct      numeric(20,14) NOT NULL,
  tolerance_pct  numeric(20,14) NOT NULL,
  exceeded       boolean NOT NULL,
  signature      text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX drift_reports_tenant_ts ON drift_reports (tenant_id, created_at DESC);

-- +goose Down
DROP TABLE drift_reports;
