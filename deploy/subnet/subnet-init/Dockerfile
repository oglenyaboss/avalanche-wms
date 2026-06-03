# Одноразовый контейнер: поднимает цепочку WMS Subnet-EVM на subnet-node1 и пишет
# динамический RPC URL в shared-том. См. main.go.
# Нужен CGO (avalanchego тянет BLS-библиотеку blst), поэтому собираем с C-тулчейном и
# запускаем на glibc-базе.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -o /subnet-init .

FROM gcr.io/distroless/base-debian12
COPY --from=build /subnet-init /usr/local/bin/subnet-init
ENTRYPOINT ["/usr/local/bin/subnet-init"]
