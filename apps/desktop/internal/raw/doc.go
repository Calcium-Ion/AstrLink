// Package raw is the desktop side of raw audit content: deciding an agent's
// request, revoking grants, unlocking, and the raw password. Proofs are
// forwarded to Core once and never kept. It ports raw_access.rs; the screen
// lock (os_lock.rs) and Touch ID (raw_biometric.rs) live beside it.
package raw
