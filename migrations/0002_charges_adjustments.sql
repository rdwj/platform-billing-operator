-- +goose Up
CREATE TABLE charges (
  charge_id     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  record_id     text NOT NULL REFERENCES usage_events(record_id),
  tenant_id     text NOT NULL,
  price_book    text NOT NULL,
  price_book_rev int  NOT NULL,
  amount        numeric(18,6) NOT NULL,
  currency      text NOT NULL,
  components    jsonb NOT NULL,
  warnings      jsonb NOT NULL DEFAULT '[]'::jsonb,
  rated_at      timestamptz NOT NULL,
  UNIQUE (record_id)                          -- one charge per event; re-rating = adjustment
);
CREATE INDEX charges_tenant_rated ON charges (tenant_id, rated_at);

CREATE TABLE adjustments (
  adjustment_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     text NOT NULL,
  kind          text NOT NULL CHECK (kind IN
    ('reload','manual_credit','manual_debit','sla_credit','promo','volume_tier_rebate',
     'writeoff','unmetered_gap','dispute_hold','dispute_release','refund','balance_carry_in')),
  amount        numeric(18,6) NOT NULL,       -- signed: credits +, debits −
  currency      text NOT NULL,
  reason        text NOT NULL,
  actor         text NOT NULL,
  psp_ref       text,
  period        text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX adjustments_tenant_created ON adjustments (tenant_id, created_at);

-- Append-only enforcement (ledger-schema.md "Cross-cutting: Audit"):
-- corrections are new adjustment rows; UPDATE/DELETE on money tables is a bug.
-- +goose StatementBegin
CREATE FUNCTION ledger_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'ledger table % is append-only (attempted %)', TG_TABLE_NAME, TG_OP;
END $$;
-- +goose StatementEnd
CREATE TRIGGER charges_append_only     BEFORE UPDATE OR DELETE ON charges     FOR EACH ROW EXECUTE FUNCTION ledger_append_only();
CREATE TRIGGER adjustments_append_only BEFORE UPDATE OR DELETE ON adjustments FOR EACH ROW EXECUTE FUNCTION ledger_append_only();

-- +goose Down
DROP TRIGGER adjustments_append_only ON adjustments;
DROP TRIGGER charges_append_only ON charges;
DROP FUNCTION ledger_append_only;
DROP TABLE adjustments;
DROP TABLE charges;
