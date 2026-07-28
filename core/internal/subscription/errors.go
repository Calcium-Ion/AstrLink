package subscription

import "errors"

var (
	ErrNotFound                = errors.New("subscription account not found")
	ErrCredentialUnavailable   = errors.New("account credential store unavailable")
	ErrAccountAlreadyConnected = errors.New("subscription account is already connected")
)
