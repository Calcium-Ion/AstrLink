package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
	"github.com/MoeForge/moejs/node"
)

// loaderName is the loader's name in stack traces.
const loaderName = "astrlink:node"

// nodeLoader is node/loader.js, compiled once.
var nodeLoader = sync.OnceValues(func() (*moejs.Script, error) {
	source, err := nodeFiles.ReadFile("node/loader.js")
	if err != nil {
		return nil, err
	}
	return moejs.CompileScript(loaderName, string(source))
})

// DefaultDataBytes is what a plugin's data directory may hold unless Limits
// say otherwise.
const DefaultDataBytes = 1 << 30

// pluginDirs are the directories a plugin reaches through node:fs: its data
// directory, which it reads and writes and which is its working directory,
// and its install directory, which it reads. Every runtime of the plugin
// shares them.
type pluginDirs struct {
	// data and install are absolute paths without symbolic links above
	// them; install is "" for a plugin with no files on disk.
	data    string
	install string
	// quota is what data may hold.
	quota int64
	// temporary is set for the data directory of a check, which goes when
	// the check ends.
	temporary bool

	mutex sync.Mutex
	// made is set once data exists.
	made bool
	// used is what data held when counted, plus what was written since.
	used    int64
	counted time.Time
}

// recount is how long a count of the data directory holds before writes
// count it again.
const recount = 30 * time.Second

// newPluginDirs returns a plugin's directories: data, which holds at most
// quota bytes, and install, "" for a plugin with no files on disk.
func newPluginDirs(data, install string, quota int64) *pluginDirs {
	dirs := &pluginDirs{data: data, install: install, quota: quota}
	if dirs.quota <= 0 {
		dirs.quota = DefaultDataBytes
	}
	return dirs
}

// temporaryDirs returns directories for a plugin checked before it is
// installed: a data directory that close removes, and install, which may be
// "".
func temporaryDirs(install string) *pluginDirs {
	parent := filepath.Join(os.TempDir(), fmt.Sprintf("astrlink-plugin-check-%d-%d", os.Getpid(), checkCounter.Add(1)))
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		parent = filepath.Join(resolved, filepath.Base(parent))
	}
	return &pluginDirs{data: parent, install: install, quota: DefaultDataBytes, temporary: true}
}

// close removes a temporary data directory.
func (dirs *pluginDirs) close() {
	if dirs.temporary {
		_ = os.RemoveAll(dirs.data)
	}
}

// canonicalDir returns dir with the symbolic links above it resolved, making
// it first when make is set, so that the paths a plugin is given match what
// realpath returns.
func canonicalDir(dir string, make bool) string {
	if dir == "" {
		return ""
	}
	if make {
		_ = os.MkdirAll(dir, 0o700)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	if absolute, err := filepath.Abs(dir); err == nil {
		return absolute
	}
	return dir
}

// tmp is os.tmpdir() of the plugin, in its data directory.
func (dirs *pluginDirs) tmp() string { return filepath.Join(dirs.data, ".tmp") }

// root opens the data directory, making it first, or the install directory.
// Each operation opens its own and closes it, so that no handle keeps a
// directory that an update or an uninstall replaces.
func (dirs *pluginDirs) root(data bool) (*os.Root, error) {
	if !data {
		return os.OpenRoot(dirs.install)
	}
	if err := dirs.make(); err != nil {
		return nil, err
	}
	return os.OpenRoot(dirs.data)
}

// make makes the data directory and its .tmp the first time an operation
// reaches it.
func (dirs *pluginDirs) make() error {
	dirs.mutex.Lock()
	defer dirs.mutex.Unlock()
	if !dirs.made {
		if err := os.MkdirAll(dirs.data, 0o700); err != nil {
			return err
		}
		_ = os.Mkdir(dirs.tmp(), 0o700)
		dirs.made = true
	}
	return nil
}

// allow is the policy of the node:fs of moejs, which works on the
// directory that holds the data directory and sees each path with its
// links resolved: the plugin reaches its data directory and nothing beside
// it, and writes there until the directory holds its quota, as the last
// count, at most a second old, found.
func (dirs *pluginDirs) allow(op, path string) error {
	if _, ok := within(dirs.data, filepath.FromSlash(path)); !ok {
		return errOutside
	}
	if err := dirs.make(); err != nil {
		return err
	}
	if op == "write" && dirs.fullSince(time.Second) {
		return syscall.ENOSPC
	}
	return nil
}

// errOutside is the error of a path outside the plugin's directories.
var errOutside = errors.New("plugins read and write only their own directories")

// errReadOnly is the error of a write to the install directory.
var errReadOnly = errors.New("the plugin's install directory is read-only")

// fsLocation is a path resolved into one of the plugin's directories, with
// the directory opened for one operation.
type fsLocation struct {
	root     *os.Root
	rel      string
	abs      string
	writable bool
}

// close closes the directory the location opened.
func (at fsLocation) close() {
	if at.root != nil {
		_ = at.root.Close()
	}
}

// resolve maps a path the plugin names into the directory it is in: a
// relative path is relative to the data directory, the working directory.
// Paths elsewhere are errOutside.
func (dirs *pluginDirs) resolve(name string) (fsLocation, error) {
	abs := name
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dirs.data, abs)
	}
	abs = filepath.Clean(abs)
	if rel, ok := within(dirs.data, abs); ok {
		root, err := dirs.root(true)
		if err != nil {
			return fsLocation{abs: abs}, err
		}
		return fsLocation{root: root, rel: rel, abs: abs, writable: true}, nil
	}
	if dirs.install != "" {
		if rel, ok := within(dirs.install, abs); ok {
			root, err := dirs.root(false)
			if err != nil {
				return fsLocation{abs: abs}, err
			}
			return fsLocation{root: root, rel: rel, abs: abs}, nil
		}
	}
	return fsLocation{abs: abs}, errOutside
}

// within returns target relative to base when it is base or under it.
func within(base, target string) (string, bool) {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// reserve counts n more bytes against the quota of the data directory, and
// fails with ENOSPC when it would hold more.
func (dirs *pluginDirs) reserve(n int64) error {
	if n <= 0 {
		return nil
	}
	dirs.mutex.Lock()
	defer dirs.mutex.Unlock()
	if dirs.counted.IsZero() || time.Since(dirs.counted) > recount {
		dirs.used = directorySize(dirs.data)
		dirs.counted = time.Now()
	}
	if dirs.used+n > dirs.quota {
		return errQuota
	}
	dirs.used += n
	return nil
}

// full reports whether the data directory holds its quota already, for
// writes whose size is not known before, such as node:sqlite's.
func (dirs *pluginDirs) full() bool { return dirs.fullSince(recount) }

// fullSince is full with a count at most age old.
func (dirs *pluginDirs) fullSince(age time.Duration) bool {
	dirs.mutex.Lock()
	defer dirs.mutex.Unlock()
	if dirs.counted.IsZero() || time.Since(dirs.counted) > age {
		dirs.used = directorySize(dirs.data)
		dirs.counted = time.Now()
	}
	return dirs.used >= dirs.quota
}

// free is what the data directory may still take.
func (dirs *pluginDirs) free() int64 {
	if dirs.full() {
		return 0
	}
	dirs.mutex.Lock()
	defer dirs.mutex.Unlock()
	return dirs.quota - dirs.used
}

// released marks the count of the data directory stale after something
// shrank it.
func (dirs *pluginDirs) released() {
	dirs.mutex.Lock()
	dirs.counted = time.Time{}
	dirs.mutex.Unlock()
}

// errQuota is the error of a write past the quota.
var errQuota = errors.New("quota")

// directorySize is what the files under dir hold.
func directorySize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

// nodeState is what the node: modules keep in one runtime.
type nodeState struct {
	// bound are the host namespaces made so far.
	bound map[string]moejs.Value
}

// nodeResources are what a runtime holds open for the node: modules. They
// refer to nothing of the runtime, so that a cleanup can close them once
// the runtime is gone.
type nodeResources struct {
	// files are the open file descriptors, closed when a call ends.
	files  map[int]*nodeFile
	nextFD int
	// databases are the open node:sqlite databases, which live as long as
	// the runtime.
	databases map[int]*nodeDatabase
	nextDB    int
}

// endCall closes what a call left open: its file descriptors.
func (resources *nodeResources) endCall() {
	for fd, file := range resources.files {
		_ = file.file.Close()
		delete(resources.files, fd)
	}
}

// close closes everything the runtime holds open.
func (resources *nodeResources) close() {
	resources.endCall()
	for id, database := range resources.databases {
		database.close()
		delete(resources.databases, id)
	}
}

// officialFS reports whether node:fs runs on moejs's for the paths of the
// data directory: for apiVersion 2 plugins, whose calls run the loop its
// callbacks and promises settle on, where paths are POSIX paths, which its
// mount is.
func (loaded *program) officialFS() bool {
	return loaded.async() && filepath.Separator == '/'
}

// installFiles installs moejs's node:fs and Buffer (package node) on the
// runtime's loop. Its node:fs works on the directory that holds the data
// directory, which it sees at its own path, and reaches only the data
// directory (pluginDirs.allow); node/fs.js sends it the operations it
// implements on the data directory's paths and does the rest itself.
func (bound *instance) installFiles() error {
	var options node.Options
	if dirs := bound.program.dirs; bound.program.officialFS() {
		parent := filepath.Dir(dirs.data)
		if err := os.MkdirAll(parent, 0o700); err == nil {
			options.FS = &node.FSOptions{Root: parent, Mount: filepath.ToSlash(parent), Cwd: filepath.ToSlash(dirs.data), Write: true, Allow: dirs.allow}
		}
	}
	api, err := node.Install(nodeLoop{bound}, options)
	if err != nil {
		return err
	}
	bound.files = api
	return nil
}

// nodeLoop is the runtime's loop as package node needs it: the operations
// of node:fs settle as tasks of the call, its script time.
type nodeLoop struct{ bound *instance }

func (loop nodeLoop) Runtime() *moejs.Runtime { return loop.bound.runtime }

func (loop nodeLoop) Post(task func(*moejs.Runtime) error) bool { return loop.bound.post(task) }

func (loop nodeLoop) Hold() func() {
	loop.bound.queue()
	done := loop.bound.loop.Hold()
	return func() { done(nil) }
}

// official returns what the module node:<name> of package node exports in
// this runtime, undefined when it has none. The package hands a module's
// binding to its import.meta, which it fills for the runtime it is
// installed in; the CommonJS loader needs it without an import.
func (bound *instance) official(realm *moejs.Realm, name string) (moejs.Value, error) {
	if bound.files == nil || (name != "buffer" && !bound.program.officialFS()) {
		return moejs.Undefined(), nil
	}
	module, err := node.Resolver(nil)(nil, "node:"+name)
	if err != nil {
		return moejs.Undefined(), nil
	}
	hooks := realm.ImportHooks()
	if hooks == nil || hooks.Meta == nil {
		return moejs.Undefined(), nil
	}
	meta := realm.NewObject()
	if err := hooks.Meta(realm, module, meta); err != nil {
		return moejs.Undefined(), nil
	}
	return bound.runtime.Get(engine.ObjectValue(meta), "binding")
}

// installNode adds the globals of Node and the loader of the node: modules.
func (bound *instance) installNode() error {
	if err := bound.installFiles(); err != nil {
		return err
	}
	runtime := bound.runtime
	names := make([]any, 0, len(nodeModules))
	for _, entry := range nodeNames() {
		name := entry[0].(string)
		names = append(names, []any{name, entry[1], entry[2], strings.Fields(nodeModules[name].exports), hasNodeFactory(name)})
	}
	filename := bound.program.filename()
	host := map[string]any{
		"names": names,
		"factory": runtime.Function("factory", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "module name")
			if err != nil {
				return moejs.Undefined(), err
			}
			script, err := nodeFactory(name)
			if err != nil {
				return moejs.Undefined(), err
			}
			return runtime.RunScript(script)
		}),
		"bind": runtime.Function("bind", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "namespace")
			if err != nil {
				return moejs.Undefined(), err
			}
			return bound.bindNode(realm, name)
		}),
		"filename": filename,
		"dirname":  filepath.Dir(filename),
		"data":     bound.program.dirs.data,
		"official": runtime.Function("official", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "module name")
			if err != nil {
				return moejs.Undefined(), err
			}
			return bound.official(realm, name)
		}),
		"timeOrigin": float64(processStart.UnixMicro()) / 1000,
		"now": runtime.Function("now", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.Number(float64(time.Since(processStart).Microseconds()) / 1000), nil
		}),
		// promiseDetails gives util.inspect the state of a promise: [0]
		// pending, [1, value] fulfilled, [2, reason] rejected, undefined
		// for anything else.
		"promiseDetails": runtime.Function("promiseDetails", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			state, result, ok := moejs.PromiseResult(moejs.Arg(args, 0))
			if !ok {
				return moejs.Undefined(), nil
			}
			code := 0
			switch state {
			case moejs.PromiseFulfilled:
				code = 1
			case moejs.PromiseRejected:
				code = 2
			}
			return engine.ObjectValue(realm.NewArray(moejs.Int(int64(code)), result)), nil
		}),
	}
	script, err := nodeLoader()
	if err != nil {
		return fmt.Errorf("compile the loader of the node: modules: %w", err)
	}
	if err := runWith(runtime, script, host); err != nil {
		return fmt.Errorf("run the loader of the node: modules: %w", err)
	}
	return nil
}

// bindNode returns the host functions of a node: module, made once per
// runtime.
func (bound *instance) bindNode(realm *moejs.Realm, name string) (moejs.Value, error) {
	if value, ok := bound.node.bound[name]; ok {
		return value, nil
	}
	var functions map[string]any
	switch name {
	case "process":
		functions = bound.processFunctions()
	case "os":
		functions = bound.osFunctions()
	case "fs":
		functions = bound.fsFunctions()
	case "crypto":
		functions = bound.cryptoFunctions()
	case "zlib":
		functions = bound.zlibFunctions()
	case "sqlite":
		functions = bound.sqliteFunctions()
	default:
		return moejs.Undefined(), realm.TypeError("the host has no functions for %s", name)
	}
	value, err := realm.FromGo(functions)
	if err != nil {
		return moejs.Undefined(), err
	}
	if bound.node.bound == nil {
		bound.node.bound = make(map[string]moejs.Value)
	}
	bound.node.bound[name] = value
	return value, nil
}

// function makes a host function of a node: module.
func (bound *instance) function(name string, length int, fn moejs.NativeFunc) moejs.Value {
	return bound.runtime.Function(name, length, fn)
}

// blocking runs fn, which waits on the disk: the wait is not script time.
func (bound *instance) blocking(fn func()) {
	if c := bound.current; c != nil && c.watchdog != nil && c.watchdog.running() {
		c.watchdog.pause()
		defer c.watchdog.start()
	}
	fn()
}

// nodePlatform is process.platform.
func nodePlatform() string {
	if goruntime.GOOS == "windows" {
		return "win32"
	}
	return goruntime.GOOS
}

// nodeArch is process.arch.
func nodeArch() string {
	switch goruntime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	}
	return goruntime.GOARCH
}

// processFunctions are the host's side of process.
func (bound *instance) processFunctions() map[string]any {
	dirs := bound.program.dirs
	return map[string]any{
		"info": bound.function("info", 0, func(realm *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			env := map[string]any{
				"NODE_ENV": "production",
				"HOME":     dirs.data,
				"TMPDIR":   dirs.tmp(),
				"TZ":       realm.TimeZone().String(),
			}
			if goruntime.GOOS == "windows" {
				env["USERPROFILE"], env["TEMP"], env["TMP"] = dirs.data, dirs.tmp(), dirs.tmp()
			}
			return realm.FromGo(map[string]any{
				"platform": nodePlatform(),
				"arch":     nodeArch(),
				"pid":      os.Getpid(),
				"ppid":     os.Getppid(),
				"cwd":      dirs.data,
				"version":  nodeVersion,
				"env":      env,
				"filename": bound.program.filename(),
			})
		}),
		"hrtime": bound.function("hrtime", 0, func(realm *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			since := time.Since(processStart)
			return realm.FromGo([]any{int64(since / time.Second), int64(since % time.Second)})
		}),
		"uptime": bound.function("uptime", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.Number(time.Since(processStart).Seconds()), nil
		}),
		"memory": bound.function("memory", 0, func(realm *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			stats := bound.runtime.Stats()
			return realm.FromGo(map[string]any{
				"rss":          stats.AllocatedBytes,
				"heapTotal":    realm.MemoryLimit(),
				"heapUsed":     stats.RequestAllocatedBytes,
				"external":     0,
				"arrayBuffers": 0,
			})
		}),
		"write": bound.function("write", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				// A module's top level has no call to log for.
				return moejs.Undefined(), nil
			}
			level, err := stringArg(realm, args, 0, "level")
			if err != nil {
				return moejs.Undefined(), err
			}
			text, err := stringArg(realm, args, 1, "text")
			if err != nil {
				return moejs.Undefined(), err
			}
			text = strings.TrimRight(text, "\r\n")
			if text == "" {
				return moejs.Undefined(), nil
			}
			return hostLog(c, realm, []moejs.Value{moejs.String(level), moejs.String(text)})
		}),
	}
}

// nodeError is an error of Node's file system functions: an Error whose
// message is "<code>: <description>, <syscall> '<path>'", with code, errno,
// syscall and path, and dest for the functions of two paths.
func nodeError(realm *moejs.Realm, code, syscall, path, dest, detail string) error {
	message := code + ": " + errnoDescriptions[code] + ", " + syscall
	if path != "" {
		message += " '" + path + "'"
	}
	if dest != "" {
		message += " -> '" + dest + "'"
	}
	if detail != "" {
		message += " (" + detail + ")"
	}
	object := realm.NewError(engine.KindError, "%s", message)
	set := func(key string, value moejs.Value) {
		_, _ = object.CreateDataProperty(realm, realm.KeyFromGoString(key), value)
	}
	set("errno", moejs.Int(-errnoNumbers[code]))
	set("code", moejs.String(code))
	set("syscall", moejs.String(syscall))
	if path != "" {
		set("path", moejs.String(path))
	}
	if dest != "" {
		set("dest", moejs.String(dest))
	}
	return realm.Throw(engine.ObjectValue(object))
}

// errnoNumbers are the POSIX numbers of the codes, errno negated as Node
// reports it.
var errnoNumbers = map[string]int64{
	"EPERM": 1, "ENOENT": 2, "EIO": 5, "EBADF": 9, "EAGAIN": 11, "EACCES": 13, "EBUSY": 16,
	"EEXIST": 17, "EXDEV": 18, "ENOTDIR": 20, "EISDIR": 21, "EINVAL": 22, "ENFILE": 23,
	"EMFILE": 24, "ETXTBSY": 26, "EFBIG": 27, "ENOSPC": 28, "EROFS": 30, "EMLINK": 31,
	"ENAMETOOLONG": 36, "ENOSYS": 38, "ENOTEMPTY": 39, "ELOOP": 40, "ENOTSUP": 95,
}

// errnoDescriptions are libuv's descriptions of the codes.
var errnoDescriptions = map[string]string{
	"EPERM": "operation not permitted", "ENOENT": "no such file or directory", "EIO": "i/o error",
	"EBADF": "bad file descriptor", "EAGAIN": "resource temporarily unavailable", "EACCES": "permission denied",
	"EBUSY": "resource busy or locked", "EEXIST": "file already exists", "EXDEV": "cross-device link not permitted",
	"ENOTDIR": "not a directory", "EISDIR": "illegal operation on a directory", "EINVAL": "invalid argument",
	"ENFILE": "file table overflow", "EMFILE": "too many open files", "ETXTBSY": "text file is busy",
	"EFBIG": "file too large", "ENOSPC": "no space left on device", "EROFS": "read-only file system",
	"EMLINK": "too many links", "ENAMETOOLONG": "name too long", "ENOSYS": "function not implemented",
	"ENOTEMPTY": "directory not empty", "ELOOP": "too many symbolic links encountered",
	"ENOTSUP": "operation not supported on socket",
}
