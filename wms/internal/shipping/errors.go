package shipping

import "errors"

var (
	ErrInvalidInput            = errors.New("INVALID_INPUT")
	ErrBinNotFound             = errors.New("BIN_NOT_FOUND")
	ErrBinNotShippingBuffer    = errors.New("BIN_NOT_SHIPPING_BUFFER")
	ErrDispatchNotFound        = errors.New("DISPATCH_NOT_FOUND")
	ErrDispatchAlreadyDeparted = errors.New("DISPATCH_ALREADY_DEPARTED")
	ErrDispatchCancelled       = errors.New("DISPATCH_CANCELLED")
)
