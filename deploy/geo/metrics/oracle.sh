#!/usr/bin/env bash
# oracle.sh [http-port] — read a geo node's state. Run ON the box. No jq dependency.
# Reports: API readiness, full /ext/health, P-chain bootstrap, current primary-network
# validators (nodeID + weight), connected peers, recent genesis/consensus log lines,
# node RSS + proxy liveness. Used as the dino-alone oracle and during soak.
set -uo pipefail
PORT="${1:-9650}"
INFO="http://127.0.0.1:${PORT}/ext/info"
PCHAIN="http://127.0.0.1:${PORT}/ext/bc/P"
HEALTH="http://127.0.0.1:${PORT}/ext/health"
post() { curl -s -m 5 -X POST --data "$1" -H 'content-type:application/json' "$2"; }

echo "=== waiting for API (max 45s) ==="
for i in $(seq 1 45); do
  curl -s -m 2 "$HEALTH" >/dev/null 2>&1 && { echo "API up after ${i}s"; break; }
  sleep 1
done

echo "=== /ext/health (full) ==="
curl -s -m 5 "$HEALTH"; echo

echo "=== info.isBootstrapped P ==="
post '{"jsonrpc":"2.0","id":1,"method":"info.isBootstrapped","params":{"chain":"P"}}' "$INFO"; echo

echo "=== platform.getCurrentValidators (primary network) ==="
VRESP=$(post '{"jsonrpc":"2.0","id":1,"method":"platform.getCurrentValidators","params":{}}' "$PCHAIN")
echo "  count: $(echo "$VRESP" | grep -oE '"nodeID":"NodeID-[^"]*"' | grep -c .)"
echo "  nodeIDs:"; echo "$VRESP" | grep -oE '"nodeID":"NodeID-[^"]*"' | sort -u | sed 's/^/    /'
echo "  weights:"; echo "$VRESP" | grep -oE '"weight":"[0-9]*"' | sort | uniq -c | sed 's/^/    /'
echo "  connected:"; echo "$VRESP" | grep -oE '"connected":(true|false)' | sort | uniq -c | sed 's/^/    /'
echo "  raw[0:500]: ${VRESP:0:500}"

echo "=== info.peers (connected) ==="
post '{"jsonrpc":"2.0","id":1,"method":"info.peers","params":{}}' "$INFO" | grep -oE '"nodeID":"NodeID-[^"]*"' | sort -u | sed 's/^/    /' || true

echo "=== journalctl (genesis/bls/consensus/errors) ==="
journalctl -u avalanchego -n 80 --no-pager 2>/dev/null \
  | grep -iE 'genesis|bls|proof|possession|sample|quorum|error|panic|fatal|bootstrap|health|chain' | tail -28

echo "=== resources ==="
echo -n "  node RSS(MB): "; ps -C avalanchego -o rss= | awk '{s+=$1} END{print int(s/1024)}'
free -m | awk '/Mem:/{print "  mem avail(MB): "$7} /Swap:/{print "  swap used(MB): "$3}'
echo -n "  sing-box: "; pgrep -x sing-box >/dev/null && echo alive || echo DEAD
