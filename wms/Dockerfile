# Стадия 1: Сборка
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /build/wms-service ./cmd/wms/main.go

# Стадия 2: Финальный образ
FROM alpine:latest

WORKDIR /app
RUN apk add --no-cache curl

COPY --from=builder /build/wms-service .

CMD ["./wms-service"]
