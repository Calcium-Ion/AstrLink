package coreapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDefaultConfigUsesFixedInferenceAndEphemeralControlPorts(t *testing.T) {
	config := DefaultConfig("", "")
	if config.InferenceListen != "127.0.0.1:8317" {
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
	for _, address := range []string{"0.0.0.0:8317", "192.0.2.1:8317", ":8317", "localhost:8317", "[::1]:8317"} {
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
	select {
	case err := <-runErrors:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second stdout value error = %v, value=%#v; want EOF", err, extra)
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
		}, Dependencies{InferenceHandler: handler})
	}()

	waitForReadyWrite(t, readyWriter)
	if _, err := io.WriteString(clientConn, "GET /v1/responses HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"); err != nil {
		t.Fatalf("write inference request: %v", err)
	}
	select {
	case <-requestStarted:
	case err := <-runErrors:
		t.Fatalf("Run stopped before inference handler started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("inference handler did not start")
	}
	response, err := http.ReadResponse(bufio.NewReader(clientConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read inference response: %v", err)
	}
	defer response.Body.Close()

	cancel()
	select {
	case <-requestCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("active inference request context survived Core cancellation")
	}
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
	select {
	case <-writer.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not emit a ready event")
	}
}

func waitForRunError(t *testing.T, runErrors <-chan error) error {
	t.Helper()
	select {
	case err := <-runErrors:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return fmt.Errorf("unreachable")
	}
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
