//go:build darwin

package raw

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
)

// biometricWait withdraws an unanswered prompt so the dialog recovers.
const biometricWait = 2 * time.Minute

// systemBiometrics is Touch ID with the key in the login keychain.
type systemBiometrics struct{}

var (
	keychainOnce    sync.Once
	keychainEnabled bool
)

// enabled reports whether this build keeps keys in the keychain: a signed
// build running from its bundle. The signature does not change while the
// app runs.
func (systemBiometrics) enabled() bool {
	keychainOnce.Do(func() { keychainEnabled = keystore.KeychainEnabled() })
	return keychainEnabled
}

func (b systemBiometrics) Available() bool { return b.enabled() && darwin.BiometryAvailable() }

func (b systemBiometrics) Release(account, reason string) ([]byte, string) {
	if !b.Available() {
		return nil, OutcomeBiometricUnavailable
	}
	if outcome := darwin.EvaluateBiometry(reason, biometricWait); !outcome.Passed {
		return nil, biometricOutcome(outcome)
	}
	key, err := mygo.Secrets.Service(BiometricKeychainService).Get(account)
	switch {
	case errors.Is(err, mygo.ErrSecretNotFound):
		return nil, OutcomeBiometricUnavailable
	case err != nil:
		log.Printf("astrlink: unable to read the Touch ID key from the keychain: %v", err)
		return nil, OutcomeBiometricUnavailable
	case len(key) != BiometricKeyBytes:
		clear(key)
		log.Print("astrlink: the Touch ID key in the keychain has the wrong length")
		return nil, OutcomeBiometricUnavailable
	}
	return key, ""
}

func (b systemBiometrics) Store(account string, key []byte) error {
	if !b.enabled() {
		return errors.New("Touch ID is not available in this build")
	}
	if err := mygo.Secrets.Service(BiometricKeychainService).Set(account, key); err != nil {
		return errors.New("unable to keep the Touch ID key in the keychain: " + err.Error())
	}
	return nil
}

func (b systemBiometrics) Forget(account string) {
	if !b.enabled() {
		return
	}
	err := mygo.Secrets.Service(BiometricKeychainService).Delete(account)
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		log.Printf("astrlink: unable to remove the Touch ID key from the keychain: %v", err)
	}
}
