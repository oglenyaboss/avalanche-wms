# E2E Tests

Two layers of end-to-end coverage for the WMS → ledger pipeline:

1. **Go integration suite** (primary) — drives the **full outbound flow through the real WMS HTTP API** and verifies on-chain mirroring: `WMS API → public.outbox_events → Debezium CDC → Kafka (wms.events.v1) → ledger-adapter → Avalanche Subnet-EVM`. Plus edge-case coverage (auth, validation, FSM guards, idempotency, multi-product). Build tag `//go:build e2e`.
2. **Bash adapter-level scenarios** (`scenarios/*.sh`) — publish straight to the unified Kafka topic via `kcat`, bypassing WMS/outbox/Debezium, to test the **adapter in isolation** (batching, revert/DLQ, ordering).

## Pre-requisites
- Docker Compose v2.22+, Go 1.25+, `docker` in PATH (the Go suite uses testcontainers; `psql`/`jq` only for the bash scenarios).
- macOS/arm64: `subnet-node1` builds and runs natively (the subnet-evm plugin is built for `TARGETARCH`) — no QEMU emulation, faster startup.

## Go suite — quick start
```bash
# Fresh, isolated stack (project "blockchain_project_e2e"); brings up only the
# services under test, runs the whole suite, tears down. Creates .env from
# .env.example if missing.
make e2e-test-outbound

# Against a long-lived stack you manage yourself (faster iteration).
# RPC_URL / CONTRACT_ADDR are pulled from the shared_state volume automatically,
# so you usually don't set them. To target an external node, pass the dynamic
# Subnet-EVM path: RPC_URL=http://localhost:9650/ext/bc/<blockchainID>/rpc CONTRACT_ADDR=<addr>
cd tests/e2e
E2E_USE_EXISTING_STACK=true go test -tags=e2e -count=1 -v ./...
```

### Env toggles (testmain_test.go)
| Var | Effect |
|---|---|
| `E2E_USE_EXISTING_STACK=true` | Don't manage Docker; run against whatever is already up. |
| `E2E_KEEP_STACK=true` | Don't tear the stack down after the run (for debugging). |
| `E2E_SKIP_STACK_RESET=true` | Skip the pre-run `down -v`. |
| `E2E_SKIP_TESTMAIN=true` | Skip all setup (used for compile-checks: `go test -tags=e2e -run=NONE`). |

The suite uses an **isolated compose project `blockchain_project_e2e`**, so it can never wipe a developer's `blockchain_project` dev stack. It brings up only `postgres, db-init, kafka, kafka-init, debezium, subnet-node1, subnet-init, contract-deploy, ledger-adapter, wms_app` (no monitoring → no host-port clashes).

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
Bring the stack up under the **same compose project the `lib/` scripts target** —
`lib/env.sh` and `lib/kafka.sh` reference the `blockchain_project_e2e_*` network and
volumes, so the `-p blockchain_project_e2e` flag is required (omitting it names the
project after the directory and the scripts then fail with `network ... not found`).
Use raw `docker compose`, not the Go harness: a testcontainers stack
(`E2E_KEEP_STACK=true`) is reaped by Ryuk when the test process exits and is unusable
by these scripts.

```bash
docker compose -p blockchain_project_e2e --profile test up -d
./tests/e2e/wait_for_ready.sh
./tests/e2e/run_all.sh
docker compose -p blockchain_project_e2e --profile test down -v
```

Scenarios `10` and `11` need a temporary ledger-adapter env override. Keep
`docker-compose.yaml` untouched by using an overlay file and recreating just the adapter:

```bash
printf 'services:\n  ledger-adapter:\n    environment:\n      BATCH_TIMEOUT: "5s"\n' > /tmp/o.yaml
docker compose -p blockchain_project_e2e -f docker-compose.yaml -f /tmp/o.yaml \
  --profile test up -d --no-deps --force-recreate ledger-adapter
bash tests/e2e/scenarios/10-s3-batch-poisoning.sh    # use RECEIPT_POLL_TIMEOUT: "1ms" for 11
docker compose -p blockchain_project_e2e --profile test up -d --no-deps --force-recreate ledger-adapter  # restore
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
- **`chain-id mismatch`** — `cast chain-id --rpc-url $RPC_URL` must be `99999` (local Avalanche Subnet-EVM). Note the RPC path is dynamic: `/ext/bc/<blockchainID>/rpc` (blockchainID is in `shared_state:/rpc_url.txt`).
- **`db connection refused`** — `docker compose ps` → postgres health.
- **subnet-node1 won't start / serves a stale chain after a Dockerfile or genesis change** — stale image cached under the project name; `docker rmi <project>-subnet-node1` and let it rebuild (`<project>` is `blockchain_project_e2e` for this suite).

## Known limitations
- Product bugs are encoded as `t.Skip` specs in `known_failures_test.go` and, for S2/S3/N1, as off-gate bash repros `scenarios/09-11`. The full prioritised backlog — every defect and untested path from the 2026-05-25 multi-module bug-hunt — is in [BUGHUNT.md](BUGHUNT.md). Remove a stub's skip / implement its body once the product is fixed.
- `scenarios/09`, `10`, and `11` were **validated on a live stack 2026-05-26** — each exits non-zero, reproducing its bug (09→S2, 10→S3, 11→N1). `10`/`11` require the adapter env override above; `09` is self-contained.
