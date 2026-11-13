// Command astrlink gives coding agents read-only, shell-friendly access to
// the local AstrLink gateway's request records through the Control API, and
// plugin authors the tools of astrlink plugin.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/QuantumNous/astrlink/core/internal/agentcli"
	"github.com/QuantumNous/astrlink/core/internal/plugincli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var code int
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		code = plugincli.Run(ctx, os.Args[2:], plugincli.Environment{
			Stdout: os.Stdout, Stderr: os.Stderr,
			Dial: func() (plugincli.Gateway, error) { return agentcli.Dial(agentcli.DialOptions{}) },
		})
	} else {
		code = agentcli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	}
	stop()
	os.Exit(code)
}
