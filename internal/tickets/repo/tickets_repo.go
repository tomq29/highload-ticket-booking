package repo

import (
	"context"
	"highload/internal/seats"
	"highload/internal/tickets"

	"github.com/jackc/pgx/v5/pgxpool"
)

type TicketRepo struct {
	DB *pgxpool.Pool
}

func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo {
	return &TicketRepo{
		DB: pool,
	}
}

//Логика: Проверяет, свободно ли место. Если да — бронирует (меняет статус) и создает запись в tickets.

func (r *TicketRepo) Create(ctx context.Context, bookRequest *tickets.BookRequest) error {

	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	seatStatus := ""
	err = tx.QueryRow(ctx, "select status from seats where id = $1 for update", bookRequest.SeatID).Scan(&seatStatus)
	if err != nil {
		return err
	}

	if seatStatus != string(seats.SeatAvailabe) {
		return tickets.ErrNotAvailable
	}

	tag, err := tx.Exec(ctx, "update seats set status = $1 where id = $2", seats.SeatBooked, bookRequest.SeatID)
	if err != nil {
		return err
	}
	if rows := tag.RowsAffected(); rows == 0 {
		return tickets.ErrCantBookSeat
	}

	tag, err = tx.Exec(ctx,
		"insert into tickets(status, event_id, seats_id, user_id) values ($1, $2, $3, $4)",
		tickets.TicketStatusProcessing, bookRequest.EventID, bookRequest.SeatID, bookRequest.UserID)

	if err != nil {
		return err
	}
	if rows := tag.RowsAffected(); rows == 0 {
		return tickets.ErrProccesingTicket
	}

	tx.Commit(ctx)

	return nil
}
