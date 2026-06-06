-- stress-throughput-seed.sql
-- Сид для сквозного throughput-бенча всего проекта (09-throughput.js).
--
-- Цель — измерить ЧЕСТНУЮ пропускную способность всей системы
-- (receiving → putaway → assembly → shipping → on-chain), сняв искусственные
-- тормоза существующего 07:
--   1) РАЗНОС ПО МАГАЗИНАМ — 70 магазинов SHOP-T01..T70 (\set N_DEST), заказы размазаны по ним,
--      чтобы FOR UPDATE-лок allocate (per-destination) не становился глобальным.
--   2) ПЛАНИРОВЩИК-КАДЕНС — allocate вынесен из горячего пути операторов: его
--      периодически зовёт отдельный planner-сценарий, операторы лишь берут
--      готовые задачи. См. 09-throughput.js.
--
-- Изоляция: всё с префиксом STRESS-TPUT-* / SHOP-T*. Транзакционные данные
-- (cargoplaces, orders, products, dispatches, …) подпадают под LIKE 'STRESS-%'
-- и чистятся существующим stress-cleanup.sql. Справочники (destinations, bins,
-- sku, операторы) создаются идемпотентно и живут между прогонами.
--
-- Выполнение:
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-throughput-seed.sql
--
-- Очистка перед повторным прогоном:
--   docker exec -i postgres_db psql -U root -d wms_blockchain_db \
--     < tests/stress/setup/stress-cleanup.sql

\set N_ITEMS 14000
\set N_DEST 70
-- Много рейсов на магазин: непрерывная отгрузка каденсом берёт по одному СВЕЖЕМУ рейсу
-- на тик (рейс уезжает по опустошении буфера, переиспользовать нельзя) → нужен пул на
-- ~N_CP/SHIP_EVERY/N_DEST тиков + хвост teardown. 50 с запасом.
\set N_DISP_PER_DEST 50
\set N_SKU 1
-- Товаров на грузоместо (реалистичная «паллета»: одно грузоместо = много коробок/товаров,
-- а не 1 товар/паллета). Амортизирует open/close грузоместа (scan-cargoplace, scan-buffer,
-- close-cargoplace) по N_PER_CP товарам → во столько же раз меньше тяжёлых receiving-транзакций.
-- N_CP = N_ITEMS / N_PER_CP грузомест; orders/products остаются N_ITEMS (баланс supply==demand).
-- N_PER_CP=1 + N_CP=N_ITEMS => прежнее «1 товар/паллета» поведение.
\set N_PER_CP 1
\set N_CP 14000

BEGIN;

-- ────────────────────────────────────────────────────────────────────────────
-- 0. Операторы stress-op-01 … stress-op-130 (своя «корзина сборки» у каждого VU).
--    Каждому VU нужен УНИКАЛЬНЫЙ оператор: иначе два VU делят корзину и
--    scan-shipping-buffer (WHERE operator_id=$1) заберёт чужой товар. 130 = запас
--    под VU-свип до ~128. Идемпотентно. Паддинг ДОЛЖЕН совпадать с k6 pad(i,2)
--    (JS padStart: min-width 2, БЕЗ обрезки → 1→"01", 100→"100", 130→"130").
--    ВНИМАНИЕ: LPAD(x,2,'0') в Postgres ОБРЕЗАЕт длинные строки (100→"10"!), из-за
--    чего op-100..130 коллапсировали в op-10..13 и создавалось лишь 99 операторов,
--    а k6 с VUS>99 падал на логине stress-op-100. CASE ниже эквивалентен padStart(2).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'stress-op-' || CASE WHEN gs.i < 10 THEN '0' || gs.i::text ELSE gs.i::text END,
  crypt('stressop', gen_salt('bf')),
  'OPERATOR',
  true, now(), now()
FROM generate_series(1, 130) AS gs(i)
ON CONFLICT (username) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 1. Магазины SHOP-T01 … SHOP-T10 + их буферы отгрузки BIN-SHIP-T01 … T10.
--    Справочные данные — создаются один раз, переживают cleanup.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.destinations (destination_id, code, name, address, warehouse_id, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'SHOP-T' || LPAD(gs.i::text, 2, '0'),
  'Throughput-магазин №' || gs.i,
  'г. Москва, Нагрузочная ул., ' || gs.i,
  w.warehouse_id, now(), now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, :N_DEST) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (code) DO NOTHING;

-- Буфер отгрузки на каждый магазин (section=SHIPPING_BUFFER, привязан к destination).
INSERT INTO wms_inventory.bins (bin_id, warehouse_id, code, section, destination_id, volume, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'BIN-SHIP-T' || LPAD(gs.i::text, 2, '0'),
  'SHIPPING_BUFFER',
  d.destination_id,
  9999.0::numeric,  -- большой объём: fill-level не enforced (TODO #50), буфер не лимитирует бенч
  now(), now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, :N_DEST) AS gs(i)
JOIN wms_inventory.destinations d
  ON d.code = 'SHOP-T' || LPAD(gs.i::text, 2, '0')
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 2. N_SKU выделенных SKU + штрихкодов: STRESS-TPUT-SKU-01..NN / STRESS-TPUT-BC-01..NN.
--    Товары и заказы размазаны по ним (§3, §4), чтобы allocate/pick не дрались за
--    ОДИН products-пул — измеренный фронт-затык был lock-контеншном на products при
--    одном SKU. N_SKU=1 => прежнее одно-SKU поведение (BC-01). Изолирует от 06/07.
--    LPAD min-width 2 совпадает с k6 pad(i,2) при сборке штрихкода.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.skus (sku_id, name, description, volume, created_at, updated_at)
SELECT gen_random_uuid(), 'STRESS-TPUT-SKU-' || LPAD(gs.i::text, 2, '0'),
       'Throughput SKU №' || gs.i || ' для бенча 09', 1.0::numeric, now(), now()
FROM generate_series(1, :N_SKU) AS gs(i)
WHERE NOT EXISTS (SELECT 1 FROM wms_inventory.skus s2 WHERE s2.name = 'STRESS-TPUT-SKU-' || LPAD(gs.i::text, 2, '0'));

INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, 'STRESS-TPUT-BC-' || LPAD(gs.i::text, 2, '0')
FROM generate_series(1, :N_SKU) AS gs(i)
JOIN wms_inventory.skus s ON s.name = 'STRESS-TPUT-SKU-' || LPAD(gs.i::text, 2, '0')
ON CONFLICT (barcode) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 3. Грузоместа потока: N_CP штук в RECEIVED_AT_GATE (= N_ITEMS / N_PER_CP).
--    Одно грузоместо = одна итерация = N_PER_CP продуктов (через N_PER_CP scan-qr в одной коробке).
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.inbound_shipments
  (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(), w.warehouse_id,
  'STRESS-TPUT-TTN-' || LPAD(gs.i::text, 5, '0'),
  'GATE_IN_PROGRESS', now(), now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, :N_CP) AS gs(i)
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces
  (cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at)
SELECT
  gen_random_uuid(), s.shipment_id,
  'STRESS-TPUT-CP-' || LPAD(idx.i::text, 5, '0'),
  'RECEIVED_AT_GATE', now(), now(), now()
FROM wms_inventory.inbound_shipments s
JOIN (SELECT i FROM generate_series(1, :N_CP) AS gs(i)) AS idx
  ON s.ttn_code = 'STRESS-TPUT-TTN-' || LPAD(idx.i::text, 5, '0')
ON CONFLICT DO NOTHING;

-- Ожидаемый SKU для каждого грузоместа (нужно для scan-sku). Грузоместо c → SKU
-- ((c-1)%N_SKU)+1, expected_qty=N_PER_CP (все товары паллеты — один SKU). Баланс
-- supply==demand по каждому SKU держится: товаров SKU = (N_CP/N_SKU)*N_PER_CP = N_ITEMS/N_SKU.
INSERT INTO wms_inventory.expected_cargoplace_skus
  (cargoplace_id, sku_id, expected_qty)
SELECT c.cargoplace_id, s.sku_id, :N_PER_CP
FROM wms_inventory.cargoplaces c
JOIN wms_inventory.skus s
  ON s.name = 'STRESS-TPUT-SKU-' || LPAD(((((right(c.cargoplace_code, 5))::int - 1) % :N_SKU) + 1)::text, 2, '0')
WHERE c.cargoplace_code LIKE 'STRESS-TPUT-CP-%'
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 4. Заказы потока: N_ITEMS штук NEW, РАЗМАЗАНЫ по N_DEST магазинам (round-robin).
--    1 заказ ↔ 1 товар (qty=1 STRESS-TPUT-SKU). Магазин = SHOP-T<((i-1)%N_DEST)+1>.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.orders
  (order_id, external_order_no, customer_id, warehouse_id, destination_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  -- LPAD width 6: LPAD УСЕКАЕТ строки длиннее width (LPAD('100000',5)='10000')! При
  -- N_ITEMS>99999 5-значные коды коллапсируют → ON CONFLICT роняет дубли (было 99999 из 300000).
  -- 6 цифр держит до 999999 заказов. right(...,6) ниже синхронен.
  'STRESS-TPUT-ORD-' || LPAD(gs.i::text, 6, '0'),
  u.user_id, w.warehouse_id, d.destination_id,
  'NEW', now(), now()
FROM generate_series(1, :N_ITEMS) AS gs(i)
CROSS JOIN public.users u
CROSS JOIN wms_inventory.warehouses w
JOIN wms_inventory.destinations d
  ON d.code = 'SHOP-T' || LPAD((((gs.i - 1) % :N_DEST) + 1)::text, 2, '0')
WHERE u.username = 'customer'
  AND w.name = 'Склад Москва-Север'
ON CONFLICT (external_order_no) DO NOTHING;

-- Заказ i → SKU ((i-1)%N_SKU)+1 — та же формула, что у грузоместа i (§3),
-- поэтому по каждому SKU кол-во товаров == кол-ву заказов (allocate всегда находит сток).
INSERT INTO wms_inventory.order_lines (order_id, sku_id, qty)
SELECT o.order_id, s.sku_id, 1
FROM wms_inventory.orders o
JOIN wms_inventory.skus s
  ON s.name = 'STRESS-TPUT-SKU-' || LPAD(((((right(o.external_order_no, 6))::int - 1) % :N_SKU) + 1)::text, 2, '0')
WHERE o.external_order_no LIKE 'STRESS-TPUT-ORD-%'
ON CONFLICT DO NOTHING;

-- ────────────────────────────────────────────────────────────────────────────
-- 5. Рейсы: N_DISP_PER_DEST на каждый магазин (teardown-отгрузка пакетами).
--    Код: STRESS-TPUT-DSP-T<dd>-<n>.
-- ────────────────────────────────────────────────────────────────────────────
INSERT INTO wms_inventory.outbound_dispatches
  (dispatch_id, dispatch_code, warehouse_id, destination_id,
   vehicle_number, driver_name, driver_phone,
   status, scheduled_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'STRESS-TPUT-DSP-T' || LPAD(gd.d::text, 2, '0') || '-' || gn.n,
  w.warehouse_id, dst.destination_id,
  'STRESS-TPUT-VH-' || LPAD(gd.d::text, 2, '0') || '-' || gn.n,
  'TPUT Driver ' || gd.d || '-' || gn.n,
  '+7200' || LPAD(gd.d::text, 4, '0') || LPAD(gn.n::text, 2, '0'),
  'SCHEDULED', now() + interval '1 day', now(), now()
FROM wms_inventory.warehouses w
CROSS JOIN generate_series(1, :N_DEST) AS gd(d)
CROSS JOIN generate_series(1, :N_DISP_PER_DEST) AS gn(n)
JOIN wms_inventory.destinations dst
  ON dst.code = 'SHOP-T' || LPAD(gd.d::text, 2, '0')
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (dispatch_code) DO NOTHING;

COMMIT;
