#!/usr/bin/env python3
# load-sampler.py — on-box validator sampler for saturation load tests.
#
# Runs ON a validator. Every INTERVAL s for NSAMP samples, captures SYNCHRONIZED:
#   - whole-box CPU busy% (delta of /proc/stat) and per-core count + loadavg  → "is the 1 core maxed?"
#   - avalanchego process CPU-seconds + RSS (from :9650/ext/metrics, # lines skipped — fixes the
#     consensus-collect.sh bug where grep matched the '# HELP' line and emitted "HELP")
#   - consensus timing counters (build_block sum/count, blks_accepted, polls succ/fail, fork rejects)
#   - chain height (eth_blockNumber)
# Cumulative counters are diffed by the analyzer over the run window, so a dirty baseline is fine.
#
# Launch:  nohup python3 /tmp/load-sampler.py <BID> <INTERVAL_S> <NSAMP> /tmp/geo-load.csv >/dev/null 2>&1 & echo $!
import sys, time, json, urllib.request

BID = sys.argv[1]
INTERVAL = float(sys.argv[2]) if len(sys.argv) > 2 else 1.0
NSAMP = int(sys.argv[3]) if len(sys.argv) > 3 else 120
OUT = sys.argv[4] if len(sys.argv) > 4 else "/tmp/geo-load.csv"
RPC = f"http://127.0.0.1:9650/ext/bc/{BID}/rpc"
METRICS = "http://127.0.0.1:9650/ext/metrics"


def ncpu():
    try:
        with open("/proc/stat") as f:
            return sum(1 for ln in f if ln.startswith("cpu") and ln[3].isdigit())
    except Exception:
        return 1


def cpu_jiffies():
    # returns (busy, total) from /proc/stat aggregate line
    with open("/proc/stat") as f:
        parts = f.readline().split()[1:]
    v = [int(x) for x in parts]
    idle = v[3] + (v[4] if len(v) > 4 else 0)  # idle + iowait
    total = sum(v)
    return total - idle, total


def loadavg():
    try:
        with open("/proc/loadavg") as f:
            return f.read().split()[0]
    except Exception:
        return ""


def metrics():
    out = {}
    try:
        raw = urllib.request.urlopen(METRICS, timeout=5).read().decode()
    except Exception:
        return out
    chain = f'chain="{BID}"'
    for ln in raw.splitlines():
        if ln.startswith("#") or not ln:
            continue
        # process-level (no chain label)
        if ln.startswith("avalanche_process_process_cpu_seconds_total "):
            out["proc_cpu"] = ln.split()[1]
        elif ln.startswith("avalanche_process_process_resident_memory_bytes "):
            out["rss"] = ln.split()[1]
        elif chain in ln:
            for key, name in (
                ("build_sum", "avalanche_meterchainvm_build_block_sum"),
                ("build_cnt", "avalanche_meterchainvm_build_block_count"),
                ("accepted", "avalanche_snowman_blks_accepted_count"),
                ("polls_succ", "avalanche_snowman_polls_successful"),
                ("polls_fail", "avalanche_snowman_polls_failed"),
                ("poll_dur_sum", "avalanche_snowman_poll_duration_sum"),
                ("poll_dur_cnt", "avalanche_snowman_poll_duration_count"),
                ("forks_rej", "avalanche_snowman_blks_polls_rejected_count"),
                ("height", "avalanche_snowman_last_accepted_height"),
            ):
                if ln.startswith(name + "{"):
                    out[key] = ln.split()[1]
    return out


def height():
    body = json.dumps({"jsonrpc": "2.0", "method": "eth_blockNumber", "params": [], "id": 1}).encode()
    req = urllib.request.Request(RPC, data=body, headers={"content-type": "application/json"})
    try:
        r = json.load(urllib.request.urlopen(req, timeout=5))
        return int(r["result"], 16)
    except Exception:
        return ""


NC = ncpu()
cols = ["ts", "height", "cpu_busy_pct", "load1", "ncpu", "proc_cpu_sec", "rss",
        "build_sum_ns", "build_count", "blks_accepted", "polls_succ", "polls_fail",
        "poll_dur_sum_ns", "poll_dur_cnt", "forks_rej"]
with open(OUT, "w") as f:
    f.write(",".join(cols) + "\n")
pb, pt = cpu_jiffies()
time.sleep(INTERVAL)
for _ in range(NSAMP):
    b, t = cpu_jiffies()
    pct = round(100.0 * (b - pb) / (t - pt), 1) if t > pt else ""
    pb, pt = b, t
    m = metrics()
    row = [int(time.time()), height(), pct, loadavg(), NC,
           m.get("proc_cpu", ""), m.get("rss", ""), m.get("build_sum", ""), m.get("build_cnt", ""),
           m.get("accepted", ""), m.get("polls_succ", ""), m.get("polls_fail", ""),
           m.get("poll_dur_sum", ""), m.get("poll_dur_cnt", ""), m.get("forks_rej", "")]
    with open(OUT, "a") as f:
        f.write(",".join(str(x) for x in row) + "\n")
    time.sleep(INTERVAL)
