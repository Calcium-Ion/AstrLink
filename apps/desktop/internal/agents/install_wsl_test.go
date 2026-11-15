package agents

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

func wslHome(share string, dirs ToolDirs) WSLHome {
	return WSLHome{Home: wsl.Home{Distribution: "Ubuntu", Share: share, MountRoot: "/mnt"}, Dirs: dirs}
}

func TestWSLToolsInstallIntoTheirHomeAndRunTheDesktopCLI(t *testing.T) {
	host, share := t.TempDir(), t.TempDir()
	mustMkdir(t, filepath.Join(host, ".claude"), filepath.Join(share, ".claude"), filepath.Join(share, ".codex"))
	context := &InstallContext{Home: host, CLISource: filepath.Join(host, "src-astrlink-cli"), WSL: []WSLHome{wslHome(share, ToolDirs{})}}
	mustWrite(t, context.CLISource, "cli")
	target := InWSL(host, &context.WSL[0])

	before := Status(context)
	var listed []string
	hostTools := 0
	for _, tool := range before.Tools {
		if tool.WSL != nil {
			listed = append(listed, string(tool.ID)+"@"+*tool.WSL)
		} else {
			hostTools++
		}
	}
	if !slices.Equal(listed, []string{"claude@Ubuntu", "codex@Ubuntu"}) || hostTools != len(allTools) {
		t.Fatalf("listed = %v, host tools = %d", listed, hostTools)
	}

	selected := []AgentToolTarget{{ID: ToolClaude, WSL: ptr("Ubuntu")}, {ID: ToolCodex, WSL: ptr("Ubuntu")}}
	receipt, err := Install(context, allSkills, selected)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(receipt.WSL, []string{"Ubuntu"}) || !slices.Equal(RecordedWSL(host), []string{"Ubuntu"}) {
		t.Fatalf("wsl = %v", receipt.WSL)
	}
	// The CLI stays in the desktop user's home; WSL runs it from there.
	if !isFile(cliBinaryDest(host)) || pathExists(cliBinaryDest(share)) || pathExists(toolSkillDir(Local(host), debugBundle, ToolClaude)) {
		t.Fatal("the CLI or a skill landed in the wrong home")
	}
	for _, id := range []AgentToolID{ToolClaude, ToolCodex} {
		dir := toolSkillDir(target, debugBundle, id)
		if !strings.HasPrefix(dir, share) ||
			mustRead(t, filepath.Join(dir, "SKILL.md")) != strings.ReplaceAll(bundleFile(debugBundle, "SKILL.md"), cliPlaceholder, cliCommand(target)) {
			t.Fatalf("%s skill at %s", id, dir)
		}
	}
	if !isFile(receiptPath(share)) || !claudeRulesPresent(claudeSettingsPath(target), "allow", ClaudeAllowRules(target)) ||
		mustRead(t, codexRulesPath(target)) != codexRules(target) {
		t.Fatal("the WSL home lacks its receipt or rules")
	}
	after := Status(context)
	for _, tool := range after.Tools {
		if tool.WSL == nil {
			if tool.ID == ToolClaude && (!tool.Detected || slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return s.Installed })) {
				t.Fatalf("host claude = %+v", tool)
			}
			continue
		}
		if slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return !s.Installed }) ||
			!tool.CLIAccessInstalled || !tool.GuardInstalled ||
			!slices.Contains(tool.skill(SkillAstrlinkDebug).PreviewPaths, receiptPath(share)) {
			t.Fatalf("%s = %+v", tool.ID, tool)
		}
	}

	// A distribution that went away fails the install instead of being
	// skipped.
	gone := &InstallContext{Home: host, CLISource: context.CLISource}
	if _, err := Install(gone, debugOnly, selected[:1]); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("err = %v", err)
	}

	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		toolSkillDir(target, debugBundle, ToolClaude), canonicalSkillDir(share, debugBundle), codexRulesPath(target),
		claudeSettingsPath(target), receiptPath(share), receiptPath(host), cliBinaryDest(host),
	} {
		if pathExists(path) {
			t.Fatalf("%s stayed", path)
		}
	}
	if !isDir(filepath.Join(share, ".claude")) || !isDir(filepath.Join(share, ".codex")) {
		t.Fatal("uninstall removed the tools' own directories")
	}
}

func TestWSLRulesReachDesktopFilesThroughTheDriveMounts(t *testing.T) {
	home := wslHome(`\\wsl$\Ubuntu\home\ada`, ToolDirs{})
	target := InWSL(`C:\Users\Ada Lovelace`, &home)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	cli := "/mnt/c/Users/Ada Lovelace/.astrlink/bin/astrlink" + exe
	if got := cliPath(target); got != cli {
		t.Fatalf("cli = %s", got)
	}
	if got := cliCommand(target); got != "'"+cli+"'" {
		t.Fatalf("command = %s", got)
	}
	if got := ClaudeAllowRules(target); !slices.Equal(got, []string{"Bash('" + cli + "' *)"}) {
		t.Fatal(got)
	}
	if !strings.Contains(codexRules(target), format.Encode(cli)) {
		t.Fatal(codexRules(target))
	}
	data := []string{`C:\Users\Ada Lovelace\AppData\Roaming\AstrLink`}
	rules := ClaudeDenyRules(target, data, nil)
	if rules[0] != "Read(//mnt/c/Users/Ada Lovelace/AppData/Roaming/AstrLink/**)" ||
		rules[1] != "Read(//mnt/c/Users/Ada Lovelace/.astrlink/control-session.json)" ||
		rules[2] != "Read(//mnt/c/Users/Ada Lovelace/.astrlink/control-session-dev.json)" {
		t.Fatalf("rules = %v", rules)
	}
	block := codexGuardBlock(target, data, nil)
	if !strings.Contains(block, "`/mnt/c/Users/Ada Lovelace/AppData/Roaming/AstrLink`") ||
		!strings.Contains(block, "`/mnt/c/Users/Ada Lovelace/.astrlink/control-session.json`, `/mnt/c/Users/Ada Lovelace/.astrlink/control-session-dev.json`") ||
		strings.Contains(block, "~/.astrlink") {
		t.Fatal(block)
	}
}

func TestMovedToolDirsTakeSkillsAndRules(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, "work", "claude")
	codex := filepath.Join(home, "work", "codex")
	pi := filepath.Join(home, "work", "pi-agent")
	// The default directories exist too, and stay untouched.
	mustMkdir(t, claude, codex, pi, filepath.Join(home, ".claude"))
	context := &InstallContext{
		Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"),
		Dirs: ToolDirs{Claude: claude, Codex: codex, Grok: filepath.Join(home, "missing-grok"), Pi: pi},
	}
	mustWrite(t, context.CLISource, "cli")
	var detected []AgentToolID
	for _, tool := range Status(context).Tools {
		if tool.Detected {
			detected = append(detected, tool.ID)
		}
	}
	if !slices.Equal(detected, []AgentToolID{ToolClaude, ToolCodex, ToolPi}) {
		t.Fatalf("detected = %v", detected)
	}
	if _, err := installLocal(context, debugOnly, detected); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(claude, "skills", debugBundle.name, "SKILL.md"), filepath.Join(claude, "settings.json"),
		filepath.Join(codex, "rules", "astrlink.rules"), filepath.Join(codex, "AGENTS.md"),
		filepath.Join(canonicalSkillDir(home, debugBundle), "SKILL.md"),
	} {
		if !isFile(path) {
			t.Fatalf("%s missing", path)
		}
	}
	for _, path := range []string{filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".codex")} {
		if pathExists(path) {
			t.Fatalf("%s written", path)
		}
	}
	for _, tool := range Status(context).Tools {
		if tool.Detected && !tool.skill(SkillAstrlinkDebug).Installed {
			t.Fatalf("%s lacks the skill", tool.ID)
		}
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(claude, "skills", debugBundle.name), filepath.Join(claude, "settings.json"),
		filepath.Join(codex, "rules", "astrlink.rules"), filepath.Join(codex, "AGENTS.md"),
	} {
		if pathExists(path) {
			t.Fatalf("%s stayed", path)
		}
	}
}

func TestResolveSidecarLooksInTheResourcesAndWhereTheyWereStaged(t *testing.T) {
	resources := t.TempDir()
	name := "astrlink-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if _, err := ResolveSidecar(resources, "astrlink-cli"); err == nil {
		t.Fatal("found a missing sidecar")
	}
	staged := filepath.Join(resources, runtime.GOOS+"-"+runtime.GOARCH, "bin", name)
	mustWrite(t, staged, "cli")
	if got, err := ResolveSidecar(resources, "astrlink-cli"); err != nil || got != staged {
		t.Fatalf("got %s, %v", got, err)
	}
	bundled := filepath.Join(resources, "bin", name)
	mustWrite(t, bundled, "cli")
	if got, err := ResolveSidecar(resources, "astrlink-cli"); err != nil || got != bundled {
		t.Fatalf("got %s, %v", got, err)
	}
	if _, err := ResolveSidecar("", "astrlink-cli"); err == nil {
		t.Fatal("found a sidecar without resources")
	}
}
