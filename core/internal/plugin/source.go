package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/syntax"

	"github.com/QuantumNous/astrlink/core/contract"
)

// MaxSourceBytes bounds a plugin's source. Compiling takes about 85 ms and
// 30 MiB of temporary memory per MiB of source, once per load; a plugin made
// of several files is bundled into one first.
const MaxSourceBytes = 8 << 20

// MaxIconBytes bounds a plugin's icon, which the plugin page shows as is.
const MaxIconBytes = 64 << 10

// iconFiles are the names a plugin's icon may have beside its source, the
// vector one first.
var iconFiles = []struct{ name, mime string }{
	{"icon.svg", "image/svg+xml"},
	{"icon.png", "image/png"},
}

// Codes of LoadError, stable for the desktop to word.
const (
	CodeNotFound      = "not_found"
	CodeAmbiguous     = "ambiguous"
	CodeTooLarge      = "too_large"
	CodeEncoding      = "encoding"
	CodeSyntax        = "syntax"
	CodeAsync         = "async"
	CodeAwait         = "await"
	CodeImport        = "import"
	CodeReexport      = "reexport"
	CodeMissingMeta   = "missing_meta"
	CodeInvalidMeta   = "invalid_meta"
	CodeUnknownExport = "unknown_export"
	CodeNotFunction   = "not_function"
	CodeLoadFailed    = "load_failed"
)

// LoadError explains why a plugin did not load. Line and Column, when set,
// point into File; Message is safe to show.
type LoadError struct {
	Code    string
	File    string
	Line    int
	Column  int
	Message string
}

func (e *LoadError) Error() string {
	switch {
	case e.File != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
	case e.File != "":
		return e.File + ": " + e.Message
	}
	return e.Message
}

// Origin tells where a plugin's source comes from.
type Origin string

const (
	// OriginBuiltin is a plugin compiled into Core.
	OriginBuiltin Origin = "builtin"
	// OriginDirectory is a plugin in the plugin directory.
	OriginDirectory Origin = "directory"
	// OriginOverride is a plugin in the plugin directory that replaces the
	// builtin plugin of the same key.
	OriginOverride Origin = "override"
)

// Source is a plugin before it is compiled. Err is set instead of Text when
// the source could not be read.
type Source struct {
	Key    string
	Origin Origin
	// File is the source's name in errors and stack traces: plugin.ts or
	// plugin.js.
	File string
	Text string
	Err  *LoadError
	// Icon is the icon beside the source as a data URL, "" for none.
	Icon string
	// SourceMap is the source map beside a built plugin.js, plugin.js.map,
	// "" for none.
	SourceMap string
	// Development is set for a plugin astrlink plugin dev keeps built from
	// a project on this computer, which DevelopmentMarker names.
	Development *Development
	// Installed is set for a plugin installed from source: InstallRecord
	// beside it says where it came from.
	Installed *contract.PluginInstallRecord
	// Client is set for a built plugin with interface code, client.js
	// beside plugin.js; ClientHash names its content.
	Client     bool
	ClientHash string
	// Dir is the directory the plugin's files are in, which its node:fs
	// reads; "" when they are not on disk. A directory plugin's is under
	// the plugin directory.
	Dir string
}

// InstallRecord is the file an install from source writes beside the
// plugin it built.
const InstallRecord = "install.json"

// DevelopmentMarker is the file astrlink plugin dev writes beside the plugin
// it builds: the host reloads such a plugin when its files change.
const DevelopmentMarker = ".astrlink-dev.json"

// Development describes a plugin astrlink plugin dev keeps built.
type Development struct {
	// Project is the directory of the project it is built from.
	Project string `json:"project"`
	// BuiltAt is when it was last built, in RFC 3339.
	BuiltAt string `json:"built_at"`
}

// sourceFiles are the names a plugin's source may have, TypeScript first.
var sourceFiles = []string{"plugin.ts", "plugin.js"}

// Discover lists the plugins of builtin and of dir, each as
// <key>/plugin.ts or <key>/plugin.js, sorted by key. A directory plugin
// replaces the builtin plugin of its key. builtin and dir may each be
// missing; a dir that does not exist holds no plugins.
func Discover(builtin fs.FS, dir string) ([]Source, error) {
	byKey := make(map[string]Source)
	if builtin != nil {
		sources, err := discoverFS(builtin, OriginBuiltin)
		if err != nil {
			return nil, fmt.Errorf("read builtin plugins: %w", err)
		}
		for _, source := range sources {
			byKey[source.Key] = source
		}
	}
	if dir != "" {
		sources, err := discoverFS(os.DirFS(dir), OriginDirectory)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read plugin directory: %w", err)
		}
		for _, source := range sources {
			if _, builtin := byKey[source.Key]; builtin {
				source.Origin = OriginOverride
			}
			source.Dir = filepath.Join(dir, source.Key)
			byKey[source.Key] = source
		}
	}
	sources := make([]Source, 0, len(byKey))
	for _, source := range byKey {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Key < sources[j].Key })
	return sources, nil
}

func discoverFS(fsys fs.FS, origin Origin) ([]Source, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var sources []Source
	for _, entry := range entries {
		if !ValidPluginKey(entry.Name()) {
			continue
		}
		if !entry.IsDir() {
			// A link to a directory, such as a plugin checked out elsewhere,
			// counts as one.
			info, err := fs.Stat(fsys, entry.Name())
			if err != nil || !info.IsDir() {
				continue
			}
		}
		sources = append(sources, readSource(fsys, entry.Name(), origin))
	}
	return sources, nil
}

func readSource(fsys fs.FS, key string, origin Origin) Source {
	source := Source{Key: key, Origin: origin, Icon: readIcon(fsys, key)}
	var found []string
	for _, name := range sourceFiles {
		if info, err := fs.Stat(fsys, path.Join(key, name)); err == nil && info.Mode().IsRegular() {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		source.Err = &LoadError{Code: CodeNotFound, Message: "the plugin directory has no plugin.ts or plugin.js"}
		return source
	case 2:
		source.Err = &LoadError{Code: CodeAmbiguous, Message: "the plugin directory has both plugin.ts and plugin.js"}
		return source
	}
	source.File = found[0]
	file, err := fsys.Open(path.Join(key, source.File))
	if err != nil {
		source.Err = &LoadError{Code: CodeNotFound, File: source.File, Message: "cannot read the source"}
		return source
	}
	defer file.Close()
	text, err := io.ReadAll(io.LimitReader(file, MaxSourceBytes+1))
	if err != nil {
		source.Err = &LoadError{Code: CodeNotFound, File: source.File, Message: "cannot read the source"}
		return source
	}
	source.Text = string(text)
	if source.File == "plugin.js" {
		source.SourceMap = readSourceMap(fsys, key)
		if client, err := fs.ReadFile(fsys, path.Join(key, "client.js")); err == nil {
			sum := sha256.Sum256(client)
			source.Client, source.ClientHash = true, hex.EncodeToString(sum[:8])
		}
	}
	if origin != OriginBuiltin {
		source.Development = readDevelopment(fsys, key)
		source.Installed = readInstalled(fsys, key)
	}
	return source
}

// readInstalled reads the record of a plugin installed from source, nil
// when there is none.
func readInstalled(fsys fs.FS, key string) *contract.PluginInstallRecord {
	data, err := fs.ReadFile(fsys, path.Join(key, InstallRecord))
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	var record contract.PluginInstallRecord
	if json.Unmarshal(data, &record) != nil || record.Source.Repository == "" {
		return nil
	}
	return &record
}

// readDevelopment reads the marker of a plugin astrlink plugin dev keeps
// built, nil when there is none.
func readDevelopment(fsys fs.FS, key string) *Development {
	data, err := fs.ReadFile(fsys, path.Join(key, DevelopmentMarker))
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var marker Development
	if json.Unmarshal(data, &marker) != nil {
		return nil
	}
	return &marker
}

// readSourceMap returns the source map beside a plugin.js, "" when there is
// none it can use. A source map never keeps a plugin from loading.
func readSourceMap(fsys fs.FS, key string) string {
	file, err := fsys.Open(path.Join(key, "plugin.js.map"))
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxSourceMapBytes+1))
	if err != nil || len(data) > MaxSourceMapBytes || !utf8.Valid(data) {
		return ""
	}
	return string(data)
}

// readIcon returns the plugin's icon as a data URL, or "" when it has none
// the page can show: each name is skipped when missing, larger than
// MaxIconBytes, or not the image it says. An icon never keeps a plugin from
// loading.
func readIcon(fsys fs.FS, key string) string {
	for _, icon := range iconFiles {
		file, err := fsys.Open(path.Join(key, icon.name))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxIconBytes+1))
		file.Close()
		if err != nil || len(data) > MaxIconBytes {
			continue
		}
		valid := bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
		if icon.mime == "image/svg+xml" {
			valid = svgDocument(data)
		}
		if valid {
			return "data:" + icon.mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return ""
}

// svgDocument tells whether data is an SVG document without scripts: its
// root, after any XML declaration, comments and doctype, is an svg element.
// The page shows icons as images, where scripts never run; refusing them
// keeps an icon opened on its own from running one either.
func svgDocument(data []byte) bool {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	lower := strings.ToLower(text)
	if strings.Contains(lower, "<script") || strings.Contains(lower, "javascript:") {
		return false
	}
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		var end string
		switch {
		case strings.HasPrefix(text, "<?xml"):
			end = "?>"
		case strings.HasPrefix(text, "<!--"):
			end = "-->"
		case strings.HasPrefix(text, "<!DOCTYPE"), strings.HasPrefix(text, "<!doctype"):
			end = ">"
		default:
			rest, ok := strings.CutPrefix(text, "<svg")
			return ok && rest != "" && strings.ContainsRune(" \t\r\n>", rune(rest[0]))
		}
		_, after, found := strings.Cut(text, end)
		if !found {
			return false
		}
		text = after
	}
}

// Path returns where a directory plugin's source lives under dir.
func (source Source) Path(dir string) string {
	if source.Origin == OriginBuiltin || source.File == "" {
		return ""
	}
	return filepath.Join(dir, source.Key, source.File)
}

// compile checks a plugin's source against what the host runs and compiles
// it once, for every runtime of its pool to load. It returns the parse tree
// too, for the checks that depend on the plugin's apiVersion.
func compile(file, text string) (*moejs.Module, *syntax.Module, error) {
	if len(text) > MaxSourceBytes {
		return nil, nil, &LoadError{Code: CodeTooLarge, File: file, Message: fmt.Sprintf("the source is larger than %d MiB", MaxSourceBytes>>20)}
	}
	if !utf8.ValidString(text) {
		return nil, nil, &LoadError{Code: CodeEncoding, File: file, Message: "the source is not valid UTF-8"}
	}
	typescript := strings.HasSuffix(file, ".ts")
	parsed, err := syntax.ParseModule(file, text, syntax.Options{TypeScript: typescript})
	if err != nil {
		return nil, nil, syntaxLoadError(file, err)
	}
	if err := checkSingleFile(file, parsed); err != nil {
		return nil, nil, err
	}
	var module *moejs.Module
	if typescript {
		module, err = moejs.CompileTS(file, text)
	} else {
		module, err = moejs.Compile(file, text)
	}
	if err != nil {
		return nil, nil, syntaxLoadError(file, err)
	}
	if len(module.Requests()) > 0 {
		linked, err := moejs.Link(module, resolveNode)
		if err != nil {
			return nil, nil, &LoadError{Code: CodeImport, File: file, Message: cleanText(linkMessage(err), maxMessageRunes, "")}
		}
		module = linked
	}
	return module, parsed, nil
}

// linkMessage is what linking a plugin with the modules of Node failed
// with.
func linkMessage(err error) string {
	var resolve *moejs.ResolveError
	if errors.As(err, &resolve) && resolve.Err != nil {
		return resolve.Err.Error()
	}
	return err.Error()
}

// checkSingleFile refuses imports of anything but the modules of Node,
// which the host provides: a plugin made of several files, or with
// packages, is bundled into one when it is built.
func checkSingleFile(file string, parsed *syntax.Module) error {
	return inspectSource(file, parsed, func(node syntax.Node) (string, string) {
		var source *syntax.StringLit
		code := CodeImport
		switch node := node.(type) {
		case *syntax.ImportDecl:
			source = node.Source
		case *syntax.ImportCall:
			if literal, ok := node.Source.(*syntax.StringLit); ok {
				source = literal
			}
		case *syntax.ExportNamed:
			source, code = node.Source, CodeReexport
		case *syntax.ExportAll:
			source, code = node.Source, CodeReexport
		}
		if source == nil {
			return "", ""
		}
		if _, _, found := NodeBuiltin(source.Value); found {
			return "", ""
		}
		return code, fmt.Sprintf("imports %q; a plugin imports only the modules of Node, so bundle it and its packages into one file", source.Value)
	})
}

// inspectSource returns the first node of parsed that check finds fault with,
// as a LoadError at its position. The parse tree tells code from comments,
// strings and regular expressions.
func inspectSource(file string, parsed *syntax.Module, check func(syntax.Node) (code, message string)) error {
	var found *LoadError
	syntax.Inspect(parsed, func(node syntax.Node) bool {
		if found != nil {
			return false
		}
		if code, message := check(node); code != "" {
			pos, _ := node.Range()
			line, column := parsed.File.Position(pos)
			found = &LoadError{Code: code, File: file, Line: line, Column: column, Message: message}
		}
		return found == nil
	})
	if found != nil {
		return found
	}
	return nil
}

// checkSynchronous refuses what an apiVersion 1 plugin, which is
// synchronous, cannot use: async functions and await, which need the loop
// the host runs for apiVersion 2 only.
func checkSynchronous(file string, parsed *syntax.Module) error {
	return inspectSource(file, parsed, func(node syntax.Node) (string, string) {
		switch node := node.(type) {
		case *syntax.Function:
			if node.IsAsync {
				return CodeAsync, "async functions need apiVersion 2; an apiVersion 1 plugin is synchronous and http.fetch takes an array to run requests in parallel"
			}
		case *syntax.AwaitExpr:
			return CodeAwait, "await needs apiVersion 2; an apiVersion 1 plugin is synchronous"
		case *syntax.ForInOfStmt:
			if node.Await {
				return CodeAwait, "for await needs apiVersion 2; an apiVersion 1 plugin is synchronous"
			}
		}
		return "", ""
	})
}

func syntaxLoadError(file string, err error) error {
	var parse *syntax.Error
	if errors.As(err, &parse) {
		return &LoadError{Code: CodeSyntax, File: file, Line: parse.Line, Column: parse.Col, Message: cleanText(strings.TrimPrefix(parse.Msg, "SyntaxError: "), maxMessageRunes, "")}
	}
	var compiled *moejs.SyntaxError
	if errors.As(err, &compiled) {
		return &LoadError{Code: CodeSyntax, File: file, Line: compiled.Line, Column: compiled.Column, Message: cleanText(compiled.Message, maxMessageRunes, "")}
	}
	return &LoadError{Code: CodeSyntax, File: file, Message: cleanText(err.Error(), maxMessageRunes, "")}
}
