package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// fakeReleases stands in for github.com, api.github.com,
// download.astrlink.com and astrlink.com: requests to them are rewritten to
// one test server, which answers by host and path.
type fakeReleases struct {
	t   *testing.T
	key testKey

	mu sync.Mutex
	// latest is the tag github.com/…/releases/latest redirects to, "" for
	// a 404; latestStatus answers instead when set.
	latest       string
	latestStatus int
	// apiReleases is what api.github.com lists; apiStatus answers instead.
	apiReleases []githubRelease
	apiStatus   int
	apiHeaders  map[string]string
	// mirror is what astrlink.com/api/releases lists.
	mirror []githubRelease
	// files are served by path on github.com and download.astrlink.com.
	files map[string][]byte
	// requests counts requests per host.
	requests map[string]int
	// hold, when set, blocks package downloads until closed.
	hold chan struct{}
	// served is told when a held download started.
	served chan struct{}
}

func newFakeReleases(t *testing.T) *fakeReleases {
	return &fakeReleases{t: t, key: newTestKey(t), files: map[string][]byte{}, requests: map[string]int{}}
}

const testTarget = "linux-amd64"

// publish adds a release of version with a signed archive and its
// manifest, on GitHub and the download site.
func (f *fakeReleases) publish(version string, prerelease bool) githubRelease {
	f.mu.Lock()
	defer f.mu.Unlock()
	tag := "v" + version
	archive := []byte("archive of " + version + strings.Repeat(".", 4096))
	name := "astrlink-" + version + "-" + testTarget + ".tar.gz"
	m := manifest{
		Version:   version,
		Notes:     "notes of " + version,
		Date:      "2026-11-01T00:00:00Z",
		URL:       "https://github.com/Calcium-Ion/AstrLink/releases/download/" + tag + "/" + name,
		Size:      int64(len(archive)),
		Signature: f.key.sign(archive),
	}
	body, _ := json.Marshal(m)
	for _, prefix := range []string{repositoryPath, downloadSitePath} {
		f.files[prefix+tag+"/"+name] = archive
		f.files[prefix+tag+"/update-"+testTarget+".json"] = body
	}
	notes := "notes of " + version
	published := "2026-11-01T00:00:00Z"
	r := githubRelease{
		TagName: tag, Prerelease: prerelease, Body: &notes, PublishedAt: &published,
		Assets: []releaseAsset{
			{Name: "update-" + testTarget + ".json", BrowserDownloadURL: "https://github.com/Calcium-Ion/AstrLink/releases/download/" + tag + "/update-" + testTarget + ".json"},
			{Name: name, BrowserDownloadURL: m.URL},
		},
	}
	f.apiReleases = append(f.apiReleases, r)
	f.mirror = append(f.mirror, r)
	if !prerelease {
		f.latest = tag
	}
	return r
}

func (f *fakeReleases) count(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[host]
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Header.Get("X-Original-Host")
	f.mu.Lock()
	f.requests[host]++
	f.mu.Unlock()
	path := r.URL.EscapedPath()
	path = strings.NewReplacer("%2B", "+", "%2b", "+").Replace(path)
	switch {
	case host == "api.github.com":
		f.mu.Lock()
		status, headers, releases := f.apiStatus, f.apiHeaders, f.apiReleases
		f.mu.Unlock()
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		if status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, "[]")
			return
		}
		_ = json.NewEncoder(w).Encode(releases)
	case host == "astrlink.com" && path == "/api/releases":
		f.mu.Lock()
		releases := f.mirror
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(releases)
	case host == "github.com" && strings.HasPrefix(path, "/Calcium-Ion/AstrLink/releases/latest/download/"):
		f.mu.Lock()
		latest, status := f.latest, f.latestStatus
		f.mu.Unlock()
		if status != 0 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(status)
			return
		}
		if latest == "" {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path, "/Calcium-Ion/AstrLink/releases/latest/download/")
		w.Header().Set("Location", "https://github.com/Calcium-Ion/AstrLink/releases/download/"+latest+"/"+name)
		w.WriteHeader(http.StatusFound)
	case host == "github.com" || host == "download.astrlink.com":
		f.mu.Lock()
		data, ok := f.files[path]
		hold, served := f.hold, f.served
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if hold != nil && strings.HasSuffix(path, ".tar.gz") {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data[:10])
			w.(http.Flusher).Flush()
			served <- struct{}{}
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write(data[10:])
			return
		}
		_, _ = w.Write(data)
	default:
		f.t.Errorf("unexpected request to %s%s", host, path)
		http.NotFound(w, r)
	}
}

// rewrite sends every request to the test server, keeping the host it was
// meant for.
type rewrite struct {
	server *httptest.Server
}

func (rt rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	target, _ := url.Parse(rt.server.URL)
	clone := r.Clone(r.Context())
	clone.Header.Set("X-Original-Host", r.URL.Host)
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	clone.Host = target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// fakeCore records what an install did to Core.
type fakeCore struct {
	mu                       sync.Mutex
	running                  bool
	stops, starts, released  int
	stopErr, startErr, begin error
	// events records the install's calls in order; waitErr is what the
	// wait for Core's helpers returns.
	events  []string
	waitErr error
}

func (c *fakeCore) WaitForProcessTreeExit(_ context.Context, timeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, "wait "+timeout.String())
	return c.waitErr
}

func (c *fakeCore) BeginUpdate() (func(), bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.begin != nil {
		return nil, false, c.begin
	}
	return func() {
		c.mu.Lock()
		c.released++
		c.mu.Unlock()
	}, c.running, nil
}

func (c *fakeCore) StopAndWait(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	c.events = append(c.events, "stop")
	if c.stopErr != nil {
		return c.stopErr
	}
	c.running = false
	return nil
}

func (c *fakeCore) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	if c.startErr != nil {
		return c.startErr
	}
	c.running = true
	return nil
}

// fakeUpdater checks manifests as mygo.Updater does and installs by
// reading the archive through the source.
type fakeUpdater struct {
	current    string
	installErr error
	installed  []byte
	// core records the install among Core's calls.
	core *fakeCore
}

type fakeInstall struct {
	u        *fakeUpdater
	manifest manifest
	open     Opener
}

func (u *fakeUpdater) Check(ctx context.Context, body []byte, open Opener) (Installer, error) {
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	next, _ := parseVersion(m.Version)
	current, _ := parseVersion(u.current)
	if next.compare(current) <= 0 {
		return nil, nil
	}
	return &fakeInstall{u: u, manifest: m, open: open}, nil
}

func (i *fakeInstall) Install(ctx context.Context, _ func(int64, int64)) error {
	if i.u.core != nil {
		i.u.core.mu.Lock()
		i.u.core.events = append(i.u.core.events, "install")
		i.u.core.mu.Unlock()
	}
	if i.u.installErr != nil {
		return i.u.installErr
	}
	body, err := i.open(ctx, i.manifest.URL)
	if err != nil {
		return err
	}
	defer body.Close()
	i.u.installed, err = io.ReadAll(body)
	return err
}

type harness struct {
	t        *testing.T
	releases *fakeReleases
	server   *httptest.Server
	core     *fakeCore
	updater  *fakeUpdater
	values   prefs.Preferences
	saved    []prefs.UpdatePreferences
	now      time.Time

	mu         sync.Mutex
	published  []UpdateSnapshot
	announced  []string
	relaunched int
	m          *Manager
}

func newHarness(t *testing.T, current string, configure func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, releases: newFakeReleases(t), core: &fakeCore{running: true},
		updater: &fakeUpdater{current: current}, values: prefs.Default(18317), now: noon}
	h.updater.core = h.core
	h.values.Updates.AutoDownload = false
	h.server = httptest.NewServer(h.releases)
	t.Cleanup(h.server.Close)
	cfg := Config{
		CurrentVersion: current, Platform: "linux", Arch: "x86_64", Target: testTarget,
		InstallSupported: true, PublicKey: h.releases.key.public, CacheDir: t.TempDir(),
		Preferences: func() prefs.Preferences {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.values
		},
		SaveUpdates: func(u prefs.UpdatePreferences) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.saved = append(h.saved, u)
			h.values.Updates = u
			return nil
		},
		Publish: func(s UpdateSnapshot) {
			h.mu.Lock()
			h.published = append(h.published, s)
			h.mu.Unlock()
		},
		Announce: func(kind, version string) {
			h.mu.Lock()
			h.announced = append(h.announced, kind+" "+version)
			h.mu.Unlock()
		},
		Core:      h.core,
		Updater:   h.updater,
		Relaunch:  func() { h.mu.Lock(); h.relaunched++; h.mu.Unlock() },
		Transport: func(bool) http.RoundTripper { return rewrite{h.server} },
		Now: func() time.Time {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.now
		},
	}
	if configure != nil {
		configure(&cfg)
	}
	h.m = NewManager(cfg)
	return h
}

func (h *harness) setUpdates(change func(*prefs.UpdatePreferences)) {
	h.mu.Lock()
	change(&h.values.Updates)
	h.mu.Unlock()
}

func (h *harness) check() UpdateSnapshot {
	h.t.Helper()
	s, err := h.m.Check(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestStableCheckFindsTheLatestReleaseAndDownloadsAVerifiedPackage(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.0.0", false)
	h.releases.publish("1.1.0", false)
	h.releases.publish("1.2.0-beta.1", true)
	s := h.check()
	if s.Phase != PhaseAvailable || s.Release == nil || s.Release.Version != "1.1.0" || deref(s.LatestVersion) != "1.1.0" {
		t.Fatalf("check: %+v", s)
	}
	if s.Release.URL != "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0" || s.Release.Notes != "notes of 1.1.0" {
		t.Fatalf("release %+v", s.Release)
	}
	// The stable check never spends the API quota.
	if h.releases.count("api.github.com") != 0 {
		t.Fatal("the stable channel read the API")
	}
	if s.LastCheckedAt == nil {
		t.Fatal("no check time")
	}
	s, err := h.m.Download(context.Background())
	if err != nil || s.Phase != PhaseReady || s.TotalBytes == nil || s.DownloadedBytes != *s.TotalBytes {
		t.Fatalf("download: %+v %v", s, err)
	}
	if deref((*string)(s.DownloadSource)) != "github" {
		t.Fatalf("source %v", s.DownloadSource)
	}
	for i := 1; i < len(h.published); i++ {
		if h.published[i].Revision <= h.published[i-1].Revision {
			t.Fatal("revisions do not increase")
		}
	}
	// A later check finds the download in the cache.
	s = h.check()
	if s.Phase != PhaseReady || h.releases.count("download.astrlink.com") != 0 {
		t.Fatalf("recheck: %+v", s)
	}
}

func TestPreviewChannelReadsEveryReleaseThroughTheAPI(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.releases.publish("1.2.0-beta.1", true)
	h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
	s := h.check()
	if s.Phase != PhaseAvailable || s.Release.Version != "1.2.0-beta.1" || h.releases.count("api.github.com") == 0 {
		t.Fatalf("preview: %+v", s)
	}
}

func TestTheMirrorChecksAndDownloadsFromTheDownloadSite(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	if _, err := h.m.SetPreferences(prefs.UpdatePreferences{AutoCheck: true, AutoDownload: true, Channel: prefs.ChannelStable, OfficialMirror: true}); err != nil {
		t.Fatal(err)
	}
	s := h.check()
	if s.Phase != PhaseReady || deref((*string)(s.DownloadSource)) != "download_site" {
		t.Fatalf("mirror: %+v", s)
	}
	if h.releases.count("github.com") != 0 || h.releases.count("api.github.com") != 0 {
		t.Fatalf("the mirror reached GitHub: %v", h.releases.requests)
	}
}

func TestUpToDateAndNoReleases(t *testing.T) {
	h := newHarness(t, "1.1.0", nil)
	if s := h.check(); s.Phase != PhaseNoReleases || s.Release != nil {
		t.Fatalf("no releases: %+v", s)
	}
	h.releases.publish("1.1.0", false)
	if s := h.check(); s.Phase != PhaseUpToDate || deref(s.LatestVersion) != "1.1.0" {
		t.Fatalf("up to date: %+v", s)
	}
}

func TestBuildsThatCannotInstallOfferTheReleasePage(t *testing.T) {
	for name, configure := range map[string]func(*Config){
		"development":  func(c *Config) { c.Development = true },
		"unsupported":  func(c *Config) { c.InstallSupported = false },
		"unconfigured": func(c *Config) { c.PublicKey = "" },
	} {
		h := newHarness(t, "1.0.0", configure)
		h.releases.publish("1.1.0", false)
		s := h.check()
		if s.Phase != PhaseManual || s.Release == nil {
			t.Errorf("%s: %+v", name, s)
		}
		if _, err := h.m.Install(context.Background()); err == nil {
			t.Errorf("%s: installed", name)
		}
	}
}

func TestRateLimitsHoldChecksUntilTheyLift(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.releases.latestStatus = http.StatusTooManyRequests
	s := h.check()
	if s.Phase != PhaseError || deref(s.ErrorCode) != "rate_limit" || deref(s.RetryAt) != noon.Add(120*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("rate limited: %+v", s)
	}
	requests := h.releases.count("github.com")
	// Until the limit lifts a check answers with the snapshot.
	if again := h.check(); again.Phase != PhaseError || h.releases.count("github.com") != requests {
		t.Fatal("a check went out under the rate limit")
	}
	// The scheduler waits for it too.
	if delay, on := h.m.automaticCheckDelay(); !on || delay != 120*time.Second {
		t.Fatalf("automatic check in %v", delay)
	}
	// Switching the channel reads another source, without the limit.
	if _, err := h.m.SetPreferences(prefs.UpdatePreferences{AutoCheck: true, Channel: prefs.ChannelPreview}); err != nil {
		t.Fatal(err)
	}
	if s := h.m.Snapshot(); s.RetryAt != nil {
		t.Fatal("the rate limit carried over")
	}
	h.releases.latestStatus = 0
	h.mu.Lock()
	h.now = noon.Add(3 * time.Minute)
	h.mu.Unlock()
	if s := h.check(); s.Phase != PhaseAvailable {
		t.Fatalf("after the limit: %+v", s)
	}
}

func TestAPIRateLimitsReportWhenTheyLift(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
	h.releases.apiStatus = http.StatusForbidden
	h.releases.apiHeaders = map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Limit": "60", "X-RateLimit-Reset": fmt.Sprint(noon.Unix() + 600)}
	s := h.check()
	if deref(s.ErrorCode) != "rate_limit" || deref(s.RetryAt) != noon.Add(600*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(deref(s.ErrorDetail), "0 of 60 requests left") {
		t.Fatalf("detail %q", deref(s.ErrorDetail))
	}
}

func TestAPackageSignedWithAnotherKeyIsRefused(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	// The manifest names a signature the build's key did not make.
	other := newTestKey(t)
	path := repositoryPath + "v1.1.0/update-" + testTarget + ".json"
	var m manifest
	_ = json.Unmarshal(h.releases.files[path], &m)
	m.Signature = other.sign(h.releases.files[repositoryPath+"v1.1.0/astrlink-1.1.0-"+testTarget+".tar.gz"])
	h.releases.files[path], _ = json.Marshal(m)
	h.check()
	s, err := h.m.Download(context.Background())
	if err != nil || s.Phase != PhaseError || deref(s.ErrorCode) != "verification" {
		t.Fatalf("%+v %v", s, err)
	}
	entries, _ := os.ReadDir(h.m.cache.dir)
	if len(entries) != 0 {
		t.Fatalf("the refused package stayed: %v", entries)
	}
	if _, err := h.m.Install(context.Background()); err == nil {
		t.Fatal("a refused package installed")
	}
}

func TestATruncatedOrOversizedPackageIsRefused(t *testing.T) {
	for name, change := range map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)-1] },
		"oversized": func(b []byte) []byte { return append(b, 'x') },
	} {
		h := newHarness(t, "1.0.0", nil)
		h.releases.publish("1.1.0", false)
		h.check()
		path := repositoryPath + "v1.1.0/astrlink-1.1.0-" + testTarget + ".tar.gz"
		h.releases.files[path] = change(h.releases.files[path])
		s, _ := h.m.Download(context.Background())
		if s.Phase != PhaseError || deref(s.ErrorCode) != "verification" {
			t.Errorf("%s: %+v", name, s)
		}
	}
}

func TestManifestsMustMatchTheSelectedRelease(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	path := repositoryPath + "v1.1.0/update-" + testTarget + ".json"
	var m manifest
	_ = json.Unmarshal(h.releases.files[path], &m)
	m.URL = "https://example.org/astrlink.tar.gz"
	h.releases.files[path], _ = json.Marshal(m)
	if s := h.check(); deref(s.ErrorCode) != "manifest" {
		t.Fatalf("%+v", s)
	}
	h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
	delete(h.releases.files, path)
	h.releases.apiReleases[0].Assets = h.releases.apiReleases[0].Assets[1:]
	if s := h.check(); deref(s.ErrorCode) != "missing_artifact" {
		t.Fatalf("%+v", s)
	}
}

func TestInstallStopsCoreAndRelaunches(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.m.Download(context.Background())
	s, err := h.m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.core.stops != 1 || h.core.starts != 0 || h.relaunched != 1 {
		t.Fatalf("core stops %d starts %d relaunched %d", h.core.stops, h.core.starts, h.relaunched)
	}
	// Core's helpers exit before the installer replaces their files.
	if want := []string{"stop", "wait 10s", "install"}; !slices.Equal(h.core.events, want) {
		t.Fatalf("events %q, want %q", h.core.events, want)
	}
	if string(h.updater.installed) != string(h.releases.files[repositoryPath+"v1.1.0/astrlink-1.1.0-"+testTarget+".tar.gz"]) {
		t.Fatal("the installer did not read the verified download")
	}
	if s.Phase != PhaseInstalling {
		t.Fatalf("phase %s", s.Phase)
	}
	if h.releases.count("github.com") > 3 {
		t.Fatalf("install downloaded again: %d requests", h.releases.count("github.com"))
	}
}

func TestHelpersStillRunningAfterTheWaitDoNotStopTheInstall(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.core.waitErr = errors.New("2 astrlink-core helper process(es) still running after 10000 ms")
	h.releases.publish("1.1.0", false)
	h.check()
	h.m.Download(context.Background())
	s, err := h.m.Install(context.Background())
	if err != nil || s.Phase != PhaseInstalling || s.ErrorCode != nil {
		t.Fatal(s, err)
	}
	if h.relaunched != 1 || len(h.updater.installed) == 0 {
		t.Fatal("the install did not go on after the wait timed out")
	}
}

func TestAFailedInstallRestartsCoreAndReportsIt(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.m.Download(context.Background())
	h.updater.installErr = errors.New("disk full")
	s, err := h.m.Install(context.Background())
	if err != nil || s.Phase != PhaseError || deref(s.ErrorCode) != "install" || deref(s.ErrorDetail) != "disk full" {
		t.Fatalf("%+v %v", s, err)
	}
	if h.core.stops != 1 || h.core.starts != 1 || h.core.released != 1 || h.relaunched != 0 {
		t.Fatalf("core %+v relaunched %d", h.core, h.relaunched)
	}
	// Core that did not come back is reported with the install failure.
	h.m.Download(context.Background())
	h.core.startErr = errors.New("port in use")
	h.check()
	s, _ = h.m.Install(context.Background())
	if deref(s.ErrorDetail) != "disk full; gateway recovery failed: port in use" {
		t.Fatalf("detail %q", deref(s.ErrorDetail))
	}
}

func TestAnInstallThatCannotStopCoreLeavesItRunning(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.m.Download(context.Background())
	h.core.stopErr = errors.New("stuck")
	s, _ := h.m.Install(context.Background())
	if deref(s.ErrorCode) != "shutdown" || h.core.released != 1 || h.updater.installed != nil {
		t.Fatalf("%+v core %+v", s, h.core)
	}
	h.core.stopErr = nil
	h.core.begin = errors.New("Application update in progress")
	h.m.Download(context.Background())
	if s, _ := h.m.Install(context.Background()); deref(s.ErrorCode) != "shutdown" || h.core.stops != 1 {
		t.Fatalf("%+v core %+v", s, h.core)
	}
}

func TestACorruptedCacheIsCheckedBeforeInstalling(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.m.Download(context.Background())
	p := h.m.cache.path(h.m.pending.file)
	data, _ := os.ReadFile(p)
	data[0] ^= 1
	_ = os.WriteFile(p, data, 0o600)
	s, _ := h.m.Install(context.Background())
	if deref(s.ErrorCode) != "verification" || h.core.stops != 0 {
		t.Fatalf("%+v core %+v", s, h.core)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the corrupted cache stayed")
	}
}

func TestSwitchingChannelAbandonsARunningDownload(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.releases.hold = make(chan struct{})
	h.releases.served = make(chan struct{}, 1)
	done := make(chan UpdateSnapshot, 1)
	go func() {
		s, _ := h.m.Download(context.Background())
		done <- s
	}()
	select {
	case <-h.releases.served:
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not start")
	}
	if s := h.m.Snapshot(); s.Phase != PhaseDownloading {
		t.Fatalf("phase %s", s.Phase)
	}
	if _, err := h.m.Check(context.Background()); err == nil {
		t.Fatal("a check ran beside the download")
	}
	s, err := h.m.SetPreferences(prefs.UpdatePreferences{AutoCheck: true, AutoDownload: false, Channel: prefs.ChannelPreview})
	if err != nil || s.Phase != PhaseIdle || s.Release != nil {
		t.Fatalf("switched: %+v %v", s, err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not end")
	}
	close(h.releases.hold)
	if s := h.m.Snapshot(); s.Phase != PhaseIdle {
		t.Fatalf("after the download: %+v", s)
	}
	entries, _ := os.ReadDir(h.m.cache.dir)
	if len(entries) != 0 {
		t.Fatalf("the abandoned download stayed: %v", entries)
	}
	if len(h.saved) != 1 || h.saved[0].Channel != prefs.ChannelPreview {
		t.Fatalf("saved %+v", h.saved)
	}
}

func TestAnnouncementsAreMadeOncePerStateAndVersion(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.check()
	h.check()
	h.m.Download(context.Background())
	h.check()
	want := []string{"available 1.1.0", "ready 1.1.0"}
	if strings.Join(h.announced, ",") != strings.Join(want, ",") {
		t.Fatalf("announced %v", h.announced)
	}
}

func TestPreferencesCannotChangeDuringAnInstall(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.m.mu.Lock()
	h.m.snapshot.Phase = PhaseInstalling
	h.m.mu.Unlock()
	if _, err := h.m.SetPreferences(prefs.DefaultUpdatePreferences()); err == nil {
		t.Fatal("preferences changed during an install")
	}
}

func TestTheSchedulerChecksWhenDueAndStopsWhenCancelled(t *testing.T) {
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	checked := make(chan UpdateSnapshot, 8)
	h.m.cfg.Publish = func(s UpdateSnapshot) {
		if s.Phase == PhaseAvailable {
			checked <- s
		}
	}
	// The first check is due 30 seconds after launch.
	h.mu.Lock()
	h.now = noon.Add(firstCheckDelay)
	h.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.m.Run(ctx)
		close(done)
	}()
	select {
	case <-checked:
	case <-done:
		t.Fatal("the scheduler returned")
	case <-time.After(10 * time.Second):
		t.Fatal("the scheduler did not check")
	}
	// The check ends by planning the next one; take the operation the
	// check held to wait for it.
	h.m.operation <- struct{}{}
	h.m.endOperation()
	if delay, _ := h.m.automaticCheckDelay(); delay != checkInterval {
		t.Fatalf("next check in %v", delay)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the scheduler did not stop")
	}
	if _, on := (&Manager{snapshot: UpdateSnapshot{Development: true}}).automaticCheckDelay(); on {
		t.Fatal("development builds check automatically")
	}
}

func TestTheCacheKeepsOnlyTheCurrentDownload(t *testing.T) {
	dir := t.TempDir()
	c := cache{dir: dir}
	for _, name := range []string{"a.update", "b.part", "keep.txt"} {
		_ = os.WriteFile(filepath.Join(dir, name), nil, 0o600)
	}
	c.clear()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("left %v", entries)
	}
}

// A latest release without this platform's update manifest, as the releases
// before the first MyGo build are, still names the newest version to builds
// that only link to it; a build that installs updates reports the missing
// manifest.
func TestALatestReleaseWithoutAManifestStillNamesItsVersion(t *testing.T) {
	for _, development := range []bool{true, false} {
		h := newHarness(t, "1.0.0", func(c *Config) { c.Development = development })
		h.releases.publish("1.1.0", false)
		h.releases.mu.Lock()
		delete(h.releases.files, repositoryPath+"v1.1.0/update-"+testTarget+".json")
		h.releases.mu.Unlock()
		s := h.check()
		if deref(s.LatestVersion) != "1.1.0" || s.Release == nil || s.Release.Version != "1.1.0" {
			t.Fatalf("development %v: %+v", development, s)
		}
		if development && (s.Phase != PhaseManual || s.ErrorCode != nil) {
			t.Fatalf("development: %+v", s)
		}
		if !development && (s.Phase != PhaseError || deref(s.ErrorCode) != "missing_artifact") {
			t.Fatalf("installable: %+v", s)
		}
	}
}
