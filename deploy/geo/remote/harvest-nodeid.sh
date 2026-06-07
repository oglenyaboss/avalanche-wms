#!/usr/bin/env bash
# harvest-nodeid.sh [data-dir] [http-port] — boot avalanchego briefly to generate (if absent)
# and read this node's identity. Prints the info.getNodeID JSON: nodeID + BLS nodePOP
# {publicKey, proofOfPossession}. Staking creds persist in <data-dir>/staking for reuse by
# the real node. Used to bake validators into a custom genesis as initialStakers.
set -uo pipefail
DATA="${1:-/var/lib/avalanchego}"
PORT="${2:-9650}"
export GOMEMLIMIT="${GOMEMLIMIT:-500MiB}"
mkdir -p "$DATA"
/opt/avalanchego/avalanchego \
  --network-id=local \
  --staking-port=0 \
  --http-host=127.0.0.1 --http-port="$PORT" \
  --data-dir="$DATA" \
  --sybil-protection-enabled=false \
  >/tmp/avago-harvest.log 2>&1 &
PID=$!
RESP=""
for i in $(seq 1 45); do
  RESP=$(curl -s -m 2 -X POST -H 'content-type:application/json' \
      --data '{"jsonrpc":"2.0","id":1,"method":"info.getNodeID"}' \
      "http://127.0.0.1:${PORT}/ext/info" 2>/dev/null || true)
  case "$RESP" in *'"nodeID"'*) break;; esac
  sleep 1
done
kill "$PID" 2>/dev/null || true
wait "$PID" 2>/dev/null || true
case "$RESP" in
  *'"nodeID"'*) echo "$RESP";;
  *) echo "HARVEST FAILED in $DATA" >&2; tail -30 /tmp/avago-harvest.log >&2; exit 1;;
esac
