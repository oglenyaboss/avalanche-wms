package domain

type OutboundDispatchStatus string

const (
	OutboundDispatchStatusScheduled OutboundDispatchStatus = "SCHEDULED"
	OutboundDispatchStatusAtGate    OutboundDispatchStatus = "AT_GATE"
	OutboundDispatchStatusDeparted  OutboundDispatchStatus = "DEPARTED"
	OutboundDispatchStatusCancelled OutboundDispatchStatus = "CANCELLED"
)
