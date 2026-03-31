# Раскладка (Putaway)

## Суть

Перенос товаров из буфера приемки в ячейки хранения на мезонине. Работник физически забирает товары из буфера и раскладывает по полкам.

**Ключевое:** в MVP нет системных ограничений на то, куда класть товар. Работник сам решает.

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    actor Op as Оператор раскладки
    participant UI as UI (Scanner/ТСД)
    participant WMS as WMS API
    participant PG as Postgres

    Note over Op,PG: === Шаг 1: Сканирование буфера приемки ===
    Op->>UI: Подходит к буферу, сканирует ячейку буфера
    UI->>WMS: POST /putaway/scan-buffer {buffer_bin_id}
    WMS->>PG: SELECT products WHERE bin_id = buffer_bin_id AND status = RECEIVED
    WMS-->>UI: Список товаров в буфере (15 шт.)

    Note over Op,PG: === Шаг 2: Сканирование товаров (забирает с собой) ===
    loop Для каждого товара, который берёт
        Op->>UI: Сканирует QR товара
        UI->>WMS: POST /putaway/scan-product {product_id, buffer_bin_id}
        WMS->>WMS: Проверка: product.bin_id == buffer_bin_id? status == RECEIVED?
        WMS-->>UI: Товар добавлен в "корзину" раскладки (взято: 3)
    end

    Note over Op: Физически кладёт товары на тележку и идёт к мезонину

    Note over Op,PG: === Шаг 3: Сканирование ячейки хранения ===
    Op->>UI: Сканирует ячейку на мезонине
    UI->>WMS: POST /putaway/scan-storage-bin {product_ids[], storage_bin_id}
    WMS->>WMS: Валидация: ячейка существует, товары в "корзине"

    loop Для каждого товара в "корзине"
        WMS->>PG: UPDATE products SET bin_id = storage_bin_id, status = STORED
        WMS->>PG: INSERT putaways (product_id, bin_id, operator_id, onchain_status=PENDING_ONCHAIN)
    end

    loop Для каждого товара
        WMS->>PG: INSERT outbox_events (event_id, aggregate_id=product_id, aggregate_type='putaway')
    end
    Note over WMS,PG: N outbox events → Kafka → batchPutAway → Accepted → PutAway
    WMS->>PG: COMMIT
    WMS-->>UI: Размещено 3 товара в ячейку M2-A-03 ✓

    Note over Op: Может вернуться к буферу и продолжить раскладку оставшихся товаров
```

## Физический процесс (простая схема)

```mermaid
flowchart LR
    subgraph BUFFER["Буфер приемки (у стола)"]
        B1["📦 QR-001\n📦 QR-002\n📦 QR-003\n📦 QR-004\n📦 QR-005"]
    end

    subgraph WORKER["Работник"]
        W1["🛒 Тележка\n📦 QR-001\n📦 QR-002\n📦 QR-003"]
    end

    subgraph MEZZANINE["Мезонин (хранение)"]
        S1["Ячейка M2-A-03\n📦 QR-001\n📦 QR-002"]
        S2["Ячейка M2-B-07\n📦 QR-003"]
    end

    B1 -->|"1. Скан буфера\n2. Скан товаров"| W1
    W1 -->|"3. Скан ячейки\n→ товары размещены"| S1
    W1 -->|"3. Скан другой ячейки\n→ товар размещён"| S2
```

## Что меняется в БД

```mermaid
stateDiagram-v2
    direction LR
    state "products" as P {
        RECEIVED : RECEIVED (bin_id = buffer_bin)
        STORED : STORED (bin_id = storage_bin)
        RECEIVED --> STORED : Раскладка
    }
```

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `products` | UPDATE | bin_id: buffer → storage, status: RECEIVED → STORED |
| `putaways` | INSERT | Запись о размещении (product_id, bin_id, operator_id) |
| `outbox_events` | INSERT | 1 event per product (aggregate_id=product_id, aggregate_type='putaway') |

## Почему "нет системных ограничений"

В MVP система **не проверяет**:
- Вместимость ячейки (`bins.volume` vs сумма `skus.volume`)
- Совместимость SKU в одной ячейке
- Принадлежность ячейки к определённой зоне
- Максимальный вес полки

Работник опирается на своё знание склада. В post-MVP можно добавить правила размещения (putaway rules).

## Связь с предыдущими этапами

```mermaid
flowchart TD
    subgraph GATE["1. КПП"]
        G1["Скан TTN → Скан грузомест\nРезультат: cargoplace.status = RECEIVED_AT_GATE"]
    end

    subgraph TABLE["2. Стол приемки"]
        T1["Скан грузоместа → Скан коробок → Скан ШК → Скан QR → Скан буфера\nРезультат: product создан, bin_id = buffer"]
    end

    subgraph PUTAWAY["3. Раскладка"]
        P1["Скан буфера → Скан товаров → Скан ячейки хранения\nРезультат: product.bin_id = storage, status = STORED"]
    end

    subgraph ASSEMBLY["4. Сборка (будущее)"]
        A1["Назначение заказа → Подбор товаров\nproduct.status: STORED → ALLOCATED → ASSEMBLED"]
    end

    GATE --> TABLE
    TABLE --> PUTAWAY
    PUTAWAY --> ASSEMBLY

    style PUTAWAY fill:#FFD700,stroke:#333
```
