package tickets

import "errors"

type Ticket struct {
	ID      int64
	EventID int64
	SeatID  int64
	UserID  int64
	Status  TicketStatus
}

type TicketStatus string

const (
	TicketStatusProcessing TicketStatus = "processing"
	TicketStatusSuccess    TicketStatus = "success"
	TicketStatusFailed     TicketStatus = "failed"
)

type BookRequest struct {
	EventID int64 `json:"eventID"`
	SeatID  int64 `json:"seatID"`
	UserID  int64 `json:"userID"`
}

var (
	ErrNotFound         = errors.New("not found error")  //ErrNotFound means that object not found
	ErrInternal         = errors.New("internal error")   //ErrInternal means that something goes wrong
	ErrValidation       = errors.New("validation error") //ErrValidation means that object is invalid
	ErrNotAvailable     = errors.New("seat is not available")
	ErrCantBookSeat     = errors.New("cannot book the seat")
	ErrProccesingTicket = errors.New("cannot processing ticket")
)
