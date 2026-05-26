# E2E Tests

Two layers of end-to-end coverage for the WMS → ledger pipeline:

1. **Go integration suite** (primary) — drives the **full outbound flow through the real WMS HTTP API** and verifies on-chain mirroring: `WMS API → public.outbox_events → Debezium CDC → Kafka (wms.events.v1) → ledger-adapter → Avalanche C-Chain`. Plus edge-case coverage (auth, validation, FSM guards, idempotency, multi-product). Build tag `//go:build e2e`.
2. **Bash adapter-level scenarios** (`scenarios/*.sh`) — publish straight to the unified Kafka topic via `kcat`, bypassing WMS/outbox/Debezium, to test the **adapter in isolation** (batching, revert/DLQ, ordering).

## Pre-requisites
- Docker Compose v2.22+, Go 1.25+, `docker` in PATH (the Go suite uses testcontainers; `psql`/`jq` only for the bash scenarios).
- macOS/arm64: `avalanchego` is pinned `platform: linux/amd64` and runs under emulation.

## Go suite — quick start
```bash
# Fresh, isolated stack (project "blockchain_project_e2e"); brings up only the
# services under test, runs the whole suite, tears down. Creates .env from
# .env.example if missing.
make e2e-test-outbound

# Against a long-lived stack you manage yourself (faster iteration):
cd tests/e2e
E2E_USE_EXISTING_STACK=true CONTRACT_ADDR=<addr> RPC_URL=http://localhost:9650/ext/bc/C/rpc \
  go test -tags=e2e -count=1 -v ./...
```

### Env toggles (testmain_test.go)
| Var | Effect |
|---|---|
| `E2E_USE_EXISTING_STACK=true` | Don't manage Docker; run against whatever is already up. |
| `E2E_KEEP_STACK=true` | Don't tear the stack down after the run (for debugging). |
| `E2E_SKIP_STACK_RESET=true` | Skip the pre-run `down -v`. |
| `E2E_SKIP_TESTMAIN=true` | Skip all setup (used for compile-checks: `go test -tags=e2e -run=NONE`). |

The suite uses an **isolated compose project `blockchain_project_e2e`**, so it can never wipe a developer's `blockchain_project` dev stack. It brings up only `postgres, db-init, kafka, kafka-init, debezium, avalanchego, contract-deploy, ledger-adapter, wms_app` (no monitoring → no host-port clashes).

### What the Go suite covers
| File | Coverage |
|---|---|
| `outbound_happy_test.go` | Full flow → all stages COMMITTED on-chain, `itemStatus==Shipped`, on-chain FSM ordering. |
| `auth_test.go` | No/garbage/wrong-secret token → 401; CUSTOMER → 403; operator-guard across putaway/assembly/shipping. |
| `validation_test.go` | Malformed JSON / bad UUID / nonexistent entity / wrong bin type → 400/404. |
| `wrongstatus_test.go` | Out-of-order FSM transitions → 409 (receiving / assembly / shipping guards). |
| `idempotency_test.go` | Double close-cargoplace → 409 + **no duplicate outbox event** (FSM-guard dedup). |
| `multiproduct_test.go` | Per-run multi-product order allocate → pick → assemble (re-runnable; fixes harness T11). |
| `partialshipment_test.go` | Ship M of N: order/dispatch complete only after all items ship; on-chain isolation. |
| `insufficientstock_test.go` | Allocate shortage → 200 + `insufficient_orders`, all-or-nothing (order NEW, 0 tasks). |
| `noop_test.go` | Allocate a destination with no orders → 0/0. |
| `known_failures_test.go` | `t.Skip` specs for S1/S2/S3 + Assembly-1/N1/Shipping-2/Receiving-1 (flip green once fixed). |

Helpers: `api_test.go` (HTTP + auth + error helpers), `db_assertions_test.go`, `chain_assertions_test.go`, `fixtures_test.go` (per-run unique fixtures + FK-ordered cleanup).

## Bash adapter-level scenarios
```bash
docker compose --profile test up -d
./tests/e2e/wait_for_ready.sh
./tests/e2e/run_all.sh
docker compose --profile test down -v
```
All scenarios publish to the **single unified topic `wms.events.v1`** with an `aggregate_type` header (the #42 consolidation), via `kcat`.

| Файл | Что проверяет |
|---|---|
| `01-receiving-happy.sh` | receiving event → `batchAccept` → itemStatus=1 (Accepted), COMMITTED |
| `02-putaway-happy.sh` | Accepted → putaway → `batchPutAway` → itemStatus=2 |
| `03-picking-happy.sh` | PutAway → picking → `batchPick` → itemStatus=3 |
| `04-shipping-happy.sh` | Picked → shipping → `batchShip` → itemStatus=4 (Shipped) |
| `05-batch-receiving.sh` | 3 events in one batch-tx, all COMMITTED, one tx_hash |
| `06-revert-invalid-transition.sh` | batchPick without Accepted/PutAway → revert → FAILED + DLQ |
| `07-idempotency-restart.sh` | re-publish after restart → no on-chain duplicate |
| `08-ordering-stress.sh` | 5×(receiving+putaway) rapid-fire → FSM ordering, zero FAILED |
| `09-s2-crash-recovery.sh` | S2: redelivered SENT/PENDING row must reconcile, not FAIL (fills 07's blind spot) |
| `10-s3-batch-poisoning.sh` | S3: one poison must not FAIL valid siblings (needs `BATCH_TIMEOUT` override — see header) |
| `11-receipt-timeout.sh` | N1: a mined tx must reconcile to COMMITTED, not FAIL (needs `RECEIPT_POLL_TIMEOUT` override) |

## Troubleshooting
- **`itemStatus`/`wait_for_status` timeout** — `docker logs ledger-adapter`; common causes: missing header `id`, chain revert.
- **`chain-id mismatch`** — `cast chain-id --rpc-url $RPC_URL` must be `43112` (local Avalanche C-Chain).
- **`db connection refused`** — `docker compose ps` → postgres health.
- **avalanchego "does not provide platform linux/amd64"** — stale arm64 image cached under the project name; `docker rmi <project>-avalanchego` and let it rebuild, or retag a good amd64 build.

## Known limitations
- Product bugs are encoded as `t.Skip` specs in `known_failures_test.go` and, for S2/S3/N1, as off-gate bash repros `scenarios/09-11`. The full prioritised backlog — every defect and untested path from the 2026-05-25 multi-module bug-hunt — is in [BUGHUNT.md](BUGHUNT.md). Remove a stub's skip / implement its body once the product is fixed.
- `scenarios/10` and `11` need a temporary adapter env override (documented in their headers) and are **UNVALIDATED** pending a live run; `09` is self-contained.
