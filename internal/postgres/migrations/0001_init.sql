-- +goose Up
CREATE TABLE events (
    id serial PRIMARY KEY,
    name TEXT NOT NULL,
    data TIMESTAMP NOT NULL,
    location text
);

CREATE TABLE users (
    id serial PRIMARY KEY,
    name TEXT UNIQUE
);

CREATE TYPE seat_status AS ENUM ('available', 'booked', 'sold');

CREATE TABLE seats(
    id serial PRIMARY KEY,
    status seat_status DEFAULT 'available',
    event_id int NOT NULL REFERENCES events(id)
);

CREATE TYPE ticket_status as ENUM ('processing', 'success', 'failed');

CREATE TABLE tickets (
    id serial PRIMARY KEY,
    event_id int NOT NULL REFERENCES events(id),
    seats_id int NOT NULL REFERENCES seats(id),
    status ticket_status,
    user_id int NOT NULL REFERENCES users(id)
);