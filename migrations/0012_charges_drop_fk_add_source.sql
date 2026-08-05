-- +goose Up
-- Reservation charges don't originate from usage events. Drop the FK on
-- record_id so we can insert charges with source='reservation' whose
-- record_id follows the format "resv-{uid}-{period}".
ALTER TABLE charges
  DROP CONSTRAINT charges_record_id_fkey;
ALTER TABLE charges
  ADD COLUMN source text NOT NULL DEFAULT 'usage';
ALTER TABLE charges
  ADD CONSTRAINT charges_source_check CHECK (source IN ('usage', 'reservation'));

-- +goose Down
ALTER TABLE charges DROP CONSTRAINT charges_source_check;
ALTER TABLE charges DROP COLUMN source;
ALTER TABLE charges
  ADD CONSTRAINT charges_record_id_fkey
  FOREIGN KEY (record_id) REFERENCES usage_events(record_id);
