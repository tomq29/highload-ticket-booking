package booking

import (
	"context"
	"fmt"
	"time"
)

// Repository holds every seat-taking attempt in a single database transaction.
// The transaction lives there and not here because the invariant it protects —
// one live booking per seat — is enforced by the schema, and because the
// strategies that resolve contention are pure SQL.
type Repository interface {
	Hold(ctx context.Context, req Request, ttl time.Duration) (Result, error)
	Confirm(ctx context.Context, id, userID int64) (Booking, error)
	Cancel(ctx context.Context, id, userID int64) error
	Seats(ctx context.Context, eventID int64) ([]Seat, error)
	Expire(ctx context.Context, limit int) (int, error)
}

type Service struct {
	repo Repository
	ttl  time.Duration
}

func NewService(repo Repository, ttl time.Duration) *Service {
	return &Service{repo: repo, ttl: ttl}
}

func (s *Service) Book(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	return s.repo.Hold(ctx, req, s.ttl)
}

func (s *Service) Confirm(ctx context.Context, id, userID int64) (Booking, error) {
	if err := identify(id, userID); err != nil {
		return Booking{}, err
	}
	return s.repo.Confirm(ctx, id, userID)
}

func (s *Service) Cancel(ctx context.Context, id, userID int64) error {
	if err := identify(id, userID); err != nil {
		return err
	}
	return s.repo.Cancel(ctx, id, userID)
}

func (s *Service) Seats(ctx context.Context, eventID int64) ([]Seat, error) {
	if eventID <= 0 {
		return nil, fmt.Errorf("%w: event id must be positive", ErrInvalidRequest)
	}
	return s.repo.Seats(ctx, eventID)
}

func identify(id, userID int64) error {
	switch {
	case id <= 0:
		return fmt.Errorf("%w: booking id must be positive", ErrInvalidRequest)
	case userID <= 0:
		return fmt.Errorf("%w: user_id must be positive", ErrInvalidRequest)
	}
	return nil
}
