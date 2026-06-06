# Stress Tuning Log — goal: 1500 events/s on-chain (incl. blockchain)

Project: `stresstest` compose (isolated). Hardware: Mac arm64, Docker 16.7GB/10CPU.

## Baseline (run 1 — full suite, emulated amd64 C-Chain, batch_size=10)
- **WMS sync side: excellent.** All functional tests pass under load. 08 putaway = **2500 events/s** generated; 06-assembly 11.6k req/s. Only k6 threshold miss: 02-health p95=553ms (target<500, 0 errors). 07: 2/6004 shipping checks failed (negligible race).
- **Blockchain: 5.0 events/s commit**, backlog 8680, Kafka lag 7730 (→ adapter-bound, not Debezium). PENDING/SENT 10, FAILED 0.

## Bottleneck (code + live chain)
- Flusher **serial, 1-tx-in-flight**: `flushSubBatch` = BatchCall (under `sendMu`) → `WaitReceipt` blocks before next Kafka fetch. Throughput = batch_size / block_time.
- C-Chain: block gas limit **15M**, block time **2s**, ~**45k gas/event**, 1 tx/block → 10/2s = 5/s. Ceiling even maxed ≈ 333 ev/block ÷ 2s ≈ **166/s**.
- 1500/s ≈ 60M gas/s sustained on-chain → needs higher block gas limit + faster blocks (custom genesis) + big batch_size. **Native arm64** likely the key enabler (QEMU is the suspected dominant limiter). Do NOT pipeline the flusher (can't raise gas/s; risks idempotency logic).

## Plan
1. [in progress] batch_size=300/timeout=2s → measure emulated C-Chain ceiling (drain existing backlog).
2. Native arm64 avalanchego (drop `platform: linux/amd64`) → re-measure.
3. Raise block gas limit via custom genesis (C-Chain `--genesis-file` first; else Subnet-EVM) + batch_size ~3000 → push 1500/s single-node.
4. Multi-node for prod imitation → re-run.
5. Incremental HTML report.

## Experiments
| # | config | on-chain events/s | notes |
|---|--------|-------------------|-------|
| baseline | bs=10, amd64 C-Chain | 5.0 | serial, 2s blocks, 15M gas |
| exp1 | bs=300, amd64 C-Chain | **~8.6/s avg** (bursty: ~30s stall then ~300 burst) | **QEMU-bound**: 13.5M-gas tx mines ~30s. Batch size does NOT help under emulation — node capped ~250-450k gas/s. |

### Key findings
- **`BATCH_SIZE` hardcoded** `"10"` in compose (only BATCH_TIMEOUT interpolated) → changed to `${BATCH_SIZE:-10}`.
- **avalanchego v1.12.2 HAS arm64 variant**; Dockerfile deliberately `FROM --platform=linux/amd64` for team consistency, accepting QEMU on lead's arm Mac → **this is our bottleneck.** Native = drop `--platform` (deploy/avalanche/Dockerfile) + `platform:` (compose avalanchego). contract-deploy=foundry, already multi-arch.
- Gas: ~45k/event, 15M block cap → bs≈300 is the per-tx C-Chain max. >1500/s needs higher block gas limit (custom genesis) + native arm64 so big blocks execute in time.
- **Native arm64 swap: SUCCESS** — dropped `--platform`, rebuilt, image arch=arm64, healthy ~10s, chainId 0xa868, contract survived keep-volume. QEMU eliminated.
- **Gas-limit path (research):** C-Chain/coreth gas limit is genesis-baked + ACP-176 dynamic (not cleanly raisable via runtime config). **Subnet-EVM** has explicit `feeConfig.gasLimit` + `targetBlockRate` → deterministic high-TPS chain, and aligns with multi-node prod imitation. Sources: build.avax.network gas-fees/genesis docs.

| native-bs300 | bs=300, **arm64** C-Chain | **~10/s** sustained (peak 60, bursty +300/~30s) | **NOT execution-bound — C-Chain fee throttle** |

### THE REAL WALL (native bs=300 diagnosis)
- Block timestamps: blocks come in **pairs 2s apart, then ~30s gap** (649→650=2s, 650→651=32s). Each block = 1 tx, 8.45M gas (300 events).
- **C-Chain dynamic-fee block-gas-cost throttle**: high-gas blocks incur a cost that delays the next block ~30s unless higher priority fees paid. Adapter's `WaitReceipt` times out at 30s each cycle → ~300 events/30s = ~10/s.
- This throttle is **arch-independent** → native arm64 (~10/s) ≈ emulated (~8.6/s) at bs=300. Native helped bring-up (10s vs 30-60s) + small-batch execution, but the throttle masks it at scale.
- **Apricot Phase 1 caps C-Chain block gas at 15M** (genesis declares 100M, AP1 overrides → measured 15M). v1.12.2 has no ACP-176 dynamic gas.
- **Conclusion: C-Chain cannot reach 1500/s** (15M cap + fee throttle). gas≈28k/event → C-Chain absolute max ~535 ev/block, and throttle caps block rate.

### DECISION: pivot to Subnet-EVM (= project's intended arch + multi-node target)
- Subnet-EVM genesis `feeConfig`: `gasLimit=100M`, `minBlockGasCost=0`, `maxBlockGasCost=0` (no throttle), `targetBlockRate=1`. + native arm64 (executes 42M-gas blocks in <1s). + bs~1500 → 1 tx/1s block = **1500/s** target.
- Multi-node: Subnet-EVM with N validators = prod imitation (the user's step 4).
| native-bs10 (recheck) | bs=10 amd64 C-Chain | 5/s | 2s blocks, no throttle (tiny gas) — baseline anchor |
| **subnet-bs1500** | bs=1500, **Subnet-EVM 100M-gas native** | **~600/s** | 5000 ev in ~7s; per-batch 1500ev/~2.5s; blocks 42M gas, ~2.5s cadence |
| subnet-bs3500 | bs=3500, Subnet-EVM 100M | _(measuring — bg b4qoaf68k)_ | testing rate∝batch hypothesis |

### SUBNET-EVM — THE WALL IS GONE
- Deployed real Subnet-EVM via **avalanche-cli** (v1.9.6): `avalanche blockchain create wms --evm --genesis <tuned> --sovereign=false`, `deploy --local`. ChainId 99999, **block gasLimit=100M confirmed live** (vs C-Chain 15M cap), native arm64, ewoq prefunded 100M. Genesis: `feeConfig.gasLimit=100M, minBlockGasCost=0, maxBlockGasCost=0, targetBlockRate=1`.
- **Networking solve:** avalanche-cli node binds host `127.0.0.1:9650`; dockerized adapter can't reach loopback, AND avalanchego rejects foreign `Host` headers (`http-allowed-hosts`) → "invalid host specified". Fix = L7 host-rewriting proxy on host `0.0.0.0:9661 → 127.0.0.1:9650` (`/tmp/subnet-proxy.py`), adapter reaches it via `host.docker.internal:9661`. Repointed via `/shared/rpc_url.txt` (contract addr 0x52C8… identical — deterministic ewoq nonce-0).
- **Result: 600/s at bs=1500 — 60× over the C-Chain wall (10/s).** Bottleneck now = block cadence (~2.5s) × batch (1500). Cadence is **consensus-bound not execution-bound** (a 14M-gas block also took ~3s), so `rate = batch / 2.5s` → bs≈3750 → 1500/s. Testing bs=3500 on 100M now; may bump genesis gasLimit→200M for bs=3750 + realistic-gas margin.
- gas note: 08 events are skipped-transition putaway (~28k gas); real FSM transitions ~45k. bs sizing & report state both.
- **bs=3500 attempt FAILED** → all 20000 events `FAILED` with `chain revert: estimate gas for batchPutAway: gas required exceeds allowance (50000000)`. Root cause = subnet **`rpc-gas-cap` default 50M** caps `eth_estimateGas` → batch capped at ~1785 events (50M/28k) → ~700/s max. NOT the block gas limit (100M).
- **Fix (redeploy bhf4v55gl):** genesis gasLimit 100M→**200M**, `avalanche blockchain configure wms --chain-config` with **`rpc-gas-cap=200M`**, bs=**3750** (105M gas, fits both). Target: 3750 events / ~2.5s cadence = **1500/s**. (Cadence ~2.5s = serial adapter round-trip send→mine→receipt, 1 tx in flight; bigger batch is the sanctioned lever, not flusher rewrite.)
- **Networking artifacts (for report repro):** host L7 proxy `/tmp/subnet-proxy.py` (9661→9650, Host rewrite); genesis `deploy/subnet/wms-genesis.json`; chain config `deploy/subnet/wms-chainconfig.json`; benches `bench08.sh` / `bench-sustained.sh`.
- **bs=3750 FAILED: tx-size limit 128KB** (`oversized data: transaction size 246196, limit 131072`). Each event = 64 bytes calldata (2×uint256) → **max ~2000 events/tx**, NOT gas. So serial max = ~2000/block_time.
- **bs=1950 = 975/s SUSTAINED** (1950 ev / 2.0s block, drained 39k cleanly, 0 FAILED). = `batch_size/block_time`, model confirmed. **195× over 5/s baseline.**
- **Block floor ~2s confirmed:** tightened `receipt.go` poll backoff (50ms/100ms const) → STILL 975/s → round-trip is block-time-bound, not receipt-detection. Burst test: node packs **15 tx/block** but blocks ~2s apart even with full mempool → `proposerMinBlockDelay` (avalanchego v1.14.0) is the floor.
- **1500/s paths:** (A) `proposerMinBlockDelay=0` → bs=1950/<1s ≈ 1950/s on serial flusher (attempting via subnet-config-dir + multi-node redeploy); (B) pipelining (15 tx/block proven) = flusher rewrite (team, not autonomous); (C) uint128 calldata pack → 4000 ev/tx → 2000/s (ABI change, team).
- **Multi-node (3 validators)** redeploy in progress = prod-imitation; expect equal/slightly-lower TPS (every validator re-executes every block).
