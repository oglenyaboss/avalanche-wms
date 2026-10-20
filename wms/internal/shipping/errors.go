package shipping

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput            = errors.New("INVALID_INPUT")
	ErrBinNotFound             = errors.New("BIN_NOT_FOUND")
	ErrBinNotShippingBuffer    = errors.New("BIN_NOT_SHIPPING_BUFFER")
	ErrDispatchNotFound        = errors.New("DISPATCH_NOT_FOUND")
	ErrDispatchAlreadyDeparted = errors.New("DISPATCH_ALREADY_DEPARTED")
	//nolint:misspell // Must match API contract and DB enum value: CANCELLED.
	ErrDispatchCancelled   = errors.New("DISPATCH_CANCELLED")
	ErrDestinationMismatch = errors.New("DESTINATION_MISMATCH")
	ErrDispatchNotAtGate   = errors.New("DISPATCH_NOT_AT_GATE")
	ErrBufferEmpty         = errors.New("BUFFER_EMPTY")
	ErrProductNotInBuffer  = errors.New("PRODUCT_NOT_IN_BUFFER")
	// ErrDraftStale indicates the signed draft no longer matches the warehouse
	// state (a signed product left the buffer bin or changed status).
	ErrDraftStale = errors.New("DRAFT_STALE")
	// ErrPickingNotConfirmed indicates some products' picking is not yet
	// COMMITTED on chain, so the ship must not advance them (issue #15).
	// Handlers map this to HTTP 409.
	ErrPickingNotConfirmed = errors.New("PICKING_NOT_CONFIRMED")
)

// PickingNotConfirmedError carries the products whose picking is not confirmed
// on chain. The shipping error envelope has no details field, so the handler
// renders the count inside the message.
type PickingNotConfirmedError struct {
	ProductIDs []uuid.UUID
}

func (e *PickingNotConfirmedError) Error() string {
	return fmt.Sprintf("%s: %d", ErrPickingNotConfirmed.Error(), len(e.ProductIDs))
}

func (e *PickingNotConfirmedError) Unwrap() error { return ErrPickingNotConfirmed }
