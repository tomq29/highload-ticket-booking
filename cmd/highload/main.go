package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	ticketsHandler "highload/internal/tickets/handler"
	ticketsRepo "highload/internal/tickets/repo"
	ticketsService "highload/internal/tickets/service"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var (
	//go:embed migrations/*.sql
	embedMigrations embed.FS
)

// "postgres://test:test@localhost:5432/highload-tickets?sslmode=disable"
func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://test:test@localhost:5432/highload-tickets?sslmode=disable"
	}
	migration(url)
	conn := dbPool(url)
	defer conn.Close()

	ticketsR := ticketsRepo.NewTicketRepo(conn)
	ticketsS := ticketsService.NewTicketService(ticketsR)
	ticketsH := ticketsHandler.NewTicketsHandler(ticketsS)

	r := http.NewServeMux()

	r.HandleFunc("POST /book", ticketsH.PostBook)

	addr := ":8080"
	fmt.Println("server started at port ", addr)
	http.ListenAndServe(addr, r)

	// conn.Exec(context.Background(), "insert into events(name, data) values ('Kisssonik Debute',now())")
	// conn.Exec(context.Background(), "insert into seats(event_id, status) values (1, 'available')")
	// conn.Exec(context.Background(), "insert into users(name) values ('tom')")

	// ticketRepo := repo.NewTicketRepo(conn)

	// ticket := tickets.Ticket{
	// 	EventID: 1,
	// 	SeatID:  1,
	// 	UserID: 1,
	// }

	// err = ticketRepo.Create(context.Background(), ticket)
	// if err != nil {
	// 	log.Fatalln("ticketRepo.Create", err)
	// }

}

func migration(url string) {
	pool, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatalln("unable to use data source name", err)
	}

	if err := pool.Ping(); err != nil {
		log.Fatalln("unable to connect to database", err)
	}

	goose.SetBaseFS(embedMigrations)

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalln("Unable SetDialect to postgres", err)
	}

	if err := goose.Up(pool, "migrations"); err != nil {
		log.Fatalln("Unable SetDialect to postgres", err)

	}

	pool.Close()
}

func dbPool(url string) *pgxpool.Pool {
	conn, err := pgxpool.New(context.Background(), url)
	if err != nil {
		log.Fatalln("Unable to connect to database", err)
	}

	return conn
}
