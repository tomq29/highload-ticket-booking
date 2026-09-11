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
	Book(ctx context.Context, req booking.Request) (booking.Booking, error)
	Seats(ctx context.Context, eventID int64) ([]booking.Seat, error)
}

type Server struct {
	service Service
	ready   func(context.Context) error
	log     *slog.Logger
}

func New(service Service, ready func(context.Context) error, log *slog.Logger) http.Handler {
	s := &Server{service: service, ready: ready, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/bookings", s.book)
	mux.HandleFunc("GET /v1/events/{id}/seats", s.seats)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)

	return withLogging(log, mux)
}

type bookRequest struct {
	EventID int64 `json:"event_id"`
	SeatID  int64 `json:"seat_id"`
	UserID  int64 `json:"user_id"`
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
	var req bookRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, r, err)
		return
	}

	held, err := s.service.Book(r.Context(), booking.Request{
		EventID: req.EventID,
		SeatID:  req.SeatID,
		UserID:  req.UserID,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	respond(w, http.StatusCreated, bookingResponse{
		ID:        held.ID,
		EventID:   held.EventID,
		SeatID:    held.SeatID,
		UserID:    held.UserID,
		Status:    string(held.Status),
		ExpiresAt: held.ExpiresAt,
		CreatedAt: held.CreatedAt,
	})
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
