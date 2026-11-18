// Package clientupdates checks and updates the agent clients the user has
// installed: Codex, Claude Code, Cursor's agent and Pi, on this computer
// and in running WSL distributions. Every update goes through the method
// that installed the client (its own updater, npm or Homebrew) and is
// checked afterwards; a client of unknown origin is only reported.
package clientupdates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
)

const (
	probeTimeout  = 8 * time.Second
	updateTimeout = 10 * time.Minute
	// startTimeout covers starting a stopped distribution, which may boot
	// the WSL VM.
	startTimeout     = 60 * time.Second
	cursorLatestURL  = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCliDownloadUrl"
	piLatestURL      = "https://pi.dev/api/latest-version"
	npmCacheLocation = ".cache/astrlink/npm"
)

// ClientID names a client.
type ClientID string

const (
	ClientCodex  ClientID = "codex"
	ClientClaude ClientID = "claude"
	ClientCursor ClientID = "cursor"
	ClientPi     ClientID = "pi"
)

var allClients = []ClientID{ClientCodex, ClientClaude, ClientCursor, ClientPi}

// UnmarshalJSON accepts only the known clients.
func (id *ClientID) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if !slices.Contains(allClients, ClientID(name)) {
		return fmt.Errorf("unknown client %q", name)
	}
	*id = ClientID(name)
	return nil
}

// name is the client's command.
func (id ClientID) name() string {
	if id == ClientCursor {
		return "cursor-agent"
	}
	return string(id)
}

// packages are the npm packages that provide the command, current name
// first.
func (id ClientID) packages() []string {
	switch id {
	case ClientCodex:
		return []string{"@openai/codex"}
	case ClientClaude:
		return []string{"@anthropic-ai/claude-code"}
	case ClientPi:
		return []string{"@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"}
	}
	return nil
}

func (id ClientID) brewPackages() []string {
	switch id {
	case ClientCodex:
		return []string{"codex"}
	case ClientClaude:
		return []string{"claude-code", "claude-code@latest"}
	case ClientCursor:
		return []string{"cursor-cli"}
	}
	return []string{"pi-coding-agent"}
}

type methodKind int

const (
	methodUnknown methodKind = iota
	methodNative
	methodNpm
	// methodPackage is a Bun, pnpm, or Yarn global installation.
	methodPackage
	methodBrew
)

// installMethod is how a client was installed.
type installMethod struct {
	kind methodKind
	// prefix is npm's.
	prefix string
	// manager is the package manager's name.
	manager string
	// root, pkg and cask are Homebrew's.
	root string
	pkg  string
	cask bool
}

var (
	native  = installMethod{kind: methodNative}
	unknown = installMethod{kind: methodUnknown}
)

func npmAt(prefix string) installMethod { return installMethod{kind: methodNpm, prefix: prefix} }

func packageManager(manager string) installMethod {
	return installMethod{kind: methodPackage, manager: manager}
}

func brew(root, pkg string, cask bool) installMethod {
	return installMethod{kind: methodBrew, root: root, pkg: pkg, cask: cask}
}

func (m installMethod) label() string {
	switch m.kind {
	case methodNative:
		return "native"
	case methodNpm:
		return "npm"
	case methodPackage:
		return m.manager
	case methodBrew:
		return "homebrew"
	}
	return "unknown"
}

type installation struct {
	executable string
	resolved   string
	method     installMethod
	others     []string
}

// program is a program on PATH and the file it resolves to.
type program struct {
	path     string
	resolved string
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

// canonical resolves path's links, as a full path.
func canonical(path string) (string, bool) {
	resolved, err := realpath.Resolve(path)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	return resolved, err == nil
}

// windows reports whether clients here use Windows layouts and npm's .cmd
// shims.
func (env *Environment) windows() bool { return env.wsl == nil && runtime.GOOS == "windows" }

func (env *Environment) distribution() *string {
	if env.wsl == nil {
		return nil
	}
	name := env.wsl.distribution
	return &name
}

// programs are the programs named name on PATH, in order, each target
// once.
func (env *Environment) programs(name string) []program {
	if env.wsl != nil {
		return wslPrograms(env, env.wsl, name)
	}
	return findPrograms(env.path, name)
}

// program is path and the file it resolves to, if it is an executable
// file.
func (env *Environment) program(path string) (program, bool) {
	if env.wsl != nil {
		return wslProgram(env, env.wsl, path)
	}
	if !executable(path) {
		return program{}, false
	}
	resolved, ok := canonical(path)
	return program{path: path, resolved: resolved}, ok
}

func (env *Environment) read(path string) (string, bool) {
	if env.wsl != nil {
		var ok bool
		if path, ok = env.wsl.shared(path); !ok {
			return "", false
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return "", false
	}
	// Without the BOM Windows PowerShell 5.1 writes, which JSON parsers
	// reject.
	return strings.TrimPrefix(string(data), "\uFEFF"), true
}

func (env *Environment) executable(path string) bool {
	if env.wsl != nil {
		return wslIsFile(env, path, true)
	}
	return executable(path)
}

func (env *Environment) isFile(path string) bool {
	if env.wsl != nil {
		return wslIsFile(env, path, false)
	}
	return isFile(path)
}

// pathArg is a path argument as the client's system spells it.
func (env *Environment) pathArg(path string) string {
	if env.wsl != nil {
		return linux(path)
	}
	return path
}

func findPrograms(path, name string) []program {
	var found []program
	seen := map[string]bool{}
	names := []string{name}
	if runtime.GOOS == "windows" {
		// npm also writes an extensionless sh shim beside each .cmd.
		// Windows cannot launch it, and it is not a second installation.
		names = []string{name + ".exe", name + ".cmd"}
	}
	for _, directory := range filepath.SplitList(path) {
		if !filepath.IsAbs(directory) {
			continue
		}
		for _, name := range names {
			file := filepath.Join(directory, name)
			if !executable(file) {
				continue
			}
			if resolved, ok := canonical(file); ok && !seen[resolved] {
				seen[resolved] = true
				found = append(found, program{path: file, resolved: resolved})
			}
		}
	}
	return found
}

// components splits a path into its parts, the root dropped. Both
// separators split it on Windows, as Rust's Path does.
func components(path string) []string {
	path = filepath.ToSlash(path[len(filepath.VolumeName(path)):])
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

func rooted(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path[len(filepath.VolumeName(path)):]), "/")
}

func sameVolume(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

// endsWith compares path's last components with suffix's.
func endsWith(path, suffix string) bool {
	parts, tail := components(path), components(suffix)
	return len(tail) <= len(parts) && slices.Equal(parts[len(parts)-len(tail):], tail)
}

// startsWith compares path's first components with base's.
func startsWith(path, base string) bool {
	parts, head := components(path), components(base)
	return sameVolume(path, base) && rooted(path) == rooted(base) &&
		len(head) <= len(parts) && slices.Equal(parts[:len(head)], head)
}

func samePath(a, b string) bool {
	return sameVolume(a, b) && rooted(a) == rooted(b) && slices.Equal(components(a), components(b))
}

// parent is path's directory; false at the root.
func parent(path string) (string, bool) {
	directory := filepath.Dir(path)
	return directory, directory != path && path != "."
}

// ancestors are path and each directory above it.
func ancestors(path string) []string {
	all := []string{path}
	for {
		directory, ok := parent(path)
		if !ok {
			return all
		}
		all = append(all, directory)
		path = directory
	}
}

// extension and stem split a file name as Rust's Path does: a leading dot
// starts no extension.
func extension(path string) string {
	name := filepath.Base(path)
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[i+1:]
	}
	return ""
}

func stem(path string) string {
	name := filepath.Base(path)
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// cmdShimTarget reads what an npm Windows shim runs: `"%dp0%\<path>"`
// (`"%~dp0\<path>"` before npm 7), after the script's interpreter. `%~dp0`
// already ends in a backslash, so Pi's installer writes
// `"%~dp0pi-launcher.js"`. Reading it beats guessing each package's entry
// point; package ownership is checked later.
func cmdShimTarget(shim string) (string, bool) {
	data, err := os.ReadFile(shim)
	if err != nil || !utf8.Valid(data) {
		return "", false
	}
	raw := string(data)
	var rest string
	if i := strings.LastIndex(raw, `"%dp0%`); i >= 0 {
		rest = raw[i+len(`"%dp0%`):]
	} else if i := strings.LastIndex(raw, `"%~dp0`); i >= 0 {
		rest = raw[i+len(`"%~dp0`):]
	} else {
		return "", false
	}
	target, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return "", false
	}
	directory, ok := parent(shim)
	if !ok {
		return "", false
	}
	// Global shims point into their own directory. A project's
	// node_modules\.bin shim climbs out of it and is not a global install.
	return shimJoin(directory, target)
}

// shimJoin is base joined with a path a shim wrote after it, if that names
// something inside base.
func shimJoin(base, relative string) (string, bool) {
	parts := strings.FieldsFunc(relative, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) == 0 {
		return "", false
	}
	path := base
	for _, part := range parts {
		if part == ".." || strings.Contains(part, ":") {
			return "", false
		}
		path = filepath.Join(path, part)
	}
	return path, true
}

// shimNode is the node.exe a shim runs if it exists: npm's
// `"%dp0%\node.exe"` beside it, or the standalone Node.js that Pi's
// installer names under `%LOCALAPPDATA%`. A new terminal's PATH lists the
// system's Node.js before that one.
func shimNode(env *Environment, shim string, variable func(string) (string, bool)) (program, bool) {
	data, err := os.ReadFile(shim)
	if err != nil || !utf8.Valid(data) {
		return program{}, false
	}
	directory, ok := parent(shim)
	if !ok {
		return program{}, false
	}
	for _, rest := range strings.Split(string(data), `EXIST "`)[1:] {
		written, _, ok := strings.Cut(rest, `"`)
		if !ok {
			continue
		}
		base, relative := directory, ""
		if after, found := strings.CutPrefix(written, "%~dp0"); found {
			relative = after
		} else {
			after, found := strings.CutPrefix(written, "%")
			if !found {
				continue
			}
			name, tail, found := strings.Cut(after, "%")
			if !found {
				continue
			}
			relative = tail
			if name != "dp0" {
				value, ok := variable(name)
				if !ok {
					continue
				}
				base = value
			}
		}
		if !filepath.IsAbs(base) {
			continue
		}
		node, ok := shimJoin(base, relative)
		if !ok || !strings.EqualFold(filepath.Base(node), "node.exe") {
			continue
		}
		if found, ok := env.program(node); ok {
			return found, true
		}
	}
	return program{}, false
}

// readJSON reads a JSON file as serde_json does, nil when it is missing
// or invalid.
func (env *Environment) readJSON(path string) map[string]any {
	raw, ok := env.read(path)
	if !ok {
		return nil
	}
	value, err := format.DecodeJSON(raw)
	if err != nil {
		return nil
	}
	object, _ := value.(map[string]any)
	return object
}

func (env *Environment) manifestName(directory string) (string, bool) {
	name, ok := env.readJSON(filepath.Join(directory, "package.json"))["name"].(string)
	return name, ok
}

func installationMethod(env *Environment, id ClientID, resolved string) installMethod {
	// Check package-manager ownership before native layouts. Never replace
	// a Homebrew or npm install with a second, unrelated installation.
	// Homebrew comes first because its formulae may wrap an npm layout.
	for _, ancestor := range ancestors(resolved) {
		base := filepath.Base(ancestor)
		if base != "Caskroom" && base != "Cellar" {
			continue
		}
		root, ok := parent(ancestor)
		if !ok {
			continue
		}
		parts, above := components(resolved), components(ancestor)
		if len(parts) <= len(above) {
			continue
		}
		if pkg := parts[len(above)]; slices.Contains(id.brewPackages(), pkg) {
			return brew(root, pkg, base == "Caskroom")
		}
	}
	for _, ancestor := range ancestors(resolved) {
		pkg := ""
		for _, candidate := range id.packages() {
			if endsWith(ancestor, "node_modules/"+candidate) {
				pkg = candidate
				break
			}
		}
		if pkg == "" {
			continue
		}
		if name, ok := env.manifestName(ancestor); !ok || name != pkg {
			continue
		}
		// pnpm, Bun and Yarn stores are not npm global installations. Only
		// Pi's own updater knows how to update them.
		manager := ""
		for _, part := range components(resolved) {
			switch part {
			case ".pnpm", "pnpm":
				manager = "pnpm"
			case ".bun":
				manager = "bun"
			case "yarn", ".yarn":
				manager = "yarn"
			}
			if manager != "" {
				break
			}
		}
		if manager != "" {
			if id == ClientPi {
				return packageManager(manager)
			}
			return unknown
		}
		prefix, ok := parent(ancestor)
		if ok {
			prefix, ok = parent(prefix)
		}
		if ok {
			prefix, ok = parent(prefix)
		}
		if ok {
			lib := endsWith(prefix, "lib")
			if !env.windows() && !lib {
				return unknown
			}
			if lib {
				if above, ok := parent(prefix); ok {
					prefix = above
				}
			}
			return npmAt(prefix)
		}
	}
	home := env.home
	switch id {
	case ClientClaude:
		if startsWith(resolved, filepath.Join(home, ".local/share/claude/versions")) {
			return native
		}
	case ClientCursor:
		if startsWith(resolved, filepath.Join(home, ".local/share/cursor-agent/versions")) {
			return native
		}
	case ClientCodex:
		// A standalone package names its binary: bin/codex, or
		// bin/codex.exe on Windows.
		above := ancestors(resolved)[1:]
		for _, directory := range above[:min(3, len(above))] {
			value := env.readJSON(filepath.Join(directory, "codex-package.json"))
			entry, ok := value["entrypoint"].(string)
			if value["variant"] == "codex" && value["layoutVersion"] == format.Number("1") && ok &&
				samePath(filepath.Join(directory, entry), resolved) {
				return native
			}
		}
	case ClientPi:
		// The managed installer links <agent>/bin/pi, a launcher that runs
		// the release selected under <agent>/install.
		bin, ok := parent(resolved)
		if ok {
			var agent string
			if agent, ok = parent(bin); ok {
				marker := env.readJSON(filepath.Join(agent, "install", "managed-install.json"))
				if marker["kind"] == "pi-managed-install" && marker["schemaVersion"] == format.Number("1") &&
					marker["layout"] == "releases-v1" {
					return native
				}
			}
		}
	}
	return unknown
}

func detect(env *Environment, id ClientID) *installation {
	programs := env.programs(id.name())
	if len(programs) == 0 {
		return nil
	}
	first := programs[0]
	resolved := first.resolved
	if env.windows() && extension(first.path) == "cmd" {
		if target, ok := cmdShimTarget(first.path); ok && isFile(target) {
			if target, ok = canonical(target); ok {
				resolved = target
			}
		}
	}
	method := installationMethod(env, id, resolved)
	// `pi` is a generic command name. Only report a launcher or package
	// that is verifiably Pi; standalone binaries ship package.json beside
	// themselves.
	if id == ClientPi && method == unknown {
		above := ancestors(resolved)[1:]
		verified := false
		for _, directory := range above[:min(4, len(above))] {
			if name, ok := env.manifestName(directory); ok && slices.Contains(id.packages(), name) {
				verified = true
				break
			}
		}
		if !verified {
			return nil
		}
	}
	others := []string{}
	for _, other := range programs[1:] {
		others = append(others, other.path)
	}
	return &installation{executable: first.path, resolved: resolved, method: method, others: others}
}

func currentVersion(env *Environment, id ClientID, install *installation) (clientVersion, *clientError) {
	return versionWithin(env, id, install, probeTimeout)
}

func versionWithin(env *Environment, id ClientID, install *installation, deadline time.Duration) (clientVersion, *clientError) {
	spec, failure := clientCommand(env, install)
	if failure != nil {
		return clientVersion{}, failure
	}
	spec.args = append(spec.args, "--version")
	output, failure := run(env, spec, deadline)
	if failure != nil {
		return clientVersion{}, failure
	}
	return parseVersion(id, output)
}

// nodeProgram is Node.js beside an npm prefix, else the first on PATH.
// Bun also answers to `node` through a link of that name; it is used only
// when Node.js is absent.
func nodeProgram(env *Environment, prefix string) (program, bool) {
	sibling := filepath.Join(prefix, "bin", "node")
	if env.windows() {
		sibling = filepath.Join(prefix, "node.exe")
	}
	var all []program
	if found, ok := env.program(sibling); ok {
		all = append(all, found)
	}
	all = append(all, env.programs("node")...)
	slices.SortStableFunc(all, func(a, b program) int {
		switch x, y := isBun(a.resolved), isBun(b.resolved); {
		case !x && y:
			return -1
		case x && !y:
			return 1
		}
		return 0
	})
	if len(all) == 0 {
		return program{}, false
	}
	return all[0], true
}

func isBun(path string) bool { return strings.EqualFold(stem(path), "bun") }

func script(path string) bool {
	switch strings.ToLower(extension(path)) {
	case "js", "cjs", "mjs":
		return true
	}
	return false
}

// piNpmCommand runs Pi's entry with Node.js: Pi under Bun takes itself
// for a Bun install and refuses to update an npm one, and its launcher's
// `env node` may find Bun.
func piNpmCommand(env *Environment, prefix string, install *installation) (commandSpec, *clientError) {
	if !script(install.resolved) {
		return clientCommand(env, install)
	}
	node, ok := nodeProgram(env, prefix)
	if !ok || isBun(node.resolved) {
		return commandSpec{}, newError("unsupported", "Node.js is unavailable")
	}
	return command(node.path, env.pathArg(install.resolved)), nil
}

func clientCommand(env *Environment, install *installation) (commandSpec, *clientError) {
	if env.windows() && extension(install.executable) == "cmd" {
		return shimCommand(env, install.executable, install.resolved)
	}
	return command(install.executable), nil
}

// shimCommand runs a .cmd shim's target as the shim would, but with a
// literal argument vector instead of a command line that cmd.exe parses
// again.
func shimCommand(env *Environment, shim, target string) (commandSpec, *clientError) {
	switch strings.ToLower(extension(target)) {
	case "exe":
		return command(target), nil
	case "js", "cjs", "mjs":
		// Like the shim, prefer the node.exe it names, then PATH.
		node, ok := shimNode(env, shim, os.LookupEnv)
		if !ok {
			var prefix string
			if prefix, ok = parent(shim); ok {
				node, ok = nodeProgram(env, prefix)
			}
		}
		if !ok {
			return commandSpec{}, newError("unsupported", "Node.js is unavailable")
		}
		return command(node.path, target), nil
	}
	return commandSpec{}, newError("unsupported", "Unsupported command wrapper")
}

// cursorChannel is the release channel Cursor's `update` reads from
// cli-config.json.
func cursorChannel(env *Environment) string {
	channel, ok := env.readJSON(filepath.Join(env.home, ".cursor", "cli-config.json"))["channel"].(string)
	if !ok || channel == "prod-stable-internal" {
		return "prod"
	}
	return channel
}

func updateCommand(env *Environment, id ClientID, install *installation, latest clientVersion) (commandSpec, *clientError) {
	method := install.method
	switch {
	// Pi's updater handles its npm, Bun, pnpm, Yarn, and managed layouts,
	// including the move to the renamed package.
	case id == ClientPi && (method.kind == methodNative || method.kind == methodNpm || method.kind == methodPackage):
		var spec commandSpec
		var failure *clientError
		if method.kind == methodNpm {
			spec, failure = piNpmCommand(env, method.prefix, install)
		} else {
			spec, failure = clientCommand(env, install)
		}
		if failure != nil {
			return commandSpec{}, failure
		}
		spec.args = append(spec.args, "update", "--self")
		return spec, nil
	case method.kind == methodNative:
		if id == ClientCursor && cursorChannel(env) == "static" {
			return commandSpec{}, newError("unsupported", "Updates are disabled for the static channel")
		}
		spec, failure := clientCommand(env, install)
		if failure != nil {
			return commandSpec{}, failure
		}
		spec.args = append(spec.args, "update")
		return spec, nil
	case method.kind == methodBrew:
		brewProgram := filepath.Join(method.root, "bin", "brew")
		if !env.executable(brewProgram) {
			return commandSpec{}, newError("unsupported", "Homebrew is unavailable")
		}
		kind := "--formula"
		if method.cask {
			kind = "--cask"
		}
		return command(brewProgram, "upgrade", kind, method.pkg), nil
	case method.kind == methodNpm:
		return npmCommand(env, id, method.prefix, method.prefix, latest)
	}
	return commandSpec{}, newError("unsupported", "Use the original installer or package manager")
}

// npmCommand is npm's command line that installs latest globally into
// target, using the npm and Node.js that serve the installation in prefix.
func npmCommand(env *Environment, id ClientID, prefix, target string, latest clientVersion) (commandSpec, *clientError) {
	npm := filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if env.windows() {
		npm = filepath.Join(prefix, "node_modules", "npm", "bin", "npm-cli.js")
	}
	if !env.isFile(npm) {
		npm = ""
		for _, found := range env.programs("npm") {
			candidate := found.resolved
			if extension(found.path) == "cmd" {
				// Node's Windows npm.cmd runs the npm-cli.js beside it.
				directory, ok := parent(found.path)
				if !ok {
					continue
				}
				candidate = filepath.Join(directory, "node_modules", "npm", "bin", "npm-cli.js")
			}
			if endsWith(candidate, "npm/bin/npm-cli.js") && env.isFile(candidate) {
				npm = candidate
				break
			}
		}
		if npm == "" {
			return commandSpec{}, newError("unsupported", "npm is unavailable")
		}
	}
	node, ok := nodeProgram(env, prefix)
	if !ok {
		return commandSpec{}, newError("unsupported", "Node.js is unavailable")
	}
	pkg := id.packages()[0]
	args := []string{env.pathArg(npm), "install", "--global", "--prefix", env.pathArg(target)}
	if target != prefix {
		// npm reads the global npmrc from <prefix>/etc/npmrc. The staging
		// prefix has none, so keep the installation's own settings.
		args = append(args, "--globalconfig", env.pathArg(filepath.Join(prefix, "etc", "npmrc")))
	}
	// Earlier sudo installs can leave ~/.npm owned by root. Keep update
	// downloads in this user's separate cache, using a Linux path for a
	// client inside WSL.
	args = append(args, "--cache", env.pathArg(filepath.Join(env.home, npmCacheLocation)))
	if id == ClientPi {
		// As `pi update --self` installs Pi with npm.
		args = append(args, "--ignore-scripts", "--min-release-age=0")
	} else {
		// npm 12 skips install scripts missing from allowScripts and still
		// exits 0. Claude Code's postinstall replaces the bin/claude.exe
		// stub with its native binary.
		args = append(args, "--allow-scripts="+pkg)
	}
	args = append(args, pkg+"@"+latest.String())
	return command(node.path, args...), nil
}

// usualDirectories are where clients install that a PATH may miss: a GUI
// app's on macOS, or one a login shell could not report.
func usualDirectories(home string) []string {
	return []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".volta", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
}

// loadEnvironment is this computer's environment for client checks: the
// PATH a terminal gets, and with the system proxy on macOS, its settings
// for command-line installers.
func loadEnvironment(home string, useSystemProxy bool) (*Environment, *clientError) {
	env := &Environment{home: home, path: os.Getenv("PATH"), useSystemProxy: useSystemProxy}
	if runtime.GOOS == "windows" {
		// A Windows app keeps the PATH it started with; installers save
		// theirs for later processes. Read the saved one, as a new
		// terminal does.
		if path, ok := savedPath(); ok {
			env.path = path
		}
	} else {
		// GUI apps on macOS do not inherit a terminal's PATH
		// (nvm/mise/asdf). This fixed script only prints PATH; no
		// frontend data becomes shell code.
		shell := os.Getenv("SHELL")
		if !filepath.IsAbs(shell) || !executable(shell) {
			shell = "/bin/sh"
		}
		if output, failure := run(env, command(shell, "-ilc", `printf '\n__ASTRLINK_PATH__%s\n' "$PATH"`), 4*time.Second); failure == nil {
			for _, line := range lines(output) {
				if value, ok := strings.CutPrefix(line, "__ASTRLINK_PATH__"); ok {
					env.path = value
					break
				}
			}
		}
	}
	var paths []string
	for _, path := range filepath.SplitList(env.path) {
		if filepath.IsAbs(path) {
			paths = append(paths, path)
		}
	}
	for _, path := range usualDirectories(home) {
		if filepath.IsAbs(path) && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "npm"))
		}
		// The Codex standalone installer's default command folder.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, filepath.Join(local, "Programs", "OpenAI", "Codex", "bin"))
		}
	}
	if joinable(paths) {
		env.path = strings.Join(paths, string(os.PathListSeparator))
	}
	if runtime.GOOS == "darwin" && useSystemProxy {
		output, failure := run(env, command("/usr/sbin/scutil", "--proxy"), 3*time.Second)
		if failure != nil {
			return nil, newError("proxy", failure.detail)
		}
		if env.proxyEnv, failure = macEnvironment(output); failure != nil {
			return nil, failure
		}
	}
	return env, nil
}

// joinable reports whether paths make a PATH that splits back into them.
func joinable(paths []string) bool {
	for _, path := range paths {
		if strings.ContainsRune(path, os.PathListSeparator) || runtime.GOOS == "windows" && strings.Contains(path, `"`) {
			return false
		}
	}
	return true
}
