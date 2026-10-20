-- stress-seed.sql
-- Засев данных для нагрузочных тестов (04-08).
--
-- Создаёт изолированный набор данных с префиксом STRESS-*,
-- не пересекающийся с dev-seed и e2e-фикстурами.
-- Все INSERT идемпотентны (ON CONFLICT DO NOTHING).
--
-- Выполнение (контейнер postgres_db должен быть запущен):
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-seed.sql
--
-- Очистка данных перед повторным прогоном тестов:
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-cleanup.sql

BEGIN;

-- ────────────────────────────────────────────────────────────────────────────
-- 0. Пользователь admin (нужен для 01-smoke.js и 03-auth.js)
--    В deploy/seed.sql он отсутствует; создаём здесь идемпотентно.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'admin',
  crypt('admin', gen_salt('bf')),
  'ADMIN',
  true,
  now(),
  now()
ON CONFLICT (username) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 0.1 Пул операторов stress-op-01 … stress-op-30 для теста 07-full-flow.js.
--     Каждый k6-VU логинится СВОИМ оператором: иначе «корзина сборки»
--     (assembly_tasks.operator_id; scan-shipping-buffer WHERE operator_id=$1)
--     делится между конкурентными VU и первый scan-shipping-buffer забирает
--     чужие товары — товары застревают в ASSEMBLED, отгрузки не происходит.
--     Пароль у всех — 'stressop'.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'stress-op-' || LPAD(gs.i::text, 2, '0'),
  crypt('stressop', gen_salt('bf')),
  'OPERATOR',
  true,
  now(),
  now()
FROM generate_series(1, 30) AS gs(i)
ON CONFLICT (username) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 0.2 Выделенный SKU + штрихкод для теста 07-full-flow (STANDALONE).
--     allocate выбирает товары глобально по SKU (oldest-first) и не смотрит на
--     цепочку. На общем 'E2E Seed Outbound SKU' заказы 07 разбирали pre-STORED
--     товары других фикстур без цепочечной истории. Отдельный SKU изолирует 07:
--     allocate матчит ТОЛЬКО собственные товары потока, и picking/shipping
--     становятся реальными FSM-переходами (None→Accepted→PutAway→Picked→Shipped).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.skus (sku_id, name, description, volume, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-FLOW-SKU', 'Выделенный SKU для нагрузочного теста 07-full-flow', 1.0::numeric, now(), now()
WHERE NOT EXISTS (SELECT 1 FROM wms_inventory.skus WHERE name = 'STRESS-FLOW-SKU');

INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, 'STRESS-FLOW-BC-01'
FROM wms_inventory.skus s
WHERE s.name = 'STRESS-FLOW-SKU'
ON CONFLICT (barcode) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 0.3 Выделенные SKU + штрихкоды для тестов 06 и 08.
--     Товары этих тестов создаются в setup() через подписанный receiving
--     (lib/bootstrap.js), поэтому allocate матчит только собственный пул теста
--     и не задевает dev-сид (QR-2026-*, ORD-2026-0501) и другие пулы.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.skus (sku_id, name, description, volume, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-ASM-SKU', 'Выделенный SKU для нагрузочного теста 06-assembly', 1.0::numeric, now(), now()
WHERE NOT EXISTS (SELECT 1 FROM wms_inventory.skus WHERE name = 'STRESS-ASM-SKU');

INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, 'STRESS-ASM-BC-01'
FROM wms_inventory.skus s
WHERE s.name = 'STRESS-ASM-SKU'
ON CONFLICT (barcode) DO NOTHING;

INSERT INTO wms_inventory.skus (sku_id, name, description, volume, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-TPS-SKU', 'Выделенный SKU для нагрузочного теста 08-blockchain-tps', 1.0::numeric, now(), now()
WHERE NOT EXISTS (SELECT 1 FROM wms_inventory.skus WHERE name = 'STRESS-TPS-SKU');

INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, 'STRESS-TPS-BC-01'
FROM wms_inventory.skus s
WHERE s.name = 'STRESS-TPS-SKU'
ON CONFLICT (barcode) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 0.4 Выделенные магазины и их буферы отгрузки для тестов 06 и 07.
--     Свои destination на каждый тест ⇒ в пуле NEW-заказов магазина нет чужих
--     заказов (dev-сид ORD-2026-0501 → SHOP-5 раньше уводил allocate 07 на
--     товары QR-2026-* без цепочечной истории). Буфер отгрузки обязан быть
--     привязан к магазину (bins_shipping_buffer_requires_destination).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.destinations (destination_id, code, name, address, warehouse_id, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-SHOP-06', 'Stress-магазин сборки (06)',
       'г. Москва, Нагрузочная ул., 6', w.warehouse_id, now(), now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (code) DO NOTHING;

INSERT INTO wms_inventory.destinations (destination_id, code, name, address, warehouse_id, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-SHOP-07', 'Stress-магазин полного потока (07)',
       'г. Москва, Нагрузочная ул., 7', w.warehouse_id, now(), now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (code) DO NOTHING;

INSERT INTO wms_inventory.bins (bin_id, warehouse_id, code, section, destination_id, volume, created_at, updated_at)
SELECT gen_random_uuid(), w.warehouse_id, 'BIN-SHIP-S06', 'SHIPPING_BUFFER', d.destination_id,
       9999.0::numeric, now(), now()
FROM wms_inventory.warehouses w
JOIN wms_inventory.destinations d ON d.code = 'STRESS-SHOP-06'
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.bins (bin_id, warehouse_id, code, section, destination_id, volume, created_at, updated_at)
SELECT gen_random_uuid(), w.warehouse_id, 'BIN-SHIP-S07', 'SHIPPING_BUFFER', d.destination_id,
       9999.0::numeric, now(), now()
FROM wms_inventory.warehouses w
JOIN wms_inventory.destinations d ON d.code = 'STRESS-SHOP-07'
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 1. Входящие поставки для теста 04-receiving-gate.js
--    STRESS-TTN-0001 … STRESS-TTN-2000 в статусе CREATED
--    (сервис знает только CREATED / GATE_IN_PROGRESS / GATE_CLOSED;
--     scan-ttn переводит CREATED → GATE_IN_PROGRESS, что нужно для scan-cargoplace)
--    2000 поставок × 3 грузоместа = 6000 операций КПП-приёмки.
--    Тест 04 использует shared-iterations (ровно 2000 итераций, каждая уникальна).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-TTN-' || LPAD(gs.i::text, 4, '0'),
  'CREATED',
  now(),
  now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, 2000) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

-- 3 грузоместа на каждую поставку КПП
INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  s.shipment_id,
  'STRESS-CP-' || LPAD(idx.i::text, 4, '0') || '-' || cp.j,
  'EXPECTED',
  now(),
  now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, 2000) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-TTN-' || LPAD(idx.i::text, 4, '0')
CROSS JOIN (SELECT j FROM generate_series(1, 3) AS gs(j)) AS cp
ON CONFLICT DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 2. Грузоместа для теста 05-receiving-table.js
--    STRESS-TABLE-CP-0001 … STRESS-TABLE-CP-2000 в статусе RECEIVED_AT_GATE
--    Тест 05 использует shared-iterations (ровно 2000 итераций).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-TABLE-TTN-' || LPAD(gs.i::text, 4, '0'),
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, 2000) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  s.shipment_id,
  'STRESS-TABLE-CP-' || LPAD(idx.i::text, 4, '0'),
  'RECEIVED_AT_GATE',
  now(),
  now(),
  now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, 2000) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-TABLE-TTN-' || LPAD(idx.i::text, 4, '0')
ON CONFLICT DO NOTHING;

-- Ожидаемые SKU для каждого грузоместа стола (E2E Seed Outbound SKU, barcode 4600000099999)
INSERT INTO wms_inventory.expected_cargoplace_skus
  (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  10
FROM wms_inventory.cargoplaces c
JOIN wms_inventory.inbound_shipments sh ON sh.shipment_id = c.shipment_id
JOIN wms_inventory.skus s ON s.name = 'E2E Seed Outbound SKU'
WHERE sh.ttn_code LIKE 'STRESS-TABLE-TTN-%'
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 2b. Грузоместа для теста 07-full-flow.js (отдельный пул, не пересекается с 05)
--     STRESS-FLOW-CP-0001 … STRESS-FLOW-CP-2000 в статусе RECEIVED_AT_GATE
--     Тест 07 использует shared-iterations (ровно 2000 итераций).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-FLOW-TTN-' || LPAD(gs.i::text, 4, '0'),
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, 2000) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  s.shipment_id,
  'STRESS-FLOW-CP-' || LPAD(idx.i::text, 4, '0'),
  'RECEIVED_AT_GATE',
  now(),
  now(),
  now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, 2000) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-FLOW-TTN-' || LPAD(idx.i::text, 4, '0')
ON CONFLICT DO NOTHING;

-- Ожидаемые SKU для flow-грузомест
INSERT INTO wms_inventory.expected_cargoplace_skus
  (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  10
FROM wms_inventory.cargoplaces c
JOIN wms_inventory.inbound_shipments sh ON sh.shipment_id = c.shipment_id
JOIN wms_inventory.skus s ON s.name = 'STRESS-FLOW-SKU'
WHERE sh.ttn_code LIKE 'STRESS-FLOW-TTN-%'
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 3. Пул грузомест для теста 06-assembly.js
--    100 грузомест STRESS-ASM-CP-001..100 в RECEIVED_AT_GATE по 10 товаров SKU
--    STRESS-ASM-SKU (= 1000 товаров). Сами товары создаёт setup() теста через
--    подписанный receiving (lib/bootstrap.js): прямой INSERT в products оставлял
--    их на контракте в Status.None, и PutAway/Pick отклонялись (WrongItemStatus).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-ASM-TTN-' || LPAD(gs.i::text, 4, '0'),
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, 100) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  s.shipment_id,
  'STRESS-ASM-CP-' || LPAD(idx.i::text, 3, '0'),
  'RECEIVED_AT_GATE',
  now(),
  now(),
  now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, 100) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-ASM-TTN-' || LPAD(idx.i::text, 4, '0')
ON CONFLICT DO NOTHING;

-- Ожидаемые SKU для assembly-грузомест (10 товаров на грузоместо)
INSERT INTO wms_inventory.expected_cargoplace_skus
  (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  10
FROM wms_inventory.cargoplaces c
JOIN wms_inventory.inbound_shipments sh ON sh.shipment_id = c.shipment_id
JOIN wms_inventory.skus s ON s.name = 'STRESS-ASM-SKU'
WHERE sh.ttn_code LIKE 'STRESS-ASM-TTN-%'
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 4. NEW заказы и SCHEDULED рейсы для теста 06-assembly (STRESS-SHOP-06)
--    1000 заказов — 1:1 с товарами пула (1 заказ = 1 товар STRESS-ASM-SKU),
--    свой destination ⇒ в пуле нет чужих NEW-заказов.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.orders
  (order_id, external_order_no, customer_id, warehouse_id, destination_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'STRESS-ORD-' || LPAD(gs.i::text, 4, '0'),
  u.user_id,
  w.warehouse_id,
  d.destination_id,
  'NEW',
  now(),
  now()
FROM generate_series(1, 1000) AS gs(i)
CROSS JOIN public.users u
CROSS JOIN wms_inventory.warehouses w
CROSS JOIN wms_inventory.destinations d
WHERE u.username = 'customer'
  AND w.name = 'Склад Москва-Север'
  AND d.code = 'STRESS-SHOP-06'
ON CONFLICT (external_order_no) DO NOTHING;

-- Строки заказов: 1 единица STRESS-ASM-SKU на заказ
INSERT INTO wms_inventory.order_lines (order_id, sku_id, qty)
SELECT
  o.order_id,
  s.sku_id,
  1
FROM wms_inventory.orders o
JOIN wms_inventory.skus s ON s.name = 'STRESS-ASM-SKU'
WHERE o.external_order_no LIKE 'STRESS-ORD-%'
ON CONFLICT DO NOTHING;

-- 10 рейсов для STRESS-SHOP-06
INSERT INTO wms_inventory.outbound_dispatches
  (dispatch_id, dispatch_code, warehouse_id, destination_id,
   vehicle_number, driver_name, driver_phone,
   status, scheduled_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'STRESS-DSP-' || LPAD(gs.i::text, 4, '0'),
  w.warehouse_id,
  d.destination_id,
  'STRESS-VH-' || LPAD(gs.i::text, 4, '0'),
  'Stress Driver ' || gs.i,
  '+70000' || LPAD(gs.i::text, 6, '0'),
  'SCHEDULED',
  now() + interval '1 day',
  now(),
  now()
FROM generate_series(1, 10) AS gs(i)
CROSS JOIN wms_inventory.warehouses w
CROSS JOIN wms_inventory.destinations d
WHERE w.name = 'Склад Москва-Север'
  AND d.code = 'STRESS-SHOP-06'
ON CONFLICT (dispatch_code) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 5. NEW заказы и SCHEDULED рейсы для теста 07-full-flow (STRESS-SHOP-07)
--    2000 заказов — 1:1 с товарами (1 на итерацию), чтобы КАЖДЫЙ товар мог быть
--    аллоцирован и пройти все 4 стадии (сбалансированный 4-стадийный backlog).
--    Пул изолирован от тестов 06/08 и от dev-сида (ORD-2026-0501 → SHOP-5).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.orders
  (order_id, external_order_no, customer_id, warehouse_id, destination_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'STRESS-FLOW-ORD-' || LPAD(gs.i::text, 4, '0'),
  u.user_id,
  w.warehouse_id,
  d.destination_id,
  'NEW',
  now(),
  now()
FROM generate_series(1, 2000) AS gs(i)
CROSS JOIN public.users u
CROSS JOIN wms_inventory.warehouses w
CROSS JOIN wms_inventory.destinations d
WHERE u.username = 'customer'
  AND w.name = 'Склад Москва-Север'
  AND d.code = 'STRESS-SHOP-07'
ON CONFLICT (external_order_no) DO NOTHING;

INSERT INTO wms_inventory.order_lines (order_id, sku_id, qty)
SELECT
  o.order_id,
  s.sku_id,
  1
FROM wms_inventory.orders o
JOIN wms_inventory.skus s ON s.name = 'STRESS-FLOW-SKU'
WHERE o.external_order_no LIKE 'STRESS-FLOW-ORD-%'
ON CONFLICT DO NOTHING;

-- 10 рейсов для STRESS-SHOP-07
INSERT INTO wms_inventory.outbound_dispatches
  (dispatch_id, dispatch_code, warehouse_id, destination_id,
   vehicle_number, driver_name, driver_phone,
   status, scheduled_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'STRESS-FLOW-DSP-' || LPAD(gs.i::text, 4, '0'),
  w.warehouse_id,
  d.destination_id,
  'STRESS-FLOW-VH-' || LPAD(gs.i::text, 4, '0'),
  'Stress Flow Driver ' || gs.i,
  '+71000' || LPAD(gs.i::text, 6, '0'),
  'SCHEDULED',
  now() + interval '1 day',
  now(),
  now()
FROM generate_series(1, 10) AS gs(i)
CROSS JOIN wms_inventory.warehouses w
CROSS JOIN wms_inventory.destinations d
WHERE w.name = 'Склад Москва-Север'
  AND d.code = 'STRESS-SHOP-07'
ON CONFLICT (dispatch_code) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 6. Пул грузомест для теста 08-blockchain-tps.js
--    500 грузомест STRESS-TPS-CP-001..500 в RECEIVED_AT_GATE по 10 товаров SKU
--    STRESS-TPS-SKU (= 5000 товаров). Товары создаёт setup() теста подписанным
--    receiving (lib/bootstrap.js): они приходят в BUFFER-01 в статусе RECEIVED
--    и с on-chain Receive (Accepted), поэтому каждый putaway
--    (scan-product + scan-storage-bin) — настоящий переход Accepted→PutAway
--    и даёт outbox_event для Debezium → Kafka → ledger-adapter → контракт.
--    Тест 08: shared-iterations 5000, измеряет WMS event-generation TPS
--    и (через run-all.sh мониторинг) blockchain commit rate.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-TPS-TTN-' || LPAD(gs.i::text, 4, '0'),
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, 500) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  s.shipment_id,
  'STRESS-TPS-CP-' || LPAD(idx.i::text, 3, '0'),
  'RECEIVED_AT_GATE',
  now(),
  now(),
  now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, 500) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-TPS-TTN-' || LPAD(idx.i::text, 4, '0')
ON CONFLICT DO NOTHING;

-- Ожидаемые SKU для TPS-грузомест (10 товаров на грузоместо)
INSERT INTO wms_inventory.expected_cargoplace_skus
  (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  10
FROM wms_inventory.cargoplaces c
JOIN wms_inventory.inbound_shipments sh ON sh.shipment_id = c.shipment_id
JOIN wms_inventory.skus s ON s.name = 'STRESS-TPS-SKU'
WHERE sh.ttn_code LIKE 'STRESS-TPS-TTN-%'
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

COMMIT;
