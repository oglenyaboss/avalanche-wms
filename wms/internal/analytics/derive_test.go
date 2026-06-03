package analytics

import (
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestConfirmationRate(t *testing.T) {
	tests := []struct {
		name      string
		committed int64
		total     int64
		want      float64
	}{
		{"zero total yields zero, not NaN", 0, 0, 0},
		{"all committed", 10, 10, 1},
		{"three quarters", 18, 24, 0.75},
		{"none committed", 0, 24, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := confirmationRate(tt.committed, tt.total); got != tt.want {
				t.Fatalf("confirmationRate(%d,%d) = %v, want %v", tt.committed, tt.total, got, tt.want)
			}
		})
	}
}

func TestClampNonNeg(t *testing.T) {
	if got := clampNonNeg(-3); got != 0 {
		t.Fatalf("clampNonNeg(-3) = %d, want 0", got)
	}
	if got := clampNonNeg(5); got != 5 {
		t.Fatalf("clampNonNeg(5) = %d, want 5", got)
	}
}

func TestOrderStages_CanonicalFirstThenSortedExtras(t *testing.T) {
	present := map[string]struct{}{
		"shipping": {}, "picking": {}, "zeta": {}, "alpha": {},
	}
	got := orderStages(present)
	want := []string{"receiving", "putaway", "picking", "shipping", "alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("orderStages length = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orderStages[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestDeriveOnchain_StatusesAndPending(t *testing.T) {
	status := []statusCount{
		{"COMMITTED", 18}, {"SENT", 2}, {"FAILED", 1},
		// note: no explicit PENDING row — pending must be derived by subtraction
	}
	byAgg := []aggStatusCount{
		{"picking", "COMMITTED", 9}, {"picking", "SENT", 1}, {"picking", "FAILED", 1},
		{"shipping", "COMMITTED", 9}, {"shipping", "SENT", 1},
	}
	outboxByType := []statusCount{{"picking", 12}, {"shipping", 12}}
	report := deriveOnchain(status, byAgg, 24, outboxByType, nil, nil)

	if report.TotalEvents != 24 {
		t.Fatalf("TotalEvents = %d, want 24", report.TotalEvents)
	}
	if report.Committed != 18 || report.Sent != 2 || report.Failed != 1 {
		t.Fatalf("committed/sent/failed = %d/%d/%d, want 18/2/1", report.Committed, report.Sent, report.Failed)
	}
	// pending = 24 - 18 - 2 - 1 = 3
	if report.Pending != 3 {
		t.Fatalf("Pending = %d, want 3", report.Pending)
	}
	if report.ConfirmationRate != 0.75 {
		t.Fatalf("ConfirmationRate = %v, want 0.75", report.ConfirmationRate)
	}

	// by_stage must lead with the canonical FSM order, with receiving/putaway
	// present (zeroed) even though they have no events.
	if len(report.ByStage) != 4 {
		t.Fatalf("ByStage length = %d, want 4 (%v)", len(report.ByStage), report.ByStage)
	}
	order := []string{"receiving", "putaway", "picking", "shipping"}
	for i, st := range report.ByStage {
		if st.AggregateType != order[i] {
			t.Fatalf("ByStage[%d] = %q, want %q", i, st.AggregateType, order[i])
		}
	}
	// picking: total 12, committed 9, sent 1, failed 1 → pending 1
	picking := report.ByStage[2]
	if picking.Total != 12 || picking.Committed != 9 || picking.Sent != 1 || picking.Failed != 1 || picking.Pending != 1 {
		t.Fatalf("picking stage = %+v, want total12 c9 s1 f1 p1", picking)
	}
	// shipping: total 12, committed 9, sent 1 → pending 2
	shipping := report.ByStage[3]
	if shipping.Pending != 2 {
		t.Fatalf("shipping.Pending = %d, want 2", shipping.Pending)
	}
}

func TestDeriveOnchain_EmptyIsHonestZero(t *testing.T) {
	report := deriveOnchain(nil, nil, 0, nil, nil, nil)
	if report.TotalEvents != 0 || report.Committed != 0 || report.Pending != 0 || report.ConfirmationRate != 0 {
		t.Fatalf("empty report not zeroed: %+v", report)
	}
	// canonical stages still rendered (all zero) so the UI has a stable skeleton
	if len(report.ByStage) != 4 {
		t.Fatalf("ByStage length = %d, want 4 even when empty", len(report.ByStage))
	}
	if report.RecentFailed == nil || report.RecentCommitted == nil {
		t.Fatalf("recent feeds must be non-nil slices for clean JSON, got %+v / %+v",
			report.RecentFailed, report.RecentCommitted)
	}
}

func TestToEvents_FormatsAndPreservesNullables(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	refs := []eventRef{
		{EventID: "e1", AggregateType: "shipping", TxHash: strPtr("0xabc"), UpdatedAt: ts},
		{EventID: "e2", AggregateType: "picking", ErrorMessage: strPtr("Invalid status transition"), UpdatedAt: ts},
	}
	got := toEvents(refs)
	if got[0].TxHash == nil || *got[0].TxHash != "0xabc" {
		t.Fatalf("tx hash not preserved: %+v", got[0])
	}
	if got[0].UpdatedAt != "2026-06-01T12:30:00Z" {
		t.Fatalf("UpdatedAt = %q, want RFC3339 UTC", got[0].UpdatedAt)
	}
	if got[1].ErrorMessage == nil || *got[1].ErrorMessage != "Invalid status transition" {
		t.Fatalf("error message not preserved: %+v", got[1])
	}
}

func TestPivotThroughput_GapFreeAxisAndTotals(t *testing.T) {
	today := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	rows := []throughputRow{
		{Day: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), AggregateType: "picking", Count: 12},
		{Day: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), AggregateType: "shipping", Count: 12},
		{Day: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), AggregateType: "putaway", Count: 4},
	}
	report := pivotThroughput(rows, 3, today)

	wantDays := []string{"2026-05-31", "2026-06-01", "2026-06-02"}
	if len(report.Days) != 3 {
		t.Fatalf("Days length = %d, want 3 (%v)", len(report.Days), report.Days)
	}
	for i, d := range wantDays {
		if report.Days[i] != d {
			t.Fatalf("Days[%d] = %q, want %q", i, report.Days[i], d)
		}
	}
	// totals: 2026-05-31 → 4, 2026-06-01 → 24, 2026-06-02 → 0
	wantTotals := []int64{4, 24, 0}
	for i, w := range wantTotals {
		if report.Totals[i] != w {
			t.Fatalf("Totals[%d] = %d, want %d (%v)", i, report.Totals[i], w, report.Totals)
		}
	}
	// canonical 4 bands always present
	if len(report.Series) != 4 {
		t.Fatalf("Series length = %d, want 4", len(report.Series))
	}
}

func TestPivotThroughput_IgnoresRowsOutsideWindow(t *testing.T) {
	today := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	rows := []throughputRow{
		{Day: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), AggregateType: "picking", Count: 99}, // outside 3-day window
		{Day: today, AggregateType: "picking", Count: 5},
	}
	report := pivotThroughput(rows, 3, today)
	var sum int64
	for _, v := range report.Totals {
		sum += v
	}
	if sum != 5 {
		t.Fatalf("total across window = %d, want 5 (out-of-window row must be dropped)", sum)
	}
}

func TestPivotThroughput_MinimumOneDay(t *testing.T) {
	today := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	report := pivotThroughput(nil, 0, today)
	if len(report.Days) != 1 || report.Days[0] != "2026-06-02" {
		t.Fatalf("days<1 must clamp to a single day, got %v", report.Days)
	}
}
