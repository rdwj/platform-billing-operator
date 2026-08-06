-- +goose Up
ALTER TABLE dlq ADD COLUMN attempts int NOT NULL DEFAULT 0;
ALTER TABLE dlq ADD COLUMN last_error text;

-- +goose Down
ALTER TABLE dlq DROP COLUMN last_error;
ALTER TABLE dlq DROP COLUMN attempts;
