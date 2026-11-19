package plugincli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// Intervals of astrlink plugin dev.
var (
	watchInterval   = 500 * time.Millisecond
	journalInterval = time.Second
)

// iconFiles are the icons a project may keep at its root, copied beside the
// build.
var iconFiles = []string{"icon.svg", "icon.png"}

func runDev(ctx context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("dev", flag.ContinueOnError)
	force := flags.Bool("force", false, "replace a plugin of the same key that was installed otherwise")
	once := flags.Bool("once", false, "build and install once, then exit")
	positional, ok := parse(env, "dev", flags, args, 1)
	if !ok {
		return 2
	}
	dir, err := filepath.Abs(projectDir(positional))
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if env.Dial == nil {
		fmt.Fprintln(env.Stderr, "error: the gateway is not reachable")
		return 1
	}
	gateway, err := env.Dial()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: reach the gateway: %v\n", err)
		return 1
	}
	pluginsDir, err := pluginDirectory(ctx, gateway)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	session := &devSession{env: env, gateway: gateway, project: dir, pluginsDir: pluginsDir, force: *force}
	if !session.rebuild(ctx) && *once {
		return 1
	}
	if *once {
		return 0
	}
	fmt.Fprintf(env.Stderr, "watching %s; press Ctrl+C to stop (the build stays installed)\n", dir)
	session.loop(ctx)
	return 0
}

// pluginDirectory asks the gateway where it keeps plugins.
func pluginDirectory(ctx context.Context, gateway Gateway) (string, error) {
	raw, err := gateway.Get(ctx, "/control/v1/plugins", nil)
	if err != nil {
		return "", fmt.Errorf("read the gateway's plugins: %w", err)
	}
	var list struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || list.Directory == "" {
		return "", errors.New("the gateway did not say where it keeps plugins")
	}
	return list.Directory, nil
}

// devSession keeps one project built into the gateway.
type devSession struct {
	env        Environment
	gateway    Gateway
	project    string
	pluginsDir string
	force      bool
	key        string
	// inputs are the files the last build read, with their times.
	inputs map[string]time.Time
	since  uint64
}

func (session *devSession) loop(ctx context.Context) {
	watch := time.NewTicker(watchInterval)
	defer watch.Stop()
	tail := time.NewTicker(journalInterval)
	defer tail.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
			if session.changed() {
				session.rebuild(ctx)
			}
		case <-tail.C:
			session.tail(ctx)
		}
	}
}

// rebuild builds the project and installs it, reporting either way.
func (session *devSession) rebuild(ctx context.Context) bool {
	started := time.Now()
	project, ok := buildProject(session.env, session.project)
	session.snapshot(project)
	if !ok {
		fmt.Fprintln(session.env.Stderr, "build failed; waiting for changes")
		return false
	}
	key := project.checked.Meta.Key
	if session.key != "" && key != session.key {
		fmt.Fprintf(session.env.Stderr, "error: meta.key changed from %s to %s; restart astrlink plugin dev\n", session.key, key)
		return false
	}
	if err := session.install(project); err != nil {
		fmt.Fprintf(session.env.Stderr, "error: install the build: %v\n", err)
		return false
	}
	if session.key == "" {
		session.key = key
		session.since = session.latest(ctx)
	}
	fmt.Fprintf(session.env.Stderr, "%s built and installed in %s (%s)\n", key, time.Since(started).Round(time.Millisecond), time.Now().Format("15:04:05"))
	return true
}

// install writes a build into the gateway's plugin directory, which the
// gateway reloads within a second.
func (session *devSession) install(project *built) error {
	target := filepath.Join(session.pluginsDir, project.checked.Meta.Key)
	if _, err := os.Stat(target); err == nil {
		if _, markerErr := os.Stat(filepath.Join(target, plugin.DevelopmentMarker)); markerErr != nil && !session.force {
			return fmt.Errorf("%s holds a plugin installed otherwise; pass --force to replace it", target)
		}
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	// The source map and interface go first, and the marker last: the
	// gateway reloads once it sees the plugin's files settle.
	if project.result.Client == nil {
		for _, name := range []string{"client.js", "client.js.map"} {
			_ = os.Remove(filepath.Join(target, name))
		}
	}
	if _, err := writeOutputs(target, project.result); err != nil {
		return err
	}
	for _, name := range iconFiles {
		data, err := os.ReadFile(filepath.Join(session.project, name))
		if err == nil {
			if err := os.WriteFile(filepath.Join(target, name), data, 0o644); err != nil {
				return err
			}
		}
	}
	marker, err := json.Marshal(plugin.Development{Project: session.project, BuiltAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(target, plugin.DevelopmentMarker), marker, 0o644)
}

// snapshot remembers the times of the files a build read, and of
// package.json, to rebuild when one changes.
func (session *devSession) snapshot(project *built) {
	inputs := map[string]time.Time{}
	names := []string{"package.json", "tsconfig.json"}
	names = append(names, iconFiles...)
	if project != nil {
		names = append(names, project.result.Inputs...)
	}
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(session.project, filepath.FromSlash(name))); err == nil {
			inputs[name] = info.ModTime()
		}
	}
	if project == nil && session.inputs != nil {
		// A build that failed read no list of inputs: keep watching the
		// last good one, which holds the file being fixed.
		for name := range session.inputs {
			if info, err := os.Stat(filepath.Join(session.project, filepath.FromSlash(name))); err == nil {
				inputs[name] = info.ModTime()
			}
		}
	}
	if project == nil && len(session.inputs) == 0 {
		// Nothing built yet: watch every source file of the project.
		_ = filepath.WalkDir(session.project, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == "dist" || strings.HasPrefix(entry.Name(), ".")) && path != session.project {
				return filepath.SkipDir
			}
			if !entry.IsDir() && sourceFile(entry.Name()) {
				if info, err := entry.Info(); err == nil {
					rel, _ := filepath.Rel(session.project, path)
					inputs[filepath.ToSlash(rel)] = info.ModTime()
				}
			}
			return nil
		})
	}
	session.inputs = inputs
}

func sourceFile(name string) bool {
	for _, suffix := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func (session *devSession) changed() bool {
	for name, seen := range session.inputs {
		info, err := os.Stat(filepath.Join(session.project, filepath.FromSlash(name)))
		if err != nil || !info.ModTime().Equal(seen) {
			return true
		}
	}
	return false
}

// latest is the journal's newest entry now, so that dev shows only what
// comes after it starts.
func (session *devSession) latest(ctx context.Context) uint64 {
	journal, err := session.journal(ctx, 0)
	if err != nil {
		return 0
	}
	return journal.Latest
}

func (session *devSession) journal(ctx context.Context, since uint64) (contract.PluginJournal, error) {
	query := url.Values{"since": {strconv.FormatUint(since, 10)}}
	raw, err := session.gateway.Get(ctx, "/control/v1/plugins/"+session.key+"/journal", query)
	if err != nil {
		return contract.PluginJournal{}, err
	}
	var journal contract.PluginJournal
	err = json.Unmarshal(raw, &journal)
	return journal, err
}

// tail prints the journal entries since the last ones printed.
func (session *devSession) tail(ctx context.Context) {
	if session.key == "" {
		return
	}
	journal, err := session.journal(ctx, session.since)
	if err != nil {
		return
	}
	for _, entry := range journal.Entries {
		printEntry(session.env.Stderr, entry)
	}
	session.since = journal.Latest
}

func printEntry(output io.Writer, entry contract.PluginJournalEntry) {
	at := entry.Time
	if parsed, err := time.Parse(time.RFC3339Nano, entry.Time); err == nil {
		at = parsed.Local().Format("15:04:05")
	}
	dry := ""
	if entry.DryRun {
		dry = " (dry run)"
	}
	fmt.Fprintf(output, "%s %-5s %s%s: %s\n", at, entry.Level, entry.Hook, dry, entry.Message)
	if entry.Stack != "" {
		fmt.Fprintf(output, "      %s\n", strings.ReplaceAll(entry.Stack, "\n", "\n      "))
	}
}
