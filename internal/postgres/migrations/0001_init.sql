-- +goose Up
CREATE TABLE events (
    id        bigserial PRIMARY KEY,
    name      text        NOT NULL,
    venue     text        NOT NULL,
    starts_at timestamptz NOT NULL
);

CREATE TABLE users (
    id   bigserial PRIMARY KEY,
    name text NOT NULL UNIQUE
);

CREATE TYPE seat_status AS ENUM ('available', 'held', 'sold');

CREATE TABLE seats (
    id        bigserial PRIMARY KEY,
    event_id  bigint      NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    row_label text        NOT NULL,
    number    int         NOT NULL,
    status    seat_status NOT NULL DEFAULT 'available',
    version   int         NOT NULL DEFAULT 0,
    UNIQUE (event_id, row_label, number),
    -- Redundant on its own, but a composite foreign key needs a matching
    -- unique constraint: it is what lets bookings reference (seat, event).
    UNIQUE (id, event_id)
);

CREATE TYPE booking_status AS ENUM ('held', 'confirmed', 'cancelled', 'expired');

CREATE TABLE bookings (
    id         bigserial PRIMARY KEY,
    event_id   bigint         NOT NULL,
    seat_id    bigint         NOT NULL,
    user_id    bigint         NOT NULL,
    status     booking_status NOT NULL,
    expires_at timestamptz,
    created_at timestamptz    NOT NULL DEFAULT now(),
    updated_at timestamptz    NOT NULL DEFAULT now(),
    CONSTRAINT bookings_user_fkey FOREIGN KEY (user_id) REFERENCES users (id),
    -- A booking cannot name an event its seat does not belong to.
    CONSTRAINT bookings_seat_fkey FOREIGN KEY (seat_id, event_id) REFERENCES seats (id, event_id)
);

-- The invariant of the whole service, enforced by the database rather than by
-- application code: a seat carries at most one booking that is still alive.
CREATE UNIQUE INDEX bookings_one_live_per_seat
    ON bookings (seat_id)
    WHERE status IN ('held', 'confirmed');

CREATE INDEX bookings_expiring ON bookings (expires_at) WHERE status = 'held';

-- Postgres does not index the referencing side of a foreign key on its own,
-- and the unique index above covers live bookings only. Without this one,
-- touching a seat row makes the database scan every booking ever made to
-- prove the seat is not referenced.
CREATE INDEX bookings_seat ON bookings (seat_id, event_id);

-- +goose Down
DROP TABLE bookings;
DROP TYPE booking_status;
DROP TABLE seats;
DROP TYPE seat_status;
DROP TABLE users;
DROP TABLE events;
