#!/bin/sh
# node-collect.sh — on-box avalanchego metrics sampler for the geo bench.
#
# Runs ON a validator (POSIX sh / BusyBox-safe). SELF-BOUNDED: exits after NSAMP samples,
# so it dies on its own even if the launching SSH session drops mid-run (the evolvica proxy
# route is flaky). This is why we push ONE collector per box and fetch the file at the end,
# instead of per-poll `ssh curl` over the flaky link.
#
# Samples the node's loopback /ext/metrics every INTERVAL seconds into a CSV:
#   ts,height,peers,rss_bytes,cpu_sec
# height = subnet consensus height (avalanche_snowman_last_accepted_height{chain=<BID>}),
# the per-node series that proves the 3 validators stay in sync under load.
#
# Launch via ssh:
#   ssh vpn-dino 'nohup sh /tmp/node-collect.sh <BID> <INTERVAL_S> <NSAMP> /tmp/geo-nodestat.csv >/dev/null 2>&1 & echo $!'
set -u
BID="${1:?blockchain id required}"
INTERVAL="${2:-5}"
NSAMP="${3:-150}"
OUT="${4:-/tmp/geo-nodestat.csv}"
HEIGHT_KEY="avalanche_snowman_last_accepted_height{chain=\"$BID\"}"

echo "ts,height,peers,rss_bytes,cpu_sec" > "$OUT"
i=0
while [ "$i" -lt "$NSAMP" ]; do
  t=$(date +%s)
  m=$(timeout 5 curl -s localhost:9650/ext/metrics 2>/dev/null)
  h=$(printf '%s\n' "$m" | grep -F "$HEIGHT_KEY" | awk '{print $2}')
  p=$(printf '%s\n' "$m" | grep '^avalanche_network_peers ' | awk '{print $2}')
  r=$(printf '%s\n' "$m" | grep '^avalanche_process_process_resident_memory_bytes' | awk '{print $2}')
  c=$(printf '%s\n' "$m" | grep '^avalanche_process_process_cpu_seconds_total' | awk '{print $2}')
  echo "${t},${h:-NA},${p:-NA},${r:-NA},${c:-NA}" >> "$OUT"
  i=$((i + 1))
  sleep "$INTERVAL"
done
