# ER-диаграмма: связи между таблицами

**Версия:** 1.0
**Дата:** 2026-03-31

---

## Полная ER-диаграмма

```mermaid
erDiagram
    users {
        uuid user_id PK
        text username
        text password_hash
        user_role role
        boolean is_active
        timestamptz created_at
        timestamptz updated_at
    }

    evm_addresses {
        bigserial id PK
        uuid user_id FK
        text evm_address
        text onchain_role
        timestamptz created_at
        timestamptz updated_at
    }

    warehouses {
        bigserial warehouse_id PK
        text name
        text address
        text contact
        timestamptz created_at
        timestamptz updated_at
    }

    bins {
        uuid bin_id PK
        bigint warehouse_id FK
        uuid destination_id FK
        text code
        text section
        numeric volume
        timestamptz created_at
        timestamptz updated_at
    }

    skus {
        uuid sku_id PK
        text name
        text description
        numeric volume
        timestamptz created_at
        timestamptz updated_at
    }

    sku_barcodes {
        bigserial id PK
        uuid sku_id FK
        text barcode
        timestamptz created_at
    }

    destinations {
        uuid destination_id PK
        text code
        text name
        text address
        bigint warehouse_id FK
        timestamptz created_at
        timestamptz updated_at
    }

    inbound_shipments {
        uuid shipment_id PK
        bigint warehouse_id FK
        text ttn_code
        text status
        timestamptz created_at
        timestamptz updated_at
    }

    cargoplaces {
        uuid cargoplace_id PK
        uuid shipment_id FK
        text cargoplace_code
        text status
        timestamptz received_at_gate_at
        timestamptz created_at
        timestamptz updated_at
    }

    expected_cargoplace_skus {
        bigserial id PK
        uuid cargoplace_id FK
        uuid sku_id FK
        int expected_qty
    }

    boxes {
        uuid box_id PK
        uuid cargoplace_id FK
        text box_barcode
        text status
        timestamptz created_at
        timestamptz updated_at
    }

    products {
        uuid product_id PK
        uuid sku_id FK
        uuid shipment_id FK
        uuid cargoplace_id FK
        uuid box_id FK
        text qr_code
        uuid bin_id FK
        uuid order_id FK
        text status
        timestamptz created_at
        timestamptz updated_at
    }

    orders {
        uuid order_id PK
        text external_order_no
        uuid customer_id FK
        bigint warehouse_id FK
        uuid destination_id FK
        text status
        timestamptz created_at
        timestamptz updated_at
    }

    order_lines {
        bigserial id PK
        uuid order_id FK
        uuid sku_id FK
        integer qty
    }

    outbound_dispatches {
        uuid dispatch_id PK
        text dispatch_code
        bigint warehouse_id FK
        uuid destination_id FK
        text vehicle_number
        text driver_name
        text driver_phone
        text status
        timestamptz scheduled_at
        timestamptz arrived_at
        timestamptz departed_at
        timestamptz created_at
        timestamptz updated_at
    }

    outbox_events {
        bigserial id PK
        uuid event_id
        uuid aggregate_id
        text aggregate_type
        text event_type
        text payload_hash
        timestamptz created_at
    }

    onchain_events {
        bigserial id PK
        uuid event_id
        text aggregate_type
        text tx_hash
        text status
        text error_message
        timestamptz created_at
        timestamptz updated_at
    }

    receiving_gate {
        bigserial id PK
        text ttn_code
        text cargoplace_code
        uuid event_id
        uuid shipment_id FK
        uuid cargoplace_id FK
        uuid operator_id FK
        text action
        timestamptz occurred_at
        timestamptz created_at
    }

    receiving_table {
        bigserial id PK
        uuid event_id
        uuid cargoplace_id FK
        uuid box_id FK
        uuid operator_id FK
        text action
        text box_barcode
        uuid sku_id FK
        text qr_code
        uuid product_id FK
        uuid buffer_bin_id FK
        timestamptz occurred_at
        timestamptz created_at
    }

    putaways {
        bigserial id PK
        uuid event_id
        uuid product_id FK
        uuid from_bin_id FK
        uuid bin_id FK
        uuid operator_id FK
        text onchain_status
        text onchain_tx_hash
        timestamptz occurred_at
        timestamptz created_at
    }

    assembly_tasks {
        bigserial id PK
        uuid event_id
        uuid order_id FK
        uuid product_id FK
        uuid sku_id FK
        uuid from_bin_id FK
        uuid destination_id FK
        text section
        text status
        text onchain_status
        text onchain_tx_hash
        uuid operator_id FK
        timestamptz occurred_at
        timestamptz created_at
        timestamptz updated_at
    }

    shippings {
        bigserial id PK
        uuid event_id
        uuid product_id FK
        uuid operator_id FK
        uuid dispatch_id FK
        text onchain_status
        text onchain_tx_hash
        timestamptz shipped_at
        timestamptz occurred_at
        timestamptz created_at
    }

    %% Relationships
    users ||--o{ evm_addresses : "has"
    warehouses ||--o{ bins : "contains"
    warehouses ||--o{ inbound_shipments : "receives"
    warehouses ||--o{ orders : "fulfills"
    warehouses ||--o{ destinations : "serves"
    destinations ||--o{ orders : "delivers to"
    destinations ||--o{ bins : "assigned to"
    destinations ||--o{ outbound_dispatches : "destination of"
    destinations ||--o{ assembly_tasks : "destination of"
    skus ||--o{ sku_barcodes : "identified by"
    skus ||--o{ products : "instantiated as"
    skus ||--o{ expected_cargoplace_skus : "expected in"
    skus ||--o{ order_lines : "referenced in"
    inbound_shipments ||--o{ cargoplaces : "contains"
    inbound_shipments ||--o{ products : "originated from"
    cargoplaces ||--o{ boxes : "contains"
    cargoplaces ||--o{ products : "unpacked into"
    cargoplaces ||--o{ expected_cargoplace_skus : "expects"
    boxes ||--o{ products : "contains"
    bins ||--o{ products : "stores"
    orders ||--o{ products : "includes"
    orders ||--o{ assembly_tasks : "requires"
    orders ||--o{ order_lines : "contains"
    outbound_dispatches ||--o{ shippings : "used in"
    products ||--o{ putaways : "placed by"
    products ||--o{ assembly_tasks : "picked in"
    products ||--o{ shippings : "shipped in"
    users ||--o{ receiving_gate : "operates"
    users ||--o{ receiving_table : "operates"
    users ||--o{ putaways : "operates"
    users ||--o{ assembly_tasks : "operates"
    users ||--o{ shippings : "operates"
    users ||--o{ orders : "ordered by"
```

---

## Группировка по схемам PostgreSQL

### wms_inventory (справочники и состояние)

```mermaid
graph TD
    subgraph wms_inventory
        warehouses --> bins
        warehouses --> inbound_shipments
        warehouses --> orders
        warehouses --> destinations
        skus --> sku_barcodes
        skus --> products
        skus --> expected_cargoplace_skus
        skus --> order_lines
        inbound_shipments --> cargoplaces
        cargoplaces --> boxes
        cargoplaces --> expected_cargoplace_skus
        cargoplaces --> products
        boxes --> products
        bins --> products
        orders --> products
        orders --> order_lines
        destinations --> orders
        destinations --> bins
        destinations --> outbound_dispatches
        destinations --> assembly_tasks
        outbound_dispatches --> shippings
    end
```

**Назначение:** текущее состояние склада. Где какой товар, в каком статусе, к какому заказу привязан.

### wms_ops (журналы операций)

```mermaid
graph TD
    subgraph wms_ops
        receiving_gate["receiving_gate\n(КПП, append-only)"]
        receiving_table["receiving_table\n(стол приёмки, append-only)"]
        putaways["putaways\n(раскладка, append-only)"]
        assembly_tasks["assembly_tasks\n(сборка, обновляемый)"]
        shippings["shippings\n(отгрузка, append-only)"]
    end
```

**Назначение:** журнал всех операций. Кто, когда, что сделал. Операции (кроме assembly_tasks) — append-only.

### public (общие + события)

```mermaid
graph TD
    subgraph public_schema["public"]
        users --> evm_addresses
        outbox_events["outbox_events\n(WMS → Kafka)"]
        onchain_events["onchain_events\n(Kafka → Blockchain)"]
    end
```

**Назначение:** пользователи, EVM-адреса, интеграционные таблицы.

---

## Ключевые связи

### Путь данных: TTN → product

```
inbound_shipments (TTN)
  └── cargoplaces (грузоместо)
        ├── expected_cargoplace_skus (ожидаемые SKU × qty)
        ├── boxes (коробки)
        │     └── products (товары, bin_id → bins)
        └── products (товары без коробки — теоретически)
```

### Путь данных: order → shipment

```
orders (заказ → destination_id)
  ├── order_lines (строки заказа: SKU × qty)
  └── products (order_id назначен при аллокации)
        └── assembly_tasks (задачи подбора → destination_id)
              └── shippings (записи отгрузки → dispatch_id)
                    └── outbound_dispatches (рейс, содержит vehicle_number)
```

### Связь off-chain ↔ on-chain

```
products.product_id
  → outbox_events.aggregate_id (= product_id)
    → Kafka message key (= product_id UUID string)
      → onchain_events.event_id (= outbox_events.event_id)
        → BatchMappingWMS.itemStatus[uint256(keccak256(product_id))]
```

---

## Уникальные ограничения

| Таблица | Constraint | Назначение |
|---------|-----------|------------|
| `inbound_shipments` | UNIQUE(ttn_code) | Один TTN = одна поставка |
| `cargoplaces` | UNIQUE(shipment_id, cargoplace_code) | Грузоместо уникально в рамках TTN |
| `boxes` | UNIQUE(cargoplace_id, box_barcode) | Коробка уникальна в рамках грузоместа |
| `products` | UNIQUE(qr_code) | QR глобально уникален |
| `sku_barcodes` | UNIQUE(barcode) | Штрихкод глобально уникален |
| `skus` | UNIQUE(name) | Название SKU уникально |
| `destinations` | UNIQUE(code) | Код магазина-получателя глобально уникален |
| `order_lines` | UNIQUE(order_id, sku_id) | Одна позиция SKU в заказе уникальна |
| `outbound_dispatches` | UNIQUE(dispatch_code) | Код рейса глобально уникален |
| `onchain_events` | UNIQUE(event_id) | Идемпотентность Ledger Adapter |
| `expected_cargoplace_skus` | UNIQUE(cargoplace_id, sku_id) | Один SKU на грузоместо |

### CHECK-ограничения и индексы

| Таблица | Constraint / Index | Назначение |
|---------|-------------------|------------|
| `bins` | CHECK `bins_shipping_buffer_requires_destination` | Если `section = 'SHIPPING_BUFFER'`, то `destination_id IS NOT NULL` |
| `bins` | partial-индекс `idx_bins_destination_id` | Ускоряет поиск по `destination_id` только для ячеек `SHIPPING_BUFFER` |
| `order_lines` | CHECK `order_lines_qty_positive` | `qty > 0` |
