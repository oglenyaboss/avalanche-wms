# Сборка заказа (Assembly / Picking)

## Суть

Подбор товаров со стеллажей по заказу клиента. Два этапа: система назначает товары (аллокация), оператор физически их собирает (подбор).

**Ключевое:** аллокация — системная операция (без оператора). Подбор — физическая операция оператора. Outbox event создаётся **только при подборе** (когда товар физически взят с полки).

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    participant SYS as Система / Менеджер
    actor Op as Оператор сборки
    participant UI as UI (ТСД / Scanner)
    participant WMS as WMS API
    participant PG as Postgres
    participant KF as Kafka (async)

    Note over SYS,PG: === Этап 1: Аллокация (системная) ===
    SYS->>WMS: POST /assembly/allocate {order_id}
    WMS->>PG: SELECT products WHERE sku_id IN (...) AND status = 'STORED'
    WMS->>WMS: Выбрать конкретные экземпляры (FIFO или по ячейке)

    loop Для каждого нужного товара
        WMS->>PG: UPDATE products SET status = 'ALLOCATED', order_id = :order_id
        WMS->>PG: INSERT assembly_tasks (order_id, product_id, sku_id, from_bin_id, status=PENDING)
    end
    WMS-->>SYS: Аллокация завершена: 5 товаров назначено

    Note over SYS,PG: outbox events НЕ создаются — аллокация не меняет on-chain статус

    Note over Op,PG: === Этап 2: Подбор (оператор) ===
    Op->>UI: Открывает задачу сборки для заказа
    UI->>WMS: GET /assembly/tasks?order_id=...
    WMS->>PG: SELECT assembly_tasks WHERE order_id AND status = 'PENDING'
    WMS-->>UI: Список задач: 5 товаров, с указанием ячеек

    loop Для каждого товара
        Op->>UI: Сканирует QR товара с полки
        UI->>WMS: POST /assembly/pick {assembly_task_id}
        WMS->>PG: BEGIN TX
        WMS->>PG: UPDATE assembly_tasks SET status = 'DONE', onchain_status = 'PENDING_ONCHAIN'
        WMS->>PG: UPDATE products SET status = 'ASSEMBLED'
        WMS->>PG: INSERT outbox_events (event_id, aggregate_id=product_id, aggregate_type='picking')
        WMS->>PG: COMMIT
        WMS-->>UI: Товар подобран ✓ (3/5)
    end

    Note over PG,KF: Debezium → Kafka → Ledger Adapter → batchPick → PutAway → Picked

    Note over Op,PG: === Этап 3: Завершение сборки ===
    WMS->>WMS: Все tasks для order_id = DONE?
    WMS->>PG: UPDATE orders SET status = 'ASSEMBLED'
    WMS-->>UI: Заказ собран ✓
```

## Состояния сущностей

### assembly_tasks.status
```mermaid
stateDiagram-v2
    [*] --> PENDING : Аллокация (система)
    PENDING --> IN_PROGRESS : Оператор начал сборку (опционально)
    IN_PROGRESS --> DONE : Оператор подобрал товар
    PENDING --> DONE : Оператор подобрал товар (без IN_PROGRESS)
    PENDING --> CANCELLED : Отмена (деаллокация)
    IN_PROGRESS --> CANCELLED : Отмена
```

### products.status (в контексте сборки)
```mermaid
stateDiagram-v2
    STORED --> ALLOCATED : Аллокация (система)
    ALLOCATED --> ASSEMBLED : Подбор (оператор)
    ALLOCATED --> STORED : Деаллокация (отмена заказа)
```

### orders.status
```mermaid
stateDiagram-v2
    NEW --> ALLOCATED : Все товары назначены
    ALLOCATED --> ASSEMBLY_IN_PROGRESS : Первый товар подобран
    ASSEMBLY_IN_PROGRESS --> ASSEMBLED : Все товары подобраны
    ASSEMBLED --> READY_TO_SHIP : Упаковка (MVP: автоматически)
```

## Какие таблицы затрагиваются

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `products` | UPDATE | status: STORED → ALLOCATED → ASSEMBLED |
| `products` | UPDATE | order_id: NULL → order_id (при аллокации) |
| `assembly_tasks` | INSERT | Создание задач подбора (при аллокации) |
| `assembly_tasks` | UPDATE | status: PENDING → DONE, onchain_status = PENDING_ONCHAIN |
| `orders` | UPDATE | status: NEW → ALLOCATED → ASSEMBLED |
| `outbox_events` | INSERT | **Только при подборе** (1 event per product, aggregate_type='picking') |

## Outbox events

```sql
-- При каждом POST /assembly/pick (в той же транзакции):
INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
VALUES (
  assembly_task.event_id,    -- тот же event_id что и в assembly_tasks
  assembly_task.product_id,  -- product_id → Kafka key → itemId в контракте
  'picking',                 -- → topic: wms.picking.v1
  'wms.picking.v1',
  sha256(...)
);
```

**Блокчейн:** `batchPick(eventIds[], itemIds[])` → переход `PutAway → Picked`.

## Связь с предыдущими этапами

```mermaid
flowchart TD
    subgraph PREV["Предыдущие этапы"]
        R["Приёмка → product создан, status=RECEIVED"]
        P["Раскладка → product в ячейке, status=STORED"]
    end

    subgraph ASSEMBLY["Сборка"]
        A1["Аллокация → status=ALLOCATED, order_id назначен"]
        A2["Подбор → status=ASSEMBLED, outbox event создан"]
    end

    subgraph NEXT["Следующий этап"]
        S["Отгрузка → status=SHIPPED"]
    end

    R --> P --> A1 --> A2 --> S

    style ASSEMBLY fill:#FFD700,stroke:#333
```
