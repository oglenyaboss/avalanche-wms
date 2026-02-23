
---

# **1. Основные данные / таблицы текущего состояния（wms_inventory）**

---

## `skus`（основные данные о товарах）

| Имя поля         | Тип          | Описание     |
| ----------- | ----------- | ------ |
| sku_id      | uuid (PK)   | Первичный ключ SKU |
| name        | text        | Наименование товара |
| description | text        | Описание товара |
| volume      | numeric     | Единица: литры |
| created_at  | timestamptz | Время создания |
| updated_at  | timestamptz | Время последнего обновления |


## `warehouses`（таблица складов）

| Имя поля          | Тип            | Описание                   |
| ------------ | ------------- | -------------------- |
| warehouse_id | bigserial(PK) | ID первичного ключа склада |
| name         | text          | Название склада |
| address      | text          | Адрес склада |
| contact      | text          | Контактная информация |
| created_at   | timestamptz   | Время создания |
| updated_at   | timestamptz   | Время обновления |


## `bins`（ячейки склада）

| Имя поля          | Тип                       | Описание                        |
| ------------ | ------------------------ | ------------------------- |
| bin_id       | uuid (PK)                | Первичный ключ ячейки |
| warehouse_id | bigint (FK - warehouses) | Принадлежит складу |
| code         | text                     | Код ячейки |
| section      | text                     | Зона (RECEIVING_BUFFER_T1 и т. д.) |
| volume       | numeric                  | Объем |
| created_at   | timestamptz              | Время создания |
| updated_at   | timestamptz              | Время обновления |
В section добавлено назначение буферной зоны


## `inbound_shipments`（TTN）

| Имя поля          | Тип                       | Описание                                       |
| ------------ | ------------------------ | ---------------------------------------- |
| shipment_id  | uuid (PK)                | Первичный ключ TTN |
| warehouse_id | bigint (FK - warehouses) | Принадлежит складу |
| ttn_code     | text UNIQUE              | Номер транспортной накладной |
| status       | text                     | CREATED / GATE_IN_PROGRESS / GATE_CLOSED |
| created_at   | timestamptz              | Время создания |
| updated_at   | timestamptz              | Время обновления |

- Представляет одну полную перевозку (TTN)
- Верхнеуровневая сущность этапа КПП
- GATE_CLOSED устанавливается только после «поставка принята»

## `cargoplaces`（транспортная единица）

| Имя поля                 | Тип                            | Описание                                                                            |
| ------------------- | ----------------------------- | ----------------------------------------------------------------------------- |
| cargoplace_id       | uuid (PK)                     | ID транспортной единицы |
| shipment_id         | uuid (FK - inbound_shipments) | К какой TTN относится |
| cargoplace_code     | text                          | Отсканированный код транспортной единицы |
| status              | text                          | EXPECTED / RECEIVED_AT_GATE / NOT_RECEIVED / TABLE_IN_PROGRESS / TABLE_CLOSED |
| received_at_gate_at | timestamptz                   | Время приема на КПП |
| created_at          | timestamptz                   | Время создания |
| updated_at          | timestamptz                   | Время обновления |
- `UNIQUE(shipment_id, cargoplace_code)`
- На КПП принимаемой сущностью является cargoplace
- Только cargoplace в статусе RECEIVED_AT_GATE может перейти к приемке на столе
- Статус EXPECTED используется для поддержки сценария «не отсканировано = не получено»


## `boxes`（короб）

|Имя поля|Тип|Описание|
|---|---|---|
|box_id|uuid (PK)|ID короба|
|cargoplace_id|uuid (FK - cargoplaces)|К какой транспортной единице относится|
|box_barcode|text|Штрихкод короба|
|status|text|OPEN / CLOSED|
|created_at|timestamptz|Время создания|
|updated_at|timestamptz|Время обновления|
- `UNIQUE(cargoplace_id, box_barcode)`
- В одном cargoplace может быть несколько box
- Не является единицей запаса, используется только для структурного управления


## `expected_cargoplace_skus`

| Имя поля           | Тип                      | Описание  |
| ------------- | ----------------------- | --- |
| id            | bigserial (PK)          |     |
| cargoplace_id | uuid (FK - cargoplaces) |     |
| sku_id        | uuid (FK - skus)        |     |
| expected_qty  | int                     |     |
- `UNIQUE(cargoplace_id, sku_id)`
- Обозначает «сколько единиц определенного SKU ожидается в данной транспортной единице»
- Используется для сверки недостач при приемке на столе
- Существует только как плановое/ожидаемое количество


## `orders`（заказы）

| Имя поля               | Тип                       | Описание           |
| ----------------- | ------------------------ | ------------ |
| order_id          | uuid (PK)                | Первичный ключ заказа |
| external_order_no | text                     | Номер внешнего заказа (ERP и т. д.) |
| customer_id       | uuid (FK - users)        | ID клиента |
| warehouse_id      | bigint (FK - warehouses) | Принадлежит складу |
| status            | text                     | Статус заказа |
| created_at        | timestamptz              | Время создания |
| updated_at        | timestamptz              | Время обновления |
**Перечень значений `status`:**
- NEW（новый заказ）
- ALLOCATED（для заказа уже выделены конкретные products）
- ASSEMBLY_IN_PROGRESS（комплектация в процессе）
- ASSEMBLED（комплектация завершена）
- READY_TO_SHIP（ожидает отгрузки）
- SHIPPED（отгружен）

- Когда у заказа есть любой product в `ALLOCATED` и при этом еще есть не созданные задачи комплектации, заказ находится в `ALLOCATED`;
- Когда у заказа есть product, который сейчас комплектуется, заказ находится в `ASSEMBLY_IN_PROGRESS`;
- Когда все products заказа находятся в `ASSEMBLED` / `READY_TO_SHIP` и еще не отгружены, заказ переходит в `READY_TO_SHIP`;
- Когда все products достигают `SHIPPED`, заказ переходит в статус `SHIPPED`.

## `products` （таблица экземпляров товара）

| Имя поля           | Тип                            | Описание       |
| ------------- | ----------------------------- | -------- |
| product_id    | uuid (PK)                     | Уникальный идентификатор единицы товара |
| sku_id        | uuid (FK - skus)              | SKU      |
| shipment_id   | uuid (FK - inbound_shipments) | Исходный TTN |
| cargoplace_id | uuid (FK - cargoplaces)       | Исходная транспортная единица |
| box_id        | uuid (FK - boxes)             | Исходный короб |
| qr_code       | text UNIQUE                   | Уникальный QR |
| bin_id        | uuid (FK - bins)              | Текущая ячейка |
| order_id      | uuid                          | К какому заказу назначен |
| status        | text                          | Статус товара |
| created_at    | timestamptz                   | Время создания |
| updated_at    | timestamptz                   | Время обновления |

- product создается только при «сканировании QR»
- Каждая запись представляет одну реально существующую единицу товара
- На этапе КПП product не создается
- Сканирование штрихкода товара не создает product


**Перечень значений `status`:**
- RECEIVED（только поступил на склад, еще не размещен）
- STORED（размещен на хранение）
- ALLOCATED（назначен заказу, но еще не подобран）
- ASSEMBLED（подбор завершен）
- READY_TO_SHIP（ожидает отгрузки）
- SHIPPED（отгружен）


# **2. Четыре основные таблицы истории операций（wms_ops）**

Эти четыре таблицы являются **append-only журналами истории**; при каждой операции добавляется одна запись.

---

## `receiving_gate`

| Имя поля             | Тип             | Описание                                             |
| --------------- | -------------- | ---------------------------------------------- |
| id              | bigserial (PK) |                                                |
| ttn_code        | text           |                                                |
| cargoplace_code | text           |                                                |
| event_id        | uuid           |                                                |
| shipment_id     | uuid           |                                                |
| cargoplace_id   | uuid           |                                                |
| operator_id     | uuid           |                                                |
| action          | text           | SCAN_TTN / SCAN_CARGOPLACE / SHIPMENT_ACCEPTED |
| occurred_at     | timestamptz    |                                                |
| created_at      | timestamptz    |                                                |

- Фиксирует все операции этапа КПП
- SHIPMENT_ACCEPTED массово обновляет неотсканированные cargoplace в NOT_RECEIVED


## `receiving_table`

| Имя поля           | Тип             | Описание                                                                    |
| ------------- | -------------- | --------------------------------------------------------------------- |
| id            | bigserial (PK) | Первичный ключ |
| event_id      | uuid           | Уникальный ID события（для идемпотентности Outbox/Kafka） |
| cargoplace_id | uuid           | Транспортная единица, к которой относится текущая операция |
| box_id        | uuid           | Короб текущей операции（может быть NULL） |
| operator_id   | uuid           | Оператор |
| action        | text           | SCAN_BOX / SCAN_SKU / SCAN_QR / SCAN_BUFFER / CLOSE_BOX / CLOSE_CARGO |
| box_barcode   | text           | Отсканированный штрихкод короба（записывается при action=SCAN_BOX） |
| sku_id        | uuid           | Распознанный SKU（записывается при action=SCAN_SKU） |
| qr_code       | text           | Отсканированный уникальный QR（записывается при action=SCAN_QR） |
| product_id    | uuid           | Созданный product_id（записывается при action=SCAN_QR） |
| buffer_bin_id | uuid           | Отсканированная буферная ячейка（записывается при action=SCAN_BUFFER） |
| occurred_at   | timestamptz    | Время бизнес-события |
| created_at    | timestamptz    | Время вставки в БД |

- Логирует только операции на столе приемки
- Реальные изменения остатков происходят в таблице products


---

## `putaways`（размещение）

| Имя поля             | Тип                   | Описание                                  |
| --------------- | -------------------- | ----------------------------------- |
| id              | bigserial (PK)       |                                     |
| event_id        | uuid                 |                                     |
| product_id      | uuid (FK → products) |                                     |
| bin_id          | uuid (FK → bins)     |                                     |
| operator_id     | uuid (FK - users)    | Оператор |
| onchain_status  | text                 | PENDING_ONCHAIN / ONCHAIN_COMMITTED |
| onchain_tx_hash | text                 |                                     |
| occurred_at     | timestamptz          |                                     |
| created_at      | timestamptz          |                                     |

- До putaway product должен находиться в buffer
- После putaway поле product.bin_id обновляется на целевую ячейку

---

## `assembly_tasks` （таблица задач комплектации）

| Имя поля             | Тип                   | Описание                                       |
| --------------- | -------------------- | ---------------------------------------- |
| id              | bigserial(PK)        | Уникальный идентификатор задачи |
| event_id        | uuid                 |                                          |
| order_id        | uuid (FK - orders)   | К какому заказу относится |
| product_id      | uuid (FK - products) | Какая конкретно единица товара |
| sku_id          | uuid (FK - skus)     | sku                                      |
| from_bin_id     | uuid (FK - bins)     | Ячейка |
| section         | text                 | Берется из bins.section, используется для распределения задач по зонам |
| status          | text                 | PENDING / IN_PROGRESS / DONE / CANCELLED |
| onchain_status  | text                 |                                          |
| onchain_tx_hash | text                 |                                          |
| operator_id     | uuid (FK - users)    | Оператор |
| occurred_at     | timestamptz          | Время бизнес-события |
| created_at      | timestamptz          | Время создания |
| updated_at      | timestamptz          | Время обновления |


---

## `shippings`（отгрузка）

| Имя поля             | Тип                   | Описание     |
| --------------- | -------------------- | ------ |
| id              | bigserial (PK)       |        |
| event_id        | uuid                 |        |
| product_id      | uuid (FK - products) |        |
| operator_id     | uuid (FK - users)    | Оператор |
| vehicle_number  | text                 | Номер ТС |
| onchain_status  | text                 |        |
| onchain_tx_hash | text                 |        |
| shipped_at      | timestamptz          | Фактическое время отгрузки |
| occurred_at     | timestamptz          |        |
| created_at      | timestamptz          |        |

**Здесь рассматриваются только отгрузка заказа и запись в блокчейн, без учета доставки до клиента**

---

# **3. Таблицы событий（common schema）**

---

## `outbox_events`（PostgreSQL → Kafka）

| Имя поля            | Тип             | Описание                                           |
| -------------- | -------------- | -------------------------------------------- |
| id             | bigserial (PK) |                                              |
| event_id       | uuid           |                                              |
| aggregate_id   | uuid           | ID агрегата, ID связанной бизнес-сущности |
| aggregate_type | text           | RECEIVING_GATE / RECEIVING_TABLE / PUTAWAY и т. д. |
| event_type     | text           | Kafka-маршрутизация（wms.receiving.v1） |
| payload_hash   | text           |                                              |
| created_at     | timestamptz    |                                              |
**`aggregate_id`：ID агрегата, то есть ID связанной бизнес-сущности, нужно добавить**


---

## `onchain_events`（записи в блокчейне）

| Имя поля            | Тип             | Описание                            |
| -------------- | -------------- | ----------------------------- |
| id             | bigserial (PK) |                               |
| event_id       | uuid           |                               |
| aggregate_type | text           | Тип операции |
| tx_hash        | text           | Хэш транзакции в блокчейне |
| status         | text           | PENDING/SENT/COMMITTED/FAILED |
| error_message  | text           | Причина неудачной записи в блокчейн |
| created_at     | timestamptz    |                               |
| updated_at     | timestamptz    |                               |

**Ledger Adapter отправляет события Kafka в блокчейн**
**Значение `tx_hash` создается после отправки транзакции и записывается в БД**
**После подтверждения в блокчейне значение `status` обновляется до `COMMITTED`**


---

# `users`（таблица учетных записей пользователей）

| Имя поля           | Тип          | Описание                                |
| ------------- | ----------- | --------------------------------- |
| user_id       | uuid (PK)   | ID первичного ключа пользователя |
| username      | text        | Имя пользователя |
| password_hash | text        | Хэш пароля |
| role          | text        | Роль пользователя: ADMIN / OPERATOR / CUSTOMER и т. д. |
| is_active     | boolean     | Активен ли пользователь |
| created_at    | timestamptz | Время создания |
| updated_at    | timestamptz | Время последнего изменения |

---

# `evm_addresses`（таблица привязки пользователь ↔ адрес EVM в сети）

| Имя поля          | Тип                | Описание        |
| ------------ | ----------------- | --------- |
| id           | bigserial (PK)    | ID первичного ключа |
| user_id      | uuid (FK → users） | Принадлежит пользователю |
| evm_address  | text              | Адрес кошелька пользователя в сети |
| onchain_role | text              | Роль этого адреса в блокчейне |
| created_at   | timestamptz       | Время создания |
| updated_at   | timestamptz       | Время последнего изменения |

1. **После создания пользователя для него генерируется `evm_address`, который заполняется в этой таблице**
2. **`onchain_role` используется для управления правами записи в блокчейне; эта роль определяется на цепочке, здесь может использоваться как кэш/отображение**



---
