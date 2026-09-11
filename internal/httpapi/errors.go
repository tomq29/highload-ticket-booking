package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
)

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{booking.ErrInvalidRequest}, args...)...)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	// The caller hung up: nobody is left to read a status code.
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}

	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, booking.ErrInvalidRequest):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, booking.ErrSeatTaken):
		status, code = http.StatusConflict, "seat_taken"
	case errors.Is(err, booking.ErrSeatNotFound):
		status, code = http.StatusNotFound, "seat_not_found"
	case errors.Is(err, booking.ErrEventNotFound):
		status, code = http.StatusNotFound, "event_not_found"
	case errors.Is(err, booking.ErrUserNotFound):
		status, code = http.StatusNotFound, "user_not_found"
	case errors.Is(err, booking.ErrBookingNotFound):
		status, code = http.StatusNotFound, "booking_not_found"
	}

	message := err.Error()
	if status == http.StatusInternalServerError {
		s.log.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
		message = "internal error"
	}

	respond(w, status, errorResponse{Error: code, Message: message})
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("%s", err)
	}
	if dec.More() {
		return invalid("body must hold a single JSON object")
	}
	return nil
}
