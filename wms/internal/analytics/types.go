package analytics

import "time"

// ---- internal raw rows (repository → derive) ----

// statusCount is a single GROUP BY status aggregate row.
type statusCount struct {
	Status string
	Count  int64
}

// aggStatusCount is a single (aggregate_type, status) aggregate row.
type aggStatusCount struct {
	AggregateType string
	Status        string
	Count         int64
}

// throughputRow is one (day, aggregate_type) volume bucket from outbox_events.
type throughputRow struct {
	Day           time.Time
	AggregateType string
	Count         int64
}

// eventRef is a single onchain_events row surfaced to operators (recent
// failed/committed lists). TxHash/ErrorMessage are nullable.
type eventRef struct {
	EventID       string
	AggregateType string
	TxHash        *string
	ErrorMessage  *string
	UpdatedAt     time.Time
}

// ---- wire DTOs (snake_case JSON, returned inside the standard envelope) ----

// Totals are headline counters across the master-data tables.
type Totals struct {
	Skus         int64 `json:"skus"`
	Products     int64 `json:"products"`
	Orders       int64 `json:"orders"`
	Dispatches   int64 `json:"dispatches"`
	Destinations int64 `json:"destinations"`
}

// StatusBucket is one status → count pair for a lifecycle breakdown.
type StatusBucket struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// SummaryReport backs GET /analytics/summary.
type SummaryReport struct {
	Totals             Totals         `json:"totals"`
	EventsToday        int64          `json:"events_today"`
	ProductsByStatus   []StatusBucket `json:"products_by_status"`
	OrdersByStatus     []StatusBucket `json:"orders_by_status"`
	DispatchesByStatus []StatusBucket `json:"dispatches_by_status"`
}

// StageOnchain is the onchain confirmation breakdown for one warehouse stage.
// Pending folds together explicit PENDING rows and outbox events that have not
// yet reached the chain at all.
type StageOnchain struct {
	AggregateType string `json:"aggregate_type"`
	Total         int64  `json:"total"`
	Committed     int64  `json:"committed"`
	Sent          int64  `json:"sent"`
	Pending       int64  `json:"pending"`
	Failed        int64  `json:"failed"`
}

// OnchainEvent is a single event surfaced in the recent failed/committed feeds.
type OnchainEvent struct {
	EventID       string  `json:"event_id"`
	AggregateType string  `json:"aggregate_type"`
	TxHash        *string `json:"tx_hash,omitempty"`
	ErrorMessage  *string `json:"error_message,omitempty"`
	UpdatedAt     string  `json:"updated_at"`
}

// OnchainReport backs GET /analytics/onchain — the mandatory blockchain hero.
// ConfirmationRate is COMMITTED / TotalEvents at the event level (TotalEvents is
// the count of outbox_events; KPP gate events are not outbox-bound and so never
// counted). It ranges 0.0–1.0.
type OnchainReport struct {
	TotalEvents      int64          `json:"total_events"`
	Committed        int64          `json:"committed"`
	Sent             int64          `json:"sent"`
	Pending          int64          `json:"pending"`
	Failed           int64          `json:"failed"`
	ConfirmationRate float64        `json:"confirmation_rate"`
	ByStage          []StageOnchain `json:"by_stage"`
	RecentFailed     []OnchainEvent `json:"recent_failed"`
	RecentCommitted  []OnchainEvent `json:"recent_committed"`
}

// ThroughputSeries is one stacked-area band: a stage and its per-day counts,
// aligned positionally with ThroughputReport.Days.
type ThroughputSeries struct {
	AggregateType string  `json:"aggregate_type"`
	Counts        []int64 `json:"counts"`
}

// ThroughputReport backs GET /analytics/throughput. Days is a gap-free ascending
// date axis; Totals[i] is the sum across all series on Days[i].
type ThroughputReport struct {
	Days   []string           `json:"days"`
	Series []ThroughputSeries `json:"series"`
	Totals []int64            `json:"totals"`
}

// fsmOrder is the canonical warehouse-stage order used to keep onchain stage
// breakdowns and throughput bands stable regardless of which stages have data.
var fsmOrder = []string{"receiving", "putaway", "picking", "shipping"}

const dayLayout = "2006-01-02"
