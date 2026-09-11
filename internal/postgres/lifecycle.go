package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

func (r *Repository) Confirm(ctx context.Context, id, userID int64) (booking.Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return booking.Booking{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	found, expired, err := lockBooking(ctx, tx, id, userID)
	if err != nil {
		return booking.Booking{}, err
	}
	if found.Status != booking.StatusHeld || expired {
		return booking.Booking{}, booking.ErrBookingNotHeld
	}

	if err := tx.QueryRow(ctx,
		`UPDATE bookings SET status = 'confirmed', updated_at = now()
		  WHERE id = $1
		 RETURNING status`, id,
	).Scan(&found.Status); err != nil {
		return booking.Booking{}, fmt.Errorf("confirm booking: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE seats SET status = 'sold', version = version + 1 WHERE id = $1`,
		found.SeatID,
	); err != nil {
		return booking.Booking{}, fmt.Errorf("sell seat: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return booking.Booking{}, fmt.Errorf("commit: %w", err)
	}
	return found, nil
}

func (r *Repository) Cancel(ctx context.Context, id, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	found, _, err := lockBooking(ctx, tx, id, userID)
	if err != nil {
		return err
	}
	// An expired hold can still be cancelled: the seat is free either way, and
	// refusing would only puzzle the caller.
	if found.Status != booking.StatusHeld {
		return booking.ErrBookingNotHeld
	}

	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET status = 'cancelled', updated_at = now() WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("cancel booking: %w", err)
	}

	if err := releaseSeat(ctx, tx, found.SeatID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Expire releases holds nobody confirmed. SKIP LOCKED lets several workers run
// at once without waiting on each other or handing the same booking out twice.
func (r *Repository) Expire(ctx context.Context, limit int) (int, error) {
	rows, err := r.pool.Query(ctx,
		`WITH due AS (
		     SELECT id, seat_id
		       FROM bookings
		      WHERE status = 'held' AND expires_at <= now()
		      ORDER BY expires_at
		      LIMIT $1
		        FOR UPDATE SKIP LOCKED
		 ), released AS (
		     UPDATE bookings SET status = 'expired', updated_at = now()
		      WHERE id IN (SELECT id FROM due)
		     RETURNING seat_id
		 )
		 UPDATE seats SET status = 'available', version = version + 1
		  WHERE id IN (SELECT seat_id FROM released) AND status = 'held'
		 RETURNING id`,
		limit,
	)
	if err != nil {
		return 0, fmt.Errorf("expire holds: %w", err)
	}
	defer rows.Close()

	released := 0
	for rows.Next() {
		released++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("expire holds: %w", err)
	}
	return released, nil
}

func lockBooking(ctx context.Context, tx pgx.Tx, id, userID int64) (booking.Booking, bool, error) {
	var (
		found   booking.Booking
		expired bool
	)
	// The database decides whether the hold has run out: two clocks would
	// eventually disagree about it.
	err := tx.QueryRow(ctx,
		`SELECT id, event_id, seat_id, user_id, status, expires_at, created_at, expires_at <= now()
		   FROM bookings
		  WHERE id = $1 AND user_id = $2
		    FOR UPDATE`,
		id, userID,
	).Scan(&found.ID, &found.EventID, &found.SeatID, &found.UserID,
		&found.Status, &found.ExpiresAt, &found.CreatedAt, &expired)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return booking.Booking{}, false, booking.ErrBookingNotFound
	case err != nil:
		return booking.Booking{}, false, fmt.Errorf("lock booking: %w", err)
	}
	return found, expired, nil
}

func releaseSeat(ctx context.Context, tx pgx.Tx, seatID int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE seats SET status = 'available', version = version + 1
		  WHERE id = $1 AND status = 'held'`,
		seatID,
	); err != nil {
		return fmt.Errorf("release seat: %w", err)
	}
	return nil
}
