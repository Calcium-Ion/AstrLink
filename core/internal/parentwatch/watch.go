// Package parentwatch cancels a Core context when its launching desktop
// process disappears. A zero parent PID keeps the Core independent.
package parentwatch

import (
	"context"
	"fmt"
	"time"
)

type parentPIDLookup func() (pid int, supported bool)

// NotifyContext returns a child context that is cancelled when the current
// parent PID no longer matches expectedPID. Platforms without a reliable
// parent-PID signal leave the context attached only to parent; the desktop
// process lifecycle is enforced separately there.
func NotifyContext(parent context.Context, expectedPID int, interval time.Duration) (context.Context, context.CancelFunc, error) {
	return notifyContext(parent, expectedPID, interval, currentParentPID)
}

func notifyContext(
	parent context.Context,
	expectedPID int,
	interval time.Duration,
	lookup parentPIDLookup,
) (context.Context, context.CancelFunc, error) {
	if expectedPID < 0 {
		return nil, nil, fmt.Errorf("parent PID must not be negative")
	}
	if expectedPID > 0 && interval <= 0 {
		return nil, nil, fmt.Errorf("parent watchdog interval must be positive")
	}

	ctx, cancel := context.WithCancel(parent)
	if expectedPID == 0 {
		return ctx, cancel, nil
	}

	actualPID, supported := lookup()
	if !supported {
		return ctx, cancel, nil
	}
	if actualPID != expectedPID {
		cancel()
		return nil, nil, fmt.Errorf("parent PID mismatch: expected %d, got %d", expectedPID, actualPID)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pid, ok := lookup()
				if !ok || pid != expectedPID {
					cancel()
					return
				}
			}
		}
	}()

	return ctx, cancel, nil
}
