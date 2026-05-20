# Issue #24: On-chain Status Sync via VIEWs

## Summary

Replace dead `onchain_status` / `onchain_tx_hash` columns in `wms_ops.putaways`, `wms_ops.shippings`, `wms_ops.assembly_tasks` with SQL VIEWs that JOIN on `public.onchain_events`. Fix the putaway event_id divergence that breaks the JOIN. Fix the ScanQR/ScanBox/ScanSKU race condition that causes permanent WMS-blockchain divergence.

## Problem Statement

### 1. Dead onchain_status columns

After ledger-adapter confirms a transaction on-chain, it updates `public.onchain_events.status = 'COMMITTED'`. But `wms_ops.putaways.onchain_status` stays `PENDING_ONCHAIN` forever — no code performs the reverse sync. Same for `shippings` and `assembly_tasks`.

Consequence: any UI or audit query reading `wms_ops.*` sees stale data. The real chain status lives only in `onchain_events`.

### 2. Putaway event_id divergence

`putaway/service.go:183` generates `uuid.New()` for `InsertPutaway`. `putaway/repository.go:236` generates a different `uuid.New()` inside `InsertOutboxEvents`. The two tables (`wms_ops.putaways` and `public.outbox_events`) store different UUIDs for the same logical operation.

A VIEW joining on `event_id` returns NULL for chain status — the IDs don't match.

Shipping is correct (one `EventID` passed to both inserts). Assembly was fixed in issue #31.

### 3. ScanQR vs CloseCargoplace race condition (CRITICAL)

All table-phase scan operations (`ScanQR`, `ScanBox`, `ScanSKU`) read cargoplace status **outside** their transaction:

```
ScanQR:
  1. GetCargoplaceByID(ctx, id)     ← outside tx, no lock
  2. check status == TABLE_IN_PROGRESS
  3. WithTx:
       InsertProduct(...)
       InsertReceivingTableLog(...)

CloseCargoplace:
  1. GetCargoplaceByID(ctx, id)     ← outside tx, no lock
  2. CloseCargoplaceWithOutbox:      ← inside tx
       UPDATE cargoplaces SET status='TABLE_CLOSED'
       listProductIDsByCargoplaceTx  ← snapshot of products
       insertOutboxEventsTx          ← outbox for snapshot
```

Race scenario:
1. Operator A calls ScanQR — reads cargoplace as IN_PROGRESS (step 1)
2. Operator B calls CloseCargoplace — takes snapshot of products, writes outbox, commits
3. Operator A's WithTx commits — product inserted into WMS
4. Result: product exists in WMS but is **missing from outbox** → blockchain never sees it → permanent divergence

This is the CRITICAL blocker for e2e tests (issue #34).

## Architecture Decision

**Chosen: Variant F (VIEW + DROP columns)**

Rationale documented in issue #24 comments (2026-04-14, 2026-05-17, 2026-05-20):
- Single source of truth — chain status in one place (`onchain_events`)
- Zero synchronization — nothing to get out of sync
- Consistency with receiving module — already has no `onchain_status` columns
- Minimal code — ~1 day vs ~2 weeks for reverse-outbox
- Performance adequate — JOIN on indexed UUID is sub-millisecond, read path is cold (reporting/UI), not hot (operator scans)

**Deferred: Reverse-outbox (issue #41)** for push notifications and denormalized reads when needed.

## Design

### Block 1: Putaway event_id sharing

#### Changes

**`putaway/types.go`**:
- Add `EventIDs []uuid.UUID` to `OutboxEventsParams`
- Remove `OnChainStatus string` from `InsertPutawayParams`

**`putaway/service.go` — `PlaceProductsToStorageBin`**:
- Collect `eventIDs []uuid.UUID` before the loop
- In the loop: `eventIDs[i] = uuid.New()`, pass same `eventIDs[i]` to `InsertPutaway`
- Pass `EventIDs: eventIDs` to `InsertOutboxEvents`

**`putaway/repository.go` — `InsertPutaway`**:
- Remove `onchain_status` from INSERT query (6 columns → 5)
- Remove `params.OnChainStatus` from Exec args

**`putaway/repository.go` — `InsertOutboxEvents`**:
- Replace `eventIDs = append(eventIDs, uuid.New())` with `eventIDs = params.EventIDs`
- Validate `len(params.EventIDs) == len(params.ProductIDs)`

**`putaway/service_test.go`**:
- Update mock expectations for changed `OutboxEventsParams` struct

#### Invariant

After this change: `wms_ops.putaways.event_id == public.outbox_events.event_id == public.onchain_events.event_id` for every putaway operation. The JOIN chain works end-to-end.

### Block 2: Migration 0008 + code cleanup

#### Execution order (critical)

1. **First**: strip `onchain_status` from all INSERT/UPDATE statements in Go code
2. **Then**: migration drops the columns

The INSERT SQL text explicitly names the `onchain_status` column (`repository.go:209`). If the migration drops the column before the code stops referencing it, PostgreSQL returns a column-not-found error at runtime. (Note: a column with DEFAULT would tolerate being *omitted* from INSERT, but the current code *explicitly names* it — so DROP first would break.)

In a single branch/MR this is safe: both changes ship together. But the **commit order** must be: code first, migration second.

#### Code changes

**`putaway/repository.go` — `InsertPutaway`** (already done in Block 1):
- `onchain_status` removed from INSERT

**`assembly/repository.go` — `MarkTaskDone` (line 295)**:
```sql
-- Before:
SET status = 'DONE', operator_id = $2, onchain_status = 'PENDING_ONCHAIN', ...
-- After:
SET status = 'DONE', operator_id = $2, ...
```

**`shipping/repository.go` — `BatchInsertShippings` (lines 297-313)**:
- Remove `onchain_status` column from INSERT
- Remove `'PENDING_ONCHAIN'` literal from unnest

**Domain struct cleanup**:
- `domain/putaway.go`: remove `OnchainStatus` and `OnchainTxHash` fields
- `domain/shipping.go`: remove `OnchainStatus` and `OnchainTxHash` fields
- `domain/assembly_task.go`: remove `OnchainStatus` and `OnchainTxHash` fields
- `domain/operation_onchain_status.go`: **delete entire file** (type + constants for dropped enum)

#### Migration 0008_chain_views.up.sql

Column lists verified against migrations 0001 + 0005 + 0007:

- **putaways** (0001 + 0005): `id, event_id, product_id, from_bin_id, bin_id, operator_id, occurred_at, created_at`
- **shippings** (0001 + 0007, vehicle_number dropped): `id, event_id, product_id, operator_id, dispatch_id, shipped_at, occurred_at, created_at`
- **assembly_tasks** (0001 + 0007): `id, event_id, order_id, product_id, sku_id, from_bin_id, section, destination_id, status, operator_id, occurred_at, created_at, updated_at`

```sql
BEGIN;

-- Step 1: Drop columns, indexes, and orphaned enum
DROP INDEX IF EXISTS wms_ops.idx_putaways_onchain_status;
DROP INDEX IF EXISTS wms_ops.idx_shippings_onchain_status;

ALTER TABLE wms_ops.putaways
  DROP COLUMN onchain_status,
  DROP COLUMN onchain_tx_hash;

ALTER TABLE wms_ops.shippings
  DROP COLUMN onchain_status,
  DROP COLUMN onchain_tx_hash;

ALTER TABLE wms_ops.assembly_tasks
  DROP COLUMN onchain_status,
  DROP COLUMN onchain_tx_hash;

DROP TYPE wms_ops.operation_onchain_status;

-- Step 2: Create VIEWs with explicit column lists (no SELECT *)
CREATE VIEW wms_ops.v_putaways_with_chain AS
SELECT
  p.id, p.event_id, p.product_id, p.from_bin_id, p.bin_id,
  p.operator_id, p.occurred_at, p.created_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.putaways p
LEFT JOIN public.onchain_events oe USING (event_id);

CREATE VIEW wms_ops.v_shippings_with_chain AS
SELECT
  s.id, s.event_id, s.product_id, s.operator_id, s.dispatch_id,
  s.shipped_at, s.occurred_at, s.created_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.shippings s
LEFT JOIN public.onchain_events oe USING (event_id);

CREATE VIEW wms_ops.v_assembly_tasks_with_chain AS
SELECT
  t.id, t.event_id, t.order_id, t.product_id, t.sku_id,
  t.from_bin_id, t.section, t.destination_id,
  t.status, t.operator_id, t.occurred_at, t.created_at, t.updated_at,
  COALESCE(oe.status::text, 'PENDING') AS chain_status,
  oe.tx_hash                           AS chain_tx_hash,
  oe.error_message                     AS chain_error_message,
  oe.updated_at                        AS chain_updated_at
FROM wms_ops.assembly_tasks t
LEFT JOIN public.onchain_events oe USING (event_id);

INSERT INTO public.schema_migrations(version) VALUES (8);

COMMIT;
```

#### Migration 0008_chain_views.down.sql

```sql
BEGIN;

DROP VIEW IF EXISTS wms_ops.v_putaways_with_chain;
DROP VIEW IF EXISTS wms_ops.v_shippings_with_chain;
DROP VIEW IF EXISTS wms_ops.v_assembly_tasks_with_chain;

CREATE TYPE wms_ops.operation_onchain_status AS ENUM ('PENDING_ONCHAIN', 'ONCHAIN_COMMITTED');

ALTER TABLE wms_ops.putaways
  ADD COLUMN onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  ADD COLUMN onchain_tx_hash text;

ALTER TABLE wms_ops.shippings
  ADD COLUMN onchain_status wms_ops.operation_onchain_status NOT NULL DEFAULT 'PENDING_ONCHAIN',
  ADD COLUMN onchain_tx_hash text;

ALTER TABLE wms_ops.assembly_tasks
  ADD COLUMN onchain_status wms_ops.operation_onchain_status,
  ADD COLUMN onchain_tx_hash text;

CREATE INDEX idx_putaways_onchain_status ON wms_ops.putaways(onchain_status);
CREATE INDEX idx_shippings_onchain_status ON wms_ops.shippings(onchain_status);

DELETE FROM public.schema_migrations WHERE version = 8;

COMMIT;
```

#### VIEW column selection

VIEWs use explicit column lists, not `SELECT *`. Reasons:
- `SELECT *` in a VIEW captures the column list at creation time; schema changes break it
- Explicit lists document exactly what the API layer receives
- `COALESCE(oe.status::text, 'PENDING')` handles the window between WMS INSERT and adapter INSERT (~5s Debezium lag)

### Block 3: ScanQR/ScanBox/ScanSKU race fix

#### New repository method

**`receiving/repository.go`**:
```go
func (r *Repository) GetCargoplaceByIDForUpdate(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error) {
    const query = `
        SELECT cargoplace_id, shipment_id, status, ...
        FROM wms_inventory.cargoplaces
        WHERE cargoplace_id = $1
        FOR UPDATE`
    // Same scan logic as GetCargoplaceByID
}
```

**`receiving/service.go` interface**: add `GetCargoplaceByIDForUpdate` to `receivingRepository`.

#### ScanQR fix

Move cargoplace read + status check inside WithTx:

```go
func (s *Service) ScanQR(...) (*ScanQRResult, error) {
    // validation, validateScanCode — stays outside tx (no DB)
    // SKU lookup — stays outside tx (read-only, not race-sensitive)

    sku, err := s.repo.GetSKUByID(ctx, skuID)

    var product *domain.Product
    if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
        // Lock cargoplace row — serializes with CloseCargoplace
        cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
        if cargoplace.Status != TABLE_IN_PROGRESS { return ErrCargoplaceNotInProgress }

        // Validate box belongs to cargoplace and is open
        box, err := txRepo.GetBoxByID(ctx, boxID)
        if box.CargoplaceID != cargoplaceID { return ErrBoxNotInCargoplace }
        if box.Status != boxStatusOpen { return ErrBoxNotOpen }

        // Insert product + log (was already inside tx)
        product = &domain.Product{...}
        txRepo.InsertProduct(ctx, product)
        txRepo.InsertReceivingTableLog(ctx, ...)
        return nil
    }); err != nil {
        return nil, err
    }

    // Progress counts — outside tx (read-only, non-critical)
    return &ScanQRResult{...}, nil
}
```

#### ScanBox fix

Current ScanBox has two separate WithTx calls (existing box path and new box path). Merge into one:

```go
func (s *Service) ScanBox(...) (*ScanBoxResult, error) {
    // validation — outside tx

    var box *receivingBox
    if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
        cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
        if cargoplace.Status != TABLE_IN_PROGRESS { return ErrCargoplaceNotInProgress }

        box, err = txRepo.GetBoxByCargoplaceAndBarcode(ctx, cargoplaceID, boxBarcode)
        switch {
        case err == nil:
            if box.Status != boxStatusOpen { return ErrBoxNotOpen }
            txRepo.InsertReceivingTableLog(ctx, ...)
        case errors.Is(err, ErrBoxNotFound):
            box, err = txRepo.UpsertBox(ctx, ...)
            txRepo.InsertReceivingTableLog(ctx, ...)
        default:
            return err
        }
        return nil
    }); err != nil {
        return nil, err
    }

    return &ScanBoxResult{...}, nil
}
```

#### ScanSKU fix

Same pattern as ScanQR — move cargoplace read + box validation inside WithTx:

```go
func (s *Service) ScanSKU(...) (*ScanSKUResult, error) {
    // validation — outside tx

    if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
        cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
        if cargoplace.Status != TABLE_IN_PROGRESS { return ErrCargoplaceNotInProgress }

        box, err := txRepo.GetBoxByID(ctx, boxID)
        // box validation...
        // SKU lookup, product insert, log insert...
        return nil
    }); err != nil {
        return nil, err
    }

    return &ScanSKUResult{...}, nil
}
```

#### CloseCargoplace fix

`CloseCargoplaceWithOutbox` already runs inside a transaction with `UPDATE ... WHERE status = 'TABLE_IN_PROGRESS'`. But the cargoplace read at `service.go:738` is outside the tx. Two options:

**Option A**: Move the read inside `CloseCargoplaceWithOutbox` with FOR UPDATE.
**Option B**: Keep as-is — the UPDATE's WHERE clause already guards against the race.

**Chosen: A** — for consistency and to eliminate the TOCTOU window entirely. The UPDATE's RowsAffected=0 check is a fallback, not a primary guard.

**Implementation note**: `CloseCargoplaceWithOutbox` manages its own transaction (`r.db.Begin` at repository.go:706). The FOR UPDATE read must go inside this existing internal transaction, not into a new `WithTx` wrapper. The service-level cargoplace read at `service.go:738` becomes redundant and is removed.

#### Serialization semantics

After the fix, ScanQR and CloseCargoplace both acquire `FOR UPDATE` on the same cargoplace row:
- If ScanQR locks first → CloseCargoplace waits → sees the newly inserted product in its snapshot
- If CloseCargoplace locks first → sets status=CLOSED → ScanQR sees CLOSED → returns error

No more divergence window.

#### GetBoxByID scope

`GetBoxByID` is also called outside the transaction in ScanQR/ScanSKU. Moving it inside the tx is necessary because box status could change (CloseBox) between the read and the insert. Moving all reads of mutable state inside the transaction is the correct pattern.

## What changes for the API layer

Current API handlers read directly from `wms_ops.*` tables. After this change:
- **Write operations** (InsertPutaway, BatchInsertShippings, MarkTaskDone): unchanged, write to base tables
- **Read operations** (list, get-by-id): should read from `v_*_with_chain` VIEWs to include chain status

No existing read endpoints need immediate changes — they don't expose `onchain_status` today. When list/detail endpoints are added for the UI, they should query the VIEWs.

## What does NOT change

- Forward outbox flow (WMS → outbox → Kafka → adapter → chain): untouched
- Ledger-adapter code: untouched
- Debezium configuration: untouched
- Receiving module: no onchain_status columns, no changes needed for Block 2
- Domain types: no changes to `domain.Product`, `domain.Cargoplace`, etc. (But `domain.Putaway`, `domain.Shipping`, `domain.AssemblyTask` structs and `domain/operation_onchain_status.go` are cleaned up in Block 2.)

## Commit strategy

Three commits in one MR:

1. `fix(putaway): share event_id between putaways and outbox_events`
   - Block 1: types.go, service.go, repository.go, service_test.go

2. `refactor(wms): replace onchain_status columns with chain VIEWs`
   - Block 2: strip onchain_status from putaway/assembly/shipping code + domain struct cleanup + delete operation_onchain_status.go + migration 0008

3. `fix(receiving): serialize table-phase scans with FOR UPDATE on cargoplace`
   - Block 3: repository.go (new method), service.go (ScanQR, ScanBox, ScanSKU, CloseCargoplace)

## Testing

- `go build ./...` — compile check after each commit
- `go test -race ./internal/putaway/...` — after commit 1
- `go test -race ./internal/assembly/... ./internal/shipping/...` — after commit 2
- `go test -race ./internal/receiving/...` — after commit 3
- `go vet ./...` and `golangci-lint run` — after all commits

## Risks

| Risk | Mitigation |
|------|-----------|
| Migration runs before code deploys | Single MR, atomic deployment. Down-migration available. |
| VIEW performance at scale | JOIN on UNIQUE-indexed UUID — sub-ms. Issue #41 (reverse-outbox) as upgrade path. |
| FOR UPDATE increases lock contention | Lock held only for duration of scan tx (~1-5ms). Acceptable for warehouse throughput. |
| Existing tests reference onchain_status | Grep and fix all test files in Block 2 commit. |
| assembly_tasks VIEW column list mismatch | Verify against 0001+0007 migrations before writing VIEW. |

## Future work

- Issue #41: Reverse-outbox for push notifications and denormalized reads
- Issue #34: E2e tests (unblocked by Block 3)
- API endpoints reading from VIEWs (when UI is ready)
