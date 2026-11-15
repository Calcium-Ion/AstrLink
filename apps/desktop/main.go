// Command astrlink-desktop is the AstrLink desktop app: the React frontend in
// the system web view, with Core running as a child process it supervises.
//
// `mygo generate` builds and runs this program to write the TypeScript client
// (src/mygo.ts), and App.Run returns before the app starts. So main only binds
// services and declares events: everything with a side effect, such as reading
// preferences or starting Core, waits for App.WhenReady.
package main

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
)

func main() {
	h := host.New()
	// One file per area binds its services, so each can change on its own.
	registerCoreCommands(h)
	registerWindowCommands(h)
	registerClientCommands(h)
	registerAgentCommands(h)
	registerReleaseCommands(h)
	mygo.App.WhenReady(h.Start)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
