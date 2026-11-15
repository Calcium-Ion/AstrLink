package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// The install tests work in t.TempDir() homes only: a real home holds the
// agent tools this computer runs.

func ptr[T any](v T) *T { return &v }

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustMkdir(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func decode(t *testing.T, text string) any {
	t.Helper()
	value, err := format.DecodeJSON(text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	if !reflect.DeepEqual(decode(t, got), decode(t, want)) {
		t.Fatalf("JSON differs:\n got %s\nwant %s", got, want)
	}
}

// desktop is any desktop home; local rules do not depend on it.
func desktop() Home { return Local("/home/user") }

func (s AgentToolStatus) skill(id AgentSkillID) AgentSkillStatus {
	for _, skill := range s.Skills {
		if skill.ID == id {
			return skill
		}
	}
	panic("no skill " + string(id))
}

func installLocal(context *InstallContext, skills []AgentSkillID, tools []AgentToolID) (InstallReceipt, error) {
	var targets []AgentToolTarget
	for _, id := range tools {
		targets = append(targets, AgentToolTarget{ID: id})
	}
	return Install(context, skills, targets)
}

func bundleFile(bundle *skillBundle, relative string) string {
	for _, file := range bundle.files {
		if file.relative == relative {
			return file.contents
		}
	}
	panic("no file " + relative)
}

func renderedSkill(home string) string {
	return strings.ReplaceAll(bundleFile(debugBundle, "SKILL.md"), cliPlaceholder, cliCommand(Local(home)))
}

func managedHashes(t *testing.T, dir string) map[string]string {
	t.Helper()
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(mustRead(t, managedFilesPath(dir))), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.Files
}

func assertRealSkillCopy(t *testing.T, home string, bundle *skillBundle, dir string) {
	t.Helper()
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is not a real directory: %v", dir, err)
	}
	hashes := managedHashes(t, dir)
	for _, file := range bundle.files {
		rendered := strings.ReplaceAll(file.contents, cliPlaceholder, cliCommand(Local(home)))
		if got := mustRead(t, filepath.Join(dir, file.relative)); got != rendered {
			t.Fatalf("%s differs", file.relative)
		}
		if hashes[file.relative] != sha256Hex([]byte(rendered)) {
			t.Fatalf("%s hash differs", file.relative)
		}
	}
}

func TestBundlesMatchTheRepository(t *testing.T) {
	for _, bundle := range []*skillBundle{debugBundle, placeholderBundle} {
		root := filepath.Join("..", "..", "..", "..", "agent-bundle", bundle.name)
		var onDisk []string
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			onDisk = append(onDisk, filepath.ToSlash(relative))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var embedded []string
		for _, file := range bundle.files {
			embedded = append(embedded, file.relative)
			if got := mustRead(t, filepath.Join(root, file.relative)); got != file.contents {
				t.Errorf("%s/%s differs from agent-bundle; copy it into internal/agents/bundle", bundle.name, file.relative)
			}
		}
		slices.Sort(onDisk)
		slices.Sort(embedded)
		if !slices.Equal(onDisk, embedded) {
			t.Errorf("%s files: agent-bundle has %v, the embedded copy %v", bundle.name, onDisk, embedded)
		}
	}
}

func TestBundlesMatchTheirManifestsAndThePlaceholderSkillStaysNeutral(t *testing.T) {
	for _, skill := range allSkills {
		bundle := skill.bundle()
		var manifest struct {
			Name     string        `json:"name"`
			Version  string        `json:"version"`
			Adapters []AgentToolID `json:"adapters"`
		}
		if err := json.Unmarshal([]byte(bundleFile(bundle, "manifest.json")), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Name != bundle.name || manifest.Version != bundle.version || string(skill) != bundle.name {
			t.Errorf("%s manifest says %s %s, the bundle %s", bundle.name, manifest.Name, manifest.Version, bundle.version)
		}
		if !slices.Equal(manifest.Adapters, allTools) {
			t.Errorf("%s adapters = %v", bundle.name, manifest.Adapters)
		}
		if !strings.HasPrefix(bundleFile(bundle, "SKILL.md"), "---\nname: "+bundle.name+"\n") {
			t.Errorf("%s SKILL.md does not start with its name", bundle.name)
		}
		drives := slices.ContainsFunc(bundle.files, func(f skillFile) bool { return strings.Contains(f.contents, cliPlaceholder) })
		if bundle.needsCLI != drives {
			t.Errorf("%s needsCLI = %v", bundle.name, bundle.needsCLI)
		}
	}
	// Hosts may send the skill upstream, so it must not name the gateway.
	for _, file := range placeholderBundle.files {
		if strings.Contains(strings.ToLower(file.contents), "astrlink") {
			t.Errorf("%s names the gateway", file.relative)
		}
	}
}

func TestClaudePatternsFollowTheDocumentedPathForms(t *testing.T) {
	for _, tc := range []struct {
		path    string
		windows bool
		want    string
	}{
		{"/Users/a/Library/Application Support/com.x", false, "//Users/a/Library/Application Support/com.x"},
		{`C:\Users\a\AppData\Roaming\com.x`, true, "//c/Users/a/AppData/Roaming/com.x"},
		{`\\?\D:\data\`, true, "//d/data"},
		{"/srv/a[1]*?(x)", false, `//srv/a\[1\]\*\?\(x\)`},
		{`\\server\share\x`, true, ""},
		{"relative/path", false, ""},
		{"/", false, ""},
	} {
		got, ok := ClaudeAbsolutePattern(tc.path, tc.windows)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("ClaudeAbsolutePattern(%q, %v) = %q, %v; want %q", tc.path, tc.windows, got, ok, tc.want)
		}
	}

	windows := runtime.GOOS == "windows"
	data := []string{"/data/astrlink"}
	rules := ClaudeDenyRules(desktop(), data, nil)
	if windows {
		if len(rules) != 4 {
			t.Fatalf("rules = %v", rules)
		}
	} else if rules[0] != "Read(//data/astrlink/**)" {
		t.Fatalf("rules = %v", rules)
	}
	// Both instances' session files: the default one and the development
	// one beside it.
	for _, rule := range []string{"Read(~/.astrlink/control-session.json)", "Read(~/.astrlink/control-session-dev.json)", "Bash(sqlite3 *)", "Bash(sqlite3*)"} {
		if !slices.Contains(rules, rule) {
			t.Fatalf("rules %v lack %s", rules, rule)
		}
	}
	if got := ClaudeDenyRules(desktop(), nil, nil); len(got) != 4 {
		t.Fatalf("rules = %v", got)
	}

	// The pin file sits apart from the data on Linux and gets the same kind
	// of rule as the data directory, and only that kind.
	pins := []string{"/config/astrlink/raw-key-pins.json"}
	withPins := ClaudeDenyRules(desktop(), data, pins)
	var pinRules []string
	for _, rule := range withPins {
		if strings.Contains(rule, "raw-key-pins.json") {
			pinRules = append(pinRules, rule)
		}
	}
	if windows {
		if len(pinRules) != 0 {
			t.Fatalf("pin rules = %v", pinRules)
		}
	} else {
		if !slices.Equal(pinRules, []string{"Read(//config/astrlink/raw-key-pins.json)"}) || len(withPins) != len(rules)+1 {
			t.Fatalf("pin rules = %v", withPins)
		}
	}
	if got := ClaudeDenyRules(desktop(), nil, pins); !slices.Equal(got[1:], rules[1:]) {
		t.Fatalf("rules = %v", got)
	}

	// Every instance's directory and pin file get a rule, data first.
	both := ClaudeDenyRules(desktop(), []string{data[0], "/data/other"}, []string{pins[0], "/config/other/raw-key-pins.json"})
	if !windows {
		want := []string{
			"Read(//data/astrlink/**)",
			"Read(//data/other/**)",
			"Read(//config/astrlink/raw-key-pins.json)",
			"Read(//config/other/raw-key-pins.json)",
		}
		if !slices.Equal(both[:4], want) || !slices.Equal(both[4:], rules[1:]) {
			t.Fatalf("rules = %v", both)
		}
	}
}

func TestClaudeDenyMergeKeepsUserRulesAndRemovesOnlyItsOwn(t *testing.T) {
	existing := `{"model":"opus","permissions":{"allow":["Bash(ls *)"],"deny":["Read(./.env)","Bash(sqlite3 *)"]}}`
	rules := []string{"Read(//data/**)", "Bash(sqlite3 *)", "Bash(sqlite3*)"}
	merged, record, err := MergeClaudeSettingsRules(&existing, "deny", rules, nil)
	if err != nil {
		t.Fatal(err)
	}
	deny := lookup(decode(t, merged), "permissions", "deny")
	want := []any{"Read(./.env)", "Bash(sqlite3 *)", "Read(//data/**)", "Bash(sqlite3*)"}
	if !reflect.DeepEqual(deny, want) {
		t.Fatalf("deny = %v", deny)
	}
	// The user already had `Bash(sqlite3 *)`, so uninstall must keep it.
	if !slices.Equal(record.Managed, []string{"Read(//data/**)", "Bash(sqlite3*)"}) {
		t.Fatalf("managed = %v", record.Managed)
	}
	if record.CreatedFile || record.CreatedPermissions || record.CreatedList {
		t.Fatalf("record = %+v", record)
	}

	again, same, err := MergeClaudeSettingsRules(&merged, "deny", rules, &record)
	if err != nil {
		t.Fatal(err)
	}
	if again != merged || !reflect.DeepEqual(same, record) {
		t.Fatalf("a second merge changed the file:\n%s\n%s", again, merged)
	}

	restored, err := RemoveClaudeSettingsRules(merged, "deny", &record)
	if err != nil || restored == nil {
		t.Fatal(restored, err)
	}
	sameJSON(t, *restored, existing)
}

func TestClaudeDenyMergeReplacesStaleRulesAndCleansUpWhatItCreated(t *testing.T) {
	first := []string{"Read(//old/**)", "Bash(sqlite3*)"}
	merged, record, err := MergeClaudeSettingsRules(nil, "deny", first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !record.CreatedFile || !record.CreatedPermissions || !record.CreatedList {
		t.Fatalf("record = %+v", record)
	}
	next := []string{"Read(//new/**)", "Bash(sqlite3*)"}
	moved, record, err := MergeClaudeSettingsRules(&merged, "deny", next, &record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(moved, "//old/") || !strings.Contains(moved, "//new/") {
		t.Fatal(moved)
	}
	if !slices.Equal(record.Managed, next) || !record.CreatedFile {
		t.Fatalf("record = %+v", record)
	}
	if removed, err := RemoveClaudeSettingsRules(moved, "deny", &record); err != nil || removed != nil {
		t.Fatal(removed, err)
	}

	// A key the user added later keeps the file alive.
	value := decode(t, moved).(map[string]any)
	value["theme"] = "dark"
	kept, err := RemoveClaudeSettingsRules(format.Encode(value), "deny", &record)
	if err != nil || kept == nil {
		t.Fatal(kept, err)
	}
	sameJSON(t, *kept, `{"theme":"dark"}`)

	for _, invalid := range []string{"{not json", "[]", `{"permissions":[]}`, `{"permissions":{"deny":{}}}`} {
		if _, _, err := MergeClaudeSettingsRules(ptr(invalid), "deny", next, nil); err == nil {
			t.Errorf("%s merged", invalid)
		}
	}
}

func TestClaudeRuleRecordsReadTheirOldName(t *testing.T) {
	var record ClaudeRuleRecord
	if err := json.Unmarshal([]byte(`{"rules":[],"managed":["a"],"created_file":false,"created_permissions":true,"created_deny":true}`), &record); err != nil {
		t.Fatal(err)
	}
	if !record.CreatedList || !record.CreatedPermissions || !slices.Equal(record.Managed, []string{"a"}) {
		t.Fatalf("record = %+v", record)
	}
}

func TestCodexGuardBlockRoundTripsAndReplacesItself(t *testing.T) {
	user := "# My rules\n\nBe terse.\n"
	data := []string{"/data/astrlink"}
	pins := []string{"/config/astrlink/raw-key-pins.json"}
	block := codexGuardBlock(desktop(), data, nil)
	if !strings.Contains(block, "/data/astrlink") || !strings.Contains(block, "observer access") ||
		strings.Contains(block, "skill") || strings.Contains(block, "raw key pin file") {
		t.Fatal(block)
	}
	pinned := codexGuardBlock(desktop(), data, pins)
	if !strings.Contains(pinned, "Do not read, copy, search, or open AstrLink's data directory (`/data/astrlink`), "+
		"its raw key pin file (`/config/astrlink/raw-key-pins.json`), any `astrlink.db*` file") {
		t.Fatal(pinned)
	}
	every := codexGuardBlock(desktop(), []string{data[0], "/data/other"}, []string{pins[0], "/config/other/raw-key-pins.json"})
	if !strings.Contains(every, "Do not read, copy, search, or open AstrLink's data directories "+
		"(`/data/astrlink`, `/data/other`), its raw key pin files "+
		"(`/config/astrlink/raw-key-pins.json`, `/config/other/raw-key-pins.json`), "+
		"any `astrlink.db*` file") {
		t.Fatal(every)
	}
	merged := MergeCodexAgentsGuard(&user, block)
	if !strings.HasPrefix(merged, user) || RemoveCodexAgentsGuard(merged) != user {
		t.Fatal(merged)
	}

	other := codexGuardBlock(desktop(), []string{"/elsewhere"}, nil)
	replaced := MergeCodexAgentsGuard(&merged, other)
	if strings.Count(replaced, codexGuardBegin) != 1 || !strings.Contains(replaced, "/elsewhere") || strings.Contains(replaced, "/data/astrlink") {
		t.Fatal(replaced)
	}

	fresh := MergeCodexAgentsGuard(nil, block)
	if got := RemoveCodexAgentsGuard(fresh); got != "" {
		t.Fatal(got)
	}
	middle := "before\n\n" + block + "\n\nafter\n"
	if got := RemoveCodexAgentsGuard(middle); got != "before\n\nafter\n" {
		t.Fatal(got)
	}
}

func TestALeftoverBeginMarkerKeepsTheUserTextAfterIt(t *testing.T) {
	// The user deleted the section by hand but kept its begin marker, then
	// installed again.
	user := "# My rules\n\n" + codexGuardBegin + "\nKeep this.\n"
	block := codexGuardBlock(desktop(), []string{"/data/astrlink"}, nil)
	merged := MergeCodexAgentsGuard(&user, block)
	if got := RemoveCodexAgentsGuard(merged); got != user {
		t.Fatal(got)
	}
	other := codexGuardBlock(desktop(), []string{"/elsewhere"}, nil)
	replaced := MergeCodexAgentsGuard(&merged, other)
	if !strings.HasPrefix(replaced, user) || !strings.Contains(replaced, "/elsewhere") || strings.Contains(replaced, "/data/astrlink") {
		t.Fatal(replaced)
	}
}

func TestTheSkillNamesTheCLIByItsAbsolutePath(t *testing.T) {
	home := "/home/agent"
	if runtime.GOOS == "windows" {
		home = `C:\Users\agent`
	}
	cli := cliCommand(Local(home))
	if cli != cliBinaryDest(home) {
		t.Fatalf("cli = %s", cli)
	}
	if !strings.Contains(bundleFile(debugBundle, "SKILL.md"), cliPlaceholder) {
		t.Fatal("the skill lost its placeholder")
	}
	rendered := renderedSkill(home)
	if strings.Contains(rendered, cliPlaceholder) || !strings.Contains(rendered, cli+" sessions") {
		t.Fatal("the skill does not name the CLI")
	}
	if got := ClaudeAllowRules(Local(home)); !slices.Equal(got, []string{"Bash(" + cli + " *)"}) {
		t.Fatal(got)
	}
	rules := codexRules(Local(home))
	if !strings.HasPrefix(rules, codexRulesMarker) || !strings.Contains(rules, "pattern = ["+format.Encode(cli)+"]") || strings.Contains(rules, "~") {
		t.Fatal(rules)
	}
	// A path a shell would split is quoted as one word.
	if runtime.GOOS != "windows" {
		spaced := filepath.Join(home, "John Doe")
		if got := cliCommand(Local(spaced)); got != "'"+cliBinaryDest(spaced)+"'" {
			t.Fatal(got)
		}
		quoted := filepath.Join(home, "it's")
		if got := cliCommand(Local(quoted)); got != `'/home/agent/it'\''s/.astrlink/bin/astrlink'` {
			t.Fatal(got)
		}
	}
}

func newContext(home string) *InstallContext {
	return &InstallContext{Home: home, CLISource: filepath.Join(home, "src-astrlink-cli")}
}

func TestInstallAndUninstallDetectedTools(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".cursor", ".claude", ".codex", ".grok", ".pi", ".dsh"} {
		mustMkdir(t, filepath.Join(home, dir))
	}
	cursorMCP := `{"mcpServers":{"keep":{"command":"x"}}}`
	mustWrite(t, filepath.Join(home, ".cursor", "mcp.json"), cursorMCP)
	grokConfig := "[models]\ndefault = \"keep-model\"\n\n[mcp_servers.keep]\ncommand = \"x\"\n"
	mustWrite(t, filepath.Join(home, ".grok", "config.toml"), grokConfig)
	context := newContext(home)
	mustWrite(t, context.CLISource, "cli")

	before := Status(context)
	for _, tool := range before.Tools {
		if !tool.Detected || tool.CLIAccessInstalled || slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return s.Installed }) {
			t.Fatalf("before: %+v", tool)
		}
	}

	receipt, err := installLocal(context, allSkills, allTools)
	if err != nil {
		t.Fatal(err)
	}
	wantSkills := []ReceiptSkill{{SkillAstrlinkDebug, debugBundle.version}, {SkillRedactionPlaceholders, placeholderBundle.version}}
	if !reflect.DeepEqual(receipt.Skills, wantSkills) {
		t.Fatalf("skills = %v", receipt.Skills)
	}
	if receipt.CLIBinary == nil || *receipt.CLIBinary != cliBinaryDest(home) || mustRead(t, cliBinaryDest(home)) != "cli" {
		t.Fatalf("cli = %v", receipt.CLIBinary)
	}
	for _, skill := range allSkills {
		bundle := skill.bundle()
		for _, id := range allTools {
			assertRealSkillCopy(t, home, bundle, toolSkillDir(Local(home), bundle, id))
		}
		if pathExists(legacyCodexSkillDir(home, bundle)) {
			t.Fatal("a Codex copy was written")
		}
		shared := canonicalSkillDir(home, bundle)
		count := 0
		for _, path := range receipt.Files {
			if path == shared {
				count++
			}
		}
		if count != 1 || slices.Contains(receipt.Files, legacyCodexSkillDir(home, bundle)) {
			t.Fatalf("files = %v", receipt.Files)
		}
	}

	after := Status(context)
	shared := canonicalSkillDir(home, debugBundle)
	var sharing []AgentToolID
	for _, tool := range after.Tools {
		if slices.Contains(tool.skill(SkillAstrlinkDebug).PreviewPaths, shared) {
			sharing = append(sharing, tool.ID)
		}
		for _, skill := range tool.Skills {
			if slices.Contains(skill.PreviewPaths, legacyCodexSkillDir(home, debugBundle)) {
				t.Fatal("a preview names the Codex copy")
			}
		}
	}
	// Codex, Pi, and DeepSeek Harness all read the one shared copy.
	if !slices.Equal(sharing, []AgentToolID{ToolCodex, ToolPi, ToolDsh}) {
		t.Fatalf("sharing = %v", sharing)
	}
	if !after.CLIBinary {
		t.Fatal("no CLI")
	}
	for _, tool := range after.Tools {
		want := map[AgentToolID]AgentCLIAccessKind{
			ToolClaude: AccessAllowRules, ToolCodex: AccessExecPolicy,
			ToolPi: AccessUnrestricted, ToolDsh: AccessUnrestricted,
			ToolCursor: AccessPrompt, ToolGrok: AccessPrompt,
		}[tool.ID]
		if !tool.Detected || slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return !s.Installed }) {
			t.Fatalf("%s: %+v", tool.ID, tool)
		}
		if tool.CLIAccess != want || tool.CLIAccessInstalled != (want == AccessAllowRules || want == AccessExecPolicy) {
			t.Fatalf("%s: %+v", tool.ID, tool)
		}
	}
	// The CLI needs no host MCP entry, so other servers stay untouched.
	if mustRead(t, filepath.Join(home, ".cursor", "mcp.json")) != cursorMCP ||
		mustRead(t, filepath.Join(home, ".grok", "config.toml")) != grokConfig {
		t.Fatal("an MCP config changed")
	}

	if err := Uninstall(context); err != nil {
		t.Fatal(err)
	}
	gone := Status(context)
	if gone.CLIBinary {
		t.Fatal("the CLI stayed")
	}
	for _, tool := range gone.Tools {
		if tool.CLIAccessInstalled || slices.ContainsFunc(tool.Skills, func(s AgentSkillStatus) bool { return s.Installed }) {
			t.Fatalf("%s stayed: %+v", tool.ID, tool)
		}
	}
	for _, skill := range allSkills {
		for _, id := range allTools {
			if pathExists(toolSkillDir(Local(home), skill.bundle(), id)) {
				t.Fatalf("%s skill stayed", id)
			}
		}
	}
	for _, path := range []string{codexRulesPath(Local(home)), claudeSettingsPath(Local(home)), receiptPath(home)} {
		if pathExists(path) {
			t.Fatalf("%s stayed", path)
		}
	}
	if mustRead(t, filepath.Join(home, ".cursor", "mcp.json")) != cursorMCP {
		t.Fatal("the Cursor MCP config changed")
	}
}

func jsonUnmarshal(text string, v any) error { return json.Unmarshal([]byte(text), v) }
