# Сборка заказа (Assembly / Picking)

## Суть

Подбор товаров для отгрузки в конкретный магазин. Поток работает **по магазинам (`destination_id`)**: система читает `NEW` заказы и их `order_lines`, аллоцирует конкретные `products`, создаёт `assembly_tasks`, а оператор физически подбирает товары и переносит их в `SHIPPING_BUFFER` этого магазина.

**Ключевое:**

- `order_lines` определяют, какие SKU и в каком количестве нужно собрать.
- `orders.destination_id` фиксирует магазин-получатель.
- `assembly_tasks.destination_id` денормализует магазин в задачу, чтобы оператор работал с пачкой задач для одного destination.
- `SHIPPING_BUFFER` закреплён за конкретным `destination_id` и используется как staging исходящего потока.
- `pick` — физическая операция оператора, создаёт outbox `picking` -> on-chain переход `PutAway -> Picked`.
- `scan-shipping-buffer` — физическое перемещение в буфер магазина, **без outbox**: on-chain статус уже `Picked`, смена bin — off-chain деталь.
- Паттерн симметричен раскладке (putaway), только в обратную сторону: `storage -> cart -> shipping_buffer` вместо `buffer -> cart -> storage`.

### Три этапа

1. **Аллокация** (системная) — система находит `NEW` заказы для магазина, читает `order_lines`, подбирает `STORED` products по SKU и создаёт `assembly_tasks`.
2. **Подбор (pick)** — оператор обходит полки, сканирует QR каждого товара. Товар попадает в in-memory cart оператора, статус переходит в `ASSEMBLED`, создаётся outbox event.
3. **Размещение в буфер магазина** — оператор приходит в зону буфера, сканирует его. Все товары из cart попадают в буфер, статус товара переходит в `READY_TO_SHIP`. Outbox не создаётся.

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

    Note over SYS,PG: === Этап 1: Аллокация (системная, без оператора) ===
    SYS->>WMS: POST /assembly/allocate {destination_id}
    WMS->>PG: SELECT orders WHERE destination_id = :destination_id AND status = 'NEW'
    WMS->>PG: SELECT order_lines WHERE order_id IN (...)
    WMS->>PG: SELECT bins WHERE section = 'SHIPPING_BUFFER' AND destination_id = :destination_id
    WMS->>PG: SELECT products WHERE sku_id IN (...) AND status = 'STORED'
    WMS->>WMS: Подобрать конкретные экземпляры (FIFO / по ячейке)

    loop Для каждой строки заказа и каждой требуемой единицы
        WMS->>PG: UPDATE products SET status = 'ALLOCATED', order_id = :order_id
        WMS->>PG: INSERT assembly_tasks (order_id, product_id, sku_id, from_bin_id, section, destination_id, status='PENDING')
    end

    WMS->>PG: UPDATE orders SET status = 'ALLOCATED'
    WMS-->>SYS: Аллокация завершена: N товаров для магазина

    Note over SYS,PG: outbox events НЕ создаются — аллокация не меняет on-chain статус

    Note over Op,PG: === Этап 2: Получение задач ===
    Op->>UI: Открывает меню "Сборка", выбирает магазин
    UI->>WMS: GET /assembly/tasks?destination_id=...
    WMS->>PG: SELECT assembly_tasks WHERE destination_id = ... AND status = 'PENDING'
    WMS-->>UI: Список задач: товары, ячейки, порядок обхода

    Note over Op,PG: === Этап 3: Подбор товаров (pick) ===
    loop Для каждого товара
        Op->>UI: Подходит к ячейке, сканирует QR товара
        UI->>WMS: POST /assembly/pick {product_id}
        WMS->>WMS: Проверка: PENDING assembly_task для product / валидный операторский pick
        WMS->>PG: BEGIN TX
        WMS->>PG: UPDATE assembly_tasks SET status = 'DONE', onchain_status = 'PENDING_ONCHAIN', operator_id = :op
        WMS->>PG: UPDATE products SET status = 'ASSEMBLED'
        WMS->>PG: INSERT outbox_events (event_id=assembly_task.event_id, aggregate_id=product_id, aggregate_type='picking')
        WMS->>PG: COMMIT
        WMS->>WMS: Добавить product в in-memory cart оператора
        WMS-->>UI: Товар подобран ✓ (в cart: M, всего: N)
    end

    Note over WMS,KF: Debezium -> Kafka -> Ledger Adapter -> batchPick -> PutAway -> Picked

    Note over Op,PG: === Этап 4: Размещение в буфер магазина ===
    Op->>UI: Приходит к буферу магазина, сканирует его QR
    UI->>WMS: POST /assembly/scan-shipping-buffer {buffer_bin_id}
    WMS->>PG: SELECT bins WHERE bin_id = :id AND section = 'SHIPPING_BUFFER'
    WMS->>WMS: Валидация: bin.destination_id совпадает с destination всех товаров в cart
    WMS->>PG: BEGIN TX

    loop Для каждого product в cart
        WMS->>PG: UPDATE products SET bin_id = :buffer_bin_id, status = 'READY_TO_SHIP'
    end

    WMS->>PG: UPDATE orders SET status = 'ASSEMBLED' WHERE все products заказа READY_TO_SHIP
    WMS->>PG: COMMIT
    WMS->>WMS: Очистить cart оператора
    WMS-->>UI: Размещено M товаров в буфере магазина ✓

    Note over Op,PG: outbox events НЕ создаются — on-chain статус уже Picked
```

## Физический процесс

```mermaid
flowchart LR
    subgraph STORAGE["Мезонин (storage)"]
        S1["Ячейка M2-A-03\n📦 QR-001\n📦 QR-002"]
        S2["Ячейка M2-B-07\n📦 QR-003"]
    end

    subgraph CART["Сборщик (in-memory cart)"]
        C1["🛒 Тележка\n📦 QR-001\n📦 QR-002\n📦 QR-003"]
    end

    subgraph BUFFER["Буфер магазина №5\n(SHIPPING_BUFFER)"]
        B1["🏪 destination_id = SHOP-5\n📦 QR-001\n📦 QR-002\n📦 QR-003"]
    end

    S1 -->|"Скан QR товара — /pick\nALLOCATED -> ASSEMBLED\noutbox(picking)"| C1
    S2 -->|"Скан QR товара — /pick"| C1
    C1 -->|"Скан QR буфера магазина\n/scan-shipping-buffer\nASSEMBLED -> READY_TO_SHIP\nвсе товары в cart -> буфер\nbin_id = buffer_bin_id\nНЕТ outbox"| B1
```

## Состояния сущностей

### products.status (в контексте сборки)

```mermaid
stateDiagram-v2
    STORED --> ALLOCATED : Аллокация (система)
    ALLOCATED --> ASSEMBLED : /pick — оператор сканирует товар с полки
    ASSEMBLED --> READY_TO_SHIP : /scan-shipping-buffer — размещение в буфере магазина
    ALLOCATED --> STORED : Деаллокация (отмена)
```

Промежуточный статус `ASSEMBLED` означает «товар в руках сборщика, ещё не доставлен в буфер». После `scan-shipping-buffer` товар физически лежит в `SHIPPING_BUFFER` bin и ждёт машину.

### assembly_tasks.status

```mermaid
stateDiagram-v2
    [*] --> PENDING : Аллокация
    PENDING --> IN_PROGRESS : Оператор начал сборку (опционально)
    IN_PROGRESS --> DONE : /pick
    PENDING --> DONE : /pick без IN_PROGRESS
    PENDING --> CANCELLED : Деаллокация
    IN_PROGRESS --> CANCELLED : Деаллокация
```

### orders.status (в контексте сборки)

```mermaid
stateDiagram-v2
    NEW --> ALLOCATED : Все SKU из order_lines обеспечены конкретными products
    ALLOCATED --> ASSEMBLED : Все products заказа — в буфере магазина (READY_TO_SHIP)
```

**Важно:** `orders.status = ASSEMBLED` наступает **когда все products заказа уже в буфере** после `scan-shipping-buffer`, а не когда они просто подобраны в статус `ASSEMBLED`. До этого заказ остаётся в `ALLOCATED`.

## Какие таблицы затрагиваются

| Таблица | Операция | Этап | Что меняется |
|---------|----------|------|--------------|
| `orders` | SELECT / UPDATE | Аллокация -> сборка | Читается `destination_id`, статус `NEW -> ALLOCATED -> ASSEMBLED` |
| `order_lines` | SELECT | Аллокация | Источник потребности по SKU и количеству |
| `products.status` | UPDATE | Аллокация | `STORED -> ALLOCATED` |
| `products.status` | UPDATE | `/pick` | `ALLOCATED -> ASSEMBLED` |
| `products.status` | UPDATE | `/scan-shipping-buffer` | `ASSEMBLED -> READY_TO_SHIP` |
| `products.order_id` | UPDATE | Аллокация | `NULL -> :order_id` |
| `products.bin_id` | UPDATE | `/scan-shipping-buffer` | `storage_bin -> shipping_buffer_bin` |
| `assembly_tasks` | INSERT | Аллокация | Создание задач подбора с `destination_id`, `status = PENDING` |
| `assembly_tasks` | UPDATE | `/pick` | `status: PENDING/IN_PROGRESS -> DONE`, `onchain_status = PENDING_ONCHAIN` |
| `bins` | SELECT | Аллокация / буфер | Обычные storage bins и destination-specific `SHIPPING_BUFFER` |
| `outbox_events` | INSERT | **только `/pick`** | 1 event per product, `aggregate_type = 'picking'` |

## Outbox events

Создаются **только при `/assembly/pick`**, в той же транзакции:

```sql
INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
VALUES (
  assembly_task.event_id,
  assembly_task.product_id,
  'picking',
  'wms.picking.v1',
  sha256(payload)
);
```

**Блокчейн:** `batchPick(eventIds[], itemIds[])` -> переход `PutAway -> Picked`.

Ledger Adapter автоматически батчит N последовательных pick'ов в одну on-chain транзакцию (по max-size или timeout окну). Дополнительных усилий для батчинга в WMS не требуется.

**При `/assembly/scan-shipping-buffer` outbox НЕ создаётся** — on-chain статус остаётся `Picked`, перемещение в буфер магазина — off-chain деталь физического процесса.

## In-memory cart оператора

Аналогично раскладке (putaway):

- `assembly.Service` хранит `map[operator_id][]product_id` + `sync.RWMutex`.
- Заполняется при каждом успешном `/assembly/pick`.
- Очищается при успешном `/assembly/scan-shipping-buffer`.
- **Не персистентный:** при рестарте WMS cart теряется. Для MVP это приемлемо: уже сделанные pick'и сохранены в БД (`assembly_tasks.status = DONE`), оператор просто заново сканирует буфер и получает свежий список своих `ASSEMBLED` товаров.

**Частичная сдача поддерживается:** оператор может собрать 5 товаров и сразу отнести в буфер, потом собрать ещё 10 и отнести снова. Каждый цикл `pick` + `scan-shipping-buffer` независим.

## Валидации

| Условие | Ошибка |
|---------|--------|
| `/allocate`: нет `SHIPPING_BUFFER` для `destination_id` | `SHIPPING_BUFFER_NOT_FOUND` |
| `/allocate`: недостаточно `STORED` products по SKU | `INSUFFICIENT_STOCK` |
| `/allocate`: заказ уже в статусе != `NEW` | `ORDER_NOT_NEW` |
| `/pick`: `product.status != ALLOCATED` | `PRODUCT_NOT_ALLOCATED` |
| `/pick`: нет `PENDING` / `IN_PROGRESS` assembly_task для product | `NO_TASK_FOR_PRODUCT` |
| `/scan-shipping-buffer`: cart оператора пуст | `CART_EMPTY` |
| `/scan-shipping-buffer`: `bin.section != 'SHIPPING_BUFFER'` | `BIN_NOT_SHIPPING_BUFFER` |
| `/scan-shipping-buffer`: в cart есть товар с destination != `bin.destination_id` | `DESTINATION_MISMATCH` |

`DESTINATION_MISMATCH` защищает от ситуации «сборщик собирал для магазина №5, а сканирует буфер магазина №7». Ни один товар из cart не применяется, оператору сообщается о проблеме.

## Связь с соседними этапами

```mermaid
flowchart LR
    subgraph PUT["3. Раскладка"]
        P["product.status = STORED\nbin = storage_bin"]
    end

    subgraph ASM["4. Сборка"]
        A1["Аллокация\nSTORED -> ALLOCATED\norder_id назначен"]
        A2["pick\nALLOCATED -> ASSEMBLED\noutbox(picking)"]
        A3["scan-shipping-buffer\nASSEMBLED -> READY_TO_SHIP\nbin = shipping_buffer\nНЕТ outbox"]
    end

    subgraph SHIP["5. Отгрузка"]
        S["ship\nREADY_TO_SHIP -> SHIPPED\noutbox(shipping)"]
    end

    P --> A1 --> A2 --> A3 --> S

    style ASM fill:#FFD700,stroke:#333
```
