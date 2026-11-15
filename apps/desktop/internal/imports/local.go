package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/clients"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

// The local import reads the providers Claude Code, Codex and Pi already
// use, from their config files in the desktop user's home and in running
// WSL homes.
//
// Nothing is read until the user picks tools and asks to read them, and
// only those tools' files are read. Keys stay here: the dialog sees hints
// and confirms by id, and a key is attached only to a request for the
// address it was configured with. Commands that would print a key never
// run, and source files are never written.

const (
	// idleTimeout: keys read for one dialog are dropped after this long
	// without a request.
	idleTimeout       = 10 * time.Minute
	maxFileBytes      = 4 * 1024 * 1024
	officialAnthropic = "https://api.anthropic.com"
	officialOpenAI    = "https://api.openai.com/v1"
	closedImport      = "this import has closed; read the configs again"
)

// localAuthSchemes are the sign-in methods that send an API key; others
// cannot carry an imported one.
var localAuthSchemes = []string{"bearer", "anthropic_api_key", "google_api_key"}

// Tool is an agent tool whose config files the import reads.
type Tool string

const (
	ToolClaude Tool = "claude"
	ToolCodex  Tool = "codex"
	ToolPi     Tool = "pi"
)

var tools = []Tool{ToolClaude, ToolCodex, ToolPi}

// files are the files a read opens. The first one holds the providers.
func (t Tool) files(home agents.Home) []string {
	switch t {
	case ToolClaude:
		return []string{filepath.Join(home.ClaudeDir(), "settings.json")}
	case ToolCodex:
		dir := home.CodexDir()
		return []string{filepath.Join(dir, "config.toml"), filepath.Join(dir, "auth.json")}
	}
	return []string{filepath.Join(home.PiAgentDir(), "models.json")}
}

func (t Tool) detected(home agents.Home) bool {
	switch t {
	case ToolClaude:
		return home.ClaudeDetected()
	case ToolCodex:
		info, err := os.Stat(home.CodexDir())
		return err == nil && info.IsDir()
	}
	return home.PiDetected()
}

// SourceView is one tool in one home, as the consent step lists it.
type SourceView struct {
	ID   string `json:"id"`
	Tool Tool   `json:"tool"`
	// WSL names the WSL distribution, null for the desktop user's home.
	WSL *string `json:"wsl"`
	// Paths are the files a read opens, with the home shown as `~`.
	Paths []string `json:"paths"`
}

type source struct {
	view  SourceView
	files []string
}

// Opened is a new import's session and the sources it may read.
type Opened struct {
	Session string       `json:"session"`
	Sources []SourceView `json:"sources"`
}

// SourceRef is the tool and home an entry was read from.
type SourceRef struct {
	Tool Tool    `json:"tool"`
	WSL  *string `json:"wsl"`
}

func (r SourceRef) same(other SourceRef) bool {
	return r.Tool == other.Tool && sameString(r.WSL, other.WSL)
}

func sameString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

type keyKind int

const (
	keyMissing keyKind = iota
	keyFound
	// keyEnv is named by an environment variable AstrLink's own
	// environment lacks.
	keyEnv
	// keyCommand is printed by a command, which is never run.
	keyCommand
)

// key is a provider's key: the secret, the variable's name, or the
// command.
type key struct {
	kind  keyKind
	value string
}

func foundKey(secret string) key { return key{keyFound, secret} }

func (k key) view() KeyView {
	switch k.kind {
	case keyFound:
		return KeyView{Status: KeyFound, Hint: KeyHint(k.value)}
	case keyEnv:
		return KeyView{Status: KeyEnv, Name: k.value}
	case keyCommand:
		return KeyView{Status: KeyCommand}
	}
	return KeyView{Status: KeyMissing}
}

// KeyStatus is how much of a provider's key the import could read.
type KeyStatus string

const (
	KeyFound   KeyStatus = "found"
	KeyEnv     KeyStatus = "env"
	KeyCommand KeyStatus = "command"
	KeyMissing KeyStatus = "missing"
)

// KeyView is a provider's key as the dialog shows it: a hint of a found
// key, or the variable that names one.
type KeyView struct {
	Status KeyStatus `json:"status"`
	Hint   *string   `json:"hint,omitempty"`
	Name   string    `json:"name,omitempty"`
}

// MarshalJSON writes the fields of the key's status, a found key's hint
// even when it is null.
func (k KeyView) MarshalJSON() ([]byte, error) {
	switch k.Status {
	case KeyFound:
		return json.Marshal(struct {
			Status KeyStatus `json:"status"`
			Hint   *string   `json:"hint"`
		}{k.Status, k.Hint})
	case KeyEnv:
		return json.Marshal(struct {
			Status KeyStatus `json:"status"`
			Name   string    `json:"name"`
		}{k.Status, k.Name})
	}
	return json.Marshal(struct {
		Status KeyStatus `json:"status"`
	}{k.Status})
}

// entry is a provider as one config file sets it up.
type entry struct {
	name     *string
	address  string
	protocol string
	models   []string
	key      key
}

// SkipReason is why a read does not offer something it found.
type SkipReason string

const (
	// SkipChatGPTLogin: Codex signed in with ChatGPT; a subscription
	// account covers it.
	SkipChatGPTLogin SkipReason = "chatgpt_login"
	// SkipAstrLink: the entry points at AstrLink itself.
	SkipAstrLink       SkipReason = "astrlink"
	SkipMissing        SkipReason = "missing"
	SkipUnreadable     SkipReason = "unreadable"
	SkipInvalid        SkipReason = "invalid"
	SkipInvalidAddress SkipReason = "invalid_address"
	SkipUnsupported    SkipReason = "unsupported"
	SkipSignIn         SkipReason = "sign_in"
)

// Skipped is something a read found but does not offer, and why.
type Skipped struct {
	Reason SkipReason `json:"reason"`
	Tool   Tool       `json:"tool"`
	WSL    *string    `json:"wsl"`
	Name   *string    `json:"name"`
	Path   *string    `json:"path"`
	Detail *string    `json:"detail"`
}

func (s Skipped) same(other Skipped) bool {
	return s.Reason == other.Reason && s.Tool == other.Tool && sameString(s.WSL, other.WSL) &&
		sameString(s.Name, other.Name) && sameString(s.Path, other.Path) && sameString(s.Detail, other.Detail)
}

type skip struct {
	reason SkipReason
	name   *string
	path   *string
	detail *string
}

func named(reason SkipReason, name *string) skip {
	s := skip{reason: reason}
	if name != nil {
		s.name = shownName(*name)
	}
	return s
}

func fileSkip(reason SkipReason, path string, detail *string) skip {
	return skip{reason: reason, path: &path, detail: detail}
}

func (s skip) at(place SourceRef) Skipped {
	return Skipped{Reason: s.reason, Tool: place.Tool, WSL: place.WSL, Name: s.name, Path: s.path, Detail: s.detail}
}

type progress int

const (
	progressReady progress = iota
	progressBusy
	progressImported
)

// provider is one the read found; entries with the same address and key
// merge.
type provider struct {
	id         string
	name       *string
	address    string
	origin     string
	normalized string
	protocols  []string
	models     []string
	key        key
	sources    []SourceRef
	progress   progress
	probed     bool
}

func (p *provider) view() Candidate {
	return Candidate{
		ID:        p.id,
		Name:      p.name,
		Address:   p.address,
		Protocols: slices.Clone(p.protocols),
		Models:    slices.Clone(p.models),
		Key:       p.key.view(),
		Sources:   slices.Clone(p.sources),
	}
}

// Candidate is a found provider as the dialog shows it; it never carries a
// key.
type Candidate struct {
	ID      string  `json:"id"`
	Name    *string `json:"name"`
	Address string  `json:"address"`
	// Protocols are the formats the tools speak to it, as Core protocol
	// ids.
	Protocols []string    `json:"protocols"`
	Models    []string    `json:"models"`
	Key       KeyView     `json:"key"`
	Sources   []SourceRef `json:"sources"`
}

// ReadResult is what a read offers and what it skipped.
type ReadResult struct {
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
}

type collected struct {
	found   []*provider
	skipped []Skipped
}

type localSession struct {
	id      string
	sources []source
	found   []*provider
	touched time.Time
	watched bool
}

func (s *localSession) provider(candidate string) (*provider, error) {
	for _, p := range s.found {
		if p.id == candidate {
			return p, nil
		}
	}
	return nil, errors.New("this provider is not in the import")
}

// LocalImports holds the open import dialog's sources and, after a read,
// what it found. Opening the dialog again replaces it.
type LocalImports struct {
	mu      sync.Mutex
	session *localSession
	idle    time.Duration
	// schedule runs the idle check later; tests run it by hand.
	schedule func(time.Duration, func())
}

// NewLocalImports drops a dialog's keys after ten idle minutes.
func NewLocalImports() *LocalImports { return newLocalImports(idleTimeout) }

func newLocalImports(idle time.Duration) *LocalImports {
	return &LocalImports{idle: idle, schedule: func(wait time.Duration, check func()) { time.AfterFunc(wait, check) }}
}

// Open lists every tool in the desktop user's home, then the tools found
// in each WSL home, and starts a new import that replaces any earlier one.
// Only directories are checked; no file is opened.
func (l *LocalImports) Open(homes *agents.Homes) (Opened, error) {
	return l.open(locate(homes))
}

func (l *LocalImports) open(sources []source) (Opened, error) {
	id, err := NewID()
	if err != nil {
		return Opened{}, err
	}
	opened := Opened{Session: id, Sources: []SourceView{}}
	for _, s := range sources {
		opened.Sources = append(opened.Sources, s.view)
	}
	l.mu.Lock()
	l.session = &localSession{id: id, sources: sources, touched: time.Now()}
	l.mu.Unlock()
	return opened, nil
}

// with runs task on the session named id unless it closed, or sat idle
// while holding what a read found. Before a read it holds no keys.
func (l *LocalImports) with(id string, task func(*localSession) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.session; s != nil && len(s.found) > 0 && time.Since(s.touched) >= l.idle {
		l.session = nil
	}
	s := l.session
	if s == nil || s.id != id {
		return errors.New(closedImport)
	}
	s.touched = time.Now()
	return task(s)
}

// chosen returns the sources the user checked, in the order the dialog
// listed them.
func (l *LocalImports) chosen(id string, chosen []string) ([]source, error) {
	var picked []source
	err := l.with(id, func(s *localSession) error {
		if len(chosen) == 0 {
			return errors.New("choose a tool to read")
		}
		for _, want := range chosen {
			if !slices.ContainsFunc(s.sources, func(src source) bool { return src.view.ID == want }) {
				return fmt.Errorf("unknown source %s", want)
			}
		}
		for _, src := range s.sources {
			if slices.Contains(chosen, src.view.ID) {
				picked = append(picked, src)
			}
		}
		return nil
	})
	return picked, err
}

func (l *LocalImports) store(id string, read collected) (ReadResult, error) {
	var result ReadResult
	err := l.with(id, func(s *localSession) error {
		s.found = read.found
		result = ReadResult{Candidates: []Candidate{}, Skipped: read.skipped}
		for _, p := range s.found {
			result.Candidates = append(result.Candidates, p.view())
		}
		return nil
	})
	return result, err
}

// Read reads the checked sources. The result names keys only by hint.
// Loopback addresses on gatewayPorts are AstrLink itself.
func (l *LocalImports) Read(id string, sources []string, gatewayPorts []uint16) (ReadResult, error) {
	chosen, err := l.chosen(id, sources)
	if err != nil {
		return ReadResult{}, err
	}
	result, err := l.store(id, collect(chosen, readFile, processEnv, gatewayPorts))
	if err != nil {
		return ReadResult{}, err
	}
	l.watch(id)
	return result, nil
}

// beginProbe attaches the provider's key to the one model-list request it
// gets.
func (l *LocalImports) beginProbe(id, candidate string, input json.RawMessage) (json.RawMessage, string, error) {
	var authorized json.RawMessage
	var secret string
	err := l.with(id, func(s *localSession) error {
		p, err := s.provider(candidate)
		if err != nil {
			return err
		}
		if p.progress != progressReady {
			return errors.New("this provider was already imported")
		}
		if p.probed {
			return errors.New("the model list was already requested")
		}
		authorized, secret, err = authorizeLocal(p, input, []string{"kind", "http", "protocol"})
		if err != nil {
			return err
		}
		p.probed = true
		return nil
	})
	return authorized, secret, err
}

// beginConfirm holds the provider while it is created so a second confirm
// cannot add it twice.
func (l *LocalImports) beginConfirm(id, candidate string, input json.RawMessage) (json.RawMessage, string, error) {
	var authorized json.RawMessage
	var secret string
	err := l.with(id, func(s *localSession) error {
		p, err := s.provider(candidate)
		if err != nil {
			return err
		}
		switch p.progress {
		case progressBusy:
			return errors.New("this provider is being imported")
		case progressImported:
			return errors.New("this provider was already imported")
		}
		authorized, secret, err = authorizeLocal(p, input, []string{"name", "kind", "enabled", "models", "http", "capabilities"})
		if err != nil {
			return err
		}
		p.progress = progressBusy
		return nil
	})
	return authorized, secret, err
}

// finishConfirm records the outcome; an imported provider's key is dropped
// at once.
func (l *LocalImports) finishConfirm(id, candidate string, imported bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session == nil || l.session.id != id {
		return
	}
	p, err := l.session.provider(candidate)
	if err != nil {
		return
	}
	if imported {
		p.progress = progressImported
		p.key = key{}
	} else {
		p.progress = progressReady
	}
}

// ModelProber is Core's control API for a draft service's model list.
type ModelProber interface {
	ProbeDraftServiceModels(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Probe makes the one model-list request a found provider gets before it
// is created.
func (l *LocalImports) Probe(ctx context.Context, core ModelProber, id, candidate string, input json.RawMessage) (json.RawMessage, error) {
	authorized, secret, err := l.beginProbe(id, candidate, input)
	if err != nil {
		return nil, err
	}
	models, err := core.ProbeDraftServiceModels(ctx, authorized)
	if err != nil {
		return nil, errors.New(redact(err.Error(), &secret))
	}
	return models, nil
}

// Confirm creates a found provider with the key the read found, or the one
// the user pasted for it.
func (l *LocalImports) Confirm(ctx context.Context, core ServiceCreator, id, candidate string, input json.RawMessage) (control.ServiceRecord, error) {
	authorized, secret, err := l.beginConfirm(id, candidate, input)
	if err != nil {
		return control.ServiceRecord{}, err
	}
	record, err := core.CreateService(ctx, authorized)
	l.finishConfirm(id, candidate, err == nil)
	if err != nil {
		return control.ServiceRecord{}, errors.New(redact(err.Error(), &secret))
	}
	return record, nil
}

// Dismiss ends the import and drops every key it read.
func (l *LocalImports) Dismiss(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session != nil && l.session.id == id {
		l.session = nil
	}
}

// watch drops the session's keys once it sits idle, even while the dialog
// stays open without another request.
func (l *LocalImports) watch(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.session; s != nil && s.id == id && !s.watched {
		s.watched = true
		l.schedule(l.idle, func() { l.expire(id) })
	}
}

func (l *LocalImports) expire(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.session
	if s == nil || s.id != id {
		return
	}
	elapsed := time.Since(s.touched)
	if elapsed >= l.idle {
		l.session = nil
		return
	}
	l.schedule(l.idle-elapsed, func() { l.expire(id) })
}

// locate lists every tool in the desktop user's home, then the tools
// found in each WSL home.
func locate(homes *agents.Homes) []source {
	var sources []source
	for _, home := range homes.All() {
		for _, tool := range tools {
			distribution := home.Distribution()
			if distribution != "" && !tool.detected(home) {
				continue
			}
			files := tool.files(home)
			view := SourceView{ID: fmt.Sprintf("s%d", len(sources)), Tool: tool, Paths: []string{}}
			if distribution != "" {
				view.WSL = &distribution
			}
			for _, file := range files {
				view.Paths = append(view.Paths, displayPath(home, file))
			}
			sources = append(sources, source{view: view, files: files})
		}
	}
	return sources
}

func displayPath(home agents.Home, path string) string {
	rest, err := filepath.Rel(home.Dir(), path)
	if err != nil || rest == ".." || strings.HasPrefix(rest, ".."+string(filepath.Separator)) {
		return home.Shown(path)
	}
	if rest == "." {
		return "~"
	}
	separator := "/"
	if home.Windows() {
		separator = `\`
	}
	return "~" + separator + strings.Join(strings.Split(rest, string(filepath.Separator)), separator)
}

// fileReader returns a file's text, nil when it does not exist, or the code
// of why it cannot be read.
type fileReader func(path string) (*string, string)

func readFile(path string) (*string, string) {
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ""
	case errors.Is(err, fs.ErrPermission):
		return nil, "permission"
	case err != nil:
		return nil, "io"
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, "io"
	}
	bytes, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, "io"
	}
	if len(bytes) > maxFileBytes {
		return nil, "too_large"
	}
	if !utf8.Valid(bytes) {
		return nil, "not_text"
	}
	text := strings.TrimPrefix(string(bytes), "\uFEFF")
	return &text, ""
}

// lookupEnv reads one environment variable.
type lookupEnv func(name string) (string, bool)

// processEnv is an environment variable of AstrLink's own process; no
// shell is asked.
func processEnv(name string) (string, bool) {
	value, ok := os.LookupEnv(name)
	return value, ok && trim(value) != ""
}

// GatewayPorts are the ports of Core's inference addresses; loopback
// addresses there are AstrLink.
func GatewayPorts(addresses ...string) []uint16 {
	ports := []uint16{}
	for _, address := range addresses {
		if u, err := url.Parse(address); err == nil {
			if port, ok := portOrDefault(u); ok {
				ports = append(ports, port)
			}
		}
	}
	return ports
}

func collect(sources []source, read fileReader, env lookupEnv, gatewayPorts []uint16) collected {
	result := collected{skipped: []Skipped{}}
	for _, src := range sources {
		place := SourceRef{Tool: src.view.Tool, WSL: src.view.WSL}
		entries, skips := readSource(src, read, env)
		var skipped []Skipped
		for _, s := range skips {
			skipped = append(skipped, s.at(place))
		}
		for _, e := range entries {
			admitted, u, rejected := admit(e, gatewayPorts)
			if rejected != nil {
				skipped = append(skipped, rejected.at(place))
				continue
			}
			result.found = merge(result.found, admitted, u, place)
		}
		for _, s := range skipped {
			if !slices.ContainsFunc(result.skipped, s.same) {
				result.skipped = append(result.skipped, s)
			}
		}
	}
	return result
}

func readSource(src source, read fileReader, env lookupEnv) ([]entry, []skip) {
	var skipped []skip
	load := func(index int) *string {
		shown := src.view.Paths[index]
		text, code := read(src.files[index])
		switch {
		case code != "":
			skipped = append(skipped, fileSkip(SkipUnreadable, shown, &code))
		case text == nil && index == 0:
			// Codex's auth.json is optional; the provider file is not.
			skipped = append(skipped, fileSkip(SkipMissing, shown, nil))
		}
		return text
	}
	invalid := func(index int, err error) {
		message := err.Error()
		skipped = append(skipped, fileSkip(SkipInvalid, src.view.Paths[index], &message))
	}
	var entries []entry
	switch src.view.Tool {
	case ToolClaude:
		if text := load(0); text != nil {
			found, skips, err := claudeEntries(*text)
			if err != nil {
				invalid(0, err)
			}
			entries = found
			skipped = append(skipped, skips...)
		}
	case ToolCodex:
		var config *format.TOMLDocument
		if text := load(0); text != nil {
			var err error
			if config, err = clients.ReadCodexConfig(*text); err != nil {
				invalid(0, err)
				config = nil
			}
		}
		var auth *codexAuth
		if text := load(1); text != nil {
			var err error
			if auth, err = readCodexAuth(*text); err != nil {
				invalid(1, err)
				auth = nil
			}
		}
		found, skips := codexEntries(config, auth, env)
		entries = found
		skipped = append(skipped, skips...)
	case ToolPi:
		if text := load(0); text != nil {
			found, skips, err := piEntries(*text, env)
			if err != nil {
				invalid(0, err)
			}
			entries = found
			skipped = append(skipped, skips...)
		}
	}
	return entries, skipped
}

// admit rejects entries that point at AstrLink or have no usable address.
func admit(e entry, gatewayPorts []uint16) (entry, *url.URL, *skip) {
	address := strings.TrimRight(trim(e.address), "/")
	var u *url.URL
	if ValidBaseURL(address) {
		u, _ = url.Parse(address)
	}
	label := e.name
	if label == nil && u != nil {
		authority := authority(u)
		label = &authority
	}
	if e.key.kind == keyFound && strings.HasPrefix(strings.TrimLeftFunc(e.key.value, isRustWhitespace), "astr_") {
		rejected := named(SkipAstrLink, label)
		return entry{}, nil, &rejected
	}
	if u == nil {
		rejected := named(SkipInvalidAddress, label)
		return entry{}, nil, &rejected
	}
	if port, ok := portOrDefault(u); ok && loopback(u) && slices.Contains(gatewayPorts, port) {
		rejected := named(SkipAstrLink, label)
		return entry{}, nil, &rejected
	}
	e.address = address
	return e, u, nil
}

// host is the URL's host as WHATWG URLs serialize it: lowercased, an IPv6
// address in brackets.
func host(u *url.URL) string {
	name := strings.ToLower(u.Hostname())
	if strings.Contains(name, ":") {
		return "[" + name + "]"
	}
	return name
}

func defaultPort(scheme string) (uint16, bool) {
	switch scheme {
	case "http", "ws":
		return 80, true
	case "https", "wss":
		return 443, true
	}
	return 0, false
}

func portOrDefault(u *url.URL) (uint16, bool) {
	if u.Port() == "" {
		return defaultPort(u.Scheme)
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	return uint16(port), err == nil
}

// explicitPort is the URL's port unless it is the scheme's default.
func explicitPort(u *url.URL) (uint16, bool) {
	port, ok := portOrDefault(u)
	if known, isDefault := defaultPort(u.Scheme); !ok || isDefault && known == port {
		return 0, false
	}
	return port, true
}

func authority(u *url.URL) string {
	if port, ok := explicitPort(u); ok {
		return fmt.Sprintf("%s:%d", host(u), port)
	}
	return host(u)
}

func origin(u *url.URL) string { return u.Scheme + "://" + authority(u) }

func loopback(u *url.URL) bool {
	name := strings.ToLower(u.Hostname())
	if name == "" {
		return false
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return name == "localhost" || strings.HasSuffix(name, ".localhost")
}

// normalized is the scheme, host, port and path, ignoring a trailing slash
// and `/v1`, which clients append differently for the same endpoint.
func normalized(u *url.URL) string {
	path := strings.TrimRight(u.EscapedPath(), "/")
	path = strings.TrimRight(strings.TrimSuffix(path, "/v1"), "/")
	return origin(u) + path
}

func merge(found []*provider, e entry, u *url.URL, place SourceRef) []*provider {
	norm := normalized(u)
	index := slices.IndexFunc(found, func(p *provider) bool { return p.normalized == norm && p.key == e.key })
	if index < 0 {
		found = append(found, &provider{
			id:         fmt.Sprintf("c%d", len(found)),
			address:    e.address,
			origin:     origin(u),
			normalized: norm,
			protocols:  []string{},
			models:     []string{},
			key:        e.key,
			sources:    []SourceRef{},
		})
		index = len(found) - 1
	}
	p := found[index]
	if p.name == nil && e.name != nil {
		p.name = shownName(*e.name)
	}
	if e.protocol != "" && !slices.Contains(p.protocols, e.protocol) {
		p.protocols = append(p.protocols, e.protocol)
	}
	for _, model := range e.models {
		model = trim(model)
		if len(p.models) < maxModels && model != "" && cc.ValidModel(model) && !slices.Contains(p.models, model) {
			p.models = append(p.models, model)
		}
	}
	if !slices.ContainsFunc(p.sources, place.same) {
		p.sources = append(p.sources, place)
	}
	return found
}

// shownName is a name from a config file, if it is short plain text worth
// showing.
func shownName(name string) *string {
	name = trim(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameChars || !PlainText(name) {
		return nil
	}
	return &name
}

func envName(value string) bool {
	if len(value) > 256 || value == "" || !(isASCIIAlpha(value[0]) || value[0] == '_') {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isASCIIAlnum(value[i]) && value[i] != '_' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func envKey(name string, env lookupEnv) key {
	if !envName(name) {
		return key{}
	}
	if value, ok := env(name); ok {
		return foundKey(value)
	}
	return key{keyEnv, name}
}

// trimmed is a value's text, trimmed; false when it is blank.
func trimmed(value string, ok bool) (string, bool) {
	value = trim(value)
	return value, ok && value != ""
}

func jsonStr(object map[string]any, name string) (string, bool) {
	value, ok := object[name].(string)
	return trimmed(value, ok)
}

func tomlStr(table *format.TOMLTable, name string) (string, bool) {
	if table == nil {
		return "", false
	}
	return trimmed(format.TOMLItemString(table.Get(name)))
}

func tomlTrue(table *format.TOMLTable, name string) bool {
	item := table.Get(name)
	if item == nil {
		return false
	}
	value, _ := format.TOMLJSON(item)
	return value == true
}

// sortedKeys is serde_json's order of an object's keys.
func sortedKeys(object map[string]any) []string {
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// claudeEntries reads the one provider Claude Code's `env` sets: its own
// address or Anthropic's.
func claudeEntries(text string) ([]entry, []skip, error) {
	parsed, err := clients.ReadClaudeSettings(text)
	if err != nil {
		return nil, nil, err
	}
	settings, _ := parsed.(map[string]any)
	vars, _ := settings["env"].(map[string]any)
	variable := func(name string) (string, bool) { return jsonStr(vars, name) }
	for _, platform := range [][2]string{
		{"CLAUDE_CODE_USE_BEDROCK", "Amazon Bedrock"},
		{"CLAUDE_CODE_USE_VERTEX", "Google Vertex AI"},
		{"CLAUDE_CODE_USE_FOUNDRY", "Microsoft Foundry"},
	} {
		if value, ok := variable(platform[0]); ok && slices.Contains([]string{"1", "true", "yes", "on"}, asciiLower(value)) {
			return nil, []skip{named(SkipUnsupported, &platform[1])}, nil
		}
	}
	// Claude Code's order: the bearer token, the API key, then the helper.
	var k key
	if token, ok := variable("ANTHROPIC_AUTH_TOKEN"); ok {
		k = foundKey(token)
	} else if apiKey, ok := variable("ANTHROPIC_API_KEY"); ok {
		k = foundKey(apiKey)
	} else if helper, ok := jsonStr(settings, "apiKeyHelper"); ok {
		k = key{keyCommand, helper}
	}
	address, hasAddress := variable("ANTHROPIC_BASE_URL")
	if !hasAddress && k.kind == keyMissing {
		// Signed in to Claude, or nothing set up here.
		return nil, nil, nil
	}
	if !hasAddress {
		address = officialAnthropic
	}
	var models []string
	for _, pair := range clients.ClaudeModelKeys {
		if name, ok := strings.CutPrefix(pair[1], "env."); ok {
			if model, ok := variable(name); ok {
				models = append(models, model)
			}
		}
	}
	if model, ok := variable("ANTHROPIC_SMALL_FAST_MODEL"); ok {
		models = append(models, model)
	}
	for i, model := range models {
		// `[1m]` selects Claude Code's long context; it is not in the
		// model id.
		models[i] = strings.TrimSuffix(model, "[1m]")
	}
	return []entry{{address: address, protocol: "anthropic.messages", models: models, key: k}}, nil, nil
}

// asciiLower lowercases ASCII letters only, as Rust's to_ascii_lowercase.
func asciiLower(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

// codexAuth is the part of Codex's auth.json an import may use.
type codexAuth struct {
	apiKey *string
	// chatgpt is whether ChatGPT sign-in tokens are there; they are never
	// read beyond that.
	chatgpt bool
}

func readCodexAuth(text string) (*codexAuth, error) {
	if strings.Trim(text, " \t\n\r") == "" {
		// serde_json stops at the end of the text.
		line := strings.Count(text, "\n") + 1
		column := utf8.RuneCountInString(text[strings.LastIndex(text, "\n")+1:])
		return nil, fmt.Errorf("is not valid JSON (line %d, column %d)", line, column)
	}
	document, err := format.ParseJSON(text, format.JSONFormat{})
	if err != nil {
		var invalid *format.Invalid
		if errors.As(err, &invalid) {
			return nil, errors.New(invalid.Message)
		}
		return nil, errors.New("is not valid JSON")
	}
	value, _ := document.Value()
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("is not a JSON object")
	}
	auth := &codexAuth{chatgpt: object["tokens"] != nil}
	switch apiKey := object["OPENAI_API_KEY"].(type) {
	case nil:
	case string:
		auth.apiKey = &apiKey
	default:
		return nil, errors.New("is not valid JSON")
	}
	return auth, nil
}

// codexEntries reads each provider in Codex's config.toml, and OpenAI
// when auth.json holds an API key. A ChatGPT sign-in is left to
// subscription accounts.
func codexEntries(config *format.TOMLDocument, auth *codexAuth, env lookupEnv) ([]entry, []skip) {
	var entries []entry
	var skipped []skip
	chatgpt := auth != nil && auth.chatgpt
	if chatgpt {
		skipped = append(skipped, skip{reason: SkipChatGPTLogin})
	}
	var apiKey *string
	if auth != nil && !chatgpt && auth.apiKey != nil {
		if value, ok := trimmed(*auth.apiKey, true); ok {
			apiKey = &value
		}
	}
	var root *format.TOMLTable
	if config != nil {
		root = config.Root()
	}
	models := codexModels(root)
	modelsFor := func(id string) []string {
		var ids []string
		for _, pair := range models {
			if pair[0] == id {
				ids = append(ids, pair[1])
			}
		}
		return ids
	}
	openaiAddress, ok := tomlStr(root, "openai_base_url")
	if !ok {
		openaiAddress = officialOpenAI
	}

	configuredOpenAI := false
	var providers *format.TOMLTable
	if root != nil {
		providers = format.TableLike(root.Get("model_providers"))
	}
	var ids []string
	if providers != nil {
		ids = providers.Names()
	}
	for _, id := range ids {
		table := format.TableLike(providers.Get(id))
		if table == nil {
			continue
		}
		name, ok := tomlStr(table, "name")
		if !ok {
			name = id
		}
		if id == clients.CodexProvider {
			skipped = append(skipped, named(SkipAstrLink, &id))
			continue
		}
		if table.ContainsKey("aws") {
			skipped = append(skipped, named(SkipUnsupported, &name))
			continue
		}
		configuredOpenAI = configuredOpenAI || id == "openai"
		command, hasCommand := tomlStr(format.TableLike(table.Get("auth")), "command")
		var k key
		if token, ok := tomlStr(table, "experimental_bearer_token"); ok {
			k = foundKey(token)
		} else if variable, ok := tomlStr(table, "env_key"); ok {
			k = envKey(variable, env)
		} else if hasCommand {
			k = key{keyCommand, command}
		} else if (id == "openai" || tomlTrue(table, "requires_openai_auth")) && apiKey != nil {
			k = foundKey(*apiKey)
		}
		fallback := officialOpenAI
		if id == "openai" {
			fallback = openaiAddress
		}
		address, ok := tomlStr(table, "base_url")
		if !ok {
			address = fallback
		}
		protocol := "openai.responses"
		if wire, _ := tomlStr(table, "wire_api"); wire == "chat" {
			protocol = "openai.chat"
		}
		entries = append(entries, entry{name: &name, address: address, protocol: protocol, models: modelsFor(id), key: k})
	}
	if !configuredOpenAI && apiKey != nil {
		name := "OpenAI"
		entries = append(entries, entry{name: &name, address: openaiAddress, protocol: "openai.responses", models: modelsFor("openai"), key: foundKey(*apiKey)})
	}
	return entries, skipped
}

// codexModels is (provider, model) for the default model and each
// profile's.
func codexModels(root *format.TOMLTable) [][2]string {
	if root == nil {
		return nil
	}
	defaultProvider, ok := tomlStr(root, "model_provider")
	if !ok {
		defaultProvider = "openai"
	}
	var models [][2]string
	if model, ok := tomlStr(root, "model"); ok {
		models = append(models, [2]string{defaultProvider, model})
	}
	profiles := format.TableLike(root.Get("profiles"))
	if profiles == nil {
		return models
	}
	for _, name := range profiles.Names() {
		profile := format.TableLike(profiles.Get(name))
		if profile == nil {
			continue
		}
		if model, ok := tomlStr(profile, "model"); ok {
			provider, ok := tomlStr(profile, "model_provider")
			if !ok {
				provider = defaultProvider
			}
			models = append(models, [2]string{provider, model})
		}
	}
	return models
}

// piProtocol is the Core protocol of one of Pi's API formats AstrLink can
// serve.
func piProtocol(api string) (string, bool) {
	switch api {
	case "openai-completions":
		return "openai.chat", true
	case "openai-responses":
		return "openai.responses", true
	case "anthropic-messages":
		return "anthropic.messages", true
	case "google-generative-ai":
		return "google.generate_content", true
	}
	return "", false
}

// piEntries reads each provider in Pi's models.json; models with their
// own address or format become entries of their own.
func piEntries(text string, env lookupEnv) ([]entry, []skip, error) {
	parsed, err := clients.ReadPiModels(text)
	if err != nil {
		return nil, nil, err
	}
	config, _ := parsed.(map[string]any)
	providers, _ := config["providers"].(map[string]any)
	var entries []entry
	var skipped []skip
	for _, id := range sortedKeys(providers) {
		table, ok := providers[id].(map[string]any)
		if !ok {
			continue
		}
		name, ok := jsonStr(table, "name")
		if !ok {
			name = id
		}
		if id == clients.PiProvider {
			skipped = append(skipped, named(SkipAstrLink, &id))
			continue
		}
		if _, ok := table["oauth"]; ok {
			skipped = append(skipped, named(SkipSignIn, &name))
			continue
		}
		var k key
		if value, ok := jsonStr(table, "apiKey"); ok {
			k = piKey(value, env)
		}
		address, hasAddress := jsonStr(table, "baseUrl")
		api, hasAPI := jsonStr(table, "api")
		type group struct {
			at, speaks       string
			hasAt, hasSpeaks bool
			models           []string
		}
		var groups []*group
		list, _ := table["models"].([]any)
		for _, item := range list {
			model, _ := item.(map[string]any)
			id, ok := jsonStr(model, "id")
			if !ok {
				continue
			}
			at, hasAt := jsonStr(model, "baseUrl")
			if !hasAt {
				at, hasAt = address, hasAddress
			}
			speaks, hasSpeaks := jsonStr(model, "api")
			if !hasSpeaks {
				speaks, hasSpeaks = api, hasAPI
			}
			index := slices.IndexFunc(groups, func(g *group) bool {
				return g.hasAt == hasAt && g.at == at && g.hasSpeaks == hasSpeaks && g.speaks == speaks
			})
			if index < 0 {
				groups = append(groups, &group{at: at, speaks: speaks, hasAt: hasAt, hasSpeaks: hasSpeaks})
				index = len(groups) - 1
			}
			groups[index].models = append(groups[index].models, id)
		}
		if len(groups) == 0 {
			groups = append(groups, &group{at: address, speaks: api, hasAt: hasAddress, hasSpeaks: hasAPI})
		}
		for _, g := range groups {
			// Without an address it only adjusts one of Pi's own providers.
			if !g.hasAt {
				continue
			}
			var protocol string
			if g.hasSpeaks {
				var ok bool
				if protocol, ok = piProtocol(g.speaks); !ok {
					unsupported := named(SkipUnsupported, &name)
					if len(g.speaks) <= 64 && envName(strings.ReplaceAll(g.speaks, "-", "_")) {
						unsupported.detail = &g.speaks
					}
					skipped = append(skipped, unsupported)
					continue
				}
			}
			entries = append(entries, entry{name: &name, address: g.at, protocol: protocol, models: g.models, key: k})
		}
	}
	return entries, skipped, nil
}

// piKey resolves Pi's `apiKey` as Pi does, without running anything: `!`
// runs a command; `$NAME`, `${NAME}` read the environment; `$$` and `$!`
// escape. Before Pi 0.77 a bare variable name also read the environment.
func piKey(value string, env lookupEnv) key {
	if strings.HasPrefix(value, "!") {
		return key{keyCommand, value}
	}
	bareName := value != "" && value[0] >= 'A' && value[0] <= 'Z' && strings.Contains(value, "_") &&
		!strings.HasSuffix(value, "_") && !strings.Contains(value, "__") &&
		strings.IndexFunc(value, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') }) < 0
	if bareName {
		return envKey(value, env)
	}
	var resolved strings.Builder
	missing := ""
	lookup := func(name string) {
		if found, ok := env(name); ok {
			resolved.WriteString(found)
		} else if missing == "" {
			missing = name
		}
	}
	for index := 0; index < len(value); {
		offset := strings.IndexByte(value[index:], '$')
		if offset < 0 {
			resolved.WriteString(value[index:])
			break
		}
		dollar := index + offset
		resolved.WriteString(value[index:dollar])
		rest := value[dollar+1:]
		switch {
		case strings.HasPrefix(rest, "$") || strings.HasPrefix(rest, "!"):
			resolved.WriteString(rest[:1])
			index = dollar + 2
		case strings.HasPrefix(rest, "{"):
			braced := rest[1:]
			end := strings.IndexByte(braced, '}')
			switch {
			case end >= 0 && envName(braced[:end]):
				lookup(braced[:end])
				index = dollar + end + 3
			case end >= 0:
				resolved.WriteString(value[dollar : dollar+end+3])
				index = dollar + end + 3
			default:
				resolved.WriteByte('$')
				index = dollar + 1
			}
		default:
			length := 0
			for length < len(rest) && (isASCIIAlpha(rest[length]) || rest[length] == '_' || length > 0 && rest[length] >= '0' && rest[length] <= '9') {
				length++
			}
			if length > 0 {
				lookup(rest[:length])
				index = dollar + 1 + length
			} else {
				resolved.WriteByte('$')
				index = dollar + 1
			}
		}
	}
	switch text := trim(resolved.String()); {
	case missing != "":
		return key{keyEnv, missing}
	case text == "":
		return key{}
	default:
		return foundKey(text)
	}
}

// authorizeLocal checks that a request goes to the provider's own origin
// with an API-key sign-in, then attaches the key it was configured with. A
// provider without a readable key takes the one the user pasted; one with
// a key takes no other. It returns the request and the key it carries.
func authorizeLocal(p *provider, input json.RawMessage, allowed []string) (json.RawMessage, string, error) {
	invalid := errors.New("invalid service input")
	value, err := format.DecodeJSON(string(input))
	if err != nil {
		return nil, "", invalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, "", invalid
	}
	for name := range object {
		if !slices.Contains(allowed, name) {
			return nil, "", invalid
		}
	}
	kind, ok := object["kind"].(string)
	if !ok {
		return nil, "", invalid
	}
	if strings.HasSuffix(kind, "_subscription") || kind == "vertex" {
		return nil, "", errors.New("this service type cannot use an imported key")
	}
	httpObject, ok := object["http"].(map[string]any)
	if !ok {
		return nil, "", invalid
	}
	for name := range httpObject {
		if !slices.Contains([]string{"base_url", "auth", "credential"}, name) {
			return nil, "", invalid
		}
	}
	baseURL, ok := httpObject["base_url"].(string)
	if !ok {
		return nil, "", invalid
	}
	if !ValidBaseURL(baseURL) {
		return nil, "", errors.New("the service address does not match the imported provider")
	}
	if u, err := url.Parse(baseURL); err != nil || origin(u) != p.origin {
		return nil, "", errors.New("the service address does not match the imported provider")
	}
	auth, ok := httpObject["auth"].(map[string]any)
	if !ok {
		return nil, "", invalid
	}
	scheme, ok := auth["scheme"].(string)
	if !ok {
		return nil, "", invalid
	}
	if len(auth) != 1 || !slices.Contains(localAuthSchemes, scheme) {
		return nil, "", errors.New("this sign-in method cannot use an imported key")
	}
	var secret string
	if p.key.kind == keyFound {
		if _, ok := httpObject["credential"]; ok {
			return nil, "", errors.New("the imported key cannot be replaced")
		}
		secret = p.key.value
		httpObject["credential"] = map[string]any{"secret": secret}
	} else {
		credential, ok := httpObject["credential"].(map[string]any)
		if !ok {
			return nil, "", errors.New("paste the key first")
		}
		secret, ok = credential["secret"].(string)
		if !ok || trim(secret) == "" {
			return nil, "", errors.New("paste the key first")
		}
		if len(credential) != 1 || len(secret) > maxSecretBytes || !PlainText(secret) {
			return nil, "", errors.New("invalid key")
		}
	}
	return json.RawMessage(format.Encode(value)), secret, nil
}
