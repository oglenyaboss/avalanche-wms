#!/usr/bin/env bash
# geo-bench-report.sh — sourced by run-geo-bench.sh after the run. Turns the run's CSVs
# ($OUT/db.csv, nodehome.csv, node-<name>.csv, fault.log) + a few final run-scoped queries into
# $OUT/geo-bench.json — the single artifact the geo report and the UI "Узлы" panel consume.
#
# Inherits from the caller: OUT, DBCSV, NHCSV, RUN_START, RUN_T0, RUN_T1, FAULTLOG, BLOCKCHAIN_ID,
#   QR, FAULT, FAULT_AT, FAULT_DOWN, PACE_RPS, PACE_DURATION, NODES_NAME/HOST/GEO, PSQL/RAW fns.
# NOTE: latency is COMMIT/PIPELINE latency (updated_at-created_at), not raw consensus finality.

JSON="$OUT/geo-bench.json"
DBCSV="${DBCSV:-$OUT/db.csv}"; NHCSV="${NHCSV:-$OUT/nodehome.csv}"

# --- fault window epochs (if any) --------------------------------------------------------------
DOWN_TS=$(awk '/DOWN/{print $1}' "$FAULTLOG" 2>/dev/null | head -1)
UP_TS=$(awk '/UP/{print $1}'   "$FAULTLOG" 2>/dev/null | head -1)
[ -n "${DOWN_TS:-}" ] && FAULT_FIRED=1 || FAULT_FIRED=0

# --- sustained commit-rate (slope of committed vs ts), windowed -------------------------------
# pre-fault steady = [RUN_T0+30 .. DOWN_TS] (or whole run if no fault). The honest headline number.
slope(){ awk -F, -v a="$1" -v b="$2" 'NR>1 && $2!="NA" && $1>=a && $1<=b { if(ft==""){ft=$1;fc=$2} lt=$1;lc=$2 } END{ if(lt>ft) printf "%.2f",(lc-fc)/(lt-ft); else print "0" }' "$DBCSV"; }
PRE_END="${DOWN_TS:-$RUN_T1}"
SUSTAINED=$(slope "$((RUN_T0+30))" "$PRE_END")
if [ "$FAULT_FIRED" = 1 ]; then
  DURING_FAULT=$(slope "$DOWN_TS" "$UP_TS")
  POST_FAULT=$(slope "$UP_TS" "$(date +%s)")
else
  DURING_FAULT="NA"; POST_FAULT="NA"
fi

# --- backlog: max + final + bounded? -----------------------------------------------------------
BACKLOG_MAX=$(awk -F, 'NR>1 && $5!="NA" && $5>m{m=$5} END{print m+0}' "$DBCSV")
BACKLOG_PREFAULT_MAX=$(awk -F, -v b="${DOWN_TS:-9999999999}" 'NR>1 && $5!="NA" && $1<b && $5>m{m=$5} END{print m+0}' "$DBCSV")
BACKLOG_FINAL=$(PSQL "SELECT count(*) FROM public.outbox_events oe WHERE oe.created_at>='$RUN_START' AND NOT EXISTS (SELECT 1 FROM public.onchain_events oc WHERE oc.event_id=oe.event_id)")
# bounded if it drained back to ~0 by the end (transient fault spike is allowed and expected)
[ "${BACKLOG_FINAL:-99}" -le 5 ] && BOUNDED=true || BOUNDED=false

# --- final run-scoped integrity + commit latency -----------------------------------------------
COMMITTED=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='COMMITTED' AND created_at>='$RUN_START'")
FAILED=$(PSQL "SELECT count(*) FROM public.onchain_events WHERE status='FAILED' AND created_at>='$RUN_START'")
read -r LP50 LP95 LP99 <<<"$(RAW "SELECT percentile_disc(0.5) WITHIN GROUP (ORDER BY e), percentile_disc(0.95) WITHIN GROUP (ORDER BY e), percentile_disc(0.99) WITHIN GROUP (ORDER BY e) FROM (SELECT EXTRACT(EPOCH FROM (updated_at-created_at)) e FROM public.onchain_events WHERE status='COMMITTED' AND created_at>='$RUN_START') q" | tr '|' ' ')"
SHIPPED_WMS=$(PSQL "SELECT count(*) FROM wms_inventory.products WHERE qr_code LIKE '$QR' AND status='SHIPPED'")
SHIPPED_CHAIN=$(PSQL "SELECT count(*) FROM public.onchain_events oe JOIN public.outbox_events ob USING(event_id) JOIN wms_inventory.products p ON p.product_id=ob.aggregate_id WHERE oe.aggregate_type='shipping' AND oe.status='COMMITTED' AND p.qr_code LIKE '$QR'")
[ "${SHIPPED_WMS:-0}" = "${SHIPPED_CHAIN:-1}" ] && CONSISTENT=true || CONSISTENT=false
RECONCILED=$(docker logs ledger-adapter --since "$(date -u -r "$RUN_T0" +%Y-%m-%dT%H:%M:%S 2>/dev/null || echo '10m')" 2>&1 | grep -oE 'reconciled to committed n:[0-9]+' | awk -F: '{s+=$2} END{print s+0}')

# --- k6 front facts ----------------------------------------------------------------------------
K6_ITERS=$(grep -E '^\s*iterations' "$OUT/k6.log" | grep -oE '[0-9]+' | head -1)
K6_DROPPED=$(grep -E 'dropped_iterations' "$OUT/k6.log" | grep -oE '[0-9]+' | head -1)
K6_FAILPCT=$(grep -E 'http_req_failed' "$OUT/k6.log" | grep -oE '[0-9]+\.[0-9]+%' | head -1)
K6_P95=$(grep -E 'iteration_duration' "$OUT/k6.log" | grep -oE 'p\(95\)=[0-9.]+m?s' | head -1 | sed 's/p(95)=//')

# --- per-node stats (validators + node-home) ---------------------------------------------------
# returns: end_height peers max_rss_mb cpu_pct  from a <ts,height,peers,rss,cpu> csv
nodestat(){ awk -F, 'NR>1 && $5!="NA"{ if(fc==""){ft=$1;fc=$5} lt=$1;lc=$5; if($4!="NA"&&$4+0>maxr)maxr=$4; if($2!="NA")lh=$2; if($3!="NA")lp=$3 } END{ cpu=(lt>ft)?(lc-fc)/(lt-ft)*100:0; printf "%s %s %.0f %.1f", (lh==""?"NA":lh), (lp==""?"NA":lp), maxr/1048576, cpu }' "$1"; }
NODES_JSON=""
declare -a END_HEIGHTS=()
for idx in "${!NODES_NAME[@]}"; do
  n="${NODES_NAME[$idx]}"; g="${NODES_GEO[$idx]}"; f="$OUT/node-$n.csv"
  if [ -s "$f" ] && [ "$(wc -l < "$f")" -gt 1 ]; then
    read -r EH PEERS RSS CPU <<<"$(nodestat "$f")"
  else
    EH=NA; PEERS=NA; RSS=NA; CPU=NA
  fi
  [ "$EH" != NA ] && END_HEIGHTS+=("$EH")
  NODES_JSON="$NODES_JSON{\"name\":\"$n\",\"geo\":\"$g\",\"end_height\":${EH:-null},\"peers\":${PEERS:-null},\"rss_mb_max\":${RSS:-null},\"cpu_pct_avg\":${CPU:-null}},"
done
read -r NH_EH NH_PEERS NH_RSS NH_CPU <<<"$(nodestat "$NHCSV")"
NODES_JSON="$NODES_JSON{\"name\":\"node-home\",\"geo\":\"local\",\"end_height\":${NH_EH:-null},\"peers\":${NH_PEERS:-null},\"rss_mb_max\":${NH_RSS:-null},\"cpu_pct_avg\":${NH_CPU:-null}}"
# height-sync spread across validators
SPREAD=$(printf '%s\n' "${END_HEIGHTS[@]:-0}" | awk 'NR==1{mn=mx=$1} {if($1>mx)mx=$1; if($1<mn)mn=$1} END{print mx-mn}')

# --- fault liveness facts (TWO LEVELS) ---------------------------------------------------------
# Consensus liveness = how far the SURVIVING VALIDATORS advanced while the killed node was down.
# It must come from a validator CSV, NOT node-home: the observer gates block acceptance below the
# 73.33% connected-stake health threshold, so node-home FREEZES during a kill-1-of-3 and would
# wrongly report ~0 advancement. The contrast (validators advanced N, observer advanced ~0) is the
# pipeline-vs-consensus finding itself.
hgt_at(){ awk -F, -v a="$2" 'NR>1 && $2!="NA" && $1<=a{h=$2} END{print h+0}' "$1"; }
KILLED_NODE="${KILLED_NODE:-alex}"
if [ "$FAULT_FIRED" = 1 ]; then
  VAL_BLOCKS_DOWN=0
  for vn in "${NODES_NAME[@]}"; do
    [ "$vn" = "$KILLED_NODE" ] && continue          # the killed node's CSV is NA during down
    f="$OUT/node-$vn.csv"; [ -s "$f" ] || continue
    d=$(( $(hgt_at "$f" "$UP_TS") - $(hgt_at "$f" "$DOWN_TS") ))
    [ "$d" -gt "$VAL_BLOCKS_DOWN" ] && VAL_BLOCKS_DOWN=$d
  done
  OBS_BLOCKS_DOWN=$(( $(hgt_at "$NHCSV" "$UP_TS") - $(hgt_at "$NHCSV" "$DOWN_TS") ))  # ~0 = the observer stall
  cAtDown=$(awk -F, -v a="$DOWN_TS" 'NR>1 && $1<=a{c=$2} END{print c+0}' "$DBCSV")
  cAtUp=$(awk -F, -v b="$UP_TS"   'NR>1 && $1<=b{c=$2} END{print c+0}' "$DBCSV")
  COMMITTED_DURING_DOWN=$(( ${cAtUp:-0} - ${cAtDown:-0} ))
  RECOVERED_S=$(awk -F, -v up="$UP_TS" -v base="$BACKLOG_PREFAULT_MAX" 'NR>1 && $1>up && $5!="NA" && $5+0<=base+2 {print $1-up; exit}' "$DBCSV")
  FAULT_JSON="{\"enabled\":true,\"killed_node\":\"$KILLED_NODE\",\"down_from\":$DOWN_TS,\"up_at\":$UP_TS,\"down_s\":$((UP_TS-DOWN_TS)),\"validator_blocks_during_down\":$VAL_BLOCKS_DOWN,\"observer_blocks_during_down\":$OBS_BLOCKS_DOWN,\"committed_during_down\":$COMMITTED_DURING_DOWN,\"lost_events\":${FAILED:-0},\"recovered_s\":${RECOVERED_S:-null}}"
else
  FAULT_JSON="{\"enabled\":false}"
fi

# --- emit JSON ---------------------------------------------------------------------------------
norm(){ case "$1" in ''|NA) echo null;; *) echo "$1";; esac; }
flag_for(){ case "$1" in MD) printf '🇲🇩';; RO) printf '🇷🇴';; NL) printf '🇳🇱';; *) printf '🌐';; esac; }
VAL_META=""; SERIES_NODES=""
for vi in "${!NODES_NAME[@]}"; do
  g="${NODES_GEO[$vi]}"
  VAL_META="$VAL_META{\"name\":\"${NODES_NAME[$vi]}\",\"geo\":\"$g $(flag_for "$g")\"},"
  SERIES_NODES="$SERIES_NODES\"node-${NODES_NAME[$vi]}.csv\","
done
VAL_META="[${VAL_META%,}]"; SERIES_NODES="[${SERIES_NODES%,}]"
cat > "$JSON" <<EOF
{
  "meta": {
    "ts": "$TS",
    "blockchain_id": "$BLOCKCHAIN_ID",
    "chain_id_hex": "0x1869f",
    "network_id": 1338,
    "offered_rate_rps": $PACE_RPS,
    "duration": "$PACE_DURATION",
    "validators": $VAL_META
  },
  "throughput": {
    "sustained_commit_evps": $(norm "$SUSTAINED"),
    "during_fault_evps": $(norm "$DURING_FAULT"),
    "post_fault_evps": $(norm "$POST_FAULT"),
    "committed_total": $(norm "$COMMITTED"),
    "k6_iterations": $(norm "$K6_ITERS"),
    "k6_dropped_iterations": $(norm "$K6_DROPPED"),
    "k6_http_fail_pct": "${K6_FAILPCT:-0.00%}",
    "k6_iter_p95": "${K6_P95:-NA}"
  },
  "commit_latency_s": { "p50": $(norm "$LP50"), "p95": $(norm "$LP95"), "p99": $(norm "$LP99") },
  "backlog": { "max": $(norm "$BACKLOG_MAX"), "prefault_max": $(norm "$BACKLOG_PREFAULT_MAX"), "final": $(norm "$BACKLOG_FINAL"), "bounded": $BOUNDED },
  "integrity": {
    "committed": $(norm "$COMMITTED"), "failed": $(norm "$FAILED"), "lost_events": $(norm "$FAILED"), "reconciled": $(norm "$RECONCILED"),
    "shipped_wms": $(norm "$SHIPPED_WMS"), "shipped_chain": $(norm "$SHIPPED_CHAIN"), "shipping_consistent": $CONSISTENT
  },
  "nodes": [ $NODES_JSON ],
  "sync": { "validator_height_spread": $(norm "$SPREAD") },
  "fault_window": $FAULT_JSON,
  "series": { "db": "db.csv", "nodehome": "nodehome.csv", "nodes": $SERIES_NODES }
}
EOF

echo "  sustained=${SUSTAINED} ev/s | commit-lat p50/p95/p99=${LP50}/${LP95}/${LP99}s | backlog max=${BACKLOG_MAX} final=${BACKLOG_FINAL} bounded=$BOUNDED"
echo "  integrity: committed=$COMMITTED failed=$FAILED reconciled=$RECONCILED  shipped wms=$SHIPPED_WMS==chain=$SHIPPED_CHAIN ($CONSISTENT)"
echo "  sync spread=${SPREAD} blocks  fault: ${FAULT_JSON}"
echo "  JSON → $JSON"
