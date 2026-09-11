.PHONY: up down logs test lint build run

up:
	docker compose up --build -d
	docker compose ps

down:
	docker compose down -v

logs:
	docker compose logs -f api

build:
	go build ./...

test:
	go test ./... -race -count=1

lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

run:
	DATABASE_URL=postgres://booking:booking@localhost:$${POSTGRES_PORT:-5432}/booking?sslmode=disable go run ./cmd/api
