package analytics

import (
	"sort"
	"time"
)

// clampNonNeg returns 0 for negative values. Pending is computed by subtraction
// (outbox total minus the chain-side statuses); a clock skew or out-of-order
// row could in theory drive it slightly negative, which would be nonsensical to
// show, so we floor it at zero.
func clampNonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// confirmationRate is COMMITTED / total at the event level. Returns 0 when there
// are no events so the UI can render "0%" rather than divide by zero.
func confirmationRate(committed, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(committed) / float64(total)
}

// orderStages returns the stages to render: the canonical FSM stages first (in
// pipeline order), then any unexpected extras present in the data, sorted, so a
// new aggregate type is surfaced rather than silently dropped.
func orderStages(present map[string]struct{}) []string {
	stages := make([]string, 0, len(present)+len(fsmOrder))
	seen := map[string]struct{}{}
	for _, s := range fsmOrder {
		stages = append(stages, s)
		seen[s] = struct{}{}
	}
	extras := make([]string, 0)
	for s := range present {
		if _, ok := seen[s]; !ok {
			extras = append(extras, s)
		}
	}
	sort.Strings(extras)
	return append(stages, extras...)
}

// deriveOnchain assembles the onchain hero report from the raw aggregates.
//   - statusCounts: onchain_events grouped by status (overall)
//   - byAgg: onchain_events grouped by (aggregate_type, status)
//   - outboxTotal: total outbox_events (the confirmation-rate denominator)
//   - outboxByType: outbox_events grouped by aggregate_type (per-stage totals)
//
// Pending is derived as total − committed − sent − failed, so outbox events that
// have not yet produced an onchain_events row count as pending.
func deriveOnchain(
	statusCounts []statusCount,
	byAgg []aggStatusCount,
	outboxTotal int64,
	outboxByType []statusCount,
	recentFailed []eventRef,
	recentCommitted []eventRef,
) OnchainReport {
	overall := map[string]int64{}
	for _, s := range statusCounts {
		overall[s.Status] = s.Count
	}
	committed := overall["COMMITTED"]
	sent := overall["SENT"]
	failed := overall["FAILED"]
	pending := clampNonNeg(outboxTotal - committed - sent - failed)

	// Per-stage totals from outbox, per-stage statuses from onchain.
	stageTotal := map[string]int64{}
	present := map[string]struct{}{}
	for _, t := range outboxByType {
		stageTotal[t.Status] = t.Count // Status field carries the aggregate_type here
		present[t.Status] = struct{}{}
	}
	type stageAcc struct{ committed, sent, failed int64 }
	stageStatus := map[string]*stageAcc{}
	acc := func(k string) *stageAcc {
		if stageStatus[k] == nil {
			stageStatus[k] = &stageAcc{}
		}
		present[k] = struct{}{}
		return stageStatus[k]
	}
	for _, a := range byAgg {
		switch a.Status {
		case "COMMITTED":
			acc(a.AggregateType).committed += a.Count
		case "SENT":
			acc(a.AggregateType).sent += a.Count
		case "FAILED":
			acc(a.AggregateType).failed += a.Count
		}
	}

	byStage := make([]StageOnchain, 0, len(present))
	for _, stage := range orderStages(present) {
		total := stageTotal[stage]
		var c, s, f int64
		if a := stageStatus[stage]; a != nil {
			c, s, f = a.committed, a.sent, a.failed
		}
		byStage = append(byStage, StageOnchain{
			AggregateType: stage,
			Total:         total,
			Committed:     c,
			Sent:          s,
			Pending:       clampNonNeg(total - c - s - f),
			Failed:        f,
		})
	}

	return OnchainReport{
		TotalEvents:      outboxTotal,
		Committed:        committed,
		Sent:             sent,
		Pending:          pending,
		Failed:           failed,
		ConfirmationRate: confirmationRate(committed, outboxTotal),
		ByStage:          byStage,
		RecentFailed:     toEvents(recentFailed),
		RecentCommitted:  toEvents(recentCommitted),
	}
}

func toEvents(refs []eventRef) []OnchainEvent {
	out := make([]OnchainEvent, 0, len(refs))
	for _, r := range refs {
		out = append(out, OnchainEvent{
			EventID:       r.EventID,
			AggregateType: r.AggregateType,
			TxHash:        r.TxHash,
			ErrorMessage:  r.ErrorMessage,
			UpdatedAt:     r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// pivotThroughput turns sparse (day, type, count) rows into a gap-free, chart-
// ready report: a continuous day axis of length `days` ending at `today`, one
// band per stage (canonical stages always present, extras appended), and a
// per-day totals row. Rows outside the window are ignored.
func pivotThroughput(rows []throughputRow, days int, today time.Time) ThroughputReport {
	if days < 1 {
		days = 1
	}
	today = today.UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))

	axis := make([]string, days)
	idx := map[string]int{}
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format(dayLayout)
		axis[i] = key
		idx[key] = i
	}

	present := map[string]struct{}{}
	for _, r := range rows {
		present[r.AggregateType] = struct{}{}
	}
	stages := orderStages(present)

	counts := map[string][]int64{}
	for _, stage := range stages {
		counts[stage] = make([]int64, days)
	}
	totals := make([]int64, days)
	for _, r := range rows {
		key := r.Day.UTC().Format(dayLayout)
		i, ok := idx[key]
		if !ok {
			continue
		}
		counts[r.AggregateType][i] += r.Count
		totals[i] += r.Count
	}

	series := make([]ThroughputSeries, 0, len(stages))
	for _, stage := range stages {
		series = append(series, ThroughputSeries{AggregateType: stage, Counts: counts[stage]})
	}
	return ThroughputReport{Days: axis, Series: series, Totals: totals}
}
