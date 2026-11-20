// Package sigstack keeps the web view's signal handlers from overflowing
// goroutine stacks.
//
// On Linux the web view's libraries (GLib, WebKit's JavaScriptCore) install
// signal handlers without SA_ONSTACK. A signal that lands on a Go thread then
// runs its handler on the running goroutine's stack, which can be as small as
// 2 KiB: the kernel's signal frame alone overflows it and the app crashes in
// the Go runtime. Go gives each of its threads a signal stack, so a handler
// marked SA_ONSTACK runs there instead; threads without one are unaffected.
//
// JavaScriptCore's suspend signal is the exception: its handler must run on
// the stack its thread is using (see fix). It reaches only the UI thread,
// which waits on its own stack in the GTK main loop and runs the app's Go
// callbacks on the main goroutine's stack; Run keeps room on that stack for
// the signal's frame.
package sigstack

import (
	"context"
	"log"
	"runtime"
	"time"
)

// every is how often handlers installed since the last pass are fixed.
const every = time.Second

// room is how much of the main goroutine's stack Run keeps free for the app's
// callbacks: many times the largest signal frame, about 4 KiB with AVX-512.
const room = 64 << 10

// Keep makes GLib install its SIGCHLD handler for good, fixes every handler
// now, and fixes new ones every second until ctx ends.
func Keep(ctx context.Context) {
	if !supported {
		return
	}
	if err := holdChildHandler(); err != nil {
		log.Printf("astrlink: unable to hold GLib's SIGCHLD handler: %v", err)
	}
	suspend := suspendSignal()
	fix(suspend)
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fix(suspend)
			}
		}
	}()
}

// Run calls run, the app's main loop, from the main goroutine. The UI thread
// runs the app's Go callbacks on this goroutine's stack, below Run, and
// JavaScriptCore's suspend signal can arrive during one: its frame then goes
// on that stack, where Go may have left only a few hundred bytes free. A
// local as large as room makes Go grow the stack before run starts, to twice
// room, and keeps it from shrinking, so the callbacks start with about room
// free beneath them.
func Run(run func() error) error {
	if !supported {
		return run()
	}
	var reserve [room]byte
	err := run()
	runtime.KeepAlive(&reserve)
	return err
}
