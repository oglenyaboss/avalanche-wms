# Сборка заказа (Assembly / Picking)

## Суть

Подбор товаров для отгрузки в конкретный магазин. Поток работает **по магазинам (`destination_id`)**: система читает `NEW` заказы и их `order_lines`, аллоцирует конкретные `products`, создаёт `assembly_tasks`, а оператор физически подбирает товары и переносит их в `SHIPPING_BUFFER` этого магазина.

**Ключевое:**

- `order_lines` определяют, какие SKU и в каком количестве нужно собрать.
- `orders.destination_id` фиксирует магазин-получатель.
- `assembly_tasks.destination_id` денормализует магазин в задачу, чтобы оператор работал с пачкой задач для одного destination.
- `SHIPPING_BUFFER` закреплён за конкретным `destination_id` и используется как staging исходящего потока.
- `pick` — физическая операция оператора: товар → `ASSEMBLED`, задача → `DONE`. **On-chain записи нет** — Pick-действие уходит в цепь позже, при выгрузке корзины в буфер.
- `scan-shipping-buffer` — выгрузка корзины в буфер магазина: **подписанное действие** (draft → passkey → submit), on-chain переход `PutAway -> Picked`, 1 строка outbox на действие-часть.
- Паттерн симметричен раскладке (putaway), только в обратную сторону: `storage -> cart -> shipping_buffer` вместо `buffer -> cart -> storage`.

### Три этапа

1. **Аллокация** (системная) — система находит `NEW` заказы для магазина, читает `order_lines`, подбирает `STORED` products по SKU и создаёт `assembly_tasks`.
2. **Подбор (pick)** — оператор обходит полки, сканирует QR каждого товара. Товар становится `ASSEMBLED` (корзина — DB-derived: ASSEMBLED-товары оператора с DONE-задачей). On-chain записи нет.
3. **Размещение в буфер магазина** — оператор приходит в зону буфера, сканирует его. Все товары из cart попадают в буфер, статус товара переходит в `READY_TO_SHIP`. Одно подписанное Pick-действие (draft → passkey → submit) уходит в цепь.

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
        WMS->>PG: UPDATE assembly_tasks SET status = 'DONE', operator_id = :op
        WMS->>PG: UPDATE products SET status = 'ASSEMBLED'
        WMS->>PG: COMMIT
        WMS->>WMS: Товар становится ASSEMBLED (корзина оператора читается из БД)
        WMS-->>UI: Товар подобран ✓ (в cart: M, всего: N)
    end

    Note over Op,PG: outbox НЕ пишется — Pick-действие уходит в цепь при выгрузке корзины

    Note over Op,PG: === Этап 4: Размещение в буфер магазина (подписанное Pick-действие) ===
    Op->>UI: Приходит к буферу магазина, сканирует его QR
    UI->>WMS: POST /assembly/scan-shipping-buffer/draft {buffer_bin_id}
    WMS->>PG: SELECT bins WHERE bin_id = :id AND section = 'SHIPPING_BUFFER'
    WMS->>WMS: Валидация: bin.destination_id совпадает с destination всех товаров в cart
    WMS->>WMS: Товары сортируются по itemId, операция режется на части (≤2000 товаров)
    WMS->>PG: INSERT warehouse_actions (строка на часть: action_id, salt, challenge, details)
    WMS-->>UI: Черновик: operation_id + части (action_id, challenge)
    UI->>Op: Запрос passkey-подписи для каждой части
    Op->>UI: Подтверждает каждую часть
    UI->>WMS: POST /assembly/scan-shipping-buffer/submit {operation_id, assertions[]}
    WMS->>WMS: Проверка WebAuthn-подписи каждой части
    WMS->>PG: BEGIN TX

    loop Для каждого product в cart
        WMS->>PG: UPDATE products SET bin_id = :buffer_bin_id, status = 'READY_TO_SHIP'
        WMS->>PG: UPDATE assembly_tasks SET action_id, unloaded_at
    end

    WMS->>PG: UPDATE orders SET status = 'ASSEMBLED' WHERE все products заказа READY_TO_SHIP
    loop Для каждой части операции
        WMS->>PG: INSERT outbox_events (event_id=action_id, aggregate_type='signed_action', event_type='wms.action.v1', payload=конверт)
    end
    WMS->>PG: COMMIT
    WMS-->>UI: Размещено M товаров в буфере магазина ✓

    Note over WMS,KF: 1 outbox-строка на действие → wms.events.v1 → SignedMappingWMS.applyActions → FSM на каждый товар: PutAway -> Picked
```

## Физический процесс

```mermaid
flowchart LR
    subgraph STORAGE["Мезонин (storage)"]
        S1["Ячейка M2-A-03\n📦 QR-001\n📦 QR-002"]
        S2["Ячейка M2-B-07\n📦 QR-003"]
    end

    subgraph CART["Сборщик (корзина)"]
        C1["🛒 Тележка\n📦 QR-001\n📦 QR-002\n📦 QR-003"]
    end

    subgraph BUFFER["Буфер магазина №5\n(SHIPPING_BUFFER)"]
        B1["🏪 destination_id = SHOP-5\n📦 QR-001\n📦 QR-002\n📦 QR-003"]
    end

    S1 -->|"Скан QR товара — /pick\nALLOCATED -> ASSEMBLED\nбез outbox"| C1
    S2 -->|"Скан QR товара — /pick"| C1
    C1 -->|"Скан QR буфера магазина\n/scan-shipping-buffer/draft → подпись → /submit\nASSEMBLED -> READY_TO_SHIP\nвсе товары в cart -> буфер\nbin_id = buffer_bin_id\nподписанное Pick-действие"| B1
```

## Состояния сущностей

### products.status (в контексте сборки)

```mermaid
stateDiagram-v2
    STORED --> ALLOCATED : Аллокация (система)
    ALLOCATED --> ASSEMBLED : /pick — оператор сканирует товар с полки
    ASSEMBLED --> READY_TO_SHIP : /assembly/scan-shipping-buffer/submit (подписанное Pick-действие)
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
| `products.status` | UPDATE | `/scan-shipping-buffer/submit` | `ASSEMBLED -> READY_TO_SHIP` |
| `products.order_id` | UPDATE | Аллокация | `NULL -> :order_id` |
| `products.bin_id` | UPDATE | `/scan-shipping-buffer/submit` | `storage_bin -> shipping_buffer_bin` |
| `assembly_tasks` | INSERT | Аллокация | Создание задач подбора с `destination_id`, `status = PENDING` |
| `assembly_tasks` | UPDATE | `/pick` | `status: PENDING/IN_PROGRESS -> DONE`, `operator_id` |
| `assembly_tasks` | UPDATE | `/scan-shipping-buffer/submit` | `action_id` (ссылка на подписанное Pick-действие), `unloaded_at` |
| `warehouse_actions` | INSERT | `/scan-shipping-buffer/draft` | Строка на каждую часть Pick-операции |
| `bins` | SELECT | Аллокация / буфер | Обычные storage bins и destination-specific `SHIPPING_BUFFER` |
| `outbox_events` | INSERT | **только `/scan-shipping-buffer/submit`** | 1 строка на действие-часть, `aggregate_type = 'signed_action'` |

## Outbox events

Создаются **только при `POST /assembly/scan-shipping-buffer/submit`**, в той же транзакции, что и перемещение товаров в буфер — **по одной строке на действие-часть** (операция режется на части по ≤2000 товаров, у каждой свой `action_id`):

```sql
-- после проверки всех passkey-подписей, для каждой части операции:
INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash, payload)
VALUES (
  action_id,                 -- = warehouse_actions.action_id
  action_id,
  'signed_action',
  'wms.action.v1',
  challenge (hex),
  подписанный конверт (ADR-0001 §9)
);
```

**Блокчейн:** Ledger Adapter публикует действия в `SignedMappingWMS.applyActions` → проверка подписи (precompile P-256) → FSM на каждый товар: переход `PutAway -> Picked`.

**При `/assembly/pick` outbox НЕ создаётся** — подбор товара с полки меняет только off-chain статусы. On-chain переход `PutAway -> Picked` происходит одним подписанным действием при выгрузке корзины в буфер магазина.

## Корзина оператора

Корзина — **не in-memory структура, а запрос к БД**: `ASSEMBLED`-товары оператора с DONE-задачей (`CountAssembledByOperator`, `ListOperatorBasket`). Поэтому `cart_size` в ответе `/assembly/pick` и состав корзины переживают рестарт WMS и горизонтальное масштабирование.

- Наполняется при каждом успешном `/assembly/pick` (товар становится `ASSEMBLED`, задача — `DONE`).
- Состав фиксируется в черновике `/assembly/scan-shipping-buffer/draft` и сверяется на `/submit` (расхождение → `DRAFT_STALE`).
- После `/submit` товары уже в `READY_TO_SHIP`, в буфере — в корзину больше не попадают.

**Частичная сдача поддерживается:** оператор может собрать 5 товаров и сразу отнести в буфер, потом собрать ещё 10 и отнести снова. Каждый цикл `pick` + `scan-shipping-buffer` + `submit` независим.

## Валидации

| Условие | Ошибка |
|---------|--------|
| `/allocate`: нет `SHIPPING_BUFFER` для `destination_id` | `SHIPPING_BUFFER_NOT_FOUND` |
| `/allocate`: недостаточно `STORED` products по SKU | `INSUFFICIENT_STOCK` |
| `/allocate`: заказ уже в статусе != `NEW` | `ORDER_NOT_NEW` |
| `/pick`: `product.status != ALLOCATED` | `PRODUCT_NOT_ALLOCATED` |
| `/pick`: нет `PENDING` / `IN_PROGRESS` assembly_task для product | `NO_TASK_FOR_PRODUCT` |
| `/pick`: `FAILED` PutAway-действие товара в цепи | `CHAIN_EVENT_REJECTED` |
| `/scan-shipping-buffer/draft`: оператор не несёт ASSEMBLED товаров (ничего не подобрано / уже размещено) | `CART_EMPTY` |
| `/scan-shipping-buffer/draft`: `bin.section != 'SHIPPING_BUFFER'` | `BIN_NOT_SHIPPING_BUFFER` (400) |
| `/scan-shipping-buffer/draft`: оператор несёт ASSEMBLED товары, но для другого `destination_id`, чем у буфера | `DESTINATION_MISMATCH` |
| `/scan-shipping-buffer/submit`: состав корзины изменился после подписи | `DRAFT_STALE` |

`DESTINATION_MISMATCH` защищает от ситуации «сборщик собирал для магазина №5, а сканирует буфер магазина №7». Механика: `ListOperatorBasket` фильтрует корзину по `assembly_task.destination_id = bin.destination_id`, поэтому для чужого буфера корзина пуста. Сервис различает два случая через `CountAssembledByOperator`: если у оператора **есть** ASSEMBLED товары (для другого магазина) → `DESTINATION_MISMATCH`; если ASSEMBLED товаров нет вовсе → `CART_EMPTY`. Ни один товар чужого магазина не попадает в буфер (защита от mis-delivery).

## Связь с соседними этапами

```mermaid
flowchart LR
    subgraph PUT["3. Раскладка"]
        P["product.status = STORED\nbin = storage_bin"]
    end

    subgraph ASM["4. Сборка"]
        A1["Аллокация\nSTORED -> ALLOCATED\norder_id назначен"]
        A2["pick\nALLOCATED -> ASSEMBLED\nбез outbox"]
        A3["scan-shipping-buffer\n(draft → подпись → submit)\nASSEMBLED -> READY_TO_SHIP\nbin = shipping_buffer\nподписанное Pick-действие"]
    end

    subgraph SHIP["5. Отгрузка"]
        S["ship\n(draft → подпись → submit)\nREADY_TO_SHIP -> SHIPPED\nподписанное Ship-действие"]
    end

    P --> A1 --> A2 --> A3 --> S

    style ASM fill:#FFD700,stroke:#333
```
