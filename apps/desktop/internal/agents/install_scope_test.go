package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func allDirs(t *testing.T, home string) {
	t.Helper()
	for _, dir := range []string{".cursor", ".claude", ".codex", ".grok", ".pi", ".dsh"} {
		mustMkdir(t, filepath.Join(home, dir))
	}
}

func setOf(paths []string) []string {
	set := slices.Clone(paths)
	slices.Sort(set)
	return slices.Compact(set)
}

func TestInstallsOnlySelectedToolsAndStartupPreservesScope(t *testing.T) {
	placeholderOnly := []AgentSkillID{SkillRedactionPlaceholders}
	for _, tc := range []struct {
		skills   []AgentSkillID
		selected []AgentToolID
	}{
		{debugOnly, []AgentToolID{ToolGrok}},
		{debugOnly, []AgentToolID{ToolCursor, ToolGrok}},
		{debugOnly, []AgentToolID{ToolCodex}},
		{debugOnly, []AgentToolID{ToolClaude, ToolGrok, ToolGrok}},
		{debugOnly, []AgentToolID{ToolPi}},
		{allSkills, []AgentToolID{ToolClaude, ToolPi}},
		{allSkills, []AgentToolID{ToolDsh}},
		{placeholderOnly, []AgentToolID{ToolClaude, ToolCodex}},
	} {
		home := t.TempDir()
		allDirs(t, home)
		context := newContext(home)
		mustWrite(t, context.CLISource, "cli")
		before := Status(context)
		expected := slices.Clone(before.SharedPaths)
		for _, tool := range before.Tools {
			if !slices.Contains(tc.selected, tool.ID) {
				continue
			}
			for _, skill := range tool.Skills {
				if slices.Contains(tc.skills, skill.ID) {
					expected = append(expected, skill.PreviewPaths...)
				}
			}
		}
		receipt, err := installLocal(context, tc.skills, tc.selected)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := setOf(receipt.Files), setOf(expected); !slices.Equal(got, want) {
			t.Fatalf("%v %v files:\n got %v\nwant %v", tc.skills, tc.selected, got, want)
		}
		if err := SyncInstalledSkills(Local(home)); err != nil {
			t.Fatal(err)
		}
		if err := SyncInstalledCLI(context); err != nil {
			t.Fatal(err)
		}
		if err := SyncInstalledHostGuards(Local(home), nil, nil); err != nil {
			t.Fatal(err)
		}
		needsCLI := slices.Contains(tc.skills, SkillAstrlinkDebug)
		shared := slices.ContainsFunc(tc.selected, AgentToolID.usesSharedSkills)
		for _, tool := range Status(context).Tools {
			chosen := slices.Contains(tc.selected, tool.ID)
			for _, skill := range tool.Skills {
				// Codex, Pi, and DeepSeek Harness share a directory, so
				// choosing any one installs the skill for all of them.
				installed := slices.Contains(tc.skills, skill.ID) && (chosen || tool.ID.usesSharedSkills() && shared)
				if skill.Installed != installed {
					t.Fatalf("%v %v: %s %s installed = %v", tc.skills, tc.selected, tool.ID, skill.ID, skill.Installed)
				}
			}
			access := needsCLI && chosen && (tool.CLIAccess == AccessAllowRules || tool.CLIAccess == AccessExecPolicy)
			guard := needsCLI && chosen && tool.Guard != GuardSkillOnly
			if tool.CLIAccessInstalled != access || tool.GuardInstalled != guard {
				t.Fatalf("%v %v: %s %+v", tc.skills, tc.selected, tool.ID, tool)
			}
		}
		if pathExists(cliBinaryDest(home)) != needsCLI ||
			pathExists(codexRulesPath(Local(home))) != (needsCLI && slices.Contains(tc.selected, ToolCodex)) {
			t.Fatalf("%v %v: the CLI or Codex rules", tc.skills, tc.selected)
		}
		if err := Uninstall(context); err != nil {
			t.Fatal(err)
		}
		if pathExists(cliBinaryDest(home)) {
			t.Fatal("the CLI stayed")
		}
		for _, tool := range Status(context).Tools {
			if tool.CLIAccessInstalled || slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return s.Installed }) {
				t.Fatalf("%s stayed", tool.ID)
			}
		}
	}
}

func TestSelectingGrokPreservesExistingUnselectedInstallations(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	mustMkdir(t, filepath.Join(home, ".grok"), filepath.Join(home, ".cursor"))
	cursorConfig, _ := legacyMCPConfigPath(home, ToolCursor)
	mustWrite(t, cursorConfig, "invalid JSON must remain untouched")
	canonical := canonicalSkillDir(home, debugBundle)
	legacy := legacyCodexSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, legacy); err != nil {
		t.Fatal(err)
	}
	tracked := []string{
		filepath.Join(canonical, "SKILL.md"), managedFilesPath(canonical), filepath.Join(legacy, "SKILL.md"),
		codexRulesPath(Local(home)), codexAgentsPath(Local(home)), cursorConfig,
	}
	var before []string
	for _, path := range tracked {
		before = append(before, mustRead(t, path))
	}
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolGrok}); err != nil {
		t.Fatal(err)
	}
	for i, path := range tracked {
		if mustRead(t, path) != before[i] {
			t.Fatalf("%s changed", path)
		}
	}
	status := Status(context)
	for _, id := range []AgentToolID{ToolCodex, ToolGrok} {
		if !toolOf(status, id).skill(SkillAstrlinkDebug).Installed {
			t.Fatalf("%s lacks the skill", id)
		}
	}
	if len(codexBackups(t, home)) != 0 {
		t.Fatal("a backup was made")
	}
}

func TestPlaceholderSkillAloneBringsNoCLIRulesOrGuards(t *testing.T) {
	home := t.TempDir()
	allDirs(t, home)
	settings := claudeSettingsPath(Local(home))
	userSettings := `{"permissions":{"allow":["Bash(ls *)"]}}`
	mustWrite(t, settings, userSettings)
	// No sidecar is needed for a skill that never runs the CLI.
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "missing"), DataDirectories: []string{filepath.Join(home, "data")}}
	receipt, err := installLocal(context, []AgentSkillID{SkillRedactionPlaceholders}, allTools)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CLIBinary != nil || len(receipt.Skills) != 1 || receipt.Skills[0] != (ReceiptSkill{SkillRedactionPlaceholders, placeholderBundle.version}) {
		t.Fatalf("receipt = %+v", receipt)
	}
	after := Status(context)
	expected := slices.Clone(after.SharedPaths)
	for _, tool := range after.Tools {
		skill := tool.skill(SkillRedactionPlaceholders)
		if !skill.Installed || tool.skill(SkillAstrlinkDebug).Installed || tool.CLIAccessInstalled || tool.GuardInstalled {
			t.Fatalf("%s: %+v", tool.ID, tool)
		}
		expected = append(expected, skill.PreviewPaths...)
		assertRealSkillCopy(t, home, placeholderBundle, toolSkillDir(Local(home), placeholderBundle, tool.ID))
		if pathExists(toolSkillDir(Local(home), debugBundle, tool.ID)) {
			t.Fatalf("%s got the debugging skill", tool.ID)
		}
	}
	if !slices.Equal(setOf(receipt.Files), setOf(expected)) {
		t.Fatalf("files = %v", receipt.Files)
	}
	untouched := func() {
		t.Helper()
		for _, path := range []string{cliBinaryDest(home), codexRulesPath(Local(home)), codexAgentsPath(Local(home)), hostGuardsPath(home)} {
			if pathExists(path) {
				t.Fatalf("%s was written", path)
			}
		}
		if mustRead(t, settings) != userSettings {
			t.Fatal("the Claude settings changed")
		}
	}
	untouched()

	// Startup keeps the install skill-only once a sidecar exists.
	context.CLISource = filepath.Join(home, "src-astrlink-cli")
	mustWrite(t, context.CLISource, "cli")
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledHostGuards(Local(home), context.DataDirectories, nil); err != nil {
		t.Fatal(err)
	}
	untouched()

	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	for _, id := range allTools {
		if pathExists(toolSkillDir(Local(home), placeholderBundle, id)) {
			t.Fatalf("%s skill stayed", id)
		}
	}
	if mustRead(t, settings) != userSettings || pathExists(receiptPath(home)) {
		t.Fatal("uninstall left the receipt or changed the settings")
	}
}

func TestPiAndDshUseTheSharedDirectoryAndLeaveOtherHostsAlone(t *testing.T) {
	// Each host's own directory stays untouched: Pi's agent directory, and
	// DeepSeek Harness's skills and global AGENTS.md.
	for _, tc := range []struct {
		id      AgentToolID
		hostDir string
		own     []string
	}{
		{ToolPi, ".pi", []string{"agent"}},
		{ToolDsh, ".dsh", []string{"skills", "AGENTS.md"}},
	} {
		home := t.TempDir()
		context := newContext(home)
		mustWrite(t, context.CLISource, "cli")
		tool := func() AgentToolStatus { return toolOf(Status(context), tc.id) }
		if tool().Detected {
			t.Fatalf("%s detected", tc.id)
		}
		if _, err := installLocal(context, debugOnly, []AgentToolID{tc.id}); err == nil || !strings.Contains(err.Error(), "no longer detected") {
			t.Fatalf("err = %v", err)
		}
		for _, dir := range []string{tc.hostDir, ".cursor", ".claude", ".codex", ".grok"} {
			mustMkdir(t, filepath.Join(home, dir))
		}
		settings := claudeSettingsPath(Local(home))
		mustWrite(t, settings, "{}")
		receipt, err := installLocal(context, allSkills, []AgentToolID{tc.id})
		if err != nil {
			t.Fatal(err)
		}
		for _, skill := range allSkills {
			bundle := skill.bundle()
			shared := filepath.Join(home, ".agents", "skills", bundle.name)
			if toolSkillDir(Local(home), bundle, tc.id) != shared {
				t.Fatal("not the shared directory")
			}
			assertRealSkillCopy(t, home, bundle, shared)
			if !slices.Contains(receipt.Files, shared) {
				t.Fatal("the receipt lacks the shared skill")
			}
			for _, other := range []AgentToolID{ToolCursor, ToolClaude, ToolGrok} {
				if pathExists(toolSkillDir(Local(home), bundle, other)) {
					t.Fatalf("%s got %s's skill", other, tc.id)
				}
			}
			if pathExists(legacyCodexSkillDir(home, bundle)) {
				t.Fatal("a Codex copy was written")
			}
		}
		for _, name := range tc.own {
			if pathExists(filepath.Join(home, tc.hostDir, name)) {
				t.Fatalf("%s/%s was written", tc.hostDir, name)
			}
		}
		// Neither host asks before running the CLI, so it needs no rules,
		// and the Claude Code and Codex files stay as they were.
		if !isFile(cliBinaryDest(home)) || pathExists(codexRulesPath(Local(home))) ||
			pathExists(codexAgentsPath(Local(home))) || pathExists(hostGuardsPath(home)) || mustRead(t, settings) != "{}" {
			t.Fatal("rules or guards were written")
		}
		status := tool()
		if !status.Detected || slices.ContainsFunc(status.Skills, func(s AgentSkillStatus) bool { return !s.Installed }) ||
			status.CLIAccess != AccessUnrestricted || status.CLIAccessInstalled ||
			status.Guard != GuardSkillOnly || status.GuardInstalled {
			t.Fatalf("status = %+v", status)
		}
		if err := Uninstall(context); err != nil {
			t.Fatal(err)
		}
		for _, skill := range allSkills {
			if pathExists(canonicalSkillDir(home, skill.bundle())) {
				t.Fatal("the shared skill stayed")
			}
		}
		if slices.ContainsFunc(tool().Skills, func(s AgentSkillStatus) bool { return s.Installed }) {
			t.Fatal("still installed")
		}
	}
}

func TestASharedSkillCountsOnlyForADetectedTool(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	// Codex put the skill in ~/.agents/skills, which Pi also reads, but Pi
	// is not installed.
	if !isFile(filepath.Join(canonicalSkillDir(home, debugBundle), "SKILL.md")) ||
		!toolOf(Status(context), ToolCodex).skill(SkillAstrlinkDebug).Installed {
		t.Fatal("Codex lacks the skill")
	}
	pi := toolOf(Status(context), ToolPi)
	if pi.Detected || slices.ContainsFunc(pi.Skills, func(s AgentSkillStatus) bool { return s.Installed }) {
		t.Fatalf("pi = %+v", pi)
	}
	mustMkdir(t, filepath.Join(home, ".pi"))
	if !toolOf(Status(context), ToolPi).skill(SkillAstrlinkDebug).Installed {
		t.Fatal("Pi does not see the shared skill")
	}
}

func TestALaterSkillOnlyInstallKeepsTheEarlierCLIInstall(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	mustMkdir(t, filepath.Join(home, ".pi"))
	canonical := canonicalSkillDir(home, debugBundle)
	tracked := []string{
		filepath.Join(canonical, "SKILL.md"), managedFilesPath(canonical), codexRulesPath(Local(home)),
		codexAgentsPath(Local(home)), hostGuardsPath(home), cliBinaryDest(home),
	}
	var before []string
	for _, path := range tracked {
		before = append(before, mustRead(t, path))
	}
	receipt, err := installLocal(context, []AgentSkillID{SkillRedactionPlaceholders}, []AgentToolID{ToolPi})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CLIBinary != nil {
		t.Fatal("a skill-only install named the CLI")
	}
	for i, path := range tracked {
		if mustRead(t, path) != before[i] {
			t.Fatalf("%s changed", path)
		}
	}
	codex := toolOf(Status(context), ToolCodex)
	if slices.ContainsFunc(codex.Skills, func(s AgentSkillStatus) bool { return !s.Installed }) || !codex.CLIAccessInstalled || !codex.GuardInstalled {
		t.Fatalf("codex = %+v", codex)
	}
	// Startup still keeps the CLI the earlier install copied current.
	next := filepath.Join(home, "next-astrlink-cli")
	mustWrite(t, next, "next")
	if err := SyncInstalledCLI(&InstallContext{Home: home, CLISource: next}); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, cliBinaryDest(home)) != "next" {
		t.Fatal("the CLI was not refreshed")
	}
}

func TestStartupRefreshesEveryBundleAndKeepsUserEdits(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, allSkills, []AgentToolID{ToolClaude, ToolCodex}); err != nil {
		t.Fatal(err)
	}
	dirs := func(bundle *skillBundle) []string {
		return []string{toolSkillDir(Local(home), bundle, ToolClaude), canonicalSkillDir(home, bundle)}
	}
	for _, skill := range allSkills {
		bundle := skill.bundle()
		for _, dir := range dirs(bundle) {
			mustWrite(t, filepath.Join(dir, "manifest.json"), "stale-managed")
			hashes := managedHashes(t, dir)
			hashes["manifest.json"] = sha256Hex([]byte("stale-managed"))
			if err := writeManagedManifest(dir, bundle, hashes); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(dir, "SKILL.md"), "user edit")
		}
	}
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	for _, skill := range allSkills {
		bundle := skill.bundle()
		for _, dir := range dirs(bundle) {
			if mustRead(t, filepath.Join(dir, "manifest.json")) != bundleFile(bundle, "manifest.json") ||
				mustRead(t, filepath.Join(dir, "SKILL.md")) != "user edit" {
				t.Fatalf("%s was not refreshed around the edit", dir)
			}
		}
	}
	// A reinstall keeps the edits too.
	if _, err := installLocal(context, allSkills, []AgentToolID{ToolClaude}); err != nil {
		t.Fatal(err)
	}
	for _, skill := range allSkills {
		if mustRead(t, filepath.Join(toolSkillDir(Local(home), skill.bundle(), ToolClaude), "SKILL.md")) != "user edit" {
			t.Fatal("a reinstall overwrote the edit")
		}
	}
	_ = os.Remove
}
