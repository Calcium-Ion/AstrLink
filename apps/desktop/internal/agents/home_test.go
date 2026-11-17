package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

func TestClaudeJSONSitsBesideTheDefaultDirAndInsideAMovedOne(t *testing.T) {
	home := t.TempDir()
	if got := Local(home).ClaudeJSON(); got != filepath.Join(home, ".claude.json") {
		t.Errorf("default: %s", got)
	}
	at := func(dirs ToolDirs) Home { return Home{Host: home, Dirs: dirs} }
	if got := at(ToolDirs{Claude: filepath.Join(home, ".claude") + string(filepath.Separator)}).ClaudeJSON(); got != filepath.Join(home, ".claude.json") {
		t.Errorf("default named: %s", got)
	}
	if got := at(ToolDirs{Claude: filepath.Join(home, "claude")}).ClaudeJSON(); got != filepath.Join(home, "claude", ".claude.json") {
		t.Errorf("moved: %s", got)
	}
}

func TestToolsAreFoundInTheirDirectories(t *testing.T) {
	home := Local(t.TempDir())
	if home.ClaudeDetected() || home.OpenCodeDetected() || home.PiDetected() {
		t.Fatal("detected in an empty home")
	}
	for _, dir := range []string{home.Join(".local", "share", "opencode"), home.Join(".pi")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(home.ClaudeJSON(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !home.ClaudeDetected() || !home.OpenCodeDetected() || !home.PiDetected() {
		t.Fatal("not detected")
	}
	if home.PiAgentDir() != home.Join(".pi", "agent") || home.OpenCodeDir() != home.Join(".config", "opencode") {
		t.Fatalf("%s %s", home.PiAgentDir(), home.OpenCodeDir())
	}
	if home.Windows() != (runtime.GOOS == "windows") || home.Distribution() != "" {
		t.Fatal("a local home")
	}
}

func TestConfigDirsFollowCCSwitchSettings(t *testing.T) {
	home := t.TempDir()
	if got := ConfigDirs(home); got != (ToolDirs{}) {
		t.Fatalf("no settings: %+v", got)
	}
	settings := filepath.Join(home, ".cc-switch", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		if err := os.WriteFile(settings, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"claudeConfigDir":"~/work/.claude","codexConfigDir":"  ","grokConfigDir":"relative/grok","piConfigDir":"/opt/pi/agent","geminiConfigDir":"~","opencodeConfigDir":7,"showInTray":true}`)
	want := ToolDirs{Claude: filepath.Join(home, "work", ".claude"), Gemini: home}
	if runtime.GOOS != "windows" {
		want.Pi = "/opt/pi/agent"
	}
	if got := ConfigDirs(home); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	write("not json")
	if got := ConfigDirs(home); got != (ToolDirs{}) {
		t.Fatalf("not json: %+v", got)
	}
}

func TestCCSwitchDirsSplitByDistribution(t *testing.T) {
	local, inWSL := splitDirs(ToolDirs{
		Claude: `\\wsl.localhost\Ubuntu-26.04\home\yayoi\.claude`,
		Codex:  `\\wsl$\ubuntu-26.04\opt\codex`,
		Grok:   `D:\grok`,
		Pi:     `\\?\UNC\wsl$\Debian\root\.pi\agent`,
	})
	if local != (ToolDirs{Grok: `D:\grok`}) {
		t.Errorf("local %+v", local)
	}
	want := []wslDirs{
		{
			distribution: "Ubuntu-26.04",
			home:         `\\wsl.localhost\Ubuntu-26.04\home\yayoi`,
			dirs:         ToolDirs{Claude: `\\wsl.localhost\Ubuntu-26.04\home\yayoi\.claude`, Codex: `\\wsl$\ubuntu-26.04\opt\codex`},
		},
		{
			distribution: "Debian",
			home:         `\\wsl$\Debian\root`,
			dirs:         ToolDirs{Pi: `\\?\UNC\wsl$\Debian\root\.pi\agent`},
		},
	}
	if !reflect.DeepEqual(inWSL, want) {
		t.Errorf("wsl %+v", inWSL)
	}
}

func TestHomesNameTheHostAndEachDistribution(t *testing.T) {
	host := t.TempDir()
	homes := &Homes{Host: host, WSL: []WSLHome{{
		Home: wsl.Home{Distribution: "Ubuntu", Share: filepath.Join(host, "share"), MountRoot: "/mnt"},
		Dirs: ToolDirs{Codex: filepath.Join(host, "share", "codex")},
	}}}
	all := homes.All()
	if len(all) != 2 || all[0].Dir() != host || all[1].Dir() != filepath.Join(host, "share") {
		t.Fatalf("%+v", all)
	}
	ubuntu, ok := homes.Get("ubuntu")
	if !ok || ubuntu.Distribution() != "Ubuntu" || ubuntu.Windows() || ubuntu.CodexDir() != filepath.Join(host, "share", "codex") || ubuntu.ClaudeDir() != filepath.Join(host, "share", ".claude") {
		t.Fatalf("%+v", ubuntu)
	}
	if seen, ok := ubuntu.Seen(`C:\Users\ada\bin`); !ok || seen != "/mnt/c/Users/ada/bin" {
		t.Errorf("seen %q", seen)
	}
	if ubuntu.Shown("/x") != "/x" {
		t.Error("shown")
	}
	if _, ok := homes.Get("Debian"); ok {
		t.Error("found a missing distribution")
	}
	if home, ok := homes.Get(""); !ok || home.WSL != nil || home.Dir() != host {
		t.Errorf("%+v", home)
	}
}

func TestFindHomesOutsideWindowsHasOnlyTheHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lists the real WSL distributions")
	}
	host := t.TempDir()
	homes, err := FindHomes(host, wsl.Running())
	if err != nil || homes.Host != host || len(homes.WSL) != 0 || len(homes.Unchecked) != 0 {
		t.Fatalf("%+v %v", homes, err)
	}
}
