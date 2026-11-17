package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The entries auto-launch 0.5 wrote for the Tauri shell.
const (
	legacyPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
  <key>Label</key>
  <string>AstrLink</string>
  <key>ProgramArguments</key>
  <array><string>/Applications/AstrLink.app/Contents/MacOS/AstrLink</string><string>--autostart</string></array>
  <key>RunAtLoad</key>
  <true/>
  </dict>
</plist>`
	legacyDesktop = "[Desktop Entry]\nType=Application\nVersion=1.0\nName=AstrLink\nComment=AstrLinkstartup script\nExec=/usr/bin/astrlink --autostart\nStartupNotify=false\nTerminal=false"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestTheShellsLaunchAgentIsRemoved(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, "Library", "LaunchAgents", "AstrLink.plist")
	// MyGo's own launch agent (macOS 12) is named after the identifier.
	own := filepath.Join(home, "Library", "LaunchAgents", "com.astrlink.desktop.plist")
	write(t, agent, legacyPlist)
	write(t, own, legacyPlist)
	removed, err := legacyAutostart{goos: "darwin", home: home, name: "AstrLink"}.Remove()
	if err != nil || !slices.Equal(removed, []string{agent}) {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if exists(agent) || !exists(own) {
		t.Fatal("the wrong launch agent was removed")
	}
}

func TestTheShellsAutostartEntryIsRemovedFromTheHomeConfig(t *testing.T) {
	home := t.TempDir()
	entry := filepath.Join(home, ".config", "autostart", "AstrLink Dev.desktop")
	other := filepath.Join(home, ".config", "autostart", "AstrLink.desktop")
	write(t, entry, legacyDesktop)
	write(t, other, legacyDesktop)
	removed, err := legacyAutostart{goos: "linux", home: home, name: "AstrLink Dev"}.Remove()
	if err != nil || !slices.Equal(removed, []string{entry}) {
		t.Fatalf("removed %v, %v", removed, err)
	}
	// The other instance's entry is its own to remove.
	if exists(entry) || !exists(other) {
		t.Fatal("the wrong instance's entry was removed")
	}
}

func TestAnEntryThatIsNotTheShellsStays(t *testing.T) {
	home := t.TempDir()
	entry := filepath.Join(home, ".config", "autostart", "AstrLink.desktop")
	write(t, entry, "[Desktop Entry]\nName=AstrLink\nExec=/opt/astrlink/run.sh --minimized\n")
	agent := filepath.Join(home, "Library", "LaunchAgents", "AstrLink.plist")
	write(t, agent, "<plist><string>/Applications/AstrLink.app</string></plist>")
	for _, goos := range []string{"linux", "darwin"} {
		removed, err := legacyAutostart{goos: goos, home: home, name: "AstrLink"}.Remove()
		if err != nil || len(removed) != 0 {
			t.Fatalf("%s: removed %v, %v", goos, removed, err)
		}
	}
	if !exists(entry) || !exists(agent) {
		t.Fatal("an entry the operator made was removed")
	}
	// Nothing to find is no error.
	if removed, err := (legacyAutostart{goos: "linux", home: t.TempDir(), name: "AstrLink"}).Remove(); err != nil || removed != nil {
		t.Fatalf("empty home: %v, %v", removed, err)
	}
}

// fakeRun is the Run key.
type fakeRun struct {
	values  map[string]string
	deleted []string
	fail    error
}

func (f *fakeRun) Value(name string) (string, bool, error) {
	value, ok := f.values[name]
	return value, ok, f.fail
}

func (f *fakeRun) Delete(name string) error {
	f.deleted = append(f.deleted, name)
	delete(f.values, name)
	return nil
}

func TestTheShellsRunEntryIsRemoved(t *testing.T) {
	run := &fakeRun{values: map[string]string{
		"AstrLink":             `C:\Program Files\AstrLink\astrlink-desktop.exe --autostart`,
		"com.astrlink.desktop": `"C:\Program Files\AstrLink\AstrLink.exe" --mygo-opened-at-login`,
	}}
	removed, err := legacyAutostart{goos: "windows", home: t.TempDir(), name: "AstrLink", run: run}.Remove()
	if err != nil || len(removed) != 1 || !slices.Equal(run.deleted, []string{"AstrLink"}) {
		t.Fatalf("removed %v, deleted %v, %v", removed, run.deleted, err)
	}
	if _, ok := run.values["com.astrlink.desktop"]; !ok {
		t.Fatal("MyGo's own Run entry was removed")
	}
	// A value under the name that does not start the app at login stays.
	run = &fakeRun{values: map[string]string{"AstrLink": `C:\Tools\astrlink.exe`}}
	if removed, _ := (legacyAutostart{goos: "windows", name: "AstrLink", run: run}).Remove(); len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
	failing := &fakeRun{fail: errors.New("access denied")}
	if _, err := (legacyAutostart{goos: "windows", name: "AstrLink", run: failing}).Remove(); err == nil {
		t.Fatal("a registry failure was not reported")
	}
}
