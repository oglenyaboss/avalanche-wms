# N9 e2e flake — root-cause validation + fix ranking

Scope: READ-ONLY analysis of why `TestAdapterN9_IntraBatchDuplicate_pendingFix`
intermittently fails with `known_failures_test.go:476: expected COMMITTED, got ""`
while S2/S3/N1 pass. `got ""` = no row in `public.onchain_events` for the event_id
(onchain_helpers_test.go:24-33 `onchainStatus` returns "" when QueryRow errors / no row).

## VERDICT: CONFIRMED — empirically (timing flake), mechanism rebalance-starvation after stacked hard-kill recreates

The hypothesis is correct, and the proof is EMPIRICAL FIRST: the SAME binary PASSED N9 in
42s on a prior run and FAILED in 62s here, straddling the 60s assertion deadline — that
variable wait crossing the cap is the actual proof of a timing flake, not the arithmetic
below. The mechanism: the adapter's Kafka consumer is a kafka-go consumer-group member
with a STATIC group ID; `recreateAdapterWithEnv` SIGKILLs the old container and the new one
only waits for a static HTTP 200 /health that carries no consumer-readiness signal. Two
back-to-back recreates immediately before N9's body (N1's `t.Cleanup` restore + N9's own
`BATCH_TIMEOUT=10s` recreate) leave one or two ghost members inside the broker's 30s
session-timeout window, so the freshly-started consumer must wait out a broker-side
rebalance before it is assigned the partition and starts reading. N9 publishes into an
HTTP-healthy-but-not-consuming adapter; under load that blind window is PLAUSIBLY >60s (see
the timing caveat below — this is not arithmetically proven, the empirical straddle is the
real evidence). A real dedup regression would produce status `FAILED` (a row), not an
absent row, so the absent row is positive evidence of "never consumed", i.e. timing not
logic. Stale-image is excluded by construction: the same image passed S2/S3/N1 in this very
run (and see memory note e2e_image_caching.md for the usual rebuild-before-e2e trap), so a
bad/stale binary cannot be the N9-only cause.

### Evidence

Static group ID:
- docker-compose.yaml:272 `KAFKA_GROUP_ID: ledger-adapter`
- config.go:41 `KafkaGroupID: getDefault("KAFKA_GROUP_ID", "ledger-adapter")`
- consumer.go:45 `GroupID: groupID` — same value every recreate.

Consumer sets NO group-timing knobs (so all kafka-go defaults apply):
- consumer.go:42-49 `kafka.NewReader(kafka.ReaderConfig{Brokers, Topic, GroupID,
  MinBytes:1, MaxBytes:10e6, MaxWait:100ms})` — no SessionTimeout / RebalanceTimeout /
  JoinGroupBackoff / HeartbeatInterval / StartOffset.
- reader.go:724-737 maps these straight into `ConsumerGroupConfig{...}`; zero values fall
  through to `ConsumerGroupConfig.Validate()` defaults.

kafka-go v0.4.50 DEFAULTS (go.mod:9 pins v0.4.50; cache at
`$GOMODCACHE/github.com/segmentio/[email protected]`):
- consumergroup.go:39  `defaultHeartbeatInterval = 3 * time.Second`
- consumergroup.go:43  `defaultSessionTimeout    = 30 * time.Second`
- consumergroup.go:47  `defaultRebalanceTimeout  = 30 * time.Second`
- consumergroup.go:51  `defaultJoinGroupBackoff  = 5 * time.Second`
- applied: consumergroup.go:199-212 (`if config.X == 0 { config.X = defaultX }`)
- ReaderConfig doc mirror: reader.go:73/102/111/119 (3s/30s/30s/5s).

/health carries NO readiness signal:
- handler.go:24-33 `Health` always `WriteHeader(http.StatusOK)` (`// TODO(#50): add
  readiness probe checking pool/Kafka/RPC reachability`). Handler is `struct{}` (line 11),
  comment line 9-10 says readiness is a future extension.
- main.go:78-86 launches the consumer goroutine; main.go:93 `srv := startHealthServer(...)`
  is independent — the HTTP listener (main.go:120-125) comes up with no dependency on
  consumer group join. So /health 200 happens well before the partition is assigned.
- The test only polls /health: onchain_helpers_test.go:197-198 `waitAdapterHealthy(...)`,
  defined 230-249 (GET until 200). No other readiness gate exists in ledger-adapter
  (grep: only `/health` route, handler.go:19).

Stacked recreates before N9 (tests run sequentially — `grep -c Parallel
known_failures_test.go` = 0, source order S2→S3→N1→N9):
- N1 ends → `t.Cleanup(func(){ restoreDefaultAdapter })` (known_failures_test.go:225)
  → restoreDefaultAdapter (onchain_helpers_test.go:255-259) → recreateAdapterWithEnv(nil)
  = kill+recreate #1.
- N9 body (known_failures_test.go:457) → recreateAdapterWithEnv BATCH_TIMEOUT=10s
  = kill+recreate #2.
- Each recreate: `docker rm -f` (SIGKILL, onchain_helpers_test.go:178) + `docker run`
  + waitAdapterHealthy only. A SIGKILLed member never sends LeaveGroup, so the broker
  holds it as a live member until the 30s session timeout elapses.

The mechanism that makes a fresh member wait (kafka-go run loop):
- consumergroup.go:722-723 `run()` loops `nextGeneration(memberID)`.
- consumergroup.go:798 join; :810 sync; :820 fetchOffsets; only then :860 the Generation
  is handed to `Next()` and the reader subscribes (reader.go:332 `r.subscribe`) and starts
  fetching (reader.go:337). FetchMessage in our consumer.fetchLoop (consumer.go:128)
  cannot return a message until that whole chain completes.
- consumergroup.go:740-745 `case errors.Is(err, RebalanceInProgress):` — explicit comment:
  "don't leave the group or change the member ID … the next attempt to join the group will
  then be subject to the rebalance timeout, so the broker will be responsible for
  throttling this loop." i.e. on this path kafka-go applies NO client backoff; each retry
  blocks up to RebalanceTimeout broker-side.
- The member advertises RebalanceTimeout=30s in its JoinGroup request
  (consumergroup.go:986 `RebalanceTimeout: int32(cg.config.RebalanceTimeout/time.Millisecond)`).
- On a clean error path (default, :747-755) kafka-go does `leaveGroup` + `backoff =
  time.After(JoinGroupBackoff)` = 5s.

### Worst-case timing (why 60s is unsafe)

A single ghost member can stall a fresh join for up to ~SessionTimeout(30s) before the
broker evicts it, and the join request itself can block up to ~RebalanceTimeout(30s) while
the broker waits for the rebalance to settle. With kill+recreate #2 happening 1-2s after
#1, the second fresh consumer can face a group that is mid-rebalance from #1's ghost AND
#2's own ghost. CAVEAT (do not overclaim): I have NOT arithmetically proven the worst case exceeds 60s. I
have shown each COMPONENT can be ~30s (SessionTimeout to evict a ghost; RebalanceTimeout
the join can block) and that a fresh join during an active rebalance is broker-throttled.
But the broker may not stack two full 30s windows: when the new member sends JoinGroup the
broker moves the group to PreparingRebalance immediately, and the held join response is
released when the rebalance completes OR the ghost's session expires — so the realistic
worst case is on the order of "a ghost session timeout plus rebalance settle," which is
PLAUSIBLY >60s under a loaded single-Kafka e2e host, not a guaranteed 60s. The decisive
evidence is empirical: the prior 42s PASS vs 62s FAIL of the SAME code is the signature of a
variable-length rebalance wait straddling the 60s assertion deadline. Conclusion: 60s is
not a SAFE margin (it is close enough to the blind-window distribution that it flakes),
which is all the fix needs to be justified.

## Candidate ranking

Constraints: must not break S2/S3/N1 (currently pass); NO t.Skip; robustness > speed;
prefer fixing shared `recreateAdapterWithEnv` so S3/N1 get the same robustness.

### RECOMMENDED: (A) consumer-readiness warmup probe inside `recreateAdapterWithEnv`

After `waitAdapterHealthy` returns (onchain_helpers_test.go:197), before returning to the
test: publish a throwaway warmup event with FRESH random UUIDs (its own event_id AND its
own product_id) and poll `public.onchain_events` for that warmup event_id until a row
appears with ANY status, up to ~90s. A row appearing proves the consumer pulled the message
and STARTED flushing — i.e. it is in-group and assigned the partition (the exact property we
need), because the row is created by InsertPending at the very start of flushSubBatch
(filterAndMarkPending → f.store.InsertPending, flusher.go:184) BEFORE BatchCall/WaitReceipt.
So "row-exists / any status" is a clean group-membership signal, not a "fully processed"
claim: under default config it trips at PENDING almost immediately; under N1's 1ms poll it
trips at PENDING then the row goes FAILED — non-empty either way. This converts the
meaningless "process alive" gate into a real "consumer is consuming" gate, and every recreate
(S3/N1/N9 + restoreDefaultAdapter) inherits it.

Pitfall assessment (all clear):
- (a) Interference with later assertions: NONE — verified across the WHOLE tests/e2e tree,
  not just the four onchain tests. Grepped every `onchain_events` access and every
  `count(...)` in tests/e2e: EVERY read of `public.onchain_events` is scoped to a single
  `event_id = $1` (onchain_helpers_test.go onchainStatus/onchainTxHash;
  known_failures_test.go:486-488 N9 rowCount) or an explicit id-set `event_id = ANY($1)`
  (distinctTxHashCount, onchain_helpers_test.go:70-73). There is NO unscoped
  `SELECT count(*) FROM public.onchain_events` and NO assertion on total event/transition
  counts anywhere in the suite; the only other COUNT is `wms_ops.outbox_events WHERE
  aggregate_id = $1` (outbox_helpers_test.go), also scoped. There is also no
  TRUNCATE/DELETE of onchain_events in setup that would assume a clean table. So permanent
  accreted warmup rows (which persist past restoreDefaultAdapter, the last adapter action
  before later non-onchain tests) cannot corrupt any assertion. Concretely:
  * S3 distinctTxHashCount filters `WHERE event_id = ANY($1)` over ONLY validEvents+poison
    — a separate warmup event_id is excluded; its ItemTransitionFailed check is on the
    poison's own tx receipt.
  * N9 asserts exactly one row `WHERE event_id = $1` for its own id and itemStatus for its
    own product — a different warmup id/product cannot perturb either.
  * N1 keys on its own product/event.
  The warmup MUST use a DISTINCT product_id too (not just event_id): callItemStatus and the
  on-chain itemStatus FSM are keyed by product_id, and re-using a test product could collide
  with a later same-product assertion. Fresh uuid.New() for both avoids this.
  Also confirmed: the suite's onchain_events cleanup is `DELETE ... WHERE event_id IN (SELECT
  event_id FROM public.outbox_events WHERE aggregate_id = ANY(...))` (fixtures_test.go:169/
  351, fullchain_test.go, multiflow_test.go), scoped by fixture product ids. A warmup's
  random product_id is never a fixture product AND the warmup has no outbox_events row at all
  (it is published straight to Kafka), so cleanup never matches it — it accretes harmlessly.
  The four onchain tests (S2/S3/N1/N9) do not delete their own onchain rows.
- (b) N1 recreate sets RECEIPT_POLL_TIMEOUT=1ms (known_failures_test.go:226-230). A warmup
  event published right after that recreate will likely be marked FAILED by the 1ms poll
  (flusher.go:124-128). That is FINE: the readiness probe waits for ANY status (a row at
  all), and FAILED is a row. It proves the consumer pulled the message and entered
  flushSubBatch. The throwaway event is never asserted on. One subtlety to honor in the impl:
  poll for ROW-EXISTS / any status, NOT for COMMITTED — otherwise the probe itself would hang
  on 1ms-poll-FAILED warmups under N1.
- (b2) Nonce/mempool serialization (the real residual risk, NOT batching): the adapter signs
  every tx with ONE key, so a slow-to-mine warmup tx could queue the next test's tx behind
  its nonce on the single Avalanche node. Independent of co-batching. N1 already caps its
  mining precondition at 120s (known_failures_test.go:243-246) which absorbs one extra queued
  tx; S3/N9 mine fast under normal RECEIPT_POLL_TIMEOUT. Risk LOW and bounded by existing
  caps; if it ever bites, raise the affected test's mining cap. Flagged, not hand-waved.
- (b3) No outbox row needed: consumer.Parse (message.go:36-69) derives event_id from the
  "id" header, product_id from the kafka key, aggregate_type from the header — NO DB lookup.
  So publishing the warmup straight to Kafka via the existing publishEvent is sufficient; the
  adapter will InsertPending and create the onchain_events row from the Kafka message alone.
- (c) Added latency: one extra event per recreate (~hundreds of ms once the consumer is
  actually up; the 90s cap is only hit on the very failure path we are fixing). Acceptable
  under "robustness > speed".

Exact place to change: `recreateAdapterWithEnv` in
tests/e2e/onchain_helpers_test.go, immediately after the
`require.Truef(t, waitAdapterHealthy(...))` at lines 197-198. Add a helper, e.g.
`waitAdapterConsuming(t, ctx, env)` that publishes a fresh warmup event via the existing
`publishEvent(t, ctx, "receiving", uuid.New(), uuid.New(), "{}")` (kafka_helper_test.go:71)
and polls row-exists. Reuse a row-exists poll (a thin variant of waitOnchainStatus that
returns true once `onchainStatus != ""` rather than `== expected`). Cap ~90s to cover the
worst-case double-rebalance; the partition has 1 consumer so a single warmup on the
product-keyed partition is sufficient.

Note: warmup events accrete COMMITTED/FAILED rows across recreates. That is harmless
(every assertion is event_id/product_id-scoped) but keep the warmup payload `"{}"` and
aggregate_type "receiving" so it follows the same valid path the tests use.

### 2nd: (C) unique KAFKA_GROUP_ID per recreate — REJECTED as primary (correctness risk)

A fresh single-member group has no ghost, so join is immediate. BUT: StartOffset default
is FirstOffset (earliest). Confirmed:
- reader.go:485 `// Default: FirstOffset`; reader.go:139 same on ReaderConfig.StartOffset.
- consumergroup.go:243-244 `if config.StartOffset == 0 { config.StartOffset = FirstOffset }`
  — consumer.go never sets StartOffset, so it is 0 → FirstOffset (= -2, reader.go:18).
Consequence: a brand-new group with no committed offset re-consumes the ENTIRE topic from
the beginning. Every prior test's events (S2/S3/N1 and earlier N9 runs) get re-delivered to
the new group. Re-processing is not a no-op crash, but it is a large correctness hazard for
THIS suite: it would re-run filterAndMarkPending over hundreds of already-COMMITTED
events, and — worse — it re-delivers each product's receiving event, which the contract
will reject as a duplicate transition, potentially marking those (replayed) rows FAILED and
emitting churn, plus stretching the flush pipeline so N9's own events land even later. It
also defeats the S2/N9 redelivery semantics those tests rely on. Setting StartOffset=Last
would instead create a join-race where events published right after join (before the
assignment's fetch position is established) can be missed — the exact failure we're fixing.
Net: C trades a timing flake for an offset-semantics minefield. Do not use.

### 3rd: (B) lower kafka-go SessionTimeout in consumer.go — REJECTED (scope creep)

This edits PRODUCTION hot-path code (consumer.go:42-49) to fix a TEST harness gap. It would
shrink the ghost-eviction window, but: it changes prod rebalance/failover behavior (shorter
session timeout = more aggressive eviction under real network blips), needs the
heartbeat-interval ≤ 1/3 session-timeout rule re-checked (consumergroup.go:38 comment), and
still doesn't give the test a positive readiness signal — it only shortens, not eliminates,
the blind window. Inappropriate in a test-stability fix.

### 4th: (D) bump N9 wait 60s→120s — REJECTED (symptom-only, doesn't help S3/N1)

Masks the flake for N9 only, leaves S3/N1 equally blind to the same stacked-recreate gap
(they pass today by luck of shorter ghost windows / 1-member groups). Violates "prefer
fixing the shared helper". A larger timeout is a band-aid, not robustness.

## One-line recommendation

Implement (A): add a consumer-readiness warmup probe (fresh event_id + product_id, poll
`public.onchain_events` for row-exists/ANY status, ~90s cap) inside
`recreateAdapterWithEnv` right after `waitAdapterHealthy`, so every adapter recreate
(S3/N1/N9 and restoreDefaultAdapter) waits until the consumer is actually in-group and
processing — not merely HTTP-alive.
