# Ledger Adapter

Kafka→EVM мост. Читает outbox-события WMS из Kafka, батчит, отправляет одну транзакцию на batch в `BatchMappingWMS` на Avalanche C-Chain. Idempotent, at-least-once, DLQ при фейлах.

## Архитектура

```
┌──────────┐  outbox   ┌────────┐   WMS topics    ┌──────────────┐   batch tx   ┌──────────────┐
│  WMS DB  │───────────▶│Debezium│────────────────▶│    Consumer  │──────────────▶│  BatchMap    │
└──────────┘           └────────┘                 │    + Batcher │              │  WMS.sol     │
                                                  │    + Flusher │              └──────────────┘
                                                  └──────┬───────┘                     │
                                                         │                             │ receipt
                                                         ▼                             ▼
                                                 ┌──────────────┐             ┌──────────────┐
                                                 │onchain_events│◀────────────│ WaitReceipt  │
                                                 │    (pg)      │  MarkCommit │  (polling)   │
                                                 └──────────────┘             └──────────────┘
                                                         │
                                                         │ fail → MarkFailed
                                                         ▼
                                                 ┌──────────────┐
                                                 │  wms.dlq.v1  │
                                                 └──────────────┘
```

Pipeline на batch:
1. **Idempotency**: `Exists(event_id)` в `onchain_events` → пропускаем дубли
2. **InsertPending**: запись `(event_id, aggregate_type, PENDING)`
3. **BatchCall**: `batchAccept/PutAway/Pick/Ship(eventIds[], itemIds[])` (UUID→uint256 keccak256)
4. **MarkSent**(tx_hash): фиксируем что tx отправлена
5. **WaitReceipt**: polling с exponential backoff 50ms→2s, потолок `RECEIPT_POLL_TIMEOUT`
6. **MarkCommitted** если `receipt.Status==1`, иначе **MarkFailed** + публикация в DLQ

Delivery guarantees:
- **At-least-once**: kafka offsets коммитятся ТОЛЬКО после успешного `MarkCommitted`/`MarkFailed`. Crash до коммита → пересчитаем через Exists при рестарте.
- **Idempotency on-chain**: `processedEventIds` в контракте reject'ит дубли. Adapter дублирует защиту на уровне БД (`Exists`) чтобы не тратить газ.

## Топики Kafka → методы контракта

| Topic | aggregate_type | Solidity method |
|---|---|---|
| `wms.receiving.v1` | receiving | `batchAccept(uint256[], uint256[])` |
| `wms.putaway.v1` | putaway | `batchPutAway(...)` |
| `wms.picking.v1` | picking | `batchPick(...)` |
| `wms.shipping.v1` | shipping | `batchShip(...)` |

Формат сообщения (от Debezium outbox):
- `key` = `product_id` (UUID string) → `itemId` uint256
- `headers["id"]` = `event_id` (UUID string) → `eventId` uint256
- value — произвольный JSON payload (не используется адаптером, сохраняется для DLQ)

## Структура пакетов

```
ledger-adapter/
├── cmd/adapter/main.go        # slog + config → pool → chain → dlq → Flusher → N consumer goroutines + /health
│
├── internal/
│   ├── config/                # Load() (*Config, error) с _FILE override для secrets
│   ├── chain/                 # go-ethereum wrappers
│   │   ├── client.go          # BatchAccept/PutAway/Pick/Ship + BatchCall dispatcher
│   │   ├── abi.go             # go:embed BatchMappingWMS.abi.json
│   │   ├── convert.go         # UUIDToUint256 (keccak256)
│   │   └── receipt.go         # WaitReceipt — exponential backoff
│   ├── store/                 # pgx pool + onchain_events Repository
│   ├── consumer/              # Message/Parse + Batcher + Flusher + Consumer
│   ├── dlq/                   # kafka.Writer для wms.dlq.v1
│   └── handler/               # /health only
│
└── contracts/
    └── BatchMappingWMS.sol    # Solidity контракт (компилируется в contract-deploy)
```

## Конфигурация

Все обязательные поля поддерживают `<NAME>_FILE` override (читает путь к файлу вместо прямого env). Нужно для test-профиля, где `contract-deploy` сервис пишет артефакты в shared volume.

| Переменная | Обязательно | Default | Описание |
|---|---|---|---|
| `KAFKA_BROKERS` | ✓ | — | CSV broker list, `kafka:9092` |
| `KAFKA_GROUP_ID` | | `ledger-adapter` | consumer group |
| `DB_URL` | ✓ | — | pgx DSN `postgres://user:pw@host/db` |
| `RPC_URL` | ✓ | — | Avalanche C-Chain RPC |
| `CONTRACT_ADDR` | ✓ | — | Адрес `BatchMappingWMS` |
| `PRIVATE_KEY` | ✓ | — | Hex signer key (0x-prefixed ok) |
| `BATCH_SIZE` | | `10` | Events per tx |
| `BATCH_TIMEOUT` | | `100ms` | Flush by time если batch не набрался |
| `DLQ_TOPIC` | | `wms.dlq.v1` | |
| `RECEIPT_POLL_TIMEOUT` | | `30s` | Макс время ожидания receipt |
| `PORT` | | `8085` | HTTP /health |
| `LOG_LEVEL` | | `info` | (reserved, slog default) |

## Запуск

### Test profile (полный стек с локальным avalanchego)

```bash
docker compose --profile test up -d
# contract-deploy one-shot → пишет RPC_URL / CONTRACT_ADDR / PRIVATE_KEY в shared volume
# ledger-adapter читает их через *_FILE env vars
```

### Локально без docker (unit-dev)

```bash
export KAFKA_BROKERS=kafka:9092
export DB_URL="postgres://root:root@localhost/wms_blockchain_db?sslmode=disable"
export RPC_URL=https://api.avax-test.network/ext/bc/C/rpc
export CONTRACT_ADDR=0x...
export PRIVATE_KEY=0x...

cd ledger-adapter
go run ./cmd/adapter/
```

## Тестирование

```bash
# Unit tests (быстро, ~5s)
go test -race ./...

# Integration tests (требуют Docker — testcontainers-go/postgres)
go test -tags integration -race -timeout 180s ./internal/store/...
```

Покрытие:
- `config/`: 9 tests — defaults, missing required, env, `_FILE` override (file missing, file wins, valid trim), invalid int/duration.
- `chain/`: 9 tests — UUIDToUint256 vectors (cast keccak cross-check), determinism, ABI parses+includes methods+event, WaitReceipt (happy/retry/timeout/non-NotFound/cancel).
- `store/`: 2 unit + 5 integration (testcontainers-pg).
- `consumer/`: 17 tests — Parse (valid + 4 errors), topic-map, Batcher (empty/size/timeout/drain/concurrent), Flusher (happy/duplicates/revert→DLQ/receipt-zero→DLQ/transient/partial).

## Troubleshooting

| Симптом | Причина | Фикс |
|---|---|---|
| `config: missing required env: KAFKA_BROKERS` | env не задан | см. таблицу выше |
| `chain client: dial rpc: ...` | RPC URL некорректный или узел недоступен | проверь `cast chain-id --rpc-url $RPC_URL` |
| `chain client: parse private key` | Ключ некорректный или кривой формат | 64 hex chars с/без `0x` |
| `chain call failed: execution reverted: Duplicate eventId` | Идемпотентность сработала на chain-уровне после рестарта БД | Проверь что `onchain_events` волюм не был убит отдельно от состояния контракта; нужно `down -v` обоих |
| `receipt timeout` в логах, затем DLQ | RPC slow / blockchain congested | Увеличь `RECEIPT_POLL_TIMEOUT` или разберись с инфрой |
| DLQ растёт | Смотри headers `reason` и `original_topic` в `wms.dlq.v1` |

## Полезные ссылки

- [go-ethereum book](https://goethereumbook.org/)
- [BatchMappingWMS.sol](contracts/BatchMappingWMS.sol) — контракт
- [Issue #20](https://git.miem.hse.ru/2340/blockchain_project/-/issues/20) — постановка задачи
- [docs/ADR](../docs/ADR/) — архитектурные решения WMS
