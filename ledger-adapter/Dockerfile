# Stage 1: Build the Go application
FROM golang:1.24.9-alpine AS builder

WORKDIR /app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN go build -ldflags="-s -w" -o main ./cmd/adapter/main.go

# Stage 2: Create the final, smaller image
FROM alpine:3.19
RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /app/main .
EXPOSE 8085

CMD ["./main"]