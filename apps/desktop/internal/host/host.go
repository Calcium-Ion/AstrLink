// Package host holds what the desktop app's parts share: the instance it
// is, its directories, preferences, the Core it supervises and the main
// window. Services of every area are built on a *Host.
package host

import (
	"log"
	"sync"

	"github.com/egoist/mygo"
)

// Host is the running desktop app. New only allocates it; Start, called
// once the app is ready, does the work.
type Host struct {
	mu   sync.Mutex
	main *mygo.Window
	// started runs once the host is up, for areas that start their own work
	// after Core's (the tray, updates).
	started []func()
}

// New returns a host that has not started.
func New() *Host { return &Host{} }

// OnStarted registers fn to run on the main thread once Start finished.
// Areas register in their register*Commands function.
func (h *Host) OnStarted(fn func()) {
	h.mu.Lock()
	h.started = append(h.started, fn)
	h.mu.Unlock()
}

// Start opens the main window and starts Core. It runs on the main thread
// once the app is ready (App.WhenReady).
func (h *Host) Start() {
	w := mygo.NewWindow(mygo.WindowOptions{Title: "AstrLink", URL: "/", Width: 950, Height: 620})
	h.mu.Lock()
	h.main = w
	started := h.started
	h.mu.Unlock()
	for _, fn := range started {
		fn()
	}
	log.Printf("AstrLink desktop started")
}

// MainWindow returns the main window, or nil before Start or once it closed.
func (h *Host) MainWindow() *mygo.Window {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.main == nil || h.main.IsDestroyed() {
		return nil
	}
	return h.main
}
