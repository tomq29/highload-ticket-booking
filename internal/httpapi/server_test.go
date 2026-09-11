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
	result    booking.Result
	confirmed booking.Booking
	seats     []booking.Seat
	err       error
	got       booking.Request
	cancelled struct{ id, userID int64 }
}

func (f *fakeService) Book(_ context.Context, req booking.Request) (booking.Result, error) {
	f.got = req
	return f.result, f.err
}

func (f *fakeService) Confirm(context.Context, int64, int64) (booking.Booking, error) {
	return f.confirmed, f.err
}

func (f *fakeService) Cancel(_ context.Context, id, userID int64) error {
	f.cancelled.id, f.cancelled.userID = id, userID
	return f.err
}

func (f *fakeService) Seats(context.Context, int64) ([]booking.Seat, error) {
	return f.seats, f.err
}

type countingMetrics struct {
	outcomes []string
	routes   []string
}

func (m *countingMetrics) Observe(route string, _ int, _ time.Duration) {
	m.routes = append(m.routes, route)
}
func (m *countingMetrics) Outcome(outcome string) { m.outcomes = append(m.outcomes, outcome) }
func (m *countingMetrics) Started()               {}
func (m *countingMetrics) Done()                  {}

func newTestServer(service Service) http.Handler {
	return newTestServerWith(service, &countingMetrics{}, func(context.Context) error { return nil })
}

func newTestServerWith(service Service, recorder Recorder, ready func(context.Context) error) http.Handler {
	return New(Config{
		Service: service,
		Ready:   ready,
		Metrics: recorder,
		Logger:  slog.New(slog.DiscardHandler),
	})
}

func post(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("X-User-Id", "3")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestBookReturnsTheHold(t *testing.T) {
	expires := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	service := &fakeService{result: booking.Result{Booking: booking.Booking{
		ID: 42, EventID: 1, SeatID: 2, UserID: 3,
		Status: booking.StatusHeld, ExpiresAt: expires,
	}}}

	rec := post(t, newTestServer(service), `{"event_id":1,"seat_id":2}`)

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

func TestBookPassesTheIdempotencyKey(t *testing.T) {
	service := &fakeService{result: booking.Result{
		Booking:  booking.Booking{ID: 42, Status: booking.StatusHeld},
		Replayed: true,
	}}

	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(`{"event_id":1,"seat_id":2}`))
	req.Header.Set("X-User-Id", "3")
	req.Header.Set("Idempotency-Key", "checkout-1")

	rec := httptest.NewRecorder()
	newTestServer(service).ServeHTTP(rec, req)

	if service.got.IdempotencyKey != "checkout-1" {
		t.Errorf("key = %q, want checkout-1", service.got.IdempotencyKey)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d: a replay is not a new booking", rec.Code, http.StatusOK)
	}
}

func TestBookRequiresTheCaller(t *testing.T) {
	cases := map[string]string{"missing": "", "not a number": "abc"}

	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(`{"event_id":1,"seat_id":2}`))
			if header != "" {
				req.Header.Set("X-User-Id", header)
			}

			rec := httptest.NewRecorder()
			newTestServer(&fakeService{}).ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
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
			rec := post(t, newTestServer(&fakeService{err: c.err}), `{"event_id":1,"seat_id":2}`)

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

	rec := post(t, newTestServer(service), `{"event_id":1,"seat_id":2}`)

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
		"unknown field": `{"event_id":1,"seat_id":2,"price":100}`,
		"two objects":   `{"event_id":1,"seat_id":2}{"event_id":2}`,
		"wrong type":    `{"event_id":"one","seat_id":2}`,
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

func TestConfirmReturnsTheBooking(t *testing.T) {
	service := &fakeService{confirmed: booking.Booking{ID: 7, Status: booking.StatusConfirmed}}

	req := httptest.NewRequest(http.MethodPost, "/v1/bookings/7/confirm", nil)
	req.Header.Set("X-User-Id", "3")

	rec := httptest.NewRecorder()
	newTestServer(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body)
	}
	var got bookingResponse
	decodeBody(t, rec.Body, &got)
	if got.Status != string(booking.StatusConfirmed) {
		t.Errorf("status = %q, want %q", got.Status, booking.StatusConfirmed)
	}
}

func TestConfirmReportsAStaleHold(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings/7/confirm", nil)
	req.Header.Set("X-User-Id", "3")

	rec := httptest.NewRecorder()
	newTestServer(&fakeService{err: booking.ErrBookingNotHeld}).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var got errorResponse
	decodeBody(t, rec.Body, &got)
	if got.Error != "booking_not_held" {
		t.Errorf("error = %q, want booking_not_held", got.Error)
	}
}

func TestCancelAnswersWithoutABody(t *testing.T) {
	service := &fakeService{}

	req := httptest.NewRequest(http.MethodDelete, "/v1/bookings/7", nil)
	req.Header.Set("X-User-Id", "3")

	rec := httptest.NewRecorder()
	newTestServer(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusNoContent, rec.Body)
	}
	if service.cancelled.id != 7 || service.cancelled.userID != 3 {
		t.Errorf("cancelled %+v, want {7 3}", service.cancelled)
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
	failing := newTestServerWith(&fakeService{}, &countingMetrics{},
		func(context.Context) error { return errors.New("down") })

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
	server := &Server{metrics: &countingMetrics{}, log: slog.New(slog.DiscardHandler)}
	panicking := server.wrap("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	rec := httptest.NewRecorder()
	panicking.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestBookingOutcomesAreCounted(t *testing.T) {
	cases := map[string]struct {
		service *fakeService
		body    string
		want    string
	}{
		"won":      {&fakeService{}, `{"event_id":1,"seat_id":2}`, "won"},
		"conflict": {&fakeService{err: booking.ErrSeatTaken}, `{"event_id":1,"seat_id":2}`, "conflict"},
		"rejected": {&fakeService{}, `{"bad":`, "rejected"},
		"error":    {&fakeService{err: errors.New("boom")}, `{"event_id":1,"seat_id":2}`, "error"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := &countingMetrics{}
			server := newTestServerWith(c.service, recorder, func(context.Context) error { return nil })

			post(t, server, c.body)

			if len(recorder.outcomes) != 1 || recorder.outcomes[0] != c.want {
				t.Fatalf("outcomes = %v, want [%s]", recorder.outcomes, c.want)
			}
			if len(recorder.routes) != 1 || recorder.routes[0] != "POST /v1/bookings" {
				t.Errorf("routes = %v", recorder.routes)
			}
		})
	}
}

func decodeBody(t *testing.T, body io.Reader, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
