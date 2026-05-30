#!/usr/bin/env bash
set -euo pipefail
# 11 — N1 receipt-timeout false FAILED (CRITICAL).
#
# On a WaitReceipt timeout the flusher marks the event FAILED even though the tx may still
# be mining. The tx then mines -> the transition is COMMITTED on-chain while (in the OLD
# code) the DB row stayed terminal FAILED forever. Unlike S2, NO crash and NO redelivery
# are required — silent divergence on a healthy system under nothing more than slow
# confirmation.
#
# FIX (#44 Phase 2): a background reconcile loop re-checks the receipt of stuck SENT/FAILED
# rows and pulls the DB row forward to the real on-chain result (mined+success -> COMMITTED).
# It is read-mostly: it NEVER resubmits (that would reintroduce S2).
#
# Self-contained: shrinks RECEIPT_POLL_TIMEOUT so the flusher's poll always loses the race
# to mining (forcing the wrong FAILED), and shrinks the reconcile cadence so recovery
# happens within the test's patience window. Defaults restored on exit. Exits non-zero
# while N1 exists, 0 once a mined tx reconciles to COMMITTED.

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 11 N1: a tx that mines must reconcile to COMMITTED, never terminal FAILED ==="

restore_adapter() {
  unset RECEIPT_POLL_TIMEOUT RECONCILE_INTERVAL RECONCILE_MIN_AGE
  recreate_adapter && wait_adapter 60 || true
}
trap restore_adapter EXIT
# 1ms poll always times out before the block mines; 2s reconcile cadence + 2s min-age
# recover the row well within the wait_for_status budget below.
export RECEIPT_POLL_TIMEOUT="1ms"
export RECONCILE_INTERVAL="2s"
export RECONCILE_MIN_AGE="2s"
echo "--- recreating ledger-adapter: RECEIPT_POLL_TIMEOUT=1ms, RECONCILE_INTERVAL=2s, RECONCILE_MIN_AGE=2s ---"
recreate_adapter
wait_adapter 60

eid=$(uuidgen | tr '[:upper:]' '[:lower:]')
pid=$(uuidgen | tr '[:upper:]' '[:lower:]')
iid=$(cast_cmd keccak "$pid" | tr -d '[:space:]')

# Publish a perfectly valid receiving event. With the tiny RECEIPT_POLL_TIMEOUT the adapter
# (transiently) marks it FAILED on the poll timeout while the tx still mines.
publish_event "receiving" "$eid" "$pid" '{}'

# The transition DOES land on-chain (the tx mines regardless of the adapter giving up).
if ! wait_for_item_status "$iid" "1" 30 2; then
  echo "INCONCLUSIVE: the transition never reached the chain; cannot judge N1 divergence." >&2
  exit 2
fi

# The reconcile loop must pull the (wrongly) FAILED row forward to COMMITTED once the tx is
# mined. Poll generously to cover min-age (2s) + interval (2s) + slack.
if ! wait_for_status "$eid" "COMMITTED" 20 2; then
  st=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$eid'")
  if [ "$st" = "FAILED" ]; then
    echo "FAIL: event is FAILED in DB but the transition is COMMITTED on-chain — reconcile loop did not recover it (N1)" >&2
  else
    echo "FAIL: expected COMMITTED once the tx mined, got '${st:-<none>}'" >&2
  fi
  exit 1
fi

# tx_hash must be the original mined tx — reconcile reuses it, never resubmits.
txn=$(psql_q "SELECT COALESCE(tx_hash,'') FROM public.onchain_events WHERE event_id='$eid'")
[ -n "$txn" ] || { echo "FAIL: COMMITTED row has no tx_hash; reconcile must reuse the mined tx" >&2; exit 1; }

echo "  ✓ the mined transition reconciled to COMMITTED (tx $txn reused)"
echo "PASS"
