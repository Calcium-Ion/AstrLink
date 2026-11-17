package clientupdates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// npm updates on Windows that never leave a half-installed client.
//
// npm replaces a global package by first renaming the installed package
// and its command shims aside and only then downloading the new one.
// Stopped in between (the update deadline, or quitting AstrLink), it leaves
// no command at all; when the platform binary fails to download, it
// finishes anyway and leaves a stub. Here npm installs into a staging
// prefix beside the live one. Only a staged client that starts and reports
// the new version replaces the live files, by renames that are undone on
// any failure and, after a crash, on the next check.
//
// Windows moves a running program like any other file but deletes it only
// once it exits. A running client keeps working from its moved files, the
// next start finds the new version, and a later check deletes the old
// files.

const (
	// stagedWork is AstrLink's work directory inside an npm prefix, beside
	// the live files so that moving between them is a rename on the same
	// volume.
	stagedWork    = ".astrlink-update"
	stagedJournal = "journal.json"
	stagedClaim   = "claim"
	// verifyTimeout: a fresh 350 MB program can take a while on its first
	// start while antivirus scans it.
	verifyTimeout = 60 * time.Second
)

// guardedHomes are fixture homes whose tests turn on the Windows update
// guard to run its code paths with a Unix npm layout.
var guardedHomes struct {
	sync.Mutex
	homes []string
}

// guarded reports whether updates here are guarded against leaving a
// client that no longer starts: npm installs into a staging prefix that
// replaces the live files only once it works, and broken installations are
// reported with a repair. Windows only; elsewhere npm and the standalone
// updaters replace files safely.
func guarded(env *Environment) bool {
	if env.windows() {
		return true
	}
	guardedHomes.Lock()
	defer guardedHomes.Unlock()
	return slices.Contains(guardedHomes.homes, env.home)
}

// claim lets one update or repair at a time change a client's files and
// its work directory. Another AstrLink, the installed app beside a
// development build, may update the same client. The claim ends with the
// process however it ends, so a claim that can be taken means no update is
// in progress.
type claim struct {
	file   string
	handle *os.File
}

// takeClaim takes the claim on work, creating it; nil while another update
// holds it.
func takeClaim(work string) (*claim, error) {
	file := filepath.Join(work, stagedClaim)
	for attempt := 0; ; attempt++ {
		if err := os.MkdirAll(work, 0o755); err != nil {
			return nil, err
		}
		handle, err := openClaim(file)
		switch {
		case err == nil && handle == nil:
			return nil, nil
		case err == nil:
			return &claim{file, handle}, nil
		// The previous holder removed the emptied work directory.
		case errors.Is(err, os.ErrNotExist) && attempt < 3:
			continue
		}
		return nil, err
	}
}

// holdClaim takes the claim on work or fails with `busy`.
func holdClaim(work string) (*claim, *clientError) {
	c, err := takeClaim(work)
	switch {
	case err != nil:
		return nil, newError("command", err.Error())
	case c == nil:
		return nil, newError("busy", "Another update of this client is in progress")
	}
	return c, nil
}

func (c *claim) release() { releaseClaim(c.handle, c.file) }

// layout is where an npm prefix keeps a client's packages and command
// shims.
type layout struct {
	modules string
	bins    string
	// shims are the shims npm writes for the command; the first is the one
	// PATH finds.
	shims []string
}

func newLayout(env *Environment, prefix string, id ClientID) layout {
	name := id.name()
	if env.windows() {
		return layout{filepath.Join(prefix, "node_modules"), prefix, []string{name + ".cmd", name, name + ".ps1"}}
	}
	return layout{filepath.Join(prefix, "lib", "node_modules"), filepath.Join(prefix, "bin"), []string{name}}
}

func (l layout) pkg(name string) string {
	return filepath.Join(append([]string{l.modules}, strings.Split(name, "/")...)...)
}

func (l layout) shim() string { return filepath.Join(l.bins, l.shims[0]) }

// prefixOf is the npm prefix whose bins directory is directory, with links
// resolved as detection resolves an installed client: nvm-windows, for
// one, puts a link to the active Node.js on PATH.
func prefixOf(env *Environment, directory string) (string, bool) {
	resolved, ok := canonical(directory)
	if !ok {
		return "", false
	}
	if env.windows() {
		return resolved, true
	}
	if filepath.Base(resolved) != "bin" {
		return "", false
	}
	return parent(resolved)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func removeEntry(path string) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
	case info.IsDir():
		_ = os.RemoveAll(path)
	default:
		_ = os.Remove(path)
	}
}

// renameRetrying renames, retrying while antivirus or the search indexer
// briefly holds a file that was just written.
func renameRetrying(from, to string) error {
	wait := 50 * time.Millisecond
	for range 8 {
		err := os.Rename(from, to)
		if err == nil || !renameBusy(err) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, time.Second)
	}
	return os.Rename(from, to)
}

// move is one rename of a swap: [from, to].
type move [2]string

// journal is the renames of one swap, saved before the first so that an
// interrupted swap can be undone.
type journal struct {
	Run string `json:"run"`
	// Retired are live entries moved aside: [live, saved].
	Retired []move `json:"retired"`
	// Placed are entries moved into the live places: [from, live].
	Placed []move `json:"placed"`
}

func newJournal(run string) journal { return journal{Run: run, Retired: []move{}, Placed: []move{}} }

type swap struct {
	work    string
	journal journal
}

func beginSwap(work string, j journal) (*swap, *clientError) {
	text, err := json.Marshal(j)
	if err != nil {
		return nil, newError("command", err.Error())
	}
	// On disk before the first rename, so that power loss leaves either no
	// journal or a complete one.
	temporary := filepath.Join(work, stagedJournal+".tmp")
	err = func() error {
		file, err := os.Create(temporary)
		if err != nil {
			return err
		}
		if _, err := file.Write(text); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return os.Rename(temporary, filepath.Join(work, stagedJournal))
	}()
	if err != nil {
		return nil, newError("command", err.Error())
	}
	return &swap{work, j}, nil
}

func (s *swap) apply() error {
	for _, m := range s.journal.Retired {
		live, saved := m[0], m[1]
		if !exists(live) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(saved), 0o755); err != nil {
			return err
		}
		if err := renameRetrying(live, saved); err != nil {
			return err
		}
	}
	for _, m := range s.journal.Placed {
		from, live := m[0], m[1]
		if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
			return err
		}
		if err := renameRetrying(from, live); err != nil {
			return err
		}
	}
	return nil
}

// finish keeps the placed entries and deletes the retired ones. A running
// program among them stays until a check after it exits.
func (s *swap) finish() {
	_ = os.Remove(filepath.Join(s.work, stagedJournal))
	for _, m := range s.journal.Retired {
		removeEntry(m[1])
	}
}

// abort puts the live entries back and reports code, or `damaged` when
// that failed too.
func (s *swap) abort(code, detail string) *clientError {
	if err := undo(s.work, s.journal); err != nil {
		return newError("damaged", detail+"\nThe previous files could not be put back: "+err.Error())
	}
	return newError(code, detail)
}

// undo reverses the journal's renames that took place, newest first, then
// forgets the journal. Each rename either happened or not, so this also
// finishes a swap, or an earlier undo, that stopped halfway.
func undo(work string, j journal) error {
	var failed error
	for i := len(j.Placed) - 1; i >= 0; i-- {
		from, live := j.Placed[i][0], j.Placed[i][1]
		if !exists(from) && exists(live) {
			if err := renameRetrying(live, from); err != nil && failed == nil {
				failed = err
			}
		}
	}
	// Put nothing back until every new entry has left: a later undo takes
	// what it finds in a live place for a new entry whose source is gone.
	if failed != nil {
		return failed
	}
	for i := len(j.Retired) - 1; i >= 0; i-- {
		live, saved := j.Retired[i][0], j.Retired[i][1]
		if !exists(saved) {
			continue
		}
		if exists(live) {
			aside := filepath.Join(j.Run, fmt.Sprintf("discard-%d", i))
			if err := renameRetrying(live, aside); err != nil {
				if failed == nil {
					failed = err
				}
				continue
			}
		}
		if err := renameRetrying(saved, live); err != nil && failed == nil {
			failed = err
		}
	}
	if failed != nil {
		return failed
	}
	_ = os.Remove(filepath.Join(work, stagedJournal))
	return nil
}

// recoverWork undoes a swap that AstrLink did not finish, then deletes what
// earlier runs left. The caller holds the claim on work.
func recoverWork(work string) *clientError {
	path := filepath.Join(work, stagedJournal)
	if raw, err := os.ReadFile(path); err == nil {
		var j journal
		if err := json.Unmarshal(raw, &j); err != nil {
			return newErrorf("damaged", "%s: %v", path, err)
		}
		if err := undo(work, j); err != nil {
			return newErrorf("damaged", "An interrupted update could not be undone: %v", err)
		}
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if name := entry.Name(); name != "" && digits(name) && entry.IsDir() {
			removeEntry(filepath.Join(work, name))
		}
	}
	return nil
}

func workDir(prefix string, id ClientID) string {
	return filepath.Join(prefix, stagedWork, id.name())
}

// tidy removes the work directories once nothing is left in them.
func tidy(prefix string, id ClientID) {
	if os.Remove(workDir(prefix, id)) == nil {
		_ = os.Remove(filepath.Join(prefix, stagedWork))
	}
}

// recoverAll undoes interrupted swaps in every prefix on PATH. It runs
// before detection, so that a client caught mid-swap is found as it was
// before the update.
func recoverAll(env *Environment, id ClientID) {
	for _, directory := range filepath.SplitList(env.path) {
		prefix, ok := prefixOf(env, directory)
		if !ok {
			continue
		}
		work := workDir(prefix, id)
		if info, err := os.Stat(work); err != nil || !info.IsDir() {
			continue
		}
		// An update in progress elsewhere cleans up after itself.
		c, err := takeClaim(work)
		if err != nil || c == nil {
			continue
		}
		failure := recoverWork(work)
		c.release()
		if failure != nil {
			log.Printf("astrlink: unable to undo an update of %s: %s", id.name(), failure.detail)
			continue
		}
		tidy(prefix, id)
	}
}

// within reports whether path is root or lies below it.
func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// sameVersion reports whether a and b are the same release, printed the
// same way.
func sameVersion(a, b clientVersion) bool { return a.text == b.text && a.order.compare(b.order) == 0 }

// verifyStaged is the client in l's prefix, if it starts there and reports
// expected (when given).
func verifyStaged(env *Environment, id ClientID, l layout, expected *clientVersion) (clientVersion, *clientError) {
	scoped := *env
	scoped.path = l.bins + string(os.PathListSeparator) + env.path
	install := detect(&scoped, id)
	if install == nil || filepath.Dir(install.executable) != l.bins {
		return clientVersion{}, newErrorf("incomplete", "%s was not installed", l.shim())
	}
	if !within(install.resolved, l.modules) {
		return clientVersion{}, newErrorf("incomplete", "%s does not start the installed package", install.executable)
	}
	version, failure := versionWithin(&scoped, id, install, verifyTimeout)
	if failure != nil {
		return clientVersion{}, newErrorf("incomplete", "%s did not start: %s", install.executable, failure.detail)
	}
	if expected != nil && !sameVersion(version, *expected) {
		return clientVersion{}, newErrorf("incomplete", "%s reports %s instead of %s", install.executable, version, *expected)
	}
	return version, nil
}

func runName() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }

// stagedUpdate installs latest into a staging prefix, checks it, then
// swaps it in. It also repairs a broken installation: whatever is live is
// replaced.
func stagedUpdate(env *Environment, id ClientID, prefix string, latest clientVersion) (clientVersion, *clientError) {
	work := workDir(prefix, id)
	c, failure := holdClaim(work)
	if failure != nil {
		return clientVersion{}, failure
	}
	// Named after recoverWork, which deletes earlier runs, ran.
	runDir := filepath.Join(work, runName())
	version, failure := func() (clientVersion, *clientError) {
		if failure := recoverWork(work); failure != nil {
			return clientVersion{}, failure
		}
		live := newLayout(env, prefix, id)
		stage := filepath.Join(runDir, "stage")
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return clientVersion{}, newError("command", err.Error())
		}
		spec, failure := npmCommand(env, id, prefix, stage, latest)
		if failure != nil {
			return clientVersion{}, failure
		}
		output, failure := run(env, spec, updateTimeout)
		if failure != nil {
			return clientVersion{}, failure
		}
		staged := newLayout(env, stage, id)
		if _, failure := verifyStaged(env, id, staged, &latest); failure != nil {
			return clientVersion{}, newError("incomplete", strings.TrimSpace(failure.detail+"\n"+strings.TrimSpace(output)))
		}
		j := newJournal(runDir)
		retireLive(live, id, runDir, &j)
		for _, name := range id.packages() {
			if from := staged.pkg(name); exists(from) {
				j.Placed = append(j.Placed, move{from, live.pkg(name)})
			}
		}
		for _, shim := range staged.shims {
			if from := filepath.Join(staged.bins, shim); exists(from) {
				j.Placed = append(j.Placed, move{from, filepath.Join(live.bins, shim)})
			}
		}
		return swapIn(env, id, live, work, j, &latest, "incomplete")
	}()
	finishRun(prefix, id, runDir, c)
	return version, failure
}

// finishRun ends a run: deletes it unless a swap could not be undone,
// whose journal still needs it, then lets go of the client and tidies up.
func finishRun(prefix string, id ClientID, runDir string, c *claim) {
	if !exists(filepath.Join(workDir(prefix, id), stagedJournal)) {
		removeEntry(runDir)
	}
	c.release()
	tidy(prefix, id)
}

// retireLive plans moving the live packages and shims into runDir.
func retireLive(l layout, id ClientID, runDir string, j *journal) {
	saved := filepath.Join(runDir, "previous")
	for i, name := range id.packages() {
		if live := l.pkg(name); exists(live) {
			j.Retired = append(j.Retired, move{live, filepath.Join(saved, fmt.Sprintf("package-%d", i))})
		}
	}
	for _, shim := range l.shims {
		if live := filepath.Join(l.bins, shim); exists(live) {
			j.Retired = append(j.Retired, move{live, filepath.Join(saved, shim)})
		}
	}
}

// swapIn applies j, checks the client, and keeps the result or undoes it.
func swapIn(env *Environment, id ClientID, l layout, work string, j journal, expected *clientVersion, unverified string) (clientVersion, *clientError) {
	s, failure := beginSwap(work, j)
	if failure != nil {
		return clientVersion{}, failure
	}
	if err := s.apply(); err != nil {
		// A running program moves, but not a file some program opened
		// without letting others move it.
		var paths []string
		for _, m := range append(slices.Clone(s.journal.Retired), s.journal.Placed...) {
			paths = append(paths, m[0], m[1])
		}
		code, detail := "command", err.Error()
		if running := refuseRunning(paths); running != nil {
			code, detail = "running", err.Error()+"\n"+running.detail
		} else if errors.Is(err, fs.ErrPermission) {
			code = "permission"
		}
		return clientVersion{}, s.abort(code, detail)
	}
	// npm's cache of the global tree now describes the old files.
	_ = os.Remove(filepath.Join(l.modules, ".package-lock.json"))
	version, failure := verifyStaged(env, id, l, expected)
	if failure != nil {
		return clientVersion{}, s.abort(unverified, failure.detail)
	}
	s.finish()
	removeNpmLeftovers(l, id)
	return version, nil
}

// npmRetired reports whether entry is npm's name for a retired name:
// `.<name>-<8 letters or digits>` beside it.
func npmRetired(entry, name string) bool {
	rest, ok := strings.CutPrefix(entry, ".")
	if !ok {
		return false
	}
	if rest, ok = strings.CutPrefix(rest, name); !ok {
		return false
	}
	if rest, ok = strings.CutPrefix(rest, "-"); !ok || len(rest) != 8 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if !isAlnum(rest[i]) {
			return false
		}
	}
	return true
}

// retiredCopies are npm's retired copies of path, newest first.
func retiredCopies(path string) []string {
	directory, name := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	type copy struct {
		modified time.Time
		path     string
	}
	var found []copy
	for _, entry := range entries {
		if !npmRetired(entry.Name(), name) {
			continue
		}
		var modified time.Time
		if info, err := entry.Info(); err == nil {
			modified = info.ModTime()
		}
		found = append(found, copy{modified, filepath.Join(directory, entry.Name())})
	}
	slices.SortStableFunc(found, func(a, b copy) int { return b.modified.Compare(a.modified) })
	paths := make([]string, len(found))
	for i, c := range found {
		paths[i] = c.path
	}
	return paths
}

// removeNpmLeftovers deletes what an interrupted npm left of the client,
// once it starts again. A file still in use stays until a later update.
func removeNpmLeftovers(l layout, id ClientID) {
	var paths []string
	for _, name := range id.packages() {
		paths = append(paths, l.pkg(name))
	}
	for _, shim := range l.shims {
		paths = append(paths, filepath.Join(l.bins, shim))
	}
	for _, path := range paths {
		for _, copy := range retiredCopies(path) {
			removeEntry(copy)
		}
	}
}

// binEntry is the command a package's manifest installs for id.
func binEntry(env *Environment, dir string, id ClientID) (string, bool) {
	manifest := env.readJSON(filepath.Join(dir, "package.json"))
	if manifest == nil {
		return "", false
	}
	entry, ok := manifest["bin"].(string)
	if !ok {
		bins, _ := manifest["bin"].(map[string]any)
		if entry, ok = bins[id.name()].(string); !ok {
			return "", false
		}
	}
	path := dir
	for _, part := range strings.FieldsFunc(entry, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part != "." && part != ".." {
			path = filepath.Join(path, part)
		}
	}
	return path, true
}

// completePackage is the package's version if its command file is in
// place, else what is wrong.
func completePackage(env *Environment, dir string, id ClientID) (string, error) {
	raw, ok := env.read(filepath.Join(dir, "package.json"))
	if !ok {
		return "", fmt.Errorf("%s has no package.json", dir)
	}
	value, err := format.DecodeJSON(raw)
	if err != nil {
		return "", err
	}
	manifest, _ := value.(map[string]any)
	version, ok := manifest["version"].(string)
	if !ok {
		version = "?"
	}
	entry, ok := binEntry(env, dir, id)
	if !ok {
		return "", fmt.Errorf("%s names no %s command", dir, id.name())
	}
	if !isFile(entry) {
		return "", fmt.Errorf("%s is missing", entry)
	}
	// Claude Code keeps a text stub at bin/claude.exe until its native
	// binary is in place.
	if env.windows() && strings.EqualFold(extension(entry), "exe") {
		if image, err := windowsImage(entry); err != nil || !image {
			return "", fmt.Errorf("%s is not a Windows program", entry)
		}
	}
	return version, nil
}

// restoreCopy is a previous copy npm left aside that can be put back.
type restoreCopy struct {
	version string
	pkg     string
	from    string
	// shims are missing live shims and the retired copies that replace
	// them: [copy, live].
	shims []move
}

// damage is an npm installation that cannot start.
type damage struct {
	prefix     string
	executable string
	restore    *restoreCopy
	detail     string
}

// findDamageIn is what is wrong with the client's npm installation in
// prefix; nil when nothing of it is there, or it looks complete and did
// not fail to start. failed is how the client found there failed to
// report its version.
func findDamageIn(env *Environment, id ClientID, prefix string, failed *clientError) *damage {
	l := newLayout(env, prefix, id)
	live := ""
	for _, name := range id.packages() {
		if found, ok := env.manifestName(l.pkg(name)); ok && found == name {
			live = l.pkg(name)
			break
		}
	}
	// The newest complete copy npm retired; of equal versions, the last
	// one found.
	var previous *restoreCopy
	var best *semver
	for _, name := range id.packages() {
		for _, dir := range retiredCopies(l.pkg(name)) {
			if found, ok := env.manifestName(dir); !ok || found != name {
				continue
			}
			version, err := completePackage(env, dir, id)
			if err != nil {
				continue
			}
			var key *semver
			if order, ok := parseSemver(version); ok {
				key = &order
			}
			if previous != nil && compareKeys(key, best) < 0 {
				continue
			}
			previous, best = &restoreCopy{version: version, pkg: name, from: dir}, key
		}
	}
	if live == "" && previous == nil {
		return nil
	}
	// Only a start that failed for files missing from the package is
	// damage. A slow first start while antivirus scans the program, a
	// missing or too old Node.js, or a client's own error are not, and
	// installing it again would not help.
	var notes []string
	if failed != nil && missingFiles(failed) {
		notes = append(notes, failed.detail)
	}
	shim := l.shim()
	if !exists(shim) {
		notes = append(notes, shim+" is missing")
	}
	if live != "" {
		if _, err := completePackage(env, live, id); err != nil {
			notes = append(notes, err.Error())
		}
	} else {
		notes = append(notes, l.pkg(id.packages()[0])+" is missing")
	}
	// A stub found by both the failed start and the file check is one
	// note.
	var unique []string
	for _, note := range notes {
		if !slices.Contains(unique, note) {
			unique = append(unique, note)
		}
	}
	notes = unique
	if len(notes) == 0 {
		return nil
	}
	if previous != nil {
		for i, name := range l.shims {
			live := filepath.Join(l.bins, name)
			if exists(live) {
				continue
			}
			copies := retiredCopies(live)
			if len(copies) > 0 {
				previous.shims = append(previous.shims, move{copies[0], live})
			} else if i == 0 {
				// Without the command PATH finds, putting back is no
				// repair.
				previous = nil
				break
			}
		}
		if previous != nil {
			notes = append(notes, fmt.Sprintf("The previous version %s is still in %s", previous.version, previous.from))
		}
	}
	// A copy npm left behind once, after updating a running client, is no
	// sign of an installation when the package itself is gone: it may have
	// been uninstalled. An interrupted update leaves its shims aside too.
	if live == "" && previous == nil {
		return nil
	}
	return &damage{prefix: prefix, executable: shim, restore: previous, detail: strings.Join(notes, "\n")}
}

// compareKeys orders versions npm may not parse, which come first.
func compareKeys(a, b *semver) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return a.compare(*b)
}

// missingFiles reports whether a client failed to start because files of
// its package are missing: a stub left where Claude Code's native program
// belongs, or a module or platform package npm did not install.
func missingFiles(failure *clientError) bool {
	return failure.code == "damaged" || failure.code == "command" &&
		containsAny(failure.detail, []string{"native binary not installed", "Missing optional dependency", "Cannot find module"})
}

// findDamage is a broken npm installation in a prefix on PATH, for a
// client PATH no longer finds. Only a prefix npm installs global commands
// into counts, not a project folder that happens to be on PATH.
func findDamage(env *Environment, id ClientID) *damage {
	var seen []string
	for _, directory := range filepath.SplitList(env.path) {
		if !filepath.IsAbs(directory) {
			continue
		}
		prefix, ok := prefixOf(env, directory)
		if !ok || slices.Contains(seen, prefix) {
			continue
		}
		seen = append(seen, prefix)
		if !globalPrefix(env, prefix) {
			continue
		}
		if found := findDamageIn(env, id, prefix, nil); found != nil {
			return found
		}
	}
	return nil
}

// globalPrefix reports whether npm installs global commands into prefix:
// it holds npm itself, or command shims npm wrote there, retired ones
// included.
func globalPrefix(env *Environment, prefix string) bool {
	l := newLayout(env, prefix, ClientClaude)
	if isFile(filepath.Join(l.modules, "npm", "bin", "npm-cli.js")) {
		return true
	}
	entries, err := os.ReadDir(l.bins)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		path := filepath.Join(l.bins, entry.Name())
		target, err := os.Readlink(path)
		if err != nil {
			name := strings.ToLower(entry.Name())
			info, err := entry.Info()
			if err != nil || info.Size() >= 16_384 || !strings.Contains(name, ".cmd") && !strings.Contains(name, ".ps1") {
				continue
			}
			data, _ := os.ReadFile(path)
			target = string(data)
		}
		if strings.Contains(target, "node_modules") {
			return true
		}
	}
	return false
}

// restoreDamage puts back the copy npm retired when it was stopped.
func restoreDamage(env *Environment, id ClientID, d *damage) (clientVersion, *clientError) {
	if d.restore == nil {
		return clientVersion{}, newError("unsupported", "Nothing to put back")
	}
	work := workDir(d.prefix, id)
	c, failure := holdClaim(work)
	if failure != nil {
		return clientVersion{}, failure
	}
	runDir := filepath.Join(work, runName())
	version, failure := func() (clientVersion, *clientError) {
		if failure := recoverWork(work); failure != nil {
			return clientVersion{}, failure
		}
		return putBack(env, id, d.prefix, runDir, d.restore)
	}()
	finishRun(d.prefix, id, runDir, c)
	return version, failure
}

func putBack(env *Environment, id ClientID, prefix, runDir string, r *restoreCopy) (clientVersion, *clientError) {
	if !exists(r.from) {
		return clientVersion{}, newError("changed", "Client installation changed")
	}
	l := newLayout(env, prefix, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return clientVersion{}, newError("command", err.Error())
	}
	j := newJournal(runDir)
	// Only the package moves; live shims are left as they are.
	for i, name := range id.packages() {
		if live := l.pkg(name); exists(live) {
			j.Retired = append(j.Retired, move{live, filepath.Join(runDir, "previous", fmt.Sprintf("package-%d", i))})
		}
	}
	j.Placed = append(j.Placed, move{r.from, l.pkg(r.pkg)})
	j.Placed = append(j.Placed, r.shims...)
	var expected *clientVersion
	if version, ok := parseClientVersion(id, r.version); ok {
		expected = &version
	}
	// Nothing was downloaded; a copy that does not start is not a network
	// problem.
	return swapIn(env, id, l, workDir(prefix, id), j, expected, "command")
}
