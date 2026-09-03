package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/QuantumNous/astrlink/core/internal/agentmcp"
)

func main() {
	logger := log.New(os.Stderr, "astrlink-mcp: ", log.LstdFlags)
	socket := flag.String("socket", "", "local control socket path (preferred; no token)")
	controlURL := flag.String("control-url", "", "loopback control URL for tests or Windows session fallback")
	controlToken := flag.String("control-token", "", "control token used only with --control-url")
	session := flag.String("session", "", "path to control-session.json")
	flag.CommandLine.SetOutput(os.Stderr)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := agentmcp.ServeStdio(ctx, &agentmcp.Server{
		Options: agentmcp.DialOptions{
			Socket:       *socket,
			ControlURL:   *controlURL,
			ControlToken: *controlToken,
			SessionPath:  *session,
		},
	}, os.Stdin, os.Stdout)
	if err != nil && err != context.Canceled {
		logger.Printf("%v", err)
		os.Exit(1)
	}
}
