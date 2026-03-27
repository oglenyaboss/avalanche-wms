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

-- 3) Bins (A/B + receiving buffer)
INSERT INTO wms_inventory.bins (bin_id, warehouse_id, code, section, volume, created_at, updated_at)
SELECT
  gen_random_uuid(),
  w.warehouse_id,
  v.code,
  v.section,
  v.volume,
  now(),
  now()
FROM wms_inventory.warehouses w
JOIN (
  VALUES
    ('A-01-01', 'A', 120.0::numeric),
    ('A-01-02', 'A', 120.0::numeric),
    ('A-02-01', 'A', 110.0::numeric),
    ('B-01-01', 'B', 130.0::numeric),
    ('B-01-02', 'B', 130.0::numeric),
    ('BUFFER-01', 'BUFFER', 200.0::numeric)
) AS v(code, section, volume) ON true
WHERE w.name = 'Склад Москва-Север'
ON CONFLICT DO NOTHING;

-- 4) SKUs
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
    ('Куртка The North Face', 'Демисезонная куртка', 12.0::numeric)
) AS v(name, description, volume)
ON CONFLICT DO NOTHING;

-- 4.5) Barcode -> SKU mapping (many barcodes for one SKU)
INSERT INTO wms_inventory.sku_barcodes (sku_id, barcode)
SELECT s.sku_id, v.barcode
FROM (
  VALUES
    ('Кроссовки Nike Air Max 90', '4600000000011'),
    ('Кроссовки Nike Air Max 90', '4600000000028'),
    ('Футболка Adidas Originals', '4600000000035'),
    ('Рюкзак Puma Core', '4600000000042'),
    ('Куртка The North Face', '4600000000059')
) AS v(sku_name, barcode)
JOIN wms_inventory.skus s ON s.name = v.sku_name
ON CONFLICT (barcode) DO NOTHING;

-- 5) Inbound shipment
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

-- 6) Cargoplaces
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

-- 7) Boxes
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

-- 8) Expected SKUs in cargoplaces
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

-- 9) Orders (customer = existing admin user)
INSERT INTO wms_inventory.orders (order_id, external_order_no, customer_id, warehouse_id, status, created_at, updated_at)
SELECT
  gen_random_uuid(),
  v.external_order_no,
  u.user_id,
  w.warehouse_id,
  v.status::wms_inventory.order_status,
  now(),
  now()
FROM (
  VALUES
    ('ORD-2026-0042', 'ALLOCATED'),
    ('ORD-2026-0043', 'SHIPPED')
) AS v(external_order_no, status)
JOIN public.users u ON u.username = 'admin'
JOIN wms_inventory.warehouses w ON w.name = 'Склад Москва-Север'
ON CONFLICT (external_order_no) DO NOTHING;

-- 10) Products (8 items, mixed statuses)
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
    ('QR-2026-0007', 'Рюкзак Puma Core',          'CP-002', 'BOX-003', 'B-01-02',    'ORD-2026-0043','ASSEMBLED'),
    ('QR-2026-0008', 'Куртка The North Face',     'CP-002', 'BOX-003', 'B-01-02',    'ORD-2026-0043','SHIPPED')
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

COMMIT;
