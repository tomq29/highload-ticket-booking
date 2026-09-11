-- +goose Up
-- Seeded so that a fresh `docker compose up` is immediately bookable and the
-- load tests have something to contend for.
INSERT INTO events (id, name, venue, starts_at)
VALUES (1, 'Kissonik Debut', 'Astana Arena', now() + interval '30 days');

SELECT setval('events_id_seq', (SELECT max(id) FROM events));

INSERT INTO seats (event_id, row_label, number)
SELECT 1, chr(65 + n / 20), n % 20 + 1
FROM generate_series(0, 99) AS n;

INSERT INTO users (name)
SELECT 'user-' || n
FROM generate_series(1, 500) AS n;

-- +goose Down
DELETE FROM bookings WHERE event_id = 1;
DELETE FROM seats WHERE event_id = 1;
DELETE FROM events WHERE id = 1;
DELETE FROM users WHERE name LIKE 'user-%';
