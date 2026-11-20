// Command astrlink-desktop is the AstrLink desktop app: the React frontend in
// the system web view, with Core running as a child process it supervises.
//
// `mygo generate` builds and runs this program to write the TypeScript client
// (src/mygo.ts), and App.Run returns before the app starts. So main only binds
// services and declares events: everything with a side effect, such as reading
// preferences or starting Core, waits for App.WhenReady.
package main

import (
	"context"
	"log"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/sigstack"
)

func main() {
	h := host.New()
	// One file per area binds its services, so each can change on its own.
	registerCoreCommands(h)
	app := registerWindowCommands(h)
	registerPluginCommands(h, app)
	registerClientCommands(h)
	registerAgentCommands(h)
	registerReleaseCommands(h)
	// The web view starts with the app; its signal handlers must not
	// overflow goroutine stacks.
	mygo.App.WhenReady(func() { sigstack.Keep(context.Background()) })
	mygo.App.WhenReady(h.Start)
	if err := sigstack.Run(mygo.App.Run); err != nil {
		log.Fatal(err)
	}
}
