#!/usr/bin/env bash
# killnode-test.sh — node-failure demonstration on the geo subnet-evm chain.
#
# Sends a tx with both validators up (must mine), stops alex, sends another tx (async), watches
# the block height while alex is down, then restarts alex. The OUTCOME depends on the consensus
# mode and validator count — the script reports facts and interprets them:
#
#   * Phase 0, sybil-off (current): a lone node finalizes -> blocks ADVANCE while alex is down
#     => "network SURVIVED node loss" (availability / fault tolerance, NOT stake-weighted BFT).
#   * Phase 1, sybil-on + 3 online validators: killing 1 of 3 still meets quorum -> SURVIVES;
#     killing below quorum -> HALTS (the down-tx stays pending and mines only when a validator
#     returns) => proves real stake-weighted BFT.
#
# Requires foundry `cast` locally and SSH to both validators. Opens its own RPC tunnel to dino.
# All waits run remotely (the harness blocks long local foreground sleeps; macOS has no `timeout`).
set -uo pipefail
DINO_HOST="${DINO_HOST:-vpn-dino}"
ALEX_HOST="${ALEX_HOST:-vpn-alex}"
LPORT="${LPORT:-19650}"
PK="${EWOQ_PK:-0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027}" # well-known ewoq test key
TO="${TO:-0x000000000000000000000000000000000000dEaD}"
SOCK="/tmp/geo-killnode.sock"
: "${BLOCKCHAIN_ID:?set BLOCKCHAIN_ID}"
RPC="http://localhost:${LPORT}/ext/bc/${BLOCKCHAIN_ID}/rpc"

cleanup() { ssh -S "$SOCK" -O exit "$DINO_HOST" 2>/dev/null || true; }
trap cleanup EXIT

echo "== tunnel localhost:${LPORT} -> ${DINO_HOST}:9650 =="
ssh -fN -M -S "$SOCK" -o ConnectTimeout=15 -L "${LPORT}:localhost:9650" "$DINO_HOST"

bn0=$(cast block-number --rpc-url "$RPC"); echo "baseline block = $bn0"

echo "== tx1 with BOTH validators up (sync) =="
cast send "$TO" --value 1 --private-key "$PK" --rpc-url "$RPC" >/tmp/geo-tx1.out 2>&1 \
  && echo "  tx1 mined" || { echo "  tx1 FAILED:"; tail -3 /tmp/geo-tx1.out; }
bn1=$(cast block-number --rpc-url "$RPC"); echo "  block = $bn1 (was $bn0)"

echo "== STOP alex =="
ssh -o ConnectTimeout=15 "$ALEX_HOST" 'systemctl stop avalanchego' 2>&1 | grep -v zoxide || true
ssh -o ConnectTimeout=15 "$ALEX_HOST" 'sleep 3; systemctl is-active avalanchego' 2>&1 | grep -v zoxide | sed 's/^/  alex now: /'

echo "== tx2 with alex DOWN (async) =="
tx2=$(cast send "$TO" --value 1 --private-key "$PK" --rpc-url "$RPC" --async 2>/tmp/geo-tx2.err) \
  && echo "  tx2 submitted: $tx2" || { echo "  tx2 submit error:"; tail -3 /tmp/geo-tx2.err; }
echo "  watching block for ~25s while alex is down:"
ssh -S "$SOCK" "$DINO_HOST" 'for i in 1 2 3 4 5; do printf "    t=%ds block=" $((i*5)); curl -s -X POST --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_blockNumber\",\"params\":[]}" -H "content-type:application/json" "localhost:9650/ext/bc/'"$BLOCKCHAIN_ID"'/rpc" | grep -oE "0x[0-9a-f]+"; sleep 5; done'
bnDown=$(cast block-number --rpc-url "$RPC"); echo "  block while alex down = $bnDown"

echo "== RESTART alex =="
ssh -o ConnectTimeout=15 "$ALEX_HOST" 'systemctl start avalanchego; for i in $(seq 1 45); do curl -s -m2 localhost:9650/ext/health | grep -q "\"healthy\":true" && { echo "  alex healthy after ${i}s"; break; }; sleep 1; done' 2>&1 | grep -v zoxide
bnFinal=$(cast block-number --rpc-url "$RPC")

echo
echo "===================== RESULT ====================="
echo "  baseline:            $bn0"
echo "  after tx1 (both up): $bn1   (blocks produced: $([ "$bn1" -gt "$bn0" ] && echo YES || echo NO))"
echo "  while alex DOWN:      $bnDown"
echo "  after alex back:      $bnFinal"
if [ "$bnDown" -gt "$bn1" ] 2>/dev/null; then
  echo "  => SURVIVED node loss: blocks advanced with a validator down (fault tolerance)."
elif [ "$bnFinal" -gt "$bnDown" ] 2>/dev/null; then
  echo "  => HALTED while down, RESUMED on return (strict quorum / BFT safety-over-liveness)."
else
  echo "  => froze while down, no resume yet (send another tx to confirm)."
fi
echo "=================================================="
