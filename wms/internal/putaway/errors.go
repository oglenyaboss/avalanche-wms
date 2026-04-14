package putaway

import "errors"

var (
	ErrBufferBinNotFound  = errors.New("BUFFER_BIN_NOT_FOUND")
	ErrStorageBinNotFound = errors.New("STORAGE_BIN_NOT_FOUND")
	ErrProductNotFound    = errors.New("PRODUCT_NOT_FOUND")
	ErrProductNotInBuffer = errors.New("PRODUCT_NOT_IN_BUFFER")
	ErrProductNotReceived = errors.New("PRODUCT_NOT_RECEIVED")
	ErrInvalidInput       = errors.New("INVALID_INPUT")
)
