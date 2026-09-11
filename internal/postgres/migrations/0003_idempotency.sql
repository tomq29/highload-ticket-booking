-- +goose Up
ALTER TABLE bookings ADD COLUMN idempotency_key text;

-- Partial, so the column costs nothing for the callers that do not send a key.
CREATE UNIQUE INDEX bookings_idempotency
    ON bookings (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX bookings_idempotency;
ALTER TABLE bookings DROP COLUMN idempotency_key;
