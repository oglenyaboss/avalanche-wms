#!/usr/bin/env bash
set -euo pipefail
# 11 — N1 receipt-timeout false FAILED (CRITICAL; new in the 2026-05-25 bug-hunt sweep).
#
# On a WaitReceipt timeout the flusher marks the event FAILED
# (ledger-adapter/internal/consumer/flusher.go:124-129) even though the tx may still be
# mining. The tx then mines -> the transition is COMMITTED on-chain while the DB row is
# terminal FAILED forever (there is no transition back out of FAILED). Unlike S2, NO
# crash and NO redelivery are required — this is silent divergence on a healthy system
# under nothing more than slow confirmation.
#
# ─── PREREQUISITE (the default stack will NOT reproduce this) ─────────────────────────
# RECEIPT_POLL_TIMEOUT defaults to 30s, far longer than local Avalanche block time, so
# the poll never times out in practice. Recreate the adapter with a tiny timeout so the
# poll gives up before the tx mines (a 1ms timeout always loses the race to mining, so
# no slow-mining setup is needed). RECEIPT_POLL_TIMEOUT is a hardcoded literal in
# docker-compose.yaml, so override it, e.g.:
#
#   # temporarily set, under services.ledger-adapter.environment:
#   #   RECEIPT_POLL_TIMEOUT: "1ms"
#   docker compose -p blockchain_project_e2e --profile test up -d --no-deps --force-recreate ledger-adapter
#
# Restore the 30s value afterwards.
#
# UNVALIDATED: written from code analysis; confirm on a live stack before trusting.
# Exits non-zero while N1 exists, 0 once a mined tx reconciles to COMMITTED.

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 11 N1: a tx that mines must reconcile to COMMITTED, never terminal FAILED ==="

eid=$(uuidgen | tr '[:upper:]' '[:lower:]')
pid=$(uuidgen | tr '[:upper:]' '[:lower:]')
iid=$(cast_cmd keccak "$pid" | tr -d '[:space:]')

# Publish a perfectly valid receiving event. With a tiny RECEIPT_POLL_TIMEOUT the
# adapter (buggily) marks it FAILED on the poll timeout while the tx still mines.
publish_event "receiving" "$eid" "$pid" '{}'

# The transition DOES land on-chain (the tx mines regardless of the adapter giving up).
if ! wait_for_item_status "$iid" "1" 30 2; then
  echo "INCONCLUSIVE: the transition never reached the chain; cannot judge N1 divergence." >&2
  exit 2
fi

# Give the adapter a moment to settle its (wrong) terminal state, then inspect the DB.
sleep 3
st=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$eid'")

if [ "$st" = "FAILED" ]; then
  echo "FAIL: event is FAILED in DB but the transition is COMMITTED on-chain (N1 divergence)" >&2
  exit 1
fi
if [ "$st" != "COMMITTED" ]; then
  echo "FAIL: expected COMMITTED once the tx mined, got '${st:-<none>}'" >&2
  exit 1
fi
echo "  ✓ the mined transition reconciled to COMMITTED"
echo "PASS"
