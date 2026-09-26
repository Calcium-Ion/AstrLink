// Package coreapp owns listener lifecycle and the stdout sidecar handshake.
package coreapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

const (
	DefaultInferenceListen = "127.0.0.1:18317"
	DefaultControlListen   = "127.0.0.1:0"
	inferenceReadTimeout   = 60 * time.Second
)

type Config struct {
	InferenceListen       string
	InferencePortFallback bool
	ControlListen         string
	ControlSocketPath     string
	Version               contract.VersionResponse
}

type Dependencies struct {
	InferenceHandler http.Handler
	// NewInferenceHandler builds the production Host gate from the bound address.
	NewInferenceHandler func(address string) (http.Handler, error)
	ControlHandler      http.Handler
	// RetentionSweep deletes expired request records and audit blobs.
	// Nil disables the startup/hourly retention loop (headless mode).
	RetentionSweep func(context.Context) error
}

func DefaultConfig(coreVersion, buildCommit string) Config {
	return Config{
		InferenceListen: DefaultInferenceListen,
		ControlListen:   DefaultControlListen,
		Version:         contract.DefaultVersionResponse(coreVersion, buildCommit),
	}
}

func (config Config) Validate() error {
	if err := validateLoopbackAddress(config.InferenceListen); err != nil {
		return fmt.Errorf("inference listen address: %w", err)
	}
	if err := validateLoopbackAddress(config.ControlListen); err != nil {
		return fmt.Errorf("control listen address: %w", err)
	}
	if err := config.Version.Validate(); err != nil {
		return fmt.Errorf("version handshake: %w", err)
	}
	return nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split host and port: %w", err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("address %q must use 127.0.0.1", address)
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort > 65535 {
		return fmt.Errorf("address %q has an invalid port", address)
	}
	return nil
}

// Run binds both planes, emits exactly one ready event to readyWriter, and
// blocks until context cancellation or a server failure.
func Run(ctx context.Context, config Config, readyWriter io.Writer) error {
	return run(ctx, config, readyWriter, net.Listen)
}

// RunWithDependencies preserves the process/listener contract while allowing
// later milestones to install a configured inference handler. Run remains the
// production M1 entry point and fails closed through ingress.New().
func RunWithDependencies(ctx context.Context, config Config, readyWriter io.Writer, dependencies Dependencies) error {
	return runWithDependencies(ctx, config, readyWriter, net.Listen, dependencies)
}

type listenFunc func(network, address string) (net.Listener, error)

func run(ctx context.Context, config Config, readyWriter io.Writer, listen listenFunc) error {
	return runWithDependencies(ctx, config, readyWriter, listen, Dependencies{})
}

func runWithDependencies(
	ctx context.Context,
	config Config,
	readyWriter io.Writer,
	listen listenFunc,
	dependencies Dependencies,
) error {
	if readyWriter == nil {
		return fmt.Errorf("ready writer is required")
	}
	if err := config.Validate(); err != nil {
		return err
	}

	inferenceListener, err := listen("tcp", config.InferenceListen)
	if config.InferencePortFallback && errors.Is(err, addressInUse) {
		// Bind directly instead of probing and releasing a port: the listener
		// remains owned until shutdown, so another process cannot claim it.
		inferenceListener, err = listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("inference address %s is occupied; listen on fallback port: %w", config.InferenceListen, err)
		}
	}
	if err != nil {
		return fmt.Errorf("listen on inference plane: %w", err)
	}
	defer inferenceListener.Close()

	controlListener, err := listen("tcp", config.ControlListen)
	if err != nil {
		return fmt.Errorf("listen on control plane: %w", err)
	}
	defer controlListener.Close()

	inferenceHandler := dependencies.InferenceHandler
	if dependencies.NewInferenceHandler != nil {
		inferenceHandler, err = dependencies.NewInferenceHandler(inferenceListener.Addr().String())
		if err != nil {
			return fmt.Errorf("configure production inference gate: %w", err)
		}
	}
	if inferenceHandler == nil {
		inferenceHandler = ingress.New()
	}
	controlHandler := dependencies.ControlHandler
	if controlHandler == nil {
		controlHandler = controlapi.New(config.Version)
	}
	requestContext, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	baseContext := func(net.Listener) context.Context { return requestContext }

	if dependencies.RetentionSweep != nil {
		if err := dependencies.RetentionSweep(requestContext); err != nil && !errors.Is(err, context.Canceled) {
			// Best-effort at startup; continue serving even if the first sweep fails.
			_ = err
		}
		go runRetentionSweepLoop(requestContext, dependencies.RetentionSweep)
	}

	inferenceServer := &http.Server{
		Handler:           inferenceHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       inferenceReadTimeout,
		BaseContext:       baseContext,
	}
	controlServer := &http.Server{
		Handler:           controlHandler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       baseContext,
	}

	servers := []*http.Server{inferenceServer, controlServer}
	var controlSocket net.Listener
	if config.ControlSocketPath != "" {
		controlSocket, err = listenControlSocket(config.ControlSocketPath)
		if err != nil {
			return fmt.Errorf("listen on local control socket: %w", err)
		}
		defer func() {
			_ = controlSocket.Close()
			_ = os.Remove(config.ControlSocketPath)
		}()
		servers = append(servers, &http.Server{
			Handler:           controlapi.LocalSocketHandler(controlHandler),
			ReadHeaderTimeout: 5 * time.Second,
			BaseContext:       baseContext,
		})
	}

	serverErrors := make(chan error, 3)
	var serveGroup sync.WaitGroup
	serve := func(name string, server *http.Server, listener net.Listener) {
		defer serveGroup.Done()
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("serve %s plane: %w", name, serveErr)
		}
	}
	serveGroup.Add(2)
	go serve("inference", inferenceServer, inferenceListener)
	go serve("control", controlServer, controlListener)
	if controlSocket != nil {
		serveGroup.Add(1)
		go serve("control socket", servers[2], controlSocket)
	}

	ready := contract.ReadyEvent{
		Event:                   "ready",
		CoreVersion:             config.Version.CoreVersion,
		ControlAPIVersion:       config.Version.ControlAPIVersion,
		ProtocolContractVersion: config.Version.ProtocolContractVersion,
		InferenceURL:            "http://" + inferenceListener.Addr().String(),
		ControlURL:              "http://" + controlListener.Addr().String(),
	}
	if err := ready.Validate(); err != nil {
		cancelRequests()
		return shutdownAndCollect(
			servers,
			&serveGroup,
			serverErrors,
			fmt.Errorf("validate ready event: %w", err),
		)
	}
	if err := json.NewEncoder(readyWriter).Encode(ready); err != nil {
		cancelRequests()
		return shutdownAndCollect(
			servers,
			&serveGroup,
			serverErrors,
			fmt.Errorf("write ready event: %w", err),
		)
	}

	var triggerErr error
	select {
	case <-ctx.Done():
	case triggerErr = <-serverErrors:
	}
	cancelRequests()
	return shutdownAndCollect(servers, &serveGroup, serverErrors, triggerErr)
}

const retentionSweepInterval = time.Hour

func runRetentionSweepLoop(ctx context.Context, sweep func(context.Context) error) {
	ticker := time.NewTicker(retentionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = sweep(ctx)
		}
	}
}

func shutdownAndCollect(
	servers []*http.Server,
	serveGroup *sync.WaitGroup,
	serverErrors chan error,
	primaryErr error,
) error {
	result := errors.Join(primaryErr, shutdownServers(servers...))
	serveGroup.Wait()
	close(serverErrors)
	for serveErr := range serverErrors {
		result = errors.Join(result, serveErr)
	}
	return result
}

func shutdownServers(servers ...*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result error
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
