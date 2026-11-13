package darwin

// LAError codes of LocalAuthentication that tell a refusal apart from a
// dismissed prompt (LAError.h).
const (
	LAErrorAuthenticationFailed = -1
	LAErrorUserCancel           = -2
	LAErrorUserFallback         = -3
	LAErrorSystemCancel         = -4
	LAErrorPasscodeNotSet       = -5
	LAErrorBiometryNotAvailable = -6
	LAErrorBiometryNotEnrolled  = -7
	LAErrorBiometryLockout      = -8
	LAErrorAppCancel            = -9
	LAErrorInvalidContext       = -10
	LAErrorNotInteractive       = -1004
)
