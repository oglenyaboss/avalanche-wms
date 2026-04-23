package domain

type OutboundDispatchStatus string

const (
	OutboundDispatchStatusScheduled OutboundDispatchStatus = "SCHEDULED"
	OutboundDispatchStatusAtGate    OutboundDispatchStatus = "AT_GATE"
	OutboundDispatchStatusDeparted  OutboundDispatchStatus = "DEPARTED"
	//nolint:misspell // Must match DB enum value from migration 0007: CANCELLED.
	OutboundDispatchStatusCancelled OutboundDispatchStatus = "CANCELLED"
)
