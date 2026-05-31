# Ledger Adapter — справочник разработчика

**Версия:** 1.0
**Дата:** 2026-05-31
**Аудитория:** команда, принимающая сервис на сопровождение (hand-off)

Этот документ описывает внутреннее устройство `ledger-adapter` — Go-сервиса-моста между WMS-аутбоксом и смарт-контрактом `BatchMappingWMS`. Архитектурный контекст «зачем блокчейн» и выбор подхода см. в [BatchMappingWMS — подход](../integration/batch-mapping-approach.md); общий маппинг операций — в [Маппинг WMS → Блокчейн](../integration/blockchain-mapping.md); форматы данных на каждом стыке — в [Контракт данных](../integration/data-contract.md). Здесь — устройство кода, типы, сигнатуры, конфигурация и инварианты.

Справочник по самому контракту (FSM, сигнатуры, события, revert-условия) вынесен в отдельный файл: [Smart Contract Reference](smart-contract-reference.md).

---

## 1. Назначение моста

`ledger-adapter` — это **однонаправленный потребитель** Kafka, превращающий доменные события WMS в batch-транзакции к контракту `BatchMappingWMS` на Avalanche Subnet-EVM.

Полный путь события: WMS пишет доменное событие в `public.outbox_events` → Debezium (CDC) публикует его в один Kafka-топик `wms.events.v1` → `ledger-adapter` потребляет топик, накапливает события в окно (по времени/размеру), группирует по стадии и отправляет **одну транзакцию на под-батч** в FSM-порядке (`receiving → putaway → picking → shipping`). Прогресс каждого события фиксируется в `public.onchain_events`, а фоновый reconcile-loop устраняет расхождения БД ↔ chain после таймаутов receipt-поллинга или падения процесса.

> **У адаптера нет бизнес-REST-интерфейса.** Единственный HTTP-эндпоинт — `GET /health` (liveness-проба для оркестратора). WMS не вызывает адаптер по HTTP для бизнес-логики: chain-status gate (`ledger.CheckChainStatus` на стороне WMS) читает `public.onchain_events` напрямую через общий PostgreSQL, а не через адаптер. Не ищите у адаптера API подтверждения статуса — его нет.

```
WMS → outbox_events → Debezium → Kafka (wms.events.v1) → ledger-adapter → BatchMappingWMS
                                                              │
                                                              └──→ public.onchain_events (статус: PENDING/SENT/COMMITTED/FAILED)
```

Соответствие этапов жизненного цикла товара — см. потоковые диаграммы: [Приёмка на столе](../flows/receiving-table-flow.md), [Раскладка](../flows/putaway-flow.md), [Сборка](../flows/assembly-flow.md), [Отгрузка](../flows/shipping-flow.md), а также [Сквозной путь товара](../business-process/end-to-end-flow.md) и [DFD](../business-process/data-flow-diagrams.md).

---

## 2. Структура `internal/`

| Пакет / файл | Ответственность |
|---|---|
| `cmd/adapter/main.go` | Точка входа: собирает все компоненты, поднимает `errgroup` (consumer-goroutine + reconcile-goroutine) и отдельный HTTP-сервер |
| `internal/config/config.go` | `Load()` — чтение env, `_FILE`-override для секретов, валидация обязательных полей и инвариантов на старте |
| `internal/chain/abi.go` | Встраивает `BatchMappingWMS.abi.json` как `ContractABI string` через `//go:embed` |
| `internal/chain/client.go` | `Client` — обёртка над `ethclient`; ABI-паковка; пайплайн nonce/gas/sign/send; диспетчер `BatchCall`; классификация ошибок (revert vs transient) |
| `internal/chain/convert.go` | `UUIDToUint256` — `keccak256(UUID-строка) → *big.Int` (совпадает с Solidity-конвенцией) |
| `internal/chain/receipt.go` | `WaitReceipt` — поллинг receipt'а с экспоненциальным backoff и таймаутом |
| `internal/consumer/message.go` | Тип `Message` и `Parse()` — извлечение `EventID`/`ProductID`/`AggregateType` из Kafka-заголовков и ключа |
| `internal/consumer/batcher.go` | `Batcher` — потокобезопасный аккумулятор; флаш по размеру/времени; `Unshift` для retry |
| `internal/consumer/consumer.go` | `Consumer` — fetch-loop `kafka.Reader` → `Parse` → `Batcher` → `Flusher`; коммит offset'ов только после успешного флаша |
| `internal/consumer/flusher.go` | `Flusher` — группировка по `AggregateType`, FSM-порядок, фильтр идемпотентности, вызов контракта, выставление статусов в БД |
| `internal/consumer/reconcile.go` | `Reconciler` — фоновая сверка stuck-строк `SENT`/`FAILED`; общий примитив `reconcileReceipt` |
| `internal/store/pool.go` | `NewPool` — `pgxpool` (`MaxConns=10`, `MinConns=1`, `MaxConnIdleTime=30s`, startup-ping 10s) |
| `internal/store/onchain_events.go` | `Repository` — весь SQL по `public.onchain_events`; тип `ReconcileRow` |
| `internal/dlq/producer.go` | `Producer` — `kafka.Writer` (acks `RequireAll`) в DLQ-топик; `PublishAll` сохраняет исходный payload и добавляет метаданные |
| `internal/handler/handler.go` | `Handler` — только `/health`, отдаёт `{"status":"ok","time":"..."}` 200 |

Принцип «многих маленьких файлов»: каждый пакет имеет узкую ответственность, интерфейсы определены на стороне потребителя (`consumer`), а конкретные реализации (`chain.Client`, `store.Repository`, `dlq.Producer`) их удовлетворяют.

---

## 3. Потребление Kafka (consumer group)

**Потребляемый топик** — единственный, захардкожен в `cmd/adapter/main.go`:

```go
const eventsTopic = "wms.events.v1"
```

> **Один топик, не топик-на-стадию.** В ранней версии было по топику на `aggregate_type`. Текущий код (рефактор мая 2026) использует один топик `wms.events.v1`; стадия передаётся в Kafka-заголовке `aggregate_type`, а не выводится из имени топика. README-таблица с четырьмя топиками (`wms.receiving.v1` и т.п.) отражает прошлый дизайн.

**Consumer group** — env `KAFKA_GROUP_ID`, по умолчанию `ledger-adapter`.

**Конфигурация `kafka.ReaderConfig`** (`internal/consumer/consumer.go`):

| Параметр | Значение |
|---|---|
| `MinBytes` | `1` |
| `MaxBytes` | `10e6` (10 MB) |
| `MaxWait` | `100ms` |

**Формат сообщения** (выставляется Debezium Outbox EventRouter):

| Поле | Источник | Назначение |
|---|---|---|
| Key | `aggregate_id` (UUID товара) | `Message.ProductID` |
| Header `id` | `event_id` (UUID события) | `Message.EventID` |
| Header `aggregate_type` | `receiving` / `putaway` / `picking` / `shipping` | `Message.AggregateType` |
| Value | сырой outbox-payload | непрозрачен для адаптера; сохраняется для republish в DLQ |

**Цикл обработки** (`Consumer.Run`):

1. Goroutine `fetchLoop` читает сообщения из `kafka.Reader` в канал `msgCh`.
2. Основной `select` обрабатывает: отмену контекста, ошибки чтения, новые сообщения и тикер 50ms (для флаша по таймауту).
3. Каждое сообщение проходит `Parse`; ошибка парсинга → публикация в DLQ (offset всё равно коммитится, чтобы не зациклиться на «ядовитом» сообщении).
4. Валидное сообщение кладётся в `Batcher`.
5. По `ShouldFlush()` (размер ≥ `BATCH_SIZE` или прошло `BATCH_TIMEOUT`) вызывается `flush`.
6. `flush`: `Batcher.Drain()` → `Flusher.Flush()` → коммит Kafka-offset'ов. При ошибке флаша — `Batcher.Unshift()` возвращает сообщения обратно, **offset'ы не коммитятся** (at-least-once).
7. При завершении (ctx cancel) — `finalFlush` с фоновым контекстом на 5 секунд.

> Consumer работает **в одной goroutine**. Последовательная обработка батчей — необходимое условие для FSM-упорядочивания (см. §4). Параллельные consumer-goroutine сломали бы гарантии порядка.

---

## 4. Batch-flusher

`Flusher` (`internal/consumer/flusher.go`) — сердце пайплайна. Он принимает дренированный батч смешанных по стадии сообщений и выполняет их в строгом FSM-порядке.

### Окно и размер батча

| Параметр | Env | По умолчанию | Смысл |
|---|---|---|---|
| Размер | `BATCH_SIZE` | `10` | макс. сообщений в одном флаше (и, как следствие, в одной транзакции под-батча) |
| Окно | `BATCH_TIMEOUT` | `100ms` | макс. время накопления до флаша по таймауту |

`Batcher.ShouldFlush()` возвращает `true`, если `len(msgs) >= maxSize` **или** `time.Since(firstAt) >= timeout`.

### FSM-порядок и группировка

`Flusher.Flush` группирует сообщения по `AggregateType`, затем обрабатывает под-батчи в фиксированном порядке `fsmOrder`:

```go
fsmOrder = []string{"receiving", "putaway", "picking", "shipping"}
```

Сообщения с неизвестным `aggregate_type` уходят в DLQ. Если под-батч падает, `Flush` возвращает ошибку **без коммита offset'ов**; на retry уже-`COMMITTED` события отфильтровываются в `filterAndMarkPending`.

> FSM-порядок гарантируется **внутри окна флаша**, а не глобально. События разных окон упорядочены Kafka-offset'ами (consumer одногоупен). В рамках одного смешанного батча `receiving` всегда обрабатывается раньше `putaway` и т.д.

### Соответствие стадии и функции контракта

`BatchCall` диспетчеризует по карте `aggregateToMethod` (`internal/chain/client.go`):

| `AggregateType` | Solidity-метод | On-chain переход |
|---|---|---|
| `receiving` | `batchAccept(uint256[] eventIds, uint256[] itemIds)` | `None → Accepted` |
| `putaway` | `batchPutAway(uint256[] eventIds, uint256[] itemIds)` | `Accepted → PutAway` |
| `picking` | `batchPick(uint256[] eventIds, uint256[] itemIds)` | `PutAway → Picked` |
| `shipping` | `batchShip(uint256[] eventIds, uint256[] itemIds)` | `Picked → Shipped` |

Точные сигнатуры, события и условия revert контракта — см. [Smart Contract Reference](smart-contract-reference.md).

### Пайплайн `flushSubBatch`

Для каждого под-батча (`Flusher.flushSubBatch`):

1. `filterAndMarkPending` — фильтр идемпотентности (см. §5), вставка новых строк со статусом `PENDING`.
2. `buildBatchArgs` — конвертация `event_id`/`product_id` (UUID) → `uint256` через `UUIDToUint256`.
3. `chain.BatchCall` — отправка транзакции в контракт.
4. `store.MarkSent` — `tx_hash` + статус `SENT`.
5. `chain.WaitReceipt` — блокирующий поллинг receipt'а с таймаутом `RECEIPT_POLL_TIMEOUT`.
6. `store.MarkCommitted` (`receipt.Status == 1`) **или** `recordFailure` (`receipt.Status == 0` либо таймаут).

### Поллинг receipt'а

`chain.WaitReceipt` создаёт дочерний контекст с таймаутом и поллит с экспоненциальным backoff: `50ms → 100ms → 200ms → 500ms → 1s → 2s` (далее держит 2s). `ethereum.NotFound` → продолжаем поллинг; **любая другая RPC-ошибка → немедленный возврат** (без повторов). Кратковременный сбой RPC во время поллинга приводит к таймауту и `recordFailure`; восстановление — через reconcile-loop.

### Обработка отказа

`recordFailure` сначала публикует в DLQ, затем вызывает `MarkFailed`. Порядок «DLQ → MarkFailed» намеренный: если бы `MarkFailed` шёл первым и затем падал DLQ, FAILED-строка была бы пропущена `filterAndMarkPending` на retry, а её DLQ-запись так и не появилась бы (тихая потеря). Обратный порядок ценой возможного дубля в DLQ исключает потерю.

---

## 5. Идемпотентность (`event_id` / `processedEventIds`)

Идемпотентность реализована на **трёх уровнях**:

| Уровень | Механизм | Где |
|---|---|---|
| Внутри батча | дедуп по `event_id` через `seen`-map | `filterAndMarkPending` |
| В БД | `INSERT ... ON CONFLICT (event_id) DO NOTHING` | `InsertPending` + `UNIQUE (event_id)` |
| On-chain | `mapping(uint256 => bool) processedEventIds` | контракт `BatchMappingWMS` |

**Логика `filterAndMarkPending`** для каждого сообщения (с предварительным intra-batch дедупом по `event_id`):

| Состояние в БД | Действие |
|---|---|
| Нет в БД | `InsertPending` + добавить в pending |
| Статус `COMMITTED` | skip (уже подтверждено) |
| Есть `tx_hash` (любой статус) | `reconcileReceipt` — однократная проверка receipt'а, **без переотправки** |
| Статус `FAILED`, `tx_hash` пуст | skip (терминально, транзакция никогда не отправлялась) |
| Статус `PENDING`, `tx_hash` пуст | warning + переотправка (добавить в pending) |

> **On-chain идемпотентность.** Контракт хранит `eventId → bool`. Если адаптер отправит тот же `eventId` дважды (например, после падения между `BatchCall` и `MarkSent`), контракт молча пропустит дубликат. См. известный gap «crash-recovery double-submit» в §10.

---

## 6. Reconcile-loop

`Reconciler` (`internal/consumer/reconcile.go`) — фоновая read-mostly сверка, которая подтягивает БД к on-chain-истине. **Никогда не переотправляет транзакции.**

`Reconciler.Run` — тикер с периодом `RECONCILE_INTERVAL`; каждый тик вызывает `reconcileOnce`, логирует ошибки, но не падает на транзиентных сбоях.

`reconcileOnce`:

1. `ListReconcilable(minAge)` — выбирает строки `status IN ('SENT','FAILED')` с непустым `tx_hash`, старше `RECONCILE_MIN_AGE`, `ORDER BY updated_at ASC LIMIT 1000`.
2. Группирует строки по `tx_hash` (одна проверка receipt'а на batch-транзакцию).
3. Для каждой транзакции вызывает `reconcileReceipt`.

**`reconcileReceipt`** (общий примитив, без поллинга — одна проверка):

| Результат | Действие | terminal |
|---|---|---|
| `ethereum.NotFound` | строка остаётся как есть | `false` |
| `receipt.Status == 1` | `MarkCommitted` | `true` |
| `receipt.Status == 0` | `MarkFailed` (reason: `reconcile: tx reverted on-chain`) | `true` |

> **Reconcile-FAILED НЕ публикуется в DLQ** (в отличие от синхронного `recordFailure`). Это известный gap (TODO #41). Приемлемо, поскольку валидные переходы контракта после фикса больше не ревертятся; полная видимость планируется через reverse-outbox (#41) / chain-status gate (#45).

---

## 7. Хранилище `onchain_events`

Таблица `public.onchain_events` **создаётся миграциями WMS** (`wms/migrations/0001_init.up.sql`), не адаптером — у адаптера нет своего раннера миграций, он предполагает, что схема существует. Подробная схема БД — см. [Схема БД](../db/Database_ru_v2.md) и [ER-диаграмма](../data-model/er-diagram.md).

| Колонка | Тип | Примечание |
|---|---|---|
| `id` | `bigserial` | PRIMARY KEY |
| `event_id` | `uuid` | `UNIQUE` (`onchain_events_event_id_unique`) |
| `aggregate_type` | `text` | `receiving` / `putaway` / `picking` / `shipping` |
| `tx_hash` | `text` | `NULL` до `MarkSent` |
| `status` | `public.onchain_event_status` | ENUM, по умолчанию `PENDING` |
| `error_message` | `text` | заполняется `MarkFailed` |
| `created_at` | `timestamptz` | `DEFAULT now()` |
| `updated_at` | `timestamptz` | обновляется триггером `trg_onchain_events_set_updated_at` |

**ENUM** `public.onchain_event_status`: `PENDING`, `SENT`, `COMMITTED`, `FAILED`.

**Индексы:** `idx_onchain_events_status` на `(status)`, `idx_onchain_events_created_at` на `(created_at)`.

**Статусная машина строки:**

```
PENDING ──MarkSent──► SENT ──receipt.Status=1──► COMMITTED  (терминально)
                            │
                            └──receipt.Status=0 / timeout / revert──► FAILED  (терминально)
```

**SQL-операции репозитория** (`store.Repository`):

| Метод | SQL | Защита |
|---|---|---|
| `Exists` | `SELECT 1 ... WHERE event_id=$1` | — |
| `InsertPending` | `INSERT ... ON CONFLICT (event_id) DO NOTHING` | идемпотентно |
| `StatusAndTx` | `SELECT status::text, tx_hash WHERE event_id=$1` | — |
| `MarkSent` | `UPDATE ... SET tx_hash=$2, status='SENT' WHERE event_id=ANY($1) AND status <> 'COMMITTED'` | guard `<> COMMITTED` |
| `MarkCommitted` | `UPDATE ... SET status='COMMITTED' WHERE event_id=ANY($1)` | forward-only безопасно |
| `MarkFailed` | `UPDATE ... SET status='FAILED', tx_hash=COALESCE(NULLIF($2,''), tx_hash), error_message=$3 WHERE event_id=ANY($1) AND status <> 'COMMITTED'` | guard `<> COMMITTED` |
| `ListReconcilable` | `SELECT event_id, tx_hash WHERE status IN ('SENT','FAILED') AND tx_hash IS NOT NULL AND tx_hash <> '' AND updated_at < now() - make_interval(secs => $1) ORDER BY updated_at ASC LIMIT 1000` | `LIMIT 1000` |

> **Guard `status <> 'COMMITTED'` в `MarkSent`/`MarkFailed`** критичен: если reconcile-loop пометил строку `COMMITTED`, а запоздавший `WaitReceipt`-таймаут вызовет `MarkFailed` на ту же строку — без guard'а `COMMITTED` был бы перезатёрт на `FAILED`. Guard делает такой `UPDATE` no-op.

---

## 8. DLQ

`dlq.Producer` (`internal/dlq/producer.go`) — `kafka.Writer` с acks `RequireAll` и балансировщиком `kafka.Hash`. Топик — env `DLQ_TOPIC`, по умолчанию `wms.dlq.v1`.

`PublishAll(ctx, msgs []*Message, reason string)` для каждого сообщения формирует `kafka.Message`, копируя исходные key/value/headers и добавляя метаданные:

| Заголовок | Значение |
|---|---|
| `original_topic` | исходный топик сообщения |
| `reason` | причина отправки в DLQ |
| `failed_at` | RFC3339 UTC timestamp |

**Что попадает в DLQ:**

- Сообщения с ошибкой парсинга (`Parse` вернул ошибку) и неизвестным `aggregate_type`.
- Под-батчи, чья синхронная отправка завершилась отказом (`recordFailure`: `receipt.Status=0`, таймаут, `ErrChainRevert`).

**Что НЕ попадает в DLQ:** строки, помеченные `FAILED` фоновым reconcile-loop (см. §6, TODO #41).

---

## 9. REST-эндпоинты адаптера

Единственный эндпоинт. Бизнес-REST-поверхности нет.

### `GET /health`

| Свойство | Значение |
|---|---|
| Путь | `/health` (точное совпадение `http.ServeMux`) |
| Аутентификация | нет |
| Тело запроса | нет |
| Обработчик | `handler.Handler.Health` |

**Успешный ответ** — `200 OK`, `Content-Type: application/json`:

```json
{"status":"ok","time":"2026-05-31T04:55:00Z"}
```

(`time` — RFC3339 UTC; тело собирается inline как `map[string]any`, Go-структуры нет.)

**Ошибки:** не определены. Обработчик всегда пишет 200 (процесс жив = healthy); ошибка записи тела молча игнорируется.

**Вызывающая сторона:** `wms/internal/ledger/client.go` — `Client.HealthCheck()` (GET с таймаутом 5s, ожидает 200).

> **Это liveness-проба, не readiness.** Возвращает 200, пока жив Go-процесс, независимо от доступности Kafka, PostgreSQL или RPC. Добавление readiness-пробы (проверка pool/Kafka/RPC) отслеживается в `TODO(#50)`. E2E-набор компенсирует это отдельным probe'ом готовности consumer'а.

---

## 10. Конфигурация (env-переменные)

Все переменные читаются `config.Load()` (`internal/config/config.go`). Для пяти обязательных секретов поддерживается `<NAME>_FILE`-override: если `<NAME>_FILE` задан и файл читается, используется его trim'нутое содержимое; если файл не найден — warning в stderr и fallback на прямой env.

| Переменная | Обяз. | По умолчанию | Тип | Описание |
|---|:---:|---|---|---|
| `KAFKA_BROKERS` | да | — | `string` | CSV-список брокеров. Override: `KAFKA_BROKERS_FILE` |
| `DB_URL` | да | — | `string` | pgx DSN. Override: `DB_URL_FILE` |
| `RPC_URL` | да | — | `string` | RPC Avalanche Subnet-EVM. Override: `RPC_URL_FILE` |
| `CONTRACT_ADDR` | да | — | `string` | hex-адрес `BatchMappingWMS`. Override: `CONTRACT_ADDR_FILE` |
| `PRIVATE_KEY` | да | — | `string` | hex ECDSA-ключ (с/без `0x`). Override: `PRIVATE_KEY_FILE` |
| `PORT` | нет | `8085` | `string` | HTTP-порт `/health` |
| `KAFKA_GROUP_ID` | нет | `ledger-adapter` | `string` | Kafka consumer group |
| `DLQ_TOPIC` | нет | `wms.dlq.v1` | `string` | Kafka DLQ-топик |
| `BATCH_SIZE` | нет | `10` | `int` | макс. событий на транзакцию; невалидный int → default + warning |
| `BATCH_TIMEOUT` | нет | `100ms` | `time.Duration` | макс. окно накопления; невалидный → default + warning |
| `RECEIPT_POLL_TIMEOUT` | нет | `30s` | `time.Duration` | макс. ожидание `WaitReceipt` |
| `RECONCILE_INTERVAL` | нет | `30s` | `time.Duration` | период reconcile-sweep'а; должен быть `> 0` |
| `RECONCILE_MIN_AGE` | нет | `1m` | `time.Duration` | мин. возраст stuck-строки для reconcile; должен быть `> RECEIPT_POLL_TIMEOUT` |
| `LOG_LEVEL` | нет | `info` | `string` | **зарезервировано**: читается в `Config.LogLevel`, но пока не подключено к slog (`main.go` создаёт `slog.NewJSONHandler(os.Stdout, nil)` без level-фильтра) |

**Инварианты, проверяемые на старте** (`Load()` возвращает ошибку и прерывает запуск):

- `RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT` — иначе reconcile-loop мог бы гоняться с in-flight `WaitReceipt` флашера и перезаписать статус строки. Значения по умолчанию (`1m > 30s`) удовлетворяют инварианту.
- `RECONCILE_INTERVAL > 0` — иначе `time.NewTicker` в reconcile-loop паникует. `time.ParseDuration` принимает `0s`/`-5s` без ошибки, поэтому проверка явная.

> **Hardcoded, не настраивается через env:** конфигурация пула (`MaxConns=10`, `MinConns=1`, `MaxConnIdleTime=30s`, startup-ping 10s) и `kafka.ReaderConfig` (`MinBytes=1`, `MaxBytes=10MB`, `MaxWait=100ms`).

---

## 11. Точка входа `cmd/adapter/main.go`

Порядок сборки в `run()`:

1. `config.Load()` — валидация env и инвариантов.
2. `store.NewPool(ctx, cfg.DbURL)` — `pgxpool`, startup-ping (10s).
3. `store.NewRepository(pool)`.
4. `chain.NewClient(cfg.RpcURL, cfg.PrivateKey, cfg.ContractAddr)` — dial RPC, парсинг ключа (`0x` опционален), парсинг ABI; логирует адрес signer'а.
5. `dlq.NewProducer(brokers, cfg.DLQTopic)`.
6. `consumer.NewFlusher(cli, repo, prod, cfg.ReceiptPollTimeout, log)`.
7. `consumer.NewReconciler(cli, repo, cfg.ReconcileInterval, cfg.ReconcileMinAge, log)`.
8. `errgroup.WithContext(rootCtx)` — две goroutine:
   - **g1 (Consumer):** `consumer.NewConsumer(brokers, "wms.events.v1", cfg.KafkaGroupID, flusher, prod, cfg.BatchSize, cfg.BatchTimeout, tlog).Run(gCtx)` — одна goroutine, один топик.
   - **g2 (Reconciler):** `reconciler.Run(gCtx)`.
9. `startHealthServer(log, cfg.Port)` — `http.Server` на `:8085`, `ReadHeaderTimeout=5s`.
10. На `SIGTERM`/`SIGINT`: отмена контекста → Consumer `finalFlush` (5s) → `g.Wait()` → `srv.Shutdown` (10s).

---

## 12. Ключевые типы и сигнатуры

### Интерфейсы (определены на стороне `consumer`, для DI/тестов)

```go
// internal/consumer/flusher.go
type ChainCaller interface {
    BatchCall(ctx context.Context, aggregateType string, eventIDs, itemIDs []*big.Int) (common.Hash, error)
    TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}

type Store interface {
    Exists(ctx context.Context, eventID uuid.UUID) (bool, error)
    StatusAndTx(ctx context.Context, eventID uuid.UUID) (status, txHash string, err error)
    InsertPending(ctx context.Context, eventID uuid.UUID, aggType string) error
    MarkSent(ctx context.Context, ids []uuid.UUID, txHash string) error
    MarkCommitted(ctx context.Context, ids []uuid.UUID) error
    MarkFailed(ctx context.Context, ids []uuid.UUID, txHash, reason string) error
}

type DLQPublisher interface {
    PublishAll(ctx context.Context, msgs []*Message, reason string) error
}

// internal/chain/receipt.go
type ReceiptFetcher interface {
    TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}
```

`*chain.Client` удовлетворяет `ChainCaller`/`ReceiptFetcher`; `*store.Repository` — `Store`; `*dlq.Producer` — `DLQPublisher`.

### Структуры

```go
// internal/consumer/message.go — внутренний тип пайплайна, не JSON-DTO
type Message struct {
    EventID       uuid.UUID
    ProductID     uuid.UUID
    AggregateType string        // "receiving"/"putaway"/"picking"/"shipping"
    Topic         string
    KafkaMsg      kafka.Message
}

// internal/store/onchain_events.go
type ReconcileRow struct {
    EventID uuid.UUID
    TxHash  string
}

// internal/config/config.go
type Config struct {
    Port               string
    KafkaBrokers       string
    KafkaGroupID       string
    DbURL              string
    RpcURL             string
    ContractAddr       string
    PrivateKey         string
    BatchSize          int
    BatchTimeout       time.Duration
    DLQTopic           string
    ReceiptPollTimeout time.Duration
    ReconcileInterval  time.Duration
    ReconcileMinAge    time.Duration
    LogLevel           string
}
```

### Sentinel-ошибки (`internal/chain/client.go`)

```go
var ErrChainRevert    = errors.New("chain revert")    // терминально → DLQ + MarkFailed
var ErrChainTransient = errors.New("chain transient") // retry → Batcher.Unshift, offset'ы не коммитятся
```

### Ключевые конструкторы и методы

```go
func config.Load() (*Config, error)

func chain.NewClient(rpcURL, privateKeyHex, contractAddr string) (*Client, error)
func (c *Client) BatchCall(ctx context.Context, aggregateType string, eventIDs, itemIDs []*big.Int) (common.Hash, error)
func chain.WaitReceipt(ctx context.Context, fetcher ReceiptFetcher, txHash common.Hash, timeout time.Duration) (*types.Receipt, error)
func chain.UUIDToUint256(s string) *big.Int   // keccak256([]byte(s)) → *big.Int

func consumer.Parse(m *kafka.Message) (*Message, error)
func (f *Flusher) Flush(ctx context.Context, msgs []*Message) error
func (r *Reconciler) Run(ctx context.Context) error

func store.NewRepository(p *pgxpool.Pool) *Repository
func (r *Repository) ListReconcilable(ctx context.Context, minAge time.Duration) ([]ReconcileRow, error)

func dlq.NewProducer(brokers []string, topic string) *Producer
func (p *Producer) PublishAll(ctx context.Context, msgs []*Message, reason string) error
```

> `Client.sendMethod` держит мьютекс `sendMu` на весь nonce-критичный участок (`PendingNonceAt → SuggestGasPrice → EstimateGas → ChainID → NewTransaction → SignTx → SendTransaction`), сериализуя исходящие транзакции одного `Client`. В продакшене (один consumer-goroutine) мьютекс не контендится; защищает от будущей параллелизации. RPC-ошибки оборачиваются в `ErrChainTransient`, revert на `EstimateGas` — в `ErrChainRevert`.

---

## 13. Гочи и инварианты (сводка)

| Инвариант / гоча | Детали |
|---|---|
| `RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT` | Проверяется на старте. Нарушение → reconcile-loop гоняется с in-flight `WaitReceipt`. |
| Один топик `wms.events.v1` | Стадия — в заголовке `aggregate_type`, не в имени топика. |
| Один consumer-goroutine | Последовательная обработка — условие FSM-упорядочивания. |
| Guard `<> COMMITTED` | В `MarkSent`/`MarkFailed` — защита от reconcile-гонки. |
| Reconcile-FAILED не в DLQ | Известный gap, TODO #41. |
| `WaitReceipt` не повторяет на не-`NotFound` ошибках | Кратковременный RPC-сбой → таймаут → `recordFailure`; восстановление через reconcile. |
| Crash-recovery double-submit | Если `BatchCall` успешен, но `MarkSent` упал и процесс умер: на рестарте событие в `PENDING` без `tx_hash`, `filterAndMarkPending` переотправляет. Первая tx — осиротевшая (БД её не трекает). On-chain `processedEventIds` (#44) предотвращает двойной переход, но осиротевшая tx не отслеживается. См. `ledger_adapter_bugs.md`. |
| `/health` — liveness-only | TODO #50. |
| `LOG_LEVEL` зарезервирован | Читается, но не подключён к slog. |
| Нет миграций в репо адаптера | `onchain_events` создаётся миграциями WMS. |

Конвенции проекта (Git workflow, Go style, SQL naming) — см. [Конвенции](../CONVENTIONS.md); правила оформления MR — [Гайд по MR](../MR_GUIDE.md); общая архитектура MVP — [Архитектура](../architecture/system-overview.md).
