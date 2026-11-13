package plugininstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// tgz is a gzipped tar archive of files under top/.
func tgz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body := files[name]
		if err := archive.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// sourceArchive is a commit's archive as GitHub serves it: git archive
// names the commit in a global header and dates every entry with its time.
func sourceArchive(t *testing.T, top, commit string, date time.Time, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	if commit != "" {
		if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": commit}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.WriteHeader(&tar.Header{Name: top + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: date}); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body := files[name]
		if err := archive.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, ModTime: date}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sri(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// fakeCommit is a commit of a fake repository.
type fakeCommit struct {
	files   map[string]string
	date    time.Time
	message string
}

// fakeRepo is a repository the fake GitHub serves.
type fakeRepo struct {
	defaultBranch string
	branches      map[string]string
	tags          map[string]string
	commits       map[string]fakeCommit
	releases      map[string]string
}

// fakeGitHub serves GitHub's refs over Git's protocol, its release pages,
// its API and its archives for repos. rateLimited makes the API answer
// that the hour's requests are used up; gitLimited does the same for Git's
// protocol.
type fakeGitHub struct {
	t           *testing.T
	mutex       sync.Mutex
	repos       map[string]*fakeRepo
	rateLimited bool
	gitLimited  bool
	web         *httptest.Server
	api         *httptest.Server
	codeload    *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	hub := &fakeGitHub{t: t, repos: map[string]*fakeRepo{}}
	hub.web = httptest.NewServer(http.HandlerFunc(hub.serveWeb))
	hub.api = httptest.NewServer(http.HandlerFunc(hub.serveAPI))
	hub.codeload = httptest.NewServer(http.HandlerFunc(hub.serveArchive))
	t.Cleanup(hub.web.Close)
	t.Cleanup(hub.api.Close)
	t.Cleanup(hub.codeload.Close)
	return hub
}

// serveWeb answers ls-refs at <owner>/<repo>.git/git-upload-pack and sends
// <owner>/<repo>/releases/latest to the newest release.
func (hub *fakeGitHub) serveWeb(writer http.ResponseWriter, request *http.Request) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()
	parts := segments(request)
	if len(parts) < 3 {
		http.NotFound(writer, request)
		return
	}
	if parts[2] == "git-upload-pack" && strings.HasSuffix(parts[1], ".git") {
		if hub.gitLimited {
			writer.Header().Set("Retry-After", "60")
			writer.WriteHeader(http.StatusTooManyRequests)
			return
		}
		repo := hub.repos[parts[0]+"/"+strings.TrimSuffix(parts[1], ".git")]
		body, _ := io.ReadAll(request.Body)
		if repo == nil {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodPost || request.Header.Get("Git-Protocol") != "version=2" || !bytes.Contains(body, []byte("command=ls-refs")) {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		var answer bytes.Buffer
		line := func(text string) { fmt.Fprintf(&answer, "%04x%s\n", len(text)+5, text) }
		if sha, ok := repo.branches[repo.defaultBranch]; ok {
			line(sha + " HEAD symref-target:refs/heads/" + repo.defaultBranch)
		}
		for name, sha := range repo.branches {
			line(sha + " refs/heads/" + name)
		}
		for name, sha := range repo.tags {
			// Every tag is annotated: the tag object first, the commit
			// it points to peeled.
			line(strings.Repeat("f", 40) + " refs/tags/" + name + " peeled:" + sha)
		}
		answer.WriteString("0000")
		_, _ = writer.Write(answer.Bytes())
		return
	}
	repo := hub.repos[parts[0]+"/"+parts[1]]
	if repo == nil {
		http.NotFound(writer, request)
		return
	}
	if len(parts) == 4 && parts[2] == "releases" && parts[3] == "latest" {
		latest := ""
		for tag := range repo.releases {
			if tag > latest {
				latest = tag
			}
		}
		if latest == "" {
			http.Redirect(writer, request, "/"+parts[0]+"/"+parts[1]+"/releases", http.StatusFound)
			return
		}
		http.Redirect(writer, request, "/"+parts[0]+"/"+parts[1]+"/releases/tag/"+url.PathEscape(latest), http.StatusFound)
		return
	}
	http.NotFound(writer, request)
}

// segments splits an escaped path into its unescaped segments.
func segments(request *http.Request) []string {
	parts := strings.Split(strings.Trim(request.URL.EscapedPath(), "/"), "/")
	for index, part := range parts {
		if unescaped, err := url.PathUnescape(part); err == nil {
			parts[index] = unescaped
		}
	}
	return parts
}

func (hub *fakeGitHub) find(repo *fakeRepo, ref string) (string, bool) {
	if sha, ok := repo.branches[ref]; ok {
		return sha, true
	}
	if sha, ok := repo.tags[ref]; ok {
		return sha, true
	}
	for sha := range repo.commits {
		if len(ref) >= 7 && strings.HasPrefix(sha, strings.ToLower(ref)) {
			return sha, true
		}
	}
	return "", false
}

func (hub *fakeGitHub) serveAPI(writer http.ResponseWriter, request *http.Request) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()
	if hub.rateLimited {
		writer.Header().Set("X-RateLimit-Remaining", "0")
		writer.Header().Set("X-RateLimit-Reset", "1800000000")
		writer.WriteHeader(http.StatusForbidden)
		return
	}
	parts := segments(request)
	if len(parts) < 3 || parts[0] != "repos" {
		http.NotFound(writer, request)
		return
	}
	repo := hub.repos[parts[1]+"/"+parts[2]]
	if repo == nil {
		http.NotFound(writer, request)
		return
	}
	rest := parts[3:]
	commitJSON := func(sha string) map[string]any {
		commit := repo.commits[sha]
		return map[string]any{"sha": sha, "commit": map[string]any{"message": commit.message, "committer": map[string]any{"date": commit.date.Format(time.RFC3339)}}}
	}
	var answer any
	switch {
	case len(rest) == 0:
		answer = map[string]any{"full_name": parts[1] + "/" + parts[2], "default_branch": repo.defaultBranch}
	case rest[0] == "tags":
		var tags []map[string]any
		for name, sha := range repo.tags {
			tags = append(tags, map[string]any{"name": name, "commit": map[string]any{"sha": sha}})
		}
		answer = tags
	case rest[0] == "commits" && len(rest) >= 2:
		sha, ok := hub.find(repo, strings.Join(rest[1:], "/"))
		if !ok {
			http.NotFound(writer, request)
			return
		}
		answer = commitJSON(sha)
	case rest[0] == "branches" && len(rest) >= 2:
		sha, ok := repo.branches[strings.Join(rest[1:], "/")]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		answer = map[string]any{"name": strings.Join(rest[1:], "/"), "commit": commitJSON(sha)}
	case rest[0] == "releases" && len(rest) == 2 && rest[1] == "latest":
		latest := ""
		for tag := range repo.releases {
			if tag > latest {
				latest = tag
			}
		}
		if latest == "" {
			http.NotFound(writer, request)
			return
		}
		answer = map[string]any{"tag_name": latest}
	case rest[0] == "releases" && len(rest) >= 3 && rest[1] == "tags":
		body, ok := repo.releases[strings.Join(rest[2:], "/")]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		answer = map[string]any{"body": body}
	case rest[0] == "compare" && len(rest) == 2:
		base, head, _ := strings.Cut(rest[1], "...")
		var commits []map[string]any
		if base != head {
			commits = append(commits, commitJSON(head))
		}
		answer = map[string]any{"commits": commits}
	default:
		http.NotFound(writer, request)
		return
	}
	_ = json.NewEncoder(writer).Encode(answer)
}

func (hub *fakeGitHub) serveArchive(writer http.ResponseWriter, request *http.Request) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()
	parts := segments(request)
	if len(parts) != 4 || parts[2] != "tar.gz" {
		http.NotFound(writer, request)
		return
	}
	repo := hub.repos[parts[0]+"/"+parts[1]]
	if repo == nil {
		http.NotFound(writer, request)
		return
	}
	sha, ok := hub.find(repo, parts[3])
	if !ok || !shaPattern.MatchString(parts[3]) {
		http.NotFound(writer, request)
		return
	}
	commit := repo.commits[sha]
	_, _ = writer.Write(sourceArchive(hub.t, parts[1]+"-"+sha, sha, commit.date, commit.files))
}

// fakeRegistry serves package archives at their registry paths; broken
// answers 500 to everything, and wrong serves other bytes for the paths it
// lists.
type fakeRegistry struct {
	server   *httptest.Server
	archives map[string][]byte
	wrong    map[string]bool
	broken   bool
	mutex    sync.Mutex
	served   []string
}

func newFakeRegistry(t *testing.T, archives map[string][]byte) *fakeRegistry {
	registry := &fakeRegistry{archives: archives, wrong: map[string]bool{}}
	registry.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		registry.mutex.Lock()
		defer registry.mutex.Unlock()
		registry.served = append(registry.served, request.URL.Path)
		switch {
		case registry.broken:
			writer.WriteHeader(http.StatusInternalServerError)
		case registry.wrong[request.URL.Path]:
			_, _ = writer.Write([]byte("not the package"))
		case registry.archives[request.URL.Path] != nil:
			_, _ = writer.Write(registry.archives[request.URL.Path])
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(registry.server.Close)
	return registry
}

// npmPackage is a package archive and its hash.
type npmPackage struct {
	path      string
	archive   []byte
	integrity string
}

func newPackage(t *testing.T, name, version string, files map[string]string) npmPackage {
	base := name[strings.LastIndex(name, "/")+1:]
	archive := tgz(t, "package", files)
	return npmPackage{path: "/" + name + "/-/" + base + "-" + version + ".tgz", archive: archive, integrity: sri(archive)}
}

// fixture is a repository with a plugin that imports tiny-lib, which
// imports its own version of helper, and the registries serving them.
type fixture struct {
	hub     *fakeGitHub
	primary *fakeRegistry
	mirror  *fakeRegistry
	tinyLib npmPackage
	helper1 npmPackage
	helper2 npmPackage
	dir     string
}

func newFixture(t *testing.T) *fixture {
	tinyLib := newPackage(t, "tiny-lib", "2.0.0", map[string]string{
		"package.json": `{"name":"tiny-lib","version":"2.0.0","main":"index.js","scripts":{"postinstall":"node setup.js"}}`,
		"index.js":     `const helper = require("helper"); exports.greet = (name) => "hello " + name + helper.mark;`,
	})
	helper1 := newPackage(t, "helper", "1.0.0", map[string]string{
		"package.json": `{"name":"helper","version":"1.0.0","main":"index.js"}`,
		"index.js":     `exports.mark = "<one>";`,
	})
	helper2 := newPackage(t, "helper", "2.0.0", map[string]string{
		"package.json": `{"name":"helper","version":"2.0.0","main":"index.js"}`,
		"index.js":     `exports.mark = "<two>";`,
	})
	archives := func() map[string][]byte {
		return map[string][]byte{tinyLib.path: tinyLib.archive, helper1.path: helper1.archive, helper2.path: helper2.archive}
	}
	return &fixture{
		hub: newFakeGitHub(t), primary: newFakeRegistry(t, archives()), mirror: newFakeRegistry(t, archives()),
		tinyLib: tinyLib, helper1: helper1, helper2: helper2, dir: t.TempDir(),
	}
}

func (fixture *fixture) installer() *Installer {
	return New(Options{
		Dir: fixture.dir, GitHub: fixture.hub.web.URL, GitHubAPI: fixture.hub.api.URL, Codeload: fixture.hub.codeload.URL,
		Registries: func() []string { return []string{fixture.primary.server.URL, fixture.mirror.server.URL} },
		Version:    "1.0.0",
	})
}

// npmLockFile pins tiny-lib, with helper 2.0.0 nested under it and helper
// 1.0.0 at the top, which nothing the plugin runs imports.
func (fixture *fixture) npmLockFile() string {
	return fixture.npmLockWith(fixture.tinyLib.integrity)
}

// npmLockWith is npmLockFile with tiny-lib's hash given.
func (fixture *fixture) npmLockWith(tinyLib string) string {
	return fmt.Sprintf(`{
  "name": "hello-plugin",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": { "name": "hello-plugin", "version": "1.1.0", "dependencies": { "tiny-lib": "^2.0.0" } },
    "node_modules/tiny-lib": { "version": "2.0.0", "resolved": "https://registry.npmjs.org/tiny-lib/-/tiny-lib-2.0.0.tgz", "integrity": %q, "hasInstallScript": true },
    "node_modules/helper": { "version": "1.0.0", "resolved": "https://registry.npmjs.org/helper/-/helper-1.0.0.tgz", "integrity": %q },
    "node_modules/tiny-lib/node_modules/helper": { "version": "2.0.0", "resolved": "https://registry.npmmirror.com/helper/-/helper-2.0.0.tgz", "integrity": %q }
  }
}`, tinyLib, fixture.helper1.integrity, fixture.helper2.integrity)
}

func pluginFiles(version string, capabilities string, lockName, lock string) map[string]string {
	files := map[string]string{
		"package.json": `{
  "name": "hello-plugin",
  "version": "` + version + `",
  "description": "Says hello",
  "author": { "name": "Ada" },
  "engines": { "astrlink": ">=0.1.0" },
  "dependencies": { "tiny-lib": "^2.0.0" },
  "devDependencies": { "typescript": "^5.0.0" }
}`,
		"src/plugin.ts": `import { greet } from "tiny-lib";
export const meta = { apiVersion: 2, key: "hello", name: "Hello", version: "` + version + `", capabilities: [` + capabilities + `] };
export async function prepareTurn() {
  return { text: greet("you") };
}
`,
		"icon.svg":          `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>`,
		"README.md":         "# hello",
		"node_modules/x.js": "committed by mistake",
	}
	if lockName != "" {
		files[lockName] = lock
	}
	return files
}

const (
	sha1v = "1111111111111111111111111111111111111111"
	sha2v = "2222222222222222222222222222222222222222"
	sha3v = "3333333333333333333333333333333333333333"
)

func TestInstallFromGitHubBuildsReviewsInstallsUpdatesAndRollsBack(t *testing.T) {
	fixture := newFixture(t)
	date := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fixture.hub.repos["acme/hello"] = &fakeRepo{
		defaultBranch: "main",
		branches:      map[string]string{"main": sha2v},
		tags:          map[string]string{"v1.0.0": sha1v, "v1.1.0": sha2v, "nightly": sha2v},
		commits: map[string]fakeCommit{
			sha1v: {files: pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockFile()), date: date},
			sha2v: {files: pluginFiles("1.1.0", "", "package-lock.json", fixture.npmLockFile()), date: date.Add(time.Hour)},
		},
		releases: map[string]string{"v1.1.0": "first release"},
	}
	// The first registry lacks tiny-lib and serves helper's bytes wrong:
	// the second serves both, as the lock file's hashes say.
	delete(fixture.primary.archives, fixture.tinyLib.path)
	fixture.primary.wrong[fixture.helper2.path] = true
	// The API's requests of the hour are used up, as on an address many
	// people share: refs come through Git's protocol, the commit's time from
	// its archive.
	fixture.hub.rateLimited = true
	installer := fixture.installer()
	review, err := installer.Preview(context.Background(), "https://github.com/acme/hello", func(string) *Existing { return nil })
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if review.Key != "hello" || review.Name != "Hello" || review.Version != "1.1.0" || review.APIVersion != 2 || review.Author != "Ada" || review.Description != "Says hello" {
		t.Fatalf("review = %+v", review)
	}
	if source := review.Source; source.Repository != "acme/hello" || source.Ref != "v1.1.0" || source.RefKind != contract.PluginRefTag || source.Commit != sha2v || source.CommittedAt != "2026-10-01T13:00:00Z" {
		t.Fatalf("source = %+v", source)
	}
	want := []contract.PluginDependency{{Name: "helper", Version: "2.0.0"}, {Name: "tiny-lib", Version: "2.0.0", Scripts: true}}
	if len(review.Dependencies) != 2 || review.Dependencies[0].Name != want[0].Name || review.Dependencies[0].Version != want[0].Version ||
		review.Dependencies[1].Name != want[1].Name || !review.Dependencies[1].Scripts || review.DependencyBytes <= 0 {
		t.Fatalf("dependencies = %+v", review.Dependencies)
	}
	if review.Lockfile != "package-lock.json" || review.Engines != ">=0.1.0" || review.Client || review.Replaces != nil || review.Token == "" {
		t.Fatalf("review = %+v", review)
	}
	if len(review.Warnings) != 1 || review.Warnings[0].Code != contract.PluginWarningScripts || review.Warnings[0].Package != "tiny-lib@2.0.0" {
		t.Fatalf("warnings = %+v", review.Warnings)
	}
	for _, served := range fixture.mirror.served {
		if served == fixture.helper1.path {
			t.Fatalf("helper 1.0.0 was fetched though nothing imports it")
		}
	}
	if _, ok := installer.Staged(review.Token); !ok {
		t.Fatal("the review's build is not staged")
	}

	key, err := installer.Install(review.Token)
	if err != nil || key != "hello" {
		t.Fatalf("Install = %q, %v", key, err)
	}
	built, err := os.ReadFile(filepath.Join(fixture.dir, "hello", "plugin.js"))
	if err != nil || !strings.Contains(string(built), "<two>") || strings.Contains(string(built), "<one>") {
		t.Fatalf("plugin.js = %s, %v", built, err)
	}
	for _, name := range []string{"plugin.js.map", "icon.svg", plugin.InstallRecord} {
		if _, err := os.Stat(filepath.Join(fixture.dir, "hello", name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	sources, err := plugin.Discover(nil, fixture.dir)
	if err != nil || len(sources) != 1 || sources[0].Installed == nil || sources[0].Installed.Source.Ref != "v1.1.0" || sources[0].Installed.InstalledAt == "" {
		t.Fatalf("Discover = %+v, %v", sources, err)
	}
	if _, err := installer.Install(review.Token); !hasReason(err, contract.PluginInstallExpired) {
		t.Fatalf("a second Install = %v", err)
	}
	record := *sources[0].Installed

	// No newer tag yet; then v1.2.0 asks for storage.
	check, err := installer.CheckUpdate(context.Background(), record)
	if err != nil || check.Available {
		t.Fatalf("CheckUpdate = %+v, %v", check, err)
	}
	repo := fixture.hub.repos["acme/hello"]
	repo.tags["v1.2.0"] = sha3v
	repo.tags["v2.0.0-beta.1"] = sha3v
	repo.commits[sha3v] = fakeCommit{files: pluginFiles("1.2.0", `"storage"`, "package-lock.json", fixture.npmLockFile()), date: date.Add(2 * time.Hour)}
	repo.releases["v1.2.0"] = "stores things"
	// Without the API the update shows, only without its notes and time.
	check, err = installer.CheckUpdate(context.Background(), record)
	if err != nil || !check.Available || check.Ref != "v1.2.0" || check.Commit != sha3v || check.Notes != "" || check.CommittedAt != "" {
		t.Fatalf("CheckUpdate without the API = %+v, %v", check, err)
	}
	fixture.hub.rateLimited = false
	check, err = installer.CheckUpdate(context.Background(), record)
	if err != nil || !check.Available || check.Ref != "v1.2.0" || check.Version != "1.2.0" || check.Notes != "stores things" ||
		check.URL != "https://github.com/acme/hello/tree/v1.2.0" || check.CommittedAt != "2026-10-01T14:00:00Z" {
		t.Fatalf("CheckUpdate = %+v, %v", check, err)
	}
	current := &Existing{Origin: string(plugin.OriginDirectory), Loaded: true, Name: "Hello", Version: "1.1.0", Capabilities: []string{}, Installed: &record}
	update, err := installer.Preview(context.Background(), check.URL, func(key string) *Existing {
		if key != "hello" {
			t.Errorf("existing(%q)", key)
		}
		return current
	})
	if err != nil {
		t.Fatalf("Preview of the update: %v", err)
	}
	if update.Source.RefKind != contract.PluginRefTag || update.Source.Ref != "v1.2.0" || update.Replaces == nil ||
		update.Replaces.Origin != contract.PluginReplacesSource || update.Replaces.Repository != "acme/hello" ||
		update.Changes == nil || update.Changes.FromVersion != "1.1.0" || !slices.Equal(update.Changes.AddedCapabilities, []string{"storage"}) {
		t.Fatalf("update = %+v / %+v / %+v", update, update.Replaces, update.Changes)
	}
	if _, err := installer.Install(update.Token); err != nil {
		t.Fatalf("Install the update: %v", err)
	}
	versions := installer.Versions("hello")
	if len(versions) != 1 || versions[0].Version != "1.1.0" || versions[0].Source == nil || versions[0].Source.Ref != "v1.1.0" || versions[0].ReplacedAt == "" {
		t.Fatalf("Versions = %+v", versions)
	}

	back, err := installer.RollbackReview("hello", versions[0].ID, &Existing{Origin: "directory", Loaded: true, Name: "Hello", Version: "1.2.0", Capabilities: []string{"storage"}})
	if err != nil || back.Version != "1.1.0" || back.Changes == nil || !slices.Equal(back.Changes.RemovedCapabilities, []string{"storage"}) {
		t.Fatalf("RollbackReview = %+v, %v", back, err)
	}
	if err := installer.Rollback("hello", versions[0].ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if record := readRecord(filepath.Join(fixture.dir, "hello")); record == nil || record.Version != "1.1.0" {
		t.Fatalf("after the rollback: %+v", record)
	}
	versions = installer.Versions("hello")
	if len(versions) != 1 || versions[0].Version != "1.2.0" {
		t.Fatalf("Versions after the rollback = %+v", versions)
	}
	if err := installer.Rollback("hello", "20260101T000000Z-00000000"); !errors.Is(err, errNoVersion) {
		t.Fatalf("Rollback of no version = %v", err)
	}

	if err := installer.Uninstall("hello"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.dir, "hello")); !os.IsNotExist(err) {
		t.Fatalf("the plugin is still there: %v", err)
	}
	if len(installer.Versions("hello")) != 0 {
		t.Fatal("the versions are still there")
	}
	if err := installer.Uninstall("hello"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("a second Uninstall = %v", err)
	}
}

func hasReason(err error, reason string) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Reason == reason
}

func TestInstallReadsPnpmAndBunLockFiles(t *testing.T) {
	fixture := newFixture(t)
	pnpm := fmt.Sprintf(`lockfileVersion: '9.0'

importers:

  .:
    dependencies:
      tiny-lib:
        specifier: ^2.0.0
        version: 2.0.0

packages:

  helper@1.0.0:
    resolution: {integrity: %s}

  helper@2.0.0:
    resolution: {integrity: %s}

  tiny-lib@2.0.0:
    resolution: {integrity: %s}

snapshots:

  helper@1.0.0: {}

  helper@2.0.0: {}

  tiny-lib@2.0.0:
    dependencies:
      helper: 2.0.0
`, fixture.helper1.integrity, fixture.helper2.integrity, fixture.tinyLib.integrity)
	bun := fmt.Sprintf(`{
  "lockfileVersion": 1,
  "workspaces": {
    "": {
      "name": "hello-plugin",
      "dependencies": { "tiny-lib": "^2.0.0", },
    },
  },
  // helper 1.0.0 sits at the top; tiny-lib keeps its own.
  "packages": {
    "helper": ["helper@1.0.0", "", {}, %q],
    "tiny-lib": ["tiny-lib@2.0.0", "", { "dependencies": { "helper": "^2.0.0" } }, %q],
    "tiny-lib/helper": ["helper@2.0.0", "", {}, %q],
  }
}`, fixture.helper1.integrity, fixture.tinyLib.integrity, fixture.helper2.integrity)
	fixture.hub.repos["acme/hello"] = &fakeRepo{
		defaultBranch: "main",
		branches:      map[string]string{"pnpm": sha1v, "bun": sha2v},
		tags:          map[string]string{},
		commits: map[string]fakeCommit{
			sha1v: {files: pluginFiles("1.1.0", "", "pnpm-lock.yaml", pnpm)},
			sha2v: {files: pluginFiles("1.1.0", "", "bun.lock", bun)},
		},
	}
	installer := fixture.installer()
	for _, branch := range []string{"pnpm", "bun"} {
		review, err := installer.Preview(context.Background(), "github.com/acme/hello/tree/"+branch, nil)
		if err != nil {
			t.Fatalf("%s: Preview: %v", branch, err)
		}
		if review.Source.RefKind != contract.PluginRefBranch || review.Source.Ref != branch || len(review.Dependencies) != 2 ||
			review.Dependencies[0].Name != "helper" || review.Dependencies[0].Version != "2.0.0" {
			t.Fatalf("%s: review = %+v", branch, review)
		}
	}
}

func TestPnpmVersionSixLockFile(t *testing.T) {
	lock, err := parsePnpmLock([]byte(`lockfileVersion: '6.0'

dependencies:
  alias:
    specifier: npm:real@^1.0.0
    version: /real@1.0.0
  plain:
    specifier: ^2.0.0
    version: 2.0.0(peer@1.0.0)

packages:

  /real@1.0.0:
    resolution: {integrity: sha512-AAAA}
    dependencies:
      inner: 3.0.0

  /plain@2.0.0(peer@1.0.0):
    resolution: {integrity: sha512-BBBB, tarball: https://mirror.example/npm/plain/-/plain-2.0.0.tgz}

  /inner@3.0.0:
    resolution: {integrity: sha512-CCCC}
`))
	if err != nil {
		t.Fatal(err)
	}
	key, ok := lock.dependency("", "alias")
	if !ok || key != "real@1.0.0" {
		t.Fatalf("alias = %q, %v", key, ok)
	}
	locked, err := lock.resolve(key)
	if err != nil || locked.Name != "real" || locked.Path != "/real/-/real-1.0.0.tgz" {
		t.Fatalf("resolve(alias) = %+v, %v", locked, err)
	}
	inner, ok := lock.dependency(key, "inner")
	if !ok || inner != "inner@3.0.0" {
		t.Fatalf("inner = %q, %v", inner, ok)
	}
	key, _ = lock.dependency("", "plain")
	locked, err = lock.resolve(key)
	if err != nil || locked.Path != "/plain/-/plain-2.0.0.tgz" || locked.Integrity != "sha512-BBBB" {
		t.Fatalf("resolve(plain) = %+v, %v", locked, err)
	}
}

func TestBunParentCountsAScopeAsPartOfTheName(t *testing.T) {
	for key, parent := range map[string]string{"a": "", "a/b": "a", "a/@s/b": "a", "@s/a/b": "@s/a", "@s/a/@t/b": "@s/a"} {
		if got := bunParent(key); got != parent {
			t.Errorf("bunParent(%q) = %q, want %q", key, got, parent)
		}
	}
}

func TestPreviewSaysWhyAPluginCannotBeInstalled(t *testing.T) {
	fixture := newFixture(t)
	// tiny-lib is an alias of native-lib, which holds a native module.
	native := newPackage(t, "native-lib", "2.0.0", map[string]string{
		"package.json": `{"name":"native-lib","version":"2.0.0","main":"index.js"}`,
		"index.js":     `module.exports = require("./build/Release/addon.node");`,
		"binding.gyp":  `{}`,
	})
	nativeLock := strings.Replace(fixture.npmLockWith(native.integrity),
		`"node_modules/tiny-lib": { "version": "2.0.0", "resolved": "https://registry.npmjs.org/tiny-lib/-/tiny-lib-2.0.0.tgz"`,
		`"node_modules/tiny-lib": { "name": "native-lib", "version": "2.0.0", "resolved": "https://registry.npmjs.org/native-lib/-/native-lib-2.0.0.tgz"`, 1)
	broken := pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockFile())
	broken["src/plugin.ts"] = "export const meta = {;\n"
	tooNew := pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockFile())
	tooNew["package.json"] = strings.Replace(tooNew["package.json"], ">=0.1.0", ">=99.0.0", 1)
	noLock := pluginFiles("1.0.0", "", "", "")
	commits := map[string]map[string]string{
		"native": pluginFiles("1.0.0", "", "package-lock.json", nativeLock),
		"broken": broken, "too-new": tooNew, "no-lock": noLock,
		"wrong-hash": pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockWith(fixture.helper1.integrity)),
		"yarn":       {"package.json": `{"name":"x","dependencies":{"a":"1"}}`, "yarn.lock": "", "plugin.ts": "export const meta = {};"},
	}
	repo := &fakeRepo{defaultBranch: "main", branches: map[string]string{}, tags: map[string]string{}, commits: map[string]fakeCommit{}}
	for name, files := range commits {
		sha := fmt.Sprintf("%040x", len(repo.commits)+10)
		repo.branches[name] = sha
		repo.commits[sha] = fakeCommit{files: files}
	}
	fixture.hub.repos["acme/hello"] = repo
	fixture.mirror.archives[native.path] = native.archive
	fixture.primary.archives[native.path] = native.archive
	installer := fixture.installer()
	for link, reason := range map[string]string{
		"https://gitlab.com/acme/hello":                   contract.PluginInstallInvalidLink,
		"https://github.com/acme/missing":                 contract.PluginInstallRepositoryNotFound,
		"https://github.com/acme/hello/tree/nowhere":      contract.PluginInstallRefNotFound,
		"https://github.com/acme/hello/tree/no-lock":      contract.PluginInstallNoLockfile,
		"https://github.com/acme/hello/tree/native":       contract.PluginInstallNativeModule,
		"https://github.com/acme/hello/tree/wrong-hash":   contract.PluginInstallIntegrity,
		"https://github.com/acme/hello/tree/broken":       contract.PluginInstallBuildFailed,
		"https://github.com/acme/hello/tree/too-new":      contract.PluginInstallIncompatible,
		"https://github.com/acme/hello/tree/yarn":         contract.PluginInstallLockfileUnsupported,
		"https://github.com/acme/hello/tree/native/below": contract.PluginInstallNoPlugin,
	} {
		_, err := installer.Preview(context.Background(), link, nil)
		if !hasReason(err, reason) {
			t.Errorf("%s: Preview = %v, want %s", link, err, reason)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(fixture.dir, stagingDir))
	if len(entries) != 0 {
		t.Fatalf("failed previews left %d staged builds", len(entries))
	}
	fixture.hub.gitLimited = true
	if _, err := installer.Preview(context.Background(), "acme/hello", nil); !hasReason(err, contract.PluginInstallRateLimited) {
		t.Fatalf("rate limited: %v", err)
	}
}

func TestTreeLinksTellTheBranchFromTheDirectory(t *testing.T) {
	fixture := newFixture(t)
	files := map[string]string{}
	for name, text := range pluginFiles("1.1.0", "", "package-lock.json", fixture.npmLockFile()) {
		files["plugins/hello/"+name] = text
	}
	files["README.md"] = "the repository's own"
	fixture.hub.repos["acme/tools"] = &fakeRepo{
		defaultBranch: "main",
		branches:      map[string]string{"main": sha1v, "feature/x": sha2v},
		tags:          map[string]string{},
		commits:       map[string]fakeCommit{sha1v: {files: files}, sha2v: {files: files, message: "add hello\n\nlonger"}},
	}
	installer := fixture.installer()
	review, err := installer.Preview(context.Background(), "https://github.com/acme/tools/tree/feature/x/plugins/hello", nil)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if source := review.Source; source.Ref != "feature/x" || source.RefKind != contract.PluginRefBranch || source.Subdir != "plugins/hello" || source.Commit != sha2v {
		t.Fatalf("source = %+v", source)
	}
	commit, err := installer.Preview(context.Background(), "https://github.com/acme/tools/tree/"+sha1v[:10]+"/plugins/hello", nil)
	if err != nil || commit.Source.RefKind != contract.PluginRefCommit || commit.Source.Ref != "" || commit.Source.Commit != sha1v {
		t.Fatalf("commit = %+v, %v", commit.Source, err)
	}
	key, err := installer.Install(review.Token)
	if err != nil {
		t.Fatal(err)
	}
	record := readRecord(filepath.Join(fixture.dir, key))
	fixture.hub.repos["acme/tools"].branches["feature/x"] = sha1v
	check, err := installer.CheckUpdate(context.Background(), *record)
	if err != nil || !check.Available || check.RefKind != contract.PluginRefBranch || check.Commit != sha1v ||
		check.URL != "https://github.com/acme/tools/tree/feature/x/plugins/hello" {
		t.Fatalf("CheckUpdate = %+v, %v", check, err)
	}
	pinned := *record
	pinned.Source.RefKind = contract.PluginRefCommit
	if check, err := installer.CheckUpdate(context.Background(), pinned); err != nil || !check.Pinned {
		t.Fatalf("CheckUpdate of a commit = %+v, %v", check, err)
	}
}

func TestReleaseAndCommitLinksNeedNoAPI(t *testing.T) {
	fixture := newFixture(t)
	date := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	fixture.hub.repos["acme/hello"] = &fakeRepo{
		defaultBranch: "main",
		branches:      map[string]string{"main": sha2v},
		tags:          map[string]string{"v1.0.0": sha1v, "v1.1.0": sha2v},
		commits: map[string]fakeCommit{
			sha1v: {files: pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockFile()), date: date},
			sha2v: {files: pluginFiles("1.1.0", "", "package-lock.json", fixture.npmLockFile()), date: date.Add(time.Hour)},
		},
		releases: map[string]string{"v1.0.0": "first"},
	}
	fixture.hub.repos["acme/quiet"] = &fakeRepo{defaultBranch: "main", branches: map[string]string{"main": sha2v}, tags: map[string]string{}, commits: map[string]fakeCommit{}}
	fixture.hub.rateLimited = true
	installer := fixture.installer()
	for link, want := range map[string]contract.PluginInstallSource{
		// The latest release is where its page sends a browser.
		"https://github.com/acme/hello/releases/latest":      {Ref: "v1.0.0", RefKind: contract.PluginRefTag, Commit: sha1v, CommittedAt: "2026-10-02T08:00:00Z"},
		"https://github.com/acme/hello/releases/tag/v1.1.0":  {Ref: "v1.1.0", RefKind: contract.PluginRefTag, Commit: sha2v, CommittedAt: "2026-10-02T09:00:00Z"},
		"https://github.com/acme/hello/commit/" + sha1v[:12]: {RefKind: contract.PluginRefCommit, Commit: sha1v, CommittedAt: "2026-10-02T08:00:00Z"},
	} {
		review, err := installer.Preview(context.Background(), link, nil)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if got := review.Source; got.Ref != want.Ref || got.RefKind != want.RefKind || got.Commit != want.Commit || got.CommittedAt != want.CommittedAt {
			t.Fatalf("%s: source = %+v", link, got)
		}
	}
	for link, reason := range map[string]string{
		"https://github.com/acme/quiet/releases/latest":      contract.PluginInstallRefNotFound,
		"https://github.com/acme/hello/releases/tag/v9.9.9":  contract.PluginInstallRefNotFound,
		"https://github.com/acme/hello/commit/" + sha3v[:12]: contract.PluginInstallRefNotFound,
		"https://github.com/acme/gone/releases/latest":       contract.PluginInstallRepositoryNotFound,
	} {
		if _, err := installer.Preview(context.Background(), link, nil); !hasReason(err, reason) {
			t.Errorf("%s: Preview = %v, want %s", link, err, reason)
		}
	}
}

func TestParseRefsReadsLsRefs(t *testing.T) {
	head, tag, peeled := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	var answer bytes.Buffer
	for _, line := range []string{
		head + " HEAD symref-target:refs/heads/main\n",
		head + " refs/heads/main\n",
		head + " refs/heads/feature/x\n",
		tag + " refs/tags/v1.0.0 peeled:" + peeled + "\n",
		head + " refs/tags/light\n",
	} {
		answer.Write(pktLine(line))
	}
	answer.WriteString("0000")
	refs, err := parseRefs(&answer)
	if err != nil || refs.head != "main" || refs.branches["feature/x"] != head || refs.tags["v1.0.0"] != peeled || refs.tags["light"] != head {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	for name, text := range map[string]string{
		"an error":         string(pktLine("ERR access denied\n")),
		"a short answer":   string(pktLine(head + " refs/heads/main\n")),
		"not the protocol": "<!DOCTYPE html>",
		"a bad object":     string(pktLine("xyz refs/heads/main\n")) + "0000",
	} {
		if _, err := parseRefs(strings.NewReader(text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseLink(t *testing.T) {
	for text, want := range map[string]Link{
		"https://github.com/acme/hello":                      {Owner: "acme", Repo: "hello"},
		"https://github.com/acme/hello.git":                  {Owner: "acme", Repo: "hello"},
		"github.com/acme/hello/":                             {Owner: "acme", Repo: "hello"},
		"acme/hello":                                         {Owner: "acme", Repo: "hello"},
		"https://github.com/acme/hello/tags":                 {Owner: "acme", Repo: "hello"},
		"https://github.com/acme/hello/tree/main/a/b":        {Owner: "acme", Repo: "hello", Tree: []string{"main", "a", "b"}},
		"https://github.com/acme/hello/blob/main/a/pkg.json": {Owner: "acme", Repo: "hello", Tree: []string{"main", "a"}},
		"https://github.com/acme/hello/releases/tag/v1.0.0":  {Owner: "acme", Repo: "hello", Ref: "v1.0.0", Tag: true},
		"https://github.com/acme/hello/releases/latest":      {Owner: "acme", Repo: "hello", LatestRelease: true},
		"https://github.com/acme/hello/commit/ABCDEF1":       {Owner: "acme", Repo: "hello", Ref: "abcdef1"},
	} {
		got, ok := ParseLink(text)
		if !ok || got.Owner != want.Owner || got.Repo != want.Repo || got.Ref != want.Ref || got.Tag != want.Tag ||
			got.LatestRelease != want.LatestRelease || !slices.Equal(got.Tree, want.Tree) {
			t.Errorf("ParseLink(%q) = %+v, %v; want %+v", text, got, ok, want)
		}
	}
	for _, text := range []string{
		"", "https://gitlab.com/acme/hello", "https://github.com/acme", "https://github.com/acme/hello/tree",
		"https://github.com/acme/hello/tree/main/../x", "https://github.com/acme/hello/commit/zzz",
		"https://user@github.com/acme/hello", "https://github.com:8443/acme/hello", "ftp://github.com/acme/hello",
		"https://github.com/acme/hello/pulls",
	} {
		if got, ok := ParseLink(text); ok {
			t.Errorf("ParseLink(%q) = %+v, want none", text, got)
		}
	}
}

func TestArchivePathTakesThePathOnAnyRegistry(t *testing.T) {
	for _, test := range []struct{ name, version, resolved, want string }{
		{"left-pad", "1.3.0", "", "/left-pad/-/left-pad-1.3.0.tgz"},
		{"@scope/pkg", "2.0.0", "", "/@scope/pkg/-/pkg-2.0.0.tgz"},
		{"@scope/pkg", "2.0.0", "https://registry.npmjs.org/@scope/pkg/-/pkg-2.0.0.tgz", "/@scope/pkg/-/pkg-2.0.0.tgz"},
		{"pkg", "1.0.0", "https://mirrors.example.com/npm/pkg/-/pkg-1.0.0.tgz", "/pkg/-/pkg-1.0.0.tgz"},
	} {
		got, err := archivePath(test.name, test.version, test.resolved)
		if err != nil || got != test.want {
			t.Errorf("archivePath(%q, %q, %q) = %q, %v", test.name, test.version, test.resolved, got, err)
		}
	}
	for _, resolved := range []string{"git+ssh://git@github.com/a/b.git", "file:../pkg", "https://codeload.github.com/a/b/tar.gz/main"} {
		if _, err := archivePath("pkg", "1.0.0", resolved); !hasReason(err, contract.PluginInstallDependencyUnsupported) {
			t.Errorf("archivePath(%q) = %v", resolved, err)
		}
	}
}

func TestIntegrityChecksTheStrongestHash(t *testing.T) {
	data := []byte("archive")
	check, err := parseIntegrity("sha1-AAAA " + sri(data))
	if err != nil || !check(data) || check([]byte("other")) {
		t.Fatalf("parseIntegrity = %v", err)
	}
	if _, err := parseIntegrity("md5-AAAA"); err == nil {
		t.Fatal("md5 counted")
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	got := string(stripJSONC([]byte(`{"a": "x // not a comment, ]", /* gone */ "b": [1, 2,], // gone
"c": {"d": "\"}",},}`)))
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("stripJSONC = %s: %v", got, err)
	}
	if parsed["a"] != "x // not a comment, ]" || parsed["c"].(map[string]any)["d"] != `"}` {
		t.Fatalf("parsed = %v", parsed)
	}
}
