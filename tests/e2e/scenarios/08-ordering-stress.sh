#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 08 ordering stress: 5 products, receiving+putaway rapid fire ==="

N=5
PROD_IDS=()
RECV_IDS=()
PUT_IDS=()
ITEM_IDS=()

for i in $(seq 1 $N); do
  pid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  rid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  ptid=$(uuidgen | tr '[:upper:]' '[:lower:]')
  iid=$(cast_cmd keccak "$pid" | tr -d '[:space:]')
  PROD_IDS+=("$pid")
  RECV_IDS+=("$rid")
  PUT_IDS+=("$ptid")
  ITEM_IDS+=("$iid")
done

# Fire all receiving + putaway in rapid succession with NO waits between them.
# Before the fix (4 independent topics), putaway could arrive at the contract
# before receiving → revert. With unified topic + FSM ordering, receiving
# sub-batch is always processed first.
for i in $(seq 0 $((N - 1))); do
  publish_event "receiving" "${RECV_IDS[$i]}" "${PROD_IDS[$i]}" '{}'
done
for i in $(seq 0 $((N - 1))); do
  publish_event "putaway" "${PUT_IDS[$i]}" "${PROD_IDS[$i]}" '{}'
done

echo "  published $N receiving + $N putaway events without waits"

# All receiving events must be COMMITTED
for rid in "${RECV_IDS[@]}"; do
  wait_for_status "$rid" "COMMITTED" 45 2
done

# All putaway events must be COMMITTED (not FAILED)
for ptid in "${PUT_IDS[@]}"; do
  wait_for_status "$ptid" "COMMITTED" 45 2
done

# On-chain all items should be PutAway (status=2)
for iid in "${ITEM_IDS[@]}"; do
  wait_for_item_status "$iid" "2" 10 1
done

# Verify zero FAILED events for these event IDs
FAILED_COUNT=$(psql_q "SELECT COUNT(*) FROM public.onchain_events WHERE status='FAILED' AND event_id IN ($(printf "'%s'," "${RECV_IDS[@]}" "${PUT_IDS[@]}" | sed 's/,$//'))")
if [ "$FAILED_COUNT" != "0" ]; then
  echo "FAIL: expected 0 FAILED events, got $FAILED_COUNT"
  exit 1
fi

echo "  ✓ all $((N * 2)) events COMMITTED, zero FAILED"
echo "PASS"
