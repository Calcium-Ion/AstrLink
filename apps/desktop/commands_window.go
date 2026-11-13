package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/ui"
)

// registerWindowCommands binds the services of windows, the tray and the
// system: window chrome, the tray popover, trajectory inspectors, files and
// the display currency (src/native-host/commands-window.ts). It makes this
// the only instance of the app, which a second launch hands its links to.
func registerWindowCommands(h *host.Host) {
	generating := os.Getenv("MYGO_GENERATE") != ""
	if !mygo.App.RequestSingleInstanceLock() {
		// The running instance shows its window and takes the links.
		os.Exit(0)
	}
	// Installers register the scheme; this repairs portable copies. macOS
	// takes it from the bundle (urlSchemes in mygo.config.ts).
	if !generating && runtime.GOOS != "darwin" {
		scheme := instance.FromEnvironment().LinkScheme()
		if err := mygo.App.RegisterURLScheme(scheme); err != nil {
			log.Printf("astrlink: unable to register %s:// links: %v", scheme, err)
		}
	}
	app := ui.New(h)
	mygo.Bind(append([]any{h.WindowService()}, app.Services()...)...)
}
