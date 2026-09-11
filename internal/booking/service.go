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
	Hold(ctx context.Context, req Request, ttl time.Duration) (Booking, error)
	Seats(ctx context.Context, eventID int64) ([]Seat, error)
}

type Service struct {
	repo Repository
	ttl  time.Duration
}

func NewService(repo Repository, ttl time.Duration) *Service {
	return &Service{repo: repo, ttl: ttl}
}

func (s *Service) Book(ctx context.Context, req Request) (Booking, error) {
	if err := req.Validate(); err != nil {
		return Booking{}, err
	}
	return s.repo.Hold(ctx, req, s.ttl)
}

func (s *Service) Seats(ctx context.Context, eventID int64) ([]Seat, error) {
	if eventID <= 0 {
		return nil, fmt.Errorf("%w: event id must be positive", ErrInvalidRequest)
	}
	return s.repo.Seats(ctx, eventID)
}
