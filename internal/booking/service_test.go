package booking

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type stubRepo struct {
	got     Request
	ttl     time.Duration
	touched atomic.Bool
}

func (s *stubRepo) Hold(_ context.Context, req Request, ttl time.Duration) (Result, error) {
	s.got, s.ttl = req, ttl
	return Result{Booking: Booking{
		ID: 1, EventID: req.EventID, SeatID: req.SeatID, UserID: req.UserID, Status: StatusHeld,
	}}, nil
}

func (s *stubRepo) Confirm(_ context.Context, id, userID int64) (Booking, error) {
	s.touched.Store(true)
	return Booking{ID: id, UserID: userID, Status: StatusConfirmed}, nil
}

func (s *stubRepo) Cancel(context.Context, int64, int64) error {
	s.touched.Store(true)
	return nil
}

func (s *stubRepo) Seats(context.Context, int64) ([]Seat, error) {
	return []Seat{{ID: 1, EventID: 1, Row: "A", Number: 1, Status: SeatAvailable}}, nil
}

func (s *stubRepo) Expire(context.Context, int) (int, error) { return 0, nil }

func TestBookRejectsInvalidRequests(t *testing.T) {
	cases := map[string]Request{
		"no event":  {SeatID: 1, UserID: 1},
		"no seat":   {EventID: 1, UserID: 1},
		"no user":   {EventID: 1, SeatID: 1},
		"negative":  {EventID: 1, SeatID: -1, UserID: 1},
		"long key":  {EventID: 1, SeatID: 1, UserID: 1, IdempotencyKey: string(make([]byte, 256))},
		"all zeros": {},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &stubRepo{}
			service := NewService(repo, time.Minute)

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
	result, err := service.Book(context.Background(), req)
	if err != nil {
		t.Fatalf("book: %v", err)
	}

	if repo.got != req {
		t.Errorf("repository got %+v, want %+v", repo.got, req)
	}
	if repo.ttl != 90*time.Second {
		t.Errorf("ttl = %s, want 90s", repo.ttl)
	}
	if result.Replayed {
		t.Error("a fresh booking was reported as a replay")
	}
	if result.Booking.Status != StatusHeld {
		t.Errorf("status = %q, want %q", result.Booking.Status, StatusHeld)
	}
}

func TestConfirmAndCancelRejectInvalidIdentity(t *testing.T) {
	cases := map[string]struct{ id, userID int64 }{
		"no booking": {0, 1},
		"no user":    {1, 0},
		"negative":   {-1, -1},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &stubRepo{}
			service := NewService(repo, time.Minute)

			if _, err := service.Confirm(context.Background(), c.id, c.userID); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("confirm: %v, want %v", err, ErrInvalidRequest)
			}
			if err := service.Cancel(context.Background(), c.id, c.userID); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("cancel: %v, want %v", err, ErrInvalidRequest)
			}
			if repo.touched.Load() {
				t.Error("invalid request reached the repository")
			}
		})
	}
}

func TestSeatsRejectsInvalidEvent(t *testing.T) {
	service := NewService(&stubRepo{}, time.Minute)

	if _, err := service.Seats(context.Background(), 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidRequest)
	}
}

type countingExpiry struct {
	stubRepo
	remaining int
	calls     atomic.Int64
	limits    []int
}

func (c *countingExpiry) Expire(_ context.Context, limit int) (int, error) {
	c.calls.Add(1)
	c.limits = append(c.limits, limit)

	released := min(c.remaining, limit)
	c.remaining -= released
	return released, nil
}

// A full batch means there is probably more waiting, so one tick drains it.
func TestExpirerDrainsTheBacklogInOneTick(t *testing.T) {
	repo := &countingExpiry{remaining: 25}
	expirer := NewExpirer(repo, time.Hour, 10, slog.New(slog.DiscardHandler))

	expirer.sweep(context.Background())

	if got := repo.calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (10, 10, then 5 and stop)", got)
	}
	if repo.remaining != 0 {
		t.Errorf("remaining = %d, want 0", repo.remaining)
	}
	for _, limit := range repo.limits {
		if limit != 10 {
			t.Errorf("batch = %d, want 10", limit)
		}
	}
}

func TestExpirerStopsWhenAskedTo(t *testing.T) {
	repo := &countingExpiry{}
	expirer := NewExpirer(repo, time.Millisecond, 10, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		expirer.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the expirer ignored a cancelled context")
	}
}
