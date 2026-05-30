package assembly

import "errors"

var (
	ErrDestinationNotFound  = errors.New("DESTINATION_NOT_FOUND")
	ErrInsufficientStock    = errors.New("INSUFFICIENT_STOCK")
	ErrSKUNotFound          = errors.New("SKU_NOT_FOUND")
	ErrOrderNotNew          = errors.New("ORDER_NOT_NEW")
	ErrNoTaskForProduct     = errors.New("NO_TASK_FOR_PRODUCT")
	ErrProductNotAllocated  = errors.New("PRODUCT_NOT_ALLOCATED")
	ErrInvalidInput         = errors.New("INVALID_INPUT")
	ErrBinNotShippingBuffer = errors.New("BIN_NOT_SHIPPING_BUFFER")
	ErrCartEmpty            = errors.New("CART_EMPTY")
)
