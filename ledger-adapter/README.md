# Ledger Adapter

Kafka→EVM мост. Читает outbox-события WMS из Kafka, батчит, отправляет одну транзакцию на batch в `BatchMappingWMS`. Адаптер EVM-агностичен: локально работает против Avalanche Subnet-EVM, но подойдёт любой EVM-совместимый RPC. Idempotent, at-least-once, DLQ при фейлах.

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
| `RPC_URL` | ✓ | — | RPC EVM-узла (локально — Avalanche Subnet-EVM; подойдёт любой EVM-совместимый) |
| `CONTRACT_ADDR` | ✓ | — | Адрес `BatchMappingWMS` |
| `REGISTRY_ADDR` | | — | Адрес `ConsortiumRegistry`; без него `GET /registry/keys/{keyId}` отвечает 502. Override: `REGISTRY_ADDR_FILE` |
| `PRIVATE_KEY` | ✓ (static) | — | Hex signer key (0x-prefixed ok); не нужен в kms-режиме |
| `SIGNER_MODE` | | `static` | `static` — **только локальная разработка**: приватник в env открытым текстом; `kms` — подпись во внешнем Web3Signer (ключ адаптеру не нужен) |
| `KMS_URL` | ✓ (kms) | — | Базовый URL Web3Signer (eth1 mode), напр. `http://web3signer:9000` |
| `KMS_PUBLIC_KEYS` | ✓ (kms) | — | CSV identifier'ов ключей (uncompressed X\|\|Y public key из `GET /api/v1/eth1/publicKeys`); multi-signer — просто перечислить несколько. Override: `KMS_PUBLIC_KEYS_FILE` |
| `BATCH_SIZE` | | `10` | Events per tx |
| `BATCH_TIMEOUT` | | `100ms` | Flush by time если batch не набрался |
| `DLQ_TOPIC` | | `wms.dlq.v1` | |
| `RECEIPT_POLL_TIMEOUT` | | `30s` | Макс время ожидания receipt |
| `PORT` | | `8085` | HTTP /health |
| `LOG_LEVEL` | | `info` | (reserved, slog default) |

## Запуск

### Test profile (полный стек с локальным Subnet-EVM)

```bash
docker compose --profile test up -d
# contract-deploy one-shot → пишет RPC_URL / CONTRACT_ADDR / PRIVATE_KEY в shared volume
# ledger-adapter читает их через *_FILE env vars
```

### KMS profile (ключ адаптера во внешнем Web3Signer)

```bash
make kms-up   # = docker compose -f docker-compose.yaml -f docker-compose.kms.yaml --profile test up -d
# kms-init: генерит ключи в Web3Signer (file-raw, том kms_keys — адреса стабильны),
#           фондирует их с ewoq, выдаёт роль Enabled в tx-allow-list (если он включён в genesis),
#           публикует identifier'ы в /shared/kms_public_keys.txt
# ledger-adapter: SIGNER_MODE=kms — приватник в процесс адаптера не попадает вообще
make kms-down # том kms_keys выживает — рефанд не нужен при следующем старте
```

Prod-путь без изменения адаптера — конфиги Web3Signer `type: "hashicorp"`
(ключ в Vault KV v2) или `type: "aws-kms"`; `PIPELINE_WINDOW` масштабируется
от числа KMS-ключей так же, как от `PRIVATE_KEYS`.

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

## CLI владельца консорциума

`registry-cli` (бинарь в образе адаптера) регистрирует организации в `ConsortiumRegistry` и показывает реестр. Транзакцию `registerOrganization` подписывает `OWNER_KEY` (ключ деплоера, в стенде — `/shared/deployer_key.txt`).

```bash
make registry-cli ARGS="register-org --file org.json"   # путь — относительно корня репо
make registry-cli ARGS="list"
```

Формат `org.json` (общий с WMS, менять нельзя):

```json
{"orgId":"0190f2a0-0000-7000-8000-000000000a0a","name":"Org A","qx":"0x<64 hex>","qy":"0x<64 hex>"}
```

`qx`/`qy` — координаты публичного P-256 ключа админа организации. Перед отправкой CLI проверяет, что организация и ключ ещё не зарегистрированы.

После изменения Go-кода нужен `docker compose build ledger-adapter`.

Статус ключа читается эндпоинтом адаптера `GET /registry/keys/{keyId}` (внутренняя сеть, без аутентификации, как `/onchain/tx`): `{"found":false}` или `{"found":true,"org_id":"…","role":"admin|operator","active":true|false}`.

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
