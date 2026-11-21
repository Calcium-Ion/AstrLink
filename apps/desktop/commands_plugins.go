package main

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/ui"
)

// registerPluginCommands binds the plugin page, its confirmation window and
// the request metadata (src/native-host/commands-plugins.ts), and serves
// plugin interface code to the main window over the astrlink-plugin
// scheme, which windows built later handle.
func registerPluginCommands(h *host.Host, app *ui.App) {
	api := &control.API{Caller: h}
	mygo.Bind(
		control.Plugins{
			API:       api,
			Approvals: &control.PluginApprovals{},
			Owner:     host.CallerLabel,
			Window:    app.PluginApprovalWindow(),
		},
		control.Metadata{API: api},
		PluginFolders{},
	)
	if err := mygo.Protocol.Handle(control.PluginScheme, control.PluginClientHandler(h, host.CallerLabel)); err != nil {
		log.Printf("astrlink: unable to serve plugin interfaces: %v", err)
	}
}

// PluginFolders is the folder picker of the plugin install dialog.
type PluginFolders struct{}

// PickPluginFolder asks for the folder of a plugin project to install,
// starting at start when it is one. It returns nil when the operator
// canceled.
func (PluginFolders) PickPluginFolder(start *string) (*string, error) { return pickFolder(start) }
