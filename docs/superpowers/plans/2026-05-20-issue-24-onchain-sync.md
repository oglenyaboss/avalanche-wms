# Issue #24: On-chain Status Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace dead `onchain_status` columns with VIEWs, fix putaway event_id divergence, and serialize receiving scans to prevent WMS-blockchain divergence.

**Architecture:** Three sequential commits: (1) share event_id between putaway ops and outbox tables, (2) strip onchain_status from Go code + migration with VIEWs, (3) move cargoplace reads inside transactions with FOR UPDATE.

**Tech Stack:** Go 1.22, PostgreSQL 16 (pgx/v5), sql migrations

**Spec:** `docs/superpowers/specs/2026-05-20-issue-24-onchain-sync-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `wms/internal/putaway/types.go` | Add `EventIDs` to `OutboxEventsParams`, remove `OnChainStatus` from `InsertPutawayParams` |
| Modify | `wms/internal/putaway/service.go` | Generate eventIDs once, pass to both InsertPutaway and InsertOutboxEvents |
| Modify | `wms/internal/putaway/repository.go` | Use passed EventIDs, remove onchain_status from INSERT |
| Modify | `wms/internal/putaway/service_test.go` | Update mock to capture and verify shared EventIDs |
| Modify | `wms/internal/assembly/repository.go` | Remove onchain_status from MarkTaskDone UPDATE |
| Modify | `wms/internal/shipping/repository.go` | Remove onchain_status from BatchInsertShippings INSERT |
| Modify | `wms/internal/domain/putaway.go` | Remove OnchainStatus, OnchainTxHash fields |
| Modify | `wms/internal/domain/shipping.go` | Remove OnchainStatus, OnchainTxHash fields |
| Modify | `wms/internal/domain/assembly_task.go` | Remove OnchainStatus, OnchainTxHash fields |
| Delete | `wms/internal/domain/operation_onchain_status.go` | Dead type + constants for dropped enum |
| Create | `wms/migrations/0008_chain_views.up.sql` | DROP columns + CREATE VIEWs |
| Create | `wms/migrations/0008_chain_views.down.sql` | Restore columns + DROP VIEWs |
| Modify | `wms/internal/receiving/repository.go` | Add GetCargoplaceByIDForUpdate method |
| Modify | `wms/internal/receiving/service.go` | Move cargoplace reads inside WithTx for ScanQR, ScanBox, ScanSKU; add FOR UPDATE to CloseCargoplaceWithOutbox |

---

## Task 1: Share putaway event_id between ops and outbox

**Files:**
- Modify: `wms/internal/putaway/types.go`
- Modify: `wms/internal/putaway/service.go:135-205`
- Modify: `wms/internal/putaway/repository.go:206-251`
- Modify: `wms/internal/putaway/service_test.go`

- [ ] **Step 1: Update types — add EventIDs, remove OnChainStatus**

In `wms/internal/putaway/types.go`, replace the full file:

```go
package putaway

import (
	"time"

	"github.com/google/uuid"
)

type InsertPutawayParams struct {
	EventID    uuid.UUID
	ProductID  uuid.UUID
	FromBinID  uuid.UUID
	BinID      uuid.UUID
	OperatorID uuid.UUID
	OccurredAt time.Time
}

type ProductBufferItem struct {
	ProductID uuid.UUID
	QRCode    string
	Status    string
	SKUName   string
}

type OutboxEventsParams struct {
	EventIDs     []uuid.UUID
	ProductIDs   []uuid.UUID
	StorageBinID uuid.UUID
}
```

- [ ] **Step 2: Update service — generate eventIDs once, pass to both methods**

In `wms/internal/putaway/service.go`, replace `PlaceProductsToStorageBin` from line 135 through line 205 (the `return nil` closing the `WithTx` callback). The full function body inside `WithTx` becomes:

```go
	err := s.repo.WithTx(ctx, func(txRepo putawayRepository) error {
		var err error
		storageBin, err = txRepo.GetStorageBinByID(ctx, storageBinID)
		if err != nil {
			return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin: %w", err)
		}

		verifiedBuffers := make(map[uuid.UUID]struct{})
		eventIDs := make([]uuid.UUID, 0, len(productsIDs))

		for _, productID := range productsIDs {
			product, err := txRepo.GetProductsByIDForUpdate(ctx, productID)
			if err != nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin get product %s: %w", productID, err)
			}
			if product.BinID == nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin product %s: %w", productID, ErrProductNotInBuffer)
			}
			fromBinID := *product.BinID

			if _, ok := verifiedBuffers[fromBinID]; !ok {
				if _, err := txRepo.GetBufferBinByID(ctx, fromBinID); err != nil {
					if errors.Is(err, ErrBufferBinNotFound) {
						return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin product %s: %w", productID, ErrProductNotInBuffer)
					}
					return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin verify buffer for product %s: %w", productID, err)
				}
				verifiedBuffers[fromBinID] = struct{}{}
			}

			if err := txRepo.UpdateProductStorage(ctx, productID, storageBinID); err != nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin update product %s: %w", productID, err)
			}

			eventID := uuid.New()
			eventIDs = append(eventIDs, eventID)

			if err := txRepo.InsertPutaway(ctx, &InsertPutawayParams{
				EventID:    eventID,
				ProductID:  productID,
				FromBinID:  fromBinID,
				BinID:      storageBinID,
				OperatorID: operatorID,
				OccurredAt: occurredAt,
			}); err != nil {
				return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin insert putaway: %w", err)
			}

			placedCount++
		}

		if err := txRepo.InsertOutboxEvents(ctx, &OutboxEventsParams{
			EventIDs:     eventIDs,
			ProductIDs:   productsIDs,
			StorageBinID: storageBinID,
		}); err != nil {
			return fmt.Errorf("putaway.Service.PlaceProductsToStorageBin insert outbox events: %w", err)
		}

		return nil
	})
```

- [ ] **Step 3: Update repository — InsertPutaway without onchain_status**

In `wms/internal/putaway/repository.go`, replace `InsertPutaway` (lines 206-218):

```go
func (r *Repository) InsertPutaway(ctx context.Context, params *InsertPutawayParams) error {
	const query = `
		INSERT INTO wms_ops.putaways(event_id, product_id, from_bin_id, bin_id, operator_id, occurred_at)
		VALUES($1, $2, $3, $4, $5, $6)`

	_, err := r.q.Exec(ctx, query, params.EventID, params.ProductID, params.FromBinID, params.BinID, params.OperatorID, params.OccurredAt)
	if err != nil {
		return fmt.Errorf("putaway.Repository.InsertPutaway exec: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Update repository — InsertOutboxEvents uses passed EventIDs**

In `wms/internal/putaway/repository.go`, replace `InsertOutboxEvents` (lines 220-251):

```go
func (r *Repository) InsertOutboxEvents(ctx context.Context, params *OutboxEventsParams) error {
	if len(params.ProductIDs) == 0 {
		return nil
	}

	aggregateIDs := make([]uuid.UUID, 0, len(params.ProductIDs))
	payloadHashes := make([]string, 0, len(params.ProductIDs))

	for _, productID := range params.ProductIDs {
		payloadHash, err := payloadHashForPutaway(productID, params.StorageBinID)
		if err != nil {
			return fmt.Errorf("putaway.Repository.InsertOutboxEvents build payload hash: %w", err)
		}

		aggregateIDs = append(aggregateIDs, productID)
		payloadHashes = append(payloadHashes, payloadHash)
	}

	const query = `
		INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
		SELECT event_id, aggregate_id, 'putaway', 'wms.putaway.v1', payload_hash
		FROM unnest($1::uuid[], $2::uuid[], $3::text[]) AS events(event_id, aggregate_id, payload_hash)`

	if _, err := r.q.Exec(ctx, query, params.EventIDs, aggregateIDs, payloadHashes); err != nil {
		return fmt.Errorf("putaway.Repository.InsertOutboxEvents exec: %w", err)
	}

	return nil
}
```

- [ ] **Step 5: Update test mock — capture OutboxEventsParams for verification**

In `wms/internal/putaway/service_test.go`, replace the `InsertOutboxEvents` mock method (line 141-147) and add a field:

Add field to `mockPutawayRepo` struct (after `outboxCalls int`):
```go
	lastOutboxParams *OutboxEventsParams
```

Replace mock method:
```go
func (m *mockPutawayRepo) InsertOutboxEvents(_ context.Context, params *OutboxEventsParams) error {
	m.outboxCalls++
	m.lastOutboxParams = params
	if m.insertOutboxErr != nil {
		return m.insertOutboxErr
	}
	return nil
}
```

Add a new test after `TestPlaceProductsToStorageBinSuccess` (after line 491):

```go
func TestPlaceProductsEventIDSharing(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()

	mockRepo := &mockPutawayRepo{
		storageBin: &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
		productsMap: map[uuid.UUID]*domain.Product{
			productID1: {ProductID: productID1, Status: "RECEIVED", BinID: &bufferBinID},
			productID2: {ProductID: productID2, Status: "RECEIVED", BinID: &bufferBinID},
		},
	}

	svc := NewService(mockRepo)
	_, err := svc.PlaceProductsToStorageBin(context.Background(), operatorID, []uuid.UUID{productID1, productID2}, storageBinID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockRepo.insertedPutaways) != 2 {
		t.Fatalf("expected 2 putaway records, got %d", len(mockRepo.insertedPutaways))
	}
	if mockRepo.lastOutboxParams == nil {
		t.Fatal("expected outbox params to be captured")
	}
	if len(mockRepo.lastOutboxParams.EventIDs) != 2 {
		t.Fatalf("expected 2 outbox event IDs, got %d", len(mockRepo.lastOutboxParams.EventIDs))
	}

	for i, putaway := range mockRepo.insertedPutaways {
		if putaway.EventID != mockRepo.lastOutboxParams.EventIDs[i] {
			t.Fatalf("putaway[%d].EventID=%s != outbox.EventIDs[%d]=%s",
				i, putaway.EventID, i, mockRepo.lastOutboxParams.EventIDs[i])
		}
	}
}
```

- [ ] **Step 6: Verify**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/putaway/...`
Expected: all tests pass, no compilation errors.

- [ ] **Step 7: Commit**

```bash
git add wms/internal/putaway/
git commit -m "fix(putaway): share event_id between putaways and outbox_events

Generate eventIDs once in PlaceProductsToStorageBin and pass the same
UUIDs to both InsertPutaway and InsertOutboxEvents. This ensures
wms_ops.putaways.event_id == public.outbox_events.event_id, which is
required for the chain-status VIEWs in the next commit.

Also removes OnChainStatus from InsertPutawayParams (column will be
dropped in migration 0008).

Closes the event_id divergence described in issue #24."
```

---

## Task 2: Strip onchain_status from all Go code

**Files:**
- Modify: `wms/internal/assembly/repository.go:292-297`
- Modify: `wms/internal/shipping/repository.go:295-313`
- Modify: `wms/internal/domain/putaway.go`
- Modify: `wms/internal/domain/shipping.go`
- Modify: `wms/internal/domain/assembly_task.go`
- Delete: `wms/internal/domain/operation_onchain_status.go`

- [ ] **Step 1: Remove onchain_status from assembly MarkTaskDone**

In `wms/internal/assembly/repository.go`, replace lines 293-297:

```go
	const query = `
		UPDATE wms_ops.assembly_tasks
		SET status = 'DONE', operator_id = $2,
		    occurred_at = NOW(), updated_at = NOW()
		WHERE event_id = $1 AND status = 'PENDING'`
```

- [ ] **Step 2: Remove onchain_status from shipping BatchInsertShippings**

In `wms/internal/shipping/repository.go`, replace the const query block (lines 295-313):

```go
	const query = `
		INSERT INTO wms_ops.shippings (
			event_id,
			product_id,
			dispatch_id,
			operator_id,
			shipped_at,
			occurred_at
		)
		SELECT
			event_id,
			product_id,
			$3,
			$4,
			NOW(),
			NOW()
		FROM unnest($1::uuid[], $2::uuid[]) AS events(event_id, product_id)`
```

- [ ] **Step 3: Clean up domain structs**

In `wms/internal/domain/putaway.go`, replace entire file:

```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

type Putaway struct {
	ID         int64     `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	ProductID  uuid.UUID `json:"product_id"`
	FromBinID  uuid.UUID `json:"from_bin_id"`
	BinID      uuid.UUID `json:"bin_id"`
	OperatorID uuid.UUID `json:"operator_id"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}
```

In `wms/internal/domain/shipping.go`, replace entire file:

```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

type Shipping struct {
	ID         int64     `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	ProductID  uuid.UUID `json:"product_id"`
	DispatchID uuid.UUID `json:"dispatch_id"`
	OperatorID uuid.UUID `json:"operator_id"`
	ShippedAt  time.Time `json:"shipped_at"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}
```

In `wms/internal/domain/assembly_task.go`, replace entire file:

```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

type AssemblyTask struct {
	ID            int64      `json:"id"`
	EventID       uuid.UUID  `json:"event_id"`
	OrderID       uuid.UUID  `json:"order_id"`
	ProductID     uuid.UUID  `json:"product_id"`
	SKUID         uuid.UUID  `json:"sku_id"`
	FromBinID     uuid.UUID  `json:"from_bin_id"`
	DestinationID uuid.UUID  `json:"destination_id"`
	Section       *string    `json:"section"`
	Status        TaskStatus `json:"status"`
	OperatorID    *uuid.UUID `json:"operator_id"`
	OccurredAt    *time.Time `json:"occurred_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
```

- [ ] **Step 4: Delete operation_onchain_status.go**

```bash
rm wms/internal/domain/operation_onchain_status.go
```

- [ ] **Step 5: Verify compilation**

Run: `cd wms && go build ./...`
Expected: compiles cleanly. No references to `OperationOnchainStatus` remain.

Run: `grep -rn "OperationOnchainStatus\|OnchainStatus\|OnchainTxHash" wms/internal/ --include="*.go"`
Expected: no output.

- [ ] **Step 6: Run all tests**

Run: `cd wms && go test -race -count=1 ./...`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add wms/internal/assembly/repository.go wms/internal/shipping/repository.go wms/internal/domain/
git commit -m "refactor(wms): strip onchain_status from all Go code

Remove onchain_status column references from:
- assembly/repository.go MarkTaskDone UPDATE
- shipping/repository.go BatchInsertShippings INSERT
- domain structs (Putaway, Shipping, AssemblyTask)
- domain/operation_onchain_status.go (deleted)

Prepares for migration 0008 which drops the columns."
```

---

## Task 3: Migration 0008 — DROP columns + CREATE VIEWs

**Files:**
- Create: `wms/migrations/0008_chain_views.up.sql`
- Create: `wms/migrations/0008_chain_views.down.sql`

- [ ] **Step 1: Create up migration**

Create `wms/migrations/0008_chain_views.up.sql`:

```sql
BEGIN;

-- Step 1: Drop indexes, columns, and orphaned enum
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

-- Step 2: Create VIEWs with explicit column lists
-- Column lists verified against migrations 0001 + 0005 + 0007

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

- [ ] **Step 2: Create down migration**

Create `wms/migrations/0008_chain_views.down.sql`:

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

- [ ] **Step 3: Commit**

```bash
git add wms/migrations/0008_chain_views.up.sql wms/migrations/0008_chain_views.down.sql
git commit -m "refactor(wms): replace onchain_status columns with chain VIEWs

Migration 0008:
- DROP onchain_status and onchain_tx_hash from putaways, shippings, assembly_tasks
- DROP TYPE wms_ops.operation_onchain_status
- CREATE VIEW v_putaways_with_chain, v_shippings_with_chain, v_assembly_tasks_with_chain
  joining ops tables with public.onchain_events via event_id

Chain status is now single-source-of-truth from onchain_events.
COALESCE handles the Debezium lag window (shows PENDING until adapter creates the row).

Closes #24 (blocks 1+2). Reverse-outbox deferred to #41."
```

---

## Task 4: Add GetCargoplaceByIDForUpdate to receiving repository

**Files:**
- Modify: `wms/internal/receiving/repository.go:177-201`
- Modify: `wms/internal/receiving/service.go:54-83` (interface)

- [ ] **Step 1: Add GetCargoplaceByIDForUpdate method**

In `wms/internal/receiving/repository.go`, add after the existing `GetCargoplaceByID` method (after line 201):

```go
func (r *Repository) GetCargoplaceByIDForUpdate(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error) {
	const query = `
		SELECT cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at
		FROM wms_inventory.cargoplaces
		WHERE cargoplace_id = $1
		FOR UPDATE`

	var cp domain.Cargoplace
	err := r.q.QueryRow(ctx, query, cargoplaceID).Scan(
		&cp.CargoplaceID,
		&cp.ShipmentID,
		&cp.CargoplaceCode,
		&cp.Status,
		&cp.ReceivedAtGateAt,
		&cp.CreatedAt,
		&cp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByIDForUpdate: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByIDForUpdate scan: %w", err)
	}

	return &cp, nil
}
```

- [ ] **Step 2: Add to interface**

In `wms/internal/receiving/service.go`, add to `receivingRepository` interface (after line 60, the `GetCargoplaceByID` line):

```go
	GetCargoplaceByIDForUpdate(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error)
```

- [ ] **Step 3: Verify compilation**

Run: `cd wms && go build ./...`
Expected: fails — test mock doesn't implement the new interface method yet. That's expected.

- [ ] **Step 4: Add mock method to receiving test**

Find the receiving test mock file and add the stub. Run:

```bash
grep -rn "func.*mockReceivingRepo.*GetCargoplaceByID\b" wms/internal/receiving/ --include="*_test.go"
```

Add a corresponding `GetCargoplaceByIDForUpdate` method that delegates to the same mock logic as `GetCargoplaceByID`.

- [ ] **Step 5: Verify**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/receiving/...`
Expected: compiles and all existing tests pass.

---

## Task 5: Serialize ScanQR with FOR UPDATE on cargoplace

**Files:**
- Modify: `wms/internal/receiving/service.go:525-619`

- [ ] **Step 1: Rewrite ScanQR — move all mutable reads inside WithTx**

In `wms/internal/receiving/service.go`, replace `ScanQR` (lines 525-619):

```go
func (s *Service) ScanQR(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxID uuid.UUID,
	skuID uuid.UUID,
	qrCode string,
) (*ScanQRResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || boxID == uuid.Nil || skuID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", ErrInvalidInput)
	}
	qrCode, err := validateScanCode(qrCode)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", err)
	}

	sku, err := s.repo.GetSKUByID(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR get sku: %w", err)
	}

	productID := uuid.New()
	product := &domain.Product{
		ProductID:    productID,
		SKUID:        sku.SKUID,
		CargoplaceID: cargoplaceID,
		BoxID:        &boxID,
		QRCode:       qrCode,
		Status:       domain.ProductStatusReceived,
	}

	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanQR get cargoplace: %w", err)
		}
		if cargoplace.Status != cargoplaceStatusTableInProgress {
			return fmt.Errorf("receiving.Service.ScanQR: %w", ErrCargoplaceNotInProgress)
		}

		product.ShipmentID = cargoplace.ShipmentID

		box, err := txRepo.GetBoxByID(ctx, boxID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanQR get box: %w", err)
		}
		if box.CargoplaceID != cargoplaceID {
			return fmt.Errorf("receiving.Service.ScanQR: %w", ErrBoxNotInCargoplace)
		}
		if box.Status != boxStatusOpen {
			return fmt.Errorf("receiving.Service.ScanQR: %w", ErrBoxNotOpen)
		}

		if err := txRepo.InsertProduct(ctx, product); err != nil {
			return fmt.Errorf("receiving.Service.ScanQR insert product: %w", err)
		}

		if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: cargoplaceID,
			BoxID:        &boxID,
			OperatorID:   operatorID,
			Action:       "SCAN_QR",
			SKUID:        &skuID,
			QRCode:       &qrCode,
			ProductID:    &productID,
			OccurredAt:   time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.ScanQR insert log: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	receivedInCargoplace, err := s.repo.CountProductsByCargoplace(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR count products in cargoplace: %w", err)
	}
	expectedInCargoplace, err := s.repo.CountExpectedItemsByCargoplace(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR count expected items: %w", err)
	}

	return &ScanQRResult{
		ProductID: productID,
		SKUID:     sku.SKUID,
		SKUName:   sku.Name,
		QRCode:    qrCode,
		Status:    domain.ProductStatusReceived,
		Progress: receivingProgress{
			ReceivedInCargoplace: receivedInCargoplace,
			ExpectedInCargoplace: expectedInCargoplace,
		},
	}, nil
}
```

- [ ] **Step 2: Verify**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/receiving/...`
Expected: compiles and tests pass.

---

## Task 6: Serialize ScanBox with FOR UPDATE on cargoplace

**Files:**
- Modify: `wms/internal/receiving/service.go:391-463`

- [ ] **Step 1: Rewrite ScanBox — merge two WithTx into one with cargoplace lock**

In `wms/internal/receiving/service.go`, replace `ScanBox` (lines 391-463):

```go
func (s *Service) ScanBox(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxBarcode string,
) (*ScanBoxResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.ScanBox: %w", ErrInvalidInput)
	}
	boxBarcode, err := validateScanCode(boxBarcode)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanBox: %w", err)
	}

	var box *domain.Box
	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanBox get cargoplace: %w", err)
		}
		if cargoplace.Status != cargoplaceStatusTableInProgress {
			return fmt.Errorf("receiving.Service.ScanBox: %w", ErrCargoplaceNotInProgress)
		}

		box, err = txRepo.GetBoxByCargoplaceAndBarcode(ctx, cargoplaceID, boxBarcode)
		switch {
		case err == nil:
			if box.Status != boxStatusOpen {
				return fmt.Errorf("receiving.Service.ScanBox: %w", ErrBoxNotOpen)
			}
			if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
				CargoplaceID: cargoplaceID,
				BoxID:        &box.BoxID,
				OperatorID:   operatorID,
				Action:       "SCAN_BOX",
				BoxBarcode:   &boxBarcode,
				OccurredAt:   time.Now().UTC(),
			}); err != nil {
				return fmt.Errorf("receiving.Service.ScanBox insert log: %w", err)
			}
		case errors.Is(err, ErrBoxNotFound):
			box, err = txRepo.UpsertBox(ctx, cargoplaceID, boxBarcode, boxStatusOpen)
			if err != nil {
				return fmt.Errorf("receiving.Service.ScanBox upsert box: %w", err)
			}
			if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
				CargoplaceID: cargoplaceID,
				BoxID:        &box.BoxID,
				OperatorID:   operatorID,
				Action:       "SCAN_BOX",
				BoxBarcode:   &boxBarcode,
				OccurredAt:   time.Now().UTC(),
			}); err != nil {
				return fmt.Errorf("receiving.Service.ScanBox insert log: %w", err)
			}
		default:
			return fmt.Errorf("receiving.Service.ScanBox get box by barcode: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &ScanBoxResult{
		BoxID:      box.BoxID,
		BoxBarcode: box.BoxBarcode,
		Status:     box.Status,
	}, nil
}
```

- [ ] **Step 2: Verify**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/receiving/...`
Expected: compiles and tests pass.

---

## Task 7: Serialize ScanSKU with FOR UPDATE on cargoplace

**Files:**
- Modify: `wms/internal/receiving/service.go:465-523`

- [ ] **Step 1: Rewrite ScanSKU — move all reads inside WithTx**

In `wms/internal/receiving/service.go`, replace `ScanSKU` (lines 465-523):

```go
func (s *Service) ScanSKU(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxID uuid.UUID,
	barcode string,
) (*ScanSKUResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || boxID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", ErrInvalidInput)
	}
	barcode, err := validateScanCode(barcode)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", err)
	}

	var sku *domain.SKU
	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		cargoplace, err := txRepo.GetCargoplaceByIDForUpdate(ctx, cargoplaceID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanSKU get cargoplace: %w", err)
		}
		if cargoplace.Status != cargoplaceStatusTableInProgress {
			return fmt.Errorf("receiving.Service.ScanSKU: %w", ErrCargoplaceNotInProgress)
		}

		box, err := txRepo.GetBoxByID(ctx, boxID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanSKU get box: %w", err)
		}
		if box.CargoplaceID != cargoplaceID {
			return fmt.Errorf("receiving.Service.ScanSKU: %w", ErrBoxNotInCargoplace)
		}
		if box.Status != boxStatusOpen {
			return fmt.Errorf("receiving.Service.ScanSKU: %w", ErrBoxNotOpen)
		}

		sku, err = txRepo.GetSKUByBarcode(ctx, barcode)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanSKU get sku by barcode: %w", err)
		}

		return txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: cargoplaceID,
			BoxID:        &boxID,
			OperatorID:   operatorID,
			Action:       "SCAN_SKU",
			SKUID:        &sku.SKUID,
			OccurredAt:   time.Now().UTC(),
		})
	}); err != nil {
		return nil, err
	}

	return &ScanSKUResult{
		SKUID:   sku.SKUID,
		SKUName: sku.Name,
		Barcode: barcode,
		Message: "Наклейте QR на товар",
	}, nil
}
```

- [ ] **Step 2: Verify**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/receiving/...`
Expected: compiles and tests pass.

---

## Task 8: Add FOR UPDATE to CloseCargoplaceWithOutbox

**Files:**
- Modify: `wms/internal/receiving/repository.go:702-777`
- Modify: `wms/internal/receiving/service.go:729-764`

- [ ] **Step 1: Add FOR UPDATE to CloseCargoplaceWithOutbox**

In `wms/internal/receiving/repository.go`, inside `CloseCargoplaceWithOutbox`, add a SELECT FOR UPDATE on the cargoplace row **before** the existing UPDATE. Insert after `defer func() { _ = tx.Rollback(ctx) }()` (after line 712) and before the `updateCargoplaceQuery`:

```go
	var cpStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM wms_inventory.cargoplaces WHERE cargoplace_id = $1 FOR UPDATE`,
		params.CargoplaceID).Scan(&cpStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox lock cargoplace: %w", err)
	}
	if cpStatus != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotInProgress)
	}
```

- [ ] **Step 2: Remove service-level cargoplace read from CloseCargoplace**

In `wms/internal/receiving/service.go`, replace `CloseCargoplace` (lines 729-764):

```go
func (s *Service) CloseCargoplace(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
) (*CloseCargoplaceResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace: %w", ErrInvalidInput)
	}

	occurredAt := time.Now().UTC()
	txResult, err := s.repo.CloseCargoplaceWithOutbox(ctx, &CloseCargoplaceParams{
		CargoplaceID: cargoplaceID,
		OperatorID:   operatorID,
		OccurredAt:   occurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace close cargoplace with outbox: %w", err)
	}

	summary := buildCloseCargoplaceSummary(txResult.ExpectedSKUs, txResult.ReceivedSKUCounts)

	return &CloseCargoplaceResult{
		CargoplaceID:        cargoplaceID,
		Status:              cargoplaceStatusTableClosed,
		Summary:             summary,
		OutboxEventsCreated: txResult.OutboxEventsCreated,
	}, nil
}
```

- [ ] **Step 3: Verify all receiving tests**

Run: `cd wms && go build ./... && go test -race -count=1 ./internal/receiving/...`
Expected: compiles and tests pass.

- [ ] **Step 4: Run full test suite**

Run: `cd wms && go test -race -count=1 ./... && go vet ./...`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add wms/internal/receiving/
git commit -m "fix(receiving): serialize table-phase scans with FOR UPDATE on cargoplace

Move GetCargoplaceByID reads inside transactions for ScanQR, ScanBox,
ScanSKU, and CloseCargoplaceWithOutbox. Use FOR UPDATE to acquire a
row lock on the cargoplace, serializing scan operations with
CloseCargoplace.

This prevents the race where ScanQR inserts a product after
CloseCargoplace has already taken its outbox snapshot, causing
permanent WMS-blockchain divergence.

Closes #24 (block 3). Unblocks #34 (e2e tests)."
```

---

## Task 9: Create branch, push, create MR

- [ ] **Step 1: Create branch from dev**

```bash
git checkout dev
git checkout -b fix/issue-24-onchain-sync
```

Note: all commits from tasks 1-8 should be made on this branch. If they were made on dev, cherry-pick them onto the new branch.

- [ ] **Step 2: Push and create MR**

```bash
git push -u origin fix/issue-24-onchain-sync
glab mr create \
  --title "fix(integration): sync onchain_status via VIEWs, fix event_id divergence and receiving race (#24)" \
  --description "## Summary
- **Block 1**: Share event_id between putaways and outbox_events (fixes JOIN for VIEWs)
- **Block 2**: Migration 0008 — DROP onchain_status/onchain_tx_hash columns, CREATE VIEWs joining with onchain_events
- **Block 3**: Serialize ScanQR/ScanBox/ScanSKU with FOR UPDATE on cargoplace row (fixes CRITICAL race condition)

## Test plan
- [ ] \`go test -race ./...\` passes
- [ ] \`go vet ./...\` clean
- [ ] Migration 0008 applies cleanly on fresh DB
- [ ] Migration 0008 down restores columns and drops VIEWs
- [ ] putaway event_id matches between wms_ops.putaways and public.outbox_events

Closes #24. Unblocks #34 (e2e tests). Reverse-outbox deferred to #41."
```
