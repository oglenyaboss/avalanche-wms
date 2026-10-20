package audit

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wms/internal/signedaction"
)

// Details are the detailsHash inputs of a warehouse action, recomputed from domain tables (ADR §4).
type Details struct {
	ActionType          uint8
	Salt                [32]byte
	CargoplaceID        uuid.UUID
	StorageBinID        uuid.UUID
	ShippingBufferBinID uuid.UUID
	DispatchID          uuid.UUID
	OrderIDs            []uuid.UUID
	OperatorID          uuid.UUID
	OccurredAtMs        uint64
}

// ErrUnknownActionType is wrapped by Details.Hash for an ActionType outside the lifecycle actions.
var ErrUnknownActionType = errors.New("unknown action type")

// Hash computes the detailsHash of the action (ADR §4) for its ActionType.
func (d Details) Hash() ([32]byte, error) {
	switch d.ActionType {
	case signedaction.TypeReceive:
		return signedaction.ReceiveDetailsHash(d.Salt, d.CargoplaceID, d.OperatorID, d.OccurredAtMs), nil
	case signedaction.TypePutAway:
		return signedaction.PutAwayDetailsHash(d.Salt, d.StorageBinID, d.OperatorID, d.OccurredAtMs), nil
	case signedaction.TypePick:
		return signedaction.PickDetailsHash(d.Salt, d.ShippingBufferBinID, d.OrderIDs, d.OperatorID, d.OccurredAtMs), nil
	case signedaction.TypeShip:
		return signedaction.ShipDetailsHash(d.Salt, d.DispatchID, d.ShippingBufferBinID, d.OrderIDs, d.OperatorID, d.OccurredAtMs), nil
	default:
		return [32]byte{}, fmt.Errorf("audit.Details.Hash: %w %d", ErrUnknownActionType, d.ActionType)
	}
}
