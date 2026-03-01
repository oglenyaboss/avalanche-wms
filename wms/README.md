# WMS Service

Модульный монолит на Go — основной бекенд системы управления складом.

## Архитектура

```
cmd/wms/main.go                 # Точка входа, DI-wiring
internal/
├── config/                     # Загрузка конфигурации из env
├── domain/                     # Доменные модели (общие для всех модулей)
│
├── receiving/                  # Модуль: приёмка товаров
│   ├── handler.go              #   HTTP-обработчики
│   ├── service.go              #   бизнес-логика
│   └── repository.go           #   работа с БД
│
├── assembly/                   # Модуль: сборка заказов
│   ├── handler.go
│   ├── service.go
│   └── repository.go
│
├── putaway/                    # Модуль: раскладка по ячейкам
│   ├── handler.go
│   ├── service.go
│   └── repository.go
│
├── shipping/                   # Модуль: отгрузка
│   ├── handler.go
│   ├── service.go
│   └── repository.go
│
├── ledger/                     # HTTP-клиент к ledger-adapter
│   └── client.go
│
└── platform/                   # Инфраструктура
    ├── postgres/               #   pgxpool (connection pool)
    ├── kafka/                  #   kafka-go
    └── httpserver/             #   обёртка http.Server
```

## Паттерн модуля

Каждый бизнес-модуль строится по одному шаблону:

```
handler.go    →  принимает HTTP-запрос, парсит, вызывает service, отдаёт ответ
service.go    →  бизнес-логика, валидация, оркестрация между repo и внешними сервисами
repository.go →  SQL-запросы к PostgreSQL через pgxpool
```

**Зависимости строго однонаправленные:**

```
handler → service → repository
                  → ledger.Client (для записи в блокчейн)
```

- `handler` знает о `service`, но НЕ знает о `repository`
- `service` знает о `repository` и `ledger.Client`, но НЕ знает о `handler`
- `repository` не знает ни о ком — только о `pgxpool.Pool`

### Как добавить новый эндпоинт

1. Добавь метод в `repository.go` — SQL-запрос
2. Добавь метод в `service.go` — бизнес-логика, вызов repository
3. Добавь handler в `handler.go` — парсинг запроса, вызов service
4. Зарегистрируй роут в `handler.go → RegisterRoutes()`

### Как добавить новый модуль

1. Создай директорию `internal/<module>/`
2. Создай `handler.go`, `service.go`, `repository.go` по шаблону существующих
3. Добавь wiring в `cmd/wms/main.go`:
   ```go
   myRepo := mymodule.NewRepository(dbPool)
   mySvc := mymodule.NewService(myRepo)
   myHandler := mymodule.NewHandler(mySvc)
   myHandler.RegisterRoutes(r)
   ```

## API

### `GET /health`

Проверяет подключения к PostgreSQL, Kafka и Ledger Adapter.

```json
{
  "status": "ok",
  "checks": {
    "postgres": "ok",
    "kafka": "ok",
    "ledger_adapter": "ok"
  },
  "time": "2026-03-01T15:00:00+05:00"
}
```

`status` = `"ok"` если все проверки прошли, `"degraded"` если что-то недоступно.

## Конфигурация

Все настройки через переменные окружения (см. `internal/config/config.go`):

| Переменная | Описание | По умолчанию |
|------------|----------|-------------|
| `PORT` | Порт HTTP-сервера | `8080` |
| `DB_HOST` | Хост PostgreSQL | `localhost` |
| `DB_PORT` | Порт PostgreSQL | `5432` |
| `POSTGRES_USER` | Пользователь БД | `root` |
| `POSTGRES_PASSWORD` | Пароль БД | `root` |
| `POSTGRES_DB` | Имя базы данных | `wms_blockchain_db` |
| `KAFKA_BROKER` | Адрес Kafka-брокера | `localhost:9092` |
| `LEDGER_ADAPTER_URL` | URL ledger-adapter | — |

## Локальная разработка

```bash
# Установить зависимости
go mod download

# Собрать
go build -mod=vendor ./cmd/wms/

# Запустить тесты
go test -race ./...

# Линтинг
golangci-lint run --config ../.golangci.yml ./...
```

Или через корневой Makefile:

```bash
make lint-wms
make test-wms
make tidy-wms
```

## База данных

Миграции лежат в `migrations/`. Применяются автоматически при `docker compose up` через контейнер `db-migrate`.

Схемы:
- `public` — общие таблицы (`users`, `evm_addresses`, `outbox_events`)
- `wms_inventory` — складские сущности (`warehouses`, `bins`, `skus`, `products`, `orders`...)
- `wms_ops` — операции (`receiving_gate`, `receiving_table`, `assembly_tasks`, `putaways`, `shippings`)

Подробная документация по БД: [docs/db/Database_ru_v2.md](../docs/db/Database_ru_v2.md)

## Технологии

| Что | Зачем | Ссылки |
|-----|-------|--------|
| [pgx/v5](https://github.com/jackc/pgx) | PostgreSQL-драйвер с connection pooling | [Docs](https://pkg.go.dev/github.com/jackc/pgx/v5) |
| [kafka-go](https://github.com/segmentio/kafka-go) | Kafka-клиент без CGO | [Examples](https://github.com/segmentio/kafka-go#reader) |
| [gorilla/mux](https://github.com/gorilla/mux) | HTTP-роутер | [Docs](https://pkg.go.dev/github.com/gorilla/mux) |

## Полезные ссылки

- [Effective Go](https://go.dev/doc/effective_go) — как писать идиоматичный Go
- [Go internal packages](https://go.dev/doc/go1.4#internalpackages) — зачем `internal/` и как он защищает код
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md) — подробный стайлгайд
- [Go database/sql tutorial](https://go.dev/doc/database/) — основы работы с БД (pgx совместим)
- [Standard Go Project Layout](https://github.com/golang-standards/project-layout) — структура проектов
