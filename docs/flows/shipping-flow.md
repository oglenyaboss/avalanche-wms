# Отгрузка (Shipping)

## Суть

Финальный этап: приехавшая по расписанию машина забирает товары из буфера магазина. Оператор сканирует буфер и QR водителя, после чего отгружает весь буфер или выбранные товары, если не всё влезает. После отгрузки товар покидает склад.

**Ключевое:**

- Отгрузка работает с **буфером магазина** (`SHIPPING_BUFFER` bin), а не с конкретным `order_id`. Все товары со статусом `READY_TO_SHIP` в этом буфере считаются готовыми к загрузке.
- QR водителя содержит `dispatch_code` — ссылку на заранее созданную запись `outbound_dispatches` (машина, водитель, магазин назначения). Эту запись создаёт внешняя ERP/логистика, аналогично TTN на приёмке.
- Рейс, заказ и буфер отгрузки должны сходиться по одному `destination_id`.
- В `shippings` хранится ссылка на `dispatch_id`; номер машины берётся из `outbound_dispatches`.
- Два режима: **bulk** (весь буфер одной кнопкой) и **spot** (точечный скан конкретных товаров).
- Отгрузка — **подписанное действие** (ADR-0001): `POST /shipping/ship/draft` → оператор подписывает каждую часть passkey → `POST /shipping/ship/submit`. Операция режется на части по ≤2000 товаров, каждая часть — отдельное действие в цепи.
- Одна outbox-строка на действие-часть (`aggregate_type = 'signed_action'`, топик `wms.events.v1`) -> контракт `SignedMappingWMS.applyActions` -> финальный переход `Picked -> Shipped` **на каждый товар действия** (FSM на товар на цепи).

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    actor Op as Оператор отгрузки
    participant UI as UI (Scanner)
    participant WMS as WMS API
    participant PG as Postgres
    participant KF as Kafka (async)

    Note over Op,PG: === Шаг 1: Сканирование буфера магазина ===
    Op->>UI: Открывает меню "Отгрузка", сканирует QR буфера
    UI->>WMS: POST /shipping/scan-buffer {buffer_bin_id}
    WMS->>PG: SELECT bins WHERE bin_id = :id AND section = 'SHIPPING_BUFFER'
    WMS->>PG: SELECT products WHERE bin_id = :buffer_bin_id AND status = 'READY_TO_SHIP'
    WMS-->>UI: Буфер "Магазин №5": N товаров готово к отгрузке

    Note over Op,PG: === Шаг 2: Сканирование QR водителя ===
    Op->>UI: Сканирует QR у водителя
    UI->>WMS: POST /shipping/scan-driver {dispatch_code}
    WMS->>PG: SELECT outbound_dispatches WHERE dispatch_code = :code
    WMS->>WMS: Проверка: dispatch.status = SCHEDULED
    WMS->>PG: UPDATE outbound_dispatches SET status = 'AT_GATE', arrived_at = now()
    WMS-->>UI: Машина А123БВ777 · Иванов И.И. · магазин №5

    Note over UI: UI сравнивает bin.destination_id и dispatch.destination_id.\nЕсли не совпадает — сообщает "Машина не на эту зону, разверните".

    Note over Op,PG: === Шаг 3: Отгрузка (подписанное действие) ===
    alt Bulk — кнопка "Отгрузить весь буфер"
        Op->>UI: Нажимает "Отгрузить всё"
        UI->>WMS: POST /shipping/ship/draft {buffer_bin_id, dispatch_id}
    else Spot — точечный скан товаров
        loop Выбранные товары
            Op->>UI: Сканирует QR конкретных товаров
        end
        Op->>UI: Нажимает "Отгрузить выбранные"
        UI->>WMS: POST /shipping/ship/draft {buffer_bin_id, dispatch_id, product_ids: [...]}
    end

    WMS->>WMS: Валидация: bin.destination_id == dispatch.destination_id
    WMS->>WMS: Гейт: Pick-действие каждого товара COMMITTED на цепи (иначе 409 PICKING_NOT_CONFIRMED)
    WMS->>WMS: Товары сортируются по itemId, операция режется на части (≤2000 товаров)
    WMS->>PG: INSERT warehouse_actions (строка на часть: action_id, salt, challenge, details)
    WMS-->>UI: Черновик: operation_id + части (action_id, challenge)
    UI->>Op: Запрос passkey-подписи для каждой части
    Op->>UI: Подтверждает каждую часть (N касаний)
    UI->>WMS: POST /shipping/ship/submit {operation_id, assertions[]}
    WMS->>WMS: Проверка WebAuthn-подписи каждой части
    WMS->>PG: BEGIN TX
    WMS->>PG: SELECT products (весь буфер или product_ids) FOR UPDATE

    loop Для каждого product
        WMS->>PG: UPDATE products SET status = 'SHIPPED'
        WMS->>PG: INSERT shippings (product_id, dispatch_id, operator_id, action_id)
    end

    loop Для каждой части операции
        WMS->>PG: INSERT outbox_events (event_id=action_id, aggregate_type='signed_action', event_type='wms.action.v1', payload=конверт)
    end

    WMS->>PG: UPDATE orders SET status = 'SHIPPED' WHERE все products заказа SHIPPED

    alt Буфер опустел
        WMS->>PG: UPDATE outbound_dispatches SET status = 'DEPARTED', departed_at = now()
    end

    WMS->>PG: COMMIT
    WMS-->>Op: Отгружено N товаров, машина уехала ✓

    Note over PG,KF: 1 outbox-строка на действие → Kafka wms.events.v1 → SignedMappingWMS.applyActions → FSM на каждый товар: Picked -> Shipped
```

## Физический процесс

```mermaid
flowchart LR
    subgraph BUFFER["Буфер магазина №5\n(SHIPPING_BUFFER)"]
        B1["📦 QR-001\n📦 QR-002\n📦 QR-003\n📦 QR-004"]
    end

    subgraph DRIVER["Приехавшая машина"]
        D1["🚚 QR водителя\n(dispatch_code)\n\nvehicle: А123БВ777\ndriver: Иванов И.И.\ndest: магазин №5"]
    end

    subgraph TRUCK["Кузов машины"]
        T1["📦 QR-001\n📦 QR-002\n📦 QR-003\n📦 QR-004"]
    end

    D1 -->|"/scan-driver\nвалидация destination"| BUFFER
    BUFFER -->|"/ship/draft → подпись → /ship/submit\nREADY_TO_SHIP -> SHIPPED\nподписанное Ship-действие"| TRUCK
```

## Состояния сущностей

### products.status (в контексте отгрузки)

```mermaid
stateDiagram-v2
    READY_TO_SHIP --> SHIPPED : /shipping/ship/submit (подписанное Ship-действие)
```

### orders.status (в контексте отгрузки)

```mermaid
stateDiagram-v2
    ASSEMBLED --> SHIPPED : Все products заказа имеют статус SHIPPED
    ASSEMBLED --> PARTIALLY_SHIPPED : Отгружена только часть products заказа
    PARTIALLY_SHIPPED --> SHIPPED : Остаток отгружен
```

### outbound_dispatches.status

```mermaid
stateDiagram-v2
    [*] --> SCHEDULED : Запись создана внешней ERP логистики
    SCHEDULED --> AT_GATE : /scan-driver (машина приехала)
    AT_GATE --> DEPARTED : Буфер опустел после отгрузки
    SCHEDULED --> CANCELLED : Рейс отменён
    AT_GATE --> CANCELLED : Рейс отменён
```

## Два режима отгрузки

### Bulk — «Отгрузить весь буфер» (по умолчанию)

В запросе `product_ids` отсутствует или пуст. Бэкенд берёт все products буфера со статусом `READY_TO_SHIP` и отгружает их одной подписанной операцией.

```json
POST /shipping/ship/draft
{
  "buffer_bin_id": "uuid-buffer-shop-5",
  "dispatch_id":  "uuid-dispatch"
}
```

Это основной сценарий: «приехала машина — забирает всё, что мы для неё собрали».

### Spot — «Отгрузить выбранные»

На случай, когда не всё влезает в кузов (праздники, крупный товар, ограничение по объёму). Оператор сканирует QR конкретных товаров, которые реально загружает в машину.

```json
POST /shipping/ship/draft
{
  "buffer_bin_id": "uuid-buffer-shop-5",
  "dispatch_id":  "uuid-dispatch",
  "product_ids":  ["uuid-1", "uuid-2", "uuid-3"]
}
```

Бэкенд валидирует, что каждый `product_ids[i]` действительно лежит в этом буфере со статусом `READY_TO_SHIP`. Оставшиеся в буфере товары ждут следующую машину. Черновик (`draft`) валидирует и режет операцию на части; фактическая отгрузка происходит после подписания на `POST /shipping/ship/submit`.

## Что меняется в БД

| Таблица | Операция | Что меняется |
|---------|----------|--------------|
| `bins` | SELECT | Проверяется `section = 'SHIPPING_BUFFER'` и читается `destination_id` |
| `products.status` | UPDATE | `READY_TO_SHIP -> SHIPPED` |
| `shippings` | INSERT | Запись об отгрузке: `product_id`, `dispatch_id`, `operator_id`, `shipped_at`, `action_id` — ссылка на подписанное Ship-действие |
| `warehouse_actions` | INSERT | Строка на каждую часть Ship-операции (draft): `action_id`, `item_ids`, `salt`, `challenge`, `details` |
| `orders.status` | UPDATE | `ASSEMBLED -> SHIPPED`/`PARTIALLY_SHIPPED`, когда часть/все products заказа `SHIPPED` |
| `outbound_dispatches` | SELECT / UPDATE | Поиск по `dispatch_code`, статусы `SCHEDULED -> AT_GATE -> DEPARTED` |
| `outbox_events` | INSERT | 1 строка на действие-часть при submit: `event_id = action_id`, `aggregate_type = 'signed_action'`, `event_type = 'wms.action.v1'`, `payload` = подписанный конверт |

## Outbox events

Создаются **только при `POST /shipping/ship/submit`**, в той же транзакции, что и отгрузка товаров — **по одной строке на действие-часть** (операция режется на части по ≤2000 товаров, ADR-0001 §10):

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

**Блокчейн:** Ledger Adapter публикует действия в `SignedMappingWMS.applyActions` -> проверка подписи (precompile P-256) -> FSM на каждый товар: переход `Picked -> Shipped`.

Это **финальный переход** в FSM контракта. После `Shipped` статус на блокчейне не меняется.

## Валидации

| Условие | Ошибка |
|---------|--------|
| `/scan-buffer`: `bin.section != 'SHIPPING_BUFFER'` | `BIN_NOT_SHIPPING_BUFFER` |
| `/scan-buffer`: bin не найден | `BIN_NOT_FOUND` |
| `/scan-buffer`: в буфере нет товаров `READY_TO_SHIP` | Не ошибка: `200` с пустым списком (`count: 0`). `BUFFER_EMPTY` бывает только у `/ship/draft` |
| `/scan-driver`: `dispatch_code` не найден | `DISPATCH_NOT_FOUND` |
| `/scan-driver`: `dispatch.status = DEPARTED` | `DISPATCH_ALREADY_DEPARTED` |
| `/scan-driver`: `dispatch.status = CANCELLED` | `DISPATCH_CANCELLED` (409) |
| `/scan-driver`: `dispatch.status = AT_GATE` | Не ошибка: повторный скан того же QR возвращает рейс без изменений (идемпотентно) |
| `/ship/draft`: `bin.destination_id != dispatch.destination_id` | `DESTINATION_MISMATCH` |
| `/ship/draft`: `dispatch.status != AT_GATE` | `DISPATCH_NOT_AT_GATE` |
| `/ship/draft`: один из `product_ids` не в буфере / не `READY_TO_SHIP` | `PRODUCT_NOT_IN_BUFFER` |
| `/ship/draft`: буфер пуст в bulk-режиме | `BUFFER_EMPTY` |
| `/ship/draft`: Pick-действие какого-то товара ещё не `COMMITTED` на цепи | `PICKING_NOT_CONFIRMED` (409) |
| `/ship/draft`, `/ship/submit`: у оператора нет активного passkey | `SIGNER_KEY_NOT_ACTIVE` (409) |
| `/ship/submit`: товар черновика ушёл из буфера или сменил статус после подписи | `DRAFT_STALE` (409) |
| `/ship/submit`: операция уже отправлена | `OPERATION_ALREADY_SUBMITTED` (409) |
| `/ship/submit`: подпись части не прошла проверку | `INVALID_ASSERTION` (400) |

`DESTINATION_MISMATCH` — главная защита от ошибки «не та машина на воротах». Сравниваются два `destination_id`: у буфера и у dispatch. Если не совпадает — ни один товар не отгружается.

## Ошибки блокчейна

В соответствии с решением на встрече: **не детализируем** типы on-chain ошибок. Если транзакция ревертнулась, `onchain_events.status = FAILED`, оператор видит общий индикатор «Транзакция не подтверждена. Обратитесь к администратору».

Подробная диагностика (причина реверта, failed eventId) доступна админу через `onchain_events` и логи Ledger Adapter. Оператор на воротах в реальном времени не обязан это разбирать.

## Валидация destination

В исходящем потоке `destination_id` становится сквозным ключом:

- `orders.destination_id` говорит, куда должен уехать заказ.
- `bins.destination_id` привязывает `SHIPPING_BUFFER` к магазину.
- `outbound_dispatches.destination_id` говорит, для какого магазина приехала машина.
- `assembly_tasks.destination_id` фиксирует магазин на уровне задачи сборки.
- `shippings.dispatch_id` фиксирует, в какой рейс реально погрузили товар.

За счёт этого backend может валидировать `order -> shipping_buffer -> dispatch` в одном контуре.

## Связь с процессом

```mermaid
flowchart LR
    subgraph GATE["1. КПП"]
        G["Скан TTN +\nгрузомест"]
    end

    subgraph TABLE["2. Стол приёмки"]
        T["Скан коробок -> ШК -> QR\n+ буфер приёмки"]
        T2["product = RECEIVED"]
    end

    subgraph PUTAWAY["3. Раскладка"]
        P["Буфер -> ячейка\nхранения"]
        P2["STORED"]
    end

    subgraph ASSEMBLY["4. Сборка"]
        A1["Аллокация\nALLOCATED"]
        A2["pick\nASSEMBLED"]
        A3["буфер магазина\nREADY_TO_SHIP"]
    end

    subgraph SHIPPING["5. Отгрузка"]
        S["scan-buffer -> scan-driver -> ship/draft → подпись → /ship/submit\nREADY_TO_SHIP -> SHIPPED"]
    end

    G --> T --> T2 --> P --> P2 --> A1 --> A2 --> A3 --> S

    style SHIPPING fill:#FF6347,stroke:#333
```

## On-chain FSM (параллельно)

```text
off-chain:  RECEIVED   STORED   ASSEMBLED/READY_TO_SHIP   SHIPPED
            ↓          ↓        ↓                         ↓
on-chain:   Accepted   PutAway  Picked                    Shipped
```

**4 on-chain перехода** на весь жизненный цикл — каждый выполняется одним подписанным действием (draft → passkey-подпись → submit → outbox → `wms.events.v1` → `SignedMappingWMS.applyActions`):

| Off-chain операция | Подписанное действие (action_type) | Контракт | On-chain переход |
|--------------------|-----------------------------|----------|------------------|
| Стол приёмки (`close-cargoplace`) | Receive (1) | `SignedMappingWMS.applyActions` | `None -> Accepted` |
| Раскладка (`scan-storage-bin`) | PutAway (2) | `SignedMappingWMS.applyActions` | `Accepted -> PutAway` |
| Сборка — выгрузка корзины (`scan-shipping-buffer`) | Pick (3) | `SignedMappingWMS.applyActions` | `PutAway -> Picked` |
| **Отгрузка — ship** | Ship (4) | `SignedMappingWMS.applyActions` | **`Picked -> Shipped`** |

Физический `pick` с полки (`/assembly/pick`) — off-chain only, подписанного действия не создаёт: Pick-действие уходит в цепь при выгрузке корзины в буфер.

## Связь со сборкой

```mermaid
flowchart TD
    subgraph ASM["4. Сборка"]
        A1["pick -> ASSEMBLED\n(без on-chain записи)"]
        A2["scan-shipping-buffer ->\nREADY_TO_SHIP\nбуфер магазина\nподписанное Pick-действие"]
    end

    subgraph SHIP["5. Отгрузка"]
        S1["scan-buffer ->\nсписок READY_TO_SHIP"]
        S2["scan-driver ->\nSCHEDULED -> AT_GATE"]
        S3["ship: draft -> подпись -> submit -> SHIPPED\nподписанное Ship-действие\nif буфер пуст:\nAT_GATE -> DEPARTED"]
    end

    A1 --> A2 --> S1 --> S2 --> S3

    style SHIP fill:#FF6347,stroke:#333
```
