// Package sigstack keeps every signal handler on the signal stack.
//
// On Linux the web view's libraries (WebKit's JavaScriptCore, GLib) install
// signal handlers without SA_ONSTACK. A signal that lands on a Go thread then
// runs its handler on the running goroutine's stack, which can be as small as
// 2 KiB: the kernel's signal frame alone overflows it and the app crashes in
// the Go runtime. Go gives each of its threads a signal stack, so a handler
// marked SA_ONSTACK runs there instead; threads without one are unaffected.
package sigstack

import (
	"context"
	"time"
)

// every is how often handlers installed since the last pass are fixed; the
// libraries install theirs while the first web view starts.
const every = time.Second

// Keep fixes the handlers now and again every second until ctx ends.
func Keep(ctx context.Context) {
	if !supported {
		return
	}
	fix()
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fix()
			}
		}
	}()
}
