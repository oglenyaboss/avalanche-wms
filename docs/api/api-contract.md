# API-контракт WMS

**Версия:** 1.0
**Дата:** 2026-03-31
**Базовый URL:** `http://localhost:8080/api/v1`

Все эндпоинты WMS-монолита. Каждый endpoint описан с request body, response, ошибками и побочными эффектами. Эндпоинты извлечены из flow-документации — реализация пока в TODO.

---

## Формат ответов

Все ответы используют единый конверт:

```json
{
  "success": true,
  "data": { ... },
  "error": null
}
```

При ошибке:

```json
{
  "success": false,
  "data": null,
  "error": {
    "code": "CARGOPLACE_NOT_IN_SHIPMENT",
    "message": "Грузоместо не принадлежит данной поставке"
  }
}
```

HTTP-коды: `200` — успех, `400` — ошибка валидации, `404` — не найдено, `409` — конфликт состояния, `500` — внутренняя ошибка.

---

## 1. Приёмка на КПП (Receiving Gate)

### POST /receiving/gate/scan-ttn

Начало приёмки поставки. Оператор сканирует штрихкод ТТН.

**Request:**
```json
{
  "ttn_code": "TTN-2026-001234"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "shipment_id": "uuid",
    "ttn_code": "TTN-2026-001234",
    "status": "GATE_IN_PROGRESS",
    "cargoplaces": [
      {
        "cargoplace_id": "uuid",
        "cargoplace_code": "CP-001",
        "status": "EXPECTED"
      },
      {
        "cargoplace_id": "uuid",
        "cargoplace_code": "CP-002",
        "status": "EXPECTED"
      }
    ],
    "total_cargoplaces": 10,
    "received_cargoplaces": 0
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `TTN_NOT_FOUND` | 404 | TTN не найден в БД |
| `SHIPMENT_ALREADY_CLOSED` | 409 | Поставка уже закрыта (GATE_CLOSED) |

**Побочные эффекты:**
- UPDATE `inbound_shipments.status` → GATE_IN_PROGRESS
- INSERT `receiving_gate` (action=SCAN_TTN)

---

### POST /receiving/gate/scan-cargoplace

Сканирование одного грузоместа в рамках поставки.

**Request:**
```json
{
  "shipment_id": "uuid",
  "cargoplace_code": "CP-001"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "cargoplace_id": "uuid",
    "cargoplace_code": "CP-001",
    "status": "RECEIVED_AT_GATE",
    "received_at_gate_at": "2026-03-31T10:15:00Z",
    "progress": {
      "total": 10,
      "received": 3,
      "remaining": 7
    }
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `CARGOPLACE_NOT_IN_SHIPMENT` | 400 | Грузоместо не принадлежит этой TTN |
| `CARGOPLACE_ALREADY_RECEIVED` | 409 | Грузоместо уже отсканировано |
| `SHIPMENT_NOT_IN_PROGRESS` | 409 | Поставка не в статусе GATE_IN_PROGRESS |

**Побочные эффекты:**
- UPDATE `cargoplaces.status` → RECEIVED_AT_GATE, `received_at_gate_at` = now()
- INSERT `receiving_gate` (action=SCAN_CARGOPLACE)

---

### POST /receiving/gate/accept-shipment

Завершение приёмки поставки. Неотсканированные грузоместа помечаются как NOT_RECEIVED.

**Request:**
```json
{
  "shipment_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "shipment_id": "uuid",
    "status": "GATE_CLOSED",
    "summary": {
      "total": 10,
      "received": 7,
      "not_received": 3
    }
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `SHIPMENT_NOT_IN_PROGRESS` | 409 | Поставка не в статусе GATE_IN_PROGRESS |

**Побочные эффекты:**
- UPDATE неотсканированные `cargoplaces.status` → NOT_RECEIVED
- UPDATE `inbound_shipments.status` → GATE_CLOSED
- INSERT `receiving_gate` (action=SHIPMENT_ACCEPTED)
- Outbox events: **нет** (товары ещё не существуют)

---

## 2. Приёмка на столе (Receiving Table)

### POST /receiving/table/scan-cargoplace

Открытие грузоместа для приёмки на столе.

**Request:**
```json
{
  "cargoplace_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "cargoplace_id": "uuid",
    "cargoplace_code": "CP-001",
    "status": "TABLE_IN_PROGRESS",
    "expected_skus": [
      {
        "sku_id": "uuid",
        "sku_name": "Ноутбук Lenovo X1",
        "expected_qty": 5
      }
    ],
    "total_expected": 12
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `CARGOPLACE_NOT_RECEIVED_AT_GATE` | 409 | Статус != RECEIVED_AT_GATE |

**Побочные эффекты:**
- UPDATE `cargoplaces.status` → TABLE_IN_PROGRESS
- INSERT `receiving_table` (action=SCAN_CARGOPLACE — опционально для лога)

---

### POST /receiving/table/scan-box

Сканирование штрихкода коробки внутри грузоместа.

**Request:**
```json
{
  "cargoplace_id": "uuid",
  "box_barcode": "BOX-A-001"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "box_id": "uuid",
    "box_barcode": "BOX-A-001",
    "status": "OPEN"
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `CARGOPLACE_NOT_IN_PROGRESS` | 409 | Грузоместо не открыто (status != TABLE_IN_PROGRESS) |

**Побочные эффекты:**
- INSERT `boxes` (status=OPEN) или UPDATE если повторный скан
- INSERT `receiving_table` (action=SCAN_BOX)

---

### POST /receiving/table/scan-sku

Сканирование штрихкода товара для определения SKU.

**Request:**
```json
{
  "cargoplace_id": "uuid",
  "box_id": "uuid",
  "barcode": "4607036430014"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "sku_id": "uuid",
    "sku_name": "Ноутбук Lenovo X1",
    "barcode": "4607036430014",
    "message": "Наклейте QR на товар"
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `BARCODE_NOT_FOUND` | 404 | Штрихкод не зарегистрирован в sku_barcodes |
| `BOX_NOT_OPEN` | 409 | Коробка не открыта |

**Побочные эффекты:**
- INSERT `receiving_table` (action=SCAN_SKU, sku_id)

---

### POST /receiving/table/scan-qr

Сканирование наклеенного QR-кода. **Создаёт product.**

**Request:**
```json
{
  "cargoplace_id": "uuid",
  "box_id": "uuid",
  "sku_id": "uuid",
  "qr_code": "WMS-QR-2026-00001234"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "product_id": "uuid",
    "sku_id": "uuid",
    "sku_name": "Ноутбук Lenovo X1",
    "qr_code": "WMS-QR-2026-00001234",
    "status": "RECEIVED",
    "progress": {
      "received_in_cargoplace": 5,
      "expected_in_cargoplace": 12
    }
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `QR_ALREADY_EXISTS` | 409 | QR-код уже зарегистрирован |
| `SKU_NOT_FOUND` | 404 | SKU не найден |

**Побочные эффекты:**
- INSERT `products` (sku_id, shipment_id, cargoplace_id, box_id, qr_code, status=RECEIVED)
- INSERT `receiving_table` (action=SCAN_QR, product_id)

---

### POST /receiving/table/close-box

Завершение работы с коробкой.

**Request:**
```json
{
  "box_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "box_id": "uuid",
    "status": "CLOSED",
    "products_in_box": 3
  }
}
```

**Побочные эффекты:**
- UPDATE `boxes.status` → CLOSED
- INSERT `receiving_table` (action=CLOSE_BOX)

---

### POST /receiving/table/scan-buffer

Сканирование ячейки буфера приёмки. Все products грузоместа размещаются в буфер.

**Request:**
```json
{
  "cargoplace_id": "uuid",
  "buffer_bin_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "buffer_bin_id": "uuid",
    "buffer_code": "T1-BUF",
    "products_placed": 10
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `BIN_NOT_FOUND` | 404 | Ячейка не существует |
| `BIN_NOT_BUFFER` | 400 | Ячейка не является буфером приёмки |

**Побочные эффекты:**
- UPDATE `products.bin_id` = buffer_bin_id WHERE cargoplace_id AND status=RECEIVED
- INSERT `receiving_table` (action=SCAN_BUFFER)

---

### POST /receiving/table/close-cargoplace

Завершение работы с грузоместом. Создаёт outbox events.

**Request:**
```json
{
  "cargoplace_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "cargoplace_id": "uuid",
    "status": "TABLE_CLOSED",
    "summary": {
      "products_received": 10,
      "products_expected": 12,
      "shortage": 2,
      "shortage_by_sku": [
        { "sku_name": "Ноутбук Lenovo X1", "expected": 5, "received": 4, "shortage": 1 },
        { "sku_name": "Мышь Logitech MX3", "expected": 3, "received": 2, "shortage": 1 }
      ]
    },
    "outbox_events_created": 10
  }
}
```

**Побочные эффекты (в одной транзакции):**
- UPDATE `cargoplaces.status` → TABLE_CLOSED
- INSERT `receiving_table` (action=CLOSE_CARGO)
- INSERT `outbox_events` × N (по 1 на product, aggregate_type='receiving')
- **Блокчейн:** события → Kafka → batchAccept → None → Accepted

---

## 3. Раскладка (Putaway)

### POST /putaway/scan-buffer

Сканирование ячейки буфера. Показывает товары, доступные для раскладки.

**Request:**
```json
{
  "buffer_bin_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "buffer_bin_id": "uuid",
    "buffer_code": "T1-BUF",
    "products": [
      {
        "product_id": "uuid",
        "sku_name": "Ноутбук Lenovo X1",
        "qr_code": "WMS-QR-2026-00001234",
        "status": "RECEIVED"
      }
    ],
    "total_products": 15
  }
}
```

**Побочные эффекты:** нет (только чтение).

---

### POST /putaway/scan-product

Сканирование товара для добавления в «корзину» раскладки.

**Request:**
```json
{
  "product_id": "uuid",
  "buffer_bin_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "product_id": "uuid",
    "sku_name": "Ноутбук Lenovo X1",
    "qr_code": "WMS-QR-2026-00001234",
    "cart_size": 3
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `PRODUCT_NOT_IN_BUFFER` | 409 | product.bin_id != buffer_bin_id |
| `PRODUCT_NOT_RECEIVED` | 409 | product.status != RECEIVED |

**Побочные эффекты:** товар добавлен в сессию раскладки (in-memory или временная таблица).

---

### POST /putaway/scan-storage-bin

Сканирование ячейки хранения. Размещает все товары из «корзины».

**Request:**
```json
{
  "product_ids": ["uuid-1", "uuid-2", "uuid-3"],
  "storage_bin_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "storage_bin_id": "uuid",
    "storage_bin_code": "M2-A-03",
    "products_placed": 3,
    "outbox_events_created": 3
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `BIN_NOT_FOUND` | 404 | Ячейка не существует |
| `PRODUCT_NOT_RECEIVED` | 409 | Один из товаров не в статусе RECEIVED |

**Побочные эффекты (в одной транзакции):**
- UPDATE `products` SET bin_id = storage_bin_id, status = STORED (для каждого product)
- INSERT `putaways` (product_id, from_bin_id, bin_id, operator_id)
- INSERT `outbox_events` × N (aggregate_type='putaway')
- **Блокчейн:** события → Kafka → batchPutAway → Accepted → PutAway

---

## 4. Сборка (Assembly)

### POST /assembly/allocate

Аллокация: система назначает конкретные products на заказ.

**Request:**
```json
{
  "order_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "order_id": "uuid",
    "status": "ALLOCATED",
    "tasks": [
      {
        "assembly_task_id": "bigint",
        "product_id": "uuid",
        "sku_name": "Ноутбук Lenovo X1",
        "from_bin_code": "M2-A-03",
        "section": "MEZZANINE"
      }
    ],
    "total_tasks": 5
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `ORDER_NOT_NEW` | 409 | Заказ уже аллоцирован |
| `INSUFFICIENT_STOCK` | 409 | Не хватает товаров в статусе STORED |

**Побочные эффекты:**
- UPDATE `products` SET status = ALLOCATED, order_id = order_id
- INSERT `assembly_tasks` (order_id, product_id, sku_id, from_bin_id, status=PENDING)
- UPDATE `orders.status` → ALLOCATED
- Outbox events: **нет** (аллокация не пишет в блокчейн)

---

### GET /assembly/tasks

Получение списка задач сборки для заказа.

**Query params:** `?order_id=uuid`

**Response (200):**
```json
{
  "success": true,
  "data": {
    "order_id": "uuid",
    "tasks": [
      {
        "assembly_task_id": "bigint",
        "product_id": "uuid",
        "sku_name": "Ноутбук Lenovo X1",
        "qr_code": "WMS-QR-2026-00001234",
        "from_bin_code": "M2-A-03",
        "section": "MEZZANINE",
        "status": "PENDING"
      }
    ],
    "total": 5,
    "done": 2,
    "remaining": 3
  }
}
```

---

### POST /assembly/pick

Подбор одного товара. Создаёт outbox event.

**Request:**
```json
{
  "assembly_task_id": "bigint"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "assembly_task_id": "bigint",
    "product_id": "uuid",
    "status": "DONE",
    "progress": {
      "total": 5,
      "done": 3,
      "remaining": 2
    },
    "order_status": "ASSEMBLY_IN_PROGRESS"
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `TASK_NOT_PENDING` | 409 | Задача уже выполнена или отменена |

**Побочные эффекты (в одной транзакции):**
- UPDATE `assembly_tasks` SET status = DONE, onchain_status = PENDING_ONCHAIN
- UPDATE `products.status` → ASSEMBLED
- INSERT `outbox_events` (aggregate_type='picking')
- UPDATE `orders.status` → ASSEMBLY_IN_PROGRESS или ASSEMBLED (если все tasks DONE)
- **Блокчейн:** событие → Kafka → batchPick → PutAway → Picked

---

## 5. Отгрузка (Shipping)

### GET /shipping/orders

Список заказов, готовых к отгрузке.

**Query params:** `?status=ASSEMBLED,READY_TO_SHIP`

**Response (200):**
```json
{
  "success": true,
  "data": {
    "orders": [
      {
        "order_id": "uuid",
        "external_order_no": "ORD-2026-5678",
        "status": "ASSEMBLED",
        "products_count": 5
      }
    ]
  }
}
```

---

### POST /shipping/verify

Верификация товара при отгрузке (сканирование QR).

**Request:**
```json
{
  "order_id": "uuid",
  "product_id": "uuid"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "product_id": "uuid",
    "verified": true,
    "progress": {
      "total": 5,
      "verified": 3,
      "remaining": 2
    }
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `PRODUCT_NOT_IN_ORDER` | 400 | Товар не принадлежит заказу |
| `PRODUCT_NOT_ASSEMBLED` | 409 | Товар не в статусе ASSEMBLED |

---

### POST /shipping/ship

Отгрузка заказа. Создаёт outbox events для каждого товара.

**Request:**
```json
{
  "order_id": "uuid",
  "vehicle_number": "А123БВ777"
}
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "order_id": "uuid",
    "status": "SHIPPED",
    "vehicle_number": "А123БВ777",
    "products_shipped": 5,
    "outbox_events_created": 5
  }
}
```

**Ошибки:**
| Код | HTTP | Когда |
|-----|------|-------|
| `ORDER_NOT_ASSEMBLED` | 409 | Заказ не в статусе ASSEMBLED / READY_TO_SHIP |
| `VEHICLE_NUMBER_REQUIRED` | 400 | Не указан номер ТС |

**Побочные эффекты (в одной транзакции):**
- UPDATE `products.status` → SHIPPED (для всех products заказа)
- INSERT `shippings` (product_id, vehicle_number, operator_id) × N
- UPDATE `orders.status` → SHIPPED
- INSERT `outbox_events` × N (aggregate_type='shipping')
- **Блокчейн:** события → Kafka → batchShip → Picked → Shipped

---

## Сводка всех эндпоинтов

| Метод | Путь | Этап | Outbox | Блокчейн |
|-------|------|------|--------|----------|
| POST | /receiving/gate/scan-ttn | КПП | — | — |
| POST | /receiving/gate/scan-cargoplace | КПП | — | — |
| POST | /receiving/gate/accept-shipment | КПП | — | — |
| POST | /receiving/table/scan-cargoplace | Стол | — | — |
| POST | /receiving/table/scan-box | Стол | — | — |
| POST | /receiving/table/scan-sku | Стол | — | — |
| POST | /receiving/table/scan-qr | Стол | — | — |
| POST | /receiving/table/scan-buffer | Стол | — | — |
| POST | /receiving/table/close-box | Стол | — | — |
| POST | /receiving/table/close-cargoplace | Стол | receiving | batchAccept |
| POST | /putaway/scan-buffer | Раскладка | — | — |
| POST | /putaway/scan-product | Раскладка | — | — |
| POST | /putaway/scan-storage-bin | Раскладка | putaway | batchPutAway |
| POST | /assembly/allocate | Сборка | — | — |
| GET | /assembly/tasks | Сборка | — | — |
| POST | /assembly/pick | Сборка | picking | batchPick |
| GET | /shipping/orders | Отгрузка | — | — |
| POST | /shipping/verify | Отгрузка | — | — |
| POST | /shipping/ship | Отгрузка | shipping | batchShip |
