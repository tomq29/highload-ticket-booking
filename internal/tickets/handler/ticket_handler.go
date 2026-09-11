package handler

import (
	"context"
	"encoding/json"
	"errors"
	"highload/internal/tickets"
	"net/http"
)

type service interface {
	BookTicket(ctx context.Context, BookRequest *tickets.BookRequest) error
}

type TicketHandler struct {
	Service service
}

func NewTicketsHandler(s service) *TicketHandler {
	return &TicketHandler{
		Service: s,
	}
}

func (h *TicketHandler) PostBook(w http.ResponseWriter, r *http.Request) {
	bookRequest := new(tickets.BookRequest)

	err := json.NewDecoder(r.Body).Decode(bookRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if bookRequest.EventID <= 0 || bookRequest.SeatID <= 0 || bookRequest.UserID <= 0 {
		http.Error(w, "not valid data", http.StatusBadRequest)
		return
	}

	err = h.Service.BookTicket(r.Context(), bookRequest)
	if err == nil {
		w.WriteHeader(http.StatusCreated)
		return
	}

	switch {
	case errors.Is(err, tickets.ErrNotAvailable):
		http.Error(w, tickets.ErrNotAvailable.Error(), http.StatusConflict)
		return
	default:
		http.Error(w, tickets.ErrInternal.Error(), http.StatusInternalServerError)
	}

}
