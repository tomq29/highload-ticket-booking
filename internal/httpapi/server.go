package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

type Service interface {
	Book(ctx context.Context, req booking.Request) (booking.Result, error)
	Confirm(ctx context.Context, id, userID int64) (booking.Booking, error)
	Cancel(ctx context.Context, id, userID int64) error
	Seats(ctx context.Context, eventID int64) ([]booking.Seat, error)
}

type Recorder interface {
	Observe(route string, status int, took time.Duration)
	Outcome(outcome string)
	Started()
	Done()
}

type Config struct {
	Service Service
	Ready   func(context.Context) error
	Metrics Recorder
	Scrape  http.Handler
	Logger  *slog.Logger
}

type Server struct {
	service Service
	ready   func(context.Context) error
	metrics Recorder
	log     *slog.Logger
}

func New(cfg Config) http.Handler {
	s := &Server{
		service: cfg.Service,
		ready:   cfg.Ready,
		metrics: cfg.Metrics,
		log:     cfg.Logger,
	}

	mux := http.NewServeMux()
	mux.Handle("POST /v1/bookings", s.wrap("POST /v1/bookings", s.book))
	mux.Handle("POST /v1/bookings/{id}/confirm", s.wrap("POST /v1/bookings/{id}/confirm", s.confirm))
	mux.Handle("DELETE /v1/bookings/{id}", s.wrap("DELETE /v1/bookings/{id}", s.cancel))
	mux.Handle("GET /v1/events/{id}/seats", s.wrap("GET /v1/events/{id}/seats", s.seats))
	mux.Handle("GET /healthz", s.wrap("GET /healthz", s.healthz))
	mux.Handle("GET /readyz", s.wrap("GET /readyz", s.readyz))

	if cfg.Scrape != nil {
		mux.Handle("GET /metrics", cfg.Scrape)
	}
	return mux
}

type bookRequest struct {
	EventID int64 `json:"event_id"`
	SeatID  int64 `json:"seat_id"`
}

type bookingResponse struct {
	ID        int64     `json:"id"`
	EventID   int64     `json:"event_id"`
	SeatID    int64     `json:"seat_id"`
	UserID    int64     `json:"user_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type seatResponse struct {
	ID     int64  `json:"id"`
	Row    string `json:"row"`
	Number int    `json:"number"`
	Status string `json:"status"`
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	userID, err := caller(r)
	if err != nil {
		s.metrics.Outcome("rejected")
		s.fail(w, r, err)
		return
	}

	var req bookRequest
	if err := decode(w, r, &req); err != nil {
		s.metrics.Outcome("rejected")
		s.fail(w, r, err)
		return
	}

	result, err := s.service.Book(r.Context(), booking.Request{
		EventID:        req.EventID,
		SeatID:         req.SeatID,
		UserID:         userID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	s.metrics.Outcome(outcomeFor(err))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// A replay is not a new booking, so it does not answer 201.
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	respond(w, status, newBookingResponse(result.Booking))
}

func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	id, userID, err := bookingOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	confirmed, err := s.service.Confirm(r.Context(), id, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	respond(w, http.StatusOK, newBookingResponse(confirmed))
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id, userID, err := bookingOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if err := s.service.Cancel(r.Context(), id, userID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) seats(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, invalid("event id must be a number"))
		return
	}

	seats, err := s.service.Seats(r.Context(), eventID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]seatResponse, 0, len(seats))
	for _, seat := range seats {
		out = append(out, seatResponse{
			ID:     seat.ID,
			Row:    seat.Row,
			Number: seat.Number,
			Status: string(seat.Status),
		})
	}
	respond(w, http.StatusOK, out)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(r.Context()); err != nil {
		respond(w, http.StatusServiceUnavailable, errorResponse{
			Error:   "not_ready",
			Message: "database is unavailable",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
}

// caller stands in for authentication: the id arrives where a token subject
// would, and every query is scoped by it, so no caller can touch a booking
// that is not theirs.
func caller(r *http.Request) (int64, error) {
	raw := r.Header.Get("X-User-Id")
	if raw == "" {
		return 0, invalid("X-User-Id header is required")
	}

	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, invalid("X-User-Id must be a number")
	}
	return userID, nil
}

func bookingOf(r *http.Request) (id, userID int64, err error) {
	if id, err = strconv.ParseInt(r.PathValue("id"), 10, 64); err != nil {
		return 0, 0, invalid("booking id must be a number")
	}
	if userID, err = caller(r); err != nil {
		return 0, 0, err
	}
	return id, userID, nil
}

func newBookingResponse(b booking.Booking) bookingResponse {
	return bookingResponse{
		ID:        b.ID,
		EventID:   b.EventID,
		SeatID:    b.SeatID,
		UserID:    b.UserID,
		Status:    string(b.Status),
		ExpiresAt: b.ExpiresAt,
		CreatedAt: b.CreatedAt,
	}
}
