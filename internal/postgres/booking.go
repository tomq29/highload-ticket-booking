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

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Hold(ctx context.Context, req booking.Request, ttl time.Duration) (booking.Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return booking.Booking{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := takeSeat(ctx, tx, req); err != nil {
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

func takeSeat(ctx context.Context, tx pgx.Tx, req booking.Request) error {
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

func insertHold(ctx context.Context, tx pgx.Tx, req booking.Request, ttl time.Duration) (booking.Booking, error) {
	held := booking.Booking{EventID: req.EventID, SeatID: req.SeatID, UserID: req.UserID}

	err := tx.QueryRow(ctx,
		`INSERT INTO bookings (event_id, seat_id, user_id, status, expires_at)
		 VALUES ($1, $2, $3, 'held', now() + make_interval(secs => $4))
		 RETURNING id, status, expires_at, created_at`,
		req.EventID, req.SeatID, req.UserID, ttl.Seconds(),
	).Scan(&held.ID, &held.Status, &held.ExpiresAt, &held.CreatedAt)
	if err != nil {
		return booking.Booking{}, insertError(err)
	}
	return held, nil
}

// insertError turns the constraints from 0001_init.sql back into domain errors,
// so a lost race reads the same whether it lost on the seat row or on the index.
func insertError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("insert booking: %w", err)
	}

	switch {
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
