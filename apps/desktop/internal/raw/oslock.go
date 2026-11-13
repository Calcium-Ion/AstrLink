package raw

import (
	"context"
	"log"
	"time"
)

// The raw unlock and every agent grant end when the operator leaves the
// computer: on sleep, on the screen lock and, on macOS, on a switch to
// another account. Linux reports the screen lock through logind's own
// session as well as the screen saver, since a lock started by loginctl or
// the display manager reaches no screen saver. Without a session bus or
// logind the unlock ends with Core's idle timeout only.

// lockTimeout bounds the lock request; the computer may be going to sleep.
const lockTimeout = 10 * time.Second

// SessionLock locks raw content when the session locks or sleeps.
type SessionLock struct {
	Raw Raw
	// Ready reports whether Core is ready, the only time it holds an
	// unlock to end.
	Ready func() bool
}

// Lock locks raw content now, for reason, if Core is ready. It does not
// wait: the lock is a control request.
func (s SessionLock) Lock(reason string) {
	if s.Ready != nil && !s.Ready() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
		defer cancel()
		if _, err := s.Raw.LockRaw(ctx); err != nil {
			log.Printf("astrlink: unable to lock raw content on %s: %v", reason, err)
		}
	}()
}

// Watch follows the session's own lock, which MyGo's Power events do not
// report on this platform, until ctx ends: logind on Linux. On macOS the
// app layer adds the account switch (internal/darwin).
func (s SessionLock) Watch(ctx context.Context) { watchSession(ctx, s.Lock) }
