package host

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// loginItemFake is a login item that records each change and refuses the
// ones set to fail.
type loginItemFake struct {
	enabled  bool
	readErr  error
	setErr   map[bool]error
	sticky   bool
	requests []bool
}

func (l *loginItemFake) Enabled() (bool, error) { return l.enabled, l.readErr }

func (l *loginItemFake) SetEnabled(enabled bool) error {
	l.requests = append(l.requests, enabled)
	if err := l.setErr[enabled]; err != nil {
		return err
	}
	if !l.sticky {
		l.enabled = enabled
	}
	return nil
}

type hostFixture struct {
	host    *Host
	login   *loginItemFake
	themes  []prefs.ThemePreference
	changes [][2]prefs.Preferences
	opened  []string
	openErr error
}

func newHostFixture(t *testing.T, configDirectory string) *hostFixture {
	f := &hostFixture{login: &loginItemFake{}}
	data := t.TempDir()
	h := &Host{
		instance:   instance.Current(true),
		dirs:       instance.Directories{Config: configDirectory, Data: data},
		prefs:      prefs.LoadWithFallbackLocale(configDirectory, data, prefs.DefaultInferencePort, prefs.LocaleEn),
		core:       core.New(core.Config{DataDirectory: data, Log: func(string) {}}),
		autostart:  f.login,
		applyTheme: func(theme prefs.ThemePreference) { f.themes = append(f.themes, theme) },
		openURL: func(url string) error {
			f.opened = append(f.opened, url)
			return f.openErr
		},
		stopped: make(chan struct{}),
	}
	h.OnPreferencesChanged(func(previous, current prefs.Preferences) {
		f.changes = append(f.changes, [2]prefs.Preferences{previous, current})
	})
	f.host = h
	return f
}

func TestUpdatePreferencesMovesTheLoginItemFirstAndAppliesTheRest(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	previous := f.host.prefs.Values()
	next := previous
	next.Autostart = true
	next.Theme = prefs.ThemeDark
	next.InferencePort = 19123
	if err := f.host.updatePreferences(next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.login.requests, []bool{true}) || !f.login.enabled {
		t.Errorf("login item requests = %v", f.login.requests)
	}
	if got := f.host.prefs.Values(); got.InferencePort != 19123 || !got.Autostart {
		t.Errorf("saved = %+v", got)
	}
	if !reflect.DeepEqual(f.themes, []prefs.ThemePreference{prefs.ThemeDark}) {
		t.Errorf("themes = %v", f.themes)
	}
	if len(f.changes) != 1 || f.changes[0][0].InferencePort != previous.InferencePort || f.changes[0][1].InferencePort != 19123 {
		t.Errorf("hooks saw %+v", f.changes)
	}

	// An unchanged login item is left alone.
	f.login.requests = nil
	next.InferencePort = 19124
	if err := f.host.updatePreferences(next); err != nil || len(f.login.requests) != 0 {
		t.Errorf("unchanged login item: %v, requests %v", err, f.login.requests)
	}
}

func TestUpdatePreferencesSavesNothingTheLoginItemRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		login loginItemFake
		want  string
	}{
		{"read", loginItemFake{readErr: errors.New("no session bus")},
			"Unable to read the system login-item state. Settings were not saved: no session bus"},
		{"enable", loginItemFake{setErr: map[bool]error{true: errors.New("denied")}},
			"Unable to enable the system login item. Settings were not saved: denied"},
		{"mismatch", loginItemFake{sticky: true},
			"The system login-item state does not match the request. Settings were not saved."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture(t, t.TempDir())
			*f.login = tc.login
			saved := f.host.prefs.Values()
			next := saved
			next.Autostart = true
			next.InferencePort = 19125
			err := f.host.updatePreferences(next)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
			if !reflect.DeepEqual(f.host.prefs.Values(), saved) || len(f.changes) != 0 || len(f.themes) != 0 {
				t.Errorf("a refused update applied: %+v, hooks %d, themes %v", f.host.prefs.Values(), len(f.changes), f.themes)
			}
		})
	}
}

func TestUpdatePreferencesMovesTheLoginItemBackWhenTheSaveFails(t *testing.T) {
	// A config directory that is a file makes every save fail.
	configDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(configDirectory, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newHostFixture(t, configDirectory)
	next := f.host.prefs.Values()
	next.Autostart = true
	err := f.host.updatePreferences(next)
	if err == nil || !strings.HasSuffix(err.Error(), "; the system login-item state was rolled back. Other settings did not apply.") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(f.login.requests, []bool{true, false}) || f.login.enabled {
		t.Errorf("login item requests = %v, enabled %v", f.login.requests, f.login.enabled)
	}

	f.login = &loginItemFake{setErr: map[bool]error{false: errors.New("busy")}}
	f.host.autostart = f.login
	err = f.host.updatePreferences(next)
	if err == nil || !strings.Contains(err.Error(), "busy") || !f.login.enabled {
		t.Errorf("failed rollback = %v, enabled %v", err, f.login.enabled)
	}
	if len(f.changes) != 0 {
		t.Errorf("hooks ran %d times", len(f.changes))
	}
}

func TestUpdatePreferencesRefusesInvalidInput(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	snapshot := f.host.prefs.Snapshot()
	input, err := json.Marshal(snapshot.Values)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		t.Fatal(err)
	}
	service := Preferences{f.host}
	for name, mutate := range map[string]func(map[string]any){
		"missing":  func(m map[string]any) { delete(m, "inference_port") },
		"unknown":  func(m map[string]any) { m["telemetry"] = true },
		"low port": func(m map[string]any) { m["inference_port"] = 80 },
		"theme":    func(m map[string]any) { m["theme"] = "sepia" },
	} {
		copied := map[string]any{}
		for key, value := range fields {
			copied[key] = value
		}
		mutate(copied)
		encoded, _ := json.Marshal(copied)
		if _, err := service.UpdatePreferences(encoded); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(f.changes) != 0 {
		t.Errorf("an invalid update applied")
	}
	got, err := service.UpdatePreferences(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.AutostartActual == nil || *got.AutostartActual || got.AutostartError != nil || got.DataBackups == nil {
		t.Errorf("snapshot = %+v", got)
	}
}

func TestSettingsSnapshotReportsALoginItemItCannotRead(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	f.login.readErr = errors.New("no session bus")
	snapshot := f.host.settingsSnapshot()
	if snapshot.AutostartActual != nil || snapshot.AutostartError == nil || !strings.Contains(*snapshot.AutostartError, "no session bus") {
		t.Errorf("snapshot = %+v", snapshot)
	}
	if snapshot.LocalKeyStorage != nil {
		t.Errorf("local key storage before a start = %v", *snapshot.LocalKeyStorage)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"values":`, `"autostart_actual":null`, `"autostart_error":"`, `"data_backups":[]`, `"local_key_storage":null`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("snapshot %s lacks %s", encoded, key)
		}
	}
}

func TestExternalLinksOpenOnlyOverHTTPS(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	shell := Shell{f.host}
	if err := shell.OpenExternalURL("https://example.com/docs?a=1"); err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]string{
		"":                        "invalid external URL",
		"example.com":             "invalid external URL",
		"%zz":                     "invalid external URL",
		"http://example.com":      "external URL must use https",
		"file:///etc/passwd":      "external URL must use https",
		"javascript:alert(1)":     "external URL must use https",
		"https:///no-host":        "external URL must use https",
		"astrlink://open/service": "external URL must use https",
	} {
		if err := shell.OpenExternalURL(url); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %s", url, err, want)
		}
	}
	if !reflect.DeepEqual(f.opened, []string{"https://example.com/docs?a=1"}) {
		t.Errorf("opened = %v", f.opened)
	}
	f.openErr = errors.New("no browser")
	if err := shell.OpenExternalURL("https://example.com"); err == nil ||
		err.Error() != "unable to open URL in the system browser: no browser" {
		t.Errorf("browser failure = %v", err)
	}
}

func TestAuthorizationLinksTakeHTTPSOrALoopbackCallback(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	shell := Shell{f.host}
	for _, url := range []string{"https://auth.openai.com/oauth/authorize?client_id=x", "http://localhost:1455/auth/callback"} {
		if err := shell.OpenAuthorizationURL(url); err != nil {
			t.Errorf("%s: %v", url, err)
		}
	}
	for url, want := range map[string]string{
		"http://auth.example/authorize": "authorization URL http is only allowed on loopback",
		"file:///etc/passwd":            "authorization URL must use https",
		"mailto:x@example.com":          "authorization URL is invalid",
		"":                              "authorization URL is invalid",
	} {
		if err := shell.OpenAuthorizationURL(url); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %s", url, err, want)
		}
	}
	if len(f.opened) != 2 {
		t.Errorf("opened = %v", f.opened)
	}
	f.openErr = errors.New("no browser")
	if err := shell.OpenAuthorizationURL("https://auth.openai.com/"); err == nil ||
		err.Error() != "unable to open authorization URL in the system browser: no browser" {
		t.Errorf("browser failure = %v", err)
	}
}

func TestPreloadScriptNamesTheWindowPlatformAndInstance(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	script := f.host.PreloadScript(`inspector "1"`)
	for _, line := range []string{
		`window.__ASTRLINK_WINDOW_LABEL__ = "inspector \"1\"";`,
		`window.__ASTRLINK_DESKTOP_PLATFORM__ = "` + PlatformName() + `";`,
		f.host.instance.InitializationScript(),
	} {
		if !strings.Contains(script, line) {
			t.Errorf("script lacks %s:\n%s", line, script)
		}
	}
	if Label(nil) != "" {
		t.Error("no window has a label")
	}
}

func TestCallsBeforeStartReportCoreNotRunning(t *testing.T) {
	h := New()
	if _, err := h.Call(t.Context(), control.Request{Method: "GET", Path: control.ServicesPath}); err == nil || err.Error() != "AstrLink is not running" {
		t.Errorf("call = %v", err)
	}
	if _, err := h.RegisterServiceTest("main", "service_a", "t", func() {}); err == nil {
		t.Error("a test registered before Start")
	}
	if h.CancelServiceTest("main", "service_a", "t") {
		t.Error("a test cancelled before Start")
	}
	r := h.Raw()
	if r.Core.DataDirectory() != "" || r.Core.NoteRawKeyReplaced(true) || r.Core.PollObservers(t.Context()) == nil {
		t.Error("the raw service saw a Core before Start")
	}
	r.Pins.Lock()
	r.Pins.Unlock()
	if r.Pins.Apply("account", nil, 0) {
		t.Error("pins applied before Start")
	}
}

// A clipboard that refuses the text reports it, so the copy commands
// answer false instead of claiming a copy that never happened.
func TestWriteClipboardReportsARefusedClipboard(t *testing.T) {
	f := newHostFixture(t, t.TempDir())
	var written []string
	refusal := errors.New("clipboard busy")
	f.host.writeClipboard = func(text string) error {
		written = append(written, text)
		return refusal
	}
	if err := f.host.WriteClipboard("sk-local"); !errors.Is(err, refusal) {
		t.Fatalf("WriteClipboard error = %v, want %v", err, refusal)
	}
	f.host.writeClipboard = func(text string) error {
		written = append(written, text)
		return nil
	}
	if err := f.host.WriteClipboard("sk-next"); err != nil {
		t.Fatalf("WriteClipboard error = %v", err)
	}
	if !reflect.DeepEqual(written, []string{"sk-local", "sk-next"}) {
		t.Fatalf("written %q", written)
	}
}

// A first run starts in the system's language, which the app reads from the
// system's own list rather than from LANG.
func TestAFirstRunFollowsTheSystemLanguages(t *testing.T) {
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Cleanup(func() { prefs.SetSystemLanguages(func() []string { return nil }) })
	for _, c := range []struct {
		languages []string
		want      prefs.Locale
	}{
		{[]string{"zh-Hans-SG", "en-SG"}, prefs.LocaleZhCN},
		{[]string{"ja-JP", "en-US"}, prefs.LocaleJa},
		{[]string{"fr-FR", "en-GB"}, prefs.LocaleEn},
	} {
		h := &Host{preferredLanguages: func() []string { return c.languages }}
		h.followSystemLanguages()
		config := t.TempDir()
		store := prefs.Load(config, t.TempDir(), prefs.DefaultInferencePort)
		if got := store.Values().Locale; got != c.want {
			t.Errorf("%q: first-run locale = %s, want %s", c.languages, got, c.want)
		}
		if got := h.locale(); got != c.want {
			t.Errorf("%q: locale before the preferences load = %s, want %s", c.languages, got, c.want)
		}
	}
}

func TestReviverReloadsAStoppedPageButNotOneThatStopsAgainAtOnce(t *testing.T) {
	var r reviver
	start := time.Date(2026, 11, 16, 8, 19, 15, 0, time.UTC)
	if !r.allow(start) {
		t.Fatal("the first stop does not reload the page")
	}
	if r.allow(start.Add(reviveGap - time.Second)) {
		t.Fatal("a page that stops again right after its reload is reloaded again")
	}
	if !r.allow(start.Add(reviveGap)) {
		t.Fatal("a page that ran for a while after its reload is not reloaded")
	}
	if r.allow(start.Add(reviveGap + time.Second)) {
		t.Fatal("the gap does not count from the latest reload")
	}
}
