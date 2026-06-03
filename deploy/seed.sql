BEGIN;

-- Dev seed dataset for WMS flows.
-- Idempotency strategy:
-- ON CONFLICT DO NOTHING where UNIQUE/PK exists.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 1) Users (operator)
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (
  gen_random_uuid(),
  'operator',
  crypt('operator', gen_salt('bf')),
  'OPERATOR',
  true,
  now(),
  now()
)
ON CONFLICT (username) DO NOTHING;

-- 1.1) Demo customer for outbound orders
INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (
  gen_random_uuid(),
  'customer',
  crypt('customer', gen_salt('bf')),
  'CUSTOMER',
  true,
  now(),
  now()
)
ON CONFLICT (username) DO NOTHING;

INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (
  gen_random_uuid(),
  'admin',
  crypt('admin', gen_salt('bf')),
  'ADMIN',
  true,
  now(),
  now()
)
ON CONFLICT (username) DO NOTHING;

-- 2) Warehouse
INSERT INTO wms_inventory.warehouses (name, address, contact, created_at, updated_at)
VALUES (
  'Склад Москва-Север',
  'г. Москва, ул. Складская, 15',
  '+7 (495) 100-10-10',
  now(),
  now()
)
ON CONFLICT (name) DO NOTHING;

-- 3) Destinations for outbound flow
INSERT INTO wms_inventory.destinations (destination_id, code, name, address, warehouse_id, created_at, updated_at)
SELECT
  gen_random_uuid(),
  v.code,
  v.name,
  v.address,
  w.warehouse_id,
  now(),
  now()
FROM wms_inventory.warehouses w
JOIN (
  VALUES
    ('SHOP-5', 'Магазин №5 Маросейка', 'г. Москва, ул. Маросейка, 5'),
    ('SHOP-7', 'Магазин №7 Петровка', 'г. Москва, ул. Петровка, 7')
) AS v(code, name, address) ON true
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (code) DO NOTHING;

-- 4) Bins (A/B + receiving buffer + shipping buffers)
INSERT INTO wms_inventory.bins (bin_id, warehouse_id, code, section, destination_id, volume, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  v.code,
  v.section,
  d.destination_id,
  v.volume,
  now(),
  now()
FROM wms_inventory.warehouses w
JOIN (
  VALUES
    ('A-01-01', 'A', NULL,     120.0::numeric),
    ('A-01-02', 'A', NULL,     120.0::numeric),
    ('A-02-01', 'A', NULL,     110.0::numeric),
    ('B-01-01', 'B', NULL,     130.0::numeric),
    ('B-01-02', 'B', NULL,     130.0::numeric),
    ('BUFFER-01', 'BUFFER', NULL, 200.0::numeric),
    ('BIN-SHIP-5', 'SHIPPING_BUFFER', 'SHOP-5', 180.0::numeric),
    ('BIN-SHIP-7', 'SHIPPING_BUFFER', 'SHOP-7', 180.0::numeric)
) AS v(code, section, destination_code, volume) ON true
LEFT JOIN wms_inventory.destinations d
  ON d.code = v.destination_code
 AND d.warehouse_id = w.warehouse_id
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT DO NOTHING;

-- 5) SKUs
INSERT INTO wms_inventory.skus (sku_id, name, description, volume, created_at, updated_at)
SELECT
  gen_random_uuid(),
  v.name,
  v.description,
  v.volume,
  now(),
  now()
FROM (
  VALUES
    ('Кроссовки Nike Air Max 90', 'Повседневные кроссовки, мужская линейка', 9.5::numeric),
    ('Футболка Adidas Originals', 'Базовая хлопковая футболка', 1.2::numeric),
    ('Рюкзак Puma Core', 'Городской рюкзак 22 литра', 7.0::numeric),
    ('Куртка The North Face', 'Демисезонная куртка', 12.0::numeric),
    ('E2E Seed Outbound SKU', 'Dedicated SKU for seeded outbound e2e flow', 1.0::numeric)
) AS v(name, description, volume)
ON CONFLICT DO NOTHING;

-- 5.5) Barcode -> SKU mapping (many barcodes for one SKU)
INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, v.barcode
FROM (
  VALUES
    ('Кроссовки Nike Air Max 90', '4600000000011'),
    ('Кроссовки Nike Air Max 90', '4600000000028'),
    ('Футболка Adidas Originals', '4600000000035'),
    ('Рюкзак Puma Core', '4600000000042'),
    ('Куртка The North Face', '4600000000059'),
    ('E2E Seed Outbound SKU', '4600000099999')
) AS v(sku_name, barcode)
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (barcode) DO NOTHING;

-- 6) Inbound shipment
INSERT INTO wms_inventory.inbound_shipments (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'ТТН-2026-00142',
  'RECEIVED',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

-- 7) Cargoplaces
INSERT INTO wms_inventory.cargoplaces (
  cargoplace_id,
  shipment_id,
  cargoplace_code,
  status,
  received_at_gate_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  v.cargoplace_code,
  v.status,
  now() - interval '2 hours',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
JOIN (
  VALUES
    ('CP-001', 'RECEIVED'),
    ('CP-002', 'RECEIVED')
) AS v(cargoplace_code, status) ON true
WHERE sh.ttn_code = 'ТТН-2026-00142'
ON CONFLICT DO NOTHING;

-- 8) Boxes
INSERT INTO wms_inventory.boxes (box_id, cargoplace_id, box_barcode, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  c.cargoplace_id,
  v.box_barcode,
  'OPEN',
  now(),
  now()
FROM (
  VALUES
    ('CP-001', 'BOX-001'),
    ('CP-001', 'BOX-002'),
    ('CP-002', 'BOX-003')
) AS v(cargoplace_code, box_barcode)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-00142'
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = v.cargoplace_code
ON CONFLICT DO NOTHING;

-- 9) Expected SKUs in cargoplaces
INSERT INTO wms_inventory.expected_cargoplace_skus (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  v.expected_qty
FROM (
  VALUES
    ('CP-001', 'Кроссовки Nike Air Max 90', 4),
    ('CP-001', 'Футболка Adidas Originals', 3),
    ('CP-002', 'Рюкзак Puma Core', 2)
) AS v(cargoplace_code, sku_name, expected_qty)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-00142'
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = v.cargoplace_code
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- 9.5) Dedicated receiving-table happy-path dataset
-- This cargoplace starts at RECEIVED_AT_GATE and has no boxes/products yet,
-- so the full table flow can be tested from scan-cargoplace to close-box.
INSERT INTO wms_inventory.inbound_shipments (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'ТТН-2026-TABLE-001',
  'GATE_IN_PROGRESS',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces (
  cargoplace_id,
  shipment_id,
  cargoplace_code,
  status,
  received_at_gate_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  'CP-TABLE-001',
  'RECEIVED_AT_GATE',
  now() - interval '15 minutes',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
WHERE sh.ttn_code = 'ТТН-2026-TABLE-001'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.expected_cargoplace_skus (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  v.expected_qty
FROM (
  VALUES
    ('CP-TABLE-001', 'Кроссовки Nike Air Max 90', 2),
    ('CP-TABLE-001', 'Футболка Adidas Originals', 1),
    ('CP-TABLE-001', 'E2E Seed Outbound SKU', 1)
) AS v(cargoplace_code, sku_name, expected_qty)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-TABLE-001'
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = v.cargoplace_code
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

-- 9.6) Dedicated dataset for scan-buffer and close-cargoplace
-- This cargoplace is already TABLE_IN_PROGRESS and has RECEIVED products without bin_id,
-- so buffer placement and outbox creation can be tested immediately.
INSERT INTO wms_inventory.inbound_shipments (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'ТТН-2026-TABLE-002',
  'GATE_CLOSED',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces (
  cargoplace_id,
  shipment_id,
  cargoplace_code,
  status,
  received_at_gate_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  'CP-TABLE-002',
  'TABLE_IN_PROGRESS',
  now() - interval '45 minutes',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
WHERE sh.ttn_code = 'ТТН-2026-TABLE-002'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.boxes (box_id, cargoplace_id, box_barcode, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  c.cargoplace_id,
  'BOX-TABLE-201',
  'CLOSED',
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
WHERE sh.ttn_code = 'ТТН-2026-TABLE-002'
  AND c.cargoplace_code = 'CP-TABLE-002'
ON CONFLICT DO NOTHING;

INSERT INTO wms_inventory.expected_cargoplace_skus (cargoplace_id, sku_id, expected_qty)
SELECT
  c.cargoplace_id,
  s.sku_id,
  v.expected_qty
FROM (
  VALUES
    ('CP-TABLE-002', 'Кроссовки Nike Air Max 90', 2),
    ('CP-TABLE-002', 'Футболка Adidas Originals', 2)
) AS v(cargoplace_code, sku_name, expected_qty)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-TABLE-002'
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = v.cargoplace_code
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (cargoplace_id, sku_id) DO NOTHING;

INSERT INTO wms_inventory.products (
  product_id,
  sku_id,
  shipment_id,
  cargoplace_id,
  box_id,
  qr_code,
  bin_id,
  order_id,
  status,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  s.sku_id,
  sh.shipment_id,
  c.cargoplace_id,
  b.box_id,
  v.qr_code,
  NULL,
  NULL,
  'RECEIVED'::wms_inventory.product_status,
  now(),
  now()
FROM (
  VALUES
    ('QR-TABLE-2001', 'Кроссовки Nike Air Max 90'),
    ('QR-TABLE-2002', 'Кроссовки Nike Air Max 90'),
    ('QR-TABLE-2003', 'Футболка Adidas Originals')
) AS v(qr_code, sku_name)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-TABLE-002'
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = 'CP-TABLE-002'
JOIN wms_inventory.skus s ON s.name = v.sku_name
LEFT JOIN wms_inventory.boxes b
  ON b.cargoplace_id = c.cargoplace_id
 AND b.box_barcode = 'BOX-TABLE-201'
ON CONFLICT (qr_code) DO NOTHING;

-- 9.7) Dedicated receiving-gate happy-path dataset
-- A CREATED shipment with EXPECTED cargoplaces, so the full gate flow can be
-- tested end to end: scan-ttn -> scan-cargoplace (x2) -> auto-close, or
-- scan-ttn -> accept-shipment. Other seeded shipments are already past the gate
-- stage (RECEIVED / GATE_IN_PROGRESS / GATE_CLOSED), so they cannot exercise it.
INSERT INTO wms_inventory.inbound_shipments (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'ТТН-2026-GATE-001',
  'CREATED',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces (
  cargoplace_id,
  shipment_id,
  cargoplace_code,
  status,
  received_at_gate_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  v.cargoplace_code,
  'EXPECTED',
  NULL,
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
JOIN (
  VALUES
    ('CP-GATE-001'),
    ('CP-GATE-002')
) AS v(cargoplace_code) ON true
WHERE sh.ttn_code = 'ТТН-2026-GATE-001'
ON CONFLICT DO NOTHING;

-- 9.8) Second gate shipment, so the partial-accept path (accept-shipment with
-- remaining EXPECTED places -> NOT_RECEIVED) can be tested independently of the
-- auto-close happy path on ТТН-2026-GATE-001.
INSERT INTO wms_inventory.inbound_shipments (shipment_id, warehouse_id, ttn_code, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  'ТТН-2026-GATE-002',
  'CREATED',
  now(),
  now()
FROM wms_inventory.warehouses w
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT (ttn_code) DO NOTHING;

INSERT INTO wms_inventory.cargoplaces (
  cargoplace_id,
  shipment_id,
  cargoplace_code,
  status,
  received_at_gate_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  sh.shipment_id,
  v.cargoplace_code,
  'EXPECTED',
  NULL,
  now(),
  now()
FROM wms_inventory.inbound_shipments sh
JOIN (
  VALUES
    ('CP-GATE-003'),
    ('CP-GATE-004')
) AS v(cargoplace_code) ON true
WHERE sh.ttn_code = 'ТТН-2026-GATE-002'
ON CONFLICT DO NOTHING;

-- 10) Orders (customer = seeded demo customer)
-- Test outbound flow order for e2e/manual run: ORD-2026-0501 -> SHOP-5.
INSERT INTO wms_inventory.orders (
  order_id,
  external_order_no,
  customer_id,
  warehouse_id,
  destination_id,
  status,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  v.external_order_no,
  u.user_id,
  w.warehouse_id,
  d.destination_id,
  v.status::wms_inventory.order_status,
  now(),
  now()
FROM (
  VALUES
    ('ORD-2026-0042', 'SHOP-5', 'ALLOCATED'),
    ('ORD-2026-0043', 'SHOP-7', 'SHIPPED'),
    ('ORD-2026-0501', 'SHOP-5', 'NEW')
) AS v(external_order_no, destination_code, status)
JOIN public.users u ON u.username = 'customer'
JOIN wms_inventory.warehouses w ON w.name = 'Склад Москва-Север'
JOIN wms_inventory.destinations d
  ON d.code = v.destination_code
 AND d.warehouse_id = w.warehouse_id
ON CONFLICT (external_order_no) DO NOTHING;

-- 11) Products (dev stock for receiving/putaway + outbound allocation)
INSERT INTO wms_inventory.products (
  product_id,
  sku_id,
  shipment_id,
  cargoplace_id,
  box_id,
  qr_code,
  bin_id,
  order_id,
  status,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  s.sku_id,
  sh.shipment_id,
  c.cargoplace_id,
  b.box_id,
  v.qr_code,
  bn.bin_id,
  o.order_id,
  v.status::wms_inventory.product_status,
  now(),
  now()
FROM (
  VALUES
    ('QR-2026-0001', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-001', NULL,         NULL,           'RECEIVED'),
    ('QR-2026-0002', 'Футболка Adidas Originals', 'CP-001', 'BOX-001', NULL,         NULL,           'RECEIVED'),
    ('QR-2026-0003', 'Рюкзак Puma Core',          'CP-002', 'BOX-003', 'A-01-01',    NULL,           'STORED'),
    ('QR-2026-0004', 'Куртка The North Face',     'CP-002', 'BOX-003', 'B-01-01',    NULL,           'STORED'),
    ('QR-2026-0005', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-002', 'A-01-02',    'ORD-2026-0042','ALLOCATED'),
    ('QR-2026-0006', 'Футболка Adidas Originals', 'CP-001', 'BOX-002', 'A-02-01',    'ORD-2026-0042','ALLOCATED'),
    ('QR-2026-0007', 'Рюкзак Puma Core',          'CP-002', 'BOX-003', 'B-01-02',    'ORD-2026-0043','SHIPPED'),
    ('QR-2026-0008', 'Куртка The North Face',     'CP-002', 'BOX-003', 'B-01-02',    'ORD-2026-0043','SHIPPED'),
    ('QR-2026-0009', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-001', 'A-01-01',    NULL,           'STORED'),
    ('QR-2026-0010', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-001', 'A-01-01',    NULL,           'STORED'),
    ('QR-2026-0011', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-001', 'A-01-02',    NULL,           'STORED'),
    ('QR-2026-0012', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-002', 'A-01-02',    NULL,           'STORED'),
    ('QR-2026-0013', 'Кроссовки Nike Air Max 90', 'CP-001', 'BOX-002', 'A-02-01',    NULL,           'STORED'),
    ('QR-2026-0014', 'Футболка Adidas Originals', 'CP-001', 'BOX-001', 'A-02-01',    NULL,           'STORED'),
    ('QR-2026-0015', 'Футболка Adidas Originals', 'CP-001', 'BOX-001', 'A-02-01',    NULL,           'STORED'),
    ('QR-2026-0016', 'Футболка Adidas Originals', 'CP-001', 'BOX-002', 'B-01-01',    NULL,           'STORED'),
    ('QR-2026-0017', 'Футболка Adidas Originals', 'CP-001', 'BOX-002', 'B-01-01',    NULL,           'STORED'),
    ('QR-2026-0018', 'Футболка Adidas Originals', 'CP-001', 'BOX-002', 'B-01-02',    NULL,           'STORED')
) AS v(qr_code, sku_name, cargoplace_code, box_barcode, bin_code, external_order_no, status)
JOIN wms_inventory.inbound_shipments sh ON sh.ttn_code = 'ТТН-2026-00142'
JOIN wms_inventory.skus s ON s.name = v.sku_name
JOIN wms_inventory.cargoplaces c
  ON c.shipment_id = sh.shipment_id
 AND c.cargoplace_code = v.cargoplace_code
LEFT JOIN wms_inventory.boxes b
  ON b.cargoplace_id = c.cargoplace_id
 AND b.box_barcode = v.box_barcode
LEFT JOIN wms_inventory.warehouses w ON w.warehouse_id = sh.warehouse_id
LEFT JOIN wms_inventory.bins bn
  ON bn.warehouse_id = w.warehouse_id
 AND bn.code = v.bin_code
LEFT JOIN wms_inventory.orders o ON o.external_order_no = v.external_order_no
ON CONFLICT (qr_code) DO NOTHING;

-- 12) Order lines for allocation/shipping flows
INSERT INTO wms_inventory.order_lines (order_id, sku_id, qty)
SELECT
  o.order_id,
  s.sku_id,
  v.qty
FROM (
  VALUES
    ('ORD-2026-0042', 'Кроссовки Nike Air Max 90', 1),
    ('ORD-2026-0042', 'Футболка Adidas Originals', 1),
    ('ORD-2026-0043', 'Рюкзак Puma Core', 1),
    ('ORD-2026-0043', 'Куртка The North Face', 1),
    ('ORD-2026-0501', 'Кроссовки Nike Air Max 90', 5),
    ('ORD-2026-0501', 'Футболка Adidas Originals', 5)
) AS v(external_order_no, sku_name, qty)
JOIN wms_inventory.orders o ON o.external_order_no = v.external_order_no
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (order_id, sku_id) DO NOTHING;

-- 13) Outbound dispatches
-- Test dispatch for e2e/manual scan-driver step: DSP-2026-0421-001 -> SHOP-5.
INSERT INTO wms_inventory.outbound_dispatches (
  dispatch_id,
  dispatch_code,
  warehouse_id,
  destination_id,
  vehicle_number,
  driver_name,
  driver_phone,
  status,
  scheduled_at,
  created_at,
  updated_at
)
SELECT
  gen_random_uuid(),
  v.dispatch_code,
  w.warehouse_id,
  d.destination_id,
  v.vehicle_number,
  v.driver_name,
  v.driver_phone,
  v.status::wms_inventory.outbound_dispatch_status,
  v.scheduled_at,
  now(),
  now()
FROM (
  VALUES
    ('DSP-2026-0421-001', 'SHOP-5', 'A123BC777', 'Иван Петров', '+7 (999) 555-00-11', 'SCHEDULED', now() + interval '1 day')
) AS v(dispatch_code, destination_code, vehicle_number, driver_name, driver_phone, status, scheduled_at)
JOIN wms_inventory.warehouses w ON w.name = 'Склад Москва-Север'
JOIN wms_inventory.destinations d
  ON d.code = v.destination_code
 AND d.warehouse_id = w.warehouse_id
ON CONFLICT (dispatch_code) DO NOTHING;

COMMIT;
