# WMS Blockchain Project

Система управления складом (WMS) с фиксацией ключевых операций в блокчейне Avalanche C-Chain через CDC (Change Data Capture).

## Архитектура

```
┌─────────────┐    HTTP     ┌──────────────────┐    JSON-RPC    ┌─────────────┐
│  WMS Service │───────────>│  Ledger Adapter   │──────────────>│  Avalanche   │
│  (Go, :8080) │            │  (Go, :8085)      │               │  C-Chain     │
└──────┬───────┘            └──────────────────┘               └─────────────┘
       │
       │ SQL
       v
┌──────────────┐   WAL (CDC)   ┌───────────┐   Kafka   ┌───────────┐
│  PostgreSQL   │─────────────>│  Debezium  │─────────>│   Kafka    │
│  (:5432)      │              │  Connect   │          │  (:9092)   │
└──────────────┘              └───────────┘          └───────────┘
```

**Поток данных:**
1. WMS Service выполняет складские операции (приёмка, сборка, раскладка, отгрузка)
2. Данные пишутся в PostgreSQL
3. Debezium отслеживает изменения через WAL и отправляет события в Kafka
4. WMS Service отправляет запрос в Ledger Adapter для фиксации операции в блокчейне
5. Ledger Adapter подписывает транзакцию и отправляет в Avalanche C-Chain

## Структура проекта

```
blockchain_project/
├── wms/                      # WMS-сервис (модульный монолит)
│   ├── cmd/wms/              # точка входа
│   ├── internal/             # бизнес-логика (приватные пакеты)
│   │   ├── config/           # загрузка конфигурации из env
│   │   ├── domain/           # доменные модели (Warehouse, CargoPlace...)
│   │   ├── receiving/        # модуль: приёмка
│   │   ├── assembly/         # модуль: сборка
│   │   ├── putaway/          # модуль: раскладка
│   │   ├── shipping/         # модуль: отгрузка
│   │   ├── ledger/           # HTTP-клиент к ledger-adapter
│   │   └── platform/         # инфраструктура (postgres, kafka, httpserver)
│   ├── migrations/           # SQL-миграции
│   └── Dockerfile
│
├── ledger-adapter/           # мост к блокчейну Avalanche
│   ├── cmd/adapter/          # точка входа
│   ├── internal/             # бизнес-логика
│   │   ├── config/           # конфигурация
│   │   ├── chain/            # ethclient + ABI смарт-контракта
│   │   └── handler/          # HTTP-обработчики
│   ├── contracts/            # Solidity-контракты
│   └── Dockerfile
│
├── deploy/                   # конфиги инфраструктуры
│   ├── debezium/             # CDC-коннекторы
│   ├── jmx/                  # JMX-экспортёры для мониторинга
│   └── prometheus.yml
│
├── ci/                       # GitLab CI пайплайны
├── docs/                     # документация по БД
│
├── docker-compose.yaml       # все сервисы одной командой
├── Makefile                  # команды для разработки
├── .golangci.yml             # конфиг линтера
├── .pre-commit-config.yaml   # git-хуки
├── .editorconfig             # единый стиль кода
└── .env.example              # шаблон переменных окружения
```

## Быстрый старт

### Требования

- [Docker](https://docs.docker.com/get-docker/) и [Docker Compose](https://docs.docker.com/compose/install/)
- [Go 1.24+](https://go.dev/dl/) — для локальной разработки
- [pre-commit](https://pre-commit.com/#install) — для git-хуков
- [golangci-lint](https://golangci-lint.run/welcome/install-local/) — для линтинга

### Запуск

```bash
# 1. Клонировать репозиторий
git clone <repo-url>
cd blockchain_project

# 2. Скопировать переменные окружения
cp .env.example .env

# 3. Поднять всё
make up
# или
docker compose up -d

# 4. Проверить статус
make ps
```

### Полезные команды

```bash
make help          # все доступные команды
make up            # запустить все сервисы
make down          # остановить все сервисы
make build         # пересобрать и запустить
make logs s=wms-service  # логи конкретного сервиса
make lint          # линтинг обоих сервисов
make test          # тесты обоих сервисов
make hooks-install # установить pre-commit хуки
```

### Доступные сервисы

| Сервис | URL | Описание |
|--------|-----|----------|
| WMS Service | http://localhost:8080 | Основной WMS API |
| Ledger Adapter | http://localhost:8085 | Блокчейн-мост |
| Kafka UI | http://localhost:8080 | Просмотр топиков Kafka |
| Grafana | http://localhost:3000 | Дашборды мониторинга |
| Prometheus | http://localhost:9090 | Метрики |

## Для разработчиков

### Перед началом работы

```bash
# Установить хуки (ОБЯЗАТЕЛЬНО)
make hooks-install

# Проверить что всё работает
make lint
make test
```

### Паттерн модуля

Каждый бизнес-модуль (receiving, assembly, putaway, shipping) имеет одинаковую структуру:

```
internal/receiving/
├── handler.go      # HTTP-обработчики (принимает запрос, вызывает service)
├── service.go      # бизнес-логика (валидация, оркестрация)
└── repository.go   # работа с БД (SQL-запросы)
```

**Правило зависимостей:** `handler → service → repository`. Никогда не наоборот.

Подробнее:
- [wms/README.md](./wms/README.md) — документация WMS-сервиса
- [ledger-adapter/README.md](./ledger-adapter/README.md) — документация Ledger Adapter

### Code Style

- **Линтер**: golangci-lint с конфигом [.golangci.yml](./.golangci.yml)
- **Форматирование**: `gofmt` + `goimports` (автоматически через pre-commit)
- **Коммиты**: осмысленные сообщения на русском или английском

### Полезные ссылки

**Go:**
- [Standard Go Project Layout](https://github.com/golang-standards/project-layout) — структура Go-проектов
- [Effective Go](https://go.dev/doc/effective_go) — идиоматичный Go
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md) — стайлгайд от Uber
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) — чеклист код-ревью

**Архитектура:**
- [Clean Architecture in Go](https://github.com/evrone/go-clean-template) — пример чистой архитектуры
- [Go internal packages](https://go.dev/doc/go1.4#internalpackages) — зачем нужен `internal/`

**Инфраструктура:**
- [Debezium CDC](https://debezium.io/documentation/reference/stable/tutorial.html) — Change Data Capture
- [Avalanche C-Chain](https://docs.avax.network/build/dapp/c-chain-evm) — EVM-совместимый блокчейн
- [go-ethereum](https://geth.ethereum.org/docs) — Go-клиент для EVM (работает с Avalanche)

## Переменные окружения

См. [.env.example](./.env.example) для полного списка.

| Переменная | Описание | Значение по умолчанию |
|------------|----------|----------------------|
| `DB_USER` | Пользователь PostgreSQL | `root` |
| `DB_PASSWORD` | Пароль PostgreSQL | `root` |
| `DB_NAME` | Имя базы данных | `wms_blockchain_db` |
| `RPC_URL` | Avalanche C-Chain RPC | `http://localhost:8545` |
| `PRIVATE_KEY` | Приватный ключ для транзакций | — |
| `CONTRACT_ADDR` | Адрес смарт-контракта | — |
