# Операции, сборка и развёртывание — runbook разработчика

**Назначение:** практический справочник по локальному запуску, инфраструктуре `docker-compose`, CDC-конвейеру, мониторингу, блокчейн-стеку и тестам. Документ ориентирован на разработчика, который принимает проект и должен поднять стенд с нуля, понять, как устроена доставка событий, и уметь прогнать тесты.

**Связанные документы** (читать вместе с этим runbook, не дублируются здесь):

- Архитектура и контекст: [system-overview](../architecture/system-overview.md)
- Сквозной процесс и потоки данных: [end-to-end-flow](../business-process/end-to-end-flow.md), [data-flow-diagrams](../business-process/data-flow-diagrams.md)
- Поэтапные флоу: [receiving-gate](../flows/receiving-gate-flow.md), [receiving-table](../flows/receiving-table-flow.md), [putaway](../flows/putaway-flow.md), [assembly](../flows/assembly-flow.md), [shipping](../flows/shipping-flow.md)
- Модель данных: [entity-lifecycle](../data-model/entity-lifecycle.md), [er-diagram](../data-model/er-diagram.md), [Database_ru_v2](../db/Database_ru_v2.md)
- Интеграция с блокчейном: [blockchain-mapping](../integration/blockchain-mapping.md), [data-contract](../integration/data-contract.md), [batch-mapping-approach](../integration/batch-mapping-approach.md)
- Справочник по модулям/эндпоинтам WMS: [wms-reference](./wms-reference.md)
- Конвенции и процесс ревью: [CONVENTIONS](../CONVENTIONS.md), [MR_GUIDE](../MR_GUIDE.md)
- Локальный Avalanche-стек: [deploy/subnet/README.md](../../deploy/subnet/README.md)
- E2E-тесты: [tests/e2e/README.md](../../tests/e2e/README.md)

---

## 1. Локальный запуск

### 1.1. Требования

| Инструмент | Версия | Зачем |
|-----------|--------|-------|
| Docker + Docker Compose | **v2.22+** (Docker Desktop 4.26+) | `depends_on.required: false` (Compose Spec 1.20) используется для опционального `contract-deploy` |
| Go | **1.25+** (ledger-adapter требует 1.25; WMS собирается под 1.24.5) | сборка и тесты `wms`/`ledger-adapter`, e2e-сьют (testcontainers) |
| `golangci-lint` | `v1.64.8` | линт (см. `.golangci.yml`) |
| `make` | любой | удобные таргеты |
| `pre-commit` | любой | локальные хуки (`make hooks-install`) |

> **macOS / Apple Silicon (arm64):** `subnet-node1` собирается и работает **нативно** под текущую архитектуру (плагин subnet-evm тянется под `TARGETARCH`) — эмуляции QEMU больше нет, старт заметно быстрее. На первый bootstrap всё равно заложен запас (`start_period: 30s` + 30 ретраев), пока узел поднимется и `subnet-init` создаст цепочку.

### 1.2. Файл `.env`

Compose читает переменные из `.env` (есть `.env.example`). Создайте свой `.env`:

```bash
cp .env.example .env
```

**ВАЖНО — перед первым запуском обязательно замените `JWT_SECRET`.** Значение по умолчанию из `.env.example` (`replace-with-a-random-64-character-secret`) **само находится в блок-листе** и будет отвергнуто валидацией `config.Load()`. Кроме того, в `docker-compose.yaml` стоит `${JWT_SECRET:?...}` — стек **не стартует**, если переменная пустая. Требования к секрету: после `trim` непустой, не из блок-листа дефолтов, длина **≥ 32 символов**.

```bash
# пример генерации
openssl rand -hex 32
```

Ключевые переменные `.env` (полные таблицы — в разделе 4):

| Переменная | Назначение | Замечание |
|-----------|-----------|-----------|
| `JWT_SECRET` | секрет подписи JWT (HS256) | **жёстко обязателен**, ≥32 симв., не из блок-листа |
| `DB_USER` / `DB_PASSWORD` / `DB_NAME` | доступ к PostgreSQL | по умолчанию `root` / `root` / `wms_blockchain_db` |
| `RPC_URL` | RPC EVM-узла для ledger-adapter | в профиле `test` перетирается файлом из shared-тома |
| `PRIVATE_KEY` | приватный ключ для подписи он-чейн транзакций | в профиле `test` перетирается файлом из shared-тома |
| `CONTRACT_ADDR` | адрес задеплоенного `BatchMappingWMS` | в профиле `test` перетирается файлом из shared-тома |

> В профиле `test` значения `RPC_URL`/`PRIVATE_KEY`/`CONTRACT_ADDR` подставляются автоматически из shared-тома `shared_state` (`*_FILE`-переменные ledger-adapter имеют приоритет над прямыми env). Их ручная установка нужна только для запуска против внешнего узла.

### 1.3. Поднятие стенда

Есть два режима. **Различайте их — это частый источник путаницы.**

**Режим A — только приложение (без блокчейна):**

```bash
make up                 # = docker compose up -d
```

Поднимает app-tier + инфраструктуру + мониторинг, **но НЕ поднимает** `subnet-node1`, `subnet-init` и `contract-deploy` — они под `profile: test`. `ledger-adapter` запустится (его `depends_on` на `contract-deploy` помечен `required: false`), но без живого RPC/контракта он-чейн часть работать не будет.

**Режим B — полный стек с локальным блокчейном (для реального end-to-end):**

```bash
docker compose --profile test up -d
```

Дополнительно поднимает узел `subnet-node1` (Avalanche Subnet-EVM), one-shot `subnet-init` (создаёт сабнет + цепочку через Go wallet SDK и пишет динамический `rpc_url.txt`) и one-shot `contract-deploy`, который деплоит `BatchMappingWMS` и пишет `rpc_url.txt` / `contract_addr.txt` / `deployer_key.txt` в том `shared_state`. Подробности — [deploy/subnet/README.md](../../deploy/subnet/README.md).

**Обязательный шаг после старта — регистрация Debezium-коннектора.** Коннектор **не регистрируется автоматически** (нет init-скрипта/`depends_on`, у Debezium нет healthcheck). Без него CDC-конвейер мёртв:

```bash
# Дождитесь готовности Debezium (Connect REST на :8083), затем:
make register-connector       # POST deploy/debezium/connectors/postgres-connector.json
make connector-status         # проверка статуса outbox-connector
```

Полная последовательность «с нуля» (режим B):

```bash
cp .env.example .env
# отредактируйте JWT_SECRET!
docker compose --profile test up -d
make ps                        # дождаться healthy у postgres/kafka/subnet-node1
make register-connector        # после готовности debezium
make connector-status
```

### 1.4. Проверка состояния

```bash
make ps                        # = docker compose ps -a
make logs s=wms-service        # хвост логов конкретного сервиса
curl -s localhost:8081/health  # health WMS (см. раздел 10)
curl -s localhost:8085/health  # health ledger-adapter
```

### 1.5. Доступные сервисы и порты (host → назначение)

| Host-порт | Сервис | Назначение |
|-----------|--------|-----------|
| `8081` | `wms_app` (`wms-service`) | WMS HTTP REST API (внутри контейнера `8080`) |
| `8085` | `ledger-adapter` | HTTP (только `/health`) |
| `8080` | `kafka-ui` | Kafbat UI |
| `8083` | `debezium` | Kafka Connect REST API |
| `9090` | `prometheus` | Prometheus UI |
| `3000` | `grafana` | Grafana (admin/admin) |
| `5432` | `postgres` (`postgres_db`) | PostgreSQL |
| `9092` | `kafka` | Kafka PLAINTEXT-брокер |
| `9999` | `kafka` | Kafka JMX |
| `9012` | `debezium` | Debezium Connect JMX |
| `9187` | `postgres-exporter` | метрики Postgres |
| `127.0.0.1:9650` | `subnet-node1` (`profile: test`) | Avalanche Subnet-EVM RPC, динамический путь `/ext/bc/<blockchainID>/rpc` (только loopback) |
| `127.0.0.1:9651` | `subnet-node1` (`profile: test`) | Avalanche staking (только loopback) |

> **Порты `8080`/`8081` легко перепутать:** `8080` — это `kafka-ui`, а WMS API наружу — на `8081`.
>
> **Kafka недоступна с хоста по имени `kafka`.** Advertised-listener — `PLAINTEXT://kafka:9092`, поэтому host-клиент через `localhost:9092` получит адрес `kafka:9092` и не подключится без записи в `/etc/hosts`. Это нормально для чисто-compose-режима.
>
> `kafka-jmx-exporter` и `debezium-jmx-exporter` слушают порт `5556` **без host-маппинга** — они видны только внутри `app_network`, Prometheus скрейпит их по имени контейнера.

---

## 2. Инфраструктура `docker-compose`

Один bridge-network `app_network`; именованные тома `postgres_data`, `kafka_data`, `subnet_data`, `shared_state`.

| Сервис | Образ / сборка | Порты (host:container) | depends_on | healthcheck |
|--------|----------------|------------------------|-----------|-------------|
| `wms_app` | build `./wms/Dockerfile` | `8081:8080` | `postgres` (healthy), `ledger-adapter` (started), `db-init` (completed), `kafka-init` (completed) | `curl -f localhost:8080/health` (10s/5s/5 ретраев) |
| `postgres` | `postgres:17` | `5432:5432` | — | `pg_isready -U <user> -d <db>` (5s/3s/10) |
| `db-init` | `postgres:17` (one-shot) | — | `postgres` (healthy) | — |
| `kafka` | `apache/kafka:3.7.0` (KRaft) | `9092`, `9999` | — | `kafka-broker-api-versions.sh` (сбрасывает `KAFKA_OPTS`/`JMX_PORT`) |
| `kafka-init` | `apache/kafka:3.7.0` (one-shot) | — | `kafka` (healthy), `db-init` (completed) | — |
| `debezium` | `debezium/connect:2.5` | `8083`, `9012` | `db-init` (completed), `kafka-init` (completed) | **нет** (Compose не ждёт готовности) |
| `kafka-ui` | `ghcr.io/kafbat/kafka-ui:v1.5.0` | `8080:8080` | `kafka` (started) | — |
| `prometheus` | `prom/prometheus:latest` | `9090:9090` | — | — |
| `grafana` | `grafana/grafana:latest` | `3000:3000` | `prometheus` | — |
| `postgres-exporter` | `prometheuscommunity/postgres-exporter` | `9187:9187` | `postgres` (healthy), `db-init` (completed) | — |
| `kafka-jmx-exporter` | `sscaling/jmx-prometheus-exporter` | `5556` (без host-маппинга) | — | — |
| `debezium-jmx-exporter` | `sscaling/jmx-prometheus-exporter` | `5556` (без host-маппинга) | — | — |
| `ledger-adapter` | build `./ledger-adapter/Dockerfile` | `8085:8085` | `db-init` (completed), `kafka-init` (completed), `contract-deploy` (completed, `required: false`) | **нет** (distroless — нет `curl`/`wget`/`nc`) |
| `subnet-node1` *(test)* | build `./deploy/subnet/subnet-node` (avalanchego v1.14.0 + плагин subnet-evm v0.8.0, нативная арх.) | `127.0.0.1:9650`, `127.0.0.1:9651` | — | `curl /ext/health` + grep `"healthy": true` (10s/30 ретраев/start 30s) |
| `subnet-init` *(test)* | build `./deploy/subnet/subnet-init` (Go wallet SDK, one-shot) | — | `subnet-node1` (healthy) | — |
| `contract-deploy` *(test)* | build `./deploy/contracts/Dockerfile` (foundry:stable, one-shot) | — | `subnet-init` (completed) | — |

Существенные детали:

- **`postgres`** запускается командой `postgres -c wal_level=logical` — логическая репликация обязательна для Debezium. Монтирует `seed.sql` read-only.
- **`db-init`** (скрипт `deploy/db-init.sh`) делает 5 шагов: миграции (`wms/migrations/migrate.sh`) → создание роли репликации `debezium` → grant `SELECT` на `public.outbox_events` → создание publication `outbox_publication FOR TABLE public.outbox_events` → seed admin-пользователя; опционально `seed.sql` если `SEED_DATA != false`. Слот репликации `debezium_outbox` создаётся самим Debezium при первом старте коннектора, а не `db-init`.
- **`postgres-exporter`** обращается к БД по имени контейнера `postgres_db`, а не по имени сервиса `postgres`.
- **`ledger-adapter`** монтирует `shared_state:/shared:ro` и читает `rpc_url.txt`/`contract_addr.txt`/`deployer_key.txt`, записанные `contract-deploy`. Healthcheck отсутствует — downstream используют `condition: service_started`.
- **`contract-deploy`** идемпотентен: если `/shared/contract_addr.txt` содержит адрес с ненулевым байткодом (`cast code` длиннее 4 символов) — выходит `0` без передеплоя; если файл есть, но байткод пустой — передеплоивает; если `cast code` падает с RPC-ошибкой — завершается с exit `1` (не передеплоивает «вслепую»). Полный сброс блокчейн-состояния: `docker compose --profile test down -v`.

---

## 3. CDC-конвейер (Debezium → Kafka)

### 3.1. Что слушает коннектор

Файл: `deploy/debezium/connectors/postgres-connector.json`, имя коннектора `outbox-connector`.

| Параметр | Значение |
|----------|----------|
| `connector.class` | `io.debezium.connector.postgresql.PostgresConnector` |
| `table.include.list` | **`public.outbox_events`** (только эта таблица) |
| `plugin.name` | `pgoutput` |
| `slot.name` | `debezium_outbox` |
| `publication.name` | `outbox_publication` |
| `snapshot.mode` | `never` |
| `topic.prefix` | `wms` |
| `database.user/password/dbname` | `${env:DB_USER}` / `${env:DB_PASSWORD}` / `${env:DB_NAME}` (через `EnvVarConfigProvider`, `env_file: .env`) |
| SMT `transforms` | `outbox` → `io.debezium.transforms.outbox.EventRouter` |

EventRouter:

| Поле EventRouter | Значение в `outbox_events` |
|------------------|----------------------------|
| `event.id` | `event_id` |
| `event.key` | `aggregate_id` |
| `event.type` | `event_type` |
| `event.payload` | `payload_hash` |
| `route.by.field` | `aggregate_type` |
| `route.topic.replacement` | **`wms.events.v1`** (статично — все события на один топик) |
| доп. | `aggregate_type` кладётся в **Kafka-header** `aggregate_type` |

### 3.2. Маршрутизация — два слоя (важно!)

**Слой 1 — фактическая runtime-маршрутизация (актуальная).** EventRouter имеет статичную замену `route.topic.replacement = wms.events.v1`. То есть **все** outbox-события, независимо от `aggregate_type`, попадают в **единственный топик `wms.events.v1`**. Тип агрегата едет в Kafka-header `aggregate_type`. `ledger-adapter` потребляет только `wms.events.v1` (топик захардкожен в `main.go`) и диспетчеризует по header'у на Solidity-метод:

| `aggregate_type` (header, lower-case) | Solidity-метод |
|---------------------------------------|----------------|
| `receiving` | `batchAccept` |
| `putaway` | `batchPutAway` |
| `picking` | `batchPick` |
| `shipping` | `batchShip` |

> Литералы `aggregate_type` — строчные и для этапа сборки это **`picking`**, а не `assembly`. Это значения, которые WMS реально пишет в `outbox_events` (см. [wms-reference](./wms-reference.md)).

**Слой 2 — устаревшая per-aggregate схема (legacy / rollback).** `kafka-init` создаёт ещё 4 per-aggregate топика, соответствующих значениям колонки `event_type` в `outbox_events`:

| `event_type` в `outbox_events` | Legacy-топик |
|--------------------------------|--------------|
| `wms.receiving.v1` | `wms.receiving.v1` |
| `wms.putaway.v1` | `wms.putaway.v1` |
| `wms.picking.v1` | `wms.picking.v1` |
| `wms.shipping.v1` | `wms.shipping.v1` |

**Эти 4 топика создаются, но коннектором НЕ наполняются** (EventRouter маршрутизирует всё в `wms.events.v1`). Они оставлены «на откат» и трафика не получают.

> **Расхождение с другими доками.** В [index.md](../index.md) встречается формулировка «4 топика + DLQ». Это описание прежнего дизайна. Актуально: один рабочий топик `wms.events.v1` + DLQ; четыре per-aggregate топика — legacy.

### 3.3. Все топики, создаваемые `kafka-init`

`deploy/kafka-init.sh` создаёт **7 топиков**, все с `--partitions 1 --replication-factor 1`:

| Топик | Статус |
|-------|--------|
| `__debezium-heartbeat.wms` | служебный heartbeat Debezium |
| **`wms.events.v1`** | **рабочий** — все outbox-события; **должен оставаться 1 партиция** (FSM-порядок зависит от single-partition гарантии Kafka) |
| `wms.receiving.v1` | legacy / rollback |
| `wms.putaway.v1` | legacy / rollback |
| `wms.picking.v1` | legacy / rollback |
| `wms.shipping.v1` | legacy / rollback |
| `wms.dlq.v1` | **DLQ** ledger-adapter (failed/непарсящиеся сообщения) |

> **Инвариант:** `wms.events.v1` нельзя расширять до >1 партиции — корректность FSM ledger-adapter держится на порядке внутри одной партиции.

Внутренние storage-топики Debezium Connect: `debezium_configs`, `debezium_offsets`, `debezium_statuses`; внутренний DLQ Connect — `debezium_dlq`.

---

## 4. Переменные окружения

### 4.1. WMS (`wms/internal/config/config.go`)

| Переменная | Поле `Config` | Тип | Default | Обязательна | Валидация |
|-----------|---------------|-----|---------|-------------|-----------|
| `PORT` | `Port` | string | `8080` | нет | — |
| `DB_HOST` | `DBHost` | string | `localhost` | нет | — |
| `DB_PORT` | `DBPort` | string | `5432` | нет | — |
| `POSTGRES_USER` | `DBUser` | string | `root` | нет | — |
| `POSTGRES_PASSWORD` | `DBPassword` | string | `root` | нет | — |
| `POSTGRES_DB` | `DBName` | string | `wms_blockchain_db` | нет | — |
| `KAFKA_BROKER` | `KafkaBroker` | string | `localhost:9092` | нет | — |
| `LEDGER_ADAPTER_URL` | `LedgerAdapterURL` | string | `""` | нет (но фактически нужна в рантайме) | не валидируется; пустое значение принимается, упадёт в рантайме |
| `JWT_SECRET` | `JWTSecret` | string | — | **да** (`Load()` вернёт ошибку) | непустой после trim; не из блок-листа; ≥32 симв. |
| `JWT_ACCESS_TTL` | `JWTAccessTTL` | duration | `15m` | нет | `time.ParseDuration`; при ошибке — warning + default (без падения) |
| `JWT_REFRESH_TTL` | `JWTRefreshTTL` | duration | `168h` | нет | то же |

Блок-лист `JWT_SECRET` (отвергаются с ошибкой): `change-me`, `dev-secret`, `replace-me`, `replace-with-a-random-32-byte-secret`, `replace-with-a-random-64-character-secret`.

> В контейнере `wms_app` Compose дополнительно прокидывает `DB_HOST=postgres`, `KAFKA_BROKER=kafka:9092`, `LEDGER_ADAPTER_URL=http://ledger-adapter:8085`.

### 4.2. ledger-adapter (`ledger-adapter/internal/config/config.go`)

Для пяти обязательных переменных поддерживается оверрайд через `<NAME>_FILE`: если файл существует, берётся его trim-содержимое (так профиль `test` подставляет значения из `shared_state`).

| Переменная | Обязательна | Default | Тип | Описание |
|-----------|-------------|---------|-----|----------|
| `KAFKA_BROKERS` | **да** | — | string | CSV брокеров, напр. `kafka:9092`. Есть `KAFKA_BROKERS_FILE` |
| `DB_URL` | **да** | — | string | pgx DSN. Есть `DB_URL_FILE` |
| `RPC_URL` | **да** | — | string | RPC EVM-узла. Есть `RPC_URL_FILE` (`/shared/rpc_url.txt`) |
| `CONTRACT_ADDR` | **да** | — | string | hex-адрес `BatchMappingWMS`. Есть `CONTRACT_ADDR_FILE` (`/shared/contract_addr.txt`) |
| `PRIVATE_KEY` | **да** | — | string | hex ECDSA-ключ (`0x` срезается). Есть `PRIVATE_KEY_FILE` (`/shared/deployer_key.txt`) |
| `PORT` | нет | `8085` | string | HTTP-порт `/health` |
| `KAFKA_GROUP_ID` | нет | `ledger-adapter` | string | consumer group |
| `DLQ_TOPIC` | нет | `wms.dlq.v1` | string | топик для failed/непарсящихся |
| `LOG_LEVEL` | нет | `info` | string | зарезервирован, пока не подключён к slog |
| `BATCH_SIZE` | нет | `10` | int | макс. событий на одну он-чейн транзакцию |
| `BATCH_TIMEOUT` | нет | `100ms` | duration | макс. время накопления батча |
| `RECEIPT_POLL_TIMEOUT` | нет | `30s` | duration | таймаут поллинга receipt'а |
| `RECONCILE_INTERVAL` | нет | `30s` | duration | период фоновой сверки. Должен быть `> 0` |
| `RECONCILE_MIN_AGE` | нет | `1m` | duration | мин. возраст «зависшей» SENT/FAILED-строки. Должен быть `> RECEIPT_POLL_TIMEOUT` |

> **Стартовые инварианты** (`Load()` падает при нарушении): `RECONCILE_MIN_AGE > RECEIPT_POLL_TIMEOUT` (иначе reconciler гоняется с in-flight `WaitReceipt`) и `RECONCILE_INTERVAL > 0`.

### 4.3. Init / прочее (Compose)

| Переменная | Default | Где |
|-----------|---------|-----|
| `ADMIN_PASSWORD` | `admin` | `db-init` — пароль начального admin'а |
| `DEBEZIUM_PASSWORD` | `debezium` | `db-init` — пароль роли репликации |
| `SEED_DATA` | `true` (нет в `.env.example`) | `db-init` — `false` отключает dev-seed |

---

## 5. База данных

PostgreSQL 17, три схемы. Подробный справочник по колонкам, ENUM'ам, индексам, триггерам и views — в [Database_ru_v2](../db/Database_ru_v2.md); модули и эндпоинты, которые их пишут, — в [wms-reference](./wms-reference.md). Здесь — краткая карта.

| Схема | Назначение | Основные таблицы |
|-------|-----------|------------------|
| `public` | сквозное: пользователи, auth, трекинг outbox/onchain | `users`, `evm_addresses`, `outbox_events`, `onchain_events`, `schema_migrations` |
| `wms_inventory` | доменные сущности | `skus`, `sku_barcodes`, `warehouses`, `bins`, `destinations`, `inbound_shipments`, `cargoplaces`, `boxes`, `expected_cargoplace_skus`, `orders`, `order_lines`, `outbound_dispatches`, `products` |
| `wms_ops` | append-only история операций | `receiving_gate`, `receiving_table`, `putaways`, `assembly_tasks`, `shippings` + views `v_putaways_with_chain` / `v_shippings_with_chain` / `v_assembly_tasks_with_chain` |

Ключевые точки для операций:

- `public.outbox_events` — источник CDC (append-only, без `status`/`processed`; Debezium читает WAL). `event_id` — ключ идемпотентности.
- `public.onchain_events` — статус он-чейн (`PENDING`/`SENT`/`COMMITTED`/`FAILED`), пишется ledger-adapter'ом; матчится с `outbox_events` по `event_id`.
- Views `v_*_with_chain` (миграция 0008) делают `LEFT JOIN` ops-таблиц к `public.onchain_events`, отдавая `chain_status` через `COALESCE(..., 'PENDING')`.

### 5.1. Миграции

Собственный раннер `wms/migrations/migrate.sh` (не golang-migrate/goose). Версии трекаются в `public.schema_migrations`. Раннер сам определяет режим — Docker (`docker exec` в контейнер `postgres_db`) или локальный `psql` — и применяет `*.up.sql` в порядке сортировки, пропуская применённые версии.

```bash
make migrate            # docker compose run --rm db-init bash /wms/migrations/migrate.sh
make init               # полный init: db-init (миграции+seed) + kafka-init
make seed               # переналить dev-данные (postgres должен быть запущен)
```

> **Нюанс нумерации:** версии `0002`–`0004` отсутствуют (последовательность 0001 → 0005). Раннер пропускает их без ошибок. Миграции `0009` и `0011` используют `CREATE INDEX CONCURRENTLY` и потому **не обёрнуты в транзакцию**.

> **`seed.sql` не миграция** и не трекается в `schema_migrations`; идемпотентен (`ON CONFLICT DO NOTHING`). Сеет `operator`/`operator` (OPERATOR) и `customer`/`customer` (CUSTOMER). Admin (`admin`/`admin`, из `ADMIN_PASSWORD`) создаётся `db-init.sh`, **не** `seed.sql`. Отдельного ADMIN-seed в SQL/миграциях нет.

---

## 6. Мониторинг

### 6.1. Prometheus

Config `deploy/prometheus.yml`, `scrape_interval: 10s`. URL: `http://localhost:9090`.

| Job | Target | Источник |
|-----|--------|----------|
| `postgres` | `postgres-exporter:9187` | контейнер postgres-exporter |
| `kafka` | `kafka-jmx-exporter:5556` | JMX-экспортёр скрейпит `kafka:9999` |
| `debezium` | `debezium-jmx-exporter:5556` | JMX-экспортёр скрейпит `debezium-connect:9012` |

### 6.2. JMX-экспортёры

| Экспортёр | Config | Скрейпит | Отдаёт метрику |
|-----------|--------|----------|----------------|
| `kafka-jmx-exporter` | `deploy/jmx/kafka-jmx-config.yaml` | `kafka:9999` | `kafka_under_replicated_partitions` (GAUGE) |
| `debezium-jmx-exporter` | `deploy/jmx/debezium-jmx-config.yaml` | `debezium-connect:9012` | `debezium_running_task_count` (GAUGE) |

Конфиги намеренно минимальны — каждый экспортёр отдаёт по одной целевой метрике.

### 6.3. Grafana

URL: `http://localhost:3000`, креды `admin` / `admin`.

> **Преднастроенных дашбордов и datasource нет.** Источник данных (Prometheus) и дашборды нужно добавлять вручную. Из кастомных метрик доступны только две: `kafka_under_replicated_partitions` и `debezium_running_task_count` (плюс стандартный вывод postgres-exporter).

---

## 7. Avalanche subnet (локальный блокчейн)

Профиль `test` поднимает single-node `avalanchego` v1.14.0 (`subnet-node1`) с плагином subnet-evm, через one-shot `subnet-init` создаёт кастомный сабнет + цепочку (Go wallet SDK, локальная подпись ключом ewoq) и деплоит `BatchMappingWMS.sol` на эту цепочку **Subnet-EVM** (chainID `99999`, динамический RPC-путь `/ext/bc/<blockchainID>/rpc`). `blockchainID` детерминируется на bootstrap'е и пишется в `shared_state` (`rpc_url.txt`). Полная документация, обоснование «Subnet-EVM вместо встроенного C-Chain», prefunded-ключ ewoq и троублшутинг — в [deploy/subnet/README.md](../../deploy/subnet/README.md).

Быстрый старт:

```bash
# только блокчейн-часть (узел + bootstrap сабнета + деплой контракта)
docker compose --profile test up -d subnet-node1 subnet-init contract-deploy

# проверка артефактов деплоя
docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/rpc_url.txt
docker run --rm -v blockchain_project_shared_state:/s alpine cat /s/contract_addr.txt

# весь стек сразу
docker compose --profile test up -d

# полный сброс блокчейн-состояния
docker compose --profile test down -v
```

`contract-deploy` идемпотентен: проверяет `/shared/contract_addr.txt`, при живом контракте — не передеплоит. Деплой: `forge create BatchMappingWMS` ключом ewoq, проверка `itemStatus(0)==0`, запись `rpc_url.txt` / `contract_addr.txt` / `deployer_key.txt` в `shared_state`.

Маппинг WMS→контракт и формат данных — [blockchain-mapping](../integration/blockchain-mapping.md), [data-contract](../integration/data-contract.md), [batch-mapping-approach](../integration/batch-mapping-approach.md).

---

## 8. Тесты

### 8.1. Unit-тесты

```bash
make test               # = test-wms + test-ledger
make test-wms           # cd wms && go test -race -count=1 ./...
make test-ledger        # cd ledger-adapter && go test -race -count=1 ./...
```

`-race` обязателен; `-count=1` отключает кеш результатов. В CI те же команды (см. раздел 9).

### 8.2. E2E-тесты

Go-сьют гоняет **полный outbound-флоу через реальный WMS HTTP API** и проверяет он-чейн зеркалирование:
`WMS API → public.outbox_events → Debezium CDC → Kafka (wms.events.v1) → ledger-adapter → Avalanche Subnet-EVM`.
Build-tag `//go:build e2e`. Детали, перечень покрытия и env-тогглы — в [tests/e2e/README.md](../../tests/e2e/README.md).

```bash
# изолированный стенд: поднять только сервисы под тестом, прогнать, снести
make e2e-test-outbound
# = cd tests/e2e && go test -tags=e2e -count=1 -timeout=15m ./...
# RPC_URL не задаём: сьют, поднимая стек, подтягивает реальные RPC_URL/CONTRACT_ADDR из
# тома shared_state (динамический /ext/bc/<blockchainID>/rpc). Задавать вручную нужно
# только для прогона против внешнего узла — и тогда путь динамический.

# против уже поднятого стека (быстрее итерации) — RPC_URL/CONTRACT_ADDR сьют берёт
# из тома shared_state автоматически; задавать вручную нужно только для внешнего узла,
# и тогда путь — динамический /ext/bc/<blockchainID>/rpc
cd tests/e2e
E2E_USE_EXISTING_STACK=true go test -tags=e2e -count=1 -v ./...
```

Тогглы (`testmain_test.go`): `E2E_USE_EXISTING_STACK`, `E2E_KEEP_STACK`, `E2E_SKIP_STACK_RESET`, `E2E_SKIP_TESTMAIN`.

**Нюансы, на которых легко споткнуться (из практики проекта):**

- **Деструктивный reset.** Перед прогоном сьют по умолчанию делает `down -v` своего стека. Сьют использует **изолированный compose-project `blockchain_project_e2e`**, поэтому он не трогает ваш dev-стек `blockchain_project`. Чтобы пропустить пре-ран reset — `E2E_SKIP_STACK_RESET=true`.
- **Коллизия по fixed-name контейнеру.** Если остался осиротевший контейнер с фиксированным именем (напр. `ledger-adapter`), старт падает с `container name "/ledger-adapter" already in use`. Лечится `docker compose down -v --remove-orphans` перед перезапуском (падает быстро, ~1.4s — выглядит как баг кода, но это инфраструктура).
- **Кеш образов.** testcontainers переиспользует кешированные образы `wms_app`/`ledger-adapter`. После правок кода **пересоберите образы** (`docker compose build` или `make build`), иначе протестируете старый код.
- **macOS/arm64:** `subnet-node1` работает нативно (без эмуляции QEMU) — старт быстрее прежнего C-Chain-узла, но на первый bootstrap (поднятие узла + создание сабнета/цепочки в `subnet-init`) всё равно закладывайте запас.

---

## 9. Makefile и CI

### 9.1. Makefile (основные таргеты)

| Таргет | Что делает |
|--------|-----------|
| `up` / `down` / `build` | `docker compose up -d` / `down` / `up -d --build` |
| `ps` | `docker compose ps -a` |
| `logs s=<service>` | хвост логов сервиса |
| `lint` / `lint-wms` / `lint-ledger` | `golangci-lint run --config ../.golangci.yml ./...` |
| `test` / `test-wms` / `test-ledger` | `go test -race -count=1 ./...` |
| `tidy` / `vendor` | `go mod tidy` / `go mod vendor` (per-module) |
| `init` | `db-init` + `kafka-init` (миграции + seed + топики) |
| `migrate` | только миграции |
| `seed` | переналить dev-данные |
| `e2e-test-outbound` | полный outbound e2e (нужен локальный Avalanche) |
| `register-connector` / `connector-status` / `delete-connector` | управление Debezium-коннектором через Connect REST (`:8083`) |
| `hooks-install` / `hooks-run` | pre-commit |

> Makefile подключает `.env` (`include .env; export`), если файл существует.

### 9.2. CI-пайплайн (GitLab)

Root `.gitlab-ci.yml`, стадии `lint → test → build_&_deploy`, includes `ci/go.yml`, `ci/wms.yml`, `ci/ledger_adapter.yml`. Все джобы триггерятся по `changes:`-путям, `tags: [docker]`. Go-кеш по `${CI_COMMIT_REF_SLUG}-go`.

| Джоб | Стадия | Образ | Триггер (changes) |
|------|--------|-------|-------------------|
| `wms_lint` | lint | `golangci/golangci-lint:v1.64.8-alpine` | `wms/**/*` |
| `wms_test` | test | `golang:1.24` | `wms/**/*` |
| `wms_build_&_deploy` | build_&_deploy | `docker:24.0.5` (`docker build`, без push) | `wms/**/*` |
| `ledger_adapter_lint` | lint | `golang:1.25` (ставит golangci-lint `v1.64.8`) | `ledger-adapter/**/*` |
| `ledger_adapter_test` | test | `golang:1.25` | `ledger-adapter/**/*` |
| `ledger_adapter_build_&_deploy` | build_&_deploy | `docker:24.0.5` (push в `$CI_REGISTRY`) | `ledger-adapter/**/*` |

Test-джобы: `go test -race -count=1 ./...`, retry ≤2 на runner/stuck/scheduler, `needs: []`.

> **Замечания:** (1) `ci/frontend.yml` существует, но **не подключён** в `.gitlab-ci.yml` — фронтенд в CI не линтится. (2) WMS собирается под `golang:1.24`, ledger-adapter — под `golang:1.25`; общий `.golangci.yml` указывает `go: "1.24"`. (3) Если пайплайн «красный» на стадии `get_sources` — обычно это инфраструктура раннера (не может склонировать), а не код.

Соглашения по веткам/коммитам/MR — [CONVENTIONS](../CONVENTIONS.md) и [MR_GUIDE](../MR_GUIDE.md). Pre-push хук блокирует push в `main`/`master`/`develop`/`dev` (обход — `ALLOW_PROTECTED_PUSH=1`) и требует имя ветки вида `^(feature|fix|hotfix|docs|refactor|chore|test)/...`. Commit-msg хук валидирует Conventional Commits.

---

## 10. Health-checks

### 10.1. WMS — `GET /health` (host `8081`)

Возвращает **200** если все проверки `"ok"`, **503** если хотя бы одна провалена (`status: "degraded"`). Проверки выполняются синхронно на каждый запрос: `dbPool.Ping`, `kafkaConn.Brokers()`, `ledgerClient.HealthCheck()`. Ключи `kafka`/`ledger_adapter` присутствуют, только если соответствующий клиент сконфигурирован. **Не** обёрнут в стандартный envelope.

```json
// 200 OK
{
  "status": "ok",
  "checks": { "postgres": "ok", "kafka": "ok", "ledger_adapter": "ok" },
  "time": "2026-05-31T04:55:00Z"
}
```

```json
// 503 Service Unavailable — пример с упавшим Postgres
{
  "status": "degraded",
  "checks": { "postgres": "<error string>", "kafka": "ok", "ledger_adapter": "ok" },
  "time": "2026-05-31T04:55:00Z"
}
```

### 10.2. ledger-adapter — `GET /health` (host `8085`)

> **Асимметрия с WMS:** это **liveness-only** проба. Адаптер **всегда** отвечает **200** (процесс жив = healthy) и **никогда не отдаёт 503**. Проба не проверяет Kafka-lag, доступность БД или RPC. Readiness-проба — открытый `TODO(#50)`.

```json
// 200 OK (всегда)
{ "status": "ok", "time": "2026-05-31T04:55:00Z" }
```

`wms/internal/ledger/client.go` (`Client.HealthCheck()`) считает адаптер живым **только** при HTTP 200; тело не читается. Это единственный HTTP-вызов WMS→адаптер — вся бизнес-интеграция идёт через outbox/БД, не через HTTP.
