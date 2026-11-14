package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
)

func denyList(t *testing.T, path string) []any {
	t.Helper()
	items, _ := lookup(decode(t, mustRead(t, path)), "permissions", "deny").([]any)
	return items
}

func TestInstallWritesHostGuardsAndUninstallRestoresUserFiles(t *testing.T) {
	home := t.TempDir()
	allDirs(t, home)
	local := Local(home)
	settings := claudeSettingsPath(local)
	userSettings := `{"permissions":{"deny":["Read(~/.ssh/**)"]},"env":{"A":"1"}}`
	mustWrite(t, settings, userSettings)
	agentsFile := codexAgentsPath(local)
	userAgents := "Always run tests.\n"
	mustWrite(t, agentsFile, userAgents)
	data := filepath.Join(home, "data")
	pins := filepath.Join(home, "config", "raw-key-pins.json")
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"), DataDirectories: []string{data}, RawKeyPins: []string{pins}}
	mustWrite(t, context.CLISource, "cli")

	for _, tool := range Status(context).Tools {
		if tool.GuardInstalled {
			t.Fatalf("%s guarded before install", tool.ID)
		}
	}
	receipt, err := installLocal(context, debugOnly, allTools)
	if err != nil {
		t.Fatal(err)
	}
	rulesFile := codexRulesPath(local)
	for _, path := range []string{settings, agentsFile, rulesFile, hostGuardsPath(home)} {
		if !slices.Contains(receipt.Files, path) {
			t.Fatalf("the receipt lacks %s", path)
		}
	}
	for _, tool := range Status(context).Tools {
		guard, installed := GuardSkillOnly, false
		switch tool.ID {
		case ToolClaude:
			guard, installed = GuardDenyRules, true
		case ToolCodex:
			guard, installed = GuardInstructions, true
		}
		if tool.Guard != guard || tool.GuardInstalled != installed {
			t.Fatalf("%s: %+v", tool.ID, tool)
		}
	}
	deny := denyList(t, settings)
	if deny[0] != "Read(~/.ssh/**)" {
		t.Fatalf("deny = %v", deny)
	}
	rules := ClaudeDenyRules(desktop(), []string{data}, []string{pins})
	if !slices.ContainsFunc(rules, func(rule string) bool { return strings.Contains(rule, "raw-key-pins.json") }) {
		t.Fatalf("rules = %v", rules)
	}
	for _, rule := range rules {
		if !slices.Contains(deny, any(rule)) {
			t.Fatalf("deny lacks %s", rule)
		}
	}
	allow := lookup(decode(t, mustRead(t, settings)), "permissions", "allow").([]any)
	if len(allow) != 1 || allow[0] != ClaudeAllowRules(local)[0] {
		t.Fatalf("allow = %v", allow)
	}
	if mustRead(t, rulesFile) != codexRules(local) {
		t.Fatal("the Codex rules differ")
	}
	guarded := mustRead(t, agentsFile)
	if !strings.Contains(guarded, codexGuardBegin) || !strings.Contains(guarded, "(`"+pins+"`)") {
		t.Fatal(guarded)
	}
	// No token or secret is ever written into host configuration.
	for _, path := range []string{settings, agentsFile, rulesFile} {
		raw := mustRead(t, path)
		if strings.Contains(raw, "control_token") || strings.Contains(raw, "Bearer") {
			t.Fatalf("%s holds a secret", path)
		}
	}

	// Moving the data directory updates only the managed rule.
	moved := filepath.Join(home, "moved-data")
	if err := SyncInstalledHostGuards(local, []string{moved}, []string{pins}); err != nil {
		t.Fatal(err)
	}
	synced := denyList(t, settings)
	has := func(rule string) bool { return slices.Contains(synced, any(rule)) }
	if has(ClaudeDenyRules(desktop(), []string{data}, nil)[0]) || !has(ClaudeDenyRules(desktop(), []string{moved}, nil)[0]) ||
		!has(ClaudeDenyRules(desktop(), nil, []string{pins})[0]) || !has("Read(~/.ssh/**)") {
		t.Fatalf("deny = %v", synced)
	}
	if !strings.Contains(mustRead(t, agentsFile), "moved-data") {
		t.Fatal("the Codex guard did not move")
	}

	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, mustRead(t, settings), userSettings)
	if mustRead(t, agentsFile) != userAgents || pathExists(rulesFile) || pathExists(hostGuardsPath(home)) {
		t.Fatal("uninstall did not restore the user's files")
	}
}

func TestHostGuardsCreatedByInstallAreRemovedAndSyncRespectsManualRemoval(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustWrite(t, filepath.Join(home, ".claude.json"), "{}")
	mustMkdir(t, filepath.Join(home, ".codex"))
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"), DataDirectories: []string{filepath.Join(home, "data")}}
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolClaude, ToolCodex}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{claudeSettingsPath(local), codexAgentsPath(local), codexRulesPath(local)} {
		if !isFile(path) {
			t.Fatalf("%s missing", path)
		}
	}
	// A guard or rules file the user deleted by hand stays deleted across
	// restarts.
	mustWrite(t, codexAgentsPath(local), "mine\n")
	if err := os.Remove(codexRulesPath(local)); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledHostGuards(local, []string{filepath.Join(home, "other")}, nil); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, codexAgentsPath(local)) != "mine\n" || pathExists(codexRulesPath(local)) {
		t.Fatal("sync re-created what the user removed")
	}
	if err := os.Remove(codexAgentsPath(local)); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(claudeSettingsPath(local)) || pathExists(codexAgentsPath(local)) {
		t.Fatal("files AstrLink created stayed")
	}
}

func claudeGuarded(context *InstallContext) bool {
	return toolOf(Status(context), ToolClaude).GuardInstalled
}

func TestSyncAddsTheRawKeyPinFileToGuardsInstalledBeforeIt(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustMkdir(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	settings := claudeSettingsPath(local)
	mustWrite(t, settings, `{"permissions":{"deny":["Read(~/.ssh/**)"]}}`)
	data := filepath.Join(home, "data")
	pins := filepath.Join(home, "config", "raw-key-pins.json")
	earlier := &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"), DataDirectories: []string{data}}
	mustWrite(t, earlier.CLISource, "cli")
	if _, err := installLocal(earlier, debugOnly, []AgentToolID{ToolClaude, ToolCodex}); err != nil {
		t.Fatal(err)
	}
	current := *earlier
	current.RawKeyPins = []string{pins}
	pinRule := ClaudeDenyRules(desktop(), nil, []string{pins})[0]
	if !strings.HasPrefix(pinRule, "Read(") || !strings.Contains(pinRule, "raw-key-pins.json") {
		t.Fatal(pinRule)
	}
	// The earlier rules no longer count as the whole guard.
	if claudeGuarded(&current) {
		t.Fatal("guarded without the pin rule")
	}
	if err := SyncInstalledHostGuards(local, []string{data}, []string{pins}); err != nil {
		t.Fatal(err)
	}
	deny := denyList(t, settings)
	if !slices.Contains(deny, any(pinRule)) || !slices.Contains(deny, any("Read(~/.ssh/**)")) {
		t.Fatalf("deny = %v", deny)
	}
	record, err := readHostGuards(home)
	if err != nil || !slices.Contains(record.Claude.Managed, pinRule) {
		t.Fatalf("record = %+v, %v", record, err)
	}
	if !claudeGuarded(&current) || !strings.Contains(mustRead(t, codexAgentsPath(local)), "its raw key pin file (`"+pins+"`)") {
		t.Fatal("the guards were not completed")
	}
	// Uninstall removes the pin rule with the rest of AstrLink's rules.
	if err := Uninstall(&current); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, mustRead(t, settings), `{"permissions":{"deny":["Read(~/.ssh/**)"]}}`)
}

func TestEitherInstanceKeepsAndCountsTheGuardsTheOtherWrote(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustMkdir(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	cli := filepath.Join(home, "src-astrlink-cli")
	mustWrite(t, cli, "cli")
	support := filepath.Join(home, "Application Support")
	contextOf := func(identifier string) *InstallContext {
		every := instance.EveryInstanceDirectory(filepath.Join(support, identifier))
		var pins []string
		for _, directory := range every {
			pins = append(pins, keystore.PinFile(directory))
		}
		return &InstallContext{Home: home, CLISource: cli, DataDirectories: every, RawKeyPins: pins}
	}
	installed := contextOf(instance.Identifier)
	isolated := contextOf(instance.IsolatedIdentifier)
	tools := []AgentToolID{ToolClaude, ToolCodex}
	if _, err := installLocal(installed, debugOnly, tools); err != nil {
		t.Fatal(err)
	}
	var guarded []AgentToolID
	for _, tool := range Status(isolated).Tools {
		if tool.GuardInstalled {
			guarded = append(guarded, tool.ID)
		}
	}
	if !slices.Equal(guarded, tools) {
		t.Fatalf("guarded = %v", guarded)
	}
	settings, agentsFile := claudeSettingsPath(local), codexAgentsPath(local)
	written := mustRead(t, settings) + mustRead(t, agentsFile)
	for _, identifier := range []string{instance.Identifier, instance.IsolatedIdentifier} {
		if !strings.Contains(mustRead(t, settings), identifier+"/**)") || !strings.Contains(mustRead(t, agentsFile), identifier) {
			t.Fatalf("%s is not guarded", identifier)
		}
	}
	// Installing or syncing from the other instance changes nothing.
	if _, err := installLocal(isolated, debugOnly, tools); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledHostGuards(local, isolated.DataDirectories, isolated.RawKeyPins); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, settings)+mustRead(t, agentsFile) != written {
		t.Fatal("the other instance rewrote the guards")
	}
	if err := Uninstall(isolated); err != nil {
		t.Fatal(err)
	}
	if pathExists(settings) || pathExists(agentsFile) {
		t.Fatal("the guards stayed")
	}
}

// Guards installed when only the default instance had a session file get
// the development instance's at the next startup.
func TestSyncAddsTheDevSessionFileToGuardsInstalledBeforeIt(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustMkdir(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	data := filepath.Join(home, "data")
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"), DataDirectories: []string{data}}
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolClaude, ToolCodex}); err != nil {
		t.Fatal(err)
	}
	const devRule = "Read(~/.astrlink/control-session-dev.json)"
	// Rewrite what an earlier version installed: no dev rule anywhere.
	settings, agentsFile := claudeSettingsPath(local), codexAgentsPath(local)
	mustWrite(t, settings, strings.Replace(mustRead(t, settings), `"`+devRule+`",`, "", 1))
	mustWrite(t, agentsFile, strings.Replace(mustRead(t, agentsFile),
		"the control session files (`~/.astrlink/control-session.json`, `~/.astrlink/control-session-dev.json`)",
		"`~/.astrlink/control-session.json`", 1))
	record, err := readHostGuards(home)
	if err != nil {
		t.Fatal(err)
	}
	record.Claude.Rules = slices.DeleteFunc(record.Claude.Rules, func(rule string) bool { return rule == devRule })
	record.Claude.Managed = slices.DeleteFunc(record.Claude.Managed, func(rule string) bool { return rule == devRule })
	if err := writeHostGuards(home, &record); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(denyList(t, settings), any(devRule)) || strings.Contains(mustRead(t, agentsFile), "control-session-dev.json") {
		t.Fatal("the earlier guards already name the dev session")
	}

	if err := SyncInstalledHostGuards(local, []string{data}, nil); err != nil {
		t.Fatal(err)
	}
	deny := denyList(t, settings)
	if !slices.Contains(deny, any(devRule)) || !slices.Contains(deny, any("Read(~/.astrlink/control-session.json)")) {
		t.Fatalf("deny = %v", deny)
	}
	if !strings.Contains(mustRead(t, agentsFile), "`~/.astrlink/control-session.json`, `~/.astrlink/control-session-dev.json`") {
		t.Fatal(mustRead(t, agentsFile))
	}
	if record, err := readHostGuards(home); err != nil || !slices.Contains(record.Claude.Managed, devRule) {
		t.Fatalf("record = %+v, %v", record, err)
	}
	// Uninstall removes it with the rest.
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(settings) && strings.Contains(mustRead(t, settings), "control-session") {
		t.Fatal(mustRead(t, settings))
	}
}

func TestSyncAddsCLIAccessToGuardsInstalledBeforeTheCLI(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustMkdir(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	data := filepath.Join(home, "data")
	context := &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli"), DataDirectories: []string{data}}
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolClaude, ToolCodex}); err != nil {
		t.Fatal(err)
	}
	settings := claudeSettingsPath(local)
	installed := mustRead(t, settings)

	// Recreate what the MCP-based installer left: deny rules only, and a
	// record from before the allow list was renamed.
	value := decode(t, installed)
	delete(lookup(value, "permissions").(map[string]any), "allow")
	data2, _ := json.Marshal(value)
	mustWrite(t, settings, string(data2))
	if err := os.Remove(codexRulesPath(local)); err != nil {
		t.Fatal(err)
	}
	record, err := readHostGuards(home)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	encoded, _ := json.Marshal(record)
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	claude := old["claude"].(map[string]any)
	claude["created_deny"] = claude["created_list"]
	delete(claude, "created_list")
	delete(old, "claude_allow")
	delete(old, "codex_rules")
	old["version"] = 1
	encoded, _ = json.Marshal(old)
	mustWrite(t, hostGuardsPath(home), string(encoded))
	legacy, err := readHostGuards(home)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(legacy.Claude, record.Claude) || legacy.ClaudeAllow != nil || legacy.CodexRules {
		t.Fatalf("legacy = %+v", legacy)
	}
	access := func() []AgentToolID {
		var ids []AgentToolID
		for _, tool := range Status(context).Tools {
			if tool.CLIAccessInstalled {
				ids = append(ids, tool.ID)
			}
		}
		return ids
	}
	if len(access()) != 0 {
		t.Fatal("access before sync")
	}
	if err := SyncInstalledHostGuards(local, []string{data}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(access(), []AgentToolID{ToolClaude, ToolCodex}) {
		t.Fatalf("access = %v", access())
	}
	sameJSON(t, mustRead(t, settings), installed)
	synced, err := readHostGuards(home)
	if err != nil || !synced.CodexRules || synced.ClaudeAllow == nil || !synced.ClaudeAllow.CreatedList {
		// Sync created the allow list, so uninstall removes it whole.
		t.Fatalf("synced = %+v, %v", synced, err)
	}
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if pathExists(settings) || pathExists(codexRulesPath(local)) {
		t.Fatal("uninstall left files")
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestAForeignCodexRulesFileBlocksInstallBeforeWriting(t *testing.T) {
	home := t.TempDir()
	local := Local(home)
	mustMkdir(t, filepath.Join(home, ".codex"))
	rules := codexRulesPath(local)
	foreign := "prefix_rule(pattern = [\"git\"], decision = \"allow\")\n"
	if err := writeText(rules, foreign); err != nil {
		t.Fatal(err)
	}
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolCodex}); err == nil || !strings.Contains(err.Error(), "refusing to overwrite Codex rules") {
		t.Fatalf("err = %v", err)
	}
	if mustRead(t, rules) != foreign || pathExists(codexAgentsPath(local)) || pathExists(hostGuardsPath(home)) || pathExists(receiptPath(home)) {
		t.Fatal("install wrote before refusing")
	}
	// Uninstall and startup sync leave it alone too.
	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	if err := SyncInstalledHostGuards(local, nil, nil); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, rules) != foreign {
		t.Fatal("the foreign rules changed")
	}
}

func TestInvalidClaudeSettingsBlockInstallWithoutRewritingThem(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustWrite(t, claudeSettingsPath(Local(home)), "{broken")
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")
	if _, err := installLocal(context, debugOnly, []AgentToolID{ToolClaude}); err == nil || !strings.Contains(err.Error(), "will not overwrite") {
		t.Fatalf("err = %v", err)
	}
	if mustRead(t, claudeSettingsPath(Local(home))) != "{broken" {
		t.Fatal("the settings were rewritten")
	}
}
