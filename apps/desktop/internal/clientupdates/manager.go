package clientupdates

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// ClientTarget is a client to update, on this computer or in a WSL
// distribution.
type ClientTarget struct {
	ID ClientID `json:"id"`
	// WSL is the distribution the client lives in; null for this
	// computer.
	WSL *string `json:"wsl"`
}

// ClientStatus is what the About page shows for one client.
type ClientStatus struct {
	ID ClientID `json:"id"`
	// WSL is the distribution the client lives in; null for this
	// computer.
	WSL                *string  `json:"wsl"`
	Phase              string   `json:"phase"`
	CurrentVersion     *string  `json:"current_version"`
	LatestVersion      *string  `json:"latest_version"`
	InstallMethod      string   `json:"install_method"`
	Executable         *string  `json:"executable"`
	OtherInstallations []string `json:"other_installations"`
	CanUpdate          bool     `json:"can_update"`
	CheckedAt          *string  `json:"checked_at"`
	ErrorCode          *string  `json:"error_code"`
	ErrorDetail        *string  `json:"error_detail"`
}

func newStatus(id ClientID, distribution *string) ClientStatus {
	return ClientStatus{ID: id, WSL: distribution, Phase: "idle", InstallMethod: "unknown", OtherInstallations: []string{}}
}

func (s *ClientStatus) fail(failure *clientError) {
	s.Phase = "error"
	s.CanUpdate = false
	code, detail := failure.code, failure.detail
	s.ErrorCode, s.ErrorDetail = &code, &detail
}

func sameDistribution(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (s *ClientStatus) at(distribution *string, ids []ClientID) bool {
	return sameDistribution(s.WSL, distribution) && slices.Contains(ids, s.ID)
}

// ClientSnapshot is every client's status, published on each change.
type ClientSnapshot struct {
	Revision uint64 `json:"revision"`
	Busy     bool   `json:"busy"`
	// Clients are this computer's, then those found in WSL distributions.
	Clients []ClientStatus `json:"clients"`
	// WSLUnchecked are distributions left unchecked because they were not
	// running.
	WSLUnchecked []string `json:"wsl_unchecked"`
}

func initialSnapshot() ClientSnapshot {
	snapshot := ClientSnapshot{Clients: []ClientStatus{}, WSLUnchecked: []string{}}
	for _, id := range allClients {
		snapshot.Clients = append(snapshot.Clients, newStatus(id, nil))
	}
	return snapshot
}

func (s ClientSnapshot) clone() ClientSnapshot {
	s.Clients = slices.Clone(s.Clients)
	s.WSLUnchecked = slices.Clone(s.WSLUnchecked)
	return s
}

func now() *string {
	text := time.Now().UTC().Format(time.RFC3339Nano)
	return &text
}

func text(value string) *string { return &value }

// Manager checks and updates clients, one operation at a time. The host
// owns each operation: closing the About page only drops a listener and
// never cancels a package-manager update.
type Manager struct {
	mu        sync.Mutex
	snapshot  ClientSnapshot
	operation sync.Mutex
	emit      func(ClientSnapshot)

	useSystemProxy  func() bool
	home            func() (string, error)
	client          func(useSystemProxy bool) *http.Client
	loadEnvironment func(home string, useSystemProxy bool) (*Environment, *clientError)
	distributions   func() []wsl.Distribution
	wslEnvironment  func(distribution string, useSystemProxy bool) (*Environment, *clientError)
}

// NewManager publishes each snapshot through emit. useSystemProxy reads
// the preference when an operation starts.
func NewManager(useSystemProxy func() bool, emit func(ClientSnapshot)) *Manager {
	return &Manager{
		snapshot:        initialSnapshot(),
		emit:            emit,
		useSystemProxy:  useSystemProxy,
		home:            control.UserHome,
		client:          httpClient,
		loadEnvironment: loadEnvironment,
		distributions:   wsl.Distributions,
		wslEnvironment: func(distribution string, useSystemProxy bool) (*Environment, *clientError) {
			target, failure := newWSL(distribution)
			if failure != nil {
				return nil, failure
			}
			return wslEnvironment(target, useSystemProxy)
		},
	}
}

// Snapshot is the latest status.
func (m *Manager) Snapshot() ClientSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot.clone()
}

func (m *Manager) publish(change func(*ClientSnapshot)) {
	m.mu.Lock()
	change(&m.snapshot)
	m.snapshot.Revision++
	next := m.snapshot.clone()
	m.mu.Unlock()
	if m.emit != nil {
		m.emit(next)
	}
}

// Refresh checks every client for updates in the background.
func (m *Manager) Refresh() (ClientSnapshot, error) { return m.start(nil) }

// Update updates targets in the background.
func (m *Manager) Update(targets []ClientTarget) (ClientSnapshot, error) {
	if len(targets) == 0 {
		return ClientSnapshot{}, errors.New("Invalid client selection")
	}
	for i, target := range targets {
		for _, other := range targets[:i] {
			if other.ID == target.ID && sameDistribution(other.WSL, target.WSL) {
				return ClientSnapshot{}, errors.New("Invalid client selection")
			}
		}
	}
	return m.start(targets)
}

// start checks every client, or updates targets, in the background. While
// another operation runs it returns that operation's snapshot.
func (m *Manager) start(update []ClientTarget) (ClientSnapshot, error) {
	if !m.operation.TryLock() {
		return m.Snapshot(), nil
	}
	home, err := m.home()
	if err != nil {
		m.operation.Unlock()
		return ClientSnapshot{}, err
	}
	useSystemProxy := m.useSystemProxy()
	client := m.client(useSystemProxy)
	updating := len(update) > 0
	m.publish(func(s *ClientSnapshot) {
		s.Busy = true
		for i := range s.Clients {
			status := &s.Clients[i]
			if updating && !slices.ContainsFunc(update, func(target ClientTarget) bool {
				return status.at(target.WSL, []ClientID{target.ID})
			}) {
				continue
			}
			status.Phase = "checking"
			if updating {
				status.Phase = "updating"
			}
			status.CanUpdate = false
			status.ErrorCode, status.ErrorDetail = nil, nil
		}
	})
	initial := m.Snapshot()
	local := allClients
	if updating {
		local = nil
		for _, target := range update {
			if target.WSL == nil {
				local = append(local, target.ID)
			}
		}
	}
	go func() {
		defer m.operation.Unlock()
		ctx := context.Background()
		var both sync.WaitGroup
		if len(local) > 0 {
			both.Go(func() {
				env, failure := m.loadEnvironment(home, useSystemProxy)
				m.check(ctx, client, nil, env, failure, local, updating)
			})
		}
		both.Go(func() { m.checkWSL(ctx, client, update, useSystemProxy) })
		both.Wait()
		m.publish(func(s *ClientSnapshot) { s.Busy = false })
	}()
	return initial, nil
}

// inspect checks one client: where and how it is installed, its version,
// and the latest one. With an update available, it also returns what the
// update needs.
func inspect(ctx context.Context, env *Environment, client *http.Client, id ClientID) (ClientStatus, *installation, *clientVersion) {
	status := newStatus(id, env.distribution())
	status.CheckedAt = now()
	install := detect(env, id)
	if install == nil {
		status.Phase = "not_installed"
		return status, nil, nil
	}
	status.Executable = text(install.executable)
	status.InstallMethod = install.method.label()
	status.OtherInstallations = slices.Clone(install.others)
	current, failure := currentVersion(env, id, install)
	if failure != nil {
		status.fail(failure)
		return status, nil, nil
	}
	status.CurrentVersion = text(current.String())
	latest, failure := fetchLatest(ctx, client, latestSourceOf(env, id, install), install, id)
	if failure != nil {
		status.fail(failure)
		return status, nil, nil
	}
	status.LatestVersion = text(latest.String())
	available := latest.newerThan(current)
	status.Phase = "up_to_date"
	if available {
		status.Phase = "available"
		_, failure := updateCommand(env, id, install, latest)
		if status.CanUpdate = failure == nil; !status.CanUpdate {
			status.Phase = "manual"
		}
	}
	return status, install, &latest
}

func performUpdate(env *Environment, id ClientID, install *installation, latest clientVersion) (clientVersion, *clientError) {
	fresh := detect(env, id)
	if fresh == nil || fresh.executable != install.executable || fresh.resolved != install.resolved || fresh.method != install.method {
		return clientVersion{}, newError("changed", "Client installation changed")
	}
	current, failure := currentVersion(env, id, fresh)
	if failure != nil {
		return clientVersion{}, failure
	}
	// Another updater may have replaced this file in place since the
	// check.
	if !latest.newerThan(current) {
		return current, nil
	}
	spec, failure := updateCommand(env, id, fresh, latest)
	if failure != nil {
		return clientVersion{}, failure
	}
	output, failure := run(env, spec, updateTimeout)
	if failure != nil {
		return clientVersion{}, failure
	}
	after := detect(env, id)
	if after == nil {
		return clientVersion{}, newError("command", "Client disappeared after update")
	}
	version, failure := currentVersion(env, id, after)
	if failure != nil {
		return clientVersion{}, failure
	}
	if !version.newerThan(current) {
		return clientVersion{}, newError("unchanged", "The updater finished without installing a newer version.\n"+strings.TrimSpace(output))
	}
	return version, nil
}

// check checks ids in one place, this computer or a WSL distribution, and
// with update set updates those that can be.
func (m *Manager) check(ctx context.Context, client *http.Client, distribution *string, env *Environment, failure *clientError, ids []ClientID, update bool) {
	if failure != nil {
		m.publish(func(s *ClientSnapshot) {
			for i := range s.Clients {
				item := &s.Clients[i]
				if item.at(distribution, ids) && (item.Phase == "checking" || item.Phase == "updating") {
					item.fail(newError(failure.code, failure.detail))
				}
			}
		})
		return
	}
	for _, id := range ids {
		status, install, latest := inspect(ctx, env, client, id)
		if update && status.CanUpdate && install != nil {
			status.Phase = "updating"
			status.CanUpdate = false
			m.replace(status)
			if version, failure := performUpdate(env, id, install, *latest); failure != nil {
				status.fail(failure)
			} else {
				status.CurrentVersion = text(version.String())
				status.Phase = "updated"
				status.CheckedAt = now()
			}
		}
		m.replace(status)
	}
}

// checkWSL checks clients in WSL distributions. A refresh lists what each
// running distribution has and never starts a stopped one. An update
// reaches the distribution the user chose, which starts it if stopped.
func (m *Manager) checkWSL(ctx context.Context, client *http.Client, update []ClientTarget, useSystemProxy bool) {
	distributions := m.distributions()
	if len(update) > 0 {
		var places []string
		ids := map[string][]ClientID{}
		for _, target := range update {
			if target.WSL == nil {
				continue
			}
			name := *target.WSL
			if _, ok := ids[name]; !ok {
				places = append(places, name)
			}
			ids[name] = append(ids[name], target.ID)
		}
		for _, name := range places {
			var env *Environment
			var failure *clientError
			if slices.ContainsFunc(distributions, func(d wsl.Distribution) bool { return d.Name == name }) {
				env, failure = m.wslEnvironment(name, useSystemProxy)
			} else {
				failure = newErrorf("changed", "WSL distribution %s is no longer available", name)
			}
			m.check(ctx, client, text(name), env, failure, ids[name], true)
		}
		return
	}
	var running, stopped []string
	for _, d := range distributions {
		if d.Running {
			running = append(running, d.Name)
		} else {
			stopped = append(stopped, d.Name)
		}
	}
	m.publish(func(s *ClientSnapshot) {
		s.WSLUnchecked = append([]string{}, stopped...)
		s.Clients = slices.DeleteFunc(s.Clients, func(c ClientStatus) bool {
			return c.WSL != nil && !slices.Contains(running, *c.WSL)
		})
	})
	for _, name := range running {
		var found []ClientStatus
		env, failure := m.wslEnvironment(name, useSystemProxy)
		if failure != nil {
			log.Printf("astrlink: unable to check clients in WSL %s: %s", name, failure.detail)
		} else {
			found = []ClientStatus{}
			for _, id := range allClients {
				if status, _, _ := inspect(ctx, env, client, id); status.Phase != "not_installed" {
					found = append(found, status)
				}
			}
		}
		m.publish(func(s *ClientSnapshot) {
			s.Clients = slices.DeleteFunc(s.Clients, func(c ClientStatus) bool { return c.WSL != nil && *c.WSL == name })
			if found != nil {
				s.Clients = append(s.Clients, found...)
			} else {
				s.WSLUnchecked = append(s.WSLUnchecked, name)
			}
		})
	}
}

func (m *Manager) replace(status ClientStatus) {
	m.publish(func(s *ClientSnapshot) {
		for i := range s.Clients {
			if s.Clients[i].ID == status.ID && sameDistribution(s.Clients[i].WSL, status.WSL) {
				s.Clients[i] = status
				return
			}
		}
	})
}
