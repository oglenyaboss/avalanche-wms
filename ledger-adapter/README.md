# Ledger Adapter

Kafka→EVM-мост между outbox WMS и контрактами консорциума. Читает единственный топик `wms.events.v1`, принимает только подписанные действия (`aggregate_type = signed_action`), режет окно сообщений на сегменты складских и ключевых действий и отправляет их одной nonce-цепочкой в `SignedMappingWMS.applyActions` и `ConsortiumRegistry.applyKeyActions`. Исход каждого действия пишется в `public.onchain_events`.

Модель подписанных действий — [ADR-0001](../docs/adr/0001-signed-actions.md), внутреннее устройство (пайплайн, конфиг, гочи) — [docs/developer/ledger-adapter-reference.md](../docs/developer/ledger-adapter-reference.md), контракты — [docs/developer/smart-contract-reference.md](../docs/developer/smart-contract-reference.md).

## Пайплайн

```
WMS DB ──outbox_events──▶ Debezium ──▶ Kafka wms.events.v1 ──▶ Consumer
                                                                 │ Parse: только signed_action
                                                                 ▼
                                                        Batcher (окно)
                                                                 ▼
                                              Flusher.Send: сегменты + чанки
                                                 │                      │
                                   applyActions(SignedMappingWMS)  applyKeyActions(ConsortiumRegistry)
                                                 │                      │
                                                 ▼                      ▼
                                     Pipeline → Confirm: receipt + логи → onchain_events
                                                                 │
                                                       DLQ wms.dlq.v1 (терминальные фейлы)
```

1. **Parse.** Из Kafka-заголовков берутся `id` (UUID действия) и `aggregate_type`, из value — конверт действия (колонка `payload` в `outbox_events`, ADR-0001 §9). Всё, что не `signed_action` и не парсится, уходит в DLQ.
2. **Idempotency-фильтр.** По `onchain_events`: `COMMITTED` и `FAILED` без `tx_hash` пропускаются, новые строки вставляются как `PENDING`, строка с `tx_hash` один раз сверяется с receipt'ом без переотправки.
3. **Сегменты.** Окно режется на подряд идущие сегменты одного вида: складские действия → `SignedMappingWMS.applyActions`, ключевые → `ConsortiumRegistry.applyKeyActions`. Порядок внутри сегмента сохраняется: отзыв ключа не переставится раньше действий, которые были до него (ADR-0001 §9).
4. **Чанки.** Действия жадно упаковываются в транзакцию ≤ 120 000 байт calldata: узел subnet-evm отклоняет tx больше 131 072 байт. Действие, не влезающее в лимит в одиночку, — терминальный фейл одного действия.
5. **Отправка.** Один signer на весь адаптер: локальный монотонный nonce, gas price = `SuggestGasPrice × 3`, gas limit = `EstimateGas × 3/2`, legacy-tx. Revert на estimate — терминально, RPC/сеть/KMS-недоступность — retry без траты nonce.
6. **Confirm.** Окно in-flight tx подтверждается receipt'ами (поллинг с backoff до `RECEIPT_POLL_TIMEOUT`). Исход берётся из логов: `ActionApplied` → `COMMITTED`, `ActionRejected` → `FAILED` + `reject_reason` (код причины), revert → `FAILED`. Kafka-offsets коммитятся только после терминального исхода.
7. **Reconcile.** Раз в `RECONCILE_INTERVAL` фоновый loop подтягивает застрявшие `SENT`-строки по `tx_hash` и никогда не переотправляет транзакции.

Гарантии: at-least-once из Kafka (offsets после терминального исхода), двойная идемпотентность (`event_id` уникален в `onchain_events`, на цепи — `actionState(actionId)`), порядок действий в цепи = порядок коммитов в БД (одна партиция `wms.events.v1` + один signer).

## Контракты

| Контракт | Метод | Env |
|---|---|---|
| `SignedMappingWMS` | `applyActions(Action[])` | `CONTRACT_ADDR` |
| `ConsortiumRegistry` | `applyKeyActions(KeyAction[])` | `REGISTRY_ADDR` (без него ключевые действия фейлятся терминально, `/registry/keys/{keyId}` отвечает 502) |

ABI встроены через `//go:embed` (`internal/chain/SignedMappingWMS.abi.json`, `internal/chain/ConsortiumRegistry.abi.json`) и обновляются вручную после изменения контрактов — команды в [docs/developer/README.md](../docs/developer/README.md) (`forge build` + `jq`, вторая копия для аудита в `wms/internal/audit/`).

## Структура пакетов

```
ledger-adapter/
├── cmd/adapter/               # точка входа: config → pool → signer → chain → DLQ → Flusher/Reconciler → consumer + HTTP
├── cmd/kms-init/              # bootstrap KMS-профиля: ключи в Web3Signer, фондирование, allow-list, публикация identifier'ов
├── cmd/registry-cli/          # CLI владельца консорциума: register-org / add-admin-key / list
├── internal/
│   ├── config/                # Load() (*Config, error), <NAME>_FILE override, стартовые инварианты
│   ├── chain/                 # client.go (SendCalldata: nonce/gas/sign/send), ABI (go:embed), резолверы исходов
│   ├── consumer/              # Parse, Batcher, Flusher (сегменты/чанки), Pipeline (окно), Confirm, Reconciler
│   ├── store/                 # pgx pool + onchain_events Repository
│   ├── dlq/                   # kafka.Writer в wms.dlq.v1
│   ├── handler/               # GET /health, /onchain/tx/{hash}, /registry/keys/{keyId}
│   └── kms/                   # подпись через Web3Signer
└── contracts/                 # ConsortiumRegistry.sol, SignedMappingWMS.sol, SignedAction.sol + тесты и скрипты
```

## Конфигурация

Все переменные читаются `config.Load()`. Для любой переменной, читаемой через `GetRequired`/`GetOptional`, работает `<NAME>_FILE`: если файл существует, берётся его trim-содержимое (так test-профиль подставляет артефакты `contract-deploy` из тома `shared_state`).

| Переменная | Обязательно | Default | Описание |
|---|---|---|---|
| `KAFKA_BROKERS` | ✓ | — | CSV брокеров, `kafka:9092`. Override: `KAFKA_BROKERS_FILE` |
| `DB_URL` | ✓ | — | pgx DSN. Override: `DB_URL_FILE` |
| `RPC_URL` | ✓ | — | RPC EVM-узла (локально — Avalanche Subnet-EVM). Override: `RPC_URL_FILE` |
| `CONTRACT_ADDR` | ✓ | — | адрес `SignedMappingWMS`. Override: `CONTRACT_ADDR_FILE` |
| `REGISTRY_ADDR` | | — | адрес `ConsortiumRegistry`; невалидный адрес — ошибка старта. Override: `REGISTRY_ADDR_FILE` |
| `SIGNER_MODE` | | `static` | `static` — локальный ключ из env (**только локальная разработка**); `kms` — подпись во внешнем Web3Signer |
| `PRIVATE_KEY` | ✓ (static) | — | hex ECDSA-ключ (с/без `0x`) — единственный signer. Override: `PRIVATE_KEY_FILE`; в kms-режиме не читается |
| `KMS_URL` | ✓ (kms) | — | базовый URL Web3Signer (eth1 mode). Override: `KMS_URL_FILE` |
| `KMS_PUBLIC_KEYS` | ✓ (kms) | — | identifier единственного ключа (uncompressed X\|\|Y из `GET /api/v1/eth1/publicKeys`); дубликаты и >1 ключа отклоняются. Override: `KMS_PUBLIC_KEYS_FILE` |
| `PORT` | | `8085` | HTTP-порт эндпоинтов адаптера |
| `KAFKA_GROUP_ID` | | `ledger-adapter` | consumer group |
| `DLQ_TOPIC` | | `wms.dlq.v1` | топик для failed/непарсящихся сообщений |
| `BATCH_SIZE` | | `10` | макс. сообщений в окне |
| `BATCH_TIMEOUT` | | `100ms` | макс. время накопления окна |
| `PIPELINE_WINDOW` | | `3` | сколько batch-tx держать in-flight; зажимается до `8` (50% от `TxPoolAccountSlots`) |
| `RECEIPT_POLL_TIMEOUT` | | `30s` | макс. ожидание receipt'а |
| `RECONCILE_INTERVAL` | | `30s` | период фоновой сверки; должен быть `> 0` |
| `RECONCILE_MIN_AGE` | | `1m` | мин. возраст stuck-строки; должен быть `> RECEIPT_POLL_TIMEOUT` |
| `LOG_LEVEL` | | `info` | зарезервирован (slog пишет всё) |

Стартовые инварианты: signer ровно один, `RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT`, `RECONCILE_INTERVAL > 0`.

## Запуск

### Test profile (полный стек с локальным Subnet-EVM)

```bash
docker compose --profile test up -d
# contract-deploy one-shot → пишет rpc_url.txt / registry_addr.txt / contract_addr.txt / deployer_key.txt
# в том shared_state; ledger-adapter читает их через *_FILE env vars
```

### KMS profile (ключ адаптера во внешнем Web3Signer)

```bash
make kms-up   # = docker compose -f docker-compose.yaml -f docker-compose.kms.yaml --profile test up -d
# web3signer: eth1-режим, ключи в томе kms_keys (file-raw, dev-режим), порт только на loopback
# kms-init: генерит ключи (идемпотентно — адреса стабильны между рестартами тома), фондирует их с ewoq,
#           выдаёт роль Enabled в tx-allow-list (если он включён в genesis),
#           публикует identifier'ы в /shared/kms_public_keys.txt
# ledger-adapter: SIGNER_MODE=kms — приватник в процесс адаптера не попадает вообще
make kms-down # том kms_keys выживает — рефондирование при следующем старте не нужно
```

Prod-путь без изменения адаптера — конфиги Web3Signer `type: "hashicorp"` (ключ в Vault KV v2) или `type: "aws-kms"`.

## Тесты

```bash
cd ledger-adapter
go test ./...            # или make test-ledger (go test -race -count=1 ./...)
```

Контракты (`forge` + `forge-std`, который не вендорится — `ledger-adapter/contracts/lib/` в `.gitignore`):

```bash
cd ledger-adapter/contracts
git clone --depth 1 https://github.com/foundry-rs/forge-std lib/forge-std   # один раз
forge test               # контрактные тесты + тест-векторы testdata/signed-actions/vectors.v1.json
```

Газ подписанного действия в `forge test` не показателен: precompile secp256r1 (`0x100`) в Foundry нет. Мерить только на узле — `ledger-adapter/contracts/script/GasBench.s.sol` (команда и таблица замеров — [docs/developer/README.md](../docs/developer/README.md), ADR-0001 §11).

## registry-cli

CLI владельца консорциума (бинарь `/registry-cli` в образе адаптера): регистрирует организацию с ключом админа, добавляет админа и печатает содержимое реестра. Запуск из репозитория при поднятом стеке:

```bash
make registry-cli ARGS="register-org --file org.json"   # регистрация организации + ключ админа
make registry-cli ARGS="add-admin-key --file org.json"  # дополнительный ключ админа
make registry-cli ARGS="list"                           # организации и ключи (роль, активен/отозван)
```

`org.json` — `{"orgId","name","qx","qy"}`: выгружается кнопкой «Скачать ключ админа» на странице «Ключи организации» WMS. Транзакции подписывает ключ из `OWNER_KEY` (`make` подставляет `/shared/deployer_key.txt`); перед отправкой CLI проверяет, что организация и ключ ещё не зарегистрированы, и падает с понятной ошибкой, если `OWNER_KEY` не владелец реестра.
