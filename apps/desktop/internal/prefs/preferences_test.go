package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// load pins the fallback so assertions do not depend on the host's
// language.
func load(directory string) *Store {
	return LoadWithFallbackLocale(directory, directory, DefaultInferencePort, LocaleEn)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAboutAndGeneralSettingsDoNotOverwriteEachOther(t *testing.T) {
	directory := t.TempDir()
	store := load(directory)
	staleGeneral := store.Values()
	updates := UpdatePreferences{Channel: ChannelPreview}
	if err := store.ReplaceUpdates(updates); err != nil {
		t.Fatal(err)
	}
	staleGeneral.InferencePort = 9876
	if _, err := store.Replace(staleGeneral); err != nil {
		t.Fatal(err)
	}
	if got := store.Values().Updates; got != updates {
		t.Fatalf("updates %+v", got)
	}
	if err := store.ReplaceUpdates(DefaultUpdatePreferences()); err != nil {
		t.Fatal(err)
	}
	if store.Values().InferencePort != 9876 {
		t.Fatal("the update settings overwrote the port")
	}
	reloaded := load(directory).Values()
	if reloaded.InferencePort != 9876 || reloaded.Updates != DefaultUpdatePreferences() {
		t.Fatalf("reloaded %+v", reloaded)
	}
}

func TestAutostartHiddenDefaultsOffForExistingPreferencesAndIsPersisted(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, fileName), `{"autostart":true}`)
	store := load(directory)
	values := store.Values()
	if !values.Autostart || values.AutostartHidden || store.Snapshot().LoadWarning != nil {
		t.Fatalf("values %+v, warning %v", values, store.Snapshot().LoadWarning)
	}
	values.AutostartHidden = true
	if _, err := store.Replace(values); err != nil {
		t.Fatal(err)
	}
	if !load(directory).Values().AutostartHidden {
		t.Fatal("autostart_hidden was not saved")
	}
	values = store.Values()
	values.Autostart = false
	store.Replace(values)
	if reloaded := load(directory).Values(); reloaded.Autostart || !reloaded.AutostartHidden {
		t.Fatalf("reloaded %+v", reloaded)
	}
}

func TestSavedPreferencesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	store := load(directory)
	values := store.Values()
	values.InferencePort = 9877
	if _, err := store.Replace(values); err != nil {
		t.Fatal(err)
	}
	mode := func() os.FileMode {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if mode() != 0o600 {
		t.Fatalf("mode %v", mode())
	}
	os.Chmod(path, 0o644)
	if load(directory).Values().InferencePort != 9877 || mode() != 0o600 {
		t.Fatalf("mode after load %v", mode())
	}
}

func TestTemporaryNamesAreUniqueAndPortable(t *testing.T) {
	first, second := TemporaryPath("/x/desktop-preferences.json"), TemporaryPath("/x/desktop-preferences.json")
	for _, p := range []string{first, second} {
		name := filepath.Base(p)
		if strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.") != "" {
			t.Fatalf("name %q", name)
		}
	}
	if first == second {
		t.Fatal("two temporary paths are the same")
	}
}

func TestMissingFileUsesSafeDefaultsWithoutClaimingAnError(t *testing.T) {
	directory := t.TempDir()
	snapshot := load(directory).Snapshot()
	if !reflect.DeepEqual(snapshot.Values, Default(DefaultInferencePort)) || snapshot.LoadWarning != nil {
		t.Fatalf("snapshot %+v", snapshot)
	}
	// A new install has the big pet out without saving her switch.
	if !snapshot.Values.DesktopToys.On().Pet {
		t.Fatal("a new install starts with the big pet in")
	}
	var saved struct {
		DesktopToys struct {
			Switched map[string]any `json:"switched"`
		} `json:"desktop_toys"`
	}
	if b, err := os.ReadFile(filepath.Join(directory, fileName)); err != nil || json.Unmarshal(b, &saved) != nil ||
		saved.DesktopToys.Switched == nil || len(saved.DesktopToys.Switched) != 0 {
		t.Fatalf("saved %+v %v", saved, err)
	}
	v := snapshot.Values
	if v.Locale != LocaleEn || v.Theme != ThemeSystem || v.QuotaDisplayMode != QuotaRemaining || !v.UseSystemProxy ||
		v.MaxRequestBodyMiB != 0 || v.ResponseStartTimeoutSeconds != 600 || v.InferencePort != 18317 {
		t.Fatalf("defaults %+v", v)
	}
	// Starting Core must not make a new install look like a legacy install.
	writeFile(t, filepath.Join(directory, "astrlink.db"), "")
	if got := load(directory).Values(); !reflect.DeepEqual(got, v) {
		t.Fatalf("after the database %+v", got)
	}
}

func TestExistingInferencePortsAreNotMigrated(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	for content, port := range map[string]uint16{
		`{"inference_port":8317}`: 8317,
		`{"inference_port":9123}`: 9123,
		`{"locale":"en"}`:         8317,
	} {
		writeFile(t, path, content)
		snapshot := load(directory).Snapshot()
		if snapshot.Values.InferencePort != port || snapshot.LoadWarning != nil {
			t.Fatalf("%s: %+v", content, snapshot)
		}
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Fatalf("%s was rewritten to %s", content, b)
		}
	}
}

func TestResponseStartTimeoutDefaultsWithoutOverwritingSavedValues(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	for content, want := range map[string]uint32{
		`{"locale":"en"}`:                         600,
		`{"response_start_timeout_seconds":0}`:    0,
		`{"response_start_timeout_seconds":1200}`: 1200,
	} {
		writeFile(t, path, content)
		if got := load(directory).Values().ResponseStartTimeoutSeconds; got != want {
			t.Fatalf("%s: %d", content, got)
		}
	}
}

func TestExistingInstallWithoutSavedPreferencesKeepsLegacyPort(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, "astrlink.db"), "")
	snapshot := load(directory).Snapshot()
	if snapshot.Values.InferencePort != 8317 || snapshot.LoadWarning != nil || !snapshot.Values.DesktopToys.On().Pet {
		t.Fatalf("snapshot %+v", snapshot)
	}
	if _, err := os.Stat(filepath.Join(directory, fileName)); err == nil {
		t.Fatal("an old install's preferences were written")
	}
}

func TestFirstRunAndUnusableFilesStartInTheSystemLanguage(t *testing.T) {
	directory := t.TempDir()
	loadZh := func() Snapshot {
		return LoadWithFallbackLocale(directory, directory, DefaultInferencePort, LocaleZhCN).Snapshot()
	}
	if s := loadZh(); s.Values.Locale != LocaleZhCN || s.LoadWarning != nil {
		t.Fatalf("first run %+v", s)
	}
	writeFile(t, filepath.Join(directory, fileName), "{no")
	if s := loadZh(); s.Values.Locale != LocaleZhCN || s.LoadWarning == nil || !s.Values.DesktopToys.On().Pet {
		t.Fatalf("unparseable %+v", s)
	}
	// An explicit choice outlives other invalid settings in the same file.
	writeFile(t, filepath.Join(directory, fileName), `{"inference_port":80,"locale":"en"}`)
	if s := loadZh(); s.Values.Locale != LocaleEn || s.Values.InferencePort != DefaultInferencePort {
		t.Fatalf("invalid %+v", s)
	}
}

func TestMissingLocaleFieldDefaultsToEnglish(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, fileName), `{"close_behavior":"hide_to_tray","autostart":false,"core_auto_start":true,"core_auto_recover":true,"inference_port":8317}`)
	s := load(directory).Snapshot()
	v := s.Values
	if v.Locale != LocaleEn || v.Theme != ThemeSystem || v.QuotaDisplayMode != QuotaRemaining || !v.UseSystemProxy ||
		v.MaxConcurrentInspections != DefaultMaxConcurrentInspections || v.ResponseStartTimeoutSeconds != DefaultResponseStartTimeoutSecs ||
		v.MaxRequestBodyMiB != 0 || s.LoadWarning != nil {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestMalformedAndInvalidFilesAreReportedHonestly(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, fileName), "{no")
	if w := load(directory).Snapshot().LoadWarning; w == nil || !strings.Contains(*w, "could not be parsed") {
		t.Fatalf("warning %v", w)
	}
	writeFile(t, filepath.Join(directory, fileName), `{"inference_port":80}`)
	if w := load(directory).Snapshot().LoadWarning; w == nil || !strings.Contains(*w, "invalid settings") {
		t.Fatalf("warning %v", w)
	}
}

func TestInferenceListenDefaultsToLoopbackAndRoundTrips(t *testing.T) {
	saved, err := decodePreferences([]byte(`{"inference_port":9123}`), DefaultInferencePort)
	if err != nil || saved.InferenceListen != ListenLoopback {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if ListenLoopback.Host() != "127.0.0.1" || ListenAllInterfaces.Host() != "0.0.0.0" {
		t.Fatal("hosts")
	}
	exposed := Default(DefaultInferencePort)
	exposed.InferenceListen = ListenAllInterfaces
	data, _ := json.Marshal(exposed)
	if !strings.Contains(string(data), `"inference_listen":"all_interfaces"`) {
		t.Fatalf("encoded %s", data)
	}
	if back, err := decodePreferences(data, DefaultInferencePort); err != nil || !reflect.DeepEqual(back, exposed) {
		t.Fatalf("round trip %+v, %v", back, err)
	}
	if _, err := decodePreferences([]byte(`{"inference_listen":"lan"}`), DefaultInferencePort); err == nil {
		t.Fatal("accepted an unknown scope")
	}
}

func TestReplacementIsValidatedAndDurablyReloadable(t *testing.T) {
	directory := t.TempDir()
	store := load(directory)
	values := Default(DefaultInferencePort)
	values.InferencePort, values.MaxRequestBodyMiB, values.UseSystemProxy = 9123, 64, false
	values.Theme, values.QuotaDisplayMode = ThemeDark, QuotaUsed
	if _, err := store.Replace(values); err != nil {
		t.Fatal(err)
	}
	if got := load(directory).Values(); !reflect.DeepEqual(got, values) {
		t.Fatalf("reloaded %+v", got)
	}
	invalid := values
	invalid.MaxConcurrentInspections = 200
	if _, err := store.Replace(invalid); err == nil || !strings.Contains(err.Error(), "between 4 and 128") {
		t.Fatalf("err %v", err)
	}
	invalid.MaxConcurrentInspections = DefaultMaxConcurrentInspections
	invalid.ResponseStartTimeoutSeconds = MaxResponseStartTimeoutSeconds + 1
	if _, err := store.Replace(invalid); err == nil || !strings.Contains(err.Error(), "between 0 and 86400") {
		t.Fatalf("err %v", err)
	}
}

func TestTrayPreferencesDefaultAndRejectDuplicatePages(t *testing.T) {
	directory := t.TempDir()
	// A preferences file written before the tray section existed.
	writeFile(t, filepath.Join(directory, fileName), `{"close_behavior":"hide_to_tray","inference_port":8317}`)
	s := load(directory).Snapshot()
	if !reflect.DeepEqual(s.Values.Tray, DefaultTrayPreferences()) || s.LoadWarning != nil {
		t.Fatalf("tray %+v", s.Values.Tray)
	}
	if want := []TrayPage{TrayRecords, TrayServices, TrayTokens}; !reflect.DeepEqual(s.Values.Tray.Pages, want) {
		t.Fatalf("pages %v", s.Values.Tray.Pages)
	}
	duplicate := Default(DefaultInferencePort)
	duplicate.Tray.Pages = []TrayPage{TrayRecords, TrayRecords}
	if duplicate.Validate() == nil {
		t.Fatal("accepted a page twice")
	}
	data, _ := json.Marshal(Default(DefaultInferencePort))
	var encoded map[string]any
	json.Unmarshal(data, &encoded)
	tray := encoded["tray"].(map[string]any)
	usage := tray["usage"].(map[string]any)
	if tray["menubar_text"] != "none" || tray["pages"].([]any)[0] != "records" || usage["today"] != true || usage["compare_yesterday"] != false {
		t.Fatalf("encoded %s", data)
	}
	var tp TrayPreferences
	if json.Unmarshal([]byte(`{"unknown":1}`), &tp) == nil {
		t.Fatal("accepted an unknown tray field")
	}
	// The wire names are what the frontend stores (TRAY_PAGES) and sends back.
	pages, _ := json.Marshal([]TrayPage{TrayRecords, TrayServices, TrayTokens, TraySafety, TrayRouting, TrayAgentTools})
	if string(pages) != `["records","services","tokens","safety","routing","agent_tools"]` {
		t.Fatalf("pages %s", pages)
	}
}

func TestThemePreferencesAreStrictAndRoundTrip(t *testing.T) {
	for _, theme := range []ThemePreference{ThemeSystem, ThemeLight, ThemeDark} {
		values := Default(DefaultInferencePort)
		values.Theme = theme
		data, _ := json.Marshal(values)
		if !strings.Contains(string(data), `"theme":"`+string(theme)+`"`) {
			t.Fatalf("encoded %s", data)
		}
		if back, err := decodePreferences(data, DefaultInferencePort); err != nil || back.Theme != theme {
			t.Fatalf("round trip %v, %v", back.Theme, err)
		}
	}
	if _, err := decodePreferences([]byte(`{"theme":"auto"}`), DefaultInferencePort); err == nil {
		t.Fatal("accepted theme auto")
	}
}

func TestDisplayCurrencyDefaultsToAutoAndRejectsUnknownCodes(t *testing.T) {
	// Files saved before the setting existed follow the system region.
	saved, _ := decodePreferences([]byte(`{"theme":"dark"}`), DefaultInferencePort)
	if saved.DisplayCurrency != "auto" {
		t.Fatalf("currency %q", saved.DisplayCurrency)
	}
	for code, valid := range map[string]bool{"auto": true, "CNY": true, "cny": false, "THB": false} {
		values := Default(DefaultInferencePort)
		values.DisplayCurrency = code
		if (values.Validate() == nil) != valid {
			t.Errorf("%s valid = %v", code, !valid)
		}
	}
}

func TestUnwritableDestinationIsReportedWithoutMutatingMemory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, directory, "not a directory")
	store := load(directory)
	original := store.Values()
	next := original
	next.InferencePort = 9124
	if _, err := store.Replace(next); err == nil || !strings.Contains(err.Error(), "preferences directory") {
		t.Fatalf("err %v", err)
	}
	if !reflect.DeepEqual(store.Values(), original) {
		t.Fatal("a failed save changed the preferences")
	}
}

func TestQuotaPercent(t *testing.T) {
	for _, c := range []struct {
		mode QuotaDisplayMode
		used float64
		want float64
	}{
		{QuotaRemaining, 30, 70}, {QuotaUsed, 30, 30}, {QuotaUsed, 130, 100}, {QuotaRemaining, -5, 100},
	} {
		if got := c.mode.Percent(c.used); got != c.want {
			t.Errorf("%s.Percent(%v) = %v", c.mode, c.used, got)
		}
	}
}

func TestParseInputRequiresTheSettingsFieldsAndDefaultsTheRest(t *testing.T) {
	values := Default(DefaultInferencePort)
	values.InferencePort = 19200
	values.InferenceListen = ListenAllInterfaces
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseInput(encoded)
	if err != nil || !reflect.DeepEqual(parsed, values) {
		t.Fatalf("parse = %+v, %v", parsed, err)
	}

	for _, name := range optionalInputFields {
		delete(fields, name)
	}
	encoded, _ = json.Marshal(fields)
	parsed, err = ParseInput(encoded)
	defaults := Default(DefaultInferencePort)
	if err != nil || parsed.InferenceListen != defaults.InferenceListen || parsed.AutostartHidden ||
		!reflect.DeepEqual(parsed.Tray, defaults.Tray) || !reflect.DeepEqual(parsed.Updates, defaults.Updates) {
		t.Fatalf("parse without optional fields = %+v, %v", parsed, err)
	}

	delete(fields, "inference_port")
	encoded, _ = json.Marshal(fields)
	if _, err := ParseInput(encoded); err == nil || err.Error() != "missing field `inference_port`" {
		t.Errorf("missing field error = %v", err)
	}
	fields["inference_port"] = json.RawMessage("19200")
	fields["telemetry"] = json.RawMessage("true")
	encoded, _ = json.Marshal(fields)
	if _, err := ParseInput(encoded); err == nil {
		t.Error("an unknown field was accepted")
	}
	for _, invalid := range []string{"null", "[]", "not json"} {
		if _, err := ParseInput([]byte(invalid)); err == nil {
			t.Errorf("%s accepted", invalid)
		}
	}
}
