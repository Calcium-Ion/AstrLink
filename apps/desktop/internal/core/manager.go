package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

const (
	// readyTimeout leaves room for legacy installation migration and
	// ordinary startup filesystem work on slow disks, even though model
	// weights are verified lazily.
	readyTimeout                     = 120 * time.Second
	handshakeTimeout                 = 12 * time.Second
	stopTimeout                      = 3 * time.Second
	healthAttempts                   = 8
	healthRetryDelay                 = 250 * time.Millisecond
	recoveryStabilityThreshold       = 30 * time.Second
	maxRecoveryAttempts        uint8 = 5
)

// InferencePortFallback is the saved inference port Core found occupied and
// the one it answers on for this run.
type InferencePortFallback struct {
	RequestedPort uint16 `json:"requested_port"`
	ActivePort    uint16 `json:"active_port"`
}

// Snapshot is Core's full state for the overview and settings pages.
type Snapshot struct {
	Phase                 Phase                  `json:"phase"`
	PID                   *int                   `json:"pid"`
	Ready                 *Ready                 `json:"ready"`
	Health                *Health                `json:"health"`
	Version               *Version               `json:"version"`
	Capabilities          *Capabilities          `json:"capabilities"`
	LastError             *string                `json:"last_error"`
	InferencePortFallback *InferencePortFallback `json:"inference_port_fallback"`
	// InferenceListenActive is which interfaces the running Core was
	// started to answer; nil while stopped. It differs from the saved
	// preference until the next restart.
	InferenceListenActive *prefs.InferenceListen `json:"inference_listen_active"`
	// RestartPending reports saved launch settings that differ from the
	// ones the running Core started with; they apply after the next restart.
	RestartPending        bool    `json:"restart_pending"`
	RecoveryAttempt       uint8   `json:"recovery_attempt"`
	RecoveryScheduledInMS *uint64 `json:"recovery_scheduled_in_ms"`
}

// View is the slice of Core state that native surfaces (the tray) render
// from. It is published on every change, so it stays small and cheap to
// compare: no capabilities, no health payload, no countdown that ticks on
// its own.
type View struct {
	Phase Phase `json:"phase"`
	// InferenceURL is the client-facing inference address
	// (client_inference_url), which the tray shows, copies, and syncs into
	// client configs.
	InferenceURL          *string                `json:"inference_url"`
	CoreVersion           *string                `json:"core_version"`
	InferencePortFallback *InferencePortFallback `json:"inference_port_fallback"`
	LastError             *string                `json:"last_error"`
	RecoveryAttempt       uint8                  `json:"recovery_attempt"`
	RecoveryScheduled     bool                   `json:"recovery_scheduled"`
	// ObserverActive reports an agent reading records through the agent
	// CLI right now.
	ObserverActive bool `json:"observer_active"`
	// ObserverReadLevel is how far that agent's latest read reached, while
	// it is active.
	ObserverReadLevel *ObserverReadLevel `json:"observer_read_level"`
	// PendingRawAccess counts agent requests for raw audit content awaiting
	// the operator.
	PendingRawAccess uint32 `json:"pending_raw_access"`
	// ActiveRawGrants counts timed agent grants still running.
	ActiveRawGrants uint32 `json:"active_raw_grants"`
	// RawPasswordRequired reports that no raw password is set yet; only the
	// main window's setup sets it.
	RawPasswordRequired bool         `json:"raw_password_required"`
	RawKeyEvent         *RawKeyEvent `json:"raw_key_event"`
	// RawKeyReplaced reports a raw key that differs from the one the
	// desktop pinned; only the main window resolves it.
	RawKeyReplaced bool `json:"raw_key_replaced"`
}

// Config is where a Manager finds what it runs.
type Config struct {
	// DataDirectory is the instance's data directory, which Core owns.
	DataDirectory string
	// ResourceDirectory is the app's resource directory (see Locate).
	ResourceDirectory string
	// Log receives the desktop's own diagnostics; os.Stderr by default.
	Log func(string)
	// Isolated publishes the agent tools' session as the development
	// instance's, beside the default instance's.
	Isolated bool
}

// state is everything the manager lock guards.
type state struct {
	updateInProgress bool
	lifecycle
	child                       child
	ready                       *Ready
	health                      *Health
	version                     *Version
	capabilities                *Capabilities
	controlToken                string
	observerToken               string
	started                     bool
	inferencePort               uint16
	inferenceListen             prefs.InferenceListen
	startedLaunch               *launchSettings
	maxConcurrentInspections    uint16
	responseStartTimeoutSeconds uint32
	maxRequestBodyMiB           uint32
	useSystemProxy              bool
	locale                      prefs.Locale
	autoRecover                 bool
	recoveryAttempt             uint8
	recoveryScheduledAt         time.Time
	observerSeenAt              time.Time
	observerReadLevel           ObserverReadLevel
	pendingRawAccess            uint32
	activeRawGrants             uint32
	rawPasswordRequired         bool
	rawKeyEvent                 *RawKeyEvent
	rawKeyReplaced              bool
	dataDirectory               string
	localKeyStorage             keystore.LocalKeyStorage
	job                         processTree
	// retiredTrees are the trees of exited Cores whose helpers were told
	// to stop but may still be shutting down, kept so that an update can
	// wait until they no longer hold their executables and DLLs open.
	retiredTrees []processTree
}

func defaultState() state {
	return state{
		lifecycle:                   lifecycle{phase: PhaseStopped},
		inferencePort:               prefs.DefaultInferencePort,
		inferenceListen:             prefs.ListenLoopback,
		maxConcurrentInspections:    prefs.DefaultMaxConcurrentInspections,
		responseStartTimeoutSeconds: prefs.DefaultResponseStartTimeoutSecs,
		useSystemProxy:              true,
		locale:                      prefs.LocaleEn,
		autoRecover:                 true,
	}
}

// sessionStore publishes the locator agent tools read.
type sessionStore interface {
	publish(locator control.Locator)
	clear()
}

// Manager supervises one Core process at a time. Every mutation goes
// through lock and unlock, which publishes a changed View, so no mutation
// site needs its own hook.
type Manager struct {
	config Config

	mu          sync.Mutex
	s           state
	view        View
	viewChanged chan struct{}
	// stateChanged is closed on every unlock, for stop waits.
	stateChanged chan struct{}

	client     *control.Client
	spawn      spawnFunc
	resolveKey func(dataDirectory string) keystore.ResolvedKey
	locate     func() (binary, root string, err error)
	sessions   sessionStore
	attachJob  func(pid int) (processTree, error)
	now        func() time.Time
	// treeTick paces WaitForProcessTreeExit in tests; nil polls on a
	// ticker.
	treeTick <-chan time.Time

	tests serviceTests
}

// New returns a manager whose Core is stopped.
func New(config Config) *Manager {
	if config.Log == nil {
		config.Log = func(message string) { fmt.Fprintln(os.Stderr, message) }
	}
	m := &Manager{
		config:       config,
		s:            defaultState(),
		viewChanged:  make(chan struct{}),
		stateChanged: make(chan struct{}),
		client:       control.NewClient(),
		spawn:        spawnProcess,
		resolveKey: func(dataDirectory string) keystore.ResolvedKey {
			return keystore.ResolveForStart(dataDirectory, config.Log)
		},
		locate:    func() (string, string, error) { return Locate(config.ResourceDirectory) },
		sessions:  systemSessions{log: config.Log, isolated: config.Isolated},
		attachJob: attachJobObject,
		now:       time.Now,
	}
	m.view = m.s.view(m.now())
	return m
}

func (m *Manager) lock() { m.mu.Lock() }

// unlock publishes the View if it changed and wakes stop waits.
func (m *Manager) unlock() {
	next := m.s.view(m.now())
	if !reflect.DeepEqual(next, m.view) {
		m.view = next
		close(m.viewChanged)
		m.viewChanged = make(chan struct{})
	}
	close(m.stateChanged)
	m.stateChanged = make(chan struct{})
	m.mu.Unlock()
}

// View returns the current View and a channel closed at its next change.
func (m *Manager) View() (View, <-chan struct{}) {
	m.lock()
	defer m.unlock()
	return m.view, m.viewChanged
}

func (s *state) view(now time.Time) View {
	ready := s.phase == PhaseReady
	view := View{
		Phase:                 s.phase,
		InferencePortFallback: s.inferencePortFallback(),
		LastError:             optional(s.lastError),
		RecoveryAttempt:       s.recoveryAttempt,
		RecoveryScheduled:     !s.recoveryScheduledAt.IsZero(),
		ObserverActive:        s.observerActive(now),
	}
	if s.ready != nil {
		view.InferenceURL = optional(s.ready.ClientInferenceURL)
		view.CoreVersion = optional(s.ready.CoreVersion)
	}
	if view.ObserverActive && s.observerReadLevel != "" {
		level := s.observerReadLevel
		view.ObserverReadLevel = &level
	}
	if ready {
		view.PendingRawAccess = s.pendingRawAccess
		view.ActiveRawGrants = s.activeRawGrants
		view.RawPasswordRequired = s.rawPasswordRequired
		if s.rawKeyEvent != nil {
			event := *s.rawKeyEvent
			view.RawKeyEvent = &event
		}
		view.RawKeyReplaced = s.rawKeyReplaced
	}
	return view
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *state) observerActive(now time.Time) bool {
	return s.phase == PhaseReady && !s.observerSeenAt.IsZero() && now.Sub(s.observerSeenAt) < ObserverActiveWindow
}

func (s *state) launchSettings() launchSettings {
	return launchSettings{
		inference:                   inferenceBind{listen: s.inferenceListen, port: s.inferencePort},
		maxConcurrentInspections:    s.maxConcurrentInspections,
		responseStartTimeoutSeconds: s.responseStartTimeoutSeconds,
		maxRequestBodyMiB:           s.maxRequestBodyMiB,
		useSystemProxy:              s.useSystemProxy,
	}
}

func (s *state) restartPending() bool {
	return s.startedLaunch != nil && *s.startedLaunch != s.launchSettings()
}

func (s *state) inferencePortFallback() *InferencePortFallback {
	if s.startedLaunch == nil || s.ready == nil {
		return nil
	}
	parsed, err := url.Parse(s.ready.InferenceURL)
	if err != nil {
		return nil
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil {
		return nil
	}
	requested := s.startedLaunch.inference.port
	if requested == uint16(port) {
		return nil
	}
	return &InferencePortFallback{RequestedPort: requested, ActivePort: uint16(port)}
}

// startRefusal says why a start cannot proceed now, if it cannot.
func (s *state) startRefusal() error {
	if s.updateInProgress {
		return errors.New("Application update in progress")
	}
	if !startAllowed(s.lifecycle, s.child != nil) {
		if s.lastError != "" {
			return errors.New(s.lastError)
		}
		return errors.New("astrlink-core is already running or stopping")
	}
	return nil
}

func (s *state) clearHandshake() {
	s.observerSeenAt = time.Time{}
	s.observerReadLevel = ""
	s.pendingRawAccess = 0
	s.activeRawGrants = 0
	s.rawPasswordRequired = false
	s.rawKeyEvent = nil
	s.rawKeyReplaced = false
	s.ready = nil
	s.startedLaunch = nil
	s.health = nil
	s.version = nil
	s.capabilities = nil
	s.controlToken = ""
	s.observerToken = ""
}

// clearProcessGuard ends whatever is left of Core's process tree. The
// tree is terminated at once, so helpers still end with Core, but kept
// until it is empty so that an update can wait for that.
func (s *state) clearProcessGuard() {
	if s.job != nil {
		if err := s.job.Terminate(); err != nil {
			// Closing it still ends the tree's processes.
			fmt.Fprintf(os.Stderr, "unable to stop astrlink-core helper processes: %v\n", err)
			s.job.Close()
		} else {
			s.retiredTrees = append(s.retiredTrees, s.job)
		}
		s.job = nil
	}
	s.retiredTrees, _ = pruneRetired(s.retiredTrees)
}

// Snapshot returns Core's full state.
func (m *Manager) Snapshot() Snapshot {
	m.lock()
	defer m.unlock()
	s := &m.s
	snapshot := Snapshot{
		Phase:                 s.phase,
		Ready:                 s.ready,
		Health:                s.health,
		Version:               s.version,
		Capabilities:          s.capabilities,
		LastError:             optional(s.lastError),
		InferencePortFallback: s.inferencePortFallback(),
		RestartPending:        s.restartPending(),
		RecoveryAttempt:       s.recoveryAttempt,
	}
	if s.pid != 0 {
		pid := s.pid
		snapshot.PID = &pid
	}
	if s.startedLaunch != nil {
		listen := s.startedLaunch.inference.listen
		snapshot.InferenceListenActive = &listen
	}
	if !s.recoveryScheduledAt.IsZero() {
		remaining := uint64(max(s.recoveryScheduledAt.Sub(m.now()), 0).Milliseconds())
		snapshot.RecoveryScheduledInMS = &remaining
	}
	return snapshot
}

// Configure applies saved preferences. Launch settings take effect at the
// next start; the rest at once.
func (m *Manager) Configure(p prefs.Preferences) {
	m.lock()
	defer m.unlock()
	m.s.inferencePort = p.InferencePort
	m.s.inferenceListen = p.InferenceListen
	m.s.maxConcurrentInspections = p.MaxConcurrentInspections
	m.s.responseStartTimeoutSeconds = p.ResponseStartTimeoutSeconds
	m.s.maxRequestBodyMiB = p.MaxRequestBodyMiB
	m.s.useSystemProxy = p.UseSystemProxy
	m.s.locale = p.Locale
	m.s.autoRecover = p.CoreAutoRecover
	if !p.CoreAutoRecover {
		m.s.recoveryScheduledAt = time.Time{}
		m.s.recoveryAttempt = 0
	}
}

// BeginUpdate keeps Core from starting while the app replaces itself. It
// reports whether Core was running; release ends the guard.
func (m *Manager) BeginUpdate() (release func(), running bool, err error) {
	m.lock()
	defer m.unlock()
	if m.s.updateInProgress {
		return nil, false, errors.New("Application update in progress")
	}
	m.s.updateInProgress = true
	var once sync.Once
	return func() {
		once.Do(func() {
			m.lock()
			m.s.updateInProgress = false
			m.unlock()
		})
	}, m.s.child != nil, nil
}

// LocalKeyStorage is where the last Core start found its local key.
func (m *Manager) LocalKeyStorage() keystore.LocalKeyStorage {
	m.lock()
	defer m.unlock()
	return m.s.localKeyStorage
}

// DataDirectory is the data directory of the last Core start.
func (m *Manager) DataDirectory() string {
	m.lock()
	defer m.unlock()
	return m.s.dataDirectory
}

// NoteRawKeyReplaced records the desktop's verdict on the raw key pin for
// the tray and reports whether it differs from the previous one.
func (m *Manager) NoteRawKeyReplaced(replaced bool) bool {
	m.lock()
	defer m.unlock()
	changed := m.s.rawKeyReplaced != replaced
	m.s.rawKeyReplaced = replaced
	return changed
}

// Start launches Core. It returns once the process runs; readiness, the
// handshake and failures arrive as state changes.
func (m *Manager) Start() error {
	// Turn down a start that cannot proceed before touching the keychain:
	// resolving the key may migrate or delete local.key, which must not
	// happen beside a running Core. The check repeats under the lock below.
	m.lock()
	err := m.s.startRefusal()
	m.unlock()
	if err != nil {
		return err
	}
	// Read the keychain before taking the manager lock, so a keychain
	// prompt never blocks readers of Core state.
	dataDirectory := m.config.DataDirectory
	var localKey keystore.ResolvedKey
	if dataDirectory != "" {
		localKey = m.resolveKey(dataDirectory)
	}

	// Keep the lock from publishing Spawning until the child and its
	// platform process guard are recorded, so a concurrent stop cannot see
	// "no child" and return before this start completes.
	m.lock()
	defer m.unlock()
	s := &m.s
	if err := s.startRefusal(); err != nil {
		return err
	}
	s.generation++
	s.started = true
	s.recoveryScheduledAt = time.Time{}
	s.phase = PhaseSpawning
	s.pid = 0
	s.clearHandshake()
	launch := s.launchSettings()
	s.startedLaunch = &launch
	s.lastError = ""
	s.clearProcessGuard()
	m.sessions.clear()
	generation := s.generation
	fail := func(message string) error {
		m.failGenerationLocked(generation, message)
		return errors.New(message)
	}
	if dataDirectory == "" {
		return fail("unable to resolve AstrLink data directory")
	}
	s.dataDirectory = dataDirectory
	s.localKeyStorage = localKey.Storage
	controlToken, err := generateControlToken()
	if err != nil {
		return fail(err.Error())
	}
	observerToken, err := generateControlToken()
	if err != nil {
		return fail(err.Error())
	}
	binary, resources, err := m.locate()
	if err != nil {
		return fail(err.Error())
	}
	spec := spawnSpec{
		binary: binary,
		args:   append(sidecarArgs(runtime.GOOS, os.Getpid(), dataDirectory, launch), localKeyArgs(localKey)...),
		stdin:  sidecarStdin(controlToken, localKey.StdinHex, observerToken),
	}
	if runtime.GOOS == "linux" {
		onnx := linuxONNXRuntimePath(resources)
		if info, err := os.Stat(onnx); err != nil || !info.Mode().IsRegular() {
			return fail("packaged Linux ONNX Runtime is missing or invalid")
		}
		spec.env = append(spec.env, linuxONNXRuntimePathEnv+"="+onnx)
	}
	process, events, err := m.spawn(spec)
	clear(spec.stdin)
	var stdinErr errStdin
	switch {
	case errors.As(err, &stdinErr):
		return m.killFailedStartLocked(generation, process,
			fmt.Sprintf("unable to deliver local control token to astrlink-core: %v", stdinErr.err))
	case err != nil:
		return fail(fmt.Sprintf("unable to spawn astrlink-core: %v", err))
	}
	job, err := m.attachJob(process.Pid())
	if err != nil {
		return m.killFailedStartLocked(generation, process,
			fmt.Sprintf("unable to assign astrlink-core PID %d to the desktop Job Object: %v", process.Pid(), err))
	}
	s.pid = process.Pid()
	s.child = process
	s.controlToken = controlToken
	s.observerToken = observerToken
	s.phase = PhaseWaitingForReady
	s.job = job

	go m.monitor(generation, events)
	time.AfterFunc(readyTimeout, func() {
		m.lock()
		timedOut := m.s.generation == generation && m.s.phase == PhaseWaitingForReady
		m.unlock()
		if timedOut {
			m.failGenerationAndStop(generation, "timed out waiting for astrlink-core ready signal")
		}
	})
	return nil
}

// killFailedStartLocked ends a process this start could not finish setting
// up and records why.
func (m *Manager) killFailedStartLocked(generation uint64, process child, message string) error {
	m.s.pid = process.Pid()
	event := lifecycleEvent{kind: eventFailureStopRequested, message: message}
	if err := process.Kill(); err != nil {
		event = lifecycleEvent{kind: eventFailureStopRequestFailed, message: message, killError: err.Error()}
	}
	m.s.lifecycle = transitionLifecycle(m.s.lifecycle, generation, event)
	return errors.New(m.s.lastError)
}

// Restart stops Core, waiting for it, and starts it again.
func (m *Manager) Restart(ctx context.Context) error {
	if err := m.StopAndWait(ctx); err != nil {
		return err
	}
	return m.Start()
}

// StopAndWait asks a ready Core to shut down, kills it when it does not,
// and waits for the process to end.
func (m *Manager) StopAndWait(ctx context.Context) error {
	if generation, ok := m.requestGracefulStop(ctx); ok {
		if m.waitUntilStopped(ctx, generation, stopTimeout) == nil {
			return nil
		}
		m.config.Log("astrlink-core graceful shutdown timed out; using force-stop fallback")
	}
	generation, ok, err := m.requestStop()
	if err != nil {
		return err
	}
	if ok {
		return m.waitUntilStopped(ctx, generation, stopTimeout)
	}
	return nil
}

func (m *Manager) requestGracefulStop(ctx context.Context) (uint64, bool) {
	m.lock()
	s := &m.s
	if s.phase != PhaseReady || s.child == nil || s.ready == nil || s.controlToken == "" {
		m.unlock()
		return 0, false
	}
	baseURL, token, generation := s.ready.ControlURL, s.controlToken, s.generation
	s.phase = PhaseStopping
	s.lastError = ""
	m.unlock()

	err := m.client.Shutdown(ctx, baseURL, token)
	m.lock()
	defer m.unlock()
	if err == nil {
		if m.s.generation == generation && m.s.phase == PhaseStopping {
			m.s.clearHandshake()
		}
		return generation, true
	}
	if m.s.generation == generation && m.s.phase == PhaseStopping && m.s.child != nil {
		m.s.phase = PhaseReady
	}
	m.config.Log(fmt.Sprintf("astrlink-core graceful shutdown failed: %v; using force-stop fallback", err))
	return 0, false
}

// requestStop kills Core. ok reports a stop to wait for.
func (m *Manager) requestStop() (generation uint64, ok bool, err error) {
	m.lock()
	defer m.unlock()
	s := &m.s
	generation = s.generation
	if s.phase == PhaseStopping {
		return generation, true, nil
	}
	if s.child == nil {
		if s.pid != 0 {
			if s.lastError != "" {
				return 0, false, errors.New(s.lastError)
			}
			return 0, false, fmt.Errorf("astrlink-core process handle is unavailable; manually terminate PID %d before retrying", s.pid)
		}
		s.generation++
		s.phase = PhaseStopped
		s.clearHandshake()
		s.lastError = ""
		s.recoveryScheduledAt = time.Time{}
		s.recoveryAttempt = 0
		s.clearProcessGuard()
		return 0, false, nil
	}
	// Keep the lock until the kill result is recorded so no reader sees an
	// unconfirmed Stopping state.
	process := s.child
	s.child = nil
	s.clearHandshake()
	if err := process.Kill(); err != nil {
		s.lifecycle = transitionLifecycle(s.lifecycle, generation, lifecycleEvent{kind: eventStopRequestFailed, message: err.Error()})
		return 0, false, errors.New(s.lastError)
	}
	s.lifecycle = transitionLifecycle(s.lifecycle, generation, lifecycleEvent{kind: eventStopRequested})
	return generation, true, nil
}

func (m *Manager) waitUntilStopped(ctx context.Context, generation uint64, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		m.lock()
		s := &m.s
		if s.generation != generation {
			m.unlock()
			return errors.New("astrlink-core stop was superseded")
		}
		phase, lastError, changed := s.phase, s.lastError, m.stateChanged
		m.unlock()
		switch phase {
		case PhaseStopped:
			return nil
		case PhaseError, PhaseExited:
			if lastError == "" {
				lastError = "astrlink-core did not stop cleanly"
			}
			return errors.New(lastError)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			m.lock()
			if m.s.generation == generation {
				m.s.lifecycle = transitionLifecycle(m.s.lifecycle, generation, lifecycleEvent{kind: eventStopWaitTimedOut})
			}
			message := m.s.lastError
			m.unlock()
			if message == "" {
				message = "timed out waiting for astrlink-core to terminate"
			}
			return errors.New(message)
		}
	}
}

func (m *Manager) monitor(generation uint64, events <-chan processEvent) {
	for event := range events {
		switch event.kind {
		case processStdout:
			// The handshake runs on its own so termination and errors stay
			// consumable while control requests are in flight.
			go m.handleStdout(generation, event.text)
		case processError:
			log.Printf("astrlink: lost astrlink-core: %s", event.text)
			m.handleProcessError(generation, event.text)
			return
		case processTerminated:
			log.Printf("astrlink: astrlink-core exited: %s", event.text)
			m.handleTerminated(generation, event.text)
			return
		}
	}
}

func (m *Manager) handleStdout(generation uint64, line string) {
	m.lock()
	shouldParse := m.s.generation == generation && m.s.phase == PhaseWaitingForReady
	m.unlock()
	if !shouldParse {
		return
	}
	ready, err := parseReady(line)
	if err != nil {
		m.failGenerationAndStop(generation, err.Error())
		return
	}
	m.lock()
	if m.s.generation != generation || m.s.phase != PhaseWaitingForReady {
		m.unlock()
		return
	}
	m.s.ready = &ready
	if fallback := m.s.inferencePortFallback(); fallback != nil {
		m.config.Log(fmt.Sprintf("inference port %d is occupied; using 127.0.0.1:%d for this run", fallback.RequestedPort, fallback.ActivePort))
	}
	m.s.phase = PhaseHandshaking
	m.unlock()

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	health, version, capabilities, err := m.performHandshake(ctx, ready)
	if err != nil {
		if ctx.Err() != nil {
			err = errors.New("timed out completing the astrlink-core control handshake")
		}
		m.failGenerationAndStop(generation, err.Error())
		return
	}
	m.lock()
	defer m.unlock()
	s := &m.s
	if s.generation != generation || s.phase != PhaseHandshaking {
		return
	}
	s.health, s.version, s.capabilities = &health, &version, &capabilities
	s.phase = PhaseReady
	s.lastError = ""
	m.sessions.publish(control.Locator{
		DataDirectory: s.dataDirectory,
		ControlURL:    ready.ControlURL,
		ObserverToken: s.observerToken,
		GatewayURL:    ready.ClientInferenceURL,
		PID:           s.pid,
	})
	time.AfterFunc(recoveryStabilityThreshold, func() {
		m.lock()
		defer m.unlock()
		if m.s.generation == generation && m.s.phase == PhaseReady {
			m.s.recoveryAttempt = 0
		}
	})
}

func (m *Manager) performHandshake(ctx context.Context, ready Ready) (Health, Version, Capabilities, error) {
	healthError := "health request was not attempted"
	var health *Health
	for attempt := range healthAttempts {
		var response Health
		if err := m.client.GetJSON(ctx, ready.ControlURL, "/control/v1/health", &response); err != nil {
			healthError = err.Error()
		} else if response.Status != "ok" {
			healthError = fmt.Sprintf("Core health is %q, expected \"ok\"", response.Status)
		} else {
			health = &response
			break
		}
		if attempt+1 < healthAttempts {
			select {
			case <-time.After(healthRetryDelay):
			case <-ctx.Done():
				return Health{}, Version{}, Capabilities{}, ctx.Err()
			}
		}
	}
	if health == nil {
		return Health{}, Version{}, Capabilities{}, fmt.Errorf("Core health handshake failed: %s", healthError)
	}
	var version Version
	if err := m.client.GetJSON(ctx, ready.ControlURL, "/control/v1/version", &version); err != nil {
		return Health{}, Version{}, Capabilities{}, err
	}
	var capabilities Capabilities
	if err := m.client.GetJSON(ctx, ready.ControlURL, "/control/v1/capabilities", &capabilities); err != nil {
		return Health{}, Version{}, Capabilities{}, err
	}
	if err := verifyContract(ready, version, capabilities); err != nil {
		return Health{}, Version{}, Capabilities{}, err
	}
	return *health, version, capabilities, nil
}

func (m *Manager) handleTerminated(generation uint64, termination string) {
	m.lock()
	s := &m.s
	if s.generation != generation {
		m.unlock()
		return
	}
	s.lifecycle = transitionLifecycle(s.lifecycle, generation, lifecycleEvent{kind: eventTerminated, message: termination})
	s.child = nil
	s.clearHandshake()
	s.clearProcessGuard()
	m.sessions.clear()
	shouldRecover := s.phase != PhaseStopped && s.autoRecover
	m.unlock()
	if shouldRecover {
		m.scheduleRecovery()
	}
}

func (m *Manager) scheduleRecovery() {
	m.lock()
	s := &m.s
	if !s.autoRecover || s.child != nil || s.pid != 0 {
		m.unlock()
		return
	}
	if s.recoveryAttempt >= maxRecoveryAttempts {
		s.recoveryScheduledAt = time.Time{}
		message := s.lastError
		if message == "" {
			message = prefs.T(s.locale, "host.sidecar.exited")
		}
		s.lastError = prefs.T(s.locale, "host.sidecar.recoveryStopped",
			"message", message, "attempts", strconv.Itoa(int(maxRecoveryAttempts)))
		m.unlock()
		return
	}
	if !s.started {
		m.unlock()
		return
	}
	s.recoveryAttempt++
	delay := recoveryDelay(s.recoveryAttempt)
	s.recoveryScheduledAt = m.now().Add(delay)
	generation := s.generation
	m.unlock()

	time.AfterFunc(delay, func() {
		m.lock()
		current := m.s.generation == generation && !m.s.recoveryScheduledAt.IsZero() && m.s.autoRecover
		m.unlock()
		if !current {
			return
		}
		if err := m.Start(); err != nil {
			m.lock()
			m.s.lastError = prefs.T(m.s.locale, "host.sidecar.recoveryFailed", "error", err.Error())
			m.unlock()
			m.scheduleRecovery()
		}
	})
}

func (m *Manager) handleProcessError(generation uint64, message string) {
	m.lock()
	defer m.unlock()
	s := &m.s
	if s.generation != generation {
		return
	}
	if s.phase == PhaseStopping {
		s.lifecycle = transitionLifecycle(s.lifecycle, generation, lifecycleEvent{kind: eventProcessErrorWhileStopping})
		s.child = nil
		s.clearHandshake()
		s.clearProcessGuard()
		m.sessions.clear()
		return
	}
	m.failGenerationAndStopLocked(generation, message)
}

func (m *Manager) failGenerationLocked(generation uint64, message string) {
	s := &m.s
	if s.generation != generation {
		return
	}
	s.phase = PhaseError
	s.child = nil
	s.pid = 0
	s.clearHandshake()
	s.lastError = message
	s.clearProcessGuard()
	m.sessions.clear()
}

func (m *Manager) failGenerationAndStop(generation uint64, message string) {
	m.lock()
	defer m.unlock()
	if m.s.generation != generation {
		return
	}
	switch m.s.phase {
	case PhaseSpawning, PhaseWaitingForReady, PhaseHandshaking, PhaseReady:
		m.failGenerationAndStopLocked(generation, message)
	}
}

func (m *Manager) failGenerationAndStopLocked(generation uint64, message string) {
	log.Printf("astrlink: stopping astrlink-core: %s", message)
	s := &m.s
	s.clearHandshake()
	m.sessions.clear()
	event := lifecycleEvent{kind: eventFailureWithoutChild, message: message}
	if process := s.child; process != nil {
		s.child = nil
		if err := process.Kill(); err != nil {
			event = lifecycleEvent{kind: eventFailureStopRequestFailed, message: message, killError: err.Error()}
		} else {
			s.clearProcessGuard()
			event = lifecycleEvent{kind: eventFailureStopRequested, message: message}
		}
	}
	s.lifecycle = transitionLifecycle(s.lifecycle, generation, event)
}

// Call sends one control request to the ready Core with the operator
// token. It fails while Core is not ready.
func (m *Manager) Call(ctx context.Context, req control.Request) (control.Response, error) {
	m.lock()
	s := &m.s
	if s.phase != PhaseReady {
		message := prefs.T(s.locale, "host.sidecar.notReady")
		m.unlock()
		return control.Response{}, errors.New(message)
	}
	if s.ready == nil {
		m.unlock()
		return control.Response{}, errors.New("Core ready announcement is unavailable")
	}
	if s.controlToken == "" {
		m.unlock()
		return control.Response{}, errors.New("Core local control token is unavailable")
	}
	baseURL, token := s.ready.ControlURL, s.controlToken
	m.unlock()
	return m.client.Do(ctx, baseURL, token, req)
}

// PollObservers reads what agents are doing through the agent CLI.
func (m *Manager) PollObservers(ctx context.Context) error {
	response, err := control.Send(ctx, m, control.Request{Method: "GET", Path: "/control/v1/observers"})
	if err != nil {
		return err
	}
	now := m.now()
	observed, err := parseObserverState(response.Body, now)
	if err != nil {
		return err
	}
	m.lock()
	defer m.unlock()
	s := &m.s
	s.observerSeenAt = time.Time{}
	if observed.age != nil {
		s.observerSeenAt = now.Add(-*observed.age)
	}
	s.observerReadLevel = observed.readLevel
	s.pendingRawAccess = observed.pendingRawAccess
	s.activeRawGrants = observed.activeRawGrants
	s.rawPasswordRequired = observed.rawPasswordRequired
	s.rawKeyEvent = observed.rawKeyEvent
	return nil
}

type systemSessions struct {
	log      func(string)
	isolated bool
}

func (s systemSessions) publish(locator control.Locator) {
	home, err := control.UserHome()
	if err != nil || locator.DataDirectory == "" {
		return
	}
	if err := control.PublishSession(control.InstanceSessionPath(home, s.isolated), locator); err != nil {
		s.log(fmt.Sprintf("unable to publish AstrLink control session: %v", err))
	}
}

func (s systemSessions) clear() {
	home, err := control.UserHome()
	if err != nil {
		return
	}
	if err := control.ClearSession(control.InstanceSessionPath(home, s.isolated)); err != nil {
		s.log(fmt.Sprintf("unable to clear AstrLink control session: %v", err))
	}
}
