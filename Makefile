.PHONY: up down logs test lint build run seed-load load load-hot load-spread

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

seed-load:
	docker compose exec -T db psql -q -U booking -d booking < loadtest/setup.sql

load-hot: seed-load
	docker compose run --rm k6 run /scripts/hot_seat.js

load-spread: seed-load
	docker compose run --rm k6 run /scripts/spread.js

load:
	./loadtest/bench.sh
