package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// Strategy selects how a request takes the seat row. All three uphold the same
// invariant — the partial unique index on bookings does that — and differ only
// in how they behave while contended.
type Strategy string

const (
	Pessimistic Strategy = "pessimistic"
	Optimistic  Strategy = "optimistic"
	Atomic      Strategy = "atomic"
)

type takeFunc func(ctx context.Context, tx pgx.Tx, req booking.Request) error

var strategies = map[Strategy]takeFunc{
	Pessimistic: takePessimistic,
	Optimistic:  takeOptimistic,
	Atomic:      takeAtomic,
}

type Repository struct {
	pool *pgxpool.Pool
	take takeFunc
}

func NewRepository(pool *pgxpool.Pool, strategy Strategy) (*Repository, error) {
	take, ok := strategies[strategy]
	if !ok {
		return nil, fmt.Errorf("unknown booking strategy %q", strategy)
	}
	return &Repository{pool: pool, take: take}, nil
}

func (r *Repository) Hold(ctx context.Context, req booking.Request, ttl time.Duration) (booking.Result, error) {
	if req.IdempotencyKey != "" {
		existing, found, err := r.byKey(ctx, req.UserID, req.IdempotencyKey)
		if err != nil {
			return booking.Result{}, err
		}
		if found {
			return booking.Result{Booking: existing, Replayed: true}, nil
		}
	}

	held, err := r.hold(ctx, req, ttl)

	// Two copies of the same request raced past the lookup above, and this one
	// lost — either on the seat or on the key itself. The winner had to commit
	// for us to find that out, so its booking is now visible, and it is the
	// answer to both copies: a retry must not turn into a conflict.
	if req.IdempotencyKey != "" && (errors.Is(err, errKeyTaken) || errors.Is(err, booking.ErrSeatTaken)) {
		existing, found, lookupErr := r.byKey(ctx, req.UserID, req.IdempotencyKey)
		if lookupErr != nil {
			return booking.Result{}, lookupErr
		}
		if found {
			return booking.Result{Booking: existing, Replayed: true}, nil
		}
	}
	if err != nil {
		return booking.Result{}, err
	}
	return booking.Result{Booking: held}, nil
}

func (r *Repository) hold(ctx context.Context, req booking.Request, ttl time.Duration) (booking.Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return booking.Booking{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.take(ctx, tx, req); err != nil {
		return booking.Booking{}, err
	}

	held, err := insertHold(ctx, tx, req, ttl)
	if err != nil {
		return booking.Booking{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return booking.Booking{}, fmt.Errorf("commit: %w", err)
	}
	return held, nil
}

func (r *Repository) byKey(ctx context.Context, userID int64, key string) (booking.Booking, bool, error) {
	var found booking.Booking
	err := r.pool.QueryRow(ctx,
		`SELECT id, event_id, seat_id, user_id, status, expires_at, created_at
		   FROM bookings
		  WHERE user_id = $1 AND idempotency_key = $2`,
		userID, key,
	).Scan(&found.ID, &found.EventID, &found.SeatID, &found.UserID,
		&found.Status, &found.ExpiresAt, &found.CreatedAt)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return booking.Booking{}, false, nil
	case err != nil:
		return booking.Booking{}, false, fmt.Errorf("look up idempotency key: %w", err)
	}
	return found, true, nil
}

// takePessimistic serialises everyone on the seat row: losers wait for the
// winner to commit and only then learn the seat is gone.
func takePessimistic(ctx context.Context, tx pgx.Tx, req booking.Request) error {
	var status booking.SeatStatus
	err := tx.QueryRow(ctx,
		`SELECT status FROM seats WHERE id = $1 AND event_id = $2 FOR UPDATE`,
		req.SeatID, req.EventID,
	).Scan(&status)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return booking.ErrSeatNotFound
	case err != nil:
		return fmt.Errorf("lock seat: %w", err)
	case status != booking.SeatAvailable:
		return booking.ErrSeatTaken
	}

	if _, err := tx.Exec(ctx,
		`UPDATE seats SET status = 'held', version = version + 1 WHERE id = $1`,
		req.SeatID,
	); err != nil {
		return fmt.Errorf("hold seat: %w", err)
	}
	return nil
}

// takeOptimistic reads without a lock and writes only if the row has not moved
// since. A conflicting write is not an error, it is a repeat: the seat is read
// again, and it is usually taken by then.
func takeOptimistic(ctx context.Context, tx pgx.Tx, req booking.Request) error {
	const attempts = 3

	for range attempts {
		var (
			status  booking.SeatStatus
			version int32
		)
		err := tx.QueryRow(ctx,
			`SELECT status, version FROM seats WHERE id = $1 AND event_id = $2`,
			req.SeatID, req.EventID,
		).Scan(&status, &version)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return booking.ErrSeatNotFound
		case err != nil:
			return fmt.Errorf("read seat: %w", err)
		case status != booking.SeatAvailable:
			return booking.ErrSeatTaken
		}

		tag, err := tx.Exec(ctx,
			`UPDATE seats SET status = 'held', version = version + 1
			  WHERE id = $1 AND version = $2`,
			req.SeatID, version,
		)
		if err != nil {
			return fmt.Errorf("hold seat: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return booking.ErrSeatTaken
}

// takeAtomic leaves the decision to one statement: the row is updated only if
// it is still available, and the second query runs only for the loser.
func takeAtomic(ctx context.Context, tx pgx.Tx, req booking.Request) error {
	tag, err := tx.Exec(ctx,
		`UPDATE seats SET status = 'held', version = version + 1
		  WHERE id = $1 AND event_id = $2 AND status = 'available'`,
		req.SeatID, req.EventID,
	)
	if err != nil {
		return fmt.Errorf("hold seat: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM seats WHERE id = $1 AND event_id = $2)`,
		req.SeatID, req.EventID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check seat: %w", err)
	}
	if !exists {
		return booking.ErrSeatNotFound
	}
	return booking.ErrSeatTaken
}

func insertHold(ctx context.Context, tx pgx.Tx, req booking.Request, ttl time.Duration) (booking.Booking, error) {
	held := booking.Booking{EventID: req.EventID, SeatID: req.SeatID, UserID: req.UserID}

	err := tx.QueryRow(ctx,
		`INSERT INTO bookings (event_id, seat_id, user_id, status, expires_at, idempotency_key)
		 VALUES ($1, $2, $3, 'held', now() + make_interval(secs => $4), nullif($5, ''))
		 RETURNING id, status, expires_at, created_at`,
		req.EventID, req.SeatID, req.UserID, ttl.Seconds(), req.IdempotencyKey,
	).Scan(&held.ID, &held.Status, &held.ExpiresAt, &held.CreatedAt)
	if err != nil {
		return booking.Booking{}, insertError(err)
	}
	return held, nil
}

// errKeyTaken is internal: it says the insert lost to a concurrent request
// carrying the same idempotency key, which is not a failure to report.
var errKeyTaken = errors.New("idempotency key already used")

// insertError turns the constraints from the migrations back into domain
// errors, so a lost race reads the same whether it lost on the seat row or on
// the index.
func insertError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("insert booking: %w", err)
	}

	switch {
	case pgErr.Code == uniqueViolation && pgErr.ConstraintName == "bookings_idempotency":
		return errKeyTaken
	case pgErr.Code == uniqueViolation:
		return booking.ErrSeatTaken
	case pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == "bookings_user_fkey":
		return booking.ErrUserNotFound
	case pgErr.Code == foreignKeyViolation:
		return booking.ErrSeatNotFound
	}
	return fmt.Errorf("insert booking: %w", err)
}

func (r *Repository) Seats(ctx context.Context, eventID int64) ([]booking.Seat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, event_id, row_label, number, status
		   FROM seats
		  WHERE event_id = $1
		  ORDER BY row_label, number`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("query seats: %w", err)
	}

	seats, err := pgx.CollectRows(rows, pgx.RowToStructByPos[booking.Seat])
	if err != nil {
		return nil, fmt.Errorf("collect seats: %w", err)
	}

	if len(seats) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)`, eventID,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check event: %w", err)
		}
		if !exists {
			return nil, booking.ErrEventNotFound
		}
	}
	return seats, nil
}
