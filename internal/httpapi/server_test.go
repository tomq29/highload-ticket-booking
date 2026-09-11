package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

type fakeService struct {
	held  booking.Booking
	seats []booking.Seat
	err   error
	got   booking.Request
}

func (f *fakeService) Book(_ context.Context, req booking.Request) (booking.Booking, error) {
	f.got = req
	return f.held, f.err
}

func (f *fakeService) Seats(context.Context, int64) ([]booking.Seat, error) {
	return f.seats, f.err
}

func newTestServer(service Service) http.Handler {
	discard := slog.New(slog.DiscardHandler)
	return New(service, func(context.Context) error { return nil }, discard)
}

func post(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestBookReturnsTheHold(t *testing.T) {
	expires := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	service := &fakeService{held: booking.Booking{
		ID: 42, EventID: 1, SeatID: 2, UserID: 3,
		Status: booking.StatusHeld, ExpiresAt: expires,
	}}

	rec := post(t, newTestServer(service), `{"event_id":1,"seat_id":2,"user_id":3}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusCreated, rec.Body)
	}
	if service.got != (booking.Request{EventID: 1, SeatID: 2, UserID: 3}) {
		t.Errorf("service got %+v", service.got)
	}

	var got bookingResponse
	decodeBody(t, rec.Body, &got)
	if got.ID != 42 || got.SeatID != 2 || got.Status != string(booking.StatusHeld) {
		t.Errorf("body = %+v", got)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("expires_at = %s, want %s", got.ExpiresAt, expires)
	}
}

func TestBookMapsDomainErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{booking.ErrSeatTaken, http.StatusConflict, "seat_taken"},
		{booking.ErrSeatNotFound, http.StatusNotFound, "seat_not_found"},
		{booking.ErrUserNotFound, http.StatusNotFound, "user_not_found"},
		{booking.ErrInvalidRequest, http.StatusBadRequest, "invalid_request"},
	}

	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			rec := post(t, newTestServer(&fakeService{err: c.err}), `{"event_id":1,"seat_id":2,"user_id":3}`)

			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			var got errorResponse
			decodeBody(t, rec.Body, &got)
			if got.Error != c.code {
				t.Errorf("error = %q, want %q", got.Error, c.code)
			}
		})
	}
}

func TestBookHidesInternalErrors(t *testing.T) {
	service := &fakeService{err: errors.New("pq: password authentication failed for user booking")}

	rec := post(t, newTestServer(service), `{"event_id":1,"seat_id":2,"user_id":3}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("body leaks the underlying error: %s", rec.Body)
	}
}

func TestBookRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"malformed":     `{"event_id":`,
		"unknown field": `{"event_id":1,"seat_id":2,"user_id":3,"price":100}`,
		"two objects":   `{"event_id":1,"seat_id":2,"user_id":3}{"event_id":2}`,
		"wrong type":    `{"event_id":"one","seat_id":2,"user_id":3}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := post(t, newTestServer(&fakeService{}), body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			var got errorResponse
			decodeBody(t, rec.Body, &got)
			if got.Error != "invalid_request" {
				t.Errorf("error = %q, want invalid_request", got.Error)
			}
		})
	}
}

func TestSeatsListsTheEvent(t *testing.T) {
	service := &fakeService{seats: []booking.Seat{
		{ID: 1, EventID: 1, Row: "A", Number: 1, Status: booking.SeatAvailable},
		{ID: 2, EventID: 1, Row: "A", Number: 2, Status: booking.SeatHeld},
	}}

	rec := httptest.NewRecorder()
	newTestServer(service).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events/1/seats", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []seatResponse
	decodeBody(t, rec.Body, &got)
	if len(got) != 2 || got[1].Status != string(booking.SeatHeld) {
		t.Errorf("body = %+v", got)
	}
}

func TestSeatsRejectsNonNumericEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(&fakeService{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events/abc/seats", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSeatsReportsUnknownEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	server := newTestServer(&fakeService{err: booking.ErrEventNotFound})
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events/7/seats", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHealthEndpoints(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	failing := New(&fakeService{}, func(context.Context) error { return errors.New("down") }, discard)

	rec := httptest.NewRecorder()
	newTestServer(&fakeService{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want %d", rec.Code, http.StatusOK)
	}

	rec = httptest.NewRecorder()
	failing.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz with a dead database = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestPanicInHandlerBecomesInternalError(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })

	rec := httptest.NewRecorder()
	withLogging(discard, panicking).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func decodeBody(t *testing.T, body io.Reader, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
