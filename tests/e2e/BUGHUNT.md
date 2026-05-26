# E2E Bug-Hunt Backlog (MR 59 line)

Branch `test/wms-outbound-flow-e2e-onchain-fsm`. Produced 2026-05-25 by a 5-agent
parallel sweep — one agent per layer (receiving / putaway / assembly / shipping+dispatches
/ ledger-adapter+contract) — that mapped each module's FSM, error paths, and invariants,
cross-referenced them against the existing `tests/e2e/` suite, and ranked latent defects.

This file is the durable output of that sweep. It lists **every** finding, what was shipped
this batch, and what is deferred with a proposed scenario shape. All line refs are against
the worktree at sweep time; re-confirm before acting.

**Severity:** CRITICAL = data loss / silent DB↔chain divergence · HIGH = functional bug or
stuck state · MEDIUM = maintainability/robustness · LOW = minor/style.

> ⚠️ The reproduction recipes below and in the `*_pendingFix` stubs were derived from code
> analysis, **not** from a live run (the suite was not executable at authoring time — a host
> port clash held :5432/:9092). Validate the CRITICAL/HIGH recipes on a live stack before
> trusting them; a wrong recipe in a stub is debt.

---

## 1. Shipped in this batch

**Green tests added to the `-tags=e2e` gate (correct-behaviour coverage):**

| Test | Closes |
|---|---|
| `newMultiProductFixture` + rewritten `TestMultiProduct_AllocatePickAssemble` | Harness bug **T11**: the old test consumed the seed's fixed 5+5 stock and only passed on a fresh stack. Now per-run + re-runnable in `E2E_USE_EXISTING_STACK`. |
| `TestPartialShipment_MofN` | Spot-ship M of N (DB contract): order/dispatch complete only after all items ship; `buffer_remaining` tracks the rest. On-chain isolation deferred — see §3. |
| `TestAllocate_InsufficientStock` | Allocate shortage contract: HTTP 200 + `insufficient_orders` (not 422), all-or-nothing (order NEW, unit STORED/unbound, 0 tasks). |
| `TestAuth_OperatorOnlyEndpoints` | Auth gap: no-token→401 + CUSTOMER→403 across **putaway / assembly / shipping** (previously only receiving). |

**Off-gate bug reproductions (exit non-zero while the bug lives, 0 once fixed):**

| Artifact | Proves | Status |
|---|---|---|
| `scenarios/09-s2-crash-recovery.sh` | **S2** (CRITICAL) — fills the blind spot in `07` | Self-contained; ready to run |
| `scenarios/10-s3-batch-poisoning.sh` | **S3** (HIGH) adapter-side fan-out | Needs `BATCH_TIMEOUT` override (header); UNVALIDATED |
| `scenarios/11-receipt-timeout.sh` | **N1** (CRITICAL) | Needs `RECEIPT_POLL_TIMEOUT` override (header); UNVALIDATED |

**`t.Skip` documentation stubs added to `known_failures_test.go`** (same convention as S1/S2/S3):
`TestAssemblyCartLostOnRestart_pendingFix`, `TestAdapterN1_ReceiptTimeoutDivergence_pendingFix`,
`TestShippingShipBeforeAssembled_pendingFix`, `TestReceivingOpenBoxReachesChain_pendingFix`.

**False-confidence annotations** (these *looked* like they covered S2; they don't):
`scenarios/07-idempotency-restart.sh` (drives to COMMITTED before redelivery) and
`ledger-adapter/internal/consumer/flusher_test.go::TestFlusher_StrandedPending_GetsRetried`
(mock chain lacks `_requireNewEvent`).

---

## 2. Product defects (genuine — not test bugs)

Sorted by severity. "Tracked as" → the durable artifact. S1/S2/S3 were found in the earlier
MR-59 review; N*/BUG-* are new from this sweep.

| ID | Sev | Defect | Key ref | Tracked as |
|---|---|---|---|---|
| S1 | CRITICAL | WMS never reads `onchain_events.status`; a chain revert/DLQ leaves the order SHIPPED with no compensation (one-way coupling). | `shipping/service.go` Ship; no reader of `onchain_events` in `wms/**` | `TestS1_*` stub |
| S2 | CRITICAL | Crash-recovery resubmits PENDING/SENT events → contract `Duplicate eventId` revert → event wrongly FAILED+DLQ despite succeeding. | `consumer/flusher.go:155-180` (skips only COMMITTED/FAILED), `:117` | `TestS2_*` stub + `scenarios/09` |
| N1 | CRITICAL | `WaitReceipt` timeout marks event FAILED while the tx is still mining → tx mines → DB FAILED vs chain COMMITTED forever. No crash needed. | `consumer/flusher.go:124-129`; `RECEIPT_POLL_TIMEOUT` default 30s | `TestAdapterN1_*` stub + `scenarios/11` |
| Assembly-1 | CRITICAL | Pick cart is process-memory only; no endpoint rebuilds it. WMS restart between Pick and ScanShippingBuffer strands products ASSEMBLED / order ALLOCATED forever. | `assembly/service.go:18,266-270,286,376` | `TestAssemblyCartLostOnRestart_*` stub |
| S3 | HIGH | One bad item reverts the whole batch tx; adapter then FAILs+DLQs all valid siblings. | contract `_batchTransition` loop; `consumer/flusher.go:143-153` | `TestS3_*` stub + `scenarios/10` |
| Shipping-2 | HIGH | Ship gates on product status only, never order status. Shipping a not-yet-ASSEMBLED order's ready items leaves the order stuck ALLOCATED (can never reach SHIPPED). | `shipping/service.go:147`; `UpdateOrdersShippedConditional` (status='ASSEMBLED' only) | `TestShippingShipBeforeAssembled_*` stub (pure-HTTP repro) |
| Receiving-1 | HIGH | `close-cargoplace` emits a receiving event for every RECEIVED product regardless of box status, but ScanBuffer only moves CLOSED-box products. An OPEN-box product gets Accepted(1) on-chain with `bin_id` NULL — on-chain but physically unplaceable. | `receiving/repository.go` `listProductIDsByCargoplaceTx` (no box filter) vs `ScanBufferWithLog` (`b.status='CLOSED'`) | `TestReceivingOpenBoxReachesChain_*` stub (pure-HTTP repro) |
| Shipping-5 | HIGH | `GET /dispatches/{id}` for a nonexistent id returns 503, not 404 (`pgx.ErrNoRows` unmapped). | `dispatches/repository.go:121-142`; `dispatches/handler.go:128-131` | Deferred — §3 (clean, trivial test) |
| Shipping-3 | HIGH | Depart-with-unshipped scoping: `CountReadyToShipProductsInBuffer` spans all of a destination's bins; multiple AT_GATE dispatches for one destination can claim each other's cargo. | `shipping/repository.go:383-398` | Deferred — §3 |
| Shipping-4 | HIGH | Two concurrent Ship calls draining the same buffer: one departs (200), the other hits `UpdateDispatchDeparted` 0-rows → unmapped error → 500, though its products did ship. | `shipping/repository.go:400-415` | Deferred — §3 |
| Putaway-4 | HIGH | NULL-section bin is accepted as a storage destination (`section IS DISTINCT FROM 'BUFFER'` is TRUE for NULL) → products stored in an unclassified bin, on-chain advances to PutAway. | `putaway/repository.go:82-97`; `bins.section` nullable (`0001:216`) | Deferred — §3 (pure-HTTP repro) |
| Putaway-5 | HIGH | No cross-warehouse enforcement: a product from warehouse A's buffer can be placed into warehouse B's bin. | `putaway/repository.go:62-97` | Deferred — §3 (needs 2nd warehouse) |
| Receiving-2 | HIGH | Over-receipt: scanning a SKU not in `expected_cargoplace_skus` creates a ghost product that reaches the chain; the close summary hides the overage. | `receiving/service.go:498`; no join to `expected_cargoplace_skus` | Deferred — §3 (may be lenient-by-design; needs product call) |
| Assembly-2 | HIGH* | Re-allocation can mint a duplicate PENDING task + duplicate picking outbox/chain event for one product. *Conditional: currently blocked by the order `status='NEW'` guard; no `(product_id) WHERE status='PENDING'` partial unique index backs it up. | `assembly_tasks` unique only on `event_id` (`0001:408`) | Deferred — §3 |
| N2 | HIGH | Nonce/broadcast race: a transient SendTransaction error after the tx hit the mempool leaves the row PENDING; retry reuses the eventId → `Duplicate eventId` → S2 path. | `chain/client.go:160,197-199` | Deferred — §3 (needs RPC fault injection) |
| N3 | HIGH | Cross-aggregate head-of-line blocking: a failing earlier sub-batch aborts the whole `Flush`; an unrelated later aggregate is starved/retried indefinitely. | `consumer/flusher.go:79-88` | Deferred — §3 |
| Receiving-3 | MEDIUM | TOCTOU: `ScanCargoplace`/`ScanTableCargoplace` read shipment/cargoplace status outside the tx; a concurrent close can mismatch the surfaced error code. | `receiving/service.go:189-194,343-365` | Deferred |
| Receiving-4 | MEDIUM | `ScanBuffer` callable repeatedly; after the first call it moves 0 rows but still writes a log row, with no guard. | `receiving/repository.go:596-623` | Deferred |
| Receiving-5 | MEDIUM | `MarkExpectedAsNotReceived` writes no audit log for the per-cargoplace NOT_RECEIVED transition. | `receiving/repository.go:286-300` | Deferred |
| Putaway-3 | MEDIUM | Placement status guard is SQL-only; the unit tests mock `WithTx` as a passthrough, so real DB lock/rollback is unexercised by unit tests (the new fixture-based e2e tests now hit the real path). | `putaway/service.go:154-175`; `putaway/service_test.go:56-61` | Partially mitigated |
| Putaway-6 | MEDIUM | No bin capacity (`bins.volume`) enforcement on putaway. | `putaway/repository.go` | Deferred |
| Shipping-6 | MEDIUM | Dispatch code generation (`count+1`) is not atomic under concurrency → spurious unique-violation 503s. | `dispatches/repository.go:67-77` | Deferred |
| N4 | MEDIUM | DLQ is published before MarkFailed; if MarkFailed fails, redelivery double-publishes to the DLQ. | `consumer/flusher.go:144-148` | Deferred |
| N5 | MEDIUM | `finalFlush` on shutdown uses `context.Background()`, resubmitting in-flight SENT rows through the S2 path on every routine deploy/restart. | `consumer/consumer.go:170-186` | Deferred |
| N6 | MEDIUM | DLQ replay: replayed messages re-hit terminal/duplicate paths; a fixed-and-replayed S3 sibling would be silently skipped (FAILED is terminal). | `dlq/producer.go`; `consumer/flusher.go:167` | Deferred |
| Assembly-3 | MEDIUM | No warehouse scoping in product allocation (matches on `sku_id` only). | `assembly/repository.go:113-120` | Deferred (needs 2nd warehouse) |
| Putaway-1 | — | **Design observation, not filed as a defect:** `scan-storage-bin` takes `product_ids` directly and never consults the in-memory cart — any operator can place any RECEIVED-in-a-buffer product. This is **intentional** (the cart is a UI counter; the frontend holds the authoritative `product_ids[]` — see the comment at `putaway/service.go:18-19`). Flagged here as an authorization consideration: there is no server-side check that the placing operator is the one who scanned the items. | `putaway/service.go:135,210-229` | Observation only |
| Putaway-2 | LOW | Cross-buffer mixing: one `scan-storage-bin` call may place products from different buffer bins (each validated independently). | `putaway/service.go:151` | Observation |
| Assembly-4 | LOW | `ErrOrderNotNew` is not in `mapServiceError` → would surface as 500 (currently unreachable behind the in-tx guard). | `assembly/handler.go:234-256` | Deferred |
| Assembly-5 | LOW | FIFO allocation `ORDER BY created_at` has no tiebreaker → non-deterministic per-unit bin assignment on `created_at` ties. | `assembly/repository.go:118` | Deferred |
| Shipping-8 | LOW | `scheduled_at` past-check uses wall clock with no tolerance/injection. | `dispatches/handler.go:99` | Deferred |
| N7 | LOW | `/health` always returns 200 — no pool/Kafka/RPC readiness probe, so a wedged adapter still reports healthy. | `ledger-adapter/internal/handler/handler.go:24-33` | Deferred |
| N8 | LOW | `InsertPending` rows are not rolled back on a mid-loop error, expanding the S2 (resubmittable non-terminal) surface. | `consumer/flusher.go:172-176` | Deferred |

---

## 3. Coverage gaps — correct behaviour, still untested (deferred green tests)

Each is implementable through the existing HTTP + DB + chain assertion helpers; many reuse
`newMultiProductFixture` / `newOutboundFixture`. Proposed shape = setup → action → assert.

**Receiving**
- Gate flow: `ScanTTN` (CREATED→GATE_IN_PROGRESS) + `ScanCargoplace` (EXPECTED→RECEIVED_AT_GATE) + auto-close; `ScanTTN` on GATE_CLOSED → 409; duplicate `ScanCargoplace` → 409 `CARGOPLACE_ALREADY_RECEIVED`; `AcceptShipment` marks missing cargoplaces NOT_RECEIVED.
- Table negatives: `ScanQR` duplicate → 409 `QR_ALREADY_EXISTS`; `ScanBox` on CLOSED box → 409 `BOX_NOT_OPEN`; `ScanSKU` unknown barcode → 404 `BARCODE_NOT_FOUND`; box from another cargoplace → 400 `BOX_NOT_IN_CARGOPLACE`; `ScanBuffer` non-buffer bin → 400 `BIN_NOT_BUFFER`.

**Putaway**
- Double-putaway idempotency: re-`scan-storage-bin` a STORED product → 409 `PRODUCT_NOT_IN_BUFFER`, outbox delta 0.
- Storage-bin type guards at the HTTP layer: placing into a BUFFER / SHIPPING_BUFFER bin → 404 `STORAGE_BIN_NOT_FOUND`.
- Already-STORED product via `scan-product` → 409 `PRODUCT_NOT_RECEIVED`.
- **Putaway-4 (defect):** NULL-section bin currently accepted → should be 404. Insert a NULL-section bin, putaway into it; assert it is rejected once fixed.
- Tx rollback: `scan-storage-bin` with `[P1, bogus]` → 404, P1 stays RECEIVED, no putaway outbox.

**Assembly**
- Cart isolation across operators: op1 picks P; op2 `scan-shipping-buffer` → 409 `CART_EMPTY`; op1 still completes. (Reuse `operatorToken` twice.)
- Concurrent allocate of one destination: two parallel `/assembly/allocate` → order allocated exactly once (sum `allocated_orders`==1), no product double-bound.
- Double-allocate idempotency: second `/assembly/allocate` → that order `allocated_orders` contribution 0, task count unchanged.

**Shipping / dispatches**
- Double-ship: re-ship a SHIPPED product (spot) → 409 `PRODUCT_NOT_IN_BUFFER`, one `shippings` row.
- `scan-driver` idempotency: second scan of an AT_GATE dispatch → 200, `arrived_at` unchanged.
- `scan-driver` on a DEPARTED dispatch → 409 `DISPATCH_ALREADY_DEPARTED`.
- Destination mismatch: ship a SHOP-A buffer against a SHOP-B dispatch → 409 `DESTINATION_MISMATCH`.
- **Shipping-5 (defect):** `GET /dispatches/{random uuid}` currently 503 → assert 404 once `pgx.ErrNoRows` is mapped.

**Adapter (off-gate scenarios)**
- N1/N2/N3/N4/N5 reproductions (see §2 refs). N1 has a scenario stub (`11`); the rest need RPC/chaos fault injection.

**Fixture follow-ups (on-chain priming)**
- `newMultiProductFixture` inserts pre-STORED products with no prior receiving/putaway events, so their on-chain `itemStatus` is `None` and any picking/shipping events they generate revert (land FAILED). `TestMultiProduct_AllocatePickAssemble` and `TestPartialShipment_MofN` therefore assert **DB state only**. To also verify on-chain partial isolation (shipped → Shipped(4) while the remainder stays Picked(3)), extend the fixture to emit a receiving + a putaway outbox event per product and `waitForOnchainCommitted` for both before returning (priming each item to PutAway on-chain). That also removes the FAILED-row residue the fixture currently cleans up. The same prerequisite applies before adding on-chain assertions to `TestShippingShipBeforeAssembled_*` (its DB assertion — order stuck ALLOCATED — needs no chain).

---

## 4. Methodology notes

- The merge gate (`-tags=e2e`) stays **green and hermetic**. Product defects are proved by
  **off-gate** artifacts (bash scenarios) and documented by `t.Skip` stubs — never by red
  tests on the gate. Removing a stub's `t.Skip` and implementing its body turns it into a
  live regression once the product is fixed (S2/S3/N1 also have a runnable bash repro).
- Two pre-existing tests gave **false confidence** about S2 and are now annotated (§1).
- Subagent agent IDs (for follow-up via SendMessage) and the raw per-module reports live in
  the session transcript; this file is the distilled, durable form.
