package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

const ttl = time.Minute

// Every behaviour below is required of every strategy: they are three ways to
// resolve the same race, not three different contracts.
func eachStrategy(t *testing.T, test func(t *testing.T, repo *Repository)) {
	t.Helper()

	for _, strategy := range []Strategy{Pessimistic, Optimistic, Atomic} {
		t.Run(string(strategy), func(t *testing.T) {
			reset(t)

			repo, err := NewRepository(testPool, strategy)
			if err != nil {
				t.Fatalf("new repository: %v", err)
			}
			test(t, repo)
		})
	}
}

func TestUnknownStrategyIsRejected(t *testing.T) {
	if _, err := NewRepository(nil, "hopeful"); err == nil {
		t.Fatal("an unknown strategy was accepted")
	}
}

func TestHoldTakesTheSeat(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		result, err := repo.Hold(context.Background(), booking.Request{EventID: 1, SeatID: 1, UserID: 1}, ttl)
		if err != nil {
			t.Fatalf("hold: %v", err)
		}

		held := result.Booking
		if result.Replayed {
			t.Error("a fresh booking was reported as a replay")
		}
		if held.ID == 0 {
			t.Error("booking id was not returned")
		}
		if held.Status != booking.StatusHeld {
			t.Errorf("status = %q, want %q", held.Status, booking.StatusHeld)
		}
		if until := time.Until(held.ExpiresAt); until <= 0 || until > ttl {
			t.Errorf("expires in %s, want a positive value within %s", until, ttl)
		}
		if got := seatStatus(t, 1); got != booking.SeatHeld {
			t.Errorf("seat status = %q, want %q", got, booking.SeatHeld)
		}
	})
}

func TestHoldRejectsATakenSeat(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()
		if _, err := repo.Hold(ctx, booking.Request{EventID: 1, SeatID: 2, UserID: 1}, ttl); err != nil {
			t.Fatalf("first hold: %v", err)
		}

		_, err := repo.Hold(ctx, booking.Request{EventID: 1, SeatID: 2, UserID: 2}, ttl)
		if !errors.Is(err, booking.ErrSeatTaken) {
			t.Fatalf("second hold: %v, want %v", err, booking.ErrSeatTaken)
		}
	})
}

func TestHoldRejectsSeatFromAnotherEvent(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		_, err := repo.Hold(context.Background(), booking.Request{EventID: 999, SeatID: 1, UserID: 1}, ttl)
		if !errors.Is(err, booking.ErrSeatNotFound) {
			t.Fatalf("hold: %v, want %v", err, booking.ErrSeatNotFound)
		}
	})
}

func TestHoldRejectsUnknownUser(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		_, err := repo.Hold(context.Background(), booking.Request{EventID: 1, SeatID: 3, UserID: 999_999}, ttl)
		if !errors.Is(err, booking.ErrUserNotFound) {
			t.Fatalf("hold: %v, want %v", err, booking.ErrUserNotFound)
		}
		if got := seatStatus(t, 3); got != booking.SeatAvailable {
			t.Errorf("seat status = %q, want %q: the failed insert must roll the seat back", got, booking.SeatAvailable)
		}
	})
}

// The point of the whole service: a hundred requests for one seat, one winner.
func TestConcurrentHoldsSellTheSeatOnce(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		const attempts = 100
		var won, lost atomic.Int64

		var release sync.WaitGroup
		release.Add(1)
		var done sync.WaitGroup

		for i := range attempts {
			done.Add(1)
			go func() {
				defer done.Done()
				release.Wait()

				_, err := repo.Hold(context.Background(),
					booking.Request{EventID: 1, SeatID: 7, UserID: int64(i) + 1}, ttl)
				switch {
				case err == nil:
					won.Add(1)
				case errors.Is(err, booking.ErrSeatTaken):
					lost.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}()
		}

		release.Done()
		done.Wait()

		if won.Load() != 1 {
			t.Errorf("winners = %d, want 1", won.Load())
		}
		if lost.Load() != attempts-1 {
			t.Errorf("conflicts = %d, want %d", lost.Load(), attempts-1)
		}
		if live := liveBookings(t, 7); live != 1 {
			t.Errorf("live bookings for the seat = %d, want 1", live)
		}
	})
}

func TestConcurrentHoldsOnDistinctSeatsAllSucceed(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		const seats = 50
		var won atomic.Int64
		var done sync.WaitGroup

		for i := range seats {
			done.Add(1)
			go func() {
				defer done.Done()

				seatID := int64(i) + 1
				if _, err := repo.Hold(context.Background(),
					booking.Request{EventID: 1, SeatID: seatID, UserID: seatID}, ttl); err != nil {
					t.Errorf("seat %d: %v", seatID, err)
					return
				}
				won.Add(1)
			}()
		}
		done.Wait()

		if won.Load() != seats {
			t.Errorf("bookings = %d, want %d", won.Load(), seats)
		}
	})
}

func TestSeatsListsTheEvent(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		seats, err := repo.Seats(context.Background(), 1)
		if err != nil {
			t.Fatalf("seats: %v", err)
		}
		if len(seats) != 100 {
			t.Fatalf("seats = %d, want 100", len(seats))
		}
		if seats[0].Row != "A" || seats[0].Number != 1 {
			t.Errorf("first seat = %s%d, want A1", seats[0].Row, seats[0].Number)
		}
		if seats[0].Status != booking.SeatAvailable {
			t.Errorf("first seat status = %q, want %q", seats[0].Status, booking.SeatAvailable)
		}
	})
}

func TestSeatsOfUnknownEvent(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		_, err := repo.Seats(context.Background(), 999)
		if !errors.Is(err, booking.ErrEventNotFound) {
			t.Fatalf("seats: %v, want %v", err, booking.ErrEventNotFound)
		}
	})
}

func seatStatus(t *testing.T, seatID int64) booking.SeatStatus {
	t.Helper()

	var status booking.SeatStatus
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM seats WHERE id = $1`, seatID).Scan(&status); err != nil {
		t.Fatalf("read seat: %v", err)
	}
	return status
}

func liveBookings(t *testing.T, seatID int64) int {
	t.Helper()

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings WHERE seat_id = $1 AND status IN ('held', 'confirmed')`,
		seatID).Scan(&count); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	return count
}
