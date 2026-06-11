#!/bin/sh
# consensus-collect.sh — on-box avalanchego CONSENSUS-timing sampler for the geo bench.
#
# Runs ON a validator (POSIX sh / BusyBox-safe), self-bounded (exits after NSAMP), same
# survive-SSH-drop contract as node-collect.sh. Captures the snowman/meterchainvm counters
# that DECOMPOSE block cadence so we can answer "why ~Ns per block" with measurement, not guess:
#   polls/block  = Δpoll_duration_count / Δblks_accepted   (effective snow beta + retries)
#   ms/poll      = Δpoll_duration_sum   / Δpoll_duration_count
#   build ms/blk = Δbuild_block_sum     / Δbuild_block_count
#   fail frac    = Δpolls_failed        / (Δpolls_failed+Δpolls_successful)
#   fork rejects = Δblks_polls_rejected_count
# All counters are cumulative; the analyzer diffs first vs last sample IN the run window, so a
# contaminated absolute baseline does not matter — the delta is run-scoped and clean.
#
# Launch:
#   ssh vpn-dino 'nohup sh /tmp/consensus-collect.sh <BID> <INTERVAL_S> <NSAMP> /tmp/geo-consensus.csv >/dev/null 2>&1 & echo $!'
set -u
BID="${1:?blockchain id required}"
INTERVAL="${2:-2}"
NSAMP="${3:-200}"
OUT="${4:-/tmp/geo-consensus.csv}"

echo "ts,height,blks_built,blks_accepted,blks_processing,blks_rejected,polls_succ,polls_fail,poll_dur_sum_ns,poll_dur_count,build_sum_ns,build_count,cpu_sec,rss" > "$OUT"
i=0
while [ "$i" -lt "$NSAMP" ]; do
  t=$(date +%s)
  m=$(timeout 5 curl -s localhost:9650/ext/metrics 2>/dev/null)
  gv() { printf '%s\n' "$m" | grep -F "$1{chain=\"$BID\"}" | awk '{print $2; exit}'; }
  gp() { printf '%s\n' "$m" | grep -F "$1" | awk '{print $2; exit}'; }
  echo "${t},\
$(gv avalanche_snowman_last_accepted_height),\
$(gv avalanche_snowman_blks_built),\
$(gv avalanche_snowman_blks_accepted_count),\
$(gv avalanche_snowman_blks_processing),\
$(gv avalanche_snowman_blks_polls_rejected_count),\
$(gv avalanche_snowman_polls_successful),\
$(gv avalanche_snowman_polls_failed),\
$(gv avalanche_snowman_poll_duration_sum),\
$(gv avalanche_snowman_poll_duration_count),\
$(gv avalanche_meterchainvm_build_block_sum),\
$(gv avalanche_meterchainvm_build_block_count),\
$(gp 'avalanche_process_process_cpu_seconds_total'),\
$(gp 'avalanche_process_process_resident_memory_bytes')" | tr -d ' ' >> "$OUT"
  i=$((i + 1))
  sleep "$INTERVAL"
done
