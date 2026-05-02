package dispatches

type OutboundDispatchStatus string

var (
	SCHEDULED OutboundDispatchStatus = "SCHEDULED"
	ATGATE    OutboundDispatchStatus = "AT_GATE"
	DEPARTED  OutboundDispatchStatus = "DEPARTED"
	CANCELED  OutboundDispatchStatus = "CANCELED"
)
