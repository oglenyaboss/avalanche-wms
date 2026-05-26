#!/usr/bin/env bash
set -euo pipefail
# 10 — S3 batch poisoning (HIGH).
#
# The contract batch loop (BatchMappingWMS.sol _batchTransition) has no per-item
# isolation: one bad item reverts the whole tx. The adapter then marks EVERY id in the
# sub-batch FAILED (recordFailure), dragging valid siblings to FAILED even though they
# were fine. The contract side is already proven by the Foundry test
# test_revert_batchWithOneInvalidItem; THIS script proves the adapter-side fan-out of
# the failure to valid siblings.
#
# ─── PREREQUISITE (the default stack will NOT co-batch) ───────────────────────────────
# The adapter flushes on a 100ms BATCH_TIMEOUT, but sequential `docker run kcat`
# publishes are slower than that, so each would land in its own tx and the test would be
# inconclusive. Recreate the adapter with a wider window first. BATCH_TIMEOUT is a
# hardcoded literal in docker-compose.yaml, so override it, e.g.:
#
#   # temporarily set, under services.ledger-adapter.environment:
#   #   BATCH_TIMEOUT: "5s"
#   docker compose -p blockchain_project_e2e --profile test up -d --no-deps --force-recreate ledger-adapter
#
# Restore the 100ms value afterwards. The COUNT(DISTINCT tx_hash) guard below exits 2
# (inconclusive) if the events did not actually share one tx.
#
# UNVALIDATED: written from code analysis; confirm on a live stack before trusting.
# Exits non-zero while S3 exists, 0 once the adapter isolates per-item failures.

HERE="$(cd "$(dirname "$0")/.." && pwd)"
source "$HERE/lib/env.sh"
source "$HERE/lib/wait_for.sh"
source "$HERE/lib/kafka.sh"

echo "=== 10 S3: one poisoned event must not FAIL its valid siblings ==="

N=4
EVT=(); PROD=(); ITEM=()
for _ in $(seq 1 "$N"); do
  e=$(uuidgen | tr '[:upper:]' '[:lower:]')
  p=$(uuidgen | tr '[:upper:]' '[:lower:]')
  i=$(cast_cmd keccak "$p" | tr -d '[:space:]')
  EVT+=("$e"); PROD+=("$p"); ITEM+=("$i")
done

# Poison item PX: pre-Accept it via its OWN committed receiving event, so a SECOND
# receiving event for it (None->Accepted required, but it is already Accepted) reverts
# "Invalid status transition" — no cast send / signer key needed.
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

# The valid siblings must each reach COMMITTED (fails today: they go FAILED with PX).
for idx in $(seq 0 $((N-1))); do
  if ! wait_for_status "${EVT[$idx]}" "COMMITTED" 30 2; then
    echo "FAIL: valid sibling ${EVT[$idx]} did not reach COMMITTED (poison fanned out — S3)" >&2
    exit 1
  fi
  ic=$(cast_cmd call "$CONTRACT_ADDR" "itemStatus(uint256)(uint8)" "${ITEM[$idx]}" --rpc-url "$RPC_URL" | tr -d '[:space:]')
  [ "$ic" = "1" ] || { echo "FAIL: valid sibling on-chain status $ic, expected 1" >&2; exit 1; }
done

# The poisoned event must be the only FAILED one.
PXST=$(psql_q "SELECT status::text FROM public.onchain_events WHERE event_id='$PXE2'")
[ "$PXST" = "FAILED" ] || { echo "FAIL: poisoned event expected FAILED, got '${PXST:-<none>}'" >&2; exit 1; }

# Guard: the N valids + poison must have shared ONE tx, else the test proved nothing.
IDLIST=$(printf "'%s'," "${EVT[@]}" "$PXE2" | sed 's/,$//')
NTX=$(psql_q "SELECT COUNT(DISTINCT tx_hash) FROM public.onchain_events WHERE event_id IN ($IDLIST) AND tx_hash IS NOT NULL")
if [ "$NTX" != "1" ]; then
  echo "INCONCLUSIVE: events did not share one batch tx (distinct tx_hash=$NTX). Raise BATCH_TIMEOUT — see header." >&2
  exit 2
fi

echo "  ✓ $N siblings COMMITTED, poison isolated to FAILED, single batch tx"
echo "PASS"
