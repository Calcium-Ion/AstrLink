package coreapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDefaultConfigUsesFixedInferenceAndEphemeralControlPorts(t *testing.T) {
	config := DefaultConfig("", "")
	if config.InferenceListen != "127.0.0.1:18317" {
		t.Fatalf("inference listen = %q", config.InferenceListen)
	}
	if config.ControlListen != "127.0.0.1:0" {
		t.Fatalf("control listen = %q", config.ControlListen)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestRunReturnsServeFailure(t *testing.T) {
	forcedErr := errors.New("forced listener failure")
	failing := newControlledFailingListener("127.0.0.1:8317", forcedErr)
	control := newBlockingListener("127.0.0.1:54321")
	readyWriter := newRecordingWriter()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"

	runErrors := make(chan error, 1)
	go func() {
		listeners := []net.Listener{failing, control}
		index := 0
		runErrors <- run(context.Background(), config, readyWriter, func(_, _ string) (net.Listener, error) {
			listener := listeners[index]
			index++
			return listener, nil
		})
	}()

	waitForReadyWrite(t, readyWriter)
	close(failing.fail)
	err := waitForRunError(t, runErrors)
	if !errors.Is(err, forcedErr) || !strings.Contains(err.Error(), "serve inference plane") {
		t.Fatalf("Run error = %v, want inference Serve failure", err)
	}
}

func TestRunDoesNotLoseServeFailureConcurrentWithCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forcedErr := &cancelOnFormatError{cancel: cancel}
	failing := newControlledFailingListener("127.0.0.1:8317", forcedErr)
	control := newBlockingListener("127.0.0.1:54321")
	readyWriter := newRecordingWriter()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"

	runErrors := make(chan error, 1)
	go func() {
		listeners := []net.Listener{failing, control}
		index := 0
		runErrors <- run(ctx, config, readyWriter, func(_, _ string) (net.Listener, error) {
			listener := listeners[index]
			index++
			return listener, nil
		})
	}()

	waitForReadyWrite(t, readyWriter)
	close(failing.fail)
	err := waitForRunError(t, runErrors)
	if err == nil || !strings.Contains(err.Error(), forcedErr.message()) {
		t.Fatalf("Run error = %v, want concurrent Serve failure", err)
	}
}

func TestShutdownAndCollectDrainsEveryServeError(t *testing.T) {
	firstErr := errors.New("first serve error")
	secondErr := errors.New("second serve error")
	serverErrors := make(chan error, 2)
	serverErrors <- firstErr
	serverErrors <- secondErr
	var serveGroup sync.WaitGroup

	err := shutdownAndCollect(nil, &serveGroup, serverErrors, nil)
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("shutdownAndCollect error = %v, want both Serve errors", err)
	}
}

func TestRunValidatesReadyEventBeforeWriting(t *testing.T) {
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"
	listeners := []net.Listener{
		newBlockingListener("127.0.0.1:0"),
		newBlockingListener("127.0.0.1:54321"),
	}
	listenerIndex := 0
	var ready bytes.Buffer

	err := run(context.Background(), config, &ready, func(_, _ string) (net.Listener, error) {
		listener := listeners[listenerIndex]
		listenerIndex++
		return listener, nil
	})
	if err == nil || !strings.Contains(err.Error(), "validate ready event") {
		t.Fatalf("Run error = %v, want ready validation error", err)
	}
	if ready.Len() != 0 {
		t.Fatalf("ready writer received %q before validation", ready.String())
	}
}

func TestConfigRejectsNonLoopbackListeners(t *testing.T) {
	for _, address := range []string{"192.0.2.1:8317", ":8317", "localhost:8317", "[::1]:8317", "[::]:8317"} {
		t.Run(address, func(t *testing.T) {
			config := DefaultConfig("", "")
			config.InferenceListen = address
			if err := config.Validate(); err == nil || (!strings.Contains(err.Error(), "127.0.0.1") && !strings.Contains(err.Error(), "split host")) {
				t.Fatalf("Validate error = %v, want fixed IPv4 loopback error", err)
			}
		})
	}
}

func TestRunEmitsOneReadyEventAndServesSeparatePlanes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"

	runErrors := make(chan error, 1)
	listeners := []*blockingListener{
		newBlockingListener("127.0.0.1:8317"),
		newBlockingListener("127.0.0.1:54321"),
	}
	listenerIndex := 0
	go func() {
		runErrors <- run(ctx, config, writer, func(_, _ string) (net.Listener, error) {
			listener := listeners[listenerIndex]
			listenerIndex++
			return listener, nil
		})
	}()

	decoder := json.NewDecoder(reader)
	var ready contract.ReadyEvent
	if err := decoder.Decode(&ready); err != nil {
		cancel()
		t.Fatalf("decode ready event: %v", err)
	}
	if ready.Event != "ready" || ready.CoreVersion != "0.1.0-test" || ready.ControlAPIVersion != "v1" || ready.ProtocolContractVersion != "v1" {
		cancel()
		t.Fatalf("unexpected ready event: %#v", ready)
	}
	if ready.InferenceURL == ready.ControlURL {
		cancel()
		t.Fatalf("planes share URL %q", ready.InferenceURL)
	}
	assertLoopbackURL(t, ready.InferenceURL)
	assertLoopbackURL(t, ready.ControlURL)

	cancel()
	err := <-runErrors
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second stdout value error = %v, value=%#v; want EOF", err, extra)
	}
}

func TestRunServesIPv6LoopbackAndAnnouncesLocalhostForClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	listeners := []net.Listener{newBlockingListener("127.0.0.1:8317"), newBlockingListener("127.0.0.1:54321")}
	ipv6 := newConnectionListener("[::1]:8317", serverConn)
	reader, writer := io.Pipe()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runWithDependencies(ctx, config, writer, func(_, _ string) (net.Listener, error) {
			listener := listeners[0]
			listeners = listeners[1:]
			return listener, nil
		}, func(network, address string) (net.Listener, error) {
			if network != "tcp" || address != "[::1]:8317" {
				t.Errorf("IPv6 bind = %s %s", network, address)
			}
			return ipv6, nil
		}, Dependencies{InferenceHandler: handler})
	}()

	var ready contract.ReadyEvent
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		t.Fatalf("decode ready event: %v", err)
	}
	if ready.InferenceURL != "http://127.0.0.1:8317" || ready.ClientInferenceURL != "http://localhost:8317" {
		t.Fatalf("ready URLs = %q / %q", ready.InferenceURL, ready.ClientInferenceURL)
	}
	if _, err := io.WriteString(clientConn, "GET /v1/models HTTP/1.1\r\nHost: localhost:8317\r\n\r\n"); err != nil {
		t.Fatalf("write IPv6 request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(clientConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read IPv6 response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("IPv6 status = %d", response.StatusCode)
	}

	cancel()
	_ = clientConn.Close()
	if err := waitForRunError(t, runErrors); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	select {
	case <-ipv6.closed:
	default:
		t.Fatal("IPv6 listener left open after shutdown")
	}
}

func TestRunKeepsIPv4ClientURLWhenIPv6LoopbackIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listeners := []net.Listener{newBlockingListener("127.0.0.1:8317"), newBlockingListener("127.0.0.1:54321")}
	reader, writer := io.Pipe()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runWithDependencies(ctx, config, writer, func(_, _ string) (net.Listener, error) {
			listener := listeners[0]
			listeners = listeners[1:]
			return listener, nil
		}, func(_, _ string) (net.Listener, error) {
			return nil, errors.New("address family not supported")
		}, Dependencies{})
	}()

	var ready contract.ReadyEvent
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		cancel()
		t.Fatalf("decode ready event: %v", err)
	}
	cancel()
	if err := waitForRunError(t, runErrors); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if ready.ClientInferenceURL != ready.InferenceURL {
		t.Fatalf("client URL = %q, want %q", ready.ClientInferenceURL, ready.InferenceURL)
	}
}

func TestRunCancellationCancelsActiveInferenceRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	inference := newConnectionListener("127.0.0.1:8317", serverConn)
	control := newBlockingListener("127.0.0.1:54321")
	readyWriter := newRecordingWriter()
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(writer).Flush()
		<-request.Context().Done()
		close(requestCancelled)
	})
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "127.0.0.1:0"
	config.ControlListen = "127.0.0.1:0"
	runErrors := make(chan error, 1)
	go func() {
		listeners := []net.Listener{inference, control}
		index := 0
		runErrors <- runWithDependencies(ctx, config, readyWriter, func(_, _ string) (net.Listener, error) {
			listener := listeners[index]
			index++
			return listener, nil
		}, nil, Dependencies{InferenceHandler: handler})
	}()

	waitForReadyWrite(t, readyWriter)
	if _, err := io.WriteString(clientConn, "GET /v1/responses HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"); err != nil {
		t.Fatalf("write inference request: %v", err)
	}
	select {
	case <-requestStarted:
	case err := <-runErrors:
		t.Fatalf("Run stopped before inference handler started: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(clientConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read inference response: %v", err)
	}
	defer response.Body.Close()

	cancel()
	<-requestCancelled
	_ = response.Body.Close()
	_ = clientConn.Close()
	if err := waitForRunError(t, runErrors); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

type blockingListener struct {
	address net.Addr
	closed  chan struct{}
	once    sync.Once
}

type controlledFailingListener struct {
	address net.Addr
	fail    chan struct{}
	closed  chan struct{}
	err     error
	once    sync.Once
}

type connectionListener struct {
	address     net.Addr
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func newConnectionListener(address string, connection net.Conn) *connectionListener {
	connections := make(chan net.Conn, 1)
	connections <- connection
	return &connectionListener{
		address: fakeAddress(address), connections: connections, closed: make(chan struct{}),
	}
}

func (listener *connectionListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *connectionListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *connectionListener) Addr() net.Addr {
	return listener.address
}

func newControlledFailingListener(address string, err error) *controlledFailingListener {
	return &controlledFailingListener{
		address: fakeAddress(address),
		fail:    make(chan struct{}),
		closed:  make(chan struct{}),
		err:     err,
	}
}

func (listener *controlledFailingListener) Accept() (net.Conn, error) {
	select {
	case <-listener.fail:
		return nil, listener.err
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *controlledFailingListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *controlledFailingListener) Addr() net.Addr {
	return listener.address
}

type recordingWriter struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
	mu     sync.Mutex
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{ready: make(chan struct{})}
}

func (writer *recordingWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	written, err := writer.buffer.Write(value)
	writer.mu.Unlock()
	writer.once.Do(func() { close(writer.ready) })
	return written, err
}

type cancelOnFormatError struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (err *cancelOnFormatError) message() string {
	return "forced concurrent listener failure"
}

func (err *cancelOnFormatError) Error() string {
	err.once.Do(err.cancel)
	return err.message()
}

func waitForReadyWrite(t *testing.T, writer *recordingWriter) {
	t.Helper()
	<-writer.ready
}

func waitForRunError(t *testing.T, runErrors <-chan error) error {
	t.Helper()
	return <-runErrors
}

func newBlockingListener(address string) *blockingListener {
	return &blockingListener{
		address: fakeAddress(address),
		closed:  make(chan struct{}),
	}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *blockingListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *blockingListener) Addr() net.Addr {
	return listener.address
}

type fakeAddress string

func (fakeAddress) Network() string { return "tcp" }
func (address fakeAddress) String() string {
	return string(address)
}

func assertLoopbackURL(t *testing.T, value string) {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse URL %q: %v", value, err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" {
		t.Fatalf("URL is not an IPv4 loopback HTTP URL: %q", value)
	}
}

func TestConfigAcceptsEveryInterfaceInferenceListener(t *testing.T) {
	config := DefaultConfig("", "")
	config.InferenceListen = "0.0.0.0:8317"
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want every-interface inference listener accepted", err)
	}
	if !config.NetworkExposed() {
		t.Fatal("NetworkExposed = false for 0.0.0.0")
	}
	if DefaultConfig("", "").NetworkExposed() {
		t.Fatal("NetworkExposed = true for the loopback default")
	}
	config.ControlListen = "0.0.0.0:0"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "control listen address") {
		t.Fatalf("Validate error = %v, want the control plane kept on loopback", err)
	}
}

func TestExposedRunBindsEveryInterfaceAndAdvertisesLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Every bind is scripted here; the real probe would touch port 8317.
	probed := 0
	previousProbe := exposedPortProbe
	exposedPortProbe = func(port string) error {
		probed++
		if port != "8317" {
			t.Errorf("probed port %q", port)
		}
		return nil
	}
	t.Cleanup(func() { exposedPortProbe = previousProbe })
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = "0.0.0.0:8317"
	config.InferencePortFallback = true
	config.ControlListen = "127.0.0.1:0"
	writer := newRecordingWriter()

	var binds []string
	ipv6Binds := 0
	listeners := []net.Listener{newBlockingListener("[::]:8317"), newBlockingListener("127.0.0.1:54321")}
	var factoryAddress string
	var factoryExposed bool
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runWithDependencies(ctx, config, writer, func(_, address string) (net.Listener, error) {
			binds = append(binds, address)
			if address == ":8317" && len(binds) == 1 {
				return nil, addressInUse
			}
			listener := listeners[0]
			listeners = listeners[1:]
			return listener, nil
		}, func(_, _ string) (net.Listener, error) {
			ipv6Binds++
			return newBlockingListener("[::1]:8317"), nil
		}, Dependencies{NewInferenceHandler: func(address string, networkExposed bool) (http.Handler, error) {
			factoryAddress, factoryExposed = address, networkExposed
			return http.NotFoundHandler(), nil
		}})
	}()

	err := waitForRunError(t, runErrors)
	if err == nil || !strings.Contains(err.Error(), "listen on inference plane") || !errors.Is(err, addressInUse) {
		t.Fatalf("Run error = %v, want the occupied exposed port reported without a fallback bind", err)
	}
	if len(binds) != 1 || binds[0] != ":8317" {
		t.Fatalf("binds = %v, want one dual-stack wildcard bind and no 127.0.0.1:0 fallback", binds)
	}

	binds = nil
	listeners = []net.Listener{newBlockingListener("[::]:8317"), newBlockingListener("127.0.0.1:54321")}
	go func() {
		runErrors <- runWithDependencies(ctx, config, writer, func(_, address string) (net.Listener, error) {
			binds = append(binds, address)
			listener := listeners[0]
			listeners = listeners[1:]
			return listener, nil
		}, func(_, _ string) (net.Listener, error) {
			ipv6Binds++
			return newBlockingListener("[::1]:8317"), nil
		}, Dependencies{NewInferenceHandler: func(address string, networkExposed bool) (http.Handler, error) {
			factoryAddress, factoryExposed = address, networkExposed
			return http.NotFoundHandler(), nil
		}})
	}()
	waitForReadyWrite(t, writer)
	cancel()
	if err := waitForRunError(t, runErrors); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(binds) != 2 || binds[0] != ":8317" || binds[1] != "127.0.0.1:0" {
		t.Fatalf("binds = %v, want the wildcard inference bind then the loopback control bind", binds)
	}
	if ipv6Binds != 0 {
		t.Fatalf("[::1] binds = %d, want none for a dual-stack listener", ipv6Binds)
	}
	if factoryAddress != "127.0.0.1:8317" || !factoryExposed {
		t.Fatalf("factory got (%q, %v), want the loopback authority and networkExposed", factoryAddress, factoryExposed)
	}
	if probed != 2 {
		t.Fatalf("probe ran %d times, want once per exposed run", probed)
	}
	var ready contract.ReadyEvent
	if err := json.Unmarshal(bytes.TrimSpace(writer.buffer.Bytes()), &ready); err != nil {
		t.Fatalf("decode ready event: %v (%q)", err, writer.buffer.String())
	}
	if ready.InferenceURL != "http://127.0.0.1:8317" || ready.ClientInferenceURL != "http://localhost:8317" {
		t.Fatalf("ready URLs = %q / %q", ready.InferenceURL, ready.ClientInferenceURL)
	}
}

func TestExposedLoopbackAuthorityKeeps127WhenIPv6IsUnavailable(t *testing.T) {
	authority, clientURL := exposedLoopbackAuthority(fakeAddress("0.0.0.0:8317"))
	if authority != "127.0.0.1:8317" || clientURL != "http://127.0.0.1:8317" {
		t.Fatalf("IPv4-only wildcard = %q / %q", authority, clientURL)
	}
	authority, clientURL = exposedLoopbackAuthority(fakeAddress("[::]:8317"))
	if authority != "127.0.0.1:8317" || clientURL != "http://localhost:8317" {
		t.Fatalf("dual-stack wildcard = %q / %q", authority, clientURL)
	}
	if got := inferenceBindAddress("0.0.0.0:8317", true); got != ":8317" {
		t.Fatalf("exposed bind = %q", got)
	}
	if got := inferenceBindAddress("127.0.0.1:8317", false); got != "127.0.0.1:8317" {
		t.Fatalf("loopback bind = %q", got)
	}
}
