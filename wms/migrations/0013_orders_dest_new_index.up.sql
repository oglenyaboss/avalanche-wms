-- Partial index for assembly.Allocate's bounded FIFO order fetch.
-- GetOrdersByDestinationForUpdate now does: WHERE destination_id=$1 AND status='NEW'
-- ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED. Without this index the planner
-- scans ALL of a destination's orders (idx_orders_destination_id) and filters/sorts;
-- this partial index fetches only the oldest N NEW orders directly (O(N)).
-- Mirrors 0012 (CONCURRENTLY, outside a transaction).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_dest_new
  ON wms_inventory.orders (destination_id, created_at)
  WHERE status = 'NEW';

INSERT INTO public.schema_migrations (version) VALUES (13) ON CONFLICT (version) DO NOTHING;
