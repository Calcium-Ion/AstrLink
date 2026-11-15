package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// testWait bounds how long a test waits for an outcome that should
// arrive at once; reaching it fails the test rather than hanging it.
const testWait = 5 * time.Second

type fakeSessions struct {
	mu        sync.Mutex
	published []string
	cleared   int
}

func (f *fakeSessions) publish(locator control.Locator) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, locator.GatewayURL+" "+locator.ControlURL+" "+locator.ObserverToken)
}

func (f *fakeSessions) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
}

func newTestManager(t *testing.T) (*Manager, *fakeSessions) {
	t.Helper()
	m := New(Config{DataDirectory: t.TempDir(), Log: func(string) {}})
	sessions := &fakeSessions{}
	m.sessions = sessions
	m.resolveKey = func(string) keystore.ResolvedKey { return keystore.ResolvedKey{Storage: keystore.StorageFile} }
	resources := t.TempDir()
	onnx := linuxONNXRuntimePath(resources)
	if err := os.MkdirAll(filepath.Dir(onnx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(onnx, []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.locate = func() (string, string, error) { return "astrlink-core", resources, nil }
	m.attachJob = func(int) (processTree, error) { return nil, nil }
	return m, sessions
}

// fakeChild is a Core process the test drives through its events.
type fakeChild struct {
	pid     int
	events  chan processEvent
	killErr error
	once    sync.Once
	stdin   []byte
	args    []string
}

func (c *fakeChild) Pid() int { return c.pid }

func (c *fakeChild) Kill() error {
	if c.killErr != nil {
		return c.killErr
	}
	c.terminate("astrlink-core exited after signal 9")
	return nil
}

func (c *fakeChild) terminate(how string) {
	c.once.Do(func() {
		c.events <- processEvent{kind: processTerminated, text: how}
		close(c.events)
	})
}

func (m *Manager) setReadyForTest(generation uint64, ready Ready) {
	m.lock()
	defer m.unlock()
	m.s.generation = generation
	m.s.phase = PhaseReady
	m.s.ready = &ready
	m.s.controlToken = "local-test-token"
}

func mustReady(t *testing.T, controlURL string) Ready {
	t.Helper()
	ready, err := parseReady(readyLine(controlURL))
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestTerminationClearsStaleHandshakeAndDistinguishesRequestedStop(t *testing.T) {
	m, _ := newTestManager(t)
	ready := mustReady(t, "http://127.0.0.1:43210")
	version := versionOf(ready)
	capabilities := alphaCapabilities()
	m.lock()
	m.s.generation, m.s.phase, m.s.pid = 7, PhaseReady, 42
	m.s.ready, m.s.health, m.s.version, m.s.capabilities = &ready, &Health{Status: "ok"}, &version, &capabilities
	m.s.controlToken, m.s.observerToken = "control-token", "observer-token"
	m.unlock()

	m.handleTerminated(7, "astrlink-core exited with code 1")
	snapshot := m.Snapshot()
	if snapshot.Phase != PhaseExited || snapshot.PID != nil || snapshot.Ready != nil || snapshot.Health != nil ||
		snapshot.Version != nil || snapshot.Capabilities != nil || snapshot.LastError == nil {
		t.Fatalf("snapshot after exit: %+v", snapshot)
	}
	m.lock()
	tokens := m.s.controlToken + m.s.observerToken
	m.s.generation, m.s.phase, m.s.pid = 8, PhaseStopping, 43
	m.unlock()
	if tokens != "" {
		t.Fatal("tokens survived the exit")
	}
	m.handleTerminated(8, "astrlink-core exited after signal 9")
	if snapshot := m.Snapshot(); snapshot.Phase != PhaseStopped || snapshot.LastError != nil {
		t.Fatalf("snapshot after a requested stop: %+v", snapshot)
	}
}

func TestSnapshotReportsTheListenScopeTheRunningCoreStartedWith(t *testing.T) {
	m, _ := newTestManager(t)
	if m.Snapshot().InferenceListenActive != nil {
		t.Fatal("a stopped Core has no active listen scope")
	}
	saved := prefs.Default(prefs.DefaultInferencePort)
	saved.InferenceListen = prefs.ListenAllInterfaces
	m.Configure(saved)
	// Saving the preference alone changes nothing until Core restarts.
	if m.Snapshot().InferenceListenActive != nil {
		t.Fatal("the saved scope leaked into the snapshot")
	}
	m.lock()
	launch := loopbackLaunch(8317)
	m.s.startedLaunch = &launch
	m.unlock()
	if active := m.Snapshot().InferenceListenActive; active == nil || *active != prefs.ListenLoopback {
		t.Fatalf("active scope %v", active)
	}
	m.lock()
	m.s.clearHandshake()
	m.unlock()
	if m.Snapshot().InferenceListenActive != nil {
		t.Fatal("a cleared handshake keeps the active scope")
	}
}

func TestRestartPendingTracksEveryLaunchSettingUntilTheNextStart(t *testing.T) {
	m, _ := newTestManager(t)
	saved := prefs.Default(prefs.DefaultInferencePort)
	m.Configure(saved)
	if m.Snapshot().RestartPending {
		t.Fatal("a stopped Core picks up the saved settings when it starts")
	}
	m.lock()
	started := m.s.launchSettings()
	m.s.startedLaunch = &started
	m.unlock()
	changes := []func(*prefs.Preferences){
		func(p *prefs.Preferences) { p.InferencePort++ },
		func(p *prefs.Preferences) { p.InferenceListen = prefs.ListenAllInterfaces },
		func(p *prefs.Preferences) { p.MaxConcurrentInspections++ },
		func(p *prefs.Preferences) { p.ResponseStartTimeoutSeconds++ },
		func(p *prefs.Preferences) { p.MaxRequestBodyMiB++ },
		func(p *prefs.Preferences) { p.UseSystemProxy = !p.UseSystemProxy },
	}
	for i, change := range changes {
		edited := saved
		change(&edited)
		m.Configure(edited)
		if !m.Snapshot().RestartPending {
			t.Fatalf("change %d does not wait for a restart", i)
		}
		m.Configure(saved)
		if m.Snapshot().RestartPending {
			t.Fatalf("reverting change %d still waits for a restart", i)
		}
	}
	// Desktop-only preferences apply in place.
	edited := saved
	edited.CoreAutoRecover = !saved.CoreAutoRecover
	m.Configure(edited)
	if m.Snapshot().RestartPending {
		t.Fatal("auto recovery does not need a restart")
	}
	edited = saved
	edited.MaxRequestBodyMiB++
	m.Configure(edited)
	m.lock()
	m.s.clearHandshake()
	m.unlock()
	if m.Snapshot().RestartPending {
		t.Fatal("a stopped Core has nothing to restart")
	}
}

func TestFallbackNoticeTracksTheStartedPortAndClearsOnStop(t *testing.T) {
	m, _ := newTestManager(t)
	ready := mustReady(t, "http://127.0.0.1:43210")
	m.lock()
	launch := loopbackLaunch(9000)
	m.s.startedLaunch, m.s.ready = &launch, &ready
	m.unlock()
	// Saving new preferences must not rewrite the reason for this run's
	// fallback.
	saved := prefs.Default(prefs.DefaultInferencePort)
	saved.MaxRequestBodyMiB = 64
	m.Configure(saved)
	fallback := m.Snapshot().InferencePortFallback
	if fallback == nil || *fallback != (InferencePortFallback{RequestedPort: 9000, ActivePort: 8317}) {
		t.Fatalf("fallback %+v", fallback)
	}
	m.lock()
	m.s.clearHandshake()
	launch = loopbackLaunch(8317)
	m.unlock()
	if m.Snapshot().InferencePortFallback != nil {
		t.Fatal("a stopped Core reports a fallback")
	}
	m.lock()
	m.s.startedLaunch, m.s.ready = &launch, &ready
	m.unlock()
	if m.Snapshot().InferencePortFallback != nil {
		t.Fatal("the saved port is not a fallback")
	}
}

func TestEveryUnlockPublishesAChangedViewExactlyOnce(t *testing.T) {
	m, _ := newTestManager(t)
	view, changed := m.View()
	if view.Phase != PhaseStopped {
		t.Fatalf("initial view %+v", view)
	}
	// Reading does not count as a change.
	m.Snapshot()
	select {
	case <-changed:
		t.Fatal("a read published a change")
	default:
	}
	m.lock()
	m.s.phase = PhaseSpawning
	select {
	case <-changed:
		t.Fatal("a change is visible while the lock is held")
	default:
	}
	m.unlock()
	<-changed
	view, changed = m.View()
	if view.Phase != PhaseSpawning || view.InferenceURL != nil {
		t.Fatalf("view %+v", view)
	}
	ready, err := parseReady(strings.Replace(readyLine("http://127.0.0.1:43117"), "8317", "8324", 2))
	if err != nil {
		t.Fatal(err)
	}
	m.lock()
	m.s.phase = PhaseReady
	m.s.ready = &ready
	launch := loopbackLaunch(8317)
	m.s.startedLaunch = &launch
	m.unlock()
	<-changed
	view, _ = m.View()
	if view.Phase != PhaseReady || view.InferenceURL == nil || *view.InferenceURL != "http://localhost:8324" ||
		view.InferencePortFallback == nil || *view.InferencePortFallback != (InferencePortFallback{RequestedPort: 8317, ActivePort: 8324}) {
		t.Fatalf("view %+v", view)
	}
}

func TestARawKeyVerdictReachesTheTrayOnlyWhenItChanges(t *testing.T) {
	m, _ := newTestManager(t)
	m.lock()
	m.s.phase = PhaseReady
	m.unlock()
	_, changed := m.View()
	if !m.NoteRawKeyReplaced(true) {
		t.Fatal("the first verdict is a change")
	}
	<-changed
	view, changed := m.View()
	if !view.RawKeyReplaced {
		t.Fatal("the verdict did not reach the view")
	}
	if m.NoteRawKeyReplaced(true) {
		t.Fatal("the same verdict is no change")
	}
	select {
	case <-changed:
		t.Fatal("the same verdict published a change")
	default:
	}
	if !m.NoteRawKeyReplaced(false) {
		t.Fatal("a cleared verdict is a change")
	}
}

func TestHandshakeFailureCannotOverrideInactivePhases(t *testing.T) {
	for _, phase := range []Phase{PhaseStopping, PhaseStopped, PhaseExited, PhaseError} {
		m, _ := newTestManager(t)
		m.lock()
		m.s.generation, m.s.phase, m.s.pid = 7, phase, 123
		m.unlock()
		m.failGenerationAndStop(7, "late handshake failure")
		snapshot := m.Snapshot()
		if snapshot.Phase != phase || snapshot.PID == nil || *snapshot.PID != 123 || snapshot.LastError != nil {
			t.Fatalf("%s: %+v", phase, snapshot)
		}
	}
}

func TestProcessMonitorConsumesTerminationWhileHandshakeIsInFlight(t *testing.T) {
	m, _ := newTestManager(t)
	// A server that never answers holds the handshake in its first health
	// request, which must not block process events.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	m.lock()
	m.s.generation, m.s.phase, m.s.pid = 7, PhaseWaitingForReady, 123
	m.unlock()
	events := make(chan processEvent, 2)
	events <- processEvent{kind: processStdout, text: readyLine(server.URL)}
	events <- processEvent{kind: processTerminated, text: "astrlink-core exited with code 0"}
	close(events)
	done := make(chan struct{})
	go func() {
		m.monitor(7, events)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(testWait):
		t.Fatal("the process monitor was blocked by the control handshake")
	}
	if snapshot := m.Snapshot(); snapshot.Phase != PhaseExited || snapshot.PID != nil {
		t.Fatalf("snapshot %+v", snapshot)
	}
}

func TestCommandErrorFinishesAStoppingGeneration(t *testing.T) {
	m, _ := newTestManager(t)
	m.lock()
	m.s.generation, m.s.phase, m.s.pid = 7, PhaseStopping, 123
	m.unlock()
	m.handleProcessError(7, "pipe closed")
	if snapshot := m.Snapshot(); snapshot.Phase != PhaseStopped || snapshot.PID != nil || snapshot.LastError != nil {
		t.Fatalf("snapshot %+v", snapshot)
	}
}

func TestStopWaitReportsTimeoutAndKeepsPIDBlockingRestart(t *testing.T) {
	m, _ := newTestManager(t)
	m.lock()
	m.s.generation, m.s.phase, m.s.pid = 7, PhaseStopping, 123
	m.unlock()
	err := m.waitUntilStopped(context.Background(), 7, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "PID 123") {
		t.Fatalf("got %v", err)
	}
	if snapshot := m.Snapshot(); snapshot.Phase != PhaseError || snapshot.PID == nil || *snapshot.PID != 123 {
		t.Fatalf("snapshot %+v", snapshot)
	}
	if err := m.Start(); err == nil {
		t.Fatal("a remaining PID must block a restart")
	}
}

func TestStopAndWaitHandlesStoppedAndUnrecoverableStates(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatalf("an already stopped manager should stop cleanly: %v", err)
	}
	m.lock()
	m.s.generation, m.s.phase, m.s.pid = 8, PhaseError, 99
	m.s.lastError = "manually terminate PID 99 before retrying"
	m.unlock()
	if err := m.StopAndWait(context.Background()); err == nil || !strings.Contains(err.Error(), "PID 99") {
		t.Fatalf("a missing process handle must remain unrecoverable: %v", err)
	}
}

func TestUpdateGuardIsExclusiveAndReleases(t *testing.T) {
	m, _ := newTestManager(t)
	release, running, err := m.BeginUpdate()
	if err != nil || running {
		t.Fatalf("BeginUpdate: %v running=%v", err, running)
	}
	if _, _, err := m.BeginUpdate(); err == nil {
		t.Fatal("a second update must be refused")
	}
	if err := m.Start(); err == nil || err.Error() != "Application update in progress" {
		t.Fatalf("a start during an update: %v", err)
	}
	release()
	release()
	release, _, err = m.BeginUpdate()
	if err != nil {
		t.Fatalf("the guard did not release: %v", err)
	}
	release()
}

func TestServiceTestCancellationIsSingleUseAndScoped(t *testing.T) {
	m, _ := newTestManager(t)
	cancelled := 0
	release, err := m.RegisterServiceTest("main", "service_a", "test-1", func() { cancelled++ })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RegisterServiceTest("main", "service_a", "test-1", func() {}); err == nil || err.Error() != control.ServiceTestAlreadyRunning {
		t.Fatalf("a duplicate registration: %v", err)
	}
	if m.CancelServiceTest("other-window", "service_a", "test-1") || m.CancelServiceTest("main", "service_b", "test-1") {
		t.Fatal("another owner or service cancelled the test")
	}
	if !m.CancelServiceTest("main", "service_a", "test-1") || cancelled != 1 {
		t.Fatal("the owner could not cancel its test")
	}
	if m.CancelServiceTest("main", "service_a", "test-1") || cancelled != 1 {
		t.Fatal("a cancellation is single use")
	}

	// A stale registration does not release a newer test with its key.
	newer, err := m.RegisterServiceTest("main", "service_a", "test-1", func() { cancelled++ })
	if err != nil {
		t.Fatal(err)
	}
	release()
	if !m.CancelServiceTest("main", "service_a", "test-1") || cancelled != 2 {
		t.Fatal("the stale release removed the newer test")
	}
	newer()
}

// fakeCore serves the handshake and records the control calls it gets.
type fakeCore struct {
	server   *httptest.Server
	mu       sync.Mutex
	calls    []string
	shutdown chan struct{}
}

func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	core := &fakeCore{shutdown: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /control/v1/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Health{Status: "ok"})
	})
	mux.HandleFunc("GET /control/v1/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Version{CoreVersion: "0.1.0-dev", ControlAPIVersion: "v1", ProtocolContractVersion: "v1", BuildCommit: "unknown"})
	})
	mux.HandleFunc("GET /control/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(alphaCapabilities())
	})
	mux.HandleFunc("POST /control/v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		core.shutdown <- struct{}{}
	})
	mux.HandleFunc("/control/v1/", func(w http.ResponseWriter, r *http.Request) {
		core.mu.Lock()
		core.calls = append(core.calls, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		core.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	core.server = httptest.NewServer(mux)
	t.Cleanup(core.server.Close)
	return core
}

func waitForPhase(t *testing.T, m *Manager, want Phase) View {
	t.Helper()
	deadline := time.After(testWait)
	for {
		view, changed := m.View()
		if view.Phase == want {
			return view
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("Core stayed %s, want %s (last error %v)", view.Phase, want, view.LastError)
		}
	}
}

func TestStartHandshakeCallAndGracefulStop(t *testing.T) {
	m, sessions := newTestManager(t)
	core := newFakeCore(t)
	var process *fakeChild
	m.spawn = func(spec spawnSpec) (child, <-chan processEvent, error) {
		process = &fakeChild{pid: 4321, events: make(chan processEvent, 4), stdin: append([]byte(nil), spec.stdin...), args: spec.args}
		process.events <- processEvent{kind: processStdout, text: strings.Replace(readyLine(core.server.URL), "http://localhost:8317", "http://127.0.0.1:8317", 1)}
		return process, process.events, nil
	}
	if _, err := m.Call(context.Background(), control.Request{Method: http.MethodGet, Path: "/control/v1/services"}); err == nil ||
		err.Error() != prefs.T(prefs.LocaleEn, "host.sidecar.notReady") {
		t.Fatalf("a call before ready: %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err == nil {
		t.Fatal("a second start must be refused while Core runs")
	}
	view := waitForPhase(t, m, PhaseReady)
	if view.CoreVersion == nil || *view.CoreVersion != "0.1.0-dev" {
		t.Fatalf("view %+v", view)
	}
	lines := strings.Split(strings.TrimSuffix(string(process.stdin), "\n"), "\n")
	if len(lines) != 3 || len(lines[0]) != 64 || lines[1] != "" || len(lines[2]) != 64 || lines[0] == lines[2] {
		t.Fatalf("stdin lines %q", lines)
	}
	for _, arg := range process.args {
		if strings.Contains(arg, lines[0]) || strings.Contains(arg, lines[2]) {
			t.Fatal("a token reached the arguments")
		}
	}
	// The session carries the observer token and the client gateway address.
	if len(sessions.published) != 1 || !strings.HasSuffix(sessions.published[0], " "+lines[2]) ||
		!strings.HasPrefix(sessions.published[0], "http://127.0.0.1:8317 ") {
		t.Fatalf("published sessions %q", sessions.published)
	}

	if _, err := control.Send(context.Background(), m, control.Request{Method: http.MethodGet, Path: "/control/v1/services?limit=200"}); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	calls := core.calls
	core.mu.Unlock()
	if len(calls) != 1 || calls[0] != "GET /control/v1/services?limit=200 Bearer "+lines[0] {
		t.Fatalf("calls %q", calls)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- m.StopAndWait(context.Background()) }()
	select {
	case <-core.shutdown:
		process.terminate("astrlink-core exited with code 0")
	case err := <-stopped:
		t.Fatalf("the stop returned before Core was asked to shut down: %v", err)
	case <-time.After(testWait):
		t.Fatal("Core was never asked to shut down")
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testWait):
		t.Fatal("the stop did not finish")
	}
	if snapshot := m.Snapshot(); snapshot.Phase != PhaseStopped || snapshot.PID != nil || snapshot.LastError != nil {
		t.Fatalf("snapshot %+v", snapshot)
	}
}

func TestStartFailuresKillTheProcessAndRecordWhy(t *testing.T) {
	tests := []struct {
		name      string
		spawn     func(*fakeChild) error
		attach    error
		wantError string
		wantPID   bool
	}{
		{name: "spawn", spawn: func(*fakeChild) error { return errors.New("no such file") }, wantError: "unable to spawn astrlink-core: no such file"},
		{name: "stdin", spawn: func(*fakeChild) error { return errStdin{errors.New("broken pipe")} }, wantError: "unable to deliver local control token to astrlink-core: broken pipe; astrlink-core termination request succeeded"},
		{name: "stdin with a failed kill", spawn: func(c *fakeChild) error {
			c.killErr = errors.New("access denied")
			return errStdin{errors.New("broken pipe")}
		}, wantError: "manually terminate PID 77", wantPID: true},
		{name: "job object", spawn: func(*fakeChild) error { return nil }, attach: errors.New("denied"), wantError: "unable to assign astrlink-core PID 77 to the desktop Job Object: denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestManager(t)
			m.attachJob = func(int) (processTree, error) { return nil, tt.attach }
			m.spawn = func(spawnSpec) (child, <-chan processEvent, error) {
				process := &fakeChild{pid: 77, events: make(chan processEvent, 1)}
				err := tt.spawn(process)
				var stdin errStdin
				if err != nil && !errors.As(err, &stdin) {
					return nil, nil, err
				}
				return process, process.events, err
			}
			err := m.Start()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("got %v, want %q", err, tt.wantError)
			}
			snapshot := m.Snapshot()
			if snapshot.Phase != PhaseError || (snapshot.PID != nil) != tt.wantPID {
				t.Fatalf("snapshot %+v", snapshot)
			}
		})
	}
}

func TestAnInvalidReadyLineStopsTheGeneration(t *testing.T) {
	m, _ := newTestManager(t)
	m.Configure(func() prefs.Preferences {
		p := prefs.Default(prefs.DefaultInferencePort)
		p.CoreAutoRecover = false
		return p
	}())
	var process *fakeChild
	m.spawn = func(spawnSpec) (child, <-chan processEvent, error) {
		process = &fakeChild{pid: 9, events: make(chan processEvent, 4)}
		process.events <- processEvent{kind: processStdout, text: `{"event":"started"}`}
		return process, process.events, nil
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	view := waitForPhase(t, m, PhaseExited)
	if view.LastError == nil || !strings.Contains(*view.LastError, "unexpected astrlink-core event") {
		t.Fatalf("view %+v", view)
	}
}
