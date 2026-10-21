# E2E Tests

End-to-end coverage for the WMS → ledger pipeline — the **Go integration suite**
drives the **full outbound flow through the real WMS HTTP API** and verifies
on-chain mirroring: `WMS API → public.outbox_events → Debezium CDC → Kafka
(wms.events.v1) → ledger-adapter → Avalanche Subnet-EVM`. Plus edge-case
coverage (auth, validation, FSM guards, idempotency, multi-product) and
adapter-level cases (batching, crash recovery, dedup) driven by publishing
signed actions straight to the same Kafka topic from Go. Build tag
`//go:build e2e`.

## Pre-requisites
- Docker Compose v2.22+, Go 1.25+, `docker` in PATH (the Go suite uses testcontainers).
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
| `known_failures_test.go` | Regression tests for the BUGHUNT defects: S1/S2, Assembly-1, N1, Shipping-2, Receiving-1, Dispatches-1, N9. |
| `registry_cli_test.go` | Live registry-cli smoke: register-org (+ repeat rejected), add-admin-key (+ unknown org rejected), list shows both admin keys active. |

Helpers: `api_test.go` (HTTP + auth + error helpers), `db_assertions_test.go`, `chain_assertions_test.go`, `fixtures_test.go` (per-run unique fixtures + FK-ordered cleanup).

## Troubleshooting
- **`itemStatus` assertion timeout** — `docker logs ledger-adapter`; common causes: missing header `id`, chain revert.
- **`chain-id mismatch`** — `cast chain-id --rpc-url $RPC_URL` must be `99999` (local Avalanche Subnet-EVM). Note the RPC path is dynamic: `/ext/bc/<blockchainID>/rpc` (blockchainID is in `shared_state:/rpc_url.txt`).
- **`db connection refused`** — `docker compose ps` → postgres health.
- **subnet-node1 won't start / serves a stale chain after a Dockerfile or genesis change** — stale image cached under the project name; `docker rmi <project>-subnet-node1` and let it rebuild (`<project>` is `blockchain_project_e2e` for this suite).

## Known limitations
- Product defects are encoded as regression tests in `known_failures_test.go`: each reproduces the original fault on the signed-action path and asserts the fixed invariant. The full prioritised backlog — every defect and untested path from the 2026-05-25 multi-module bug-hunt — is in [BUGHUNT.md](BUGHUNT.md).
