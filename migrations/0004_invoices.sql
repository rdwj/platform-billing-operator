-- +goose Up
CREATE TABLE invoices (
  invoice_id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     text NOT NULL,
  period        text NOT NULL,
  status        text NOT NULL CHECK (status IN ('accruing','finalized','pushed','settled','disputed','written_off')),
  total         numeric(18,6) NOT NULL,
  psp_ref       text,
  finalized_at  timestamptz,
  UNIQUE (tenant_id, period)
);
CREATE TABLE invoice_lines (
  invoice_id    uuid REFERENCES invoices,
  line_no       int,
  description   text NOT NULL,
  sku           text, geo text, quantity_type text,
  quantity      bigint,
  rate          numeric(18,6),
  amount        numeric(18,6) NOT NULL,
  charge_query  jsonb NOT NULL,
  PRIMARY KEY (invoice_id, line_no)
);
-- Finalized invoices are append-only; 'accruing' rows may be updated.
CREATE FUNCTION invoices_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'invoices are never deleted';
  END IF;
  IF OLD.status <> 'accruing' AND NEW.status = 'accruing' THEN
    RAISE EXCEPTION 'invoice cannot return to accruing from %', OLD.status;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER invoices_guard BEFORE UPDATE OR DELETE ON invoices FOR EACH ROW EXECUTE FUNCTION invoices_guard();

-- +goose Down
DROP TRIGGER invoices_guard ON invoices;
DROP FUNCTION invoices_guard;
DROP TABLE invoice_lines;
DROP TABLE invoices;
