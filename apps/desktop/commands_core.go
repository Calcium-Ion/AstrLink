package main

import (
	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
)

// registerCoreCommands binds the services of Core's lifecycle, preferences,
// raw content and the control API commands
// (src/native-host/commands-core.ts).
func registerCoreCommands(h *host.Host) {
	api := &control.API{Caller: h}
	core, preferences, shell := h.Services()
	mygo.Bind(
		core,
		preferences,
		shell,
		control.Services{API: api, Tests: h, Owner: host.CallerLabel, OpenURL: h.OpenURL},
		control.Pricing{API: api},
		control.Sync{API: api},
		control.Records{API: api, Changed: h.RecordsChanged},
		control.Routing{API: api},
		control.Audit{API: api},
		control.Tokens{API: api, WriteClipboard: h.WriteClipboard},
		control.Usage{API: api},
		control.Privacy{API: api, PolicyChanged: h.PrivacyPolicyChanged, ReleasesRead: h.PrivacyReleasesRead},
		h.Raw(),
	)
}
