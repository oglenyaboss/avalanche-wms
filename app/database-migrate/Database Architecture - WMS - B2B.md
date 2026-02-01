
# Модель данных системы управления складом (WMS)

---

# **1. Основные данные / Текущее состояние (wms_inventory)**

---

## `skus` (Основные данные товаров)

| Имя поля       | Тип           | Описание          |
| -------------- | ------------- | ----------------- |
| sku_id         | uuid (PK)     | Первичный ключ SKU |
| name           | text          | Название товара   |
| description    | text          | Описание товара   |
| volume         | numeric       | Объем (литры)     |
| created_at     | timestamptz   | Время создания    |
| updated_at     | timestamptz   | Время обновления  |

---

## <a id="warehouses"></a>`warehouses` (Таблица складов)

| Имя поля       | Тип           | Описание          |
| -------------- | ------------- | ----------------- |
| warehouse_id   | bigserial(PK) | ID склада         |
| name           | text          | Название склада   |
| address        | text          | Адрес склада      |
| contact        | text          | Контактная информация |
| created_at     | timestamptz   | Время создания    |
| updated_at     | timestamptz   | Время обновления  |

---

## <a id="bins"></a>`bins` (Складские ячейки)

| Имя поля       | Тип                       | Описание          |
| -------------- | ------------------------- | ----------------- |
| bin_id         | uuid (PK)                 | ID ячейки         |
| warehouse_id   | bigint (FK - warehouses)  | ID склада         |
| code           | text                      | Код ячейки        |
| section        | text                      | Зона              |
| volume         | numeric                   | Объем             |
| created_at     | timestamptz               | Время создания    |
| updated_at     | timestamptz               | Время обновления  |

---

## <a id="orders"></a>`orders` (Заказы)

| Имя поля          | Тип                       | Описание              |
| ----------------- | ------------------------- | --------------------- |
| order_id          | uuid (PK)                 | ID заказа             |
| external_order_no | text                      | Внешний номер заказа  |
| customer_id       | uuid (FK - users)         | ID клиента            |
| warehouse_id      | bigint (FK - warehouses)  | ID склада             |
| status            | text                      | Статус заказа         |
| created_at        | timestamptz               | Время создания        |
| updated_at        | timestamptz               | Время обновления      |

**Статусы заказа:**
- NEW (Новый заказ)
- ALLOCATED (Продукты распределены по заказу)
- ASSEMBLY_IN_PROGRESS (Комплектация)
- ASSEMBLED (Укомплектован)
- READY_TO_SHIP (Готов к отгрузке)
- SHIPPED (Отгружен)

**Логика статусов:**
- Заказ переходит в статус `ALLOCATED`, когда для любого продукта установлен статус `ALLOCATED` и существуют несгенерированные задачи комплектации;
- Заказ переходит в статус `ASSEMBLY_IN_PROGRESS`, когда существуют продукты в процессе комплектации;
- Заказ переходит в статус `READY_TO_SHIP`, когда все продукты имеют статус `ASSEMBLED` / `READY_TO_SHIP` и еще не отгружены;
- Заказ переходит в статус `SHIPPED`, когда все продукты достигли статуса `SHIPPED`.

---

## <a id="products"></a>`products` (Таблица товаров)

| Имя поля     | Тип          | Описание              |
| ------------ | ------------ | --------------------- |
| product_id   | uuid (PK)    | Уникальный ID продукта |
| sku_id       | uuid         | ID SKU                |
| bin_id       | uuid         | Текущая ячейка        |
| order_id     | uuid         | Распределен по заказу |
| status       | text         | Статус продукта       |
| created_at   | timestamptz  | Время создания        |
| updated_at   | timestamptz  | Время обновления      |

**Статусы продукта:**
- RECEIVED (Поступил на склад, не размещен)
- STORED (Размещен)
- ALLOCATED (Распределен по заказу, не собран)
- ASSEMBLED (Собран)
- READY_TO_SHIP (Готов к отгрузке)
- SHIPPED (Отгружен)

---

# **2. Таблицы истории операций (wms_ops)**

Эти четыре таблицы являются **только для добавления записей**, каждая операция записывает новую строку.

---

## <a id="receivings"></a>`receivings` (Приемка)

| Имя поля         | Тип                   | Описание                              |
| ---------------- | --------------------- | ------------------------------------- |
| id               | bigserial (PK)        | Первичный ключ                        |
| event_id         | uuid                  | Соответствует outbox / onchain        |
| product_id       | uuid (FK → products)  | Какой продукт принят                  |
| operator_id      | uuid (FK - users)     | Оператор                              |
| onchain_status   | text                  | PENDING_ONCHAIN / ONCHAIN_COMMITTED   |
| onchain_tx_hash  | text                  | Хэш транзакции в блокчейне            |
| occurred_at      | timestamptz           | Время операции (от фронтенда)         |
| created_at       | timestamptz           | Время записи в БД                     |

`event_id` гарантирует Exactly-Once доставку через Outbox + Debezium + Kafka

---

## <a id="putaways"></a>`putaways` (Размещение)

| Имя поля         | Тип                   | Описание                              |
| ---------------- | --------------------- | ------------------------------------- |
| id               | bigserial (PK)        |                                       |
| event_id         | uuid                  |                                       |
| product_id       | uuid (FK → products)  |                                       |
| bin_id           | uuid (FK → bins)      |                                       |
| operator_id      | uuid (FK - users)     | Оператор                              |
| onchain_status   | text                  | PENDING_ONCHAIN / ONCHAIN_COMMITTED   |
| onchain_tx_hash  | text                  |                                       |
| occurred_at      | timestamptz           |                                       |
| created_at       | timestamptz           |                                       |

---

## <a id="assembly_tasks"></a>`assembly_tasks` (Задачи комплектации)

| Имя поля        | Тип                  | Описание                           |
| --------------- | -------------------- | ---------------------------------- |
| id              | bigserial(PK)        | Уникальный ID задачи               |
| event_id        | uuid                 |                                    |
| order_id        | uuid (FK - orders)   | Соответствующий заказ              |
| product_id      | uuid (FK - products) | Конкретный товар                   |
| sku_id          | uuid (FK - skus)     | SKU                                |
| from_bin_id     | uuid (FK - bins)     | Ячейка                             |
| section         | text                 | Зона из bins.section               |
| status          | text                 | PENDING/IN_PROGRESS/DONE/CANCELLED |
| operator_id     | uuid (FK - users)    | Оператор                           |
| onchain_status  | text                 |                                    |
| onchain_tx_hash | text                 |                                    |
| occurred_at     | timestamptz          | Время операции                     |
| created_at      | timestamptz          | Время создания                     |
| updated_at      | timestamptz          | Время обновления                   |

---

## <a id="shippings"></a>`shippings` (Отгрузка)

| Имя поля        | Тип                  | Описание                   |
| --------------- | -------------------- | -------------------------- |
| id              | bigserial (PK)       |                            |
| event_id        | uuid                 |                            |
| product_id      | uuid (FK - products) |                            |
| operator_id     | uuid (FK - users)    | Оператор                   |
| vehicle_number  | text                 | Номер транспорта           |
| onchain_status  | text                 |                            |
| onchain_tx_hash | text                 |                            |
| shipped_at      | timestamptz          | Фактическое время отгрузки |
| occurred_at     | timestamptz          |                            |
| created_at      | timestamptz          |                            |

**Отслеживается только отгрузка со склада и запись в блокчейн, доставка до клиента не учитывается**

---

# **3. Таблицы событий (общая схема)**

---

## <a id="outbox_events"></a>`outbox_events` (PostgreSQL → Kafka)

| Имя поля       | Тип            | Описание                         |
| -------------- | -------------- | -------------------------------- |
| id             | bigserial (PK) |                                  |
| event_id       | uuid           |                                  |
| aggregate_id   | uuid           | ID связанной бизнес-сущности     |
| aggregate_type | text           | RECEIVING / PUTAWAY и т.д.       |
| event_type     | text           | Маршрут Kafka (wms.receiving.v1) |
| payload_hash   | text           |                                  |
| created_at     | timestamptz    |                                  |

**`aggregate_id`**: ID агрегата, ID связанной бизнес-сущности, нужно добавить  


---

## <a id="onchain_events"></a>`onchain_events` (Записи в блокчейне)

| Имя поля        | Тип             | Описание                            |
| --------------- | --------------- | ----------------------------------- |
| id              | bigserial (PK)  |                                     |
| event_id        | uuid            |                                     |
| aggregate_type  | text            | Тип операции                        |
| tx_hash         | text            | Хэш транзакции в блокчейне          |
| status          | text            | PENDING/SENT/COMMITTED/FAILED       |
| error_message   | text            | Причина ошибки                      |
| created_at      | timestamptz     |                                     |
| updated_at      | timestamptz     |                                     |

**Ledger Adapter отправляет события Kafka в блокчейн**  
**Значение `tx_hash` генерируется и записывается в БД при отправке транзакции**  
**При подтверждении в блокчейне значение `status` обновляется на `COMMITTED`**

---

## <a id="users"></a>`users` (Таблица пользователей)

| Имя поля       | Тип          | Описание                      |
| -------------- | ------------ | ----------------------------- |
| user_id        | uuid (PK)    | Уникальный ID пользователя    |
| username       | text         | Имя пользователя              |
| password_hash  | text         | Хэш пароля                    |
| role           | text         | Роль: ADMIN/OPERATOR/CUSTOMER |
| is_active      | boolean      | Активен ли пользователь       |
| created_at     | timestamptz  | Время создания                |
| updated_at     | timestamptz  | Время изменения               |

---

## <a id="evm_addresses"></a>`evm_addresses` (Привязка пользователей к адресам в блокчейне)

| Имя поля       | Тип                | Описание                |
| -------------- | ------------------ | ----------------------- |
| id             | bigserial (PK)     | Первичный ключ          |
| user_id        | uuid (FK → users)  | ID пользователя         |
| evm_address    | text               | Адрес кошелька в блокчейне |
| onchain_role   | text               | Роль в блокчейне        |
| created_at     | timestamptz        | Время создания          |
| updated_at     | timestamptz        | Время изменения         |

1. **После создания пользователя генерируется `evm_address` и записывается в эту таблицу**
2. **`onchain_role` управляет правами записи в блокчейне, эта роль определяется в блокчейне, здесь используется для кэширования/отображения**