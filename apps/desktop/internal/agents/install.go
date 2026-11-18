package agents

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
)

// Agent tool installs: the skills AstrLink writes into the coding agents'
// skill directories, the CLI the debugging skill drives, the rules that let
// the agents run it, and the guards that keep them out of AstrLink's files.

const (
	receiptVersion    = 2
	hostGuardsVersion = 2
	managedFilesName  = ".astrlink-managed-files.json"
	codexGuardBegin   = "<!-- astrlink-debug:begin -->"
	codexGuardEnd     = "<!-- astrlink-debug:end -->"
	codexRulesMarker  = "# astrlink-debug: managed by AstrLink"
	// cliPlaceholder stands for the CLI in the skill text; installs write its
	// absolute path.
	cliPlaceholder = "{{ASTRLINK_CLI}}"
	// legacyMCPServerName is the server name the MCP-based installer
	// registered; only the migration that removes it still looks for it.
	legacyMCPServerName = "astrlink"
)

// bundleFS holds copies of the repository's agent-bundle directory, which
// go:embed cannot reach; TestBundlesMatchTheRepository keeps them equal.
//
//go:embed bundle
var bundleFS embed.FS

type skillFile struct {
	relative string
	contents string
}

// skillBundle is a skill AstrLink can install. Only a skill that drives the
// CLI brings the CLI, its host access rules, and the host guards with it.
type skillBundle struct {
	name     string
	version  string
	files    []skillFile
	needsCLI bool
}

func loadBundle(name, version string, needsCLI bool, relatives ...string) *skillBundle {
	bundle := &skillBundle{name: name, version: version, needsCLI: needsCLI}
	for _, relative := range relatives {
		data, err := bundleFS.ReadFile("bundle/" + name + "/" + relative)
		if err != nil {
			panic(err)
		}
		bundle.files = append(bundle.files, skillFile{relative: relative, contents: string(data)})
	}
	return bundle
}

var (
	debugBundle = loadBundle("astrlink-debug", "0.3.0", true,
		"SKILL.md", "references/trajectory.md", "manifest.json")
	// Hosts may send skill names and descriptions upstream with every
	// request, so this bundle stays free of AstrLink branding and of the CLI
	// placeholder.
	placeholderBundle = loadBundle("redaction-placeholders", "0.1.0", false,
		"SKILL.md", "references/shapes.md", "manifest.json")
)

// AgentSkillID names a skill AstrLink installs.
type AgentSkillID string

const (
	SkillAstrlinkDebug         AgentSkillID = "astrlink-debug"
	SkillRedactionPlaceholders AgentSkillID = "redaction-placeholders"
)

var allSkills = []AgentSkillID{SkillAstrlinkDebug, SkillRedactionPlaceholders}

// Valid reports whether id names a known skill.
func (id AgentSkillID) Valid() bool { return slices.Contains(allSkills, id) }

func (id AgentSkillID) bundle() *skillBundle {
	if id == SkillRedactionPlaceholders {
		return placeholderBundle
	}
	return debugBundle
}

// AgentToolID names an agent tool skills install into.
type AgentToolID string

const (
	ToolCursor AgentToolID = "cursor"
	ToolClaude AgentToolID = "claude"
	ToolCodex  AgentToolID = "codex"
	ToolGrok   AgentToolID = "grok"
	ToolPi     AgentToolID = "pi"
	// ToolDsh is DeepSeek Harness, whose command is `dsh`.
	ToolDsh AgentToolID = "dsh"
)

var allTools = []AgentToolID{ToolCursor, ToolClaude, ToolCodex, ToolGrok, ToolPi, ToolDsh}

// Valid reports whether id names a known tool.
func (id AgentToolID) Valid() bool { return slices.Contains(allTools, id) }

// debugName is the tool as Rust's Debug spells it in errors.
func (id AgentToolID) debugName() string {
	if id == "" {
		return ""
	}
	return strings.ToUpper(string(id[:1])) + string(id[1:])
}

// usesSharedSkills reports the tools that discover the shared
// `~/.agents/skills` directory: Codex, Pi, and DeepSeek Harness.
func (id AgentToolID) usesSharedSkills() bool {
	return id == ToolCodex || id == ToolPi || id == ToolDsh
}

// AgentGuardKind is how a host can be kept away from AstrLink's local files.
// Only Claude Code has an enforced mechanism; the others rely on prompt
// text.
type AgentGuardKind string

const (
	// GuardDenyRules is `permissions.deny` rules in `~/.claude/settings.json`.
	GuardDenyRules AgentGuardKind = "deny_rules"
	// GuardInstructions is a marked section in the host's global
	// instructions file.
	GuardInstructions AgentGuardKind = "instructions"
	// GuardSkillOnly is no verified host mechanism; the skill text is the
	// only guidance.
	GuardSkillOnly AgentGuardKind = "skill_only"
)

// AgentCLIAccessKind is how a host lets agents run the AstrLink CLI without
// asking every time.
type AgentCLIAccessKind string

const (
	// AccessAllowRules is `permissions.allow` rules in
	// `~/.claude/settings.json`.
	AccessAllowRules AgentCLIAccessKind = "allow_rules"
	// AccessExecPolicy is a Codex rules file. Codex's sandbox blocks the
	// control socket, so the rule also runs the CLI outside it.
	AccessExecPolicy AgentCLIAccessKind = "exec_policy"
	// AccessPrompt is no verified host mechanism; the host asks the user on
	// first use.
	AccessPrompt AgentCLIAccessKind = "prompt"
	// AccessUnrestricted means the host runs commands without asking, so
	// there is nothing to allow.
	AccessUnrestricted AgentCLIAccessKind = "unrestricted"
)

// AgentSkillStatus is one skill for one tool.
type AgentSkillStatus struct {
	ID        AgentSkillID `json:"id"`
	Installed bool         `json:"installed"`
	// PreviewPaths is what installing this skill for the tool writes, apart
	// from the status-wide shared paths.
	PreviewPaths []string `json:"preview_paths"`
}

// AgentToolStatus is one tool in one home. CLI access and guards come with
// `astrlink-debug`; a skill-only install leaves them untouched.
type AgentToolStatus struct {
	ID AgentToolID `json:"id"`
	// WSL is the WSL distribution the tool runs in; nil for the desktop
	// user's.
	WSL                *string            `json:"wsl"`
	Detected           bool               `json:"detected"`
	Skills             []AgentSkillStatus `json:"skills"`
	CLIAccess          AgentCLIAccessKind `json:"cli_access"`
	CLIAccessInstalled bool               `json:"cli_access_installed"`
	Guard              AgentGuardKind     `json:"guard"`
	GuardInstalled     bool               `json:"guard_installed"`
}

// AgentInstallStatus is what is installed for every tool.
type AgentInstallStatus struct {
	CLIBinary   bool              `json:"cli_binary"`
	Tools       []AgentToolStatus `json:"tools"`
	SharedPaths []string          `json:"shared_paths"`
	// WSLUnchecked names WSL distributions whose tools went unchecked
	// because they were not running; checking never starts one.
	WSLUnchecked []string `json:"wsl_unchecked"`
}

// AgentToolTarget is a tool selected for install, in the desktop user's
// home or in a WSL distribution's.
type AgentToolTarget struct {
	ID  AgentToolID `json:"id"`
	WSL *string     `json:"wsl,omitempty"`
}

func (t AgentToolTarget) distribution() string {
	if t.WSL == nil {
		return ""
	}
	return *t.WSL
}

// ReceiptSkill is one skill an install wrote.
type ReceiptSkill struct {
	ID      AgentSkillID `json:"id"`
	Version string       `json:"version"`
}

// InstallReceipt is what the latest install wrote.
type InstallReceipt struct {
	Version         uint32         `json:"version"`
	Skills          []ReceiptSkill `json:"skills"`
	InstalledAtUnix uint64         `json:"installed_at_unix"`
	// CLIBinary is nil when no selected skill drives the CLI.
	CLIBinary *string  `json:"cli_binary"`
	Files     []string `json:"files"`
	// WSL lists the WSL distributions holding an install, so uninstall and
	// startup sync reach them. Only the desktop user's receipt lists them.
	WSL []string `json:"wsl"`
}

// InstallContext is the homes installs work on and what they write.
type InstallContext struct {
	// Home is the desktop user's home.
	Home      string
	CLISource string
	// DataDirectories is Core's data directory of every AstrLink instance,
	// denied to hosts that support deny rules. The instances share the
	// user's agent tools, so either one writes and checks the same guards.
	DataDirectories []string
	// RawKeyPins is the raw key pin file of every instance, denied like the
	// data directories. It lives in the config directory, apart from the
	// data on Linux, and is listed even for an instance that keeps its pins
	// in the keychain.
	RawKeyPins []string
	// Dirs is where the tools in Home keep their configuration.
	Dirs ToolDirs
	// WSL is the WSL distributions configured alongside Home. Their tools
	// run the CLI installed in Home through WSL interop.
	WSL []WSLHome
}

// Homes returns the desktop user's home, then each WSL home.
func (c *InstallContext) Homes() []Home {
	homes := []Home{{Host: c.Home, Dirs: c.Dirs}}
	for i := range c.WSL {
		homes = append(homes, InWSL(c.Home, &c.WSL[i]))
	}
	return homes
}

func (c *InstallContext) local() Home { return c.Homes()[0] }

func (c *InstallContext) homeFor(distribution string) (Home, bool) {
	for _, home := range c.Homes() {
		if home.Distribution() == distribution {
			return home, true
		}
	}
	return Home{}, false
}

// hostGuardRecord is what AstrLink wrote into host configuration outside
// its own files, so uninstall removes exactly that and nothing the user
// added.
type hostGuardRecord struct {
	Version uint32 `json:"version"`
	// Claude is the Claude Code deny rules; the key predates the allow
	// rules.
	Claude      *ClaudeRuleRecord        `json:"claude,omitempty"`
	ClaudeAllow *ClaudeRuleRecord        `json:"claude_allow,omitempty"`
	Codex       *codexInstructionsRecord `json:"codex,omitempty"`
	// CodexRules means AstrLink wrote the Codex rules file, so startup does
	// not re-create it after the user deletes it.
	CodexRules bool `json:"codex_rules,omitempty"`
}

// ClaudeRuleRecord is the rules AstrLink keeps in one list of Claude Code's
// settings.
type ClaudeRuleRecord struct {
	// Rules is the rule set last applied; startup only rewrites the file
	// when it changes.
	Rules []string `json:"rules"`
	// Managed is the rules AstrLink inserted. Rules the user already had
	// are never listed.
	Managed            []string `json:"managed"`
	CreatedFile        bool     `json:"created_file"`
	CreatedPermissions bool     `json:"created_permissions"`
	// CreatedList means AstrLink created the rule list itself (`deny` or
	// `allow`).
	CreatedList bool `json:"created_list"`
}

// UnmarshalJSON also reads created_deny, the name created_list had before
// the allow rules.
func (r *ClaudeRuleRecord) UnmarshalJSON(data []byte) error {
	type plain ClaudeRuleRecord
	var record struct {
		plain
		CreatedDeny *bool `json:"created_deny"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	*r = ClaudeRuleRecord(record.plain)
	if record.CreatedDeny != nil && !r.CreatedList {
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(data, &keys)
		if _, ok := keys["created_list"]; !ok {
			r.CreatedList = *record.CreatedDeny
		}
	}
	r.Rules = nonNil(r.Rules)
	r.Managed = nonNil(r.Managed)
	return nil
}

func (r *ClaudeRuleRecord) clone() *ClaudeRuleRecord {
	if r == nil {
		return nil
	}
	copied := *r
	copied.Rules = slices.Clone(r.Rules)
	copied.Managed = slices.Clone(r.Managed)
	return &copied
}

type codexInstructionsRecord struct {
	CreatedFile bool `json:"created_file"`
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// AstrlinkHome is AstrLink's directory in a home.
func AstrlinkHome(home string) string { return filepath.Join(home, ".astrlink") }

// sessionNames are the control session files of the default and the
// development instance (see internal/control).
var sessionNames = []string{"control-session.json", "control-session-dev.json"}

func sessionPaths(home string) []string {
	paths := make([]string, len(sessionNames))
	for i, name := range sessionNames {
		paths[i] = filepath.Join(AstrlinkHome(home), name)
	}
	return paths
}

// Status reports what is installed for every tool.
func Status(context *InstallContext) AgentInstallStatus {
	tools := []AgentToolStatus{}
	for _, home := range context.Homes() {
		for _, id := range allTools {
			tool := toolStatus(context, home, id)
			// A WSL distribution lists only the tools it has.
			if home.WSL == nil || tool.Detected || tool.CLIAccessInstalled {
				tools = append(tools, tool)
			}
		}
	}
	return AgentInstallStatus{
		SharedPaths:  []string{receiptPath(context.Home)},
		CLIBinary:    isFile(cliBinaryDest(context.Home)),
		Tools:        tools,
		WSLUnchecked: []string{},
	}
}

// Install writes the selected skills into the selected tools, with the CLI,
// its access rules, and the host guards when a skill drives the CLI.
func Install(context *InstallContext, skillIDs []AgentSkillID, tools []AgentToolTarget) (InstallReceipt, error) {
	defer LockHostFiles()()
	if len(skillIDs) == 0 {
		return InstallReceipt{}, errors.New("select at least one skill to install")
	}
	if len(tools) == 0 {
		return InstallReceipt{}, errors.New("select at least one agent tool to install")
	}
	for _, tool := range tools {
		home, ok := context.homeFor(tool.distribution())
		if !ok {
			return InstallReceipt{}, fmt.Errorf("WSL distribution %s is no longer available", tool.distribution())
		}
		if !toolDetected(home, tool.ID) {
			return InstallReceipt{}, fmt.Errorf("selected agent tool %s is no longer detected", tool.ID.debugName())
		}
	}
	needsCLI := false
	for _, id := range skillIDs {
		needsCLI = needsCLI || id.bundle().needsCLI
	}
	if needsCLI && !isFile(context.CLISource) {
		return InstallReceipt{}, errors.New("unable to locate astrlink-cli. Build desktop sidecars first (bun run sidecar:build).")
	}
	files := []string{}
	var cliBinary *string
	if needsCLI {
		cliDest := cliBinaryDest(context.Home)
		if err := copyCLIBinary(context.CLISource, cliDest); err != nil {
			return InstallReceipt{}, err
		}
		files = append(files, cliDest)
		cliBinary = &cliDest
		if err := removeLegacyMCP(context.Home); err != nil {
			return InstallReceipt{}, err
		}
	}

	var skills []AgentSkillID
	for _, id := range allSkills {
		if slices.Contains(skillIDs, id) {
			skills = append(skills, id)
		}
	}
	distributions := RecordedWSL(context.Home)
	for _, home := range context.Homes() {
		var toolIDs []AgentToolID
		for _, id := range allTools {
			for _, tool := range tools {
				if tool.ID == id && tool.distribution() == home.Distribution() {
					toolIDs = append(toolIDs, id)
					break
				}
			}
		}
		if len(toolIDs) == 0 {
			continue
		}
		var homeFiles []string
		for _, id := range toolIDs {
			for _, skill := range skills {
				path, err := installTool(home, skill.bundle(), id)
				if err != nil {
					return InstallReceipt{}, err
				}
				homeFiles = append(homeFiles, path)
			}
		}
		if needsCLI {
			guards, err := installHostGuards(context, home, toolIDs)
			if err != nil {
				return InstallReceipt{}, err
			}
			homeFiles = append(homeFiles, guards...)
		}
		// A distribution keeps its own receipt, so it syncs and uninstalls
		// like the desktop user's home.
		if distribution := home.Distribution(); distribution != "" {
			receipt, err := writeReceipt(home.Dir(), skills, cliBinary, slices.Clone(homeFiles), []string{})
			if err != nil {
				return InstallReceipt{}, err
			}
			homeFiles = receipt.Files
			distributions = append(distributions, distribution)
		}
		files = append(files, homeFiles...)
	}
	sort.Strings(distributions)
	return writeReceipt(context.Home, skills, cliBinary, files, slices.Compact(distributions))
}

// writeReceipt records what an install wrote, the receipt itself included.
func writeReceipt(home string, skills []AgentSkillID, cliBinary *string, files, wsl []string) (InstallReceipt, error) {
	path := receiptPath(home)
	files = append(deduplicatePaths(files), path)
	receipt := InstallReceipt{
		Version:         receiptVersion,
		Skills:          []ReceiptSkill{},
		InstalledAtUnix: unixNow(),
		CLIBinary:       cliBinary,
		Files:           files,
		WSL:             nonNil(wsl),
	}
	for _, id := range skills {
		receipt.Skills = append(receipt.Skills, ReceiptSkill{ID: id, Version: id.bundle().version})
	}
	if err := writeJSONFile(path, receipt); err != nil {
		return InstallReceipt{}, err
	}
	return receipt, nil
}

// RecordedWSL is the WSL distributions the desktop user's receipt lists.
func RecordedWSL(home string) []string {
	raw, ok, err := ReadOptional(receiptPath(home))
	if err != nil || !ok {
		return []string{}
	}
	var receipt struct {
		WSL []string `json:"wsl"`
	}
	if json.Unmarshal([]byte(raw), &receipt) != nil {
		return []string{}
	}
	return nonNil(receipt.WSL)
}

// Uninstall removes every install. WSL distributions go first, so a failure
// leaves the desktop user's receipt naming them for a retry.
func Uninstall(context *InstallContext) error {
	defer LockHostFiles()()
	for i := range context.WSL {
		home := InWSL(context.Home, &context.WSL[i])
		if err := uninstallHome(home); err != nil {
			return err
		}
		if err := RemovePath(receiptPath(home.Dir())); err != nil {
			return err
		}
	}
	if err := uninstallHome(context.local()); err != nil {
		return err
	}
	if err := removeLegacyMCP(context.Home); err != nil {
		return err
	}
	if err := RemovePath(cliBinaryDest(context.Home)); err != nil {
		return err
	}
	return RemovePath(receiptPath(context.Home))
}

func uninstallHome(home Home) error {
	for _, skill := range allSkills {
		for _, id := range allTools {
			if err := uninstallTool(home, skill.bundle(), id); err != nil {
				return err
			}
		}
	}
	if err := uninstallHostGuards(home); err != nil {
		return err
	}
	for _, skill := range allSkills {
		bundle := skill.bundle()
		canonical := canonicalSkillDir(home.Dir(), bundle)
		if isOursSkill(canonical, canonical, bundle) {
			if err := RemovePath(canonical); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncInstalledSkills rewrites the installed skills that are still
// AstrLink's with this release's text.
func SyncInstalledSkills(home Home) error {
	defer LockHostFiles()()
	if !isFile(receiptPath(home.Dir())) {
		return nil
	}
	for _, skill := range allSkills {
		bundle := skill.bundle()
		if err := migrateLegacyCodexSkill(home, bundle); err != nil {
			return err
		}
		canonical := canonicalSkillDir(home.Dir(), bundle)
		if isOursSkill(canonical, canonical, bundle) {
			if err := writeSkillTree(home, bundle, canonical); err != nil {
				return err
			}
		}
		for _, id := range allTools {
			if id.usesSharedSkills() {
				continue
			}
			dest := toolSkillDir(home, bundle, id)
			if isOursSkill(dest, canonical, bundle) {
				if err := writeSkillTree(home, bundle, dest); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// SyncInstalledCLI keeps an existing install's CLI current and retires
// what the MCP-based installer wrote. A missing sidecar (a dev build
// without one) only skips the copy, and defers upgrading an MCP-era receipt
// until a CLI exists. A skill-only install never gains a CLI here.
func SyncInstalledCLI(context *InstallContext) error {
	defer LockHostFiles()()
	receipt, ok, err := ReadOptional(receiptPath(context.Home))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	migrated := removeLegacyMCP(context.Home)
	cliDest := cliBinaryDest(context.Home)
	if isFile(context.CLISource) && (isFile(cliDest) || receiptWantsCLI(receipt)) {
		if err := copyCLIBinary(context.CLISource, cliDest); err != nil {
			return err
		}
	}
	if migrated != nil {
		return migrated
	}
	if isFile(cliDest) {
		return upgradeLegacyReceipt(context, receipt)
	}
	return nil
}

// receiptVersionOf reads a receipt's version; false when it has none.
func receiptVersionOf(receipt map[string]any) (uint64, bool) {
	number, ok := receipt["version"].(format.Number)
	if !ok {
		return 0, false
	}
	var version uint64
	if _, err := fmt.Sscan(string(number), &version); err != nil || strings.ContainsAny(string(number), ".eE-") {
		return 0, false
	}
	return version, true
}

// receiptWantsCLI: every MCP-era receipt came with a CLI-driven skill; a
// current receipt names the CLI only when the install copied it.
func receiptWantsCLI(raw string) bool {
	value, err := format.DecodeJSON(raw)
	if err != nil {
		return false
	}
	receipt, ok := value.(map[string]any)
	if !ok {
		return false
	}
	// Rust orders a missing version (None) below any Some.
	if version, ok := receiptVersionOf(receipt); !ok || version < receiptVersion {
		return true
	}
	_, named := receipt["cli_binary"].(string)
	return named
}

// upgradeLegacyReceipt rewrites a receipt the MCP-based installer wrote.
// The hosts it registered lost their server entries, so Claude Code and
// Codex get the CLI access and guards a reinstall adds; without them
// Codex's sandbox blocks the CLI.
func upgradeLegacyReceipt(context *InstallContext, raw string) error {
	value, err := format.DecodeJSON(raw)
	if err != nil {
		return nil
	}
	legacy, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if version, ok := receiptVersionOf(legacy); ok && version >= receiptVersion {
		return nil
	}
	home := context.Home
	var listed []string
	if items, ok := legacy["files"].([]any); ok {
		for _, item := range items {
			if path, ok := item.(string); ok {
				listed = append(listed, path)
			}
		}
	}
	type legacyConfig struct {
		id   AgentToolID
		path string
	}
	var legacyConfigs []legacyConfig
	for _, id := range allTools {
		if path, ok := legacyMCPConfigPath(home, id); ok {
			legacyConfigs = append(legacyConfigs, legacyConfig{id, path})
		}
	}
	var tools []AgentToolID
	for _, config := range legacyConfigs {
		if (config.id == ToolClaude || config.id == ToolCodex) &&
			toolDetected(context.local(), config.id) && slices.Contains(listed, config.path) {
			tools = append(tools, config.id)
		}
	}
	receipt := receiptPath(home)
	retired := map[string]bool{legacyMCPBinaryDest(home): true, receipt: true}
	for _, config := range legacyConfigs {
		retired[config.path] = true
	}
	cliBinary := cliBinaryDest(home)
	files := []string{cliBinary}
	for _, path := range listed {
		if !retired[path] && exists(path) {
			files = append(files, path)
		}
	}
	guards, err := installHostGuards(context, Local(home), tools)
	if err != nil {
		return err
	}
	files = append(files, guards...)
	files = append(files, receipt)
	installedAt := unixNow()
	if number, ok := legacy["installed_at_unix"].(format.Number); ok {
		var parsed uint64
		if _, err := fmt.Sscan(string(number), &parsed); err == nil && !strings.ContainsAny(string(number), ".eE-") {
			installedAt = parsed
		}
	}
	return writeJSONFile(receipt, InstallReceipt{
		Version:         receiptVersion,
		Skills:          []ReceiptSkill{{ID: SkillAstrlinkDebug, Version: debugBundle.version}},
		InstalledAtUnix: installedAt,
		CLIBinary:       &cliBinary,
		Files:           deduplicatePaths(files),
		WSL:             []string{},
	})
}

// ResolveSidecar finds a helper program the app ships: in the resource
// directory's bin/, or where `make desktop-sidecar` staged it for a
// `go run` (resources/<os>-<arch>/bin/).
func ResolveSidecar(resourceDirectory, name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if resourceDirectory != "" {
		for _, root := range []string{
			resourceDirectory,
			filepath.Join(resourceDirectory, runtime.GOOS+"-"+runtime.GOARCH),
		} {
			if candidate := filepath.Join(root, "bin", name); isFile(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("unable to locate %s sidecar in the app resources", strings.TrimSuffix(name, ".exe"))
}

func canonicalSkillDir(home string, bundle *skillBundle) string {
	return filepath.Join(home, ".agents", "skills", bundle.name)
}

func legacyCodexSkillDir(home string, bundle *skillBundle) string {
	return filepath.Join(home, ".codex", "skills", bundle.name)
}

// migrateLegacyCodexSkill: Codex discovers the shared .agents directory
// itself. Older installers also wrote a .codex copy, causing both
// descriptions to enter the prompt. Keep the shared copy active and archive
// the owned duplicate outside skill search roots so local edits and extra
// files remain recoverable.
func migrateLegacyCodexSkill(home Home, bundle *skillBundle) error {
	legacy := legacyCodexSkillDir(home.Dir(), bundle)
	canonical := canonicalSkillDir(home.Dir(), bundle)
	if !isOursSkill(legacy, canonical, bundle) {
		return nil
	}
	isLink := pointsAtCanonical(legacy, canonical)
	if !isLink {
		_, err := os.Lstat(canonical)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.MkdirAll(filepath.Dir(canonical), 0o777); err != nil {
				return fmt.Errorf("unable to create shared skill directory: %v", ioError(err))
			}
			if err := os.Rename(legacy, canonical); err != nil {
				return fmt.Errorf("unable to migrate %s: %v", legacy, ioError(err))
			}
			return nil
		case err != nil:
			return fmt.Errorf("unable to inspect %s: %v", canonical, ioError(err))
		}
	}
	// Refuse a foreign shared directory and ensure a usable replacement
	// exists before removing either a real duplicate or an old (possibly
	// broken) link.
	if _, err := writeCanonicalSkill(home, bundle); err != nil {
		return err
	}
	if isLink {
		return RemovePath(legacy)
	}

	backups := filepath.Join(AstrlinkHome(home.Dir()), "agent-skill-backups")
	if err := os.MkdirAll(backups, 0o777); err != nil {
		return fmt.Errorf("unable to create %s: %v", backups, ioError(err))
	}
	for index := uint64(0); ; index++ {
		backup := filepath.Join(backups, fmt.Sprintf("codex-%d-%d", unixNow(), index))
		err := os.Mkdir(backup, 0o777)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("unable to create %s: %v", backup, ioError(err))
		}
		dest := filepath.Join(backup, bundle.name)
		if err := os.Rename(legacy, dest); err != nil {
			return fmt.Errorf("unable to archive %s to %s: %v", legacy, dest, ioError(err))
		}
		fmt.Fprintf(os.Stderr, "migrated duplicate AstrLink Codex skill to %s\n", dest)
		return nil
	}
}

func receiptPath(home string) string {
	return filepath.Join(AstrlinkHome(home), "agent-installs.json")
}

func cliBinaryDest(home string) string {
	name := "astrlink"
	if runtime.GOOS == "windows" {
		name = "astrlink.exe"
	}
	return filepath.Join(AstrlinkHome(home), "bin", name)
}

func legacyMCPBinaryDest(home string) string {
	name := "astrlink-mcp"
	if runtime.GOOS == "windows" {
		name = "astrlink-mcp.exe"
	}
	return filepath.Join(AstrlinkHome(home), "bin", name)
}

// toolDir is the directory that marks id as installed in home.
func toolDir(home Home, id AgentToolID) string {
	switch id {
	case ToolCursor:
		return home.Join(".cursor")
	case ToolClaude:
		return home.ClaudeDir()
	case ToolCodex:
		return home.CodexDir()
	case ToolGrok:
		return home.GrokDir()
	case ToolPi:
		return home.PiDir()
	default:
		return home.Join(".dsh")
	}
}

func toolDetected(home Home, id AgentToolID) bool {
	// Claude Code also counts when only its `.claude.json` is there.
	if id == ToolClaude {
		return home.ClaudeDetected()
	}
	return isDir(toolDir(home, id))
}

func toolSkillDir(home Home, bundle *skillBundle, id AgentToolID) string {
	switch id {
	case ToolCursor:
		return home.Join(".cursor", "skills", bundle.name)
	case ToolClaude:
		return filepath.Join(home.ClaudeDir(), "skills", bundle.name)
	case ToolGrok:
		return filepath.Join(home.GrokDir(), "skills", bundle.name)
	default:
		// Pi's own skill directory moves with PI_CODING_AGENT_DIR, which the
		// desktop cannot see; the shared one does not. DeepSeek Harness
		// reads both its own and the shared one, so one copy keeps it
		// listed once.
		return canonicalSkillDir(home.Dir(), bundle)
	}
}

// legacyMCPConfigPath is where the MCP-based installer registered its
// server for each host. It never supported Pi or DeepSeek Harness.
func legacyMCPConfigPath(home string, id AgentToolID) (string, bool) {
	switch id {
	case ToolCursor:
		return filepath.Join(home, ".cursor", "mcp.json"), true
	case ToolClaude:
		return filepath.Join(home, ".claude.json"), true
	case ToolCodex:
		return filepath.Join(home, ".codex", "config.toml"), true
	case ToolGrok:
		return filepath.Join(home, ".grok", "config.toml"), true
	}
	return "", false
}

func toolStatus(context *InstallContext, home Home, id AgentToolID) AgentToolStatus {
	detected := toolDetected(home, id)
	status := AgentToolStatus{
		ID:                 id,
		Detected:           detected,
		Skills:             []AgentSkillStatus{},
		CLIAccess:          toolCLIAccessKind(id),
		CLIAccessInstalled: cliAccessPresent(home, id),
		Guard:              toolGuardKind(id),
		GuardInstalled:     guardPresent(context, home, id),
	}
	if distribution := home.Distribution(); distribution != "" {
		status.WSL = &distribution
	}
	for _, skill := range allSkills {
		status.Skills = append(status.Skills, skillStatus(home, skill, id, detected))
	}
	return status
}

func skillStatus(home Home, skill AgentSkillID, id AgentToolID, detected bool) AgentSkillStatus {
	bundle := skill.bundle()
	dir := toolSkillDir(home, bundle, id)
	previewPaths := []string{dir}
	if bundle.needsCLI {
		previewPaths = append(previewPaths, cliBinaryDest(home.Host))
		if access, ok := toolCLIAccessPath(home, id); ok {
			previewPaths = append(previewPaths, access)
		}
		if guard, ok := toolGuardPath(home, id); ok {
			previewPaths = append(previewPaths, guard, hostGuardsPath(home.Dir()))
		}
	}
	// The shared paths name only the desktop user's receipt.
	if home.WSL != nil {
		previewPaths = append(previewPaths, receiptPath(home.Dir()))
	}
	// Codex, Pi, and DeepSeek Harness share one directory, so a copy there
	// only counts for a tool that is actually present.
	return AgentSkillStatus{
		ID:           skill,
		Installed:    detected && skillPresent(dir, canonicalSkillDir(home.Dir(), bundle)),
		PreviewPaths: deduplicatePaths(previewPaths),
	}
}

func skillPresent(path, canonical string) bool {
	if target, err := os.Readlink(path); err == nil {
		return samePath(target, canonical)
	}
	return isFile(filepath.Join(path, "SKILL.md"))
}

// samePath compares paths component by component, as Rust's Path does.
func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func jsonCommand(raw string) (string, bool) {
	value, err := format.DecodeJSON(raw)
	if err != nil {
		return "", false
	}
	command, ok := lookup(value, "mcpServers", legacyMCPServerName, "command").(string)
	return command, ok
}

func lookup(value any, keys ...string) any {
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func tomlCommand(raw string) (string, bool) {
	doc, err := format.ParseTOML(raw)
	if err != nil {
		return "", false
	}
	servers := format.TableLike(doc.Root().Get("mcp_servers"))
	if servers == nil {
		return "", false
	}
	server := format.TableLike(servers.Get(legacyMCPServerName))
	if server == nil {
		return "", false
	}
	command := server.Get("command")
	if command == nil {
		return "", false
	}
	return format.TOMLItemString(command)
}

// deduplicatePaths drops empty and repeated paths, keeping the first of
// each.
func deduplicatePaths(paths []string) []string {
	seen := map[string]bool{}
	kept := []string{}
	for _, path := range paths {
		if path != "" && !seen[path] {
			seen[path] = true
			kept = append(kept, path)
		}
	}
	return kept
}

func writeCanonicalSkill(home Home, bundle *skillBundle) (string, error) {
	dest := canonicalSkillDir(home.Dir(), bundle)
	return dest, writeSkillTree(home, bundle, dest)
}

func installTool(home Home, bundle *skillBundle, id AgentToolID) (string, error) {
	skill := toolSkillDir(home, bundle, id)
	// Codex, Pi, and DeepSeek Harness discover the shared directory, so
	// only write it when one of them is selected, and never next to a
	// duplicate Codex copy.
	if id.usesSharedSkills() {
		if err := migrateLegacyCodexSkill(home, bundle); err != nil {
			return "", err
		}
	}
	if err := writeSkillTree(home, bundle, skill); err != nil {
		return "", err
	}
	return skill, nil
}

func uninstallTool(home Home, bundle *skillBundle, id AgentToolID) error {
	// The shared skill is removed once, after all tool-specific
	// installations.
	var skill string
	switch id {
	case ToolCodex:
		skill = legacyCodexSkillDir(home.Dir(), bundle)
	case ToolPi, ToolDsh:
		return nil
	default:
		skill = toolSkillDir(home, bundle, id)
	}
	if isOursSkill(skill, canonicalSkillDir(home.Dir(), bundle), bundle) {
		return RemovePath(skill)
	}
	return nil
}

func writeSkillTree(home Home, bundle *skillBundle, dest string) error {
	cli := cliCommand(home)
	info, err := os.Lstat(dest)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return writeSkillFiles(dest, bundle, nil, cli)
	case err != nil:
		return fmt.Errorf("unable to inspect %s: %v", dest, ioError(err))
	case info.Mode()&fs.ModeSymlink != 0:
		if !pointsAtCanonical(dest, canonicalSkillDir(home.Dir(), bundle)) {
			return refuseOverwrite(bundle, dest)
		}
		if err := RemovePath(dest); err != nil {
			return err
		}
		return writeSkillFiles(dest, bundle, nil, cli)
	}
	managed, ok := readManagedManifest(dest)
	if !ok || !managed.isOurs(bundle) {
		return refuseOverwrite(bundle, dest)
	}
	return writeSkillFiles(dest, bundle, &managed, cli)
}

func writeSkillFiles(dest string, bundle *skillBundle, existing *managedManifest, cli string) error {
	if err := os.MkdirAll(dest, 0o777); err != nil {
		return fmt.Errorf("unable to create %s: %v", dest, ioError(err))
	}
	nextHashes := map[string]string{}
	for _, file := range bundle.files {
		path := filepath.Join(dest, filepath.FromSlash(file.relative))
		contents := strings.ReplaceAll(file.contents, cliPlaceholder, cli)
		desiredHash := sha256Hex([]byte(contents))
		overwrite := true
		var recorded string
		var hasRecorded bool
		if existing != nil && existing.hashes != nil {
			recorded, hasRecorded = existing.hashes[file.relative]
			if hasRecorded {
				data, err := os.ReadFile(path)
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					return fmt.Errorf("unable to read %s: %v", path, ioError(err))
				default:
					overwrite = sha256Hex(data) == recorded
				}
			}
		}
		if overwrite {
			if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
				return fmt.Errorf("unable to create %s: %v", filepath.Dir(path), ioError(err))
			}
			if err := os.WriteFile(path, []byte(contents), 0o666); err != nil {
				return fmt.Errorf("unable to write %s: %v", path, ioError(err))
			}
			nextHashes[file.relative] = desiredHash
		} else if hasRecorded {
			nextHashes[file.relative] = recorded
		}
	}
	return writeManagedManifest(dest, bundle, nextHashes)
}

func writeManagedManifest(dest string, bundle *skillBundle, files map[string]string) error {
	hashes := map[string]any{}
	for relative, hash := range files {
		hashes[relative] = hash
	}
	return writeTextFile(managedFilesPath(dest), format.EncodePretty(map[string]any{
		"manager": "astrlink",
		"bundle":  bundle.name,
		"version": bundle.version,
		"files":   hashes,
	})+"\n")
}

func refuseOverwrite(bundle *skillBundle, dest string) error {
	return fmt.Errorf("refusing to overwrite existing %s skill at %s", bundle.name, dest)
}

func isOursSkill(path, canonical string, bundle *skillBundle) bool {
	if pointsAtCanonical(path, canonical) {
		return true
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	managed, ok := readManagedManifest(path)
	return ok && managed.isOurs(bundle)
}

func pointsAtCanonical(path, canonical string) bool {
	target, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if samePath(target, canonical) {
		return true
	}
	resolved := target
	if !filepath.IsAbs(target) {
		resolved = filepath.Join(filepath.Dir(path), target)
	}
	if samePath(resolved, canonical) {
		return true
	}
	left, leftErr := canonicalize(resolved)
	right, rightErr := canonicalize(canonical)
	if leftErr == nil && rightErr == nil {
		return left == right
	}
	// A relative link can still be ours when its final directory was
	// deleted. Resolve the parents without requiring the leaf to exist.
	if filepath.Base(resolved) != filepath.Base(canonical) {
		return false
	}
	left, leftErr = canonicalize(filepath.Dir(resolved))
	right, rightErr = canonicalize(filepath.Dir(canonical))
	return leftErr == nil && rightErr == nil && left == right
}

// canonicalize resolves every link in an existing path.
func canonicalize(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return realpath.Resolve(absolute)
}

func managedFilesPath(dest string) string { return filepath.Join(dest, managedFilesName) }

type managedManifest struct {
	manager string
	bundle  string
	// hashes is nil when the manifest lists its files without hashes.
	hashes map[string]string
}

func (m managedManifest) isOurs(bundle *skillBundle) bool {
	return m.manager == "astrlink" && m.bundle == bundle.name
}

func readManagedManifest(dest string) (managedManifest, bool) {
	data, err := os.ReadFile(managedFilesPath(dest))
	if err != nil {
		return managedManifest{}, false
	}
	value, err := format.DecodeJSON(string(data))
	if err != nil {
		return managedManifest{}, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return managedManifest{}, false
	}
	manager, ok := object["manager"].(string)
	if !ok {
		return managedManifest{}, false
	}
	bundle, ok := object["bundle"].(string)
	if !ok {
		return managedManifest{}, false
	}
	manifest := managedManifest{manager: manager, bundle: bundle}
	if files, ok := object["files"].(map[string]any); ok {
		manifest.hashes = map[string]string{}
		for key, item := range files {
			if hash, ok := item.(string); ok {
				manifest.hashes[key] = hash
			}
		}
	}
	return manifest, true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// copyCLIBinary installs the CLI through a temporary sibling, so a CLI
// process that is still running keeps its old file instead of seeing it
// truncated.
func copyCLIBinary(source, dest string) error {
	if filesEqual(source, dest) {
		return nil
	}
	parent, name := filepath.Dir(dest), filepath.Base(dest)
	if err := os.MkdirAll(parent, 0o777); err != nil {
		return fmt.Errorf("unable to create %s: %v", parent, ioError(err))
	}
	temporary := TemporarySibling(parent, name)
	err := func() error {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(temporary, 0o755); err != nil {
				return err
			}
		}
		return prefs.AtomicReplace(temporary, dest)
	}()
	if err != nil {
		os.Remove(temporary)
		return fmt.Errorf("unable to install astrlink-cli: %v", ioError(err))
	}
	return nil
}

func filesEqual(left, right string) bool {
	a, err := os.Stat(left)
	if err != nil {
		return false
	}
	b, err := os.Stat(right)
	if err != nil || a.Size() != b.Size() {
		return false
	}
	leftData, err := os.ReadFile(left)
	if err != nil {
		return false
	}
	rightData, err := os.ReadFile(right)
	return err == nil && bytes.Equal(leftData, rightData)
}

// cliPath is the CLI's absolute path as the home's tools run it. Tools in
// WSL run the desktop's Windows CLI through interop.
func cliPath(home Home) string { return home.Shown(cliBinaryDest(home.Host)) }

// cliCommand is the CLI's absolute path as one shell word. Hosts match the
// command text literally (Codex does not expand `~`), so the skill and the
// allow rules spell it the same way.
func cliCommand(home Home) string {
	path := cliPath(home)
	plain := true
	for _, c := range path {
		if !(c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') || strings.ContainsRune(`/\:._-`, c)) {
			plain = false
			break
		}
	}
	switch {
	case plain:
		return path
	case home.Windows():
		return `"` + path + `"`
	default:
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
}

// removeLegacyMCP removes the server entries and binary the MCP-based
// installer wrote. An entry is only removed while it still runs that
// binary, so a server the user repointed or a config that no longer parses
// is left alone.
func removeLegacyMCP(home string) error {
	binary := legacyMCPBinaryDest(home)
	for _, id := range allTools {
		path, ok := legacyMCPConfigPath(home, id)
		if !ok {
			continue
		}
		raw, present, err := ReadOptional(path)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		var next string
		switch id {
		case ToolCursor, ToolClaude:
			if command, ok := jsonCommand(raw); !ok || command != binary {
				continue
			}
			if next, err = removeJSONMCP(raw); err != nil {
				return err
			}
		default:
			if command, ok := tomlCommand(raw); !ok || command != binary {
				continue
			}
			if next, err = removeTOMLMCP(raw); err != nil {
				return err
			}
		}
		if err := writeText(path, next); err != nil {
			return err
		}
	}
	// A host may still run the old server; on Windows that locks the file,
	// so a later start retries instead of failing the install.
	if err := RemovePath(binary); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return nil
}

func removeJSONMCP(existing string) (string, error) {
	value, err := format.DecodeJSON(existing)
	if err != nil {
		return "", fmt.Errorf("MCP JSON is invalid; AstrLink will not overwrite it: %v", err)
	}
	if servers, ok := lookup(value, "mcpServers").(map[string]any); ok {
		delete(servers, legacyMCPServerName)
	}
	return prettyJSON(value), nil
}

func removeTOMLMCP(existing string) (string, error) {
	doc, err := format.ParseTOML(existing)
	if err != nil {
		return "", fmt.Errorf("config.toml is invalid; AstrLink will not overwrite it: %v", err)
	}
	if servers, ok := doc.Root().Get("mcp_servers").(*format.TOMLTable); ok {
		servers.Remove(legacyMCPServerName)
	}
	return doc.Render(), nil
}

func toolCLIAccessKind(id AgentToolID) AgentCLIAccessKind {
	switch id {
	case ToolClaude:
		return AccessAllowRules
	case ToolCodex:
		return AccessExecPolicy
	case ToolCursor, ToolGrok:
		// Cursor keeps its command allowlist in app settings, and Grok
		// Build documents no rule file, so both ask the user on first use.
		return AccessPrompt
	default:
		// Pi has no permission prompts: every enabled tool runs with the
		// process's own permissions. DeepSeek Harness asks only to widen
		// its sandbox, which confines file writes and lets the CLI read.
		return AccessUnrestricted
	}
}

func toolCLIAccessPath(home Home, id AgentToolID) (string, bool) {
	switch toolCLIAccessKind(id) {
	case AccessAllowRules:
		return claudeSettingsPath(home), true
	case AccessExecPolicy:
		return codexRulesPath(home), true
	}
	return "", false
}

func cliAccessPresent(home Home, id AgentToolID) bool {
	switch toolCLIAccessKind(id) {
	case AccessAllowRules:
		return claudeRulesPresent(claudeSettingsPath(home), "allow", ClaudeAllowRules(home))
	case AccessExecPolicy:
		raw, err := os.ReadFile(codexRulesPath(home))
		return err == nil && string(raw) == codexRules(home)
	}
	return false
}

// ClaudeAllowRules is the allow rules for Claude Code, which otherwise asks
// before every command.
func ClaudeAllowRules(home Home) []string {
	return []string{"Bash(" + cliCommand(home) + " *)"}
}

func codexRulesPath(home Home) string {
	return filepath.Join(home.CodexDir(), "rules", "astrlink.rules")
}

// codexRules is a Codex rules file allowing the CLI. Codex's sandbox blocks
// the local control socket, and an allow rule also runs the command outside
// it.
func codexRules(home Home) string {
	pattern := format.Encode(cliPath(home))
	return codexRulesMarker + "\n" +
		"# Lets agents run AstrLink's read-only debugging CLI outside the sandbox,\n" +
		"# which blocks its local control socket. Removed when AstrLink's agent\n" +
		"# debugging tools are uninstalled.\n" +
		"prefix_rule(\n" +
		"    pattern = [" + pattern + "],\n" +
		"    decision = \"allow\",\n" +
		"    justification = \"AstrLink read-only debugging CLI\",\n" +
		")\n"
}

func claudeSettingsPath(home Home) string { return filepath.Join(home.ClaudeDir(), "settings.json") }

func codexAgentsPath(home Home) string { return filepath.Join(home.CodexDir(), "AGENTS.md") }

func hostGuardsPath(home string) string {
	return filepath.Join(AstrlinkHome(home), "agent-host-guards.json")
}

func toolGuardKind(id AgentToolID) AgentGuardKind {
	switch id {
	case ToolClaude:
		return GuardDenyRules
	case ToolCodex:
		return GuardInstructions
	default:
		// Cursor keeps its global ignore list in app settings without a
		// documented file, and Grok Build documents no global instructions
		// file, so neither is written. Pi's global AGENTS.md moves with
		// PI_CODING_AGENT_DIR, and DeepSeek Harness's with DSH_HOME, which
		// the desktop cannot see, so neither is written either.
		return GuardSkillOnly
	}
}

func toolGuardPath(home Home, id AgentToolID) (string, bool) {
	switch toolGuardKind(id) {
	case GuardDenyRules:
		return claudeSettingsPath(home), true
	case GuardInstructions:
		return codexAgentsPath(home), true
	}
	return "", false
}

func guardPresent(context *InstallContext, home Home, id AgentToolID) bool {
	path, ok := toolGuardPath(home, id)
	if !ok {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if id == ToolClaude {
		return claudeRulesPresent(path, "deny", ClaudeDenyRules(home, context.DataDirectories, context.RawKeyPins))
	}
	_, _, found := codexGuardRange(string(raw))
	return found
}

func claudeRulesPresent(path, list string, rules []string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	value, err := format.DecodeJSON(string(raw))
	if err != nil {
		return false
	}
	items, ok := lookup(value, "permissions", list).([]any)
	if !ok {
		return false
	}
	for _, rule := range rules {
		if !slices.ContainsFunc(items, func(item any) bool { return item == rule }) {
			return false
		}
	}
	return true
}

// ClaudeDenyRules is the deny rules for Claude Code. Read rules also cover
// the Bash file commands Claude Code recognises (`cat`, `head`, `tail`,
// `sed`, `tee`) and redirections; the two `sqlite3` forms cover
// `sqlite3 <file>` and `sqlite3<anything>` binaries such as
// `sqlite3_analyzer`. In WSL the desktop's files sit under the drive mounts,
// the session file included.
func ClaudeDenyRules(home Home, dataDirectories, rawKeyPins []string) []string {
	pattern := func(path string) (string, bool) {
		seen, ok := home.Seen(path)
		if !ok {
			return "", false
		}
		return ClaudeAbsolutePattern(seen, home.Windows())
	}
	rules := []string{}
	for _, directory := range dataDirectories {
		if pattern, ok := pattern(directory); ok {
			rules = append(rules, "Read("+pattern+"/**)")
		}
	}
	for _, pin := range rawKeyPins {
		if pattern, ok := pattern(pin); ok {
			rules = append(rules, "Read("+pattern+")")
		}
	}
	for i, path := range sessionPaths(home.Host) {
		if home.WSL == nil {
			rules = append(rules, "Read(~/.astrlink/"+sessionNames[i]+")")
		} else if session, ok := pattern(path); ok {
			rules = append(rules, "Read("+session+")")
		}
	}
	return append(rules, "Bash(sqlite3 *)", "Bash(sqlite3*)")
}

// ClaudeAbsolutePattern converts an absolute path to a Claude Code `//`
// pattern. Claude Code normalises Windows paths to POSIX form
// (`C:\Users\a` becomes `/c/Users/a`) before matching, and patterns use
// gitignore syntax, so wildcard characters in the path are escaped. UNC
// paths have no documented form and are skipped.
func ClaudeAbsolutePattern(path string, windows bool) (string, bool) {
	posix := path
	if windows {
		path = strings.TrimPrefix(path, `\\?\`)
		if len(path) < 2 || !isASCIIAlpha(path[0]) || path[1] != ':' {
			return "", false
		}
		rest := strings.ReplaceAll(path[2:], `\`, "/")
		if !strings.HasPrefix(rest, "/") {
			return "", false
		}
		posix = "/" + strings.ToLower(path[:1]) + rest
	} else if !strings.HasPrefix(path, "/") {
		return "", false
	}
	trimmed := strings.TrimRight(posix, "/")
	if trimmed == "" {
		return "", false
	}
	var pattern strings.Builder
	pattern.WriteByte('/')
	for _, c := range trimmed {
		if strings.ContainsRune(`*?[]\()`, c) {
			pattern.WriteByte('\\')
		}
		pattern.WriteRune(c)
	}
	return pattern.String(), true
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// MergeClaudeSettingsRules adds rules to `permissions.<list>` without
// touching the user's own rules. existing is nil when the file does not
// exist. Managed rules from an earlier install that rules no longer
// contains are removed.
func MergeClaudeSettingsRules(existing *string, list string, rules []string, previous *ClaudeRuleRecord) (string, ClaudeRuleRecord, error) {
	var value any = map[string]any{}
	if existing != nil && strings.TrimSpace(*existing) != "" {
		decoded, err := format.DecodeJSON(*existing)
		if err != nil {
			return "", ClaudeRuleRecord{}, fmt.Errorf("Claude settings.json is invalid; AstrLink will not overwrite it: %v", err)
		}
		value = decoded
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "", ClaudeRuleRecord{}, errors.New("Claude settings.json root must be an object")
	}
	var prior ClaudeRuleRecord
	if previous != nil {
		prior = *previous.clone()
	}
	_, hadPermissions := root["permissions"]
	createdPermissions := prior.CreatedPermissions || !hadPermissions
	if !hadPermissions {
		root["permissions"] = map[string]any{}
	}
	permissions, ok := root["permissions"].(map[string]any)
	if !ok {
		return "", ClaudeRuleRecord{}, errors.New("Claude settings.json permissions must be an object")
	}
	_, hadList := permissions[list]
	createdList := prior.CreatedList || !hadList
	if !hadList {
		permissions[list] = []any{}
	}
	items, ok := permissions[list].([]any)
	if !ok {
		return "", ClaudeRuleRecord{}, fmt.Errorf("Claude settings.json permissions.%s must be an array", list)
	}
	for _, stale := range prior.Managed {
		if !slices.Contains(rules, stale) {
			items = removeOneRule(items, stale)
		}
	}
	managed := []string{}
	for _, rule := range rules {
		if !slices.ContainsFunc(items, func(item any) bool { return item == rule }) {
			items = append(items, rule)
			managed = append(managed, rule)
		} else if slices.Contains(prior.Managed, rule) {
			managed = append(managed, rule)
		}
	}
	permissions[list] = items
	record := ClaudeRuleRecord{
		Rules:              append([]string{}, rules...),
		Managed:            managed,
		CreatedFile:        prior.CreatedFile || existing == nil,
		CreatedPermissions: createdPermissions,
		CreatedList:        createdList,
	}
	return prettyJSON(value), record, nil
}

// RemoveClaudeSettingsRules removes the rules AstrLink inserted. It returns
// nil when the file was created by AstrLink and nothing else remains in it.
func RemoveClaudeSettingsRules(existing, list string, record *ClaudeRuleRecord) (*string, error) {
	if strings.TrimSpace(existing) == "" {
		if record.CreatedFile {
			return nil, nil
		}
		return &existing, nil
	}
	value, err := format.DecodeJSON(existing)
	if err != nil {
		return nil, fmt.Errorf("Claude settings.json is invalid; AstrLink will not overwrite it: %v", err)
	}
	if root, ok := value.(map[string]any); ok {
		if permissions, ok := root["permissions"].(map[string]any); ok {
			if items, ok := permissions[list].([]any); ok {
				for _, rule := range record.Managed {
					items = removeOneRule(items, rule)
				}
				permissions[list] = items
				if len(items) == 0 && record.CreatedList {
					delete(permissions, list)
				}
			}
			if len(permissions) == 0 && record.CreatedPermissions {
				delete(root, "permissions")
			}
		}
		if len(root) == 0 && record.CreatedFile {
			return nil, nil
		}
	}
	text := prettyJSON(value)
	return &text, nil
}

func removeOneRule(items []any, rule string) []any {
	if index := slices.IndexFunc(items, func(item any) bool { return item == rule }); index >= 0 {
		return slices.Delete(items, index, index+1)
	}
	return items
}

// codexGuardBlock: Codex loads `AGENTS.md` into every session, so this
// section only says what to avoid. Pointing at the skill here made agents
// load it for unrelated questions; the skill's own description decides
// when it applies.
func codexGuardBlock(home Home, dataDirectories, rawKeyPins []string) string {
	listed := func(paths []string) string {
		shown := make([]string, len(paths))
		for i, path := range paths {
			shown[i] = "`" + home.Shown(path) + "`"
		}
		return strings.Join(shown, ", ")
	}
	var data string
	switch len(dataDirectories) {
	case 0:
		data = "AstrLink's data directory"
	case 1:
		data = "AstrLink's data directory (" + listed(dataDirectories) + ")"
	default:
		data = "AstrLink's data directories (" + listed(dataDirectories) + ")"
	}
	if len(rawKeyPins) > 0 {
		files := "files"
		if len(rawKeyPins) == 1 {
			files = "file"
		}
		data += ", its raw key pin " + files + " (" + listed(rawKeyPins) + ")"
	}
	sessions := make([]string, len(sessionNames))
	for i, path := range sessionPaths(home.Host) {
		sessions[i] = "`~/.astrlink/" + sessionNames[i] + "`"
		if home.WSL != nil {
			sessions[i] = "`" + home.Shown(path) + "`"
		}
	}
	return codexGuardBegin + "\n" +
		"## AstrLink local data\n" +
		"\n" +
		"AstrLink added this section with its agent debugging tools and removes it when they are uninstalled.\n" +
		"\n" +
		"- Do not read, copy, search, or open " + data + ", any `astrlink.db*` file, or the control session files (" + strings.Join(sessions, ", ") + "), and do not run `sqlite3` on them.\n" +
		"- The control socket and the session token only carry observer access. Do not use them to change AstrLink settings.\n" +
		codexGuardEnd
}

func codexGuardRange(raw string) (int, int, bool) {
	first := strings.Index(raw, codexGuardBegin)
	if first < 0 {
		return 0, 0, false
	}
	offset := strings.Index(raw[first:], codexGuardEnd)
	if offset < 0 {
		return 0, 0, false
	}
	end := first + offset
	// A begin marker left behind by a hand edit must not pull the user's
	// text after it into the section.
	start := strings.LastIndex(raw[:end], codexGuardBegin)
	return start, end + len(codexGuardEnd), true
}

// MergeCodexAgentsGuard replaces AstrLink's marked section or appends it
// after the user's text.
func MergeCodexAgentsGuard(existing *string, block string) string {
	text := ""
	if existing != nil {
		text = *existing
	}
	if start, end, ok := codexGuardRange(text); ok {
		return text[:start] + block + text[end:]
	}
	prefix := strings.TrimRightFunc(text, isRustWhitespace)
	if prefix == "" {
		return block + "\n"
	}
	return prefix + "\n\n" + block + "\n"
}

// RemoveCodexAgentsGuard removes AstrLink's marked section and the blank
// line that separated it.
func RemoveCodexAgentsGuard(existing string) string {
	start, end, ok := codexGuardRange(existing)
	if !ok {
		return existing
	}
	prefix := strings.TrimRightFunc(existing[:start], isRustWhitespace)
	suffix := strings.TrimLeft(existing[end:], "\r\n")
	switch {
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix + "\n"
	}
	return prefix + "\n\n" + suffix
}

// isRustWhitespace is Rust's char::is_whitespace, which str::trim uses.
func isRustWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// writeText writes an agent file, keeping its permissions; none of them
// holds a secret.
func writeText(path, contents string) error {
	return WriteText(path, contents, WriteOptions{})
}

func readHostGuards(home string) (hostGuardRecord, error) {
	path := hostGuardsPath(home)
	raw, ok, err := ReadOptional(path)
	if err != nil {
		return hostGuardRecord{}, err
	}
	var record hostGuardRecord
	if !ok {
		return record, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return hostGuardRecord{}, fmt.Errorf("unable to read %s: %v", path, err)
	}
	if _, ok := fields["version"]; !ok {
		return hostGuardRecord{}, fmt.Errorf("unable to read %s: missing field `version`", path)
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return hostGuardRecord{}, fmt.Errorf("unable to read %s: %v", path, err)
	}
	return record, nil
}

func writeHostGuards(home string, record *hostGuardRecord) error {
	record.Version = hostGuardsVersion
	return writeJSONFile(hostGuardsPath(home), record)
}

// installHostGuards writes the record before each host file so a failed
// write can at most leave a record naming rules that were never added,
// which removal ignores.
func installHostGuards(context *InstallContext, home Home, toolIDs []AgentToolID) ([]string, error) {
	rulesPath := codexRulesPath(home)
	if slices.Contains(toolIDs, ToolCodex) {
		raw, ok, err := ReadOptional(rulesPath)
		if err != nil {
			return nil, err
		}
		if ok && !strings.HasPrefix(raw, codexRulesMarker) {
			return nil, fmt.Errorf("refusing to overwrite Codex rules at %s", rulesPath)
		}
	}
	record, err := readHostGuards(home.Dir())
	if err != nil {
		return nil, err
	}
	files := []string{}
	if slices.Contains(toolIDs, ToolClaude) {
		path := claudeSettingsPath(home)
		deny := ClaudeDenyRules(home, context.DataDirectories, context.RawKeyPins)
		existing, err := readOptionalText(path)
		if err != nil {
			return nil, err
		}
		next, denied, err := MergeClaudeSettingsRules(existing, "deny", deny, record.Claude)
		if err != nil {
			return nil, err
		}
		next, allowed, err := MergeClaudeSettingsRules(&next, "allow", ClaudeAllowRules(home), record.ClaudeAllow)
		if err != nil {
			return nil, err
		}
		record.Claude, record.ClaudeAllow = &denied, &allowed
		if err := writeHostGuards(home.Dir(), &record); err != nil {
			return nil, err
		}
		if err := writeText(path, next); err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	if slices.Contains(toolIDs, ToolCodex) {
		path := codexAgentsPath(home)
		existing, err := readOptionalText(path)
		if err != nil {
			return nil, err
		}
		createdFile := record.Codex != nil && record.Codex.CreatedFile || existing == nil
		record.Codex = &codexInstructionsRecord{CreatedFile: createdFile}
		if err := writeHostGuards(home.Dir(), &record); err != nil {
			return nil, err
		}
		block := codexGuardBlock(home, context.DataDirectories, context.RawKeyPins)
		if err := writeText(path, MergeCodexAgentsGuard(existing, block)); err != nil {
			return nil, err
		}
		files = append(files, path)
		record.CodexRules = true
		if err := writeHostGuards(home.Dir(), &record); err != nil {
			return nil, err
		}
		if err := writeText(rulesPath, codexRules(home)); err != nil {
			return nil, err
		}
		files = append(files, rulesPath)
	}
	if record.Claude != nil || record.Codex != nil {
		files = append(files, hostGuardsPath(home.Dir()))
	}
	return files, nil
}

func readOptionalText(path string) (*string, error) {
	raw, ok, err := ReadOptional(path)
	if err != nil || !ok {
		return nil, err
	}
	return &raw, nil
}

func uninstallHostGuards(home Home) error {
	record, err := readHostGuards(home.Dir())
	if err != nil {
		return err
	}
	path := claudeSettingsPath(home)
	raw, ok, err := ReadOptional(path)
	if err != nil {
		return err
	}
	if ok {
		changed, emptied := false, false
		// The allow rules were merged after the deny rules, so they come
		// out first.
		for _, entry := range []struct {
			list  string
			rules *ClaudeRuleRecord
		}{{"allow", record.ClaudeAllow}, {"deny", record.Claude}} {
			if entry.rules == nil {
				continue
			}
			changed = true
			next, err := RemoveClaudeSettingsRules(raw, entry.list, entry.rules)
			if err != nil {
				return err
			}
			if next == nil {
				emptied = true
				break
			}
			raw = *next
		}
		if emptied {
			if err := RemovePath(path); err != nil {
				return err
			}
		} else if changed {
			if err := writeText(path, raw); err != nil {
				return err
			}
		}
	}
	// The markers identify the section even if the record was lost.
	agentsPath := codexAgentsPath(home)
	raw, ok, err = ReadOptional(agentsPath)
	if err != nil {
		return err
	}
	if _, _, found := codexGuardRange(raw); ok && found {
		next := RemoveCodexAgentsGuard(raw)
		created := record.Codex != nil && record.Codex.CreatedFile
		if strings.TrimSpace(next) == "" && created {
			if err := RemovePath(agentsPath); err != nil {
				return err
			}
		} else if err := writeText(agentsPath, next); err != nil {
			return err
		}
	}
	// The marker identifies the rules file even if the record was lost.
	rulesPath := codexRulesPath(home)
	raw, ok, err = ReadOptional(rulesPath)
	if err != nil {
		return err
	}
	if ok && strings.HasPrefix(raw, codexRulesMarker) {
		if err := RemovePath(rulesPath); err != nil {
			return err
		}
	}
	return RemovePath(hostGuardsPath(home.Dir()))
}

// SyncInstalledHostGuards keeps installed guards and CLI access current
// when the data directories, pin files, CLI path, or rule set change, and
// adds CLI access to installs made before the CLI. A settings file,
// `AGENTS.md` section, or Codex rules file the user removed by hand is not
// re-created; reinstalling from Settings restores it. When the rules do
// change, every current rule missing from a kept settings file is added,
// including one the user deleted from it.
func SyncInstalledHostGuards(home Home, dataDirectories, rawKeyPins []string) error {
	defer LockHostFiles()()
	if !isFile(receiptPath(home.Dir())) || !isFile(hostGuardsPath(home.Dir())) {
		return nil
	}
	record, err := readHostGuards(home.Dir())
	if err != nil {
		return err
	}
	if record.Claude != nil {
		previous := record.Claude.clone()
		rules := ClaudeDenyRules(home, dataDirectories, rawKeyPins)
		path := claudeSettingsPath(home)
		if !slices.Equal(previous.Rules, rules) {
			existing, err := readOptionalText(path)
			if err != nil {
				return err
			}
			if existing != nil {
				next, applied, err := MergeClaudeSettingsRules(existing, "deny", rules, previous)
				if err != nil {
					return err
				}
				record.Claude = &applied
				if err := writeHostGuards(home.Dir(), &record); err != nil {
					return err
				}
				if err := writeText(path, next); err != nil {
					return err
				}
			}
		}
		rules = ClaudeAllowRules(home)
		previousAllow := record.ClaudeAllow.clone()
		if previousAllow == nil || !slices.Equal(previousAllow.Rules, rules) {
			existing, err := readOptionalText(path)
			if err != nil {
				return err
			}
			if existing != nil {
				next, applied, err := MergeClaudeSettingsRules(existing, "allow", rules, previousAllow)
				if err != nil {
					return err
				}
				record.ClaudeAllow = &applied
				if err := writeHostGuards(home.Dir(), &record); err != nil {
					return err
				}
				if err := writeText(path, next); err != nil {
					return err
				}
			}
		}
	}
	if record.Codex != nil {
		path := codexAgentsPath(home)
		existing, ok, err := ReadOptional(path)
		if err != nil {
			return err
		}
		if start, end, found := codexGuardRange(existing); ok && found {
			block := codexGuardBlock(home, dataDirectories, rawKeyPins)
			if existing[start:end] != block {
				if err := writeText(path, MergeCodexAgentsGuard(&existing, block)); err != nil {
					return err
				}
			}
		}
		rulesPath := codexRulesPath(home)
		desired := codexRules(home)
		current, ok, err := ReadOptional(rulesPath)
		if err != nil {
			return err
		}
		switch {
		case !ok && !record.CodexRules:
			record.CodexRules = true
			if err := writeHostGuards(home.Dir(), &record); err != nil {
				return err
			}
			if err := writeText(rulesPath, desired); err != nil {
				return err
			}
		case ok && strings.HasPrefix(current, codexRulesMarker) && current != desired:
			if err := writeText(rulesPath, desired); err != nil {
				return err
			}
		}
	}
	return nil
}

func prettyJSON(value any) string { return format.EncodePretty(value) + "\n" }

// writeJSONFile writes value as serde_json's pretty printer does: struct
// fields in their order, two spaces, a final newline.
func writeJSONFile(path string, value any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("unable to encode %s: %v", path, err)
	}
	return writeTextFile(path, buffer.String())
}

func writeTextFile(path, contents string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return fmt.Errorf("unable to create %s: %v", filepath.Dir(path), ioError(err))
	}
	if err := os.WriteFile(path, []byte(contents), 0o666); err != nil {
		return fmt.Errorf("unable to write %s: %v", path, ioError(err))
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func unixNow() uint64 {
	now := time.Now().Unix()
	if now < 0 {
		return 0
	}
	return uint64(now)
}
