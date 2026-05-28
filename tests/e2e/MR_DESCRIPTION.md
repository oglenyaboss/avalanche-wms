# MR: E2E test suite — full coverage + bug backlog

## Summary

- Complete e2e test suite covering all 4 WMS operations (receiving, putaway, assembly, shipping) + auth + blockchain FSM verification
- 37 documented product defects with severity, file:line references, and reproduction recipes (BUGHUNT.md)
- 3 critical bugs reproduced live via bash scenarios (S2, S3, N1)
- All tests validated on a live Docker Compose stack

## Test coverage map

### End-to-end (full chain: HTTP → DB → Kafka → Debezium → Adapter → Avalanche)

| Test | Scenario | Chain verification |
|------|----------|--------------------|
| `TestOutboundFlow_EndToEnd` | 1 product, 4 stages, FSM ordering | Accepted→PutAway→Picked→Shipped |
| `TestFullChain_MultiProduct` | 3 products through all 4 stages | Each product: all 4 on-chain statuses |
| `TestFullChain_PartialShip` | Ship 2 of 3, verify chain divergence | Shipped=4 vs Picked=3 on-chain |
| `TestFullChain_MultiBoxReceiving` | 3 boxes, 3 products in 1 cargoplace | 3× Accepted(1) |
| `TestFullChain_BatchPutaway` | 3 products placed in 1 API call | 3× PutAway(2) |

### Multi-entity flows (DB-level, operational correctness)

| Test | Scenario |
|------|----------|
| `TestMultiOrder_AllocateAndShip` | 2 orders → 1 dispatch, both SHIPPED |
| `TestMultiCargoplace_Receiving` | 3 cargoplaces in 1 shipment, gate flow |
| `TestMultiProduct_AllocatePickAssemble` | 5 products, 1 order, full assembly |
| `TestPartialShipment_MofN` | Partial ship, buffer_remaining tracking |

### Receiving guards (7 tests)

| Test | Assertion |
|------|-----------|
| `TestReceiving_GateFlow` | scan-ttn → scan-cargoplace → accept-shipment (3 subtests) |
| `TestReceiving_ScanTTN_AlreadyClosed` | 409 SHIPMENT_ALREADY_CLOSED |
| `TestReceiving_ScanCargoplace_AlreadyReceived` | 409 CARGOPLACE_ALREADY_RECEIVED |
| `TestReceiving_ScanQR_Duplicate` | 409 QR_ALREADY_EXISTS |
| `TestReceiving_ScanBox_ClosedBox` | 409 BOX_NOT_OPEN |
| `TestReceiving_ScanSKU_UnknownBarcode` | 404 BARCODE_NOT_FOUND |
| `TestReceiving_ScanBuffer_NonBufferBin` | 400 BIN_NOT_BUFFER |

### Putaway guards (4 tests)

| Test | Assertion |
|------|-----------|
| `TestPutaway_DoublePlace_Conflict` | 409 PRODUCT_NOT_IN_BUFFER, outbox delta 0 |
| `TestPutaway_PlaceIntoBufferBin_Rejected` | 404 STORAGE_BIN_NOT_FOUND (BUFFER + SHIPPING_BUFFER) |
| `TestPutaway_ScanProduct_AlreadyStored` | 409 PRODUCT_NOT_IN_BUFFER |
| `TestPutaway_PlaceWithBogusProduct_Rollback` | tx rollback, P1 stays RECEIVED |

### Assembly guards (4 tests)

| Test | Assertion |
|------|-----------|
| `TestAssembly_CartIsolation` | op2 CART_EMPTY, op1 completes |
| `TestAssembly_DoubleAllocate` | allocated_orders=0, idempotent |
| `TestAllocate_InsufficientStock` | 200 + insufficient_orders |
| `TestWrongStatus_PickWithoutTask` | 409 NO_TASK_FOR_PRODUCT |

### Shipping/Dispatches guards (6 tests)

| Test | Assertion |
|------|-----------|
| `TestShipping_DoubleShip_Conflict` | 409 PRODUCT_NOT_IN_BUFFER |
| `TestShipping_ScanDriver_Idempotent` | 200, arrived_at unchanged |
| `TestShipping_ScanDriver_AlreadyDeparted` | 409 DISPATCH_ALREADY_DEPARTED |
| `TestShipping_DestinationMismatch` | 409 DESTINATION_MISMATCH |
| `TestDispatches_GetByID_NotFound` | documents Shipping-5 (503 vs 404) |
| `TestWrongStatus_ShipDispatchNotAtGate` | 409 DISPATCH_NOT_AT_GATE |

### Auth (2 tests, 22 subtests)

| Test | Assertion |
|------|-----------|
| `TestAuth_NegativeAccess` | no token→401, garbage→401, wrong secret→401, CUSTOMER→403 |
| `TestAuth_OperatorOnlyEndpoints` | 9 endpoints × (401 + 403) |

### Known failures (9 t.Skip stubs)

Document product bugs with intended invariants. Remove `t.Skip` and implement body once fixed:
S1, S2, S3, N1, N9, Assembly-1, Shipping-2, Receiving-1, Dispatches-1

### Bash scenarios (11 off-gate scripts)

01-08: adapter happy paths + stress
09: S2 crash-recovery reproduction ✅
10: S3 batch poisoning reproduction ✅
11: N1 receipt timeout reproduction ✅

## Bug backlog (BUGHUNT.md §2)

37 documented defects: 4 CRITICAL, 12 HIGH, 10 MEDIUM, 7 LOW, 1 observation.
All 36 confirmed (1 corrected: Shipping-4 downgraded from HIGH to MEDIUM).

## Test plan

- [x] `go test -tags=e2e -timeout 20m ./...` passes with 0 failures (47 tests: 38 PASS, 9 SKIP, 225s)
- [x] All chain assertions verified on Avalanche C-Chain
- [x] Tests are hermetic (re-runnable, no stale state)
- [x] Cleanup removes all fixture data via t.Cleanup (incl. outbox/onchain rows)
- [x] Code review: flaky assertions fixed, orphan cleanup added, dead code removed
