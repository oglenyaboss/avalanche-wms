package domain

type OperationOnchainStatus string

const (
	OperationOnchainStatusPendingOnchain   OperationOnchainStatus = "PENDING_ONCHAIN"
	OperationOnchainStatusOnchainCommitted OperationOnchainStatus = "ONCHAIN_COMMITTED"
)
