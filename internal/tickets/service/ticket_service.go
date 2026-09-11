package service

import (
	"context"
	"highload/internal/tickets"
)

type repo interface {
	Create(ctx context.Context, ticket *tickets.BookRequest) error
}

type TicketService struct {
	Repo repo
}

func NewTicketService(r repo) *TicketService {
	return &TicketService{
		Repo: r,
	}
}

func (s *TicketService) BookTicket(ctx context.Context, BookRequest *tickets.BookRequest) error {
	return s.Repo.Create(ctx, BookRequest)
}
