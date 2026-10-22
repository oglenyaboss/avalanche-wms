# WMS + Blockchain — Документация

**Проект:** WMS-монолит (Go) + Avalanche Subnet-EVM
**Назначение:** складская система с неизменяемой аудит-полосой на блокчейне

---

## Что это за система

Складская система (WMS), которая управляет 5 этапами жизненного цикла товара: **Приёмка на КПП → Приёмка на столе → Раскладка → Сборка → Отгрузка**. Каждый шаг, меняющий состояние товара в цепи, оператор подтверждает и подписывает своим passkey; подписанное действие фиксируется на блокчейне через цепочку Outbox → Debezium → Kafka → Ledger Adapter → смарт-контракт.

### Стек

| Компонент | Технология |
|-----------|-----------|
| WMS-монолит | Go, net/http |
| БД | PostgreSQL 17 (3 схемы: public, wms_inventory, wms_ops) |
| CDC | Debezium (outbox → Kafka) |
| Очереди | Apache Kafka (рабочий топик `wms.events.v1` + DLQ `wms.dlq.v1`; одна партиция) |
| Мост в блокчейн | Ledger Adapter (Go) |
| Блокчейн | Avalanche Subnet-EVM (локально, dev; кастомный сабнет chainID 99999); permissioned Allow Lists включены ([deploy/subnet/README.md](../deploy/subnet/README.md), раздел Allow-list) |
| Контракты | ConsortiumRegistry + SignedMappingWMS (Solidity; подписанные действия, [ADR-0001](adr/0001-signed-actions.md)) |

### Архитектурная схема (упрощённая)

```
Оператор → WMS API → PostgreSQL → Debezium → Kafka → Ledger Adapter → Blockchain
                         ↑                                    |
                         └────── onchain_events (статус) ─────┘
```

---

## Навигация по документам

### Документация разработчика

| Документ | Описание |
|----------|----------|
| [Индекс разработчика](developer/README.md) | **Точка входа для приёма проекта:** обзор системы, архитектурная схема, карта всей документации, технологический стек, ключевые принципы, маршрут чтения и глоссарий. Начни отсюда при передаче проекта. |
| [OpenAPI-спецификация](api/openapi.yaml) | Машиночитаемый контракт API (источник истины). По нему сгенерирован `api/api-contract.md`. |

### Бизнес-процесс

| Документ | Описание |
|----------|----------|
| [Сквозной путь товара](business-process/end-to-end-flow.md) | Полный жизненный цикл: от ворот до отгрузки. Начни отсюда. |
| [Диаграммы потоков данных (DFD)](business-process/data-flow-diagrams.md) | Level 0 (контекст) → Level 1 (процессы) → Level 2 (по модулям) |

### Детальные flow-диаграммы (по этапам)

| Документ | Этап | В цепи |
|----------|------|--------|
| [Приёмка на КПП](flows/receiving-gate-flow.md) | Сканирование TTN и грузомест | Нет |
| [Приёмка на столе](flows/receiving-table-flow.md) | Вскрытие грузоместа, создание products | Подписанное действие Receive |
| [Раскладка](flows/putaway-flow.md) | Из буфера в ячейки хранения | Подписанное действие PutAway |
| [Сборка](flows/assembly-flow.md) | Аллокация + подбор товаров | Подписанное действие Pick |
| [Отгрузка](flows/shipping-flow.md) | Верификация и отправка заказа | Подписанное действие Ship |

### Модель данных

| Документ | Описание |
|----------|----------|
| [Жизненные циклы сущностей](data-model/entity-lifecycle.md) | Все статусы, все переходы, все сущности — в одном месте |
| [ER-диаграмма](data-model/er-diagram.md) | Связи между таблицами, уникальные ограничения |
| [Схема БД (подробная)](db/Database_ru_v2.md) | Все поля всех таблиц с описаниями |

### Интеграция (WMS ↔ Блокчейн)

| Документ | Описание |
|----------|----------|
| [Маппинг WMS → Блокчейн](integration/blockchain-mapping.md) | Какой шаг какое подписанное действие пишет в цепь: FSM, детали, отклонения, лимиты |
| [Контракт данных](integration/data-contract.md) | Формат outbox, Kafka, Ledger Adapter, onchain_events подписанного действия |
| [Управление ключами](integration/key-management.md) | Регистрация и отзыв ключей, роли, корень доверия консорциума |
| [Процедура аудита](integration/audit-procedure.md) | CLI-сверка `wms/cmd/audit` и страница `/audit` для аудитора |

### Документация разработчика (hand-off)

| Документ | Описание |
|----------|----------|
| [WMS Reference](developer/wms-reference.md) | Референс WMS-монолита: пакеты, модули (receiving / putaway / assembly / shipping / dispatches / auth), сервисы, репозитории, запись outbox |
| [Ledger Adapter Reference](developer/ledger-adapter-reference.md) | Внутреннее устройство моста: пакеты `internal/`, consumer, batch-flusher, reconcile, идемпотентность, env, типы и сигнатуры |
| [Smart Contract Reference](developer/smart-contract-reference.md) | Справочник по контракту старого пути (событие на товар, до #19): FSM, batch-функции, `UUID → uint256`. Текущая модель — [ADR-0001](adr/0001-signed-actions.md) |
| [Operations](developer/operations.md) | Эксплуатация: docker-compose, переменные окружения, инициализация БД/Kafka, регистрация Debezium-коннектора, мониторинг, CI |

### API

| Документ | Описание |
|----------|----------|
| [OpenAPI-спецификация](api/openapi.yaml) | Машиночитаемый контракт API — источник истины для эндпоинтов |
| [API-контракт](api/api-contract.md) | Request/response, ошибки, побочные эффекты. **Перегенерирован из `openapi.yaml`** |

### Архитектура и инфраструктура

| Документ | Описание |
|----------|----------|
| [Архитектура системы](architecture/system-overview.md) | Компоненты, роли, Kafka, Debezium, безопасность |
| [Конвенции проекта](CONVENTIONS.md) | Git workflow, Go style, SQL naming, Docker |
| [Гайд по MR](MR_GUIDE.md) | Правила оформления merge requests |

---

## С чего начать чтение

1. **[Сквозной путь товара](business-process/end-to-end-flow.md)** — обзор всей системы глазами оператора
2. **[Жизненные циклы сущностей](data-model/entity-lifecycle.md)** — все статусы и переходы
3. **[API-контракт](api/api-contract.md)** — конкретные эндпоинты для реализации
4. **[Маппинг WMS → Блокчейн](integration/blockchain-mapping.md)** — как off-chain становится on-chain

---

## Ключевые принципы

1. **Outbox pattern** — WMS пишет только в PostgreSQL. Debezium подхватывает outbox_events и публикует в Kafka. Нет двойной записи.
2. **Одна строка outbox = одно подписанное действие** — `event_id = action_id = actionId`, `aggregate_type = signed_action`; всё едет в один топик `wms.events.v1` (одна партиция — порядок в цепи равен порядку коммитов в БД).
3. **Блокчейн = верификатор, не хранилище.** Контракт `SignedMappingWMS` проверяет подпись действия через реестр `ConsortiumRegistry` и FSM каждого товара; действие применяется или отклоняется персонально — батч не ревертится целиком.
4. **Идемпотентность на каждом уровне** — `event_id` уникален в outbox и onchain_events; повтор `actionId` отсекается `actionState` в контракте.
5. **КПП не пишет в блокчейн** — товары (products) создаются только на столе приёмки.
