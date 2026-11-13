//go:build !darwin

package keystore

// KeychainEnabled reports whether the local key lives in the keychain: only
// signed macOS builds keep it there; Core keeps a key file everywhere else.
func KeychainEnabled() bool { return false }
