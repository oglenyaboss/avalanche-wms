# Отгрузка (Shipping)

## Суть

Финальный этап: собранные товары передаются водителю и покидают склад.

**Ключевое в новой схеме:**

- отгрузка привязана к заранее созданному `outbound_dispatch`
- оператор находит рейс по `dispatch_code` (QR водителя)
- рейс, заказ и буфер отгрузки должны сходиться по одному `destination_id`
- в `shippings` хранится ссылка на `dispatch_id`, а номер машины берется из `outbound_dispatches`

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    actor Op as Оператор отгрузки
    participant UI as UI (Scanner)
    participant WMS as WMS API
    participant PG as Postgres
    participant KF as Kafka (async)

    Note over Op,PG: === Шаг 1: Идентификация рейса ===
    Op->>UI: Сканирует QR водителя
    UI->>WMS: POST /shipping/dispatches/resolve {dispatch_code}
    WMS->>PG: SELECT outbound_dispatches WHERE dispatch_code = ...
    WMS->>PG: UPDATE outbound_dispatches SET status = 'AT_GATE', arrived_at = now() (опционально)
    WMS-->>UI: Рейс найден: destination_id, vehicle_number, driver_name

    Note over Op,PG: === Шаг 2: Выбор заказа ===
    Op->>UI: Выбирает заказ для отгрузки
    UI->>WMS: GET /shipping/orders?dispatch_id=...
    WMS->>PG: SELECT orders WHERE status = 'ASSEMBLED' AND destination_id = dispatch.destination_id
    WMS-->>UI: Список заказов для выбранного магазина

    Note over Op,PG: === Шаг 3: Верификация товаров ===
    loop Для каждого товара в заказе
        Op->>UI: Сканирует QR товара
        UI->>WMS: POST /shipping/verify {order_id, product_id, dispatch_id}
        WMS->>WMS: Проверка: product принадлежит order? status = ASSEMBLED? order.destination_id = dispatch.destination_id?
        WMS-->>UI: Товар подтверждён ✓
    end

    Note over Op,PG: === Шаг 4: Отгрузка ===
    Op->>UI: Подтверждает загрузку
    UI->>WMS: POST /shipping/ship {order_id, dispatch_id}
    WMS->>PG: BEGIN TX
    WMS->>PG: SELECT products WHERE order_id = ... AND status = 'ASSEMBLED'

    loop Для каждого product
        WMS->>PG: UPDATE products SET status = 'SHIPPED'
        WMS->>PG: INSERT shippings (event_id, product_id, dispatch_id, onchain_status='PENDING_ONCHAIN')
        WMS->>PG: INSERT outbox_events (event_id, aggregate_id=product_id, aggregate_type='shipping')
    end

    WMS->>PG: UPDATE orders SET status = 'SHIPPED'
    WMS->>PG: UPDATE outbound_dispatches SET status = 'DEPARTED', departed_at = now() (по бизнес-правилу)
    WMS->>PG: COMMIT
    WMS-->>Op: Заказ отгружен ✓

    Note over PG,KF: Debezium → Kafka → Ledger Adapter → batchShip → Picked → Shipped
```

## Состояния сущностей

### outbound_dispatches.status

```mermaid
stateDiagram-v2
    [*] --> SCHEDULED : Внешняя логистика создала рейс
    SCHEDULED --> AT_GATE : Водитель прибыл / QR отсканирован
    AT_GATE --> DEPARTED : Погрузка завершена
    SCHEDULED --> CANCELLED : Рейс отменён
    AT_GATE --> CANCELLED : Рейс отменён
```

### orders.status (в контексте отгрузки)

```mermaid
stateDiagram-v2
    ASSEMBLED --> SHIPPED : Все товары заказа отгружены
```

### products.status (в контексте отгрузки)

```mermaid
stateDiagram-v2
    ASSEMBLED --> SHIPPED : Отгрузка
```

## Какие таблицы затрагиваются

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `outbound_dispatches` | SELECT / UPDATE | Поиск по `dispatch_code`, статусы `SCHEDULED → AT_GATE → DEPARTED` |
| `orders` | SELECT / UPDATE | Фильтр по `destination_id`, статус `ASSEMBLED → SHIPPED` |
| `products` | UPDATE | `status: ASSEMBLED → SHIPPED` |
| `shippings` | INSERT | Запись об отгрузке с `dispatch_id` |
| `outbox_events` | INSERT | N events (1 per product, `aggregate_type='shipping'`) |

## Outbox events

```sql
-- При POST /shipping/ship, для КАЖДОГО product в заказе:
INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
VALUES (
  shipping.event_id,
  shipping.product_id,
  'shipping',
  'wms.shipping.v1',
  sha256(...)
);
```

**Блокчейн:** `batchShip(eventIds[], itemIds[])` → переход `Picked → Shipped`.

## Валидация destination

В исходящем потоке `destination_id` становится сквозным ключом:

- `orders.destination_id` говорит, куда должен уехать заказ
- `bins.destination_id` привязывает `SHIPPING_BUFFER` к магазину
- `outbound_dispatches.destination_id` говорит, для какого магазина приехала машина
- `shippings.dispatch_id` фиксирует, в какой рейс реально погрузили товар

За счет этого оператор и backend могут валидировать `order ↔ shipping_buffer ↔ dispatch` в одном контуре.
