package passkeys

import "errors"

var (
	ErrNoOrganization      = errors.New("NO_ORGANIZATION")
	ErrForbidden           = errors.New("FORBIDDEN")
	ErrPasskeyExists       = errors.New("PASSKEY_EXISTS")
	ErrInvalidRegistration = errors.New("INVALID_REGISTRATION")
	ErrPasskeyNotFound     = errors.New("PASSKEY_NOT_FOUND")

	ErrSignerKeyNotActive     = errors.New("SIGNER_KEY_NOT_ACTIVE")
	ErrRegistryUnavailable    = errors.New("REGISTRY_UNAVAILABLE")
	ErrTargetNotPending       = errors.New("TARGET_NOT_PENDING")
	ErrTargetNotActive        = errors.New("TARGET_NOT_ACTIVE")
	ErrActionInProgress       = errors.New("ACTION_IN_PROGRESS")
	ErrActionNotFound         = errors.New("ACTION_NOT_FOUND")
	ErrActionAlreadySubmitted = errors.New("ACTION_ALREADY_SUBMITTED")
	ErrInvalidAssertion       = errors.New("INVALID_ASSERTION")
	ErrInvalidRequest         = errors.New("INVALID_REQUEST")
)
