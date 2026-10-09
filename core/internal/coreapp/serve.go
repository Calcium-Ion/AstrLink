package coreapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// exposedIdleTimeout closes idle keep-alive connections from other machines.
const exposedIdleTimeout = 2 * time.Minute

// ServeConfig binds the server edition: one address that other machines
// reach for both the inference plane and the web console, and the same-uid
// control socket for the CLI. Nothing here is limited to loopback and no
// ready event is written; the desktop sidecar keeps every check in Config.
type ServeConfig struct {
	Listen            string
	ControlSocketPath string
}

// ConsoleHandler answers the requests it owns on the shared listener.
type ConsoleHandler interface {
	http.Handler
	Owns(*http.Request) bool
}

type ServeDependencies struct {
	InferenceHandler http.Handler
	Console          ConsoleHandler
	// ControlHandler answers the control socket.
	ControlHandler http.Handler
	// RetentionSweep deletes expired request records and audit blobs.
	RetentionSweep func(context.Context) error
	// Listening is called once every plane serves.
	Listening func(net.Addr)
}

func (config ServeConfig) Validate() error {
	if err := validateListenAddress(config.Listen); err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	return nil
}

func validateListenAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split host and port: %w", err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("address %q has an invalid port", address)
	}
	return nil
}

// RunServe binds the server edition's planes and blocks until context
// cancellation or a server failure.
func RunServe(ctx context.Context, config ServeConfig, dependencies ServeDependencies) error {
	return runServe(ctx, config, dependencies, net.Listen)
}

func runServe(ctx context.Context, config ServeConfig, dependencies ServeDependencies, listen listenFunc) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if dependencies.InferenceHandler == nil || dependencies.Console == nil || dependencies.ControlHandler == nil {
		return errors.New("inference, console and control handlers are required")
	}
	listener, err := listen("tcp", config.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	requestContext, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	baseContext := func(net.Listener) context.Context { return requestContext }

	startRetentionSweep(requestContext, dependencies.RetentionSweep)

	// The server keeps the inference plane's timeouts, which never cut a
	// streaming response; the console bounds its own request bodies.
	planes := []plane{{name: "inference and console", listener: listener, server: &http.Server{
		Handler:           sharedHandler(dependencies.Console, dependencies.InferenceHandler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       inferenceReadTimeout,
		IdleTimeout:       exposedIdleTimeout,
		BaseContext:       baseContext,
	}}}
	if config.ControlSocketPath != "" {
		socketPlane, closeSocket, err := controlSocketPlane(config.ControlSocketPath, dependencies.ControlHandler, baseContext)
		if err != nil {
			return err
		}
		defer closeSocket()
		planes = append(planes, socketPlane)
	}
	running := startPlanes(planes)
	if dependencies.Listening != nil {
		dependencies.Listening(listener.Addr())
	}
	return running.wait(ctx, cancelRequests)
}

// sharedHandler gives the console the requests it owns and the inference
// plane everything else, so one address and one port mapping serve both.
// Each side keeps its own authentication: inference never reads the session
// cookie and the console never accepts an access token.
func sharedHandler(console ConsoleHandler, inference http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if console.Owns(request) {
			console.ServeHTTP(writer, request)
			return
		}
		inference.ServeHTTP(writer, request)
	})
}
