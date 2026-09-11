# копирую с докеробраза голанг и помечаю его как билдер
FROM golang:1.25-alpine AS builder

# создаю рабочую папку для первого контейнера
WORKDIR /app

# копирую свои го мод и го сум. из дериктории где будет запускаться докерфайл
COPY go.mod go.sum ./

# запускаю команду чтобы копирнуть все зависимости
RUN go mod download

COPY . .

RUN go build -o app ./cmd/highload/main.go

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/app .

EXPOSE 8080

CMD [ "./app" ]