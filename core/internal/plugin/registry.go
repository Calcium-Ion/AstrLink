package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MoeForge/moejs"

	"github.com/QuantumNous/astrlink/core/contract"
)

// MaxConfigBytes bounds a plugin's configuration.
const MaxConfigBytes = 64 << 10

// Settings are the user's choices for one plugin, kept in the gateway
// settings.
type Settings struct {
	Enabled bool
	// Capabilities are the ones the user granted. Only those the manifest
	// still asks for are in effect.
	Capabilities []string
	// Config is the plugin's configuration: a JSON object of at most
	// MaxConfigBytes, which the plugin reads and cannot change.
	Config json.RawMessage
	// Origins are the addresses http.fetch may reach.
	Origins []NetworkOrigin
	// Trusted skips the privacy check of the text the host hands the
	// plugin, as a trusted provider skips it.
	Trusted bool
	// TurnWait is how long a request waits for the plugin's prepare hooks;
	// zero is the default.
	TurnWait time.Duration
	// ExcludedClients are the client types the plugin is off for.
	ExcludedClients []string
	// WorkspaceBinding binds a new session to the value of the plugin's
	// attributes its workspace had.
	WorkspaceBinding bool
	// AutoRefresh lets the host check a session's resident block for newer
	// data now and then.
	AutoRefresh bool
	// LookupEnabled lets the model run the plugin's queries, when the user
	// granted model.tools.
	LookupEnabled bool
	// LimitsMs are the user's time limits of extension points, in
	// milliseconds by hook; a hook without one follows the plugin.
	LimitsMs map[string]int
	// FailClosed is the user's consent that a request its entry hook cannot
	// decide is refused, when the plugin asks for that.
	FailClosed bool
	// UIEnabled lets the plugin's interface code load.
	UIEnabled bool
}

// Limit returns how long a request waits for one call of hook: what the user
// set, else what the plugin asks for, else the host's default, at most the
// host's bound. It is 0 for a hook without a limit of its own.
func (plugin *Plugin) Limit(hook string) time.Duration {
	bounds, known := contract.PluginHookLimits[hook]
	if !known {
		return 0
	}
	ms := bounds.DefaultMs
	if asked := plugin.Meta.Limits[hook]; asked > 0 {
		ms = asked
	}
	if set := plugin.Settings.LimitsMs[hook]; set > 0 {
		ms = set
	}
	return time.Duration(min(ms, bounds.MaxMs)) * time.Millisecond
}

// Config is the configuration the plugin reads: the user's, with the
// defaults of the fields the user left unset. Nil for a plugin that did
// not load.
func (plugin *Plugin) Config() json.RawMessage { return plugin.config }

// UILoads reports whether the plugin's interface code loads: it has some,
// it is enabled with ui.code granted, and the user did not turn its
// interface off.
func (plugin *Plugin) UILoads() bool {
	return plugin.Client && plugin.Enabled() && plugin.Can(CapabilityUICode) && plugin.Settings.UIEnabled
}

// FailsClosed reports whether a request is refused when the plugin's entry
// hook cannot decide it: the plugin asks for it and the user agreed.
func (plugin *Plugin) FailsClosed() bool {
	return plugin.Meta.FailClosed && plugin.Settings.FailClosed && plugin.Can(CapabilityRequestGate)
}

// Excludes reports whether the plugin is off for requests of client.
func (settings Settings) Excludes(client string) bool {
	return slices.Contains(settings.ExcludedClients, client)
}

// Wait returns how long a request waits for the plugin's prepare hooks,
// within the bounds the plugin page allows.
func (settings Settings) Wait() time.Duration {
	wait := settings.TurnWait
	if wait == 0 {
		wait = contract.DefaultPluginTurnWaitMs * time.Millisecond
	}
	return min(max(wait, contract.MinPluginTurnWaitMs*time.Millisecond), contract.MaxPluginTurnWaitMs*time.Millisecond)
}

// NetworkOrigin is an address a plugin may reach: scheme, host and port,
// any path. Origin is in the form NormalizeOrigin returns.
type NetworkOrigin struct {
	Origin string
	// AllowPlaintext allows plain http to a host other than this machine.
	AllowPlaintext bool
}

// NormalizeOrigin returns the origin of an absolute http or https URL as
// scheme://host:port, the host in lowercase and the default port spelt out,
// so that two spellings of one address compare equal.
func NormalizeOrigin(raw string) (string, error) {
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil {
		return "", fmt.Errorf("%q is not an absolute http or https address", raw)
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	port := target.Port()
	if port == "" {
		port = "443"
		if target.Scheme == "http" {
			port = "80"
		}
	}
	if host == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	return target.Scheme + "://" + net.JoinHostPort(host, port), nil
}

// Plaintext reports whether a normalized origin is plain http to a host other
// than this machine, which a plugin reaches only when the user allows it for
// that origin on its own.
func Plaintext(origin string) bool {
	rest, plain := strings.CutPrefix(origin, "http://")
	if !plain {
		return false
	}
	host, _, err := net.SplitHostPort(rest)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	address := net.ParseIP(host)
	return address == nil || !address.IsLoopback()
}

// SettingsSource reads the settings of every plugin by key. A plugin without
// settings is disabled and granted nothing.
type SettingsSource interface {
	PluginSettings(ctx context.Context) (map[string]Settings, error)
}

// Plugin is one plugin of a generation: where its source came from, how it
// loaded, the user's settings for it, and the runtimes that run it.
type Plugin struct {
	Key    string
	Origin Origin
	File   string
	// Icon is the plugin's icon as a data URL, "" for none.
	Icon string
	// Development is set for a plugin astrlink plugin dev keeps built.
	Development *Development
	// Installed is set for a plugin installed from source.
	Installed *contract.PluginInstallRecord
	// Client is set for a plugin with interface code; ClientHash names
	// the code.
	Client     bool
	ClientHash string
	// Meta is the manifest, zero when the plugin did not load.
	Meta Meta
	// Err is why the plugin did not load, nil when it did.
	Err      *LoadError
	Settings Settings

	granted []string
	// config is the configuration the plugin reads: the user's, with the
	// defaults of the settings it has no value for.
	config   json.RawMessage
	program  *program
	pool     *pool
	services *Services
	counter  *counter
}

// Stats count a plugin's calls since Core started, across generations.
type Stats struct {
	Calls int
	// Failures counts the calls that failed, by CallError reason.
	Failures map[string]int
	Network  NetworkStats
}

type counter struct {
	mutex   sync.Mutex
	stats   Stats
	journal journal
}

func (count *counter) record(info CallInfo, err error) {
	if count == nil {
		return
	}
	count.mutex.Lock()
	defer count.mutex.Unlock()
	count.stats.Calls++
	var failure *CallError
	if errors.As(err, &failure) {
		if count.stats.Failures == nil {
			count.stats.Failures = make(map[string]int)
		}
		count.stats.Failures[failure.Reason]++
	}
	count.stats.Network.Requests += info.Network.Requests
	count.stats.Network.Denied += info.Network.Denied
	count.stats.Network.Errors += info.Network.Errors
	count.stats.Network.Wait += info.Network.Wait
}

// Stats returns what the host counted for the plugin so far.
func (plugin *Plugin) Stats() Stats {
	if plugin.counter == nil {
		return Stats{}
	}
	plugin.counter.mutex.Lock()
	defer plugin.counter.mutex.Unlock()
	stats := plugin.counter.stats
	stats.Failures = maps.Clone(stats.Failures)
	return stats
}

// Loaded reports whether the plugin compiled and passed its checks.
func (plugin *Plugin) Loaded() bool { return plugin.program != nil }

// Enabled reports whether the plugin loaded and the user enabled it.
func (plugin *Plugin) Enabled() bool { return plugin.program != nil && plugin.Settings.Enabled }

// DataFiles counts the files of the plugin's own data directory and what
// they hold; zero for a plugin that has none or did not load.
func (plugin *Plugin) DataFiles() (files int, bytes int64) {
	if plugin.program == nil {
		return 0, 0
	}
	_ = filepath.WalkDir(plugin.program.dirs.data, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				files++
				bytes += info.Size()
			}
		}
		return nil
	})
	return files, bytes
}

// ClearData removes the files of the plugin's own data directory and
// returns how many there were. The directory stays, empty.
func (plugin *Plugin) ClearData() (int, error) {
	if plugin.program == nil {
		return 0, nil
	}
	dirs := plugin.program.dirs
	files, _ := plugin.DataFiles()
	entries, err := os.ReadDir(dirs.data)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dirs.data, entry.Name())); err != nil {
			return 0, err
		}
	}
	_ = os.Mkdir(dirs.tmp(), 0o700)
	dirs.released()
	return files, nil
}

// Granted returns the capabilities in effect.
func (plugin *Plugin) Granted() []string { return slices.Clone(plugin.granted) }

// Can reports whether capability is in effect.
func (plugin *Plugin) Can(capability string) bool { return slices.Contains(plugin.granted, capability) }

// Hooks returns the hooks the plugin exports, in name order.
func (plugin *Plugin) Hooks() []string {
	if plugin.program == nil {
		return nil
	}
	hooks := make([]string, 0, len(plugin.program.hooks))
	for name := range plugin.program.hooks {
		hooks = append(hooks, name)
	}
	slices.Sort(hooks)
	return hooks
}

// Has reports whether the plugin exports hook.
func (plugin *Plugin) Has(hook string) bool {
	if plugin.program == nil {
		return false
	}
	_, exported := plugin.program.hooks[hook]
	return exported
}

// Call runs hook with arg, a Go value or nil for none, and returns the JSON
// text of what the hook returned, nil for undefined. It does not check that
// the plugin is enabled: request hooks run only for enabled plugins, while
// the plugin page may test one first. Any failure is a *CallError; the
// CallInfo is filled either way.
func (plugin *Plugin) Call(ctx context.Context, hook string, arg any, options CallOptions) (json.RawMessage, CallInfo, error) {
	var args []any
	if arg != nil {
		args = []any{arg}
	}
	return plugin.CallArgs(ctx, hook, args, options)
}

// CallArgs runs hook with args, Go values, as Call does.
func (plugin *Plugin) CallArgs(ctx context.Context, hook string, args []any, options CallOptions) (json.RawMessage, CallInfo, error) {
	if plugin.program == nil {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: "the plugin did not load"}
	}
	exported, ok := plugin.program.hooks[hook]
	if !ok {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: fmt.Sprintf("the plugin does not export %s", hook)}
	}
	return plugin.invoke(ctx, hook, exported, args, options)
}

// invoke runs exported, the hook named name, on the plugin's pool.
func (plugin *Plugin) invoke(ctx context.Context, name string, exported moejs.Hook, args []any, options CallOptions) (json.RawMessage, CallInfo, error) {
	started := time.Now()
	// Work a call started on other goroutines ends with it.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errCallEnded)
	c := &call{ctx: ctx, end: cancel, plugin: plugin, hook: name, options: options, pool: plugin.pool, networkLeft: options.NetworkBudget, async: plugin.program.async()}
	if c.async {
		c.networkDeadline = started.Add(options.NetworkBudget)
		c.fetchSlots = make(chan struct{}, maxParallelFetches)
	}
	result, err := plugin.pool.run(ctx, c, exported, args)
	c.info.Elapsed = time.Since(started)
	c.info.Network = c.networkStats()
	if c.watchdog != nil {
		c.info.Script = c.watchdog.used()
	}
	if err != nil {
		failure := classify(err, plugin.pool.limits)
		failure.Message = plugin.program.mapStack(failure.Message)
		plugin.counter.record(c.info, failure)
		plugin.note(name, c.info, failure, options.DryRun)
		return nil, c.info, failure
	}
	plugin.counter.record(c.info, nil)
	plugin.note(name, c.info, nil, options.DryRun)
	return result, c.info, nil
}

// errCallEnded cancels what a call started once the call returned.
var errCallEnded = errors.New("the plugin call ended")

// Checked is a plugin that compiled and passed its checks before it is
// installed: its manifest and the hooks it exports. Task is the manifest of
// a task plugin, whose Meta carries only its key, name and version.
type Checked struct {
	Meta  Meta
	Hooks []string
	Task  *TaskMeta
}

// Check compiles a plugin's source as the host would load it, under the key
// its manifest names, and returns what it declares. Its node:fs reaches
// source.Dir and a data directory of its own, which goes when the check
// ends. Any failure is a *LoadError.
func Check(source Source) (*Checked, error) {
	source.Key = ""
	dirs := temporaryDirs(canonicalDir(source.Dir, false))
	defer dirs.close()
	loaded, err := load(source, DefaultLimits(), dirs)
	if err != nil {
		return nil, asLoadError(source, err)
	}
	checked := &Checked{Meta: loaded.meta, Task: loaded.task}
	for name := range loaded.hooks {
		checked.Hooks = append(checked.Hooks, name)
	}
	slices.Sort(checked.Hooks)
	return checked, nil
}

// Generation is the registry at one moment: every plugin found, loaded or
// not, with the settings in effect. A request keeps the generation it
// started with while a newer one replaces it.
type Generation struct {
	plugins []*Plugin
}

// Plugins returns every plugin in key order. The slice is shared.
func (generation *Generation) Plugins() []*Plugin { return generation.plugins }

// Plugin returns the plugin with key, nil when there is none.
func (generation *Generation) Plugin(key string) *Plugin {
	index, found := slices.BinarySearchFunc(generation.plugins, key, func(plugin *Plugin, key string) int {
		switch {
		case plugin.Key < key:
			return -1
		case plugin.Key > key:
			return 1
		}
		return 0
	})
	if !found {
		return nil
	}
	return generation.plugins[index]
}

// Enabled returns the enabled plugins that export hook, in key order.
func (generation *Generation) Enabled(hook string) []*Plugin {
	var plugins []*Plugin
	for _, plugin := range generation.plugins {
		if plugin.Enabled() && plugin.Has(hook) {
			plugins = append(plugins, plugin)
		}
	}
	return plugins
}

// ManagerOptions configure a Manager.
type ManagerOptions struct {
	// Builtin holds the plugins compiled into Core as <key>/plugin.ts.
	Builtin fs.FS
	// Dir is the plugin directory, <data-dir>/plugins.
	Dir string
	// DataDir holds the plugins' own directories, which their node:fs
	// reads and writes: <data-dir>/plugin-data. Without one, each plugin
	// gets a temporary directory.
	DataDir  string
	Settings SettingsSource
	Services Services
	Limits   Limits
}

// Manager keeps the current generation of plugins. Reload rescans the
// sources and Refresh applies changed settings; both compile only sources
// that changed, and requests read the current generation without waiting
// for either.
type Manager struct {
	builtin fs.FS
	dir     string
	// installRoot and dataRoot are dir and the data directory without
	// symbolic links above them, as node:fs gives paths.
	installRoot string
	dataRoot    string
	settings    SettingsSource
	services    *Services
	limits      Limits

	mutex    sync.Mutex
	sources  []Source
	programs map[[sha256.Size]byte]loadResult
	counters map[string]*counter
	current  atomic.Pointer[Generation]
}

type loadResult struct {
	program *program
	err     *LoadError
}

// NewManager returns a Manager with no plugins until its first Reload.
func NewManager(options ManagerOptions) *Manager {
	services := options.Services
	manager := &Manager{
		builtin:     options.Builtin,
		dir:         options.Dir,
		installRoot: canonicalDir(options.Dir, false),
		dataRoot:    canonicalDir(options.DataDir, true),
		settings:    options.Settings,
		services:    &services,
		limits:      options.Limits.withDefaults(),
		programs:    make(map[[sha256.Size]byte]loadResult),
		counters:    make(map[string]*counter),
	}
	manager.current.Store(&Generation{})
	return manager
}

// dirsFor returns the directories a plugin's node:fs reaches: its own under
// the data directory, and the one its files are in.
func (manager *Manager) dirsFor(source Source) *pluginDirs {
	install := ""
	if source.Origin != OriginBuiltin && manager.installRoot != "" {
		install = filepath.Join(manager.installRoot, source.Key)
	} else if source.Dir != "" {
		install = canonicalDir(source.Dir, false)
	}
	if manager.dataRoot == "" {
		dirs := temporaryDirs(install)
		goruntime.AddCleanup(dirs, func(data string) { _ = os.RemoveAll(data) }, dirs.data)
		return dirs
	}
	return newPluginDirs(filepath.Join(manager.dataRoot, source.Key), install, manager.limits.DataBytes)
}

// Current returns the generation in effect.
func (manager *Manager) Current() *Generation { return manager.current.Load() }

// Dir returns the plugin directory.
func (manager *Manager) Dir() string { return manager.dir }

// Reload rescans the builtin plugins and the plugin directory, compiles the
// sources that changed and publishes a generation with the current settings.
// When the sources or the settings cannot be read, the current generation
// stays.
func (manager *Manager) Reload(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	sources, err := Discover(manager.builtin, manager.dir)
	if err != nil {
		return err
	}
	settings, err := manager.readSettings(ctx)
	if err != nil {
		return err
	}
	programs := make(map[[sha256.Size]byte]loadResult, len(sources))
	for _, source := range sources {
		identity := sourceIdentity(source)
		result, compiled := manager.programs[identity]
		if !compiled {
			loaded, err := load(source, manager.limits, manager.dirsFor(source))
			if err != nil {
				result.err = asLoadError(source, err)
			} else {
				result.program = loaded
			}
		}
		programs[identity] = result
	}
	manager.sources = sources
	manager.programs = programs
	manager.publish(settings)
	return nil
}

// Refresh re-reads the settings and publishes a generation with them. The
// programs stay as they are.
func (manager *Manager) Refresh(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	settings, err := manager.readSettings(ctx)
	if err != nil {
		return err
	}
	manager.publish(settings)
	return nil
}

func (manager *Manager) readSettings(ctx context.Context) (map[string]Settings, error) {
	if manager.settings == nil {
		return nil, nil
	}
	settings, err := manager.settings.PluginSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read plugin settings: %w", err)
	}
	return settings, nil
}

// publish replaces the current generation. A plugin whose program and
// settings did not change keeps its runtimes.
func (manager *Manager) publish(settings map[string]Settings) {
	previous := manager.current.Load()
	plugins := make([]*Plugin, 0, len(manager.sources))
	for _, source := range manager.sources {
		result := manager.programs[sourceIdentity(source)]
		current := settings[source.Key]
		if kept := previous.Plugin(source.Key); kept != nil && kept.Origin == source.Origin && kept.Icon == source.Icon && reflect.DeepEqual(kept.Development, source.Development) && reflect.DeepEqual(kept.Installed, source.Installed) && kept.ClientHash == source.ClientHash && kept.program == result.program && kept.Err == result.err && reflect.DeepEqual(kept.Settings, current) {
			plugins = append(plugins, kept)
			continue
		}
		count := manager.counters[source.Key]
		if count == nil {
			count = &counter{}
			manager.counters[source.Key] = count
		}
		plugin := &Plugin{Key: source.Key, Origin: source.Origin, File: source.File, Icon: source.Icon, Development: source.Development, Installed: source.Installed, Client: source.Client, ClientHash: source.ClientHash, Settings: current, services: manager.services, counter: count}
		if result.program == nil {
			plugin.Err = result.err
		} else {
			plugin.program = result.program
			plugin.Meta = result.program.meta
			// A plugin has every capability it declares: the install review
			// lists them, and enabling the plugin grants them all.
			plugin.granted = slices.Clone(result.program.meta.Capabilities)
			plugin.config = result.program.meta.WithDefaults(current.Config)
			limits := manager.limits
			if len(result.program.meta.Providers) > 0 {
				limits = providerLimits(limits)
			}
			if result.program.task != nil {
				limits = taskLimits(limits)
			}
			plugin.pool = newPool(result.program, limits)
		}
		plugins = append(plugins, plugin)
	}
	manager.current.Store(&Generation{plugins: plugins})
}

// WatchDevelopment reloads the plugins when one that astrlink plugin dev
// keeps built changes on disk, checking every interval until ctx ends.
// reloaded runs after each such reload.
func (manager *Manager) WatchDevelopment(ctx context.Context, interval time.Duration, reloaded func()) {
	if manager.dir == "" {
		return
	}
	last := developmentFingerprint(manager.dir)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		current := developmentFingerprint(manager.dir)
		if current == last {
			continue
		}
		last = current
		if err := manager.Reload(ctx); err == nil && reloaded != nil {
			reloaded()
		}
	}
}

// developmentFingerprint sums the names, sizes and times of the files of
// the plugins in dir that astrlink plugin dev keeps built.
func developmentFingerprint(dir string) [sha256.Size]byte {
	hash := sha256.New()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !ValidPluginKey(entry.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), DevelopmentMarker)); err != nil {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(dir, entry.Name()))
		for _, file := range files {
			info, err := file.Info()
			if err != nil || info.IsDir() {
				continue
			}
			fmt.Fprintf(hash, "%s/%s\x00%d\x00%d\x00", entry.Name(), file.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	var sum [sha256.Size]byte
	hash.Sum(sum[:0])
	return sum
}

// sourceIdentity tells whether a source changed since its last compile.
func sourceIdentity(source Source) [sha256.Size]byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", source.Key, source.Origin, source.File)
	if source.Err != nil {
		fmt.Fprintf(hash, "error\x00%s\x00%s", source.Err.Code, source.Err.Message)
	} else {
		hash.Write([]byte(source.Text))
	}
	var identity [sha256.Size]byte
	hash.Sum(identity[:0])
	return identity
}

func asLoadError(source Source, err error) *LoadError {
	if loadErr, ok := err.(*LoadError); ok {
		if loadErr.File == "" {
			loadErr.File = source.File
		}
		return loadErr
	}
	return &LoadError{Code: CodeLoadFailed, File: source.File, Message: cleanText(err.Error(), maxMessageRunes, "")}
}

// Attributes returns the metadata attributes a plugin registers, as the
// metadata registry keeps them.
func (plugin *Plugin) Attributes() []contract.MetadataAttribute {
	attributes := make([]contract.MetadataAttribute, 0, len(plugin.Meta.Metadata))
	for _, attribute := range plugin.Meta.Metadata {
		attributes = append(attributes, contract.MetadataAttribute{
			Key: attribute.Key, Label: attribute.Label, Pattern: attribute.Pattern, Marker: attribute.Marker, Owner: plugin.Key,
		})
	}
	return attributes
}
