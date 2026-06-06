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
--     allocate выбирает товары глобально по SKU (oldest-first). На общем
--     'E2E Seed Outbound SKU' заказы 07 разбирали pre-STORED пул теста 06
--     (STRESS-PROD-QR), который НЕ проходил receiving/putaway on-chain →
--     picking/shipping на контракте уходили в soft-skip (no-op). Отдельный SKU
--     изолирует 07: allocate матчит ТОЛЬКО собственные товары потока, и
--     picking/shipping становятся реальными FSM-переходами.
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
-- 3. Pre-STORED товары для теста 06-assembly.js
--    2000 продуктов в статусе STORED, sku = E2E Seed Outbound SKU, bin = A-01-01
--    (4× по сравнению с прежними 500 — покрывает 1000 заказов SHOP-7 с запасом)
-- ────────────────────────────────────────────────────────────────────────────
-- Вспомогательная поставка для assembly-продуктов
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-ASSEMBLY-TTN-001',
  'GATE_CLOSED',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  'STRESS-ASSEMBLY-CP-001',
  'TABLE_CLOSED',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
WHERE sh.ttn_code = 'STRESS-ASSEMBLY-TTN-001'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.products
  (product_id, sku_id, shipment_id, cargoplace_id, qr_code, bin_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  sk.sku_id,
  sh.shipment_id,
  c.cargoplace_id,
  'STRESS-PROD-QR-' || LPAD(gs.i::text, 6, '0'),
  b.bin_id,
  'STORED',
  now(),
  now()
FROM generate_series(1, 2000) AS gs(i)
CROSS JOIN (
  SELECT sk.sku_id FROM wms_inventory.skus sk WHERE sk.name = 'E2E Seed Outbound SKU'
) sk
CROSS JOIN (
  SELECT sh.shipment_id FROM wms_inventory.inbound_shipments sh
  WHERE sh.ttn_code = 'STRESS-ASSEMBLY-TTN-001'
) sh
CROSS JOIN (
  SELECT c.cargoplace_id FROM wms_inventory.cargoplaces c
  WHERE c.cargoplace_code = 'STRESS-ASSEMBLY-CP-001'
) c
CROSS JOIN (
  SELECT b.bin_id
  FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'A-01-01' AND w.name = 'Склад Москва-Север'
) b
ON CONFLICT (qr_code) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 4. NEW заказы и SCHEDULED рейсы для теста 06-assembly (SHOP-7)
--    1000 заказов (↑ от 300) — тест 06 работает до исчерпания стока.
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
  AND d.code = 'SHOP-7'
ON CONFLICT (external_order_no) DO NOTHING;

-- Строки заказов: 1 единица E2E Seed Outbound SKU на заказ
INSERT INTO wms_inventory.order_lines (order_id, sku_id, qty)
SELECT
  o.order_id,
  s.sku_id,
  1
FROM wms_inventory.orders o
JOIN wms_inventory.skus s ON s.name = 'E2E Seed Outbound SKU'
WHERE o.external_order_no LIKE 'STRESS-ORD-%'
ON CONFLICT DO NOTHING;

-- 10 рейсов для SHOP-7
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
  AND d.code = 'SHOP-7'
ON CONFLICT (dispatch_code) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 5. NEW заказы и SCHEDULED рейсы для теста 07-full-flow (SHOP-5)
--    2000 заказов — 1:1 с товарами (1 на итерацию), чтобы КАЖДЫЙ товар мог быть
--    аллоцирован и пройти все 4 стадии (сбалансированный 4-стадийный backlog).
--    Пул изолирован от теста 06 (SHOP-7).
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
  AND d.code = 'SHOP-5'
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

-- 10 рейсов для SHOP-5
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
  AND d.code = 'SHOP-5'
ON CONFLICT (dispatch_code) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 6. Pre-RECEIVED товары для теста 08-blockchain-tps.js
--    5000 продуктов в статусе RECEIVED, bin_id = BUFFER-01.
--    Каждый putaway (scan-product + scan-storage-bin) создаёт 1 outbox_event,
--    который Debezium → Kafka → ledger-adapter доставляет в блокчейн.
--    Тест 08: shared-iterations 5000, измеряет WMS event-generation TPS
--    и (через run-all.sh мониторинг) blockchain commit rate.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'STRESS-TPS-TTN-001',
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  'STRESS-TPS-CP-001',
  'TABLE_CLOSED',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
WHERE sh.ttn_code = 'STRESS-TPS-TTN-001'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.products
  (product_id, sku_id, shipment_id, cargoplace_id, qr_code, bin_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  sk.sku_id,
  sh.shipment_id,
  c.cargoplace_id,
  'STRESS-TPS-QR-' || LPAD(gs.i::text, 6, '0'),
  b.bin_id,
  'RECEIVED',
  now(),
  now()
FROM generate_series(1, 5000) AS gs(i)
CROSS JOIN (
  SELECT sk.sku_id FROM wms_inventory.skus sk WHERE sk.name = 'E2E Seed Outbound SKU'
) sk
CROSS JOIN (
  SELECT sh.shipment_id FROM wms_inventory.inbound_shipments sh
  WHERE sh.ttn_code = 'STRESS-TPS-TTN-001'
) sh
CROSS JOIN (
  SELECT c.cargoplace_id FROM wms_inventory.cargoplaces c
  WHERE c.cargoplace_code = 'STRESS-TPS-CP-001'
) c
CROSS JOIN (
  SELECT b.bin_id
  FROM wms_inventory.bins b
  JOIN wms_inventory.warehouses w ON w.warehouse_id = b.warehouse_id
  WHERE b.code = 'BUFFER-01' AND w.name = 'Склад Москва-Север'
) b
ON CONFLICT (qr_code) DO NOTHING;

COMMIT;
