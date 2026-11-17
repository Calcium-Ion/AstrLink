//go:build !darwin

package raw

import "errors"

// systemBiometrics is no Touch ID: only macOS has it.
type systemBiometrics struct{}

func (systemBiometrics) Available() bool { return false }

func (systemBiometrics) Release(string, string) ([]byte, string) {
	return nil, OutcomeBiometricUnavailable
}

func (systemBiometrics) Store(string, []byte) error {
	return errors.New("Touch ID is only available on macOS")
}

func (systemBiometrics) Forget(string) {}
