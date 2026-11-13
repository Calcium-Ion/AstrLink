package pluginbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// Bounds of what a build produces.
const (
	// MaxPluginBytes is the plugin host's bound on a plugin's source.
	MaxPluginBytes = 8 << 20
	// MaxClientBytes bounds an interface's code.
	MaxClientBytes = 4 << 20
	// MaxFilesBytes and MaxFiles bound the files a plugin keeps beside its
	// code.
	MaxFilesBytes = 32 << 20
	MaxFiles      = 1000
)

// reservedFiles are the names of the install directory that are the
// host's, which a plugin's own files may not take.
var reservedFiles = []string{"plugin.js", "plugin.js.map", "plugin.ts", "client.js", "client.js.map", "icon.svg", "icon.png", "install.json", ".astrlink-dev.json"}

// Result is a project built: the files the gateway loads, and what the
// build read.
type Result struct {
	Manifest *Manifest
	Entries  Entries
	// Plugin is plugin.js, an ES module, and SourceMap its source map.
	Plugin    []byte
	SourceMap []byte
	// Client is client.js, nil for a project without an interface, and
	// ClientMap its source map.
	Client    []byte
	ClientMap []byte
	Warnings  []Message
	// Inputs are the project's files the build read, relative to its root
	// and with forward slashes; dependencies under node_modules left out.
	Inputs []string
	// Files are the files the plugin keeps beside its code, by their path
	// in the install directory, with forward slashes.
	Files map[string][]byte
}

// Outputs are the files a build puts in a plugin's install directory, by
// their path there: plugin.js, the interface, their source maps and the
// plugin's own files.
func (result *Result) Outputs() map[string][]byte {
	outputs := map[string][]byte{"plugin.js": result.Plugin}
	for name, data := range map[string][]byte{"plugin.js.map": result.SourceMap, "client.js": result.Client, "client.js.map": result.ClientMap} {
		if data != nil {
			outputs[name] = data
		}
	}
	for name, data := range result.Files {
		outputs[name] = data
	}
	return outputs
}

// Write writes the outputs into dir and returns the sha256 of each.
func (result *Result) Write(dir string) (map[string]string, error) {
	sums := map[string]string{}
	for name, data := range result.Outputs() {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		sums[name] = hex.EncodeToString(sum[:])
	}
	return sums, nil
}

// Message is a problem the build found, at a position of a project file
// when it has one.
type Message struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	Text   string `json:"text"`
}

func (message Message) String() string {
	if message.File == "" {
		return message.Text
	}
	if message.Line == 0 {
		return message.File + ": " + message.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", message.File, message.Line, message.Column, message.Text)
}

// Error is a build that failed, with what the bundler reported.
type Error struct {
	Messages []Message
}

func (failure *Error) Error() string {
	if len(failure.Messages) == 0 {
		return "the build failed"
	}
	texts := make([]string, 0, len(failure.Messages))
	for _, message := range failure.Messages {
		texts = append(texts, message.String())
	}
	return strings.Join(texts, "\n")
}

// hostModules are what the desktop hands an interface instead of bundling
// them: one React and one component library for the whole window.
var hostModules = []string{
	"react",
	"react/jsx-runtime",
	"react-dom",
	"@astrlink/plugin/client",
}

// clientGlobal is the parameter the desktop passes an interface's code,
// which the host modules read from.
const clientGlobal = "__astrlinkClient"

// Packages supplies a project's dependencies to a build without
// node_modules: Core's installer fetches each package a lock file pins as
// the build first imports it.
type Packages interface {
	// Locate returns the directory whose node_modules holds the package
	// name as a file of importer's package sees it: importer is a file of
	// the project, or of a package Locate placed. ok is false when the
	// project does not depend on name; err says why the package cannot be
	// had.
	Locate(importer, name string) (dir string, ok bool, err error)
}

// Options adjust a build.
type Options struct {
	// Packages, when set, supplies the dependencies the project's
	// node_modules would otherwise hold.
	Packages Packages
}

// Build builds the project in dir. Its dependencies must already be in
// dir/node_modules: the build reads nothing from the network.
func Build(dir string) (*Result, error) {
	return BuildWith(dir, Options{})
}

// BuildWith builds the project in dir as options say.
func BuildWith(dir string, options Options) (*Result, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	manifest, err := ReadManifest(dir)
	if err != nil {
		return nil, &Error{Messages: []Message{{File: "package.json", Text: err.Error()}}}
	}
	entries, err := ResolveEntries(dir, manifest)
	if err != nil {
		return nil, &Error{Messages: []Message{{Text: err.Error()}}}
	}
	result := &Result{Manifest: manifest, Entries: entries}
	inputs := map[string]bool{}
	plugin, pluginMap, warnings, err := bundle(dir, entries.Plugin, "plugin.js", false, inputs, options)
	if err != nil {
		return nil, err
	}
	if len(plugin) > MaxPluginBytes {
		return nil, &Error{Messages: []Message{{Text: fmt.Sprintf("the built plugin is larger than %d MiB", MaxPluginBytes>>20)}}}
	}
	result.Plugin, result.SourceMap = plugin, pluginMap
	result.Warnings = append(result.Warnings, warnings...)
	if entries.Client != "" {
		client, clientMap, warnings, err := bundle(dir, entries.Client, "client.js", true, inputs, options)
		if err != nil {
			return nil, err
		}
		if len(client) > MaxClientBytes {
			return nil, &Error{Messages: []Message{{Text: fmt.Sprintf("the built interface is larger than %d MiB", MaxClientBytes>>20)}}}
		}
		result.Client, result.ClientMap = client, clientMap
		result.Warnings = append(result.Warnings, warnings...)
	}
	if result.Files, err = readFiles(dir, entries.Files, inputs); err != nil {
		return nil, &Error{Messages: []Message{{Text: err.Error()}}}
	}
	for input := range inputs {
		result.Inputs = append(result.Inputs, input)
	}
	slices.Sort(result.Inputs)
	return result, nil
}

// readFiles reads the files and the files under the directories that names
// lists, relative to the project in dir, noting each in inputs.
func readFiles(dir string, names []string, inputs map[string]bool) (map[string][]byte, error) {
	if len(names) == 0 {
		return nil, nil
	}
	files := map[string][]byte{}
	total := 0
	add := func(rel string) error {
		name := filepath.ToSlash(rel)
		if _, taken := files[name]; taken {
			return nil
		}
		if slices.Contains(reservedFiles, name) {
			return fmt.Errorf("the file %q has the name of a file the host writes", name)
		}
		if len(files) == MaxFiles {
			return fmt.Errorf("a plugin keeps at most %d files beside its code", MaxFiles)
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		if total += len(data); total > MaxFilesBytes {
			return fmt.Errorf("the files a plugin keeps beside its code hold at most %d MiB", MaxFilesBytes>>20)
		}
		files[name] = data
		inputs[name] = true
		return nil
	}
	for _, name := range names {
		rel, err := insideProject(dir, name)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(filepath.Join(dir, rel), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			inner, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			return add(inner)
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// bundle builds one entry into an output named out and its source map.
func bundle(dir, entry, out string, client bool, inputs map[string]bool, with Options) ([]byte, []byte, []Message, error) {
	options := api.BuildOptions{
		EntryPoints:    []string{filepath.Join(dir, filepath.FromSlash(entry))},
		AbsWorkingDir:  dir,
		Outfile:        filepath.Join(dir, out),
		Bundle:         true,
		Write:          false,
		Platform:       api.PlatformBrowser,
		Target:         api.ES2022,
		Charset:        api.CharsetUTF8,
		Sourcemap:      api.SourceMapExternal,
		SourcesContent: api.SourcesContentExclude,
		Metafile:       true,
		LogLevel:       api.LogLevelSilent,
		Define: map[string]string{
			"process.env.NODE_ENV": `"production"`,
			"global":               "globalThis",
		},
		Plugins: []api.Plugin{trackInputs(dir, inputs)},
	}
	if client {
		options.Format = api.FormatIIFE
		options.JSX = api.JSXAutomatic
		options.JSXImportSource = "react"
		options.Plugins = append(options.Plugins, hostModulePlugin())
	} else {
		options.Format = api.FormatESModule
	}
	// The host's modules resolve first: an interface never bundles its own
	// React, and a plugin leaves Node's modules to the host, as Node
	// prefers them to packages of the same name.
	if !client {
		options.Plugins = append(options.Plugins, nodeModulesPlugin(dir))
	}
	if with.Packages != nil {
		options.Plugins = append(options.Plugins, packagesPlugin(with.Packages))
	}
	built := api.Build(options)
	warnings := messages(dir, built.Warnings)
	if len(built.Errors) > 0 {
		return nil, nil, warnings, &Error{Messages: messages(dir, built.Errors)}
	}
	var code, sourceMap []byte
	for _, file := range built.OutputFiles {
		switch filepath.Base(file.Path) {
		case out:
			code = file.Contents
		case out + ".map":
			sourceMap = file.Contents
		}
	}
	if code == nil {
		return nil, nil, warnings, &Error{Messages: []Message{{Text: "the bundler produced no " + out}}}
	}
	return code, sourceMap, warnings, nil
}

// trackInputs records the project files the build reads, for the watch of
// plugin dev.
func trackInputs(dir string, inputs map[string]bool) api.Plugin {
	return api.Plugin{Name: "astrlink-inputs", Setup: func(build api.PluginBuild) {
		build.OnEnd(func(result *api.BuildResult) (api.OnEndResult, error) {
			for _, input := range metafileInputs(result.Metafile) {
				if strings.HasPrefix(input, "node_modules/") || strings.HasPrefix(input, "../") || strings.Contains(input, ":") {
					continue
				}
				inputs[input] = true
			}
			return api.OnEndResult{}, nil
		})
	}}
}

// hostModulePlugin hands an interface the desktop's own React, component
// library and SDK instead of bundling copies.
func hostModulePlugin() api.Plugin {
	pattern := "^(" + strings.Join(quoteAll(hostModules), "|") + ")$"
	return api.Plugin{Name: "astrlink-host-modules", Setup: func(build api.PluginBuild) {
		build.OnResolve(api.OnResolveOptions{Filter: pattern}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			return api.OnResolveResult{Path: args.Path, Namespace: "astrlink-host"}, nil
		})
		build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "astrlink-host"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
			contents := fmt.Sprintf("module.exports = %s.module(%q);", clientGlobal, args.Path)
			return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
		})
	}}
}

// nodeModulesPlugin leaves the modules of Node to the host: an import of
// one stays in plugin.js as node:<name>, which the host resolves when it
// loads the plugin. A module whose functions throw for plugins, such as
// node:child_process, is an error when the project's own code imports it,
// and a warning when a dependency does, which may never call it.
func nodeModulesPlugin(dir string) api.Plugin {
	return api.Plugin{Name: "astrlink-node", Setup: func(build api.PluginBuild) {
		build.OnResolve(api.OnResolveOptions{Filter: `^(node:)?[a-z_0-9]+(/[a-z_]+)?$`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			name, usable, found := plugin.NodeBuiltin(args.Path)
			if !found {
				return api.OnResolveResult{}, nil
			}
			result := api.OnResolveResult{Path: "node:" + name, External: true}
			if usable {
				return result, nil
			}
			text := fmt.Sprintf("node:%s is not available to plugins: %s", name, plugin.NodeUnavailable(name))
			if ownCode(dir, args.Importer) {
				return api.OnResolveResult{Errors: []api.Message{{Text: text}}}, nil
			}
			result.Warnings = []api.Message{{Text: text + "; the plugin loads, and what calls it throws"}}
			return result, nil
		})
	}}
}

// ownCode reports whether file is the project's own code rather than a
// dependency's.
func ownCode(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return !slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), "node_modules")
}

// locating marks the resolution packagesPlugin asks the bundler for, which
// it leaves to the bundler.
type locating struct{}

// packagesPlugin resolves the project's bare imports in the directories
// packages places them in, as the bundler would in node_modules.
func packagesPlugin(packages Packages) api.Plugin {
	return api.Plugin{Name: "astrlink-packages", Setup: func(build api.PluginBuild) {
		build.OnResolve(api.OnResolveOptions{Filter: `^[^./]`, Namespace: "file"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			if _, again := args.PluginData.(locating); again || args.Kind == api.ResolveEntryPoint || filepath.IsAbs(args.Path) {
				return api.OnResolveResult{}, nil
			}
			name := PackageName(args.Path)
			if name == "" || strings.HasPrefix(args.Path, "node:") {
				return api.OnResolveResult{}, nil
			}
			dir, ok, err := packages.Locate(args.Importer, name)
			if err != nil {
				return api.OnResolveResult{}, err
			}
			if !ok {
				return api.OnResolveResult{}, nil
			}
			resolved := build.Resolve(args.Path, api.ResolveOptions{
				Importer: args.Importer, Kind: args.Kind, ResolveDir: dir, PluginData: locating{}, With: args.With,
			})
			if len(resolved.Errors) > 0 {
				return api.OnResolveResult{Errors: resolved.Errors, Warnings: resolved.Warnings}, nil
			}
			result := api.OnResolveResult{
				Path: resolved.Path, External: resolved.External, Namespace: resolved.Namespace,
				Suffix: resolved.Suffix, PluginData: resolved.PluginData, Warnings: resolved.Warnings,
			}
			if !resolved.SideEffects {
				result.SideEffects = api.SideEffectsFalse
			}
			return result, nil
		})
	}}
}

// PackageName is the package a bare import names: its first segment, or
// its first two for a scoped package. "" for anything else.
func PackageName(specifier string) string {
	parts := strings.SplitN(specifier, "/", 3)
	if strings.HasPrefix(specifier, "@") {
		if len(parts) < 2 || parts[0] == "@" || parts[1] == "" {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	if parts[0] == "" {
		return ""
	}
	return parts[0]
}

func quoteAll(names []string) []string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = regexp.QuoteMeta(name)
	}
	return quoted
}

// metafileInputs lists the inputs of a bundler metafile, relative to the
// working directory.
func metafileInputs(metafile string) []string {
	var parsed struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if json.Unmarshal([]byte(metafile), &parsed) != nil {
		return nil
	}
	inputs := make([]string, 0, len(parsed.Inputs))
	for input := range parsed.Inputs {
		inputs = append(inputs, input)
	}
	return inputs
}

// messages turns the bundler's messages into the build's, explaining the
// ones a plugin author meets because plugins run in moejs.
func messages(dir string, found []api.Message) []Message {
	converted := make([]Message, 0, len(found))
	for _, message := range found {
		text := message.Text
		builtin := strings.Contains(text, "built into node")
		for _, note := range message.Notes {
			builtin = builtin || strings.Contains(note.Text, "built into node")
		}
		switch {
		case builtin:
			text += ": the host does not provide this module of Node to plugins"
		case strings.Contains(text, `No loader is configured for ".node" files`):
			text = "a dependency uses a native module, which plugins cannot load"
		}
		entry := Message{Text: text}
		if location := message.Location; location != nil {
			entry.File = relative(dir, location.File)
			entry.Line = location.Line
			entry.Column = location.Column + 1
		}
		converted = append(converted, entry)
	}
	return converted
}

func relative(dir, file string) string {
	if file == "" || !filepath.IsAbs(file) {
		return filepath.ToSlash(file)
	}
	if rel, err := filepath.Rel(dir, file); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(file)
}
