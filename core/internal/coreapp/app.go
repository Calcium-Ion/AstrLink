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
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

const (
	DefaultInferenceListen = "127.0.0.1:18317"
	// NetworkInferenceHost is the inference host that answers every interface
	// of this machine. Any other inference host must be the IPv4 loopback.
	NetworkInferenceHost = "0.0.0.0"
	DefaultControlListen = "127.0.0.1:0"
	inferenceReadTimeout = 60 * time.Second
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
	// NewInferenceHandler builds the production gate. address is the
	// 127.0.0.1 authority programs on this machine use; networkExposed
	// reports that the same port also answers other machines.
	NewInferenceHandler func(address string, networkExposed bool) (http.Handler, error)
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
	if err := validateInferenceAddress(config.InferenceListen); err != nil {
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

// NetworkExposed reports whether the inference plane answers every interface
// instead of only this machine.
func (config Config) NetworkExposed() bool {
	host, _, err := net.SplitHostPort(config.InferenceListen)
	return err == nil && host == NetworkInferenceHost
}

// validateInferenceAddress accepts the IPv4 loopback or the every-interface
// host. The control plane never leaves loopback; see validateLoopbackAddress.
func validateInferenceAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split host and port: %w", err)
	}
	if host != "127.0.0.1" && host != NetworkInferenceHost {
		return fmt.Errorf("address %q must use 127.0.0.1 or %s", address, NetworkInferenceHost)
	}
	return validatePort(address, port)
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split host and port: %w", err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("address %q must use 127.0.0.1", address)
	}
	return validatePort(address, port)
}

func validatePort(address, port string) error {
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort > 65535 {
		return fmt.Errorf("address %q has an invalid port", address)
	}
	return nil
}

// inferenceBindAddress maps the every-interface host to the wildcard bind,
// which Go serves dual-stack where IPv6 is available, so `localhost` and the
// machine's IPv6 addresses answer as well.
func inferenceBindAddress(address string, exposed bool) string {
	_, port, err := net.SplitHostPort(address)
	if !exposed || err != nil {
		return address
	}
	return ":" + port
}

// exposedLoopbackAuthority returns the 127.0.0.1 authority and the client URL
// for an every-interface listener. `localhost` is advertised only when the
// bind is dual-stack, so a resolver answering ::1 first still reaches this Core.
func exposedLoopbackAuthority(bound net.Addr) (authority, clientURL string) {
	host, port, err := net.SplitHostPort(bound.String())
	if err != nil {
		return bound.String(), "http://" + bound.String()
	}
	authority = net.JoinHostPort("127.0.0.1", port)
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() && ip.To4() == nil {
		return authority, "http://localhost:" + port
	}
	return authority, "http://" + authority
}

// Run binds both planes, emits exactly one ready event to readyWriter, and
// blocks until context cancellation or a server failure.
func Run(ctx context.Context, config Config, readyWriter io.Writer) error {
	return runWithDependencies(ctx, config, readyWriter, net.Listen, net.Listen, Dependencies{})
}

// RunWithDependencies preserves the process/listener contract while allowing
// later milestones to install a configured inference handler. Run remains the
// production M1 entry point and fails closed through ingress.New().
func RunWithDependencies(ctx context.Context, config Config, readyWriter io.Writer, dependencies Dependencies) error {
	return runWithDependencies(ctx, config, readyWriter, net.Listen, net.Listen, dependencies)
}

type listenFunc func(network, address string) (net.Listener, error)

// exposedPortProbe checks an every-interface port before it is bound; tests
// that script every bind replace it.
var exposedPortProbe = probeExposedPort

// run serves IPv4 only; tests that script every bind use it.
func run(ctx context.Context, config Config, readyWriter io.Writer, listen listenFunc) error {
	return runWithDependencies(ctx, config, readyWriter, listen, nil, Dependencies{})
}

// listenIPv6 binds the inference port on [::1] as well, so clients can use
// `localhost`, which most resolvers answer with ::1 first. Nil skips it. An
// every-interface bind is dual-stack already and does not use it.
func runWithDependencies(
	ctx context.Context,
	config Config,
	readyWriter io.Writer,
	listen listenFunc,
	listenIPv6 listenFunc,
	dependencies Dependencies,
) error {
	if readyWriter == nil {
		return fmt.Errorf("ready writer is required")
	}
	if err := config.Validate(); err != nil {
		return err
	}

	exposed := config.NetworkExposed()
	if exposed {
		if _, port, splitErr := net.SplitHostPort(config.InferenceListen); splitErr == nil {
			if err := exposedPortProbe(port); err != nil {
				return fmt.Errorf("listen on inference plane: %w", err)
			}
		}
	}
	inferenceListener, err := listen("tcp", inferenceBindAddress(config.InferenceListen, exposed))
	// Other machines are configured with the saved port, so an exposed
	// listener never moves to a random one.
	if config.InferencePortFallback && !exposed && errors.Is(err, addressInUse) {
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
	// Programs on this machine always get a 127.0.0.1 authority; an exposed
	// listener answers it as well.
	inferenceAddress := inferenceListener.Addr().String()
	var inferenceIPv6 net.Listener
	var clientInferenceURL string
	if exposed {
		inferenceAddress, clientInferenceURL = exposedLoopbackAuthority(inferenceListener.Addr())
	} else {
		inferenceIPv6, clientInferenceURL = listenIPv6Loopback(listenIPv6, inferenceListener.Addr())
		if inferenceIPv6 != nil {
			defer inferenceIPv6.Close()
		}
	}

	controlListener, err := listen("tcp", config.ControlListen)
	if err != nil {
		return fmt.Errorf("listen on control plane: %w", err)
	}
	defer controlListener.Close()

	inferenceHandler := dependencies.InferenceHandler
	if dependencies.NewInferenceHandler != nil {
		inferenceHandler, err = dependencies.NewInferenceHandler(inferenceAddress, exposed)
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

	startRetentionSweep(requestContext, dependencies.RetentionSweep)

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

	planes := []plane{
		{name: "inference", server: inferenceServer, listener: inferenceListener},
		{name: "control", server: controlServer, listener: controlListener},
	}
	if config.ControlSocketPath != "" {
		socketPlane, closeSocket, err := controlSocketPlane(config.ControlSocketPath, controlHandler, baseContext)
		if err != nil {
			return err
		}
		defer closeSocket()
		planes = append(planes, socketPlane)
	}
	if inferenceIPv6 != nil {
		planes = append(planes, plane{name: "inference IPv6", server: inferenceServer, listener: inferenceIPv6})
	}
	running := startPlanes(planes)

	ready := contract.ReadyEvent{
		Event:                   "ready",
		CoreVersion:             config.Version.CoreVersion,
		ControlAPIVersion:       config.Version.ControlAPIVersion,
		ProtocolContractVersion: config.Version.ProtocolContractVersion,
		InferenceURL:            "http://" + inferenceAddress,
		ClientInferenceURL:      clientInferenceURL,
		ControlURL:              "http://" + controlListener.Addr().String(),
	}
	if err := ready.Validate(); err != nil {
		cancelRequests()
		return running.stop(fmt.Errorf("validate ready event: %w", err))
	}
	if err := json.NewEncoder(readyWriter).Encode(ready); err != nil {
		cancelRequests()
		return running.stop(fmt.Errorf("write ready event: %w", err))
	}
	return running.wait(ctx, cancelRequests)
}

// plane is one listener and the server answering it. Two planes may share
// a server, as the IPv4 and IPv6 inference listeners do.
type plane struct {
	name     string
	server   *http.Server
	listener net.Listener
}

// runningPlanes tracks the serving goroutines of one run.
type runningPlanes struct {
	servers []*http.Server
	group   sync.WaitGroup
	errors  chan error
}

func startPlanes(planes []plane) *runningPlanes {
	running := &runningPlanes{errors: make(chan error, len(planes))}
	for _, current := range planes {
		if !slices.Contains(running.servers, current.server) {
			running.servers = append(running.servers, current.server)
		}
	}
	running.group.Add(len(planes))
	for _, current := range planes {
		go func() {
			defer running.group.Done()
			if serveErr := current.server.Serve(current.listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				running.errors <- fmt.Errorf("serve %s plane: %w", current.name, serveErr)
			}
		}()
	}
	return running
}

// wait blocks until ctx ends or a plane fails, then shuts every plane down.
func (running *runningPlanes) wait(ctx context.Context, cancelRequests context.CancelFunc) error {
	var triggerErr error
	select {
	case <-ctx.Done():
	case triggerErr = <-running.errors:
	}
	cancelRequests()
	return running.stop(triggerErr)
}

func (running *runningPlanes) stop(primaryErr error) error {
	return shutdownAndCollect(running.servers, &running.group, running.errors, primaryErr)
}

// controlSocketPlane serves the same-uid control socket. closeSocket removes
// the socket file; call it only after the plane stopped.
func controlSocketPlane(
	path string,
	controlHandler http.Handler,
	baseContext func(net.Listener) context.Context,
) (socketPlane plane, closeSocket func(), err error) {
	listener, err := listenControlSocket(path)
	if err != nil {
		return plane{}, nil, fmt.Errorf("listen on local control socket: %w", err)
	}
	return plane{
			name: "control socket",
			server: &http.Server{
				Handler:           controlapi.LocalSocketHandler(controlHandler),
				ReadHeaderTimeout: 5 * time.Second,
				BaseContext:       baseContext,
			},
			listener: listener,
		}, func() {
			_ = listener.Close()
			_ = os.Remove(path)
		}, nil
}

// startRetentionSweep runs sweep once now and then hourly. Nil disables it.
func startRetentionSweep(ctx context.Context, sweep func(context.Context) error) {
	if sweep == nil {
		return
	}
	if err := sweep(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// Best-effort at startup; continue serving even if the first sweep fails.
		_ = err
	}
	go runRetentionSweepLoop(ctx, sweep)
}

// listenIPv6Loopback binds the inference port on ::1 and returns the URL
// clients should use. It is best-effort: when ::1 is unavailable or another
// process holds the port there, clients keep 127.0.0.1 rather than letting
// `localhost` reach someone else's listener.
func listenIPv6Loopback(listen listenFunc, inference net.Addr) (net.Listener, string) {
	ipv4URL := "http://" + inference.String()
	_, port, err := net.SplitHostPort(inference.String())
	if listen == nil || err != nil {
		return nil, ipv4URL
	}
	listener, err := listen("tcp", net.JoinHostPort("::1", port))
	if err != nil {
		return nil, ipv4URL
	}
	return listener, "http://localhost:" + port
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
