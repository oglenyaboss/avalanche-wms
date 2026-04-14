# syntax=docker/dockerfile:1.7
# Stage 1: build static binary (CGO off → works with distroless/static).
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 + static linking → полностью автономный бинарь без libc/SSL.
# ldflags -s -w убирает debug symbols (~30% меньше размер).
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/adapter

# Stage 2: distroless runtime, non-root uid, без shell/package manager.
# Плюсы: минимальная attack surface, нельзя exec'ить curl/wget/sh.
# Минус: нет curl для docker healthcheck → используем /bin/grpc_health_probe
# или healthcheck в compose на wget из основного процесса. У нас compose
# healthcheck использует curl из wms_app контейнера; для самого adapter
# healthcheck вынесен на TCP-level (docker-compose test на `/health` выполняет
# wms_app). Если нужен локальный check — `docker inspect --health`.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /server /server

EXPOSE 8085
USER nonroot:nonroot

ENTRYPOINT ["/server"]
