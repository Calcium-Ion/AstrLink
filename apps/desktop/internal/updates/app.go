package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

const (
	checkInterval = 3 * time.Hour
	// firstCheckDelay leaves the launch to settle before the first check.
	firstCheckDelay = 30 * time.Second
	checkTimeout    = 30 * time.Second
	downloadTimeout = 30 * time.Minute
	// progressInterval spaces the download progress events.
	progressInterval = 150 * time.Millisecond
	// schedulerWake re-reads the wall-clock deadline at least this often,
	// so a Mac that slept checks when About says it will.
	schedulerWake = time.Minute
)

// retryIntervals space the checks after a failure, then checkInterval.
var retryIntervals = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// checkSchedule is when the next automatic check runs.
type checkSchedule struct {
	next     time.Time
	failures int
}

func newSchedule(now time.Time) checkSchedule { return checkSchedule{next: now.Add(firstCheckDelay)} }

func (s *checkSchedule) restart(now time.Time) {
	s.next = now
	s.failures = 0
}

func (s *checkSchedule) completed(now time.Time, failed bool) {
	delay := checkInterval
	if failed {
		if s.failures < len(retryIntervals) {
			delay = retryIntervals[s.failures]
		}
		s.failures = min(s.failures+1, len(retryIntervals))
	} else {
		s.failures = 0
	}
	s.next = now.Add(delay)
}

// delay is how long until the next check. A preference change may ask for
// one at once, but cannot bypass the server's rate limit; wall-clock
// deadlines also survive system sleep.
func (s checkSchedule) delay(now, retryAfter time.Time) time.Duration {
	next := s.next
	if retryAfter.After(next) {
		next = retryAfter
	}
	return max(next.Sub(now), 0)
}

// UpdatePhase is where an app update is.
type UpdatePhase string

const (
	PhaseIdle        UpdatePhase = "idle"
	PhaseChecking    UpdatePhase = "checking"
	PhaseNoReleases  UpdatePhase = "no_releases"
	PhaseUpToDate    UpdatePhase = "up_to_date"
	PhaseAvailable   UpdatePhase = "available"
	PhaseManual      UpdatePhase = "manual"
	PhaseDownloading UpdatePhase = "downloading"
	PhaseReady       UpdatePhase = "ready"
	PhaseInstalling  UpdatePhase = "installing"
	PhaseError       UpdatePhase = "error"
)

// DownloadSource is where an update downloads from.
type DownloadSource string

const (
	SourceGitHub       DownloadSource = "github"
	SourceDownloadSite DownloadSource = "download_site"
)

// ReleaseInfo is the release an update installs.
type ReleaseInfo struct {
	Version     string  `json:"version"`
	Notes       string  `json:"notes"`
	PublishedAt *string `json:"published_at"`
	URL         string  `json:"url"`
}

// UpdateSnapshot is the update state About shows (app-update-status).
type UpdateSnapshot struct {
	Revision         uint64                  `json:"revision"`
	CurrentVersion   string                  `json:"current_version"`
	LatestVersion    *string                 `json:"latest_version"`
	Platform         string                  `json:"platform"`
	Arch             string                  `json:"arch"`
	InstallSupported bool                    `json:"install_supported"`
	Configured       bool                    `json:"configured"`
	Development      bool                    `json:"development"`
	Preferences      prefs.UpdatePreferences `json:"preferences"`
	Phase            UpdatePhase             `json:"phase"`
	Release          *ReleaseInfo            `json:"release"`
	DownloadedBytes  int64                   `json:"downloaded_bytes"`
	TotalBytes       *int64                  `json:"total_bytes"`
	DownloadSource   *DownloadSource         `json:"download_source"`
	LastCheckedAt    *string                 `json:"last_checked_at"`
	ErrorCode        *string                 `json:"error_code"`
	ErrorDetail      *string                 `json:"error_detail"`
	// RetryAt is when GitHub accepts the next check after a rate limit.
	RetryAt *string `json:"retry_at"`
}

// Installer installs an update a check found (a *mygo.Update).
type Installer interface {
	Install(ctx context.Context, progress func(downloaded, total int64)) error
}

// Opener opens a file an update manifest names.
type Opener func(ctx context.Context, url string) (io.ReadCloser, error)

// Updater hands the app's updater (mygo.Updater) the manifest a check read
// and where the files it names come from.
type Updater interface {
	// Check returns the update the manifest offers, nil when it is not
	// newer than the running app.
	Check(ctx context.Context, manifest []byte, open Opener) (Installer, error)
}

// Core is the Core process an install stops (core.Manager).
type Core interface {
	// BeginUpdate keeps Core from starting until release.
	BeginUpdate() (release func(), running bool, err error)
	StopAndWait(ctx context.Context) error
	// WaitForProcessTreeExit waits until the helpers of stopped Cores have
	// exited too, which may still hold files the installer replaces.
	WaitForProcessTreeExit(ctx context.Context, timeout time.Duration) error
	Start() error
}

// helperExitTimeout is how long an install waits for Core's helpers to
// exit after Core.
const helperExitTimeout = 10 * time.Second

// Config is what a Manager works with.
type Config struct {
	// CurrentVersion is the running app's version; Platform and Arch name
	// the system as About shows it (macos, aarch64); Target is the
	// manifest's platform (darwin-arm64).
	CurrentVersion   string
	Platform, Arch   string
	Target           string
	Development      bool
	InstallSupported bool
	// PublicKey is updates.publicKey, "" for a build without updates.
	PublicKey string
	// CacheDir keeps the downloaded update.
	CacheDir    string
	Preferences func() prefs.Preferences
	SaveUpdates func(prefs.UpdatePreferences) error
	// Publish hands the main window each snapshot; Announce shows a state
	// worth the operator's attention, about.phase.<kind>, once per version.
	Publish  func(UpdateSnapshot)
	Announce func(kind, version string)
	Core     Core
	Updater  Updater
	// Relaunch starts the installed version.
	Relaunch func()
	// RelaunchAppImage quits and starts the AppImage file with args, in
	// place of Relaunch when the app runs from an AppImage; nil quits the
	// app through mygo (relaunchAppImage).
	RelaunchAppImage func(file string, args []string)
	// Transport goes through the system proxy, or straight out.
	Transport func(useSystemProxy bool) http.RoundTripper
	Now       func() time.Time
}

// pending is the update a check found.
type pending struct {
	tag       string
	manifest  manifest
	file      packageFile
	installer Installer
}

type announcement struct{ kind, version string }

// Manager checks for, downloads and installs app updates.
type Manager struct {
	cfg   Config
	key   ed25519.PublicKey
	cache cache
	// appImage is the AppImage file the app runs from, "" otherwise: its
	// update is the release's AppImage, which replaces the file.
	appImage string
	// deb is set when the Debian package installed the app: its update is
	// the release's package, which apt installs.
	deb bool
	// operation is held by the check, download or install that runs.
	operation chan struct{}
	// wake asks the scheduler to read its deadline again.
	wake chan struct{}

	mu         sync.Mutex
	snapshot   UpdateSnapshot
	schedule   checkSchedule
	generation uint64
	// generationCtx ends when a channel change abandons what runs.
	generationCtx    context.Context
	cancelGeneration context.CancelFunc
	pending          *pending
	// retryAfter is wall-clock time, so a sleeping Mac ends the wait when
	// About says it does.
	retryAfter time.Time
	// notified is the last state announced, so periodic checks do not
	// repeat it.
	notified *announcement
}

// NewManager returns a manager that has not checked yet.
func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RelaunchAppImage == nil {
		cfg.RelaunchAppImage = relaunchAppImage
	}
	m := &Manager{
		cfg:       cfg,
		cache:     cache{dir: cfg.CacheDir},
		operation: make(chan struct{}, 1),
		wake:      make(chan struct{}, 1),
		schedule:  newSchedule(cfg.Now()),
	}
	if key, err := parsePublicKey(cfg.PublicKey); err == nil {
		m.key = key
	}
	if file, ok := appImageFile(); ok {
		m.appImage = file
	} else {
		m.deb = debInstalled()
	}
	m.generationCtx, m.cancelGeneration = context.WithCancel(context.Background())
	m.snapshot = UpdateSnapshot{
		CurrentVersion:   cfg.CurrentVersion,
		Platform:         cfg.Platform,
		Arch:             cfg.Arch,
		InstallSupported: cfg.InstallSupported,
		Configured:       m.key != nil,
		Development:      cfg.Development,
		Preferences:      cfg.Preferences().Updates,
		Phase:            PhaseIdle,
	}
	return m
}

// UpdateSnapshot is the update state.
func (m *Manager) Snapshot() UpdateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot
}

// notice is the state worth the operator's attention. available counts
// only while it waits on them; automatic downloads announce completion or
// failure.
func notice(phase UpdatePhase, autoDownload bool) string {
	switch phase {
	case PhaseAvailable:
		if !autoDownload {
			return "available"
		}
	case PhaseManual, PhaseReady, PhaseError:
		return string(phase)
	}
	return ""
}

// publish applies change, then hands the main window the snapshot.
func (m *Manager) publish(change func()) {
	m.mu.Lock()
	change()
	m.snapshot.Revision++
	snapshot := m.snapshot
	var announce *announcement
	if kind := notice(snapshot.Phase, snapshot.Preferences.AutoDownload); kind != "" && snapshot.Release != nil {
		current := announcement{kind: kind, version: snapshot.Release.Version}
		if m.notified == nil || *m.notified != current {
			m.notified = &current
			announce = &current
		}
	}
	m.mu.Unlock()
	if m.cfg.Publish != nil {
		m.cfg.Publish(snapshot)
	}
	if announce != nil && m.cfg.Announce != nil {
		m.cfg.Announce(announce.kind, announce.version)
	}
}

func text(s string) *string { return &s }

// fail reports err for the operation of generation.
func (m *Manager) fail(generation uint64, err error) {
	code, detail := "download", err.Error()
	var failed *checkError
	if errors.As(err, &failed) {
		code = failed.code
	}
	m.publish(func() {
		if m.generation != generation {
			return
		}
		m.snapshot.Phase = PhaseError
		m.snapshot.DownloadSource = nil
		m.snapshot.ErrorCode = text(code)
		m.snapshot.ErrorDetail = text(detail)
	})
}

// tryOperation takes the operation lock, or reports one running.
func (m *Manager) tryOperation() error {
	select {
	case m.operation <- struct{}{}:
		return nil
	default:
		return errors.New("An update operation is already running")
	}
}

func (m *Manager) endOperation() { <-m.operation }

// operationContext ends with parent or when the generation it started in
// is abandoned.
func (m *Manager) operationContext(parent context.Context, generation context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(generation, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (m *Manager) httpClient(useSystemProxy, follow bool, timeout time.Duration) *http.Client {
	client := &http.Client{Transport: m.cfg.Transport(useSystemProxy), Timeout: timeout}
	if !follow {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client
}

func (m *Manager) now() time.Time { return m.cfg.Now() }

func (m *Manager) rfc3339(t time.Time) *string { return text(t.UTC().Format(time.RFC3339Nano)) }

// reschedules reports whether a preference change moves the next check. A
// caller that switches only the source retries on its own; waking the
// scheduler as well would race it for the operation lock.
func reschedules(current, input prefs.UpdatePreferences) bool {
	if current.OfficialMirror == input.OfficialMirror {
		return true
	}
	input.OfficialMirror = current.OfficialMirror
	return input != current
}

// SetPreferences saves the update settings and applies them: a channel
// change abandons the update found on the other channel.
func (m *Manager) SetPreferences(input prefs.UpdatePreferences) (UpdateSnapshot, error) {
	m.mu.Lock()
	if m.snapshot.Phase == PhaseInstalling {
		m.mu.Unlock()
		return UpdateSnapshot{}, errors.New("An update is being installed")
	}
	if err := m.cfg.SaveUpdates(input); err != nil {
		m.mu.Unlock()
		return UpdateSnapshot{}, err
	}
	current := m.snapshot.Preferences
	changedChannel := current.Channel != input.Channel
	changedMirror := current.OfficialMirror != input.OfficialMirror
	reschedule := reschedules(current, input)
	m.snapshot.Preferences = input
	if reschedule {
		m.schedule.restart(m.now())
	}
	if changedChannel || changedMirror {
		// Each channel and the mirror read separate sources, so a rate
		// limit does not carry over.
		m.retryAfter = time.Time{}
		m.snapshot.RetryAt = nil
	}
	if changedChannel {
		m.generation++
		m.cancelGeneration()
		m.generationCtx, m.cancelGeneration = context.WithCancel(context.Background())
		m.pending = nil
		m.snapshot.Release = nil
		m.snapshot.LatestVersion = nil
		m.snapshot.Phase = PhaseIdle
		m.snapshot.ErrorCode = nil
		m.snapshot.ErrorDetail = nil
		m.snapshot.DownloadedBytes = 0
		m.snapshot.TotalBytes = nil
		m.snapshot.DownloadSource = nil
		m.cache.clear()
	}
	m.mu.Unlock()
	m.publish(func() {})
	if reschedule {
		m.wakeScheduler()
	}
	return m.Snapshot(), nil
}

func (m *Manager) wakeScheduler() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// automaticCheckDelay is how long until the next automatic check, false
// when automatic checks are off.
func (m *Manager) automaticCheckDelay() (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot.Development || !m.snapshot.Preferences.AutoCheck {
		return 0, false
	}
	return m.schedule.delay(m.now(), m.retryAfter), true
}

// Run checks automatically until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		delay, on := m.automaticCheckDelay()
		switch {
		case on && delay <= 0:
			// A manual operation may finish or preferences may change while
			// waiting; read the deadline again before checking.
			select {
			case m.operation <- struct{}{}:
			case <-ctx.Done():
				return
			}
			if delay, on := m.automaticCheckDelay(); on && delay <= 0 {
				m.checkLocked(ctx)
			}
			m.endOperation()
		case on:
			timer := time.NewTimer(min(delay, schedulerWake))
			select {
			case <-timer.C:
			case <-m.wake:
			case <-ctx.Done():
				timer.Stop()
				return
			}
			timer.Stop()
		default:
			select {
			case <-m.wake:
			case <-ctx.Done():
				return
			}
		}
	}
}

// scheduleNextCheck plans the next automatic check after an operation of
// generation; manual checks and downloads also replace a pending retry.
func (m *Manager) scheduleNextCheck(generation uint64) {
	m.mu.Lock()
	if m.generation != generation {
		m.mu.Unlock()
		return
	}
	m.schedule.completed(m.now(), m.snapshot.Phase == PhaseError)
	m.mu.Unlock()
	m.wakeScheduler()
}

// Check looks for an update now, and downloads it when automatic downloads
// are on.
func (m *Manager) Check(ctx context.Context) (UpdateSnapshot, error) {
	if err := m.tryOperation(); err != nil {
		return UpdateSnapshot{}, err
	}
	defer m.endOperation()
	return m.checkLocked(ctx), nil
}

func (m *Manager) checkLocked(parent context.Context) UpdateSnapshot {
	m.mu.Lock()
	generation, generationCtx := m.generation, m.generationCtx
	limited := m.retryAfter.After(m.now())
	m.mu.Unlock()
	if limited {
		// The snapshot already reports the rate limit and when it lifts.
		return m.Snapshot()
	}
	m.publish(func() {
		m.pending = nil
		m.retryAfter = time.Time{}
		m.snapshot.Phase = PhaseChecking
		m.snapshot.ErrorCode = nil
		m.snapshot.ErrorDetail = nil
		m.snapshot.RetryAt = nil
	})
	ctx, cancel := m.operationContext(parent, generationCtx)
	err := m.checkInner(ctx, generation)
	cancel()
	if generationCtx.Err() != nil || parent.Err() != nil {
		return m.Snapshot()
	}
	if err != nil {
		m.fail(generation, err)
	}
	if snapshot := m.Snapshot(); snapshot.Phase == PhaseAvailable && snapshot.Preferences.AutoDownload {
		m.downloadLocked(parent, generation)
	}
	m.scheduleNextCheck(generation)
	return m.Snapshot()
}

// manifestName is the manifest of this platform's update, or of the
// AppImage the app runs from, or of the Debian package that installed it.
func (m *Manager) manifestName() string {
	if m.appImage != "" {
		return appImageManifestName(m.cfg.Target)
	}
	if m.deb {
		return debManifestName(m.cfg.Target)
	}
	return "update-" + m.cfg.Target + ".json"
}

func (m *Manager) checkInner(ctx context.Context, generation uint64) error {
	values := m.cfg.Preferences()
	look := lookup{
		follow:       m.httpClient(values.UseSystemProxy, true, checkTimeout),
		direct:       m.httpClient(values.UseSystemProxy, false, checkTimeout),
		now:          m.now,
		manifestName: m.manifestName(),
	}
	mirror := values.Updates.OfficialMirror
	preview := values.Updates.Channel == prefs.ChannelPreview
	var retry int64
	var releases []githubRelease
	var err error
	switch {
	case mirror:
		releases, err = look.fetchMirrorReleases(ctx, mirrorReleaseAPI)
	case preview:
		releases, err = look.fetchReleases(ctx, releaseAPI, &retry)
	default:
		releases, err = look.fetchLatest(ctx, latestDownload+m.manifestName(), &retry)
	}
	if err != nil {
		if retry > 0 {
			until := m.now().Add(time.Duration(retry) * time.Second)
			m.mu.Lock()
			if m.generation == generation {
				m.retryAfter = until
				m.snapshot.RetryAt = m.rfc3339(until)
			}
			m.mu.Unlock()
		}
		return err
	}
	same := func(change func()) func() {
		return func() {
			if m.generation == generation {
				change()
			}
		}
	}
	m.publish(same(func() { m.snapshot.LastCheckedAt = m.rfc3339(m.now()) }))
	selected := selectRelease(releases, preview)
	if selected == nil {
		m.publish(same(func() {
			m.pending = nil
			m.snapshot.Release = nil
			m.snapshot.Phase = PhaseNoReleases
			m.snapshot.LatestVersion = nil
		}))
		return nil
	}
	version, _ := tagVersion(selected.TagName)
	m.publish(same(func() { m.snapshot.LatestVersion = text(version.String()) }))
	if current, ok := tagVersion(m.cfg.CurrentVersion); ok && version.compare(current) <= 0 {
		m.publish(same(func() {
			m.pending = nil
			m.snapshot.Release = nil
			m.snapshot.Phase = PhaseUpToDate
		}))
		return nil
	}
	info := &ReleaseInfo{
		Version:     version.String(),
		PublishedAt: selected.PublishedAt,
		URL:         repository + "/releases/tag/" + selected.TagName,
	}
	if selected.Body != nil {
		info.Notes = *selected.Body
	}
	m.publish(same(func() { m.snapshot.Release = info }))
	if snapshot := m.Snapshot(); snapshot.Development || !snapshot.InstallSupported || !snapshot.Configured {
		m.publish(same(func() { m.snapshot.Phase = PhaseManual }))
		return nil
	}

	body := selected.manifest
	if body == nil || mirror {
		var asset *releaseAsset
		for i, a := range selected.Assets {
			if a.Name == m.manifestName() && githubAsset(a.BrowserDownloadURL, selected.TagName) {
				asset = &selected.Assets[i]
				break
			}
		}
		if asset == nil {
			return failure("missing_artifact", "Release has no %s update manifest", m.manifestName())
		}
		endpoint := asset.BrowserDownloadURL
		if mirror {
			endpoint = downloadSite + "/releases/download/" + encodeComponent(selected.TagName) + "/" + m.manifestName()
		}
		if body, err = m.fetchManifest(ctx, look, endpoint); err != nil {
			return err
		}
	}
	update, err := parseManifest(body, selected.TagName, version)
	if err != nil {
		return err
	}
	var installer Installer
	switch {
	case m.appImage != "":
		// The AppImage is always installed whole.
		update.Deltas = nil
		installer = appImageInstaller{target: m.appImage, file: update.firstFile(m.cfg.CurrentVersion), key: m.key, open: m.openPackage}
	case m.deb:
		// So is the Debian package.
		update.Deltas = nil
		installer = debInstaller{dir: os.TempDir(), file: update.firstFile(m.cfg.CurrentVersion), key: m.key, open: m.openPackage}
	default:
		installer, err = m.cfg.Updater.Check(ctx, body, m.openPackage)
		if err != nil {
			return failure("manifest", "%v", err)
		}
		if installer == nil {
			return failure("manifest", "Release and update manifest versions disagree")
		}
	}
	file := update.firstFile(m.cfg.CurrentVersion)
	cached := m.cache.verified(file, m.key) == nil
	if !cached {
		_ = os.Remove(m.cache.path(file))
	}
	m.publish(same(func() {
		m.snapshot.Phase = PhaseAvailable
		m.snapshot.DownloadedBytes = 0
		m.snapshot.TotalBytes = nil
		if cached {
			m.snapshot.Phase = PhaseReady
			m.snapshot.DownloadedBytes = file.Size
			m.snapshot.TotalBytes = &file.Size
		}
		m.pending = &pending{tag: selected.TagName, manifest: update, file: file, installer: installer}
	}))
	return nil
}

func (m *Manager) fetchManifest(ctx context.Context, look lookup, endpoint string) ([]byte, error) {
	response, err := look.get(ctx, look.follow, endpoint, "")
	if err != nil {
		return nil, failure("manifest", "%v", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, failure("missing_artifact", "Release has no %s update manifest", m.manifestName())
	}
	if response.StatusCode != http.StatusOK {
		return nil, failure("manifest", "HTTP status %s", statusText(response))
	}
	body, err := readLimited(response.Body, maxManifestBytes)
	if err != nil {
		return nil, failure("manifest", "%v", err)
	}
	return body, nil
}

// openPackage serves the app's updater the files of the pending update:
// the verified download, or, as when a delta fails to make the new
// version, the release's file from where downloads come from.
func (m *Manager) openPackage(ctx context.Context, url string) (io.ReadCloser, error) {
	m.mu.Lock()
	p := m.pending
	m.mu.Unlock()
	if p == nil {
		return nil, errors.New("no update is pending")
	}
	if url == p.file.URL {
		if file, err := os.Open(m.cache.path(p.file)); err == nil {
			return file, nil
		}
	}
	values := m.cfg.Preferences()
	candidate, ok := downloadCandidate(url, p.tag, values.Updates.OfficialMirror)
	if !ok {
		return nil, fmt.Errorf("%s is not a file of the release", url)
	}
	response, err := m.get(ctx, m.httpClient(values.UseSystemProxy, true, downloadTimeout), candidate)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

func (m *Manager) get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "mygo-updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Download request failed with status: %s", statusText(response))
	}
	return response, nil
}

// Download downloads and verifies the update a check found.
func (m *Manager) Download(ctx context.Context) (UpdateSnapshot, error) {
	if err := m.tryOperation(); err != nil {
		return UpdateSnapshot{}, err
	}
	defer m.endOperation()
	m.mu.Lock()
	generation := m.generation
	m.mu.Unlock()
	m.downloadLocked(ctx, generation)
	m.scheduleNextCheck(generation)
	return m.Snapshot(), nil
}

func (m *Manager) downloadLocked(parent context.Context, generation uint64) {
	m.mu.Lock()
	p, phase, generationCtx := m.pending, m.snapshot.Phase, m.generationCtx
	stale := m.generation != generation
	m.mu.Unlock()
	if p == nil || phase == PhaseReady || stale {
		return
	}
	values := m.cfg.Preferences()
	candidate, ok := downloadCandidate(p.file.URL, p.tag, values.Updates.OfficialMirror)
	if !ok {
		m.fail(generation, failure("download", "Update manifest does not name a supported release asset"))
		return
	}
	source := DownloadSource(candidateSource(candidate))
	same := func(change func()) func() {
		return func() {
			if m.generation == generation {
				change()
			}
		}
	}
	m.publish(same(func() {
		m.snapshot.Phase = PhaseDownloading
		m.snapshot.DownloadedBytes = 0
		m.snapshot.TotalBytes = nil
		m.snapshot.DownloadSource = &source
		m.snapshot.ErrorCode = nil
		m.snapshot.ErrorDetail = nil
	}))
	ctx, cancel := m.operationContext(parent, generationCtx)
	err := m.fetchPackage(ctx, generation, m.httpClient(values.UseSystemProxy, true, downloadTimeout), candidate, p.file)
	cancel()
	if generationCtx.Err() != nil || parent.Err() != nil {
		m.publish(same(func() {
			m.snapshot.Phase = PhaseAvailable
			m.snapshot.DownloadedBytes = 0
			m.snapshot.TotalBytes = nil
			m.snapshot.DownloadSource = nil
			m.snapshot.ErrorCode = nil
			m.snapshot.ErrorDetail = nil
		}))
		return
	}
	if err != nil {
		m.fail(generation, err)
		return
	}
	size := p.file.Size
	m.publish(same(func() {
		m.snapshot.Phase = PhaseReady
		m.snapshot.DownloadedBytes = size
		m.snapshot.TotalBytes = &size
	}))
}

// fetchPackage downloads f from url into the cache and checks its size and
// signature before it replaces what the cache held.
func (m *Manager) fetchPackage(ctx context.Context, generation uint64, client *http.Client, url string, f packageFile) error {
	response, err := m.get(ctx, client, url)
	if err != nil {
		return failure("download", "%v", err)
	}
	defer response.Body.Close()
	if err := os.MkdirAll(m.cache.dir, 0o700); err != nil {
		return failure("download", "%v", err)
	}
	final := m.cache.path(f)
	part := strings.TrimSuffix(final, filepath.Ext(final)) + ".part"
	out, err := os.Create(part)
	if err != nil {
		return failure("download", "%v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(part)
		}
	}()
	total := f.Size
	var downloaded int64
	last := m.now()
	progress := func(n int) {
		downloaded += int64(n)
		if now := m.now(); now.Sub(last) >= progressInterval {
			last = now
			current := downloaded
			m.publish(func() {
				if m.generation == generation {
					m.snapshot.DownloadedBytes = current
					m.snapshot.TotalBytes = &total
				}
			})
		}
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum, progressWriter(progress)), io.LimitReader(response.Body, f.Size+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return failure("download", "%v", err)
	}
	if n > f.Size {
		return failure("verification", "Update package is larger than its manifest says")
	}
	if n != f.Size {
		return failure("verification", "Update package has %d bytes, want %d", n, f.Size)
	}
	if err := verifyDigest(m.key, sum.Sum(nil), f.Signature); err != nil {
		return failure("verification", "%v", err)
	}
	// Commit under the lock a channel change takes, so a cancelled download
	// never reappears.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation {
		return nil
	}
	m.cache.clearExcept(part)
	if err := os.Rename(part, final); err != nil {
		return failure("verification", "%v", err)
	}
	committed = true
	return nil
}

type progressWriter func(n int)

func (p progressWriter) Write(b []byte) (int, error) {
	p(len(b))
	return len(b), nil
}

// Install stops Core, installs the verified update and starts the new
// version; when installing fails, Core starts again if it ran.
func (m *Manager) Install(ctx context.Context) (UpdateSnapshot, error) {
	if err := m.tryOperation(); err != nil {
		return UpdateSnapshot{}, err
	}
	defer m.endOperation()
	// An install that started finishes even if the page that asked goes.
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	if m.snapshot.Development || m.snapshot.Phase != PhaseReady {
		m.mu.Unlock()
		return UpdateSnapshot{}, errors.New("No verified update is ready to install")
	}
	p := m.pending
	if p == nil {
		m.mu.Unlock()
		return UpdateSnapshot{}, errors.New("No pending update")
	}
	m.snapshot.Phase = PhaseInstalling
	generation := m.generation
	m.mu.Unlock()
	m.publish(func() {})
	if err := m.cache.verified(p.file, m.key); err != nil {
		_ = os.Remove(m.cache.path(p.file))
		m.fail(generation, failure("verification", "%v", err))
		return m.Snapshot(), nil
	}
	release, running, err := m.cfg.Core.BeginUpdate()
	if err != nil {
		m.fail(generation, failure("shutdown", "%v", err))
		return m.Snapshot(), nil
	}
	if err := m.cfg.Core.StopAndWait(ctx); err != nil {
		release()
		m.fail(generation, failure("shutdown", "%v", err))
		return m.Snapshot(), nil
	}
	// The installer waits for files still in use itself, so a helper that
	// outlives the wait is only logged.
	if err := m.cfg.Core.WaitForProcessTreeExit(ctx, helperExitTimeout); err != nil {
		log.Printf("astrlink: installing the update before the gateway fully exited: %v", err)
	}
	if err := p.installer.Install(ctx, nil); err != nil {
		release()
		detail := err.Error()
		if running {
			if err := m.cfg.Core.Start(); err != nil {
				detail += "; gateway recovery failed: " + err.Error()
			}
		}
		if errors.Is(err, errDebNotAuthorized) {
			// Dismissed: the update stays ready to install.
			m.publish(func() {
				if m.generation == generation {
					m.snapshot.Phase = PhaseReady
				}
			})
			return m.Snapshot(), nil
		}
		m.fail(generation, failure("install", "%s", detail))
		return m.Snapshot(), nil
	}
	m.cache.clear()
	// Core stays stopped: the new version starts its own.
	if m.appImage != "" {
		m.cfg.RelaunchAppImage(m.appImage, os.Args[1:])
	} else {
		m.cfg.Relaunch()
	}
	return m.Snapshot(), nil
}

// ParsePreferences reads the update settings About sends, rejecting
// unknown fields and channels.
func ParsePreferences(input json.RawMessage) (prefs.UpdatePreferences, error) {
	var values prefs.UpdatePreferences
	if err := json.Unmarshal(input, &values); err != nil {
		return prefs.UpdatePreferences{}, err
	}
	return values, nil
}
