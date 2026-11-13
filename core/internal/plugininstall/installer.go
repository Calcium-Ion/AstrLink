package plugininstall

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// Where the installer keeps its own files in the plugin directory, under
// names no plugin key can have.
const (
	stagingDir  = ".install"
	versionsDir = ".versions"
)

const (
	// stagedFor is how long a review's build waits to be installed.
	stagedFor = 30 * time.Minute
	// maxStaged bounds the builds waiting.
	maxStaged = 4
	// keptVersions bounds the replaced versions kept of each plugin.
	keptVersions = 3
	// previewTimeout bounds fetching, building and checking a plugin.
	previewTimeout = 3 * time.Minute
	// maxReviewWarnings bounds the bundler's warnings a review lists.
	maxReviewWarnings = 10
)

// Options configure an Installer.
type Options struct {
	// Dir is the plugin directory installs write into.
	Dir string
	// Client fetches from GitHub and the registries; the gateway's
	// outbound proxy applies.
	Client *http.Client
	// GitHub, GitHubAPI and Codeload default to GitHub's.
	GitHub    string
	GitHubAPI string
	Codeload  string
	// Registries returns the npm registries to try, in order.
	Registries func() []string
	// Version is this AstrLink's version, which engines.astrlink must
	// allow; a development build allows any.
	Version string
	// Language is the plugin page's language, which a review describes a
	// task plugin in; English without it.
	Language func() string
	// Now defaults to time.Now.
	Now func() time.Time
}

// Existing is the plugin a key names now, for a review to compare with.
type Existing struct {
	// Origin is builtin, directory or override.
	Origin string
	// Loaded is false for a plugin that did not load, whose manifest is
	// unknown.
	Loaded         bool
	Name           string
	Version        string
	Capabilities   []string
	NetworkPurpose string
	Client         bool
	Development    bool
	Installed      *contract.PluginInstallRecord
}

// Installer fetches, builds and installs plugins from source.
type Installer struct {
	options Options
	hub     *gitHub
	// busy lets one build run at a time.
	busy sync.Mutex
	// mutex guards staged and the moves in the plugin directory.
	mutex  sync.Mutex
	staged map[string]*staged
	// icons and market keep LobeHub's icons and marketplace listings.
	icons  lobeIcons
	market marketCache
}

// staged is a build waiting to be installed.
type staged struct {
	review  contract.PluginInstallReview
	record  contract.PluginInstallRecord
	dir     string
	expires time.Time
}

// New returns an installer for options.Dir, dropping what an earlier run
// left staged.
func New(options Options) *Installer {
	if options.Client == nil {
		options.Client = &http.Client{}
	}
	if options.GitHub == "" {
		options.GitHub = DefaultGitHub
	}
	if options.GitHubAPI == "" {
		options.GitHubAPI = DefaultGitHubAPI
	}
	if options.Codeload == "" {
		options.Codeload = DefaultCodeload
	}
	if options.Registries == nil {
		options.Registries = func() []string {
			return []string{contract.DefaultNpmRegistry, contract.FallbackNpmRegistry}
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Dir != "" {
		_ = os.RemoveAll(filepath.Join(options.Dir, stagingDir))
	}
	return &Installer{
		options: options,
		hub: &gitHub{
			client: options.Client, web: strings.TrimRight(options.GitHub, "/"),
			api: strings.TrimRight(options.GitHubAPI, "/"), codeload: strings.TrimRight(options.Codeload, "/"),
		},
		staged: map[string]*staged{},
	}
}

// Preview fetches the plugin a link names, builds it and checks it, and
// stages the build for Install. existing describes the plugin a key names
// now, nil for none. A *Failure says why the plugin cannot be installed.
func (installer *Installer) Preview(ctx context.Context, link string, existing func(key string) *Existing) (contract.PluginInstallReview, error) {
	parsed, ok := ParseLink(link)
	market, isMarket := parseMarketLink(link)
	if !ok && !isMarket {
		return contract.PluginInstallReview{}, fail(contract.PluginInstallInvalidLink, "")
	}
	if !installer.busy.TryLock() {
		return contract.PluginInstallReview{}, fail(contract.PluginInstallBusy, "")
	}
	defer installer.busy.Unlock()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	installer.expire()
	token := randomHex(16)
	work := filepath.Join(installer.options.Dir, stagingDir, token)
	var review contract.PluginInstallReview
	var record contract.PluginInstallRecord
	var err error
	if ok {
		var resolved Resolved
		if resolved, err = installer.hub.resolve(ctx, parsed); err != nil {
			return contract.PluginInstallReview{}, installer.failure(ctx, err)
		}
		review, record, err = installer.build(ctx, resolved, strings.TrimSpace(link), work, existing)
	} else {
		review, record, err = installer.previewMarket(ctx, market, strings.TrimSpace(link), work, existing)
	}
	if err != nil {
		_ = os.RemoveAll(work)
		return contract.PluginInstallReview{}, installer.failure(ctx, err)
	}
	review.Token = token
	expires := installer.options.Now().Add(stagedFor)
	review.ExpiresAt = expires.UTC().Format(time.RFC3339)
	installer.mutex.Lock()
	installer.staged[token] = &staged{review: review, record: record, dir: work, expires: expires}
	installer.mutex.Unlock()
	return review, nil
}

// failure is what a build that stopped says: the run out of time counts
// as the source unavailable.
func (installer *Installer) failure(ctx context.Context, err error) error {
	var failure *Failure
	switch {
	case errors.As(err, &failure):
		return failure
	case ctx.Err() != nil:
		return fail(contract.PluginInstallUnavailable, "timed out")
	}
	return err
}

// build fetches resolved's source into work and builds it into work/out.
func (installer *Installer) build(ctx context.Context, resolved Resolved, link, work string, existing func(string) *Existing) (contract.PluginInstallReview, contract.PluginInstallRecord, error) {
	var review contract.PluginInstallReview
	var record contract.PluginInstallRecord
	source := filepath.Join(work, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		return review, record, err
	}
	commit, err := installer.hub.download(ctx, resolved, source)
	if err != nil {
		return review, record, err
	}
	resolved.Commit, resolved.CommittedAt = commit.id, commit.committedAt
	if text, ok := plugin.ReadTaskSource(source); ok {
		return installer.buildTask(ctx, resolved, link, work, source, text, existing)
	}
	manifest, err := pluginbuild.ReadManifest(source)
	if err != nil {
		return review, record, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallBuildFailed, Messages: []string{err.Error()}}}
	}
	if _, err := pluginbuild.ResolveEntries(source, manifest); err != nil {
		return review, record, fail(contract.PluginInstallNoPlugin, resolved.Subdir)
	}
	lock, lockName, lockData, err := readLock(source)
	if err != nil {
		return review, record, err
	}
	if lock == nil && manifest != nil && len(manifest.Dependencies) > 0 {
		return review, record, fail(contract.PluginInstallNoLockfile, "")
	}
	if err := installer.checkEngines(manifest); err != nil {
		return review, record, err
	}
	store := newPackageStore(ctx, filepath.Join(work, "packages"), lock, &registryFetcher{client: installer.options.Client, registries: installer.registries()})
	result, err := pluginbuild.BuildWith(source, pluginbuild.Options{Packages: store})
	if store.failure != nil {
		return review, record, store.failure
	}
	if err != nil {
		if ctx.Err() != nil {
			return review, record, ctx.Err()
		}
		failure := &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallBuildFailed}}
		var buildErr *pluginbuild.Error
		if errors.As(err, &buildErr) {
			for _, message := range buildErr.Messages {
				failure.Messages = append(failure.Messages, message.String())
			}
		} else {
			failure.Messages = []string{err.Error()}
		}
		return review, record, failure
	}
	// The check runs in the directory the build goes to, where the
	// plugin's top level finds the files it keeps beside its code.
	out := filepath.Join(work, "out")
	outputs, err := writeBuild(out, source, result)
	if err != nil {
		return review, record, err
	}
	checked, err := plugin.Check(plugin.Source{File: "plugin.js", Text: string(result.Plugin), SourceMap: string(result.SourceMap), Dir: out})
	if err != nil {
		return review, record, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
	}
	record = contract.PluginInstallRecord{
		Source: resolved.Source(link), Version: checked.Meta.Version, Lockfile: lockName,
		Outputs: outputs, Dependencies: store.dependencies(),
	}
	if lockData != nil {
		sum := sha256.Sum256(lockData)
		record.LockfileSHA256 = hex.EncodeToString(sum[:])
	}
	_ = os.RemoveAll(source)
	_ = os.RemoveAll(filepath.Join(work, "packages"))
	review = newReview(checked, len(result.Plugin), len(result.Client), record)
	review.Author = manifest.AuthorName()
	review.Engines = manifest.EngineRange()
	if manifest != nil {
		review.Description = manifest.Description
	}
	if manifest != nil && manifest.Version != "" && manifest.Version != checked.Meta.Version {
		review.Warnings = append(review.Warnings, contract.PluginInstallWarning{Code: contract.PluginWarningVersion, Detail: manifest.Version + " / " + checked.Meta.Version})
	}
	for index, warning := range result.Warnings {
		if index == maxReviewWarnings {
			break
		}
		review.Warnings = append(review.Warnings, contract.PluginInstallWarning{Code: contract.PluginWarningBuild, Detail: warning.String()})
	}
	if existing != nil {
		compare(&review, record, existing(checked.Meta.Key))
	}
	return review, record, nil
}

// registries are the registries to fetch packages from, each once.
func (installer *Installer) registries() []string {
	var registries []string
	for _, registry := range installer.options.Registries() {
		registry = strings.TrimRight(registry, "/")
		if registry != "" && !slices.Contains(registries, registry) {
			registries = append(registries, registry)
		}
	}
	return registries
}

// checkEngines fails a plugin whose engines.astrlink leaves this AstrLink
// out. A development build of AstrLink takes any.
func (installer *Installer) checkEngines(manifest *pluginbuild.Manifest) error {
	engines := manifest.EngineRange()
	if engines == "" {
		return nil
	}
	current, ok := pluginbuild.ParseVersion(installer.options.Version)
	if !ok {
		return nil
	}
	compatible, err := pluginbuild.Satisfies(current, engines)
	if err != nil {
		return &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{fmt.Sprintf("engines.astrlink %q: %v", engines, err)}}}
	}
	if !compatible {
		return fail(contract.PluginInstallIncompatible, engines)
	}
	return nil
}

// iconFiles are the icons a project may keep at its root.
var iconFiles = []string{"icon.svg", "icon.png"}

// writeBuild writes a build, with the files the plugin keeps beside its
// code, and the project's icon into out, returning the sha256 of each file.
func writeBuild(out, project string, result *pluginbuild.Result) (map[string]string, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	outputs, err := result.Write(out)
	if err != nil {
		return nil, err
	}
	for _, name := range iconFiles {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil || len(data) > plugin.MaxIconBytes {
			continue
		}
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		outputs[name] = hex.EncodeToString(sum[:])
	}
	return outputs, nil
}

// newReview describes a checked plugin and where it came from.
func newReview(checked *plugin.Checked, pluginBytes, clientBytes int, record contract.PluginInstallRecord) contract.PluginInstallReview {
	meta := checked.Meta
	review := contract.PluginInstallReview{
		Source: record.Source, Key: meta.Key, Name: meta.Name, Version: meta.Version, APIVersion: meta.APIVersion,
		Capabilities: slices.Clone(meta.Capabilities), Hooks: slices.Clone(checked.Hooks),
		Client: clientBytes > 0, PluginBytes: pluginBytes, ClientBytes: clientBytes,
		Lockfile: record.Lockfile, Dependencies: slices.Clone(record.Dependencies),
	}
	if review.Capabilities == nil {
		review.Capabilities = []string{}
	}
	if review.Hooks == nil {
		review.Hooks = []string{}
	}
	if review.Dependencies == nil {
		review.Dependencies = []contract.PluginDependency{}
	}
	if meta.Network != nil {
		review.NetworkPurpose = meta.Network.Purpose
	}
	for _, dependency := range record.Dependencies {
		review.DependencyBytes += dependency.Bytes
		if dependency.Scripts {
			review.Warnings = append(review.Warnings, contract.PluginInstallWarning{Code: contract.PluginWarningScripts, Package: dependency.Name + "@" + dependency.Version})
		}
	}
	return review
}

// compare notes what the plugin a review would replace is, and what the
// review changes of it.
func compare(review *contract.PluginInstallReview, record contract.PluginInstallRecord, current *Existing) {
	if current == nil {
		return
	}
	replaces := &contract.PluginInstallReplace{Name: current.Name, Version: current.Version}
	switch {
	case current.Development:
		replaces.Origin = contract.PluginReplacesDevelopment
	case current.Installed != nil:
		replaces.Origin = contract.PluginReplacesSource
		replaces.Repository = current.Installed.Source.Repository
	case current.Origin == string(plugin.OriginBuiltin):
		replaces.Origin = contract.PluginReplacesBuiltin
	default:
		replaces.Origin = contract.PluginReplacesDirectory
	}
	review.Replaces = replaces
	changes := &contract.PluginInstallChanges{FromVersion: current.Version}
	if current.Loaded {
		for _, capability := range review.Capabilities {
			if !slices.Contains(current.Capabilities, capability) {
				changes.AddedCapabilities = append(changes.AddedCapabilities, capability)
			}
		}
		for _, capability := range current.Capabilities {
			if !slices.Contains(review.Capabilities, capability) {
				changes.RemovedCapabilities = append(changes.RemovedCapabilities, capability)
			}
		}
		switch {
		case review.Client && !current.Client:
			changes.Client = "added"
		case !review.Client && current.Client:
			changes.Client = "removed"
		}
		changes.NetworkPurposeChanged = review.NetworkPurpose != current.NetworkPurpose
	}
	if current.Installed != nil {
		before := map[string]string{}
		for _, dependency := range current.Installed.Dependencies {
			before[dependency.Name] = dependency.Version
		}
		after := map[string]string{}
		for _, dependency := range record.Dependencies {
			after[dependency.Name] = dependency.Version
		}
		for name, version := range after {
			switch old, had := before[name]; {
			case !had:
				changes.AddedDependencies = append(changes.AddedDependencies, name+" "+version)
			case old != version:
				changes.ChangedDependencies = append(changes.ChangedDependencies, name+" "+old+" → "+version)
			}
		}
		for name, version := range before {
			if _, kept := after[name]; !kept {
				changes.RemovedDependencies = append(changes.RemovedDependencies, name+" "+version)
			}
		}
		slices.Sort(changes.AddedDependencies)
		slices.Sort(changes.ChangedDependencies)
		slices.Sort(changes.RemovedDependencies)
	}
	review.Changes = changes
}

// Staged returns the review of a build waiting to be installed.
func (installer *Installer) Staged(token string) (contract.PluginInstallReview, bool) {
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	entry, ok := installer.staged[token]
	if !ok || installer.options.Now().After(entry.expires) {
		return contract.PluginInstallReview{}, false
	}
	return entry.review, true
}

// expire drops the builds no longer waiting, and the oldest past
// maxStaged.
func (installer *Installer) expire() {
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	now := installer.options.Now()
	for token, entry := range installer.staged {
		if now.After(entry.expires) {
			_ = os.RemoveAll(entry.dir)
			delete(installer.staged, token)
		}
	}
	for len(installer.staged) >= maxStaged {
		oldest := ""
		for token, entry := range installer.staged {
			if oldest == "" || entry.expires.Before(installer.staged[oldest].expires) {
				oldest = token
			}
		}
		_ = os.RemoveAll(installer.staged[oldest].dir)
		delete(installer.staged, oldest)
	}
}

// Install puts a staged build in place of the plugin of its key, keeping
// the version it replaces. It returns the plugin's key; the registry must
// reload to load it.
func (installer *Installer) Install(token string) (string, error) {
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	entry, ok := installer.staged[token]
	if !ok || installer.options.Now().After(entry.expires) {
		return "", fail(contract.PluginInstallExpired, "")
	}
	key := entry.review.Key
	out := filepath.Join(entry.dir, "out")
	record := entry.record
	record.InstalledAt = installer.options.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(out, plugin.InstallRecord), data, 0o644); err != nil {
		return "", err
	}
	if err := installer.replace(key, out); err != nil {
		return "", err
	}
	_ = os.RemoveAll(entry.dir)
	delete(installer.staged, token)
	return key, nil
}

// replace moves dir into the plugin directory as key, keeping what key
// held among its versions. The caller holds the mutex.
func (installer *Installer) replace(key, dir string) error {
	target := filepath.Join(installer.options.Dir, key)
	kept := ""
	if _, err := os.Lstat(target); err == nil {
		id, err := installer.keep(key, target)
		if err != nil {
			return err
		}
		kept = id
	}
	if err := os.Rename(dir, target); err != nil {
		if kept != "" {
			_ = os.Rename(installer.versionDir(key, kept), target)
		}
		return err
	}
	installer.prune(key)
	return nil
}

// versionIDPattern is the name of a kept version: when it was replaced,
// and a random suffix.
var versionIDPattern = regexp.MustCompile(`^(\d{8}T\d{6}Z)-[0-9a-f]{8}$`)

func (installer *Installer) versionDir(key, id string) string {
	return filepath.Join(installer.options.Dir, versionsDir, key, id)
}

// keep moves what key holds among its versions.
func (installer *Installer) keep(key, target string) (string, error) {
	id := installer.options.Now().UTC().Format("20060102T150405Z") + "-" + randomHex(4)
	dest := installer.versionDir(key, id)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(target, dest); err != nil {
		return "", err
	}
	return id, nil
}

// prune drops the oldest versions of key past keptVersions.
func (installer *Installer) prune(key string) {
	ids := installer.versionIDs(key)
	for _, id := range ids[min(len(ids), keptVersions):] {
		_ = os.RemoveAll(installer.versionDir(key, id))
	}
}

// versionIDs are the versions kept of key, the most recently replaced
// first.
func (installer *Installer) versionIDs(key string) []string {
	entries, err := os.ReadDir(filepath.Join(installer.options.Dir, versionsDir, key))
	if err != nil {
		return nil
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() && versionIDPattern.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	return ids
}

// Versions lists the versions kept of key, the most recently replaced
// first.
func (installer *Installer) Versions(key string) []contract.PluginInstalledVersion {
	if !plugin.ValidPluginKey(key) {
		return nil
	}
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	var versions []contract.PluginInstalledVersion
	for _, id := range installer.versionIDs(key) {
		version := contract.PluginInstalledVersion{ID: id}
		if replaced, err := time.Parse("20060102T150405Z", versionIDPattern.FindStringSubmatch(id)[1]); err == nil {
			version.ReplacedAt = replaced.UTC().Format(time.RFC3339)
		}
		if record := readRecord(installer.versionDir(key, id)); record != nil {
			version.Version = record.Version
			source := record.Source
			version.Source = &source
		}
		versions = append(versions, version)
	}
	return versions
}

func readRecord(dir string) *contract.PluginInstallRecord {
	data, err := os.ReadFile(filepath.Join(dir, plugin.InstallRecord))
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	var record contract.PluginInstallRecord
	if json.Unmarshal(data, &record) != nil {
		return nil
	}
	return &record
}

// errNoVersion is a version that is not kept.
var errNoVersion = errors.New("no such version")

// RollbackReview describes the version id of key, compared with current,
// for the user to confirm going back to it.
func (installer *Installer) RollbackReview(key, id string, current *Existing) (contract.PluginInstallReview, error) {
	if !plugin.ValidPluginKey(key) || !versionIDPattern.MatchString(id) {
		return contract.PluginInstallReview{}, errNoVersion
	}
	installer.mutex.Lock()
	dir := installer.versionDir(key, id)
	checked, pluginBytes, clientBytes, err := inspect(dir)
	record := readRecord(dir)
	installer.mutex.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return contract.PluginInstallReview{}, errNoVersion
	}
	if err != nil {
		return contract.PluginInstallReview{}, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
	}
	if checked.Meta.Key != key {
		return contract.PluginInstallReview{}, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{"the version kept is of another plugin"}}}
	}
	if record == nil {
		record = &contract.PluginInstallRecord{Version: checked.Meta.Version}
	}
	review := newReview(checked, pluginBytes, clientBytes, *record)
	compare(&review, *record, current)
	return review, nil
}

// inspect checks the plugin kept in dir as the registry would load it.
func inspect(dir string) (*plugin.Checked, int, int, error) {
	source := plugin.Source{File: "plugin.js"}
	text, err := os.ReadFile(filepath.Join(dir, "plugin.js"))
	if errors.Is(err, os.ErrNotExist) {
		source.File = "plugin.ts"
		text, err = os.ReadFile(filepath.Join(dir, "plugin.ts"))
	}
	if err != nil {
		return nil, 0, 0, err
	}
	source.Text = string(text)
	if sourceMap, err := os.ReadFile(filepath.Join(dir, "plugin.js.map")); err == nil && source.File == "plugin.js" {
		source.SourceMap = string(sourceMap)
	}
	clientBytes := 0
	if info, err := os.Stat(filepath.Join(dir, "client.js")); err == nil {
		clientBytes = int(info.Size())
	}
	source.Dir = dir
	checked, err := plugin.Check(source)
	if err != nil {
		return nil, 0, 0, err
	}
	return checked, len(text), clientBytes, nil
}

// Rollback puts the version id of key back in place of what key holds now,
// which is kept in turn. The registry must reload to load it.
func (installer *Installer) Rollback(key, id string) error {
	if !plugin.ValidPluginKey(key) || !versionIDPattern.MatchString(id) {
		return errNoVersion
	}
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	dir := installer.versionDir(key, id)
	if _, err := os.Stat(dir); err != nil {
		return errNoVersion
	}
	// The version leaves the versions before replace keeps the current
	// one, so that pruning cannot drop it.
	holding := filepath.Join(installer.options.Dir, stagingDir, "rollback-"+randomHex(8))
	if err := os.MkdirAll(filepath.Dir(holding), 0o700); err != nil {
		return err
	}
	if err := os.Rename(dir, holding); err != nil {
		return err
	}
	if err := installer.replace(key, holding); err != nil {
		_ = os.Rename(holding, dir)
		return err
	}
	return nil
}

// ErrNotInstalled is a plugin that was not installed from source.
var ErrNotInstalled = errors.New("the plugin was not installed from source")

// Uninstall removes a plugin installed from source, and the versions kept
// of it. The registry must reload; a builtin plugin of the key comes back.
func (installer *Installer) Uninstall(key string) error {
	if !plugin.ValidPluginKey(key) {
		return ErrNotInstalled
	}
	installer.mutex.Lock()
	defer installer.mutex.Unlock()
	target := filepath.Join(installer.options.Dir, key)
	if readRecord(target) == nil {
		return ErrNotInstalled
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(installer.options.Dir, versionsDir, key))
}

// CheckUpdate looks for a newer version of a plugin installed from source:
// a newer version tag with the same prefix for a tag, the branch's newest
// commit for a branch. A plugin installed from a commit stays on it.
func (installer *Installer) CheckUpdate(ctx context.Context, record contract.PluginInstallRecord) (contract.PluginUpdateCheck, error) {
	source := record.Source
	if source.Kind == contract.PluginSourceMarket {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return installer.checkMarketUpdate(ctx, record)
	}
	owner, repo, ok := strings.Cut(source.Repository, "/")
	if !ok || !ownerPattern.MatchString(owner) || !repoPattern.MatchString(repo) {
		return contract.PluginUpdateCheck{}, fail(contract.PluginInstallInvalidLink, source.Repository)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if source.RefKind != contract.PluginRefBranch && source.RefKind != contract.PluginRefTag {
		return contract.PluginUpdateCheck{Pinned: true}, nil
	}
	prefix, current, versioned := tagVersion(source.Ref)
	if source.RefKind == contract.PluginRefTag && !versioned {
		return contract.PluginUpdateCheck{Ref: source.Ref, RefKind: contract.PluginRefTag}, nil
	}
	refs, err := installer.hub.refs(ctx, owner, repo)
	if err != nil {
		return contract.PluginUpdateCheck{}, installer.failure(ctx, err)
	}
	var check contract.PluginUpdateCheck
	if source.RefKind == contract.PluginRefBranch {
		commit, ok := refs.branches[source.Ref]
		if !ok {
			return contract.PluginUpdateCheck{}, fail(contract.PluginInstallRefNotFound, source.Ref)
		}
		check = contract.PluginUpdateCheck{Ref: source.Ref, RefKind: contract.PluginRefBranch}
		if strings.EqualFold(commit, source.Commit) {
			return check, nil
		}
		check.Available, check.Commit = true, commit
		check.URL = TreeURL(source.Repository, source.Ref, source.Subdir)
		// When the API answers, the commits since make the notes.
		check.Notes = installer.hub.commitsSince(ctx, owner, repo, source.Commit, check.Commit)
	} else {
		tag, version, found := newestTag(refs.tagList(), prefix, &current, current.Prerelease != "")
		if !found {
			return contract.PluginUpdateCheck{Ref: source.Ref, RefKind: contract.PluginRefTag}, nil
		}
		check = contract.PluginUpdateCheck{
			Available: true, Ref: tag.Name, RefKind: contract.PluginRefTag, Commit: tag.Commit.SHA,
			Version: version.String(), URL: TreeURL(source.Repository, tag.Name, source.Subdir),
		}
		// When the API answers, the release makes the notes.
		check.Notes = installer.hub.notes(ctx, owner, repo, tag.Name)
	}
	if info, err := installer.hub.commit(ctx, owner, repo, check.Commit); err == nil {
		check.CommittedAt = info.Commit.Committer.Date.UTC().Format(time.RFC3339)
	}
	return check, nil
}

func randomHex(size int) string {
	buffer := make([]byte, size)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}
