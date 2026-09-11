package booking

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubRepo struct {
	got Request
	ttl time.Duration
}

func (s *stubRepo) Hold(_ context.Context, req Request, ttl time.Duration) (Booking, error) {
	s.got, s.ttl = req, ttl
	return Booking{ID: 1, EventID: req.EventID, SeatID: req.SeatID, UserID: req.UserID, Status: StatusHeld}, nil
}

func (s *stubRepo) Seats(context.Context, int64) ([]Seat, error) {
	return []Seat{{ID: 1, EventID: 1, Row: "A", Number: 1, Status: SeatAvailable}}, nil
}

func TestBookRejectsInvalidRequests(t *testing.T) {
	cases := map[string]Request{
		"no event": {SeatID: 1, UserID: 1},
		"no seat":  {EventID: 1, UserID: 1},
		"no user":  {EventID: 1, SeatID: 1},
		"negative": {EventID: 1, SeatID: -1, UserID: 1},
	}

	repo := &stubRepo{}
	service := NewService(repo, time.Minute)

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.Book(context.Background(), req)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidRequest)
			}
			if repo.got != (Request{}) {
				t.Fatal("invalid request reached the repository")
			}
		})
	}
}

func TestBookAppliesTheConfiguredTTL(t *testing.T) {
	repo := &stubRepo{}
	service := NewService(repo, 90*time.Second)

	req := Request{EventID: 1, SeatID: 2, UserID: 3}
	held, err := service.Book(context.Background(), req)
	if err != nil {
		t.Fatalf("book: %v", err)
	}

	if repo.got != req {
		t.Errorf("repository got %+v, want %+v", repo.got, req)
	}
	if repo.ttl != 90*time.Second {
		t.Errorf("ttl = %s, want 90s", repo.ttl)
	}
	if held.Status != StatusHeld {
		t.Errorf("status = %q, want %q", held.Status, StatusHeld)
	}
}

func TestSeatsRejectsInvalidEvent(t *testing.T) {
	service := NewService(&stubRepo{}, time.Minute)

	if _, err := service.Seats(context.Background(), 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidRequest)
	}
}
