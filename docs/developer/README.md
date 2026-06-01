# Документация разработчика

**Проект:** WMS-монолит (Go) + Ledger Adapter (Go) + Avalanche Subnet-EVM
**Назначение документа:** единая точка входа для команды, принимающей проект. Здесь — обзор системы, архитектурная схема, карта всей документации, технологический стек, ключевые принципы и маршрут чтения.

> Это «верхний» индекс пакета «Документация разработчика». Он не дублирует содержимое детальных документов, а связывает их и даёт контекст. Все конкретные эндпоинты, поля таблиц, FSM-переходы и flow-диаграммы описаны в документах, на которые ведут ссылки ниже.

---

## Назначение системы

WMS (Warehouse Management System) управляет жизненным циклом товара на складе через **5 этапов**:

**Приёмка на КПП → Приёмка на столе → Раскладка → Сборка → Отгрузка**

Каждая физическая операция (сканирование TTN, грузоместа, QR-кода товара, ячейки) фиксируется в PostgreSQL. Ключевые этапы дополнительно записываются в блокчейн как **неизменяемая аудит-полоса**: цепочка `outbox_events → Debezium → Kafka → Ledger Adapter → смарт-контракт BatchMappingWMS`.

Блокчейн используется **не как хранилище данных**, а как независимый верификатор конечного автомата (FSM): он подтверждает, что товар прошёл этапы в правильном порядке, в зафиксированное время, заданным актором. Вся «тяжёлая» бизнес-логика и данные остаются в WMS и Postgres; on-chain пишется минимум — `eventId`, `itemId` (хэши UUID) и переход статуса.

---

## Архитектурная схема

Прямой путь — от оператора до блокчейна. Обратный путь — статус on-chain записывается в `onchain_events` и читается WMS как «шлюз» (chain-status gate), блокирующий продвижение FSM при отклонённом on-chain событии.

```mermaid
flowchart LR
  subgraph CLIENT["Frontend"]
    FE["React SPA / Scanner UI"]
  end

  subgraph WMS_APP["WMS-монолит (Go, net/http + gorilla/mux)"]
    API["HTTP API / Router"]
    MOD["Модули: receiving, putaway,\nassembly, shipping, dispatches, destinations, auth"]
    GATE["CheckChainStatus\n(chain-status gate)"]
  end

  subgraph DB["PostgreSQL 17"]
    PG[("wms_inventory · wms_ops · public\n+ outbox_events\n+ onchain_events")]
  end

  subgraph CDC["CDC + Очереди"]
    DBZ["Debezium\n(EventRouter SMT)"]
    K["Kafka topic\nwms.events.v1\n(1 партиция)"]
    DLQ["Kafka\nwms.dlq.v1"]
  end

  subgraph LAD_APP["Ledger Adapter (Go)"]
    CONS["Kafka Consumer"]
    FLUSH["Flusher (FSM-порядок,\nбатчинг по aggregate_type)"]
    CHAIN["chain.Client\n(go-ethereum)"]
  end

  subgraph CH["Avalanche C-Chain (local) / Subnet-EVM (future)"]
    SC["BatchMappingWMS\nFSM: None→Accepted→PutAway→Picked→Shipped"]
  end

  FE -->|HTTP/JSON| API
  API --> MOD
  MOD <-->|SQL| PG
  MOD -->|INSERT outbox_events\n(в одной транзакции)| PG
  PG -->|logical replication| DBZ
  DBZ -->|route all events,\naggregate_type = header| K
  K -->|consume| CONS
  CONS --> FLUSH
  FLUSH --> CHAIN
  CHAIN -->|batchAccept / batchPutAway /\nbatchPick / batchShip| SC

  %% Обратный путь
  CHAIN -.->|tx receipt| FLUSH
  FLUSH -.->|UPDATE status:\nPENDING→SENT→COMMITTED/FAILED| PG
  PG -.->|onchain_events.status = FAILED| GATE
  GATE -.->|блокирует переход FSM\nCHAIN_EVENT_REJECTED| MOD
  CONS -.->|poison / unknown| DLQ
```

**Как читать схему:**

- **Прямой путь (сплошные стрелки):** WMS пишет бизнес-данные и строку `outbox_events` одной транзакцией. Debezium через `EventRouter` маршрутизирует **все** события в единственный топик `wms.events.v1`, проставляя `aggregate_type` в Kafka-заголовок. Ledger Adapter группирует сообщения по `aggregate_type`, соблюдает FSM-порядок и вызывает соответствующую `batch*`-функцию контракта.
- **Обратный путь (пунктир):** адаптер обновляет статус события в `onchain_events` (`PENDING → SENT → COMMITTED/FAILED`) по квитанции транзакции. WMS **читает** `onchain_events` через `CheckChainStatus` и блокирует продвижение FSM, если предыдущий этап получил on-chain статус `FAILED`. Это не слушатель on-chain-событий, а проверка статуса в Postgres.

> Детальные диаграммы потоков и последовательностей: [DFD](../business-process/data-flow-diagrams.md), [сквозной путь товара](../business-process/end-to-end-flow.md), [маппинг в блокчейн](../integration/blockchain-mapping.md).

---

## Карта документации

### API / интерфейсы

| Документ | Описание |
|----------|----------|
| [openapi.yaml](../api/openapi.yaml) | Машиночитаемая OpenAPI-спецификация всех эндпоинтов WMS. Источник истины для API. |
| [swagger-ui.html](../api/swagger-ui.html) | Интерактивный просмотр спецификации (Swagger UI), открывается в браузере. |
| [api-contract.md](../api/api-contract.md) | Человекочитаемый контракт: request/response, ошибки, побочные эффекты. Сгенерирован из `openapi.yaml`. |

### Код-референс

| Документ | Описание |
|----------|----------|
| [wms-reference.md](wms-reference.md) | Референс WMS-монолита: пакеты, модули (receiving / putaway / assembly / shipping / dispatches / destinations / auth), сервисы, репозитории, запись outbox. |
| [ledger-adapter-reference.md](ledger-adapter-reference.md) | Референс Ledger Adapter: consumer, Flusher, `chain.Client`, конвертация UUID→uint256, reconcile-loop, DLQ. |
| [smart-contract-reference.md](smart-contract-reference.md) | Референс `BatchMappingWMS`: FSM, batch- и single-функции, идемпотентность, события `ItemTransition` / `ItemTransitionFailed`. |
| [operations.md](operations.md) | Эксплуатация: docker-compose, переменные окружения, инициализация БД/Kafka, регистрация Debezium-коннектора, мониторинг, CI. |

### Архитектура и бизнес-процессы

| Документ | Описание |
|----------|----------|
| [architecture/system-overview.md](../architecture/system-overview.md) | Обзор архитектуры MVP: компоненты, роли, Kafka, Debezium, блокчейн-слой, безопасность. |
| [business-process/end-to-end-flow.md](../business-process/end-to-end-flow.md) | Сквозной путь товара от ворот до отгрузки. **Начни отсюда.** |
| [business-process/data-flow-diagrams.md](../business-process/data-flow-diagrams.md) | Диаграммы потоков данных (DFD): Level 0 → Level 1 → Level 2. |
| [flows/receiving-gate-flow.md](../flows/receiving-gate-flow.md) | Этап «Приёмка на КПП»: сканирование TTN и грузомест (без outbox). |
| [flows/receiving-table-flow.md](../flows/receiving-table-flow.md) | Этап «Приёмка на столе»: вскрытие грузоместа, создание `products`, outbox `receiving`. |
| [flows/putaway-flow.md](../flows/putaway-flow.md) | Этап «Раскладка»: из буфера в ячейки хранения, outbox `putaway`. |
| [flows/assembly-flow.md](../flows/assembly-flow.md) | Этап «Сборка»: аллокация и подбор товаров, outbox `picking`. |
| [flows/shipping-flow.md](../flows/shipping-flow.md) | Этап «Отгрузка»: верификация и отправка заказа, outbox `shipping`. |
| [data-model/entity-lifecycle.md](../data-model/entity-lifecycle.md) | Жизненные циклы сущностей: все статусы и переходы в одном месте. |
| [data-model/er-diagram.md](../data-model/er-diagram.md) | ER-диаграмма: связи между таблицами, уникальные ограничения. |
| [db/Database_ru_v2.md](../db/Database_ru_v2.md) | Подробная схема БД: все поля всех таблиц с описаниями (схемы `public`, `wms_inventory`, `wms_ops`). |
| [integration/blockchain-mapping.md](../integration/blockchain-mapping.md) | Маппинг WMS → блокчейн: какая операция что пишет, полный путь события. |
| [integration/data-contract.md](../integration/data-contract.md) | Контракт данных: формат outbox, Kafka, Ledger Adapter, `onchain_events`. |
| [integration/batch-mapping-approach.md](../integration/batch-mapping-approach.md) | Подход `BatchMappingWMS`: FSM, batch-функции, производительность. |
| [CONVENTIONS.md](../CONVENTIONS.md) | Конвенции проекта: git workflow, Go style, SQL naming, Docker. |
| [MR_GUIDE.md](../MR_GUIDE.md) | Правила оформления merge requests. |

---

## Технологический стек

| Слой | Технология | Примечание |
|------|-----------|-----------|
| Frontend | React (SPA), сканер-интерфейсы | RU-локализованные экраны, JWT-авторизация |
| WMS-монолит | Go, `net/http` + `gorilla/mux` | Без префикса `/api/v1`; маршруты вида `/receiving/...`, `/auth/login`, `/health` |
| Авторизация | JWT (HS256), bcrypt | Роли `ADMIN` / `OPERATOR` / `CUSTOMER`; access 15m, refresh 168h |
| БД | PostgreSQL 17 | 3 схемы: `public`, `wms_inventory`, `wms_ops`; `wal_level=logical` для CDC |
| Драйвер БД | `pgx` / `pgxpool` | Пул: MinConns=2, MaxConns=10 (захардкожено) |
| CDC | Debezium (`debezium/connect:2.5`) | `EventRouter` SMT, плагин `pgoutput`, `publication = outbox_publication` |
| Очереди | Apache Kafka 3.7 (KRaft) | Прямой топик `wms.events.v1` (1 партиция), DLQ `wms.dlq.v1` |
| Мост в блокчейн | Ledger Adapter (Go) | Kafka consumer + Flusher + reconcile-loop |
| EVM-клиент | `go-ethereum` (`ethclient`, `abi`) | Подпись tx, батчинг, ожидание квитанций |
| Блокчейн | Avalanche C-Chain (EVM) | Локально — C-Chain узел `avalanchego` (`network-id=local`, профиль `test`); пермиссионные Allow Lists ещё не реализованы — см. `deploy/avalanche/README.md` |
| Контракт | `BatchMappingWMS` (Solidity `^0.8.0`, solc 0.8.23) | Foundry, EVM target `paris`, optimizer 200 runs |
| Инфраструктура | Docker Compose | WMS, ledger-adapter, postgres, kafka, debezium, мониторинг |
| Мониторинг | Prometheus + Grafana + exporters | postgres-exporter, JMX-экспортеры Kafka/Debezium |
| CI | GitLab CI | Стадии `lint → test → build_&_deploy`, golangci-lint, `go test -race` |

---

## Ключевые архитектурные принципы

1. **Outbox pattern.** WMS пишет только в PostgreSQL. Бизнес-запись и строка `outbox_events` создаются **в одной транзакции** — нет двойной записи в БД и Kafka. Debezium подхватывает `outbox_events` и публикует в Kafka. При недоступности Debezium/Kafka событие уже надёжно лежит в БД.

2. **1 outbox event = 1 product.** `aggregate_id` в `outbox_events` всегда равен `product_id`. `aggregate_type` (`receiving` / `putaway` / `picking` / `shipping`) определяет, какой `batch*`-метод контракта будет вызван. Каждый товар на каждом этапе порождает ровно одно событие.

3. **Блокчейн = верификатор FSM, не хранилище.** Контракт `BatchMappingWMS` реализует строгий конечный автомат `None → Accepted → PutAway → Picked → Shipped`. Он не хранит бизнес-данные — только текущий статус `itemId` и факт обработки `eventId`. Batch-функции пропускают элемент с неверным статусом, эмитя `ItemTransitionFailed` (без revert всей транзакции).

4. **Идемпотентность на каждом уровне.** `event_id` уникален в `outbox_events`, отслеживается в `onchain_events`, и проверяется on-chain через `processedEventIds`. Повторная обработка одного события не создаёт дубликатов и не двигает FSM повторно.

5. **КПП не пишет в блокчейн.** На этапе «Приёмка на КПП» сущности `products` ещё не существуют — товары создаются только на этапе «Приёмка на столе» (`scan-qr`). Поэтому первый outbox-event (`receiving`) появляется при закрытии грузоместа на столе, а не на воротах.

> Дополнительно: chain-status gate (`CheckChainStatus`) блокирует продвижение FSM, если предыдущий этап получил on-chain статус `FAILED` (`CHAIN_EVENT_REJECTED`). Это связывает off-chain FSM с on-chain верификатором.

---

## С чего начать новой команде

Рекомендуемый маршрут чтения — от бизнес-обзора к коду:

1. **[Сквозной путь товара](../business-process/end-to-end-flow.md)** — как система работает глазами оператора, от ворот до отгрузки. Самый быстрый способ понять домен.
2. **[Жизненные циклы сущностей](../data-model/entity-lifecycle.md)** — все статусы и переходы (`products`, `cargoplaces`, `orders`, `dispatches`). Без этого детали API не складываются в картину.
3. **[Архитектура MVP](../architecture/system-overview.md)** + **[DFD](../business-process/data-flow-diagrams.md)** — компоненты, границы, потоки данных.
4. **[Маппинг WMS → блокчейн](../integration/blockchain-mapping.md)** + **[контракт данных](../integration/data-contract.md)** — как off-chain становится on-chain через outbox → Debezium → Kafka → adapter.
5. **API:** [api-contract.md](../api/api-contract.md) и интерактивный [swagger-ui.html](../api/swagger-ui.html) — конкретные эндпоинты для интеграции и фронтенда.
6. **Код-референс:** [wms-reference.md](wms-reference.md) → [ledger-adapter-reference.md](ledger-adapter-reference.md) → [smart-contract-reference.md](smart-contract-reference.md) — структура кода для продолжения разработки.
7. **Запуск и эксплуатация:** [operations.md](operations.md) — docker-compose, переменные окружения, инициализация, мониторинг. Нужно, чтобы поднять стек локально.
8. **Перед первым MR:** [CONVENTIONS.md](../CONVENTIONS.md) и [MR_GUIDE.md](../MR_GUIDE.md) — git-workflow, стиль кода, правила ревью (хуки проверяют автоматически).

---

## Глоссарий ключевых терминов

| Термин | Расшифровка |
|--------|-------------|
| **TTN** | Товарно-транспортная накладная. Документ поставки; сканируется на КПП, идентифицирует входящий `shipment`. |
| **Грузоместо / cargoplace** | Физическая единица поставки (палета, короб-контейнер), внутри которой едут коробки с товарами. Проходит этапы КПП и стола приёмки. |
| **SKU** | Stock Keeping Unit — позиция номенклатуры (артикул товара). Связывается со штрихкодами; по `barcode` находится `sku`. |
| **Product** | Конкретный физический экземпляр товара с уникальным QR-кодом. Создаётся на столе приёмки (`scan-qr`); проходит статусы `RECEIVED → STORED → ALLOCATED → ASSEMBLED → SHIPPED`. `aggregate_id` outbox-события = `product_id`. |
| **Putaway / Раскладка** | Перемещение товаров из приёмочного буфера в ячейки хранения (storage bins). Outbox-тип `putaway`, статус `RECEIVED → STORED`. |
| **Assembly / Picking / Сборка** | Аллокация заказов на товары + физический подбор по QR. Outbox-тип — **`picking`** (не `assembly`), статус `STORED → ALLOCATED → ASSEMBLED`. |
| **Dispatch / Рейс** | Запланированная отгрузка (машина + водитель + направление). Создаётся в модуле `dispatches`; не пишет outbox-события. |
| **Outbox** | Таблица `public.outbox_events`. WMS пишет в неё событие в одной транзакции с бизнес-записью. Поля: `event_id`, `aggregate_id` (= `product_id`), `aggregate_type`, `event_type`, `payload_hash`. |
| **CDC (Change Data Capture)** | Захват изменений БД. Здесь — Debezium читает WAL Postgres (logical replication) и публикует строки `outbox_events` в Kafka без участия приложения. |
| **onchain_events** | Таблица в `public`. Ledger Adapter ведёт в ней статус каждого on-chain события (`PENDING → SENT → COMMITTED/FAILED`). WMS читает её через `CheckChainStatus` для блокировки FSM при `FAILED`. |
| **BatchMappingWMS** | Смарт-контракт-верификатор. Хранит `itemStatus` (FSM-состояние товара) и `processedEventIds` (идемпотентность). Методы: `batchAccept / batchPutAway / batchPick / batchShip`. |
| **Flusher** | Компонент Ledger Adapter: группирует сообщения по `aggregate_type`, соблюдает FSM-порядок (`receiving → putaway → picking → shipping`) и вызывает `chain.BatchCall`. |
| **DLQ** | Dead Letter Queue (`wms.dlq.v1`). Сюда уходят «ядовитые» и нераспознанные сообщения для разбора. |
