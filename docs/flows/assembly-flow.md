# Сборка заказа (Assembly / Picking)

## Суть

Подбор товаров со стеллажей по заказу клиента. Два этапа:

- система читает `order_lines` и аллоцирует конкретные `products`
- оператор физически подбирает товар по созданным `assembly_tasks`

**Ключевое в новой схеме:**

- `order_lines` определяют, **какие SKU и в каком количестве** нужно собрать
- `orders.destination_id` фиксирует магазин-получатель
- `assembly_tasks.destination_id` денормализует магазин в задачу
- `SHIPPING_BUFFER` резервируется под staging исходящего потока для конкретного `destination_id`

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
    WMS->>PG: SELECT orders + order_lines WHERE order_id = ...
    WMS->>PG: SELECT shipping_buffer WHERE section='SHIPPING_BUFFER' AND destination_id = orders.destination_id

    loop Для каждой строки заказа и каждой требуемой единицы
        WMS->>PG: SELECT products WHERE sku_id = order_line.sku_id AND status = 'STORED'
        WMS->>WMS: Выбрать конкретный экземпляр (FIFO / по ячейке)
        WMS->>PG: UPDATE products SET status = 'ALLOCATED', order_id = :order_id
        WMS->>PG: INSERT assembly_tasks (order_id, product_id, sku_id, from_bin_id, section, destination_id, status='PENDING')
    end

    WMS->>PG: UPDATE orders SET status = 'ALLOCATED'
    WMS-->>SYS: Аллокация завершена

    Note over SYS,PG: outbox events НЕ создаются — аллокация не меняет on-chain статус

    Note over Op,PG: === Этап 2: Подбор (оператор) ===
    Op->>UI: Открывает задачу сборки для заказа
    UI->>WMS: GET /assembly/tasks?order_id=...
    WMS->>PG: SELECT assembly_tasks WHERE order_id = ... AND status = 'PENDING'
    WMS-->>UI: Список задач с ячейками, зонами и destination_id

    loop Для каждого товара
        Op->>UI: Сканирует QR товара с полки
        UI->>WMS: POST /assembly/pick {assembly_task_id}
        WMS->>PG: BEGIN TX
        WMS->>PG: UPDATE assembly_tasks SET status = 'DONE', onchain_status = 'PENDING_ONCHAIN'
        WMS->>PG: UPDATE products SET status = 'ASSEMBLED'
        WMS->>PG: INSERT outbox_events (event_id, aggregate_id=product_id, aggregate_type='picking')
        WMS->>PG: COMMIT
        WMS-->>UI: Товар подобран ✓
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
    NEW --> ALLOCATED : Все SKU из order_lines обеспечены конкретными products
    ALLOCATED --> ASSEMBLED : Все assembly_tasks выполнены
    ASSEMBLED --> SHIPPED : Отгрузка
```

## Какие таблицы затрагиваются

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `orders` | SELECT / UPDATE | Читается `destination_id`, статус: `NEW → ALLOCATED → ASSEMBLED` |
| `order_lines` | SELECT | Источник потребности по SKU и количеству |
| `products` | UPDATE | `status: STORED → ALLOCATED → ASSEMBLED` |
| `products` | UPDATE | `order_id: NULL → order_id` при аллокации |
| `assembly_tasks` | INSERT | Создание задач подбора с `destination_id` |
| `assembly_tasks` | UPDATE | `status: PENDING → DONE`, `onchain_status = PENDING_ONCHAIN` |
| `bins` | SELECT | Используются обычные storage bins и destination-specific `SHIPPING_BUFFER` |
| `outbox_events` | INSERT | Только при подборе: `aggregate_type='picking'` |

## Outbox events

```sql
-- При каждом POST /assembly/pick (в той же транзакции):
INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
VALUES (
  assembly_task.event_id,
  assembly_task.product_id,
  'picking',
  'wms.picking.v1',
  sha256(...)
);
```

**Блокчейн:** `batchPick(eventIds[], itemIds[])` → переход `PutAway → Picked`.

## Примечание про SHIPPING_BUFFER

`SHIPPING_BUFFER` вводится на уровне схемы как подготовка исходящего потока.  
Сама сборка уже знает `destination_id` заказа и задач, поэтому на следующих шагах можно валидировать, что собранный товар staging-ится и отгружается через буфер и рейс того же магазина.
