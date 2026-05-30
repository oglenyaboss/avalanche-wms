#!/usr/bin/env bash
set -euo pipefail
# 10 — S3 batch poisoning (HIGH).
#
# The OLD contract batch loop (BatchMappingWMS.sol _batchTransition) had no per-item
# isolation: one bad item reverted the whole tx, and the adapter then marked EVERY id in
# the sub-batch FAILED (recordFailure), dragging valid siblings to FAILED even though they
# were fine.
#
# FIX (#47, Design A per the issue): _batchTransition skips a bad item (wrong itemStatus)
# with an `ItemTransitionFailed` event instead of reverting, so the batch tx SUCCEEDS, the
# adapter MarkCommitts the whole sub-batch, and valid siblings reach COMMITTED. The skip is
# observable on-chain via ItemTransitionFailed (and, once #45 ships, via the WMS chain-status
# gate). The poison row itself ends COMMITTED at the DB level (the tx succeeded) — its
# per-item rejection lives in the event log, NOT in onchain_events.status. That residual
# gap (poison silently COMMITTED until #45) is reverse-outbox/#41 territory, deferred.
#
# Self-contained: widens BATCH_TIMEOUT so the rapid-fire publishes co-batch into one tx,
# then restores the default on exit. Exits non-zero if S3 regresses (siblings dragged down
# or the batch reverted), 0 on the fixed behavior.

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 10 S3: one poisoned event must not FAIL its valid siblings ==="

# Widen the batch window so sequential kcat publishes land in one tx; restore on exit.
restore_adapter() {
  unset BATCH_TIMEOUT
  recreate_adapter && wait_adapter 60 || true
}
trap restore_adapter EXIT
export BATCH_TIMEOUT="5s"
echo "--- recreating ledger-adapter with BATCH_TIMEOUT=$BATCH_TIMEOUT ---"
recreate_adapter
wait_adapter 60

N=4
EVT=(); PROD=(); ITEM=()
for _ in $(seq 1 "$N"); do
  e=$(uuidgen | tr '[:upper:]' '[:lower:]')
  p=$(uuidgen | tr '[:upper:]' '[:lower:]')
  i=$(cast_cmd keccak "$p" | tr -d '[:space:]')
  EVT+=("$e"); PROD+=("$p"); ITEM+=("$i")
done

# Poison item PX: pre-Accept it via its OWN committed receiving event, so a SECOND
# receiving event for it (None->Accepted required, but it is already Accepted) is the
# poison — itemStatus mismatch. No cast send / signer key needed.
PX=$(uuidgen | tr '[:upper:]' '[:lower:]')
PXIID=$(cast_cmd keccak "$PX" | tr -d '[:space:]')
PXE1=$(uuidgen | tr '[:upper:]' '[:lower:]')
publish_event "receiving" "$PXE1" "$PX" '{}'
wait_for_status "$PXE1" "COMMITTED" 30 2
wait_for_item_status "$PXIID" "1" 10 1

# Rapid-fire one batch: N fresh-valid receivings + 1 poisoned receiving (re-accept PX).
PXE2=$(uuidgen | tr '[:upper:]' '[:lower:]')
for idx in $(seq 0 $((N-1))); do
  publish_event "receiving" "${EVT[$idx]}" "${PROD[$idx]}" '{}'
done
publish_event "receiving" "$PXE2" "$PX" '{}'

# 1. The valid siblings must each reach COMMITTED (the core S3 fix — they were dragged
#    to FAILED before, by the whole-batch revert).
for idx in $(seq 0 $((N-1))); do
  if ! wait_for_status "${EVT[$idx]}" "COMMITTED" 30 2; then
    echo "FAIL: valid sibling ${EVT[$idx]} did not reach COMMITTED (poison fanned out — S3)" >&2
    exit 1
  fi
  ic=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "${ITEM[$idx]}" --rpc-url "$RPC_URL" | tr -d '[:space:]')
  [ "$ic" = "1" ] || { echo "FAIL: valid sibling on-chain status $ic, expected 1" >&2; exit 1; }
done

# 2. The poisoned event's row ends COMMITTED — the batch tx succeeded (Design A). Its
#    per-item rejection is surfaced via ItemTransitionFailed, asserted below.
PXST=$(wait_for_status "$PXE2" "COMMITTED" 15 2 >/dev/null 2>&1 && echo COMMITTED || psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$PXE2'")
[ "$PXST" = "COMMITTED" ] || { echo "FAIL: poisoned event expected COMMITTED (batch tx succeeded), got '${PXST:-<none>}'" >&2; exit 1; }

# 3. Isolation proven on-chain: the poison item must NOT have advanced — it was Accepted(1)
#    and a second accept is rejected, so it stays 1 (no double transition).
PXIC=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "$PXIID" --rpc-url "$RPC_URL" | tr -d '[:space:]')
[ "$PXIC" = "1" ] || { echo "FAIL: poison item on-chain status $PXIC, expected 1 (unchanged — no double transition)" >&2; exit 1; }

# 4. Guard: the N valids + poison must have shared ONE tx, else the test proved nothing.
IDLIST=$(printf "'%s'," "${EVT[@]}" "$PXE2" | sed 's/,$//')
NTX=$(psql_q "SELECT COUNT(DISTINCT tx_hash) FROM public.onchain_events WHERE event_id IN ($IDLIST) AND tx_hash IS NOT NULL")
if [ "$NTX" != "1" ]; then
  echo "INCONCLUSIVE: events did not share one batch tx (distinct tx_hash=$NTX). Raise BATCH_TIMEOUT — see header." >&2
  exit 2
fi

# 5. The skip is observable: the shared batch tx emitted an ItemTransitionFailed event.
BATCHTX=$(psql_q "SELECT tx_hash FROM public.onchain_events WHERE event_id='$PXE2'")
FAILSIG=$(cast_cmd keccak "ItemTransitionFailed(uint256,uint256,uint8,uint8)" | tr -d '[:space:]')
if ! cast_cmd receipt "$BATCHTX" --rpc-url "$RPC_URL" --json | grep -qi "$FAILSIG"; then
  echo "FAIL: batch tx $BATCHTX did not emit ItemTransitionFailed for the poison item" >&2
  exit 1
fi

echo "  ✓ $N siblings COMMITTED, poison isolated (itemStatus unchanged + ItemTransitionFailed), single batch tx"
echo "PASS"
