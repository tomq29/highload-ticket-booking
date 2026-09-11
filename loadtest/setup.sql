-- Puts the load-test event back to all-available. It has its own event id and
-- its own seat id range, so the demo event seeded by the migrations is left
-- alone, and the seats are created once and then reused between runs.
BEGIN;

TRUNCATE bookings RESTART IDENTITY;

INSERT INTO events (id, name, venue, starts_at)
VALUES (2, 'Load test', 'Nowhere', now() + interval '1 year')
ON CONFLICT (id) DO NOTHING;

SELECT setval('events_id_seq', (SELECT max(id) FROM events));

INSERT INTO seats (id, event_id, row_label, number)
SELECT 1000000 + n, 2, 'L', n
FROM generate_series(1, 1000000) AS n
ON CONFLICT (id) DO NOTHING;

SELECT setval('seats_id_seq', (SELECT max(id) FROM seats));

UPDATE seats SET status = 'available', version = 0 WHERE status <> 'available';

COMMIT;

VACUUM ANALYZE seats;
VACUUM ANALYZE bookings;
