package subscription

import "errors"

var (
	ErrNotFound                = errors.New("subscription account not found")
	ErrNotConnected            = errors.New("subscription account is not connected")
	ErrCredentialUnavailable   = errors.New("account credential store unavailable")
	ErrAccountAlreadyConnected = errors.New("subscription account is already connected")
	// ErrUsageUnavailable and ErrResetUnavailable are provider-neutral; the
	// manager prefixes the owning provider ("claude usage unavailable: …").
	ErrUsageUnavailable = errors.New("usage unavailable")
	ErrResetUnavailable = errors.New("usage reset unavailable")
	ErrNothingToReset   = errors.New("no rate-limit window is eligible for a reset")
	ErrNoResetCredit    = errors.New("no earned reset credits available")
)
