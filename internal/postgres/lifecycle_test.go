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

func TestConfirmSellsTheSeat(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()
		held := hold(t, repo, 10, 1, ttl)

		confirmed, err := repo.Confirm(ctx, held.ID, 1)
		if err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if confirmed.Status != booking.StatusConfirmed {
			t.Errorf("status = %q, want %q", confirmed.Status, booking.StatusConfirmed)
		}
		if got := seatStatus(t, 10); got != booking.SeatSold {
			t.Errorf("seat status = %q, want %q", got, booking.SeatSold)
		}

		if _, err := repo.Confirm(ctx, held.ID, 1); !errors.Is(err, booking.ErrBookingNotHeld) {
			t.Errorf("second confirm: %v, want %v", err, booking.ErrBookingNotHeld)
		}
	})
}

func TestConfirmRefusesSomeoneElsesBooking(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		held := hold(t, repo, 11, 1, ttl)

		_, err := repo.Confirm(context.Background(), held.ID, 2)
		if !errors.Is(err, booking.ErrBookingNotFound) {
			t.Fatalf("confirm: %v, want %v", err, booking.ErrBookingNotFound)
		}
		if got := seatStatus(t, 11); got != booking.SeatHeld {
			t.Errorf("seat status = %q, want %q", got, booking.SeatHeld)
		}
	})
}

func TestConfirmRefusesAnExpiredHold(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		held := hold(t, repo, 12, 1, -time.Minute)

		_, err := repo.Confirm(context.Background(), held.ID, 1)
		if !errors.Is(err, booking.ErrBookingNotHeld) {
			t.Fatalf("confirm: %v, want %v", err, booking.ErrBookingNotHeld)
		}
	})
}

func TestCancelFreesTheSeat(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()
		held := hold(t, repo, 13, 1, ttl)

		if err := repo.Cancel(ctx, held.ID, 1); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if got := seatStatus(t, 13); got != booking.SeatAvailable {
			t.Errorf("seat status = %q, want %q", got, booking.SeatAvailable)
		}
		if err := repo.Cancel(ctx, held.ID, 1); !errors.Is(err, booking.ErrBookingNotHeld) {
			t.Errorf("second cancel: %v, want %v", err, booking.ErrBookingNotHeld)
		}

		// And the freed seat can be booked again.
		if _, err := repo.Hold(ctx, booking.Request{EventID: 1, SeatID: 13, UserID: 2}, ttl); err != nil {
			t.Errorf("rebooking a cancelled seat: %v", err)
		}
	})
}

func TestCancelRefusesAnUnknownBooking(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		if err := repo.Cancel(context.Background(), 999_999, 1); !errors.Is(err, booking.ErrBookingNotFound) {
			t.Fatalf("cancel: %v, want %v", err, booking.ErrBookingNotFound)
		}
	})
}

func TestExpireReleasesOnlyOverdueHolds(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()
		overdue := hold(t, repo, 20, 1, -time.Minute)
		live := hold(t, repo, 21, 2, ttl)

		released, err := repo.Expire(ctx, 100)
		if err != nil {
			t.Fatalf("expire: %v", err)
		}
		if released != 1 {
			t.Fatalf("released = %d, want 1", released)
		}

		if got := seatStatus(t, 20); got != booking.SeatAvailable {
			t.Errorf("overdue seat = %q, want %q", got, booking.SeatAvailable)
		}
		if got := seatStatus(t, 21); got != booking.SeatHeld {
			t.Errorf("live seat = %q, want %q", got, booking.SeatHeld)
		}
		if got := bookingStatus(t, overdue.ID); got != booking.StatusExpired {
			t.Errorf("overdue booking = %q, want %q", got, booking.StatusExpired)
		}
		if got := bookingStatus(t, live.ID); got != booking.StatusHeld {
			t.Errorf("live booking = %q, want %q", got, booking.StatusHeld)
		}

		if again, err := repo.Expire(ctx, 100); err != nil || again != 0 {
			t.Errorf("second sweep released %d (err %v), want 0", again, err)
		}
	})
}

func TestExpireHonoursTheBatchSize(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		for seatID := int64(30); seatID < 35; seatID++ {
			hold(t, repo, seatID, seatID, -time.Minute)
		}

		released, err := repo.Expire(context.Background(), 2)
		if err != nil {
			t.Fatalf("expire: %v", err)
		}
		if released != 2 {
			t.Fatalf("released = %d, want 2", released)
		}
	})
}

func TestIdempotencyKeyReplaysTheSameBooking(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()
		req := booking.Request{EventID: 1, SeatID: 40, UserID: 1, IdempotencyKey: "checkout-1"}

		first, err := repo.Hold(ctx, req, ttl)
		if err != nil {
			t.Fatalf("first hold: %v", err)
		}
		if first.Replayed {
			t.Error("the first call was reported as a replay")
		}

		second, err := repo.Hold(ctx, req, ttl)
		if err != nil {
			t.Fatalf("second hold: %v", err)
		}
		if !second.Replayed {
			t.Error("the repeat was not reported as a replay")
		}
		if second.Booking.ID != first.Booking.ID {
			t.Errorf("booking id = %d, want %d", second.Booking.ID, first.Booking.ID)
		}
		if count := bookingsFor(t, "checkout-1"); count != 1 {
			t.Errorf("rows for the key = %d, want 1", count)
		}
	})
}

func TestIdempotencyKeyIsPerUser(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		ctx := context.Background()

		if _, err := repo.Hold(ctx, booking.Request{
			EventID: 1, SeatID: 41, UserID: 1, IdempotencyKey: "shared",
		}, ttl); err != nil {
			t.Fatalf("first hold: %v", err)
		}

		// Same key, different user: a different booking, not a replay.
		second, err := repo.Hold(ctx, booking.Request{
			EventID: 1, SeatID: 42, UserID: 2, IdempotencyKey: "shared",
		}, ttl)
		if err != nil {
			t.Fatalf("second hold: %v", err)
		}
		if second.Replayed {
			t.Error("another user's key was treated as a replay")
		}
	})
}

// Two copies of one request arriving together must still buy one seat once.
func TestConcurrentRequestsWithOneKeyBookOnce(t *testing.T) {
	eachStrategy(t, func(t *testing.T, repo *Repository) {
		const attempts = 20
		req := booking.Request{EventID: 1, SeatID: 43, UserID: 1, IdempotencyKey: "retry-storm"}

		var (
			ids     sync.Map
			replays atomic.Int64
			release sync.WaitGroup
			done    sync.WaitGroup
		)
		release.Add(1)

		for range attempts {
			done.Add(1)
			go func() {
				defer done.Done()
				release.Wait()

				result, err := repo.Hold(context.Background(), req, ttl)
				if err != nil {
					t.Errorf("hold: %v", err)
					return
				}
				if result.Replayed {
					replays.Add(1)
				}
				ids.Store(result.Booking.ID, struct{}{})
			}()
		}

		release.Done()
		done.Wait()

		distinct := 0
		ids.Range(func(any, any) bool { distinct++; return true })
		if distinct != 1 {
			t.Errorf("distinct bookings = %d, want 1", distinct)
		}
		if replays.Load() != attempts-1 {
			t.Errorf("replays = %d, want %d", replays.Load(), attempts-1)
		}
		if count := bookingsFor(t, "retry-storm"); count != 1 {
			t.Errorf("rows for the key = %d, want 1", count)
		}
	})
}

func hold(t *testing.T, repo *Repository, seatID, userID int64, ttl time.Duration) booking.Booking {
	t.Helper()

	result, err := repo.Hold(context.Background(),
		booking.Request{EventID: 1, SeatID: seatID, UserID: userID}, ttl)
	if err != nil {
		t.Fatalf("hold seat %d: %v", seatID, err)
	}
	return result.Booking
}

func bookingStatus(t *testing.T, id int64) booking.Status {
	t.Helper()

	var status booking.Status
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM bookings WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read booking: %v", err)
	}
	return status
}

func bookingsFor(t *testing.T, key string) int {
	t.Helper()

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings WHERE idempotency_key = $1`, key).Scan(&count); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	return count
}
