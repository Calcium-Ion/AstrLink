package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

var debugOnly = []AgentSkillID{SkillAstrlinkDebug}

func installedCodexContext(t *testing.T) *InstallContext {
	t.Helper()
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".codex"))
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCodex}); err != nil {
		t.Fatal(err)
	}
	return context
}

func codexBackups(t *testing.T, home string) []string {
	t.Helper()
	root := filepath.Join(AstrlinkHome(home), "agent-skill-backups")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, entry := range entries {
		backups = append(backups, filepath.Join(root, entry.Name()))
	}
	slices.Sort(backups)
	return backups
}

func toolOf(status AgentInstallStatus, id AgentToolID) AgentToolStatus {
	for _, tool := range status.Tools {
		if tool.ID == id && tool.WSL == nil {
			return tool
		}
	}
	panic("no tool " + string(id))
}

func receiptOf(home string, skills ...AgentSkillID) InstallReceipt {
	receipt := InstallReceipt{Version: receiptVersion, InstalledAtUnix: 1, CLIBinary: ptr("astrlink"), Files: []string{}, WSL: []string{}}
	for _, id := range skills {
		receipt.Skills = append(receipt.Skills, ReceiptSkill{ID: id, Version: id.bundle().version})
	}
	return receipt
}

func TestInstallAndStartupArchiveLegacyCodexCopyWithoutLosingEdits(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		context := installedCodexContext(t)
		home := context.Home
		canonical := canonicalSkillDir(home, debugBundle)
		legacy := legacyCodexSkillDir(home, debugBundle)
		if err := writeSkillTree(Local(home), debugBundle, legacy); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(canonical, "SKILL.md"), "shared user edit")
		mustWrite(t, filepath.Join(legacy, "SKILL.md"), "legacy user edit")
		mustWrite(t, filepath.Join(legacy, "notes.txt"), "keep this extra file")
		legacyManifest := mustRead(t, managedFilesPath(legacy))

		var err error
		if reinstall {
			_, err = installLocal(context, debugOnly, []AgentToolID{ToolCodex})
		} else {
			err = SyncInstalledSkills(Local(home))
		}
		if err != nil {
			t.Fatal(err)
		}
		if pathExists(legacy) || mustRead(t, filepath.Join(canonical, "SKILL.md")) != "shared user edit" {
			t.Fatal("the shared copy was not kept")
		}
		backups := codexBackups(t, home)
		if len(backups) != 1 {
			t.Fatalf("backups = %v", backups)
		}
		archived := filepath.Join(backups[0], debugBundle.name)
		if mustRead(t, filepath.Join(archived, "SKILL.md")) != "legacy user edit" ||
			mustRead(t, filepath.Join(archived, "notes.txt")) != "keep this extra file" ||
			mustRead(t, managedFilesPath(archived)) != legacyManifest {
			t.Fatal("the archive lost the legacy copy")
		}
		if !toolOf(Status(context), ToolCodex).skill(SkillAstrlinkDebug).Installed {
			t.Fatal("Codex lost its skill")
		}

		// Startup and a later reinstall must not recreate the duplicate.
		if err := SyncInstalledSkills(Local(home)); err != nil {
			t.Fatal(err)
		}
		if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCodex}); err != nil {
			t.Fatal(err)
		}
		if pathExists(legacy) || !slices.Equal(codexBackups(t, home), backups) {
			t.Fatal("the duplicate came back")
		}
		if err := Uninstall(context); err != nil {
			t.Fatal(err)
		}
		if pathExists(canonical) || !isFile(filepath.Join(archived, "notes.txt")) {
			t.Fatal("uninstall touched the archive or kept the skill")
		}
	}
}

func TestStartupMigrationRequiresReceipt(t *testing.T) {
	home := t.TempDir()
	if _, err := writeCanonicalSkill(Local(home), debugBundle); err != nil {
		t.Fatal(err)
	}
	legacy := legacyCodexSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, legacy); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	assertRealSkillCopy(t, home, debugBundle, legacy)
	if len(codexBackups(t, home)) != 0 {
		t.Fatal("a backup was made")
	}
}

func TestStartupMovesLoneLegacyCodexCopyToSharedDirectory(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	canonical := canonicalSkillDir(home, debugBundle)
	legacy := legacyCodexSkillDir(home, debugBundle)
	mustMkdir(t, filepath.Dir(legacy))
	if err := os.Rename(canonical, legacy); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(legacy, "SKILL.md"), "keep legacy customization")
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	if pathExists(legacy) || mustRead(t, filepath.Join(canonical, "SKILL.md")) != "keep legacy customization" || len(codexBackups(t, home)) != 0 {
		t.Fatal("the lone copy did not move")
	}
}

func TestMigrationAndUninstallPreserveForeignCodexDirectory(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	legacy := legacyCodexSkillDir(home, debugBundle)
	mustWrite(t, filepath.Join(legacy, "SKILL.md"), "not ours")
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCodex}); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, filepath.Join(legacy, "SKILL.md")) != "not ours" || len(codexBackups(t, home)) != 0 {
		t.Fatal("a foreign directory was touched")
	}
}

func TestMigrationRefusesForeignSharedDirectoryAndPreservesLegacyCopy(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	canonical := canonicalSkillDir(home, debugBundle)
	legacy := legacyCodexSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managedFilesPath(canonical)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(canonical, "SKILL.md"), "foreign shared skill")
	if err := SyncInstalledSkills(Local(home)); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v", err)
	}
	assertRealSkillCopy(t, home, debugBundle, legacy)
	if len(codexBackups(t, home)) != 0 {
		t.Fatal("a backup was made")
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(legacy) || mustRead(t, filepath.Join(canonical, "SKILL.md")) != "foreign shared skill" {
		t.Fatal("uninstall kept ours or removed theirs")
	}
}

func TestUninstallRemovesLegacyCodexInstallationBeforeStartupMigration(t *testing.T) {
	context := installedCodexContext(t)
	home := context.Home
	legacy := legacyCodexSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, legacy); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(canonicalSkillDir(home, debugBundle)) || pathExists(legacy) ||
		toolOf(Status(context), ToolCodex).skill(SkillAstrlinkDebug).Installed {
		t.Fatal("a copy stayed")
	}
}

func TestStartupRemovesLegacyCodexLinksAndRepairsMissingSharedSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for _, relative := range []bool{false, true} {
		for _, missingShared := range []bool{false, true} {
			context := installedCodexContext(t)
			home := context.Home
			canonical := canonicalSkillDir(home, debugBundle)
			legacy := legacyCodexSkillDir(home, debugBundle)
			mustMkdir(t, filepath.Dir(legacy))
			target := canonical
			if relative {
				target = "../../.agents/skills/astrlink-debug"
			}
			if err := os.Symlink(target, legacy); err != nil {
				t.Fatal(err)
			}
			if missingShared {
				if err := os.RemoveAll(canonical); err != nil {
					t.Fatal(err)
				}
			}
			if err := SyncInstalledSkills(Local(home)); err != nil {
				t.Fatal(err)
			}
			if pathExists(legacy) {
				t.Fatal("the link stayed")
			}
			assertRealSkillCopy(t, home, debugBundle, canonical)
			if len(codexBackups(t, home)) != 0 {
				t.Fatal("a backup was made")
			}
		}
	}
}

func TestHashGateOverwritesUnchangedFilesAndKeepsEdits(t *testing.T) {
	home := t.TempDir()
	dest := canonicalSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, dest); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dest, "SKILL.md")
	mustWrite(t, skill, "stale-managed")
	hashes := managedHashes(t, dest)
	hashes["SKILL.md"] = sha256Hex([]byte("stale-managed"))
	if err := writeManagedManifest(dest, debugBundle, hashes); err != nil {
		t.Fatal(err)
	}
	if err := writeSkillTree(Local(home), debugBundle, dest); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, skill) != renderedSkill(home) {
		t.Fatal("an unchanged file was not refreshed")
	}
	mustWrite(t, skill, "user-edit")
	if err := writeSkillTree(Local(home), debugBundle, dest); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, skill) != "user-edit" {
		t.Fatal("a user edit was overwritten")
	}
}

func TestOldArrayManifestUpgradesToHashMap(t *testing.T) {
	home := t.TempDir()
	dest := canonicalSkillDir(home, debugBundle)
	mustMkdir(t, filepath.Join(dest, "references"))
	mustWrite(t, filepath.Join(dest, "SKILL.md"), "legacy")
	if err := writeJSONFile(managedFilesPath(dest), map[string]any{
		"manager": "astrlink",
		"bundle":  debugBundle.name,
		"version": "0.0.1",
		"files":   []string{"SKILL.md", "references/trajectory.md", "manifest.json"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeSkillTree(Local(home), debugBundle, dest); err != nil {
		t.Fatal(err)
	}
	assertRealSkillCopy(t, home, debugBundle, dest)
}

func TestTheManagedManifestIsWrittenAsTheTauriShellWroteIt(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, "skill")
	if err := writeManagedManifest(dest, placeholderBundle, map[string]string{"b": "2", "a": "1"}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"bundle\": \"redaction-placeholders\",\n  \"files\": {\n    \"a\": \"1\",\n    \"b\": \"2\"\n  },\n" +
		"  \"manager\": \"astrlink\",\n  \"version\": \"0.1.0\"\n}\n"
	if got := mustRead(t, managedFilesPath(dest)); got != want {
		t.Fatalf("manifest:\n%s", got)
	}
}

func TestRefusesForeignSkillDirectory(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".cursor"))
	dest := toolSkillDir(Local(home), debugBundle, ToolCursor)
	mustWrite(t, filepath.Join(dest, "SKILL.md"), "not yours")
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	_, err := installLocal(context, debugOnly, []AgentToolID{ToolCursor})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v", err)
	}
	if mustRead(t, filepath.Join(dest, "SKILL.md")) != "not yours" {
		t.Fatal("the foreign skill changed")
	}
}

func TestReplacesLegacySymlinkWithRealCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".cursor"))
	canonical, err := writeCanonicalSkill(Local(home), debugBundle)
	if err != nil {
		t.Fatal(err)
	}
	dest := toolSkillDir(Local(home), debugBundle, ToolCursor)
	mustMkdir(t, filepath.Dir(dest))
	if err := os.Symlink(canonical, dest); err != nil {
		t.Fatal(err)
	}
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCursor}); err != nil {
		t.Fatal(err)
	}
	assertRealSkillCopy(t, home, debugBundle, dest)
}

func TestSyncRequiresReceiptAndSkipsUnknownTools(t *testing.T) {
	home := t.TempDir()
	canonical := canonicalSkillDir(home, debugBundle)
	if err := writeSkillTree(Local(home), debugBundle, canonical); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(canonical, "SKILL.md")
	mustWrite(t, skill, "stale-managed")
	hashes := managedHashes(t, canonical)
	hashes["SKILL.md"] = sha256Hex([]byte("stale-managed"))
	if err := writeManagedManifest(canonical, debugBundle, hashes); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, skill) != "stale-managed" || pathExists(toolSkillDir(Local(home), debugBundle, ToolCursor)) {
		t.Fatal("sync ran without a receipt")
	}
	if err := writeJSONFile(receiptPath(home), receiptOf(home, SkillAstrlinkDebug)); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledSkills(Local(home)); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, skill) != renderedSkill(home) || pathExists(toolSkillDir(Local(home), debugBundle, ToolCursor)) {
		t.Fatal("sync did not refresh only what it installed")
	}
}

func TestSyncCLIBinaryRequiresReceipt(t *testing.T) {
	home := t.TempDir()
	dest := cliBinaryDest(home)
	stale := filepath.Join(home, "stale-astrlink-cli")
	next := filepath.Join(home, "next-astrlink-cli")
	mustWrite(t, stale, "stale")
	mustWrite(t, next, "next")
	context := &InstallContext{Home: home, CLISource: next}
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(dest) {
		t.Fatal("a CLI was installed without a receipt")
	}
	if err := writeJSONFile(receiptPath(home), receiptOf(home, SkillAstrlinkDebug)); err != nil {
		t.Fatal(err)
	}
	if err := copyCLIBinary(stale, dest); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, dest) != "next" {
		t.Fatal("the CLI was not refreshed")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dest)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, %v", info.Mode(), err)
		}
	}
	// A dev build without a sidecar keeps the installed CLI.
	if err := SyncInstalledCLI(&InstallContext{Home: home, CLISource: filepath.Join(home, "missing")}); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, dest) != "next" {
		t.Fatal("the CLI changed")
	}
}

func TestRejectsEmptyOrUndetectedSelectionBeforeWriting(t *testing.T) {
	home := t.TempDir()
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	mustMkdir(t, filepath.Join(home, ".grok"))
	for _, tc := range []struct {
		skills []AgentSkillID
		tools  []AgentToolID
		want   string
	}{
		{nil, []AgentToolID{ToolGrok}, "select at least one skill"},
		{debugOnly, nil, "select at least one agent tool"},
		{debugOnly, []AgentToolID{ToolGrok, ToolCursor}, "selected agent tool Cursor is no longer detected"},
	} {
		if _, err := installLocal(context, tc.skills, tc.tools); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("err = %v, want %s", err, tc.want)
		}
	}
	for _, path := range []string{
		filepath.Join(home, ".cursor"), canonicalSkillDir(home, debugBundle), cliBinaryDest(home),
		receiptPath(home), toolSkillDir(Local(home), debugBundle, ToolGrok),
	} {
		if pathExists(path) {
			t.Fatalf("%s was written", path)
		}
	}
}

func TestInstallAndStartupRetireTheLegacyMCPServer(t *testing.T) {
	for _, startup := range []bool{false, true} {
		home := t.TempDir()
		for _, dir := range []string{".cursor", ".claude", ".codex", ".grok"} {
			mustMkdir(t, filepath.Join(home, dir))
		}
		legacy := legacyMCPBinaryDest(home)
		mustWrite(t, legacy, "mcp")
		jsonConfig := format.Encode(map[string]any{
			"mcpServers": map[string]any{
				"astrlink": map[string]any{"type": "stdio", "command": legacy, "args": []any{}},
				"keep":     map[string]any{"command": "x"},
			},
			"theme": "dark",
		})
		tomlConfig := "model = \"keep-model\"\n\n[mcp_servers.keep]\ncommand = \"x\"\n\n" +
			"[mcp_servers.astrlink]\ncommand = " + format.Encode(legacy) + "\nargs = []\n"
		for _, id := range allTools {
			path, ok := legacyMCPConfigPath(home, id)
			if !ok {
				continue
			}
			config := tomlConfig
			if id == ToolCursor || id == ToolClaude {
				config = jsonConfig
			}
			mustWrite(t, path, config)
		}
		context := newContext(home)
		mustWrite(t, context.CLISource, "cli")
		if startup {
			// Startup migrates only installs AstrLink made.
			if err := SyncInstalledCLI(context); err != nil {
				t.Fatal(err)
			}
			if !isFile(legacy) {
				t.Fatal("startup migrated without a receipt")
			}
			if err := writeJSONFile(receiptPath(home), map[string]any{"version": 1, "bundle": debugBundle.name, "mcp_binary": legacy}); err != nil {
				t.Fatal(err)
			}
			if err := SyncInstalledCLI(context); err != nil {
				t.Fatal(err)
			}
		} else if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCursor}); err != nil {
			t.Fatal(err)
		}

		if pathExists(legacy) || mustRead(t, cliBinaryDest(home)) != "cli" {
			t.Fatal("the MCP server was not replaced by the CLI")
		}
		for _, id := range allTools {
			path, ok := legacyMCPConfigPath(home, id)
			if !ok {
				continue
			}
			raw := mustRead(t, path)
			if strings.Contains(raw, "astrlink") || !strings.Contains(raw, "keep") {
				t.Fatalf("%s: %s", id, raw)
			}
		}
		cursorPath, _ := legacyMCPConfigPath(home, ToolCursor)
		if lookup(decode(t, mustRead(t, cursorPath)), "theme") != "dark" {
			t.Fatal("the Cursor config lost its theme")
		}
		grokPath, _ := legacyMCPConfigPath(home, ToolGrok)
		grok, err := format.ParseTOML(mustRead(t, grokPath))
		if err != nil {
			t.Fatal(err)
		}
		if model, ok, _ := grok.Get("model"); !ok || model != "keep-model" {
			t.Fatal("the Grok config lost its model")
		}
	}
}

func TestStartupGrantsCLIAccessToHostsAnMCPInstallRegistered(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".cursor", ".claude", ".codex", ".grok"} {
		mustMkdir(t, filepath.Join(home, dir))
	}
	local := Local(home)
	mustWrite(t, claudeSettingsPath(local), `{"permissions":{"allow":["Bash(ls *)"]}}`)
	legacy := legacyMCPBinaryDest(home)
	receiptFile := receiptPath(home)
	listed := []string{legacy, receiptFile}
	// Grok was installed too; its host has no rule file to write.
	for _, id := range []AgentToolID{ToolClaude, ToolCodex, ToolGrok} {
		path, _ := legacyMCPConfigPath(home, id)
		mustWrite(t, path, "")
		listed = append(listed, path)
	}
	skill := toolSkillDir(local, debugBundle, ToolClaude)
	mustMkdir(t, skill)
	listed = append(listed, skill, filepath.Join(home, ".codex", "skills", debugBundle.name))
	if err := writeJSONFile(receiptFile, map[string]any{
		"version": 1, "bundle": debugBundle.name, "bundle_version": "0.1.1",
		"installed_at_unix": 7, "mcp_binary": legacy, "files": listed,
	}); err != nil {
		t.Fatal(err)
	}
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "missing"), DataDirectories: []string{filepath.Join(home, "data")}}

	// Without a CLI to allow, the receipt waits for a build that has one.
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(codexRulesPath(local)) || lookup(decode(t, mustRead(t, receiptFile)), "version") != format.Number("1") {
		t.Fatal("the receipt was upgraded without a CLI")
	}

	context.CLISource = filepath.Join(home, "src-astrlink-cli")
	mustWrite(t, context.CLISource, "cli")
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	settings := mustRead(t, claudeSettingsPath(local))
	for _, tool := range Status(context).Tools {
		expected := tool.ID == ToolClaude || tool.ID == ToolCodex
		if tool.CLIAccessInstalled != expected || tool.GuardInstalled != expected {
			t.Fatalf("%s: %+v", tool.ID, tool)
		}
	}
	if !strings.Contains(settings, "Bash(ls *)") {
		t.Fatal(settings)
	}
	var receipt InstallReceipt
	if err := jsonUnmarshal(mustRead(t, receiptFile), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Version != receiptVersion || receipt.InstalledAtUnix != 7 || receipt.CLIBinary == nil || *receipt.CLIBinary != cliBinaryDest(home) {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(receipt.Skills) != 1 || receipt.Skills[0] != (ReceiptSkill{SkillAstrlinkDebug, debugBundle.version}) {
		t.Fatalf("skills = %v", receipt.Skills)
	}
	want := []string{
		cliBinaryDest(home), skill, claudeSettingsPath(local), codexAgentsPath(local),
		codexRulesPath(local), hostGuardsPath(home), receiptFile,
	}
	if !slices.Equal(receipt.Files, want) {
		t.Fatalf("files = %v\nwant %v", receipt.Files, want)
	}

	// The upgraded receipt is not migrated again.
	if err := os.Remove(codexRulesPath(local)); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledCLI(context); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledHostGuards(local, context.DataDirectories, nil); err != nil {
		t.Fatal(err)
	}
	if pathExists(codexRulesPath(local)) || mustRead(t, claudeSettingsPath(local)) != settings {
		t.Fatal("the rules came back")
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, mustRead(t, claudeSettingsPath(local)), `{"permissions": {"allow": ["Bash(ls *)"]}}`)
}

func TestLegacyMCPRemovalKeepsRepointedAndUnreadableEntries(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".cursor", ".claude", ".codex", ".grok"} {
		mustMkdir(t, filepath.Join(home, dir))
	}
	kept := map[AgentToolID]string{
		ToolCursor: `{"mcpServers":{"astrlink":{"command":"/custom/astrlink-mcp"}}}`,
		ToolClaude: "{",
		ToolCodex:  "[mcp_servers.astrlink]\ncommand = \"/custom/astrlink-mcp\"\n",
		ToolGrok:   "[models\n",
	}
	for id, raw := range kept {
		path, _ := legacyMCPConfigPath(home, id)
		mustWrite(t, path, raw)
	}
	if err := removeLegacyMCP(home); err != nil {
		t.Fatal(err)
	}
	for id, raw := range kept {
		path, _ := legacyMCPConfigPath(home, id)
		if mustRead(t, path) != raw {
			t.Fatalf("%s changed", id)
		}
	}
}
