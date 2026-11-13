package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
)

func TestLocatorSearchOrder(t *testing.T) {
	home := t.TempDir()
	install := t.TempDir()
	name := executableName()
	for _, path := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(install, ".local", "bin", name),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	versions := map[string]string{}
	locator := &Locator{
		Home: home, InstallHome: install,
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Version: func(_ context.Context, path string) (string, error) {
			return versions[path] + " (Claude Code)", nil
		},
	}
	versions[filepath.Join(home, ".local", "bin", name)] = "2.1.100"
	versions[filepath.Join(install, ".local", "bin", name)] = "2.1.300"
	binary, err := locator.Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if binary.Path != filepath.Join(install, ".local", "bin", name) || binary.Version != "2.1.300" {
		t.Fatalf("Find = %+v; an older CLI earlier in the search must be skipped", binary)
	}

	locator = &Locator{
		Home:     home,
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Version:  func(context.Context, string) (string, error) { return "2.0.9", nil },
	}
	if _, err := locator.Find(context.Background()); !errors.Is(err, ErrTooOld) || !strings.Contains(err.Error(), "claude update") {
		t.Fatalf("Find with only an old CLI error = %v", err)
	}

	locator = &Locator{Configured: filepath.Join(home, "nowhere", "claude"), Home: home}
	if _, err := locator.Find(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Find with a missing configured CLI error = %v", err)
	}
}

func TestLocatorRunsTheExecutableForItsVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	binary := fakeclaude.Install(t, t.TempDir(), fakeclaude.Settings{Version: "2.1.248"})
	found, err := (&Locator{Configured: binary}).Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if found.Version != "2.1.248" {
		t.Fatalf("version = %q", found.Version)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"2.1.248", true}, {"2.1.300 (Claude Code)", true}, {"2.2.0", true}, {"3.0.0", true},
		{"2.1.247", false}, {"1.9.999", false}, {"unknown", false},
	} {
		if got := VersionAtLeast(test.version, MinimumVersion); got != test.want {
			t.Errorf("VersionAtLeast(%q) = %v, want %v", test.version, got, test.want)
		}
	}
}

func TestLayoutKeepsGatewayNameOutOfPaths(t *testing.T) {
	desktop, err := NewLayout(filepath.Join(t.TempDir(), "com.astrlink.desktop"), false)
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config directory")
	}
	if desktop.Root != filepath.Join(config, "claude-accounts") || desktop.InstallHome != "" {
		t.Fatalf("desktop layout = %+v", desktop)
	}
	server, err := NewLayout("/data", true)
	if err != nil {
		t.Fatal(err)
	}
	if server.Root != filepath.Join("/data", "claude-accounts") || server.InstallHome != filepath.Join("/data", "claude-cli") {
		t.Fatalf("server layout = %+v", server)
	}
	for _, layout := range []Layout{desktop, server} {
		if strings.Contains(strings.ToLower(layout.WorkDir), "astrlink") {
			t.Fatalf("work dir names the gateway: %q", layout.WorkDir)
		}
	}
	dir, err := server.ConfigDir(testAccountID)
	if err != nil || dir != filepath.Join("/data", "claude-accounts", string(testAccountID)) {
		t.Fatalf("ConfigDir = %q, %v", dir, err)
	}
	if _, err := server.ConfigDir("../escape"); err == nil {
		t.Fatal("ConfigDir accepted a path")
	}
}

func TestServeEnvDropsParentSettings(t *testing.T) {
	env := ServeEnv([]string{
		"PATH=/bin", "HOME=/home/me", "HTTPS_PROXY=http://proxy", "ANTHROPIC_API_KEY=x",
		"CLAUDE_CODE_SIMPLE=1", "MCP_TOOL_TIMEOUT=5", "CLAUDE_CONFIG_DIR=/parent",
	}, "/data/claude-cli", "/accounts/a", map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"})
	got := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		got[name] = value
	}
	want := map[string]string{
		"PATH": "/bin", "HOME": "/data/claude-cli", "CLAUDE_CONFIG_DIR": "/accounts/a",
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:1",
	}
	if len(got) != len(want) {
		t.Fatalf("ServeEnv = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("ServeEnv[%s] = %q, want %q (env %v)", name, got[name], value, got)
		}
	}
}

func TestInstallerRunsOnRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the installer is a shell script")
	}
	home := t.TempDir()
	done := make(chan struct{}, 1)
	installer := &Installer{
		Home: home, Environ: []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=x"}, Shell: "/bin/sh",
		Script: `mkdir -p "$HOME/.local/bin" && env > "$HOME/env.txt"`,
		Done:   func() { done <- struct{}{} },
	}
	if !installer.Available() {
		t.Fatal("installer with a home is unavailable")
	}
	if err := installer.Start(); err != nil {
		t.Fatal(err)
	}
	<-done
	if state := installer.State(); state.Installing || state.Error != "" {
		t.Fatalf("state = %+v", state)
	}
	raw, err := os.ReadFile(filepath.Join(home, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "HOME="+home) || strings.Contains(string(raw), "ANTHROPIC_") ||
		strings.Contains(string(raw), "CLAUDE_CONFIG_DIR") {
		t.Fatalf("installer env = %s", raw)
	}

	installer.Script = `echo "curl: (6) Could not resolve host: claude.ai" >&2; exit 6`
	if err := installer.Start(); err != nil {
		t.Fatal(err)
	}
	<-done
	if state := installer.State(); state.Error != "install failed: curl: (6) Could not resolve host: claude.ai" {
		t.Fatalf("failed state = %+v", state)
	}

	if err := (&Installer{}).Start(); !errors.Is(err, ErrInstallUnavailable) {
		t.Fatalf("desktop Start error = %v", err)
	}
}
