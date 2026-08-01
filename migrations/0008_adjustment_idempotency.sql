-- +goose Up
ALTER TABLE adjustments ADD COLUMN idempotency_key text;
CREATE UNIQUE INDEX adjustments_idempotency ON adjustments (idempotency_key)
  WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX adjustments_idempotency;
ALTER TABLE adjustments DROP COLUMN idempotency_key;
