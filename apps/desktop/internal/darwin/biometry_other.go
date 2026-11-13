//go:build !darwin

package darwin

import "time"

// BiometryAvailable reports no Touch ID outside macOS.
func BiometryAvailable() bool { return false }

// BiometryOutcome is how the Touch ID prompt ended.
type BiometryOutcome struct {
	Passed   bool
	Code     int
	TimedOut bool
}

// EvaluateBiometry shows no prompt outside macOS.
func EvaluateBiometry(reason string, wait time.Duration) BiometryOutcome {
	return BiometryOutcome{Code: LAErrorBiometryNotAvailable}
}
