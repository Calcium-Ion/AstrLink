package main

import (
	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
)

// registerWindowCommands binds the services of windows, the tray and the
// system: window chrome, the tray popover, trajectory inspectors, files and
// links (src/native-host/commands-window.ts).
func registerWindowCommands(h *host.Host) {
	mygo.Bind(h.WindowService())
}
