BEGIN;

-- 1. Новая таблица: wms_inventory.sku_barcodes
-- Маппинг штрихкодов к SKU (many-to-one: один SKU может иметь
-- несколько штрихкодов — EAN-13, EAN-8, разная упаковка и т.д.)
CREATE TABLE wms_inventory.sku_barcodes (
  id bigserial PRIMARY KEY,
  sku_id uuid NOT NULL REFERENCES wms_inventory.skus(sku_id) ON DELETE CASCADE,
  barcode text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT sku_barcodes_barcode_unique UNIQUE (barcode)
);

COMMENT ON TABLE wms_inventory.sku_barcodes
  IS 'Mapping barcodes -> SKUs (many barcodes per SKU).';
COMMENT ON COLUMN wms_inventory.sku_barcodes.barcode
  IS 'Product barcode (EAN-13, EAN-8, etc). Globally unique.';

CREATE INDEX idx_sku_barcodes_sku_id
  ON wms_inventory.sku_barcodes(sku_id);

-- 2. ALTER: wms_ops.putaways + from_bin_id
-- Откуда забран товар (буфер приемки). Для полного аудит-трейла
-- и симметрии с assembly_tasks.from_bin_id.
ALTER TABLE wms_ops.putaways
  ADD COLUMN from_bin_id uuid
  REFERENCES wms_inventory.bins(bin_id) ON DELETE RESTRICT;

COMMENT ON COLUMN wms_ops.putaways.from_bin_id
  IS 'Source bin (buffer) from which product was taken.';

CREATE INDEX idx_putaways_from_bin_id
  ON wms_ops.putaways(from_bin_id);

INSERT INTO public.schema_migrations(version) VALUES (5);

COMMIT;
