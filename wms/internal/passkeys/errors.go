package passkeys

import "errors"

var (
	ErrNoOrganization      = errors.New("NO_ORGANIZATION")
	ErrForbidden           = errors.New("FORBIDDEN")
	ErrPasskeyExists       = errors.New("PASSKEY_EXISTS")
	ErrInvalidRegistration = errors.New("INVALID_REGISTRATION")
	ErrPasskeyNotFound     = errors.New("PASSKEY_NOT_FOUND")
)
