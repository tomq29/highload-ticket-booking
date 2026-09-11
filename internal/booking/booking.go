package booking

import (
	"errors"
	"fmt"
	"time"
)

type SeatStatus string

const (
	SeatAvailable SeatStatus = "available"
	SeatHeld      SeatStatus = "held"
	SeatSold      SeatStatus = "sold"
)

type Status string

const (
	StatusHeld      Status = "held"
	StatusConfirmed Status = "confirmed"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

type Seat struct {
	ID      int64
	EventID int64
	Row     string
	Number  int
	Status  SeatStatus
}

type Booking struct {
	ID        int64
	EventID   int64
	SeatID    int64
	UserID    int64
	Status    Status
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Request struct {
	EventID        int64
	SeatID         int64
	UserID         int64
	IdempotencyKey string
}

const maxIdempotencyKey = 255

func (r Request) Validate() error {
	switch {
	case r.EventID <= 0:
		return fmt.Errorf("%w: event_id must be positive", ErrInvalidRequest)
	case r.SeatID <= 0:
		return fmt.Errorf("%w: seat_id must be positive", ErrInvalidRequest)
	case r.UserID <= 0:
		return fmt.Errorf("%w: user_id must be positive", ErrInvalidRequest)
	case len(r.IdempotencyKey) > maxIdempotencyKey:
		return fmt.Errorf("%w: idempotency key is longer than %d bytes", ErrInvalidRequest, maxIdempotencyKey)
	}
	return nil
}

// Result reports whether the booking was made now or is the one an earlier
// request with the same idempotency key already made.
type Result struct {
	Booking  Booking
	Replayed bool
}

var (
	ErrInvalidRequest  = errors.New("invalid request")
	ErrEventNotFound   = errors.New("event not found")
	ErrSeatNotFound    = errors.New("seat not found")
	ErrUserNotFound    = errors.New("user not found")
	ErrBookingNotFound = errors.New("booking not found")
	ErrBookingNotHeld  = errors.New("booking is no longer held")
	ErrSeatTaken       = errors.New("seat is already taken")
)
