package dispatches

import "errors"

var (
	ErrDestinationNotFound = errors.New("DESTINATION_NOT_FOUND")
	ErrDispatchNotFound    = errors.New("DISPATCH_NOT_FOUND")
)

type ApiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
