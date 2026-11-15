package clientupdates

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path/filepath"
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
	// Repair is, for a damaged installation, "restore" to put back the
	// version from before an interrupted update or "reinstall" to install
	// the latest again.
	Repair *string `json:"repair"`
	// PreviousVersion is the version restore puts back.
	PreviousVersion *string `json:"previous_version"`
	// KeptPrevious is set after a failed update or repair when the version
	// from before still works.
	KeptPrevious bool `json:"kept_previous"`
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

func (s *ClientStatus) damaged(d *damage) {
	s.Phase = "damaged"
	s.CanUpdate = false
	s.InstallMethod = "npm"
	s.Executable = text(d.executable)
	s.ErrorCode = nil
	s.ErrorDetail = text(d.detail)
	s.Repair, s.PreviousVersion = text("reinstall"), nil
	if d.restore != nil {
		s.Repair, s.PreviousVersion = text("restore"), text(d.restore.version)
	}
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

// action is what a run of the manager does to the clients it checks.
type action int

const (
	actionCheck action = iota
	actionUpdate
	actionRepair
)

func (a action) phase() string {
	switch a {
	case actionUpdate:
		return "updating"
	case actionRepair:
		return "repairing"
	}
	return "checking"
}

// Refresh checks every client for updates in the background.
func (m *Manager) Refresh() (ClientSnapshot, error) { return m.start(nil, actionCheck) }

// Update updates targets in the background.
func (m *Manager) Update(targets []ClientTarget) (ClientSnapshot, error) {
	if !validSelection(targets) {
		return ClientSnapshot{}, errors.New("Invalid client selection")
	}
	return m.start(targets, actionUpdate)
}

// Repair puts back or reinstalls targets whose installation no longer
// starts, in the background.
func (m *Manager) Repair(targets []ClientTarget) (ClientSnapshot, error) {
	if !validSelection(targets) {
		return ClientSnapshot{}, errors.New("Invalid client selection")
	}
	return m.start(targets, actionRepair)
}

func validSelection(targets []ClientTarget) bool {
	if len(targets) == 0 {
		return false
	}
	for i, target := range targets {
		for _, other := range targets[:i] {
			if other.ID == target.ID && sameDistribution(other.WSL, target.WSL) {
				return false
			}
		}
	}
	return true
}

// start checks every client, or updates or repairs targets, in the
// background. While another operation runs it returns that operation's
// snapshot.
func (m *Manager) start(update []ClientTarget, act action) (ClientSnapshot, error) {
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
			status.Phase = act.phase()
			status.CanUpdate = false
			status.ErrorCode, status.ErrorDetail = nil, nil
			status.KeptPrevious = false
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
				m.check(ctx, client, nil, env, failure, local, act)
			})
		}
		both.Go(func() { m.checkWSL(ctx, client, update, act, useSystemProxy) })
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
	if guarded(env) {
		recoverAll(env, id)
	}
	install := detect(env, id)
	if install == nil {
		if found := findDamageWhenGuarded(env, id); found != nil {
			status.damaged(found)
		} else {
			status.Phase = "not_installed"
		}
		return status, nil, nil
	}
	current, failure := currentVersion(env, id, install)
	if failure != nil && install.method.kind == methodNpm && guarded(env) {
		if found := findDamageIn(env, id, install.method.prefix, failure); found != nil {
			status.damaged(found)
			return status, nil, nil
		}
	}
	status.Executable = text(install.executable)
	status.InstallMethod = install.method.label()
	status.OtherInstallations = slices.Clone(install.others)
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
	if prefix, ok := stagedPrefix(env, fresh); ok {
		return stagedUpdate(env, id, prefix, latest)
	}
	// On Windows only npm needs the staging above. Codex's and Pi's own
	// updaters install a new release beside the old one and then switch to
	// it, which a running client does not hinder.
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

// findDamageWhenGuarded is a broken npm installation PATH no longer
// finds, where updates are guarded.
func findDamageWhenGuarded(env *Environment, id ClientID) *damage {
	if !guarded(env) {
		return nil
	}
	return findDamage(env, id)
}

// stagedPrefix is the npm prefix of an installation updated through a
// staging prefix: an npm installation on Windows, where npm stopped midway
// or missing a download leaves a client that no longer starts. Pi's own
// updater runs the same npm command, so Pi is installed the same way here.
func stagedPrefix(env *Environment, install *installation) (string, bool) {
	if install.method.kind == methodNpm && guarded(env) {
		return install.method.prefix, true
	}
	return "", false
}

// stagedPrefixOf is the npm prefix of a damaged installation, from the
// path of its command.
func stagedPrefixOf(env *Environment, executable string) string {
	directory := filepath.Dir(executable)
	if env.windows() {
		return directory
	}
	return filepath.Dir(directory)
}

// performRepair repairs a damaged npm installation: puts back the version
// from before an interrupted update, or installs the latest one again.
func performRepair(env *Environment, id ClientID, latest *clientVersion) (clientVersion, *clientError) {
	recoverAll(env, id)
	var found *damage
	if install := detect(env, id); install != nil {
		if install.method.kind != methodNpm {
			return clientVersion{}, newError("changed", "Client installation changed")
		}
		version, failure := currentVersion(env, id, install)
		if failure == nil {
			return version, nil
		}
		found = findDamageIn(env, id, install.method.prefix, failure)
	} else {
		found = findDamage(env, id)
	}
	if found == nil {
		return clientVersion{}, newError("changed", "Client installation changed")
	}
	if found.restore != nil {
		return restoreDamage(env, id, found)
	}
	if latest == nil {
		return clientVersion{}, newError("version", "Could not read the latest version")
	}
	return stagedUpdate(env, id, found.prefix, *latest)
}

// failedUpdate is a failed update and what it left behind: the version
// that still starts (kept), or what remains of a client that no longer
// does (damaged).
type failedUpdate struct {
	failure *clientError
	kept    *clientVersion
	damaged *damage
}

// afterFailure checks the client again after a failed update on Windows:
// whether the earlier version still starts, or what can repair it.
func afterFailure(env *Environment, id ClientID, failure *clientError) failedUpdate {
	result := failedUpdate{failure: failure}
	// Other systems report a failed update as before.
	if !guarded(env) {
		return result
	}
	after := detect(env, id)
	if after == nil {
		result.damaged = findDamage(env, id)
		return result
	}
	version, problem := currentVersion(env, id, after)
	switch {
	case problem == nil:
		result.kept = &version
	case after.method.kind == methodNpm:
		result.damaged = findDamageIn(env, id, after.method.prefix, problem)
	}
	return result
}

// npmLatest is the latest release of a client installed with npm, for
// reinstalling one that no longer starts.
func npmLatest(ctx context.Context, client *http.Client, env *Environment, id ClientID, prefix string) (clientVersion, *clientError) {
	install := &installation{method: npmAt(prefix), others: []string{}}
	return fetchLatest(ctx, client, latestSourceOf(env, id, install), install, id)
}

// check checks ids in one place, this computer or a WSL distribution,
// then updates or repairs those that can be.
func (m *Manager) check(ctx context.Context, client *http.Client, distribution *string, env *Environment, failure *clientError, ids []ClientID, act action) {
	if failure != nil {
		m.publish(func(s *ClientSnapshot) {
			for i := range s.Clients {
				item := &s.Clients[i]
				if item.at(distribution, ids) && slices.Contains([]string{"checking", "updating", "repairing"}, item.Phase) {
					item.fail(newError(failure.code, failure.detail))
				}
			}
		})
		return
	}
	for _, id := range ids {
		status, install, latest := inspect(ctx, env, client, id)
		switch {
		case act == actionUpdate && status.CanUpdate && install != nil:
			status.Phase = "updating"
			status.CanUpdate = false
			m.replace(status)
			version, failure := performUpdate(env, id, install, *latest)
			if failure == nil {
				status.CurrentVersion = text(version.String())
				status.Phase = "updated"
				status.CheckedAt = now()
				break
			}
			switch result := afterFailure(env, id, failure); {
			case result.kept != nil:
				kept := result.kept.String()
				unchanged := status.CurrentVersion != nil && *status.CurrentVersion == kept
				status.KeptPrevious = unchanged && failure.code != "running"
				status.CurrentVersion = text(kept)
				status.fail(failure)
			case result.damaged != nil:
				status.damaged(result.damaged)
				status.ErrorDetail = text(failure.detail + "\n" + result.damaged.detail)
			default:
				status.fail(failure)
			}
		case act == actionRepair && status.Phase == "damaged":
			status.Phase = "repairing"
			m.replace(status)
			var latest *clientVersion
			var failure *clientError
			if status.Repair != nil && *status.Repair == "reinstall" && status.Executable != nil {
				var version clientVersion
				if version, failure = npmLatest(ctx, client, env, id, stagedPrefixOf(env, *status.Executable)); failure == nil {
					latest = &version
				}
			}
			var version clientVersion
			if failure == nil {
				version, failure = performRepair(env, id, latest)
			}
			if failure != nil {
				status.fail(failure)
				break
			}
			status.CurrentVersion = text(version.String())
			status.Phase = "repaired"
			status.ErrorDetail = nil
			status.CheckedAt = now()
		}
		m.replace(status)
	}
}

// checkWSL checks clients in WSL distributions. A refresh lists what each
// running distribution has and never starts a stopped one. An update
// reaches the distribution the user chose, which starts it if stopped.
func (m *Manager) checkWSL(ctx context.Context, client *http.Client, update []ClientTarget, act action, useSystemProxy bool) {
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
			m.check(ctx, client, text(name), env, failure, ids[name], act)
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
