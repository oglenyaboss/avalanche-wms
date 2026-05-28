#!/usr/bin/env bash
set -euo pipefail
# 09 — S2 crash-recovery double-submit (CRITICAL).
#
# This reproduces the gap that 07-idempotency-restart.sh MISSES. Scenario 07 drives the
# event to COMMITTED (a terminal state) BEFORE re-delivering it, so filterAndMarkPending
# correctly skips it — 07 only ever exercises the happy terminal-skip path. S2 lives in
# the *non-terminal* window: after BatchCall has broadcast the tx (row = SENT) or right
# after InsertPending (row = PENDING) but before MarkCommitted. A crash there leaves the
# Kafka offset uncommitted, so the same event is redelivered; the adapter then RESUBMITS
# it (filterAndMarkPending skips only COMMITTED/FAILED), the contract reverts
# "Duplicate eventId", and recordFailure marks the event FAILED + DLQ even though it
# already succeeded on-chain. The SENT sub-case is worst: the known tx_hash is discarded
# instead of resuming WaitReceipt.
#
# This script injects the SENT/PENDING window directly (psql) and redelivers the same
# event_id. No special adapter config is required. It EXITS NON-ZERO while the bug
# exists and 0 once the adapter reconciles non-terminal rows instead of resubmitting.

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 09 S2: a non-terminal (SENT/PENDING) row must reconcile, not become FAILED ==="

run_variant() {
  local inject=$1 # SENT | PENDING
  local eid pid iid tx0 st txn ichain
  eid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  pid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  iid=$(cast_cmd keccak "$pid" | tr -d '[:space:]')

  echo "--- variant: injected status=$inject ---"

  # 1. Drive a fresh receiving event to a genuine on-chain COMMITTED.
  publish_event "receiving" "$eid" "$pid" '{}'
  wait_for_status "$eid" "COMMITTED" 30 2
  wait_for_item_status "$iid" "1" 10 1
  tx0=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='$eid'")

  # 2. Simulate the crash window: the row is non-terminal and the Kafka offset was
  #    never committed, so the broker will redeliver the same message.
  psql_q "UPDATE public.onchain_events SET status='$inject' WHERE event_id='$eid'" >/dev/null

  # 3. Redeliver the SAME event_id.
  publish_event "receiving" "$eid" "$pid" '{}'
  sleep 8

  st=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$eid'")
  txn=$(psql_q "SELECT COALESCE(tx_hash,'') FROM public.onchain_events WHERE event_id='$eid'")
  ichain=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "$iid" --rpc-url "$RPC_URL" | tr -d '[:space:]')

  # Assertions — these FAIL while S2 exists.
  if [ "$st" = "FAILED" ]; then
    echo "FAIL[$inject]: event went FAILED after redelivery, but it already COMMITTED on-chain (S2)" >&2
    exit 1
  fi
  if [ "$st" != "COMMITTED" ]; then
    echo "FAIL[$inject]: expected COMMITTED after reconcile, got '${st:-<none>}'" >&2
    exit 1
  fi
  if [ "$txn" != "$tx0" ]; then
    echo "FAIL[$inject]: tx_hash changed ($tx0 -> $txn); the original tx was discarded, not reused" >&2
    exit 1
  fi
  if [ "$ichain" != "1" ]; then
    echo "FAIL[$inject]: on-chain itemStatus changed to $ichain, expected 1 (no duplicate transition)" >&2
    exit 1
  fi
  echo "  OK[$inject]: status=$st, tx_hash stable, on-chain itemStatus=1"
}

run_variant "SENT"
run_variant "PENDING"
echo "PASS"
