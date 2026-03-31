# Приемка на столе

## Суть

Работа с содержимым грузоместа: вскрываем, сканируем коробки, сканируем товары по штрихкоду (определяем SKU), клеим и сканируем уникальный QR, затем отправляем всё в буфер.

**Важно:** именно на этом этапе создаётся `product` — запись о реально существующей единице товара. До этого были только ожидаемые количества (`expected_cargoplace_skus`).

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    actor Op as Оператор стола
    participant UI as UI (Scanner)
    participant WMS as WMS API
    participant PG as Postgres

    Note over Op,PG: === Шаг 1: Начало работы с грузоместом ===
    Op->>UI: Сканирует грузоместо (QR/штрихкод)
    UI->>WMS: POST /receiving/table/scan-cargoplace {cargoplace_id}
    WMS->>WMS: Проверка: cargoplace.status == RECEIVED_AT_GATE?
    WMS->>PG: UPDATE cargoplace.status = TABLE_IN_PROGRESS
    WMS-->>UI: Грузоместо открыто, ожидается X единиц товара

    Note over Op,PG: === Шаг 2: Сканирование коробки ===
    Op->>UI: Сканирует штрихкод коробки
    UI->>WMS: POST /receiving/table/scan-box {cargoplace_id, box_barcode}
    WMS->>PG: INSERT/UPDATE boxes (status=OPEN)
    WMS->>PG: INSERT receiving_table (action=SCAN_BOX)
    WMS-->>UI: Коробка открыта, сканируйте товары

    Note over Op,PG: === Шаг 3: Цикл сканирования товаров ===
    loop Для каждого товара в коробке
        Note over Op,UI: 3a. Сканирование ШК товара (определяем SKU)
        Op->>UI: Сканирует штрихкод товара
        UI->>WMS: POST /receiving/table/scan-sku {cargoplace_id, box_id, barcode}
        WMS->>WMS: Определяем SKU по штрихкоду
        WMS->>PG: INSERT receiving_table (action=SCAN_SKU, sku_id)
        WMS-->>UI: Найден SKU: "Ноутбук Lenovo X1". Наклейте QR.

        Note over Op,UI: 3b. Генерация и сканирование QR (создание product)
        Op->>UI: Сканирует наклеенный QR
        UI->>WMS: POST /receiving/table/scan-qr {cargoplace_id, box_id, sku_id, qr_code}
        WMS->>PG: INSERT products (sku_id, shipment_id, cargoplace_id, box_id, qr_code, status=RECEIVED)
        WMS->>PG: INSERT receiving_table (action=SCAN_QR, product_id)
        WMS-->>UI: Товар зарегистрирован ✓ (принято: 5/12)
    end

    Note over Op,PG: === Шаг 4: Завершение коробки ===
    alt Все товары в коробке отсканированы
        Op->>UI: Нажимает "Завершить работу с коробкой"
    else Есть неотсканированные товары
        Op->>UI: Нажимает "Завершить работу с коробкой"
        Note over WMS: Неотсканированные товары = недостача (нет product записей)
    end
    UI->>WMS: POST /receiving/table/close-box {box_id}
    WMS->>PG: UPDATE boxes.status = CLOSED
    WMS->>PG: INSERT receiving_table (action=CLOSE_BOX)
    WMS-->>UI: Коробка закрыта

    Note over Op,PG: === Шаг 5: Сканирование буфера ===
    Op->>UI: Сканирует ячейку буфера приемки
    UI->>WMS: POST /receiving/table/scan-buffer {cargoplace_id, buffer_bin_id}
    WMS->>PG: UPDATE products SET bin_id = buffer_bin_id WHERE cargoplace_id AND status=RECEIVED
    WMS->>PG: INSERT receiving_table (action=SCAN_BUFFER)
    WMS-->>UI: 10 товаров размещены в буфере T1-BUF ✓

    Note over Op,PG: === Шаг 6: Завершение грузоместа ===
    Op->>UI: Нажимает "Завершить работу с грузоместом"
    UI->>WMS: POST /receiving/table/close-cargoplace {cargoplace_id}
    WMS->>WMS: Сверка: expected_cargoplace_skus vs реальные products
    WMS->>PG: UPDATE cargoplace.status = TABLE_CLOSED
    WMS->>PG: INSERT receiving_table (action=CLOSE_CARGO)
    WMS->>PG: SELECT product_id FROM products WHERE cargoplace_id = :cargoplace_id
    loop Для каждого product в грузоместе
        WMS->>PG: INSERT outbox_events (event_id, aggregate_id=product_id, aggregate_type='receiving')
    end
    Note over WMS,PG: N outbox events (по одному на product) → Kafka → batchAccept → None → Accepted
    WMS-->>UI: Грузоместо закрыто (принято: 10, ожидалось: 12, недостача: 2)
```

## Структура вложенности

```mermaid
graph TD
    TTN["TTN (inbound_shipment)"] --> CP1["Грузоместо 1"]
    TTN --> CP2["Грузоместо 2"]
    CP1 --> BOX1["Коробка A"]
    CP1 --> BOX2["Коробка B"]
    BOX1 --> P1["Товар (product) — QR001"]
    BOX1 --> P2["Товар (product) — QR002"]
    BOX1 --> P3["Товар (product) — QR003"]
    BOX2 --> P4["Товар (product) — QR004"]
    CP2 --> BOX3["Коробка C"]
    BOX3 --> P5["Товар (product) — QR005"]

    style P1 fill:#90EE90
    style P2 fill:#90EE90
    style P3 fill:#90EE90
    style P4 fill:#90EE90
    style P5 fill:#90EE90
```

## Сверка недостачи

```
expected_cargoplace_skus: SKU-A × 5, SKU-B × 3  (итого 8 ожидается)
products (реально создано): SKU-A × 4, SKU-B × 3  (итого 7 принято)
→ Недостача: SKU-A × 1
```

Недостача фиксируется **неявно** — если `expected_qty > count(products)` для данного SKU в данном грузоместе.

## Какие таблицы затрагиваются

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `cargoplaces` | UPDATE | status: RECEIVED_AT_GATE → TABLE_IN_PROGRESS → TABLE_CLOSED |
| `boxes` | INSERT/UPDATE | Создание коробки (OPEN), закрытие (CLOSED) |
| `products` | INSERT | **Создаётся при SCAN_QR** — единица товара с уникальным QR |
| `products` | UPDATE | bin_id заполняется при SCAN_BUFFER |
| `receiving_table` | INSERT | Лог каждого действия (append-only) |
| `expected_cargoplace_skus` | READ | Для сверки ожидаемого vs фактического |
| `outbox_events` | INSERT | **N events** при CLOSE_CARGO (по одному на product, aggregate_id=product_id, aggregate_type='receiving') |
