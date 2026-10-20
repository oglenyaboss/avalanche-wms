package signedops

import "errors"

var (
	ErrOperationNotFound     = errors.New("OPERATION_NOT_FOUND")
	ErrOperationSubmitted    = errors.New("OPERATION_ALREADY_SUBMITTED")
	ErrOperationNotSubmitted = errors.New("OPERATION_NOT_SUBMITTED")
	ErrSignerNotActive       = errors.New("SIGNER_KEY_NOT_ACTIVE")
	ErrInvalidAssertion      = errors.New("INVALID_ASSERTION")
	ErrInvalidRequest        = errors.New("INVALID_REQUEST")
)
