# Ledger Adapter — справочник разработчика

**Версия:** 2.0
**Дата:** 2026-10-20
**Аудитория:** команда, принимающая сервис на сопровождение (hand-off)

Этот документ описывает внутреннее устройство `ledger-adapter` — Go-сервиса-моста между WMS-аутбоксом и контрактами `SignedMappingWMS` (складские действия) и `ConsortiumRegistry` (ключевые действия) на Avalanche Subnet-EVM. Модель подписанных действий — [ADR-0001](../adr/0001-signed-actions.md); сигнатуры и события контрактов — [Smart Contract Reference](smart-contract-reference.md); общий маппинг операций — [Маппинг WMS → Блокчейн](../integration/blockchain-mapping.md); форматы данных на каждом стыке — [Контракт данных](../integration/data-contract.md).

---

## 1. Назначение моста

`ledger-adapter` — это **однонаправленный потребитель** Kafka, превращающий подписанные складские действия WMS в транзакции к контрактам.

Полный путь действия: WMS пишет строку в `public.outbox_events` (`aggregate_type = 'signed_action'`, `payload` — подписанный конверт, ADR-0001 §9) → Debezium (CDC) публикует её в единственный Kafka-топик `wms.events.v1` → `ledger-adapter` потребляет топик, накапливает окно (по времени/размеру), режет его на сегменты складских и ключевых действий, пакует в чанки по calldata и отправляет **одну транзакцию на чанк** в Kafka-порядке. Исход каждого действия (применено / отклонено с причиной) разбирается из логов receipt'а и пишется в `public.onchain_events`; фоновый reconcile-loop подтягивает застрявшие строки.

> **У адаптера нет бизнес-REST-интерфейса на запись.** HTTP-эндпоинты только читают: `GET /health` (liveness), `GET /onchain/tx/{hash}` (receipt транзакции для доказательства on-chain) и `GET /registry/keys/{keyId}` (статус ключа в `ConsortiumRegistry`), см. §9. Chain-status gate (`ledger.CheckChainStatus` на стороне WMS) читает `public.onchain_events` напрямую через общий PostgreSQL, а не через адаптер.

```
WMS → outbox_events → Debezium → Kafka (wms.events.v1) → ledger-adapter → SignedMappingWMS / ConsortiumRegistry
                                                              │
                                                              └──→ public.onchain_events (PENDING/SENT/COMMITTED/FAILED + reject_reason)
```

Соответствие этапов жизненного цикла товара — см. потоковые диаграммы: [Приёмка на столе](../flows/receiving-table-flow.md), [Раскладка](../flows/putaway-flow.md), [Сборка](../flows/assembly-flow.md), [Отгрузка](../flows/shipping-flow.md), а также [Сквозной путь товара](../business-process/end-to-end-flow.md) и [DFD](../business-process/data-flow-diagrams.md).

---

## 2. Структура `internal/`

| Пакет / файл | Ответственность |
|---|---|
| `cmd/adapter/main.go` | Точка входа: config → pool → signer → chain client → резолверы → DLQ → Flusher/Reconciler → consumer + HTTP |
| `internal/config/config.go` | `Load()` — чтение env, `_FILE`-override для секретов, валидация обязательных полей и инвариантов (в т.ч. «signer ровно один») |
| `internal/chain/client.go` | `Client` — обёртка над `ethclient`: единый путь `SendCalldata` (nonce/gas/sign/send), классификация ошибок (revert vs transient) |
| `internal/chain/abi.go` | Встраивает `SignedMappingWMS.abi.json` и `ConsortiumRegistry.abi.json` через `//go:embed` |
| `internal/chain/warehouseactions.go` | `WarehouseAction`, `PackApplyActions`; резолвер исходов складских действий (логи `ActionApplied`/`ActionRejected` + fallback `actionState`) |
| `internal/chain/keyactions.go` | `KeyAction`, `WebAuthnAuth`, `PackApplyKeyActions` |
| `internal/chain/registry.go` | `Registry` — read-only реестра (`getKey`) + резолвер исходов ключевых действий |
| `internal/chain/actioncontract.go` | Общая plumbing резолверов: `eth_call`, `actionState`, reason из журнала `ActionRejected` |
| `internal/chain/signer.go` | `TxSigner`: static (локальный ECDSA) и интерфейс для kms; `ErrSignerTransient` |
| `internal/chain/receipt.go` | `WaitReceipt` — поллинг receipt'а с экспоненциальным backoff и таймаутом |
| `internal/chain/convert.go` | `UUIDToUint256` — `keccak256(UUID-строка) → *big.Int` (совпадает с контрактом) |
| `internal/consumer/message.go` | `ActionEnvelope` + `Parse()` — разбор Kafka-сообщения подписанного действия |
| `internal/consumer/batcher.go` | `Batcher` — потокобезопасный аккумулятор окна; флаш по размеру/времени; `Unshift` для retry |
| `internal/consumer/consumer.go` | `Consumer` — fetch-loop → `Parse` → `Batcher` → `Flusher.Send` → `Pipeline` |
| `internal/consumer/flusher.go` | `Flusher` — idempotency-фильтр, `Send` (сегменты), Confirm-примитивы |
| `internal/consumer/flusher_actions.go` | `splitSegments`, `sendActions` — чанкирование по calldata, терминальные фейлы |
| `internal/consumer/flusher_confirm.go` | `Confirm` — receipt → `applyActionOutcomes` (COMMITTED / FAILED + reject_reason) |
| `internal/consumer/pipeline.go` | `Pipeline` — окно in-flight tx + FIFO-collector, коммит Kafka-offset'ов по подтверждённому префиксу |
| `internal/consumer/reconcile.go` | `Reconciler` — фоновая сверка stuck-строк; `reconcileReceipt`/`applyActionOutcomes` |
| `internal/store/pool.go` | `NewPool` — `pgxpool` (`MaxConns=10`, `MinConns=1`, `MaxConnIdleTime=30s`, startup-ping 10s) |
| `internal/store/onchain_events.go` | `Repository` — весь SQL по `public.onchain_events` (включая `reject_reason`); тип `ReconcileRow` |
| `internal/dlq/producer.go` | `Producer` — `kafka.Writer` (acks `RequireAll`) в DLQ-топик; `PublishAll` сохраняет исходный payload и добавляет метаданные |
| `internal/handler/handler.go` | `Handler`: `/health`, `/onchain/tx/{hash}`, `/registry/keys/{keyId}` (§9) |
| `cmd/registry-cli/` | CLI владельца консорциума: `register-org` / `add-admin-key` / `list` ([ledger-adapter/README.md](../../ledger-adapter/README.md)) |
| `cmd/kms-init/` | bootstrap kms-профиля: ключи в Web3Signer, фондирование, allow-list, публикация identifier'ов |

Принцип «многих маленьких файлов»: каждый пакет имеет узкую ответственность, интерфейсы определены на стороне потребителя (`consumer`, `handler`), а конкретные реализации (`chain.Client`, `store.Repository`, `dlq.Producer`) их удовлетворяют.

---

## 3. Потребление Kafka (consumer group)

**Потребляемый топик** — единственный, захардкожен в `cmd/adapter/main.go`:

```go
const eventsTopic = "wms.events.v1"
```

> **Одна партиция — часть контракта.** Порядок действий в цепи равен порядку коммитов в БД; на этом держится семантика отзыва ключа (ADR-0001 §7). Расширять топик до >1 партиции нельзя.

**Consumer group** — env `KAFKA_GROUP_ID`, по умолчанию `ledger-adapter`.

**Конфигурация `kafka.ReaderConfig`** (`internal/consumer/consumer.go`): `MinBytes=1`, `MaxBytes=10e6` (10 MB), `MaxWait=100ms`.

**Формат сообщения** (выставляется Debezium Outbox EventRouter; соединяется с ADR-0001 §9):

| Поле | Источник | Содержимое |
|---|---|---|
| Key | `aggregate_id` | `actionId` — обязан совпадать с header `id` |
| Header `id` | `event_id` | UUID действия (`Message.EventID`) |
| Header `aggregate_type` | `aggregate_type` | **`signed_action`** — единственное принимаемое значение |
| Value | колонка `payload` | JSON-строка с конвертом действия (ниже) |

Конверт (`ActionEnvelope` в `message.go`): `version` (1), `actionId`, `actionType` (1–6), `itemIds` (UUID товаров), `detailsHash` (только складские), `subjectKey` (qx/qy регистрируемого ключа — только ключевые), `keyId` (ключ подписанта) и `webauthn` (`authenticatorData`, `clientDataJSON`, `challengeIndex`, `typeIndex`, `r`, `s`).

**Цикл обработки** (`Consumer.Run`):

1. `fetchLoop` читает сообщения в канал; основной `select`: отмена, ошибки чтения, сообщения и тикер 50ms.
2. Каждое сообщение проходит `Parse`; ошибка парсинга (в т.ч. `aggregate_type ≠ signed_action`) → DLQ, offset коммитится **в FIFO-порядке через pipeline** (zero-tx batch), чтобы не зациклиться на «ядовитом» сообщении.
3. Валидное сообщение кладётся в `Batcher`; по `ShouldFlush()` (размер ≥ `BATCH_SIZE` или прошло `BATCH_TIMEOUT`) окно дренируется.
4. `Flusher.Send` режет окно на сегменты и чанки и возвращает отправленные tx; `Consumer` кладёт их в `Pipeline.Submit`; при transient-ошибке — `Batcher.Unshift` и retry на следующем тике, offset'ы не коммитятся.
5. Collector (одна горутина) подтверждает batch'и FIFO и коммитит offsets по полностью подтверждённому batch'у (§5).
6. На завершении (ctx cancel) — graceful drain окна.

> Consumer работает **в одной goroutine** — условие сохранения Kafka-порядка в nonce-цепочке signer'а.

---

## 4. Send: окно → сегменты → чанки

### Окно

| Параметр | Env | По умолчанию | Смысл |
|---|---|---|---|
| Размер окна | `BATCH_SIZE` | `10` | макс. сообщений в одном слитом окне |
| Окно | `BATCH_TIMEOUT` | `100ms` | макс. время накопления до флаша по таймауту |
| In-flight tx | `PIPELINE_WINDOW` | `3` | сколько batch-tx держать неподтверждёнными одновременно; зажимается сверху до `perSignerWindow = 8` (50% от `TxPoolAccountSlots=16` узла) |

### Сегменты

`Flusher.Send` (`splitSegments` в `flusher_actions.go`) режет окно на **подряд идущие** сегменты одного вида: складские действия (`WarehouseAction != nil`) и ключевые (`KeyAction != nil`). Порядок сообщений внутри сегмента сохраняется — отзыв ключа нельзя переставить раньше действий, которые были до него (ADR-0001 §9). Сегменты обрабатываются в Kafka-порядке, все tx уходят с одного signer'а одной nonce-цепочкой.

| Вид сегмента | Метод | Контракт |
|---|---|---|
| складские действия | `applyActions(Action[])` | `SignedMappingWMS` (`CONTRACT_ADDR`) |
| ключевые действия | `applyKeyActions(KeyAction[])` | `ConsortiumRegistry` (`REGISTRY_ADDR`) |

Без `REGISTRY_ADDR` ключевые действия терминально фейлятся («applyKeyActions: contract not configured» — DLQ + FAILED), складские работают.

### Пайплайн `sendActions` (для каждого сегмента)

1. `filterAndMarkPending` — фильтр идемпотентности (см. §6), вставка новых строк со статусом `PENDING`.
2. `maxActionChunk` — жадная упаковка подряд идущих действий в **чанк ≤ 120 000 байт calldata** (`maxActionCalldata`): узел subnet-evm отклоняет tx больше 131 072 байт, при таком объёме газ не упирается в лимит блока (ADR-0001 §9–10). Действие, не пакующееся в ABI, или превышающее лимит в одиночку, — терминальный фейл одного действия (DLQ + FAILED), остальные идут дальше.
3. `chain.SendCalldata` — отправка готовых calldata по общему пути `send`: локальный монотонный nonce (seed из `PendingNonceAt` один раз, `++` только после успешной отправки), `SuggestGasPrice × 3` (запас против underpricing in-flight tx при росте base fee), `EstimateGas`, gas limit = **оценка × 3/2** (`gasLimitHeadroomNum/Den` — запас на разрыв «estimate видит ранний статус товара → к майнингу статус уже изменился»), legacy-tx, подпись `TxSigner`, `SendTransaction`. Ошибки: revert на estimate → `ErrChainRevert` (терминально), сеть/RPC/KMS-недоступность → `ErrChainTransient` (retry, nonce не тратится).
4. `store.MarkSent` — `tx_hash` + статус `SENT`; tx попадает в `InflightTx` окна pipeline.

> **Свойство порядка.** Жадная упаковка в calldata-лимит сохраняет Kafka-порядок и внутри чанка, и между чанками/tx: nonce-цепочка одного signer'а исполняется строго по порядку отправки.

## 5. Pipeline, Confirm и исходы действий

`Pipeline` (`pipeline.go`) — окно in-flight batch'ей и единственная горутина-collector:

- `Submit` блокируется при заполненном окне (backpressure; `PIPELINE_WINDOW`).
- Collector подтверждает tx одного batch'а **параллельно** (до 16 одновременных receipt-poll'ов, `maxConfirmConcurrency`), порядок между batch'ами — FIFO.
- Kafka-offsets batch'а коммитятся только когда **все** его tx терминально успешны. Пустой txs (всё скиплнулось/DLQ) проходит через collector тоже — коммит offset'ов строго в очереди.
- `ErrConfirmStalled` (tx не замайнилась и не ревертнулась за `RECEIPT_POLL_TIMEOUT`) → окно бросается **без коммита offsets** → `ErrPipelineStalled` → процесс рестартует: свежий nonce (`ReseedNonce` не нужен — рестарт пересеивает его), Kafka redelivers незакоммиченное окно, SENT-строки подтягивает reconcile. Recovery = рестарт процесса, не in-place reopen.

`Confirm` (`flusher_confirm.go`) для одной in-flight tx:

1. `chain.WaitReceipt` — поллинг с экспоненциальным backoff `50ms → … → 2s`, таймаут `RECEIPT_POLL_TIMEOUT`; `ethereum.NotFound` → продолжаем, другая RPC-ошибка → немедленный возврат (stall).
2. `receipt.Status == 0` → `recordFailure` (DLQ → MarkFailed, «execution reverted»).
3. Успех → `applyActionOutcomes`: резолверы складских (`SignedMappingWMS`) и ключевых (`ConsortiumRegistry`) действий разбирают исходы **каждого действия**:
   - логи receipt'а своего контракта: `ActionApplied` (topic1 = actionId) → Applied; `ActionRejected` (topic1 = actionId, reason в data) → код причины;
   - действие без лога в этом receipt'е (повторная отправка, ADR-0001 §6) — fallback по `actionState`: `Applied` → Applied, `Rejected` → reason из журнала (`FilterLogs` по `ActionRejected` с topic1 = actionId), `None` → контракт действие не знает.
   - Applied → `MarkCommitted`; отклонённые группируются по reason → `MarkRejected` (статус `FAILED` + `reject_reason`); id без исхода ни в одном контракте — ошибка (строки остаются SENT, retry/reconcile).

> **Контракт пишет исход на каждое действие** (ADR-0001 §6): batch-транзакция коммитится даже при отклонённых действиях, поэтому `receipt.Status == 1` не означает, что все действия применены — только разбор логов даёт истину.

## 6. Идемпотентность (`actionId`)

| Уровень | Механизм | Где |
|---|---|---|
| Внутри окна | дедуп по `event_id` через `seen`-map | `filterAndMarkPending` |
| В БД | `INSERT ... ON CONFLICT (event_id) DO NOTHING`, `UNIQUE (event_id)` | `InsertPending` |
| On-chain | `actionState(actionId) != None` → тихий пропуск | оба контракта |

**Логика `filterAndMarkPending`** для каждого сообщения:

| Состояние в БД | Действие |
|---|---|
| Нет в БД | `InsertPending` + добавить в pending |
| Статус `COMMITTED` | skip (уже подтверждено) |
| Есть `tx_hash` (любой статус) | `reconcileReceipt` — однократная проверка receipt'а, **без переотправки** (tx_hash остаётся стабильным, S2) |
| Статус `FAILED`, `tx_hash` пуст | skip (терминально, транзакция никогда не отправлялась) |
| Статус `PENDING`, `tx_hash` пуст | warning + переотправка; если tx всё же ушла, а `MarkSent` упал — контракт пропустит повтор `actionId`, первая tx осиротеет в БД |

## 7. Reconcile-loop

`Reconciler` (`internal/consumer/reconcile.go`) — фоновая read-mostly сверка, которая подтягивает БД к on-chain-истине. **Никогда не переотправляет транзакции.**

`reconcileOnce` каждый тик (`RECONCILE_INTERVAL`):

1. `ListReconcilable(minAge)` — строки `status IN ('SENT','FAILED')` с непустым `tx_hash`, **без `reject_reason`** (отклонение контракта — терминальный исход, их не перечитывают), старше `RECONCILE_MIN_AGE`, `ORDER BY updated_at ASC LIMIT 1000`.
2. Группирует строки по `tx_hash` (одна проверка receipt'а на batch-транзакцию).
3. Для каждой транзакции `reconcileReceipt` — тот же примитив, что и в redelivery-пути flusher'а: mined + success → `applyActionOutcomes` (COMMITTED / MarkRejected); mined + revert → `MarkFailed`; не замайнено → строка остаётся как есть.

> **Reconcile-FAILED НЕ публикуется в DLQ** (в отличие от синхронного `recordFailure`). После перехода на подписанные действия revert замайненной tx — редкость (батч не ревертится из-за отклонённого действия); полноценная видимость отказов — открытый TODO(#41).

## 8. Хранилище `onchain_events`

Таблица `public.onchain_events` **создаётся миграциями WMS** (`wms/migrations/`), не адаптером — у адаптера нет своего раннера миграций, он предполагает, что схема существует. Подробная схема БД — см. [Схема БД](../db/Database_ru_v2.md) и [ER-диаграмма](../data-model/er-diagram.md).

| Колонка | Тип | Примечание |
|---|---|---|
| `id` | `bigserial` | PRIMARY KEY |
| `event_id` | `uuid` | `UNIQUE`; для подписанных действий = `actionId` |
| `aggregate_type` | `text` | для действий — `signed_action` |
| `tx_hash` | `text` | `NULL` до `MarkSent` |
| `status` | `public.onchain_event_status` | ENUM, по умолчанию `PENDING` |
| `reject_reason` | `smallint` | код `RejectReason` (ADR-0001 §6), если контракт отклонил действие (миграция 0015) |
| `error_message` | `text` | заполняется `MarkFailed`/`MarkRejected` |
| `created_at` / `updated_at` | `timestamptz` | `updated_at` — триггером |

**ENUM** `public.onchain_event_status`: `PENDING`, `SENT`, `COMMITTED`, `FAILED`. Индексы: `idx_onchain_events_status`, `idx_onchain_events_created_at`.

**Статусная машина строки:**

```
PENDING ──MarkSent──► SENT ──receipt + логи──► COMMITTED (Applied)
                            │              └─► FAILED + reject_reason (ActionRejected)
                            └─revert / timeout / RPC-fail──► FAILED
```

**SQL-операции репозитория** (`store.Repository`): `Exists`, `InsertPending` (ON CONFLICT no-op), `StatusAndTx`, `MarkSent` (guard `<> 'COMMITTED'`), `MarkCommitted`, `MarkFailed` (guard `<> 'COMMITTED'`, tx_hash не затирается пустым), `MarkRejected` (FAILED + reject_reason + error_message), `Status`, `ListReconcilable`.

> **Guard `status <> 'COMMITTED'` в `MarkSent`/`MarkFailed`** критичен: если reconcile-loop пометил строку `COMMITTED`, а запоздавший `WaitReceipt`-таймаут вызовет `MarkFailed` на ту же строку — без guard'а `COMMITTED` был бы перезатёрт на `FAILED`. Guard делает такой `UPDATE` no-op.

## 9. DLQ

`dlq.Producer` (`internal/dlq/producer.go`) — `kafka.Writer` с acks `RequireAll`; топик — env `DLQ_TOPIC`, по умолчанию `wms.dlq.v1`. `PublishAll` копирует исходные key/value/headers и добавляет заголовки `original_topic`, `reason`, `failed_at` (RFC3339 UTC).

**Что попадает в DLQ:**

- Сообщения с ошибкой `Parse` — в том числе `aggregate_type ≠ 'signed_action'` (адаптер принимает только подписанные действия).
- Терминальные фейлы сегмента/чанка через `recordFailure`: контракт не сконфигурирован, действие не пакуется в ABI или превышает calldata-лимит, revert на `EstimateGas` (`ErrChainRevert`), `receipt.Status == 0` в Confirm.

**Что НЕ попадает в DLQ:** строки, помеченные `FAILED` фоновым reconcile-loop (см. §7, TODO #41).

## 10. REST-эндпоинты адаптера

Три эндпоинта, все только на чтение и без аутентификации: они отдают данные, которые и так публичны в цепи. Бизнес-REST-поверхности на запись нет. Порт публикуется только на loopback (`127.0.0.1:8085`); публичный вход — аутентифицированный прокси WMS.

### `GET /health`

Liveness-проба: всегда `200 OK`, `{"status":"ok","time":"<RFC3339 UTC>"}`. Не проверяет Kafka/БД/RPC (TODO #50). Вызывающая сторона: `wms/internal/ledger/client.go` — `Client.HealthCheck()`.

### `GET /onchain/tx/{hash}`

Один живой запрос receipt к RPC (без поллинга), таймаут 4 с. `hash` — `0x` и 64 hex-символа.

| Ситуация | Ответ |
|---|---|
| `hash` не по формату | `400` `{"error":"INVALID_TX_HASH"}` |
| нет клиента или ошибка RPC | `502` `{"error":"CHAIN_UNREACHABLE"}` |
| receipt ещё нет | `200` `{"found":false,"status":"pending"}` |
| receipt есть | `200` `{"found":true,"status":"success"|"failed","block_number":…,"confirmations":…,"gas_used":…}` |

Нулевые `block_number`/`confirmations`/`gas_used` в ответ не попадают; `confirmations` = голова цепи минус блок receipt, при отстающей голове зажимается до 0. Вызывающая сторона: `Client.GetTxReceipt()`; наружу его отдаёт аутентифицированный `GET /onchain/tx/{hash}` WMS.

### `GET /registry/keys/{keyId}`

Читает `getKey(bytes32)` у `ConsortiumRegistry` (адрес из `REGISTRY_ADDR`), таймаут 4 с. `keyId` — `0x` и 64 hex-символа (`keccak256(qx ‖ qy)`).

| Ситуация | Ответ |
|---|---|
| `keyId` не по формату | `400` `{"error":"INVALID_KEY_ID"}` |
| `REGISTRY_ADDR` не задан или ошибка RPC | `502` `{"error":"CHAIN_UNREACHABLE"}` |
| ключа нет в реестре | `200` `{"found":false}` |
| ключ есть | `200` `{"found":true,"org_id":"<uuid>","role":"admin"|"operator","active":true|false}` |

Вызывающая сторона: сервис passkey WMS — при чтении синхронизирует статус ключа (`PENDING` → `ACTIVE` или `REVOKED`).

## 11. Конфигурация (env-переменные)

Все переменные читаются `config.Load()` (`internal/config/config.go`). Для секретов поддерживается `<NAME>_FILE`-override: если `<NAME>_FILE` задан и файл читается, используется его trim'нутое содержимое; если файл не найден — warning в stderr и fallback на прямой env. Так профиль `test` подставляет артефакты `contract-deploy` из тома `shared_state`.

| Переменная | Обяз. | По умолчанию | Описание |
|---|:---:|---|---|
| `KAFKA_BROKERS` | да | — | CSV-список брокеров. Override: `KAFKA_BROKERS_FILE` |
| `DB_URL` | да | — | pgx DSN. Override: `DB_URL_FILE` |
| `RPC_URL` | да | — | RPC Avalanche Subnet-EVM. Override: `RPC_URL_FILE` |
| `CONTRACT_ADDR` | да | — | hex-адрес `SignedMappingWMS`. Override: `CONTRACT_ADDR_FILE` |
| `REGISTRY_ADDR` | нет | — | hex-адрес `ConsortiumRegistry`; невалидный адрес — ошибка старта, пустое значение — ключевые действия терминально фейлятся, `/registry/keys` отвечает 502. Override: `REGISTRY_ADDR_FILE` |
| `SIGNER_MODE` | нет | `static` | `static` \| `kms` — источник подписи: локальный ключ (только локальная разработка) или внешний Web3Signer |
| `PRIVATE_KEY` | да (static) | — | hex ECDSA-ключ (с/без `0x`). Override: `PRIVATE_KEY_FILE`. В kms-режиме не читается |
| `PRIVATE_KEYS` | нет | — | историческая CSV-форма; signer ровно один — при >1 значении старт падает |
| `KMS_URL` | да (kms) | — | базовый URL Web3Signer (eth1 mode). Override: `KMS_URL_FILE` |
| `KMS_PUBLIC_KEYS` | да (kms) | — | identifier единственного ключа (uncompressed X\|\|Y, из `GET /api/v1/eth1/publicKeys`); дубликаты и >1 ключа отклоняются. Override: `KMS_PUBLIC_KEYS_FILE` |
| `PORT` | нет | `8085` | HTTP-порт эндпоинтов адаптера (§10) |
| `KAFKA_GROUP_ID` | нет | `ledger-adapter` | Kafka consumer group |
| `DLQ_TOPIC` | нет | `wms.dlq.v1` | Kafka DLQ-топик |
| `BATCH_SIZE` | нет | `10` | макс. сообщений в окне; невалидный int → default + warning |
| `BATCH_TIMEOUT` | нет | `100ms` | окно накопления; невалидный → default + warning |
| `PIPELINE_WINDOW` | нет | `3` | in-flight batch-tx; зажимается до `perSignerWindow = 8` |
| `RECEIPT_POLL_TIMEOUT` | нет | `30s` | макс. ожидание `WaitReceipt` |
| `RECONCILE_INTERVAL` | нет | `30s` | период reconcile-sweep'а; должен быть `> 0` |
| `RECONCILE_MIN_AGE` | нет | `1m` | мин. возраст stuck-строки для reconcile |
| `LOG_LEVEL` | нет | `info` | **зарезервировано**: читается в `Config.LogLevel`, но не подключено к slog (`main.go` создаёт `slog.NewJSONHandler(os.Stdout, nil)` без level-фильтра) |

**Инварианты, проверяемые на старте** (`Load()` возвращает ошибку и прерывает запуск):

- **Signer ровно один** — в static и kms: подписанные действия держат Kafka-порядок одной nonce-последовательностью, multi-signer шардирование нарушило бы порядок отзыва ключа (ADR-0001 §9). Отклоняется `PRIVATE_KEYS` с >1 ключом и `KMS_PUBLIC_KEYS` с >1 identifier'ом.
- `RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT` — иначе reconcile-loop гонялся бы с in-flight `WaitReceipt` флашера.
- `RECONCILE_INTERVAL > 0` — иначе `time.NewTicker` в reconcile-loop паникует (`time.ParseDuration` принимает `0s` без ошибки).
- `PIPELINE_WINDOW ≥ 1`; значения выше потолка зажимаются с warning.

> **Hardcoded, не настраивается через env:** пул (`MaxConns=10`, `MinConns=1`, `MaxConnIdleTime=30s`, startup-ping 10s), `kafka.ReaderConfig` (`MinBytes=1`, `MaxBytes=10MB`, `MaxWait=100ms`), calldata-лимит чанка (120 000 байт), headroom'ы цены/лимита газа, `maxConfirmConcurrency = 16`.

## 12. Точка входа `cmd/adapter/main.go`

Порядок сборки в `run()`:

1. `config.Load()` — валидация env и инвариантов.
2. `store.NewPool` + `store.NewRepository`.
3. `buildSigner(cfg)` — ровно один `TxSigner`: static (`PrivateKeys[0]`, fallback `PRIVATE_KEY`) или kms (`KMS_PUBLIC_KEYS[0]`).
4. `chain.NewClientWithSigner` — один клиент обслуживает и отправку, и receipt-чтения (receipt'ы node-global); логирует адрес signer'а.
5. `assertSignersFunded` — fail-fast, если signer не профинансирован (иначе out-of-funds реверты под нагрузкой).
6. Отдельный `ethclient` для read-only резолверов: `NewWarehouseActions(CONTRACT_ADDR)` — всегда, `NewRegistry(REGISTRY_ADDR)` — при заданном адресе.
7. `dlq.NewProducer`; `Flusher` (+`WithWarehouseActions`/`WithRegistry`), `Reconciler` (те же резолверы).
8. `errgroup`: g1 — `Consumer.Run` (fetch → batcher → Send → Pipeline), g2 — `Reconciler.Run`.
9. `startHTTPServer` на `:PORT` (`ReadHeaderTimeout=5s`).
10. На `SIGTERM`/`SIGINT`: отмена контекста → graceful drain окна → `g.Wait()` → `srv.Shutdown` (10s).

## 13. Ключевые типы и сигнатуры

```go
// internal/consumer — интерфейсы для DI/тестов, определены на стороне потребителя
type ChainCaller interface {
    SendCalldata(ctx context.Context, to common.Address, data []byte) (common.Hash, error)
    TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}
type Store interface {
    Exists(ctx, eventID) (bool, error)
    StatusAndTx(ctx, eventID) (status, txHash string, err error)
    InsertPending(ctx, eventID, aggType string) error
    MarkSent(ctx, ids []uuid.UUID, txHash string) error
    MarkCommitted(ctx, ids []uuid.UUID) error
    MarkFailed(ctx, ids []uuid.UUID, txHash, reason string) error
    MarkRejected(ctx, ids []uuid.UUID, txHash string, reason uint8) error
}
type ActionResolver interface {   // chain.WarehouseActions / chain.Registry
    Address() common.Address
    LogOutcomes(receipt *types.Receipt) (map[uuid.UUID]chain.ActionOutcome, error)
    StateOutcome(ctx context.Context, id uuid.UUID) (chain.ActionOutcome, bool, error)
}

// internal/consumer/message.go — распарсенное сообщение (одно из Action/WarehouseAction задано)
type Message struct {
    EventID         uuid.UUID
    AggregateType   string // всегда "signed_action"
    Action          *chain.KeyAction
    WarehouseAction *chain.WarehouseAction
    Topic           string
    KafkaMsg        kafka.Message
}

// internal/consumer/flusher.go — отправленная, но не подтверждённая tx
type InflightTx struct { Aggregate string; TxHash common.Hash; IDs []uuid.UUID; Msgs []*Message }

// internal/chain/keyactions.go
type WebAuthnAuth struct { R, S [32]byte; ChallengeIndex, TypeIndex *big.Int; AuthenticatorData []byte; ClientDataJSON string }
type KeyAction struct { ActionId [16]byte; ActionType uint8; Qx, Qy, SignerKeyId [32]byte; Auth WebAuthnAuth }
type ActionOutcome struct { Applied bool; Reason uint8 }

// internal/chain/warehouseactions.go
type WarehouseAction struct { ActionId [16]byte; ActionType uint8; ItemIds []*big.Int; DetailsHash, KeyId [32]byte; Auth WebAuthnAuth }
```

Sentinel-ошибки: `chain.ErrChainRevert` (терминально → DLQ + MarkFailed), `chain.ErrChainTransient` (retry → Unshift), `chain.ErrSignerTransient` (KMS недоступен → транзиентно), `consumer.ErrConfirmStalled` (stall в Confirm), `consumer.ErrPipelineStalled` (фатально → рестарт процесса).

Конструкторы и методы:

```go
func config.Load() (*Config, error)
func chain.NewClientWithSigner(rpcURL string, s TxSigner) (*Client, error)
func (c *Client) SendCalldata(ctx, to, data) (common.Hash, error)   // единый nonce/gas/sign/send-путь
func (c *Client) ReseedNonce(ctx) error
func chain.WaitReceipt(ctx, fetcher, txHash, timeout) (*types.Receipt, error)
func chain.UUIDToUint256(s string) *big.Int                          // keccak256([]byte(s)) → *big.Int
func chain.PackApplyActions([]WarehouseAction) ([]byte, error)       // ABI SignedMappingWMS.applyActions
func chain.PackApplyKeyActions([]KeyAction) ([]byte, error)          // ABI ConsortiumRegistry.applyKeyActions
func consumer.Parse(m *kafka.Message) (*Message, error)              // только aggregate_type = signed_action
func (f *Flusher) Send(ctx, msgs) ([]InflightTx, error)              // сегменты + чанки + MarkSent
func (f *Flusher) Confirm(ctx, tx *InflightTx) error                 // receipt → applyActionOutcomes
func (r *Reconciler) Run(ctx) error
func store.NewRepository(p *pgxpool.Pool) *Repository
func dlq.NewProducer(brokers []string, topic string) *Producer
```

> `Client.send` держит мьютекс `sendMu` на весь nonce-критичный участок и ведёт локальный монотонный nonce: несколько tx могут уходить не дожидаясь майнинга предыдущих (окно pipeline). RPC-ошибки оборачиваются в `ErrChainTransient`, revert на `EstimateGas` — в `ErrChainRevert`. KMS-режим: EIP-155 sighash считается локально, Web3Signer подписывает готовый digest; недоступность KMS — `ErrSignerTransient` → `ErrChainTransient` (nonce не тратится), 4xx — терминально.

## 14. Гочи и инварианты (сводка)

| Инвариант / гоча | Детали |
|---|---|
| Signer ровно один | Проверяется на старте; порядок действий = порядок nonce = порядок Kafka. |
| Одна партиция `wms.events.v1` | Расширять нельзя — иначе порядок в цепи не гарантирован. |
| Адаптер принимает только `signed_action` | Любой другой `aggregate_type` → DLQ + коммит offset'а. |
| Чанк ≤ 120 000 байт calldata | Жадная упаковка с сохранением порядка; одиночное действие сверх лимита — терминальный фейл. |
| Gas limit = оценка × 3/2 | Запас на разрыв «estimate до записи → майнинг после записи» под окном in-flight. |
| Guard `<> COMMITTED` | В `MarkSent`/`MarkFailed`/`MarkRejected` — защита от reconcile-гонки. |
| Reconcile не переотправляет tx | Только подтягивает статус; `reject_reason IS NULL` исключает перечитывание отклонённых. |
| Reconcile-FAILED не в DLQ | Известный gap, TODO #41. |
| Stall → рестарт процесса | Окно бросается без коммита offsets; redelivery + reconcile восстанавливают. |
| `/health` — liveness-only | TODO #50. |
| `LOG_LEVEL` зарезервирован | Читается, но не подключён к slog. |
| Нет миграций в репо адаптера | `onchain_events` создаётся миграциями WMS. |
| Crash-recovery double-submit | `SendCalldata` успешен, `MarkSent` упал, процесс умер: на redelivery событие в `PENDING` без `tx_hash` переотправляется; контракт пропустит повтор `actionId`, первая tx осиротеет в БД. |

Конвенции проекта (Git workflow, Go style, SQL naming) — см. [Конвенции](../CONVENTIONS.md); правила оформления MR — [Гайд по MR](../MR_GUIDE.md); общая архитектура MVP — [Архитектура](../architecture/system-overview.md).
