# Диаграммы потоков данных (DFD)

**Версия:** 1.0
**Дата:** 2026-03-31

---

## Level 0 — Контекстная диаграмма

Вся система как один процесс. Показывает внешние сущности и потоки данных между ними и WMS.

```mermaid
flowchart TB
    ERP(["ERP-система\n(внешняя)"])
    OP_GATE(["Оператор КПП"])
    OP_TABLE(["Оператор стола"])
    OP_PUT(["Оператор раскладки"])
    OP_ASM(["Оператор сборки"])
    OP_SHIP(["Оператор отгрузки"])
    MGR(["Менеджер / Система"])
    BC(["Avalanche Subnet-EVM\n(блокчейн)"])

    WMS[["WMS\n(складская система)"]]

    ERP -->|"TTN, грузоместа,\nожидаемые SKU,\nзаказы"| WMS
    OP_GATE -->|"код TTN,\nкоды грузомест"| WMS
    WMS -->|"статус приёмки,\nпрогресс"| OP_GATE
    OP_TABLE -->|"код грузоместа,\nШК коробки, ШК товара,\nQR-код, код буфера"| WMS
    WMS -->|"SKU, статус product,\nпрогресс, недостача"| OP_TABLE
    OP_PUT -->|"код буфера,\nQR товаров,\nкод ячейки хранения"| WMS
    WMS -->|"список товаров\nв буфере, подтверждение"| OP_PUT
    MGR -->|"order_id\n(запрос аллокации)"| WMS
    OP_ASM -->|"QR товара\n(подбор)"| WMS
    WMS -->|"задачи сборки,\nпрогресс"| OP_ASM
    OP_SHIP -->|"QR товара,\nномер ТС"| WMS
    WMS -->|"статус верификации,\nподтверждение отгрузки"| OP_SHIP
    WMS -->|"outbox events →\nDebezium → Kafka →\nLedger Adapter →\nbatch TX"| BC
    BC -->|"TX receipt\n(COMMITTED / FAILED)"| WMS
```

---

## Level 1 — Декомпозиция WMS на процессы

```mermaid
flowchart TB
    ERP(["ERP"])
    OP_G(["Оператор КПП"])
    OP_T(["Оператор стола"])
    OP_P(["Оператор раскладки"])
    OP_A(["Оператор сборки"])
    MGR(["Менеджер"])
    OP_S(["Оператор отгрузки"])
    BC(["Блокчейн"])

    P0["0. Загрузка\nданных поставки"]
    P1["1. Приёмка\nна КПП"]
    P2["2. Приёмка\nна столе"]
    P3["3. Раскладка"]
    P4["4. Сборка"]
    P5["5. Отгрузка"]

    DS_SHIP[("inbound_shipments\ncargoplaces\nexpected_cargoplace_skus")]
    DS_INV[("products\nboxes\nbins\nskus\nsku_barcodes")]
    DS_OPS[("receiving_gate\nreceiving_table\nputaways\nassembly_tasks\nshippings")]
    DS_ORD[("orders")]
    DS_EVT[("outbox_events\nonchain_events")]

    ERP -->|"TTN, грузоместа,\nожидаемые SKU"| P0
    P0 -->|"INSERT shipment,\ncargoplaces,\nexpected_skus"| DS_SHIP

    OP_G -->|"код TTN"| P1
    OP_G -->|"коды грузомест"| P1
    DS_SHIP -->|"shipment +\nсписок cargoplaces"| P1
    P1 -->|"UPDATE статусы\nshipment, cargoplaces"| DS_SHIP
    P1 -->|"INSERT лог действий"| DS_OPS
    P1 -->|"статус, прогресс"| OP_G

    OP_T -->|"код грузоместа,\nШК коробки, ШК товара,\nQR-код, код буфера"| P2
    DS_SHIP -->|"cargoplace,\nexpected_skus"| P2
    DS_INV -->|"sku по barcode"| P2
    P2 -->|"INSERT products\n(status=RECEIVED)"| DS_INV
    P2 -->|"INSERT/UPDATE boxes"| DS_INV
    P2 -->|"UPDATE cargoplace\nстатус"| DS_SHIP
    P2 -->|"INSERT лог действий"| DS_OPS
    P2 -->|"INSERT outbox_events\n(receiving)"| DS_EVT
    P2 -->|"SKU, product,\nпрогресс, недостача"| OP_T

    OP_P -->|"код буфера,\nQR товаров,\nкод ячейки"| P3
    DS_INV -->|"products\nв буфере"| P3
    P3 -->|"UPDATE products\n(bin_id, status=STORED)"| DS_INV
    P3 -->|"INSERT putaways"| DS_OPS
    P3 -->|"INSERT outbox_events\n(putaway)"| DS_EVT
    P3 -->|"подтверждение\nразмещения"| OP_P

    MGR -->|"order_id"| P4
    OP_A -->|"QR товара"| P4
    DS_INV -->|"products\n(STORED)"| P4
    DS_ORD -->|"заказ +\nтребуемые SKU"| P4
    P4 -->|"UPDATE products\n(ALLOCATED → ASSEMBLED)"| DS_INV
    P4 -->|"INSERT/UPDATE\nassembly_tasks"| DS_OPS
    P4 -->|"UPDATE order\nстатус"| DS_ORD
    P4 -->|"INSERT outbox_events\n(picking)"| DS_EVT
    P4 -->|"задачи, прогресс"| OP_A

    OP_S -->|"QR товара,\nномер ТС"| P5
    DS_INV -->|"products\n(ASSEMBLED)"| P5
    DS_ORD -->|"заказ"| P5
    P5 -->|"UPDATE products\n(SHIPPED)"| DS_INV
    P5 -->|"INSERT shippings"| DS_OPS
    P5 -->|"UPDATE order\n(SHIPPED)"| DS_ORD
    P5 -->|"INSERT outbox_events\n(shipping)"| DS_EVT
    P5 -->|"подтверждение\nотгрузки"| OP_S

    DS_EVT -->|"events через\nDebezium → Kafka →\nLedger Adapter"| BC
    BC -->|"TX receipt"| DS_EVT
```

---

## Level 2 — Приёмка на КПП

```mermaid
flowchart TB
    OP(["Оператор КПП"])

    P1_1["1.1 Сканирование\nТТН"]
    P1_2["1.2 Сканирование\nгрузоместа"]
    P1_3["1.3 Завершение\nпоставки"]

    DS_SHIP[("inbound_shipments")]
    DS_CARGO[("cargoplaces")]
    DS_LOG[("receiving_gate")]

    OP -->|"код TTN"| P1_1
    DS_SHIP -->|"shipment\n(CREATED)"| P1_1
    DS_CARGO -->|"список cargoplaces\n(EXPECTED)"| P1_1
    P1_1 -->|"UPDATE status =\nGATE_IN_PROGRESS"| DS_SHIP
    P1_1 -->|"INSERT\naction=SCAN_TTN"| DS_LOG
    P1_1 -->|"список ожидаемых\nгрузомест"| OP

    OP -->|"код грузоместа"| P1_2
    DS_CARGO -->|"cargoplace\n(EXPECTED)"| P1_2
    P1_2 -->|"UPDATE status =\nRECEIVED_AT_GATE,\nreceived_at_gate_at = now()"| DS_CARGO
    P1_2 -->|"INSERT\naction=SCAN_CARGOPLACE"| DS_LOG
    P1_2 -->|"прогресс (3/10)"| OP

    OP -->|"'Поставка принята'\nили все отсканированы"| P1_3
    DS_CARGO -->|"неотсканированные\ncargoplaces"| P1_3
    P1_3 -->|"UPDATE неотсканированные\nstatus = NOT_RECEIVED"| DS_CARGO
    P1_3 -->|"UPDATE status =\nGATE_CLOSED"| DS_SHIP
    P1_3 -->|"INSERT\naction=SHIPMENT_ACCEPTED"| DS_LOG
    P1_3 -->|"итог: принято 7,\nне принято 3"| OP
```

**Выходные данные:** grузоместа в статусе RECEIVED_AT_GATE, готовые для приёмки на столе.
**Outbox events:** нет. Блокчейн не задействован.

---

## Level 2 — Приёмка на столе

```mermaid
flowchart TB
    OP(["Оператор стола"])

    P2_1["2.1 Открытие\nгрузоместа"]
    P2_2["2.2 Сканирование\nкоробки"]
    P2_3["2.3 Сканирование\nШК товара"]
    P2_4["2.4 Сканирование QR\n(создание product)"]
    P2_5["2.5 Завершение\nкоробки"]
    P2_6["2.6 Сканирование\nбуфера"]
    P2_7["2.7 Закрытие\nгрузоместа"]

    DS_CARGO[("cargoplaces")]
    DS_EXP[("expected_cargoplace_skus")]
    DS_BOX[("boxes")]
    DS_SKU[("skus\nsku_barcodes")]
    DS_PROD[("products")]
    DS_BIN[("bins")]
    DS_LOG[("receiving_table")]
    DS_EVT[("outbox_events")]

    OP -->|"код грузоместа"| P2_1
    DS_CARGO -->|"cargoplace\n(RECEIVED_AT_GATE)"| P2_1
    DS_EXP -->|"ожидаемые\nSKU × qty"| P2_1
    P2_1 -->|"UPDATE status =\nTABLE_IN_PROGRESS"| DS_CARGO
    P2_1 -->|"ожидаемое кол-во"| OP

    OP -->|"ШК коробки"| P2_2
    P2_2 -->|"INSERT box\n(status=OPEN)"| DS_BOX
    P2_2 -->|"INSERT\naction=SCAN_BOX"| DS_LOG
    P2_2 -->|"коробка открыта"| OP

    OP -->|"ШК товара\n(barcode)"| P2_3
    DS_SKU -->|"sku_id\nпо barcode"| P2_3
    P2_3 -->|"INSERT\naction=SCAN_SKU"| DS_LOG
    P2_3 -->|"найден SKU:\nназвание, описание"| OP

    OP -->|"QR-код\n(уникальный)"| P2_4
    P2_4 -->|"INSERT product\n(sku_id, shipment_id,\ncargoplace_id, box_id,\nqr_code, status=RECEIVED)"| DS_PROD
    P2_4 -->|"INSERT\naction=SCAN_QR,\nproduct_id"| DS_LOG
    P2_4 -->|"товар создан,\nпрогресс (5/12)"| OP

    OP -->|"'Завершить коробку'"| P2_5
    P2_5 -->|"UPDATE status =\nCLOSED"| DS_BOX
    P2_5 -->|"INSERT\naction=CLOSE_BOX"| DS_LOG
    P2_5 -->|"коробка закрыта"| OP

    OP -->|"код буфера"| P2_6
    DS_BIN -->|"bin\n(section=BUFFER)"| P2_6
    P2_6 -->|"UPDATE products\nbin_id = buffer_bin_id"| DS_PROD
    P2_6 -->|"INSERT\naction=SCAN_BUFFER"| DS_LOG
    P2_6 -->|"N товаров\nв буфере"| OP

    OP -->|"'Завершить грузоместо'"| P2_7
    DS_PROD -->|"все products\nгрузоместа"| P2_7
    DS_EXP -->|"ожидаемые\nSKU × qty"| P2_7
    P2_7 -->|"UPDATE status =\nTABLE_CLOSED"| DS_CARGO
    P2_7 -->|"INSERT\naction=CLOSE_CARGO"| DS_LOG
    P2_7 -->|"INSERT N events\n(aggregate_type=receiving,\naggregate_id=product_id)"| DS_EVT
    P2_7 -->|"итог: принято 10,\nожидалось 12,\nнедостача 2"| OP
```

**Ключевой момент:** product создаётся в шаге 2.4 (SCAN_QR), но outbox events создаются в шаге 2.7 (CLOSE_CARGO) — батчем, по 1 на каждый product.

---

## Level 2 — Раскладка

```mermaid
flowchart TB
    OP(["Оператор раскладки"])

    P3_1["3.1 Сканирование\nбуфера"]
    P3_2["3.2 Сканирование\nтоваров (набор)"]
    P3_3["3.3 Сканирование\nячейки хранения"]

    DS_PROD[("products")]
    DS_BIN[("bins")]
    DS_PUT[("putaways")]
    DS_EVT[("outbox_events")]

    OP -->|"код буфера"| P3_1
    DS_PROD -->|"products\nWHERE bin_id = buffer\nAND status = RECEIVED"| P3_1
    P3_1 -->|"список товаров\nв буфере (15 шт.)"| OP

    OP -->|"QR товара"| P3_2
    DS_PROD -->|"product\n(проверка: bin_id\n== buffer, status\n== RECEIVED)"| P3_2
    P3_2 -->|"товар в корзине\n(взято: 3)"| OP

    OP -->|"код ячейки\nхранения"| P3_3
    DS_BIN -->|"bin\n(валидация)"| P3_3
    P3_3 -->|"UPDATE products\nbin_id = storage_bin,\nstatus = STORED"| DS_PROD
    P3_3 -->|"INSERT putaway\n(product_id, from_bin_id,\nbin_id, operator_id)"| DS_PUT
    P3_3 -->|"INSERT N events\n(aggregate_type=putaway,\naggregate_id=product_id)"| DS_EVT
    P3_3 -->|"размещено N товаров\nв ячейку M2-A-03"| OP
```

**Входные данные:** products в буфере (status=RECEIVED).
**Выходные данные:** products в ячейках хранения (status=STORED) + outbox events для блокчейна.

---

## Level 2 — Сборка

```mermaid
flowchart TB
    MGR(["Менеджер / Система"])
    OP(["Оператор сборки"])

    P4_1["4.1 Аллокация\n(системная)"]
    P4_2["4.2 Получение\nзадач"]
    P4_3["4.3 Подбор\nтовара"]

    DS_ORD[("orders")]
    DS_PROD[("products")]
    DS_TASK[("assembly_tasks")]
    DS_EVT[("outbox_events")]

    MGR -->|"order_id"| P4_1
    DS_ORD -->|"заказ\n(требуемые SKU × qty)"| P4_1
    DS_PROD -->|"products\nWHERE status = STORED\nAND sku_id IN (...)"| P4_1
    P4_1 -->|"UPDATE products\nstatus = ALLOCATED,\norder_id = order_id"| DS_PROD
    P4_1 -->|"INSERT assembly_tasks\n(order_id, product_id,\nsku_id, from_bin_id,\nstatus=PENDING)"| DS_TASK
    P4_1 -->|"UPDATE status =\nALLOCATED"| DS_ORD
    P4_1 -->|"аллокация завершена:\nN товаров назначено"| MGR

    OP -->|"запрос задач\nпо order_id"| P4_2
    DS_TASK -->|"tasks\n(PENDING)"| P4_2
    P4_2 -->|"список: product,\nSKU, ячейка, секция"| OP

    OP -->|"QR товара\n(скан с полки)"| P4_3
    DS_TASK -->|"task\n(PENDING)"| P4_3
    P4_3 -->|"UPDATE task\nstatus = DONE,\nonchain_status =\nPENDING_ONCHAIN"| DS_TASK
    P4_3 -->|"UPDATE product\nstatus = ASSEMBLED"| DS_PROD
    P4_3 -->|"INSERT event\n(aggregate_type=picking,\naggregate_id=product_id)"| DS_EVT
    P4_3 -->|"UPDATE order status\n(ASSEMBLY_IN_PROGRESS\nили ASSEMBLED)"| DS_ORD
    P4_3 -->|"товар подобран,\nпрогресс (3/5)"| OP
```

**Особенность:** аллокация (4.1) **не создаёт** outbox events. Только подбор (4.3) пишет в блокчейн.

---

## Level 2 — Отгрузка

```mermaid
flowchart TB
    OP(["Оператор отгрузки"])

    P5_1["5.1 Выбор\nзаказа"]
    P5_2["5.2 Верификация\nтовара"]
    P5_3["5.3 Отгрузка"]

    DS_ORD[("orders")]
    DS_PROD[("products")]
    DS_SHIP[("shippings")]
    DS_EVT[("outbox_events")]

    OP -->|"запрос готовых\nзаказов"| P5_1
    DS_ORD -->|"orders WHERE status\nIN (ASSEMBLED,\nREADY_TO_SHIP)"| P5_1
    P5_1 -->|"список заказов"| OP

    OP -->|"QR товара"| P5_2
    DS_PROD -->|"product\n(проверка: order_id,\nstatus = ASSEMBLED)"| P5_2
    P5_2 -->|"товар подтверждён\n(3/5)"| OP

    OP -->|"номер ТС,\n'Отгрузить'"| P5_3
    DS_PROD -->|"все products\nзаказа"| P5_3
    P5_3 -->|"UPDATE products\nstatus = SHIPPED"| DS_PROD
    P5_3 -->|"INSERT shippings\n(product_id, vehicle_number,\noperator_id)"| DS_SHIP
    P5_3 -->|"UPDATE order\nstatus = SHIPPED"| DS_ORD
    P5_3 -->|"INSERT N events\n(aggregate_type=shipping,\naggregate_id=product_id)"| DS_EVT
    P5_3 -->|"заказ отгружен:\nN товаров"| OP
```

**Финальная точка.** После отгрузки on-chain статус = Shipped. Данные товара больше не меняются.

---

## Level 2 — Интеграция (Outbox → Blockchain)

```mermaid
flowchart TB
    DS_EVT[("outbox_events")]
    DS_ONCHAIN[("onchain_events")]

    P6_1["6.1 CDC\n(Debezium)"]
    P6_2["6.2 Буферизация\n(Kafka)"]
    P6_3["6.3 Batch + TX\n(Ledger Adapter)"]

    BC(["BatchMappingWMS\n(блокчейн)"])

    DS_EVT -->|"WAL:\nINSERT event\n(event_id, aggregate_id,\naggregate_type)"| P6_1
    P6_1 -->|"topic: wms.{type}.v1\nkey: product_id (UUID)\nheader.id: event_id"| P6_2
    P6_2 -->|"batch: 10 msgs\nили timeout 100ms"| P6_3
    DS_ONCHAIN -->|"проверка\nidempotency:\nevent_id уже есть?"| P6_3
    P6_3 -->|"INSERT\nstatus=PENDING"| DS_ONCHAIN
    P6_3 -->|"UUID→uint256:\neventId = keccak256(event_id)\nitemId = keccak256(product_id)"| P6_3
    P6_3 -->|"batchAccept / batchPutAway /\nbatchPick / batchShip\n(eventIds[], itemIds[])"| BC
    BC -->|"TX receipt\n(success / revert)"| P6_3
    P6_3 -->|"UPDATE status =\nCOMMITTED,\ntx_hash = 0x..."| DS_ONCHAIN
```

---

## Сводка: какие данные создаёт каждый процесс

| Процесс | Входные данные (от оператора) | Читает из хранилища | Пишет в хранилище | Outbox |
|---------|------------------------------|-------------------|-------------------|--------|
| 1. КПП | код TTN, коды грузомест | shipments, cargoplaces | shipments, cargoplaces, receiving_gate | — |
| 2. Стол | код грузоместа, ШК коробки, ШК товара, QR, код буфера | cargoplaces, expected_skus, sku_barcodes, bins | cargoplaces, boxes, **products**, receiving_table, **outbox_events** | receiving |
| 3. Раскладка | код буфера, QR товаров, код ячейки | products, bins | **products**, putaways, **outbox_events** | putaway |
| 4. Сборка | order_id (менеджер), QR товара (оператор) | orders, products | **products**, assembly_tasks, orders, **outbox_events** | picking |
| 5. Отгрузка | QR товара, номер ТС | products, orders | **products**, shippings, orders, **outbox_events** | shipping |
| 6. Интеграция | outbox_events (CDC) | onchain_events (idempotency) | onchain_events | → blockchain |
