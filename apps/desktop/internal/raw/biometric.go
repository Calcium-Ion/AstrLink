package raw

import (
	"crypto/rand"
	"fmt"

	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
)

// Touch ID for raw content on signed macOS builds. Core keeps a second
// envelope of the raw private key, wrapped under 32 random bytes the login
// keychain keeps. The bytes leave the keychain only after LocalAuthentication
// confirms the operator's fingerprint in this app's own prompt, and go to
// Core once, over the operator channel. A same-user agent cannot read the
// keychain item without the login password, and a Core it starts itself
// finds only the envelope. The raw password always works beside it.

// BiometricKeychainService is the keychain service of every Touch ID key
// entry; the account is the data directory's, as for the local key.
const BiometricKeychainService = "com.astrlink.desktop.raw-biometric"

// Biometrics keeps the Touch ID key and asks for the fingerprint that
// releases it.
type Biometrics interface {
	// Available reports whether Touch ID can stand in for the raw
	// password: a signed build that keeps keys in the keychain, on a Mac
	// with an enrolled fingerprint.
	Available() bool
	// Release shows the Touch ID prompt with reason and, once it is
	// answered, reads the key of account. It returns the key, or the
	// outcome the dialog shows instead. It blocks until the operator
	// answers.
	Release(account, reason string) ([]byte, string)
	// Store keeps the key of account, replacing an earlier one.
	Store(account string, key []byte) error
	// Forget removes the key of account, so nothing the keychain holds
	// opens raw content.
	Forget(account string)
}

// NewBiometricKey is a fresh key for a new envelope.
func NewBiometricKey() ([]byte, error) {
	key := make([]byte, BiometricKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("unable to generate the Touch ID key: %v", err)
	}
	return key, nil
}

// biometricOutcome is the outcome the dialog shows for a prompt that did
// not release the key: Touch ID that cannot be used here is unavailable,
// anything else, such as a dismissed prompt, cancelled.
func biometricOutcome(outcome darwin.BiometryOutcome) string {
	if outcome.TimedOut {
		return OutcomeBiometricCancelled
	}
	switch outcome.Code {
	case darwin.LAErrorBiometryNotAvailable, darwin.LAErrorBiometryNotEnrolled, darwin.LAErrorBiometryLockout,
		darwin.LAErrorNotInteractive, darwin.LAErrorInvalidContext:
		return OutcomeBiometricUnavailable
	}
	return OutcomeBiometricCancelled
}
