package plugin

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// The modules of Node a plugin imports run in its runtime as CommonJS
// factories, node/<name>.js: each is a script whose completion value is a
// function of (module, exports, require, host), and the loader of
// node/loader.js runs it the first time something requires the module. An
// import of node:<name> loads a generated ES module that re-exports what
// the loader returns, so that import, require and the globals share one
// instance per runtime.

//go:embed node
var nodeFiles embed.FS

// nodeLoaderKey is the symbol, Symbol.for(nodeLoaderKey), of the global
// through which the generated ES modules reach the loader.
const nodeLoaderKey = "astrlink.node"

// nodeVersion is the version of Node the modules follow, process.version.
const nodeVersion = "v22.20.0"

// nodeModule is a module of Node that plugins import.
type nodeModule struct {
	// exports are its named exports, beside default, space separated.
	exports string
	// prefixed is set for the modules Node has only under node:.
	prefixed bool
	// unavailable, when set, says why what the module does throws. Such a
	// module loads, so that a dependency that imports it still runs, but a
	// plugin gets no processes, threads or sockets: they would reach past
	// its own directories.
	unavailable string
}

// Reasons of the modules that load but do nothing.
const (
	reasonProcesses = "plugins cannot start processes or threads, which could read and write any file"
	reasonSockets   = "plugins cannot open sockets or servers; use fetch, node:http or node:https to reach the network"
	reasonInspector = "plugins have no debugger, console or V8 internals to reach"
)

// nodeModules are the modules of Node a plugin imports as node:<name>, or by
// <name> alone unless prefixed.
var nodeModules = map[string]nodeModule{
	"assert":              {exports: "AssertionError CallTracker deepEqual deepStrictEqual doesNotMatch doesNotReject doesNotThrow equal fail ifError match notDeepEqual notDeepStrictEqual notEqual notStrictEqual ok partialDeepStrictEqual rejects strict strictEqual throws"},
	"assert/strict":       {exports: "AssertionError CallTracker deepEqual deepStrictEqual doesNotMatch doesNotReject doesNotThrow equal fail ifError match notDeepEqual notDeepStrictEqual notEqual notStrictEqual ok partialDeepStrictEqual rejects strict strictEqual throws"},
	"async_hooks":         {exports: "AsyncLocalStorage AsyncResource asyncWrapProviders createHook executionAsyncId executionAsyncResource triggerAsyncId"},
	"buffer":              {exports: "Buffer INSPECT_MAX_BYTES SlowBuffer atob btoa constants isAscii isUtf8 kMaxLength kStringMaxLength transcode"},
	"child_process":       {exports: "ChildProcess exec execFile execFileSync execSync fork spawn spawnSync", unavailable: reasonProcesses},
	"cluster":             {exports: "SCHED_NONE SCHED_RR Worker _events _eventsCount _maxListeners disconnect fork isMaster isPrimary isWorker schedulingPolicy settings setupMaster setupPrimary workers", unavailable: reasonProcesses},
	"console":             {exports: "Console assert clear count countReset debug dir dirxml error group groupCollapsed groupEnd info log table time timeEnd timeLog trace warn"},
	"constants":           {exports: "COPYFILE_EXCL COPYFILE_FICLONE COPYFILE_FICLONE_FORCE E2BIG EACCES EADDRINUSE EADDRNOTAVAIL EAGAIN EBADF EBUSY ECONNABORTED ECONNREFUSED ECONNRESET EEXIST EINTR EINVAL EIO EISDIR EMFILE ENAMETOOLONG ENOENT ENOMEM ENOSPC ENOSYS ENOTDIR ENOTEMPTY ENOTSUP EPERM EPIPE EROFS ETIMEDOUT EXDEV F_OK O_APPEND O_CREAT O_EXCL O_RDONLY O_RDWR O_SYNC O_TRUNC O_WRONLY POINT_CONVERSION_COMPRESSED POINT_CONVERSION_HYBRID POINT_CONVERSION_UNCOMPRESSED PRIORITY_ABOVE_NORMAL PRIORITY_BELOW_NORMAL PRIORITY_HIGH PRIORITY_HIGHEST PRIORITY_LOW PRIORITY_NORMAL RSA_NO_PADDING RSA_PKCS1_OAEP_PADDING RSA_PKCS1_PADDING RSA_PKCS1_PSS_PADDING RSA_PSS_SALTLEN_AUTO RSA_PSS_SALTLEN_DIGEST RSA_PSS_SALTLEN_MAX_SIGN RSA_X931_PADDING RTLD_GLOBAL RTLD_LAZY RTLD_LOCAL RTLD_NOW R_OK SIGABRT SIGALRM SIGBUS SIGCHLD SIGCONT SIGFPE SIGHUP SIGILL SIGINT SIGIO SIGIOT SIGKILL SIGPIPE SIGPOLL SIGPROF SIGPWR SIGQUIT SIGSEGV SIGSTKFLT SIGSTOP SIGSYS SIGTERM SIGTRAP SIGTSTP SIGTTIN SIGTTOU SIGURG SIGUSR1 SIGUSR2 SIGVTALRM SIGWINCH SIGXCPU SIGXFSZ S_IFBLK S_IFCHR S_IFDIR S_IFIFO S_IFLNK S_IFMT S_IFREG S_IFSOCK S_IRGRP S_IROTH S_IRUSR S_IRWXG S_IRWXO S_IRWXU S_IWGRP S_IWOTH S_IWUSR S_IXGRP S_IXOTH S_IXUSR UV_DIRENT_BLOCK UV_DIRENT_CHAR UV_DIRENT_DIR UV_DIRENT_FIFO UV_DIRENT_FILE UV_DIRENT_LINK UV_DIRENT_SOCKET UV_DIRENT_UNKNOWN UV_FS_COPYFILE_EXCL UV_FS_COPYFILE_FICLONE UV_FS_COPYFILE_FICLONE_FORCE W_OK X_OK"},
	"crypto":              {exports: "Certificate Cipheriv Decipheriv DiffieHellman ECDH Hash Hmac KeyObject Sign Verify X509Certificate constants createCipheriv createDecipheriv createDiffieHellman createECDH createHash createHmac createPrivateKey createPublicKey createSecretKey createSign createVerify generateKey generateKeyPair generateKeyPairSync generateKeySync getCipherInfo getCiphers getCurves getDiffieHellman getFips getHashes getRandomValues hash hkdf hkdfSync pbkdf2 pbkdf2Sync privateDecrypt privateEncrypt pseudoRandomBytes publicDecrypt publicEncrypt randomBytes randomFill randomFillSync randomInt randomUUID scrypt scryptSync setFips sign subtle timingSafeEqual verify webcrypto"},
	"dgram":               {exports: "Socket createSocket", unavailable: reasonSockets},
	"diagnostics_channel": {exports: "Channel channel hasSubscribers subscribe tracingChannel unsubscribe"},
	"dns":                 {exports: "ADDRCONFIG ALL NODATA NOTFOUND Resolver V4MAPPED getDefaultResultOrder getServers lookup lookupService promises resolve resolve4 resolve6 resolveAny resolveCaa resolveCname resolveMx resolveNaptr resolveNs resolvePtr resolveSoa resolveSrv resolveTxt reverse setDefaultResultOrder setServers", unavailable: reasonSockets},
	"dns/promises":        {exports: "Resolver getDefaultResultOrder getServers lookup lookupService resolve resolve4 resolve6 resolveAny resolveCaa resolveCname resolveMx resolveNaptr resolveNs resolvePtr resolveSoa resolveSrv resolveTxt reverse setDefaultResultOrder setServers", unavailable: reasonSockets},
	"domain":              {exports: "Domain active create createDomain"},
	"events":              {exports: "EventEmitter EventEmitterAsyncResource addAbortListener captureRejectionSymbol captureRejections defaultMaxListeners errorMonitor getEventListeners getMaxListeners init listenerCount on once setMaxListeners usingDomains"},
	"fs":                  {exports: "Dir Dirent F_OK FileHandle R_OK ReadStream Stats W_OK WriteStream X_OK access accessSync appendFile appendFileSync chmod chmodSync chown chownSync close closeSync constants copyFile copyFileSync cp cpSync createReadStream createWriteStream exists existsSync fchmod fchmodSync fchown fchownSync fdatasync fdatasyncSync fstat fstatSync fsync fsyncSync ftruncate ftruncateSync futimes futimesSync glob globSync lchmod lchmodSync lchown lchownSync link linkSync lstat lstatSync lutimes lutimesSync mkdir mkdirSync mkdtemp mkdtempSync open openAsBlob openSync opendir opendirSync promises read readFile readFileSync readSync readdir readdirSync readlink readlinkSync readv readvSync realpath realpathSync rename renameSync rm rmSync rmdir rmdirSync stat statSync statfs statfsSync symlink symlinkSync truncate truncateSync unlink unlinkSync unwatchFile utimes utimesSync watch watchFile write writeFile writeFileSync writeSync writev writevSync"},
	"fs/promises":         {exports: "access appendFile chmod chown constants copyFile cp glob lchmod lchown link lstat lutimes mkdir mkdtemp open opendir readFile readdir readlink realpath rename rm rmdir stat statfs symlink truncate unlink utimes watch writeFile"},
	"http":                {exports: "Agent ClientRequest IncomingMessage METHODS OutgoingMessage STATUS_CODES Server ServerResponse createServer get globalAgent maxHeaderSize request setMaxIdleHTTPParsers validateHeaderName validateHeaderValue"},
	"http2":               {exports: "connect constants createSecureServer createServer getDefaultSettings getPackedSettings getUnpackedSettings sensitiveHeaders", unavailable: reasonSockets},
	"https":               {exports: "Agent Server createServer get globalAgent request"},
	"inspector":           {exports: "Session close console open url waitForDebugger", unavailable: reasonInspector},
	"module":              {exports: "Module _cache _extensions _pathCache builtinModules constants createRequire enableCompileCache findSourceMap flushCompileCache getCompileCacheDir globalPaths isBuiltin register syncBuiltinESMExports wrap"},
	"net":                 {exports: "BlockList Server Socket SocketAddress Stream connect createConnection createServer getDefaultAutoSelectFamily getDefaultAutoSelectFamilyAttemptTimeout isIP isIPv4 isIPv6 setDefaultAutoSelectFamily setDefaultAutoSelectFamilyAttemptTimeout", unavailable: reasonSockets},
	"os":                  {exports: "EOL arch availableParallelism constants cpus devNull endianness freemem getPriority homedir hostname loadavg machine networkInterfaces platform release setPriority tmpdir totalmem type uptime userInfo version"},
	"path":                {exports: "_makeLong basename delimiter dirname extname format isAbsolute join matchesGlob normalize parse posix relative resolve sep toNamespacedPath win32"},
	"path/posix":          {exports: "_makeLong basename delimiter dirname extname format isAbsolute join matchesGlob normalize parse posix relative resolve sep toNamespacedPath win32"},
	"path/win32":          {exports: "_makeLong basename delimiter dirname extname format isAbsolute join matchesGlob normalize parse posix relative resolve sep toNamespacedPath win32"},
	"perf_hooks":          {exports: "PerformanceEntry PerformanceMark PerformanceMeasure PerformanceObserver constants createHistogram monitorEventLoopDelay performance"},
	"process":             {exports: "_events _eventsCount _linkedBinding _maxListeners abort allowedNodeEnvironmentFlags arch argv argv0 binding chdir config connected cpuUsage cwd dlopen emitWarning env execArgv execPath exit exitCode features getBuiltinModule getegid geteuid getgid getgroups getuid hasUncaughtExceptionCaptureCallback hrtime kill memoryUsage nextTick pid platform ppid reallyExit release resourceUsage setSourceMapsEnabled setUncaughtExceptionCaptureCallback stderr stdin stdout title umask uptime version versions"},
	"punycode":            {exports: "decode encode toASCII toUnicode ucs2 version"},
	"querystring":         {exports: "decode encode escape parse stringify unescape unescapeBuffer"},
	"readline":            {exports: "Interface clearLine clearScreenDown createInterface cursorTo emitKeypressEvents moveCursor promises"},
	"readline/promises":   {exports: "Interface Readline createInterface"},
	"repl":                {exports: "REPLServer start", unavailable: reasonInspector},
	"sqlite":              {exports: "DatabaseSync StatementSync constants", prefixed: true},
	"stream":              {exports: "Duplex PassThrough Readable Stream Transform Writable _isUint8Array _uint8ArrayToBuffer addAbortSignal compose destroy duplexPair finished getDefaultHighWaterMark isDestroyed isDisturbed isErrored isReadable isWritable pipeline promises setDefaultHighWaterMark"},
	"stream/consumers":    {exports: "arrayBuffer blob buffer json text"},
	"stream/promises":     {exports: "finished pipeline"},
	"string_decoder":      {exports: "StringDecoder"},
	"sys":                 {exports: "TextDecoder TextEncoder _extend aborted callbackify debug debuglog deprecate format formatWithOptions getSystemErrorMap getSystemErrorMessage getSystemErrorName inherits inspect isArray isBoolean isBuffer isDate isDeepStrictEqual isError isFunction isNull isNullOrUndefined isNumber isObject isPrimitive isRegExp isString isSymbol isUndefined parseArgs promisify stripVTControlCharacters styleText toUSVString types"},
	"timers":              {exports: "clearImmediate clearInterval clearTimeout promises setImmediate setInterval setTimeout"},
	"timers/promises":     {exports: "scheduler setImmediate setInterval setTimeout"},
	"tls":                 {exports: "CLIENT_RENEG_LIMIT CLIENT_RENEG_WINDOW DEFAULT_CIPHERS DEFAULT_ECDH_CURVE DEFAULT_MAX_VERSION DEFAULT_MIN_VERSION SecureContext Server TLSSocket checkServerIdentity connect createSecureContext createServer getCiphers rootCertificates", unavailable: reasonSockets},
	"tty":                 {exports: "ReadStream WriteStream isatty"},
	"url":                 {exports: "URL URLSearchParams Url domainToASCII domainToUnicode fileURLToPath format parse pathToFileURL resolve resolveObject urlToHttpOptions"},
	"util":                {exports: "TextDecoder TextEncoder _extend aborted callbackify debug debuglog deprecate format formatWithOptions getSystemErrorMap getSystemErrorMessage getSystemErrorName inherits inspect isArray isBoolean isBuffer isDate isDeepStrictEqual isError isFunction isNull isNullOrUndefined isNumber isObject isPrimitive isRegExp isString isSymbol isUndefined parseArgs promisify stripVTControlCharacters styleText toUSVString types"},
	"util/types":          {exports: "isAnyArrayBuffer isArgumentsObject isArrayBuffer isArrayBufferView isAsyncFunction isBigInt64Array isBigIntObject isBigUint64Array isBooleanObject isBoxedPrimitive isCryptoKey isDataView isDate isExternal isFloat32Array isFloat64Array isGeneratorFunction isGeneratorObject isInt16Array isInt32Array isInt8Array isKeyObject isMap isMapIterator isModuleNamespaceObject isNativeError isNumberObject isPromise isProxy isRegExp isSet isSetIterator isSharedArrayBuffer isStringObject isSymbolObject isTypedArray isUint16Array isUint32Array isUint8Array isUint8ClampedArray isWeakMap isWeakSet"},
	"v8":                  {exports: "cachedDataVersionTag deserialize getHeapCodeStatistics getHeapSnapshot getHeapSpaceStatistics getHeapStatistics serialize setFlagsFromString setHeapSnapshotNearHeapLimit stopCoverage takeCoverage writeHeapSnapshot"},
	"vm":                  {exports: "Script compileFunction constants createContext isContext runInContext runInNewContext runInThisContext"},
	"wasi":                {exports: "WASI", unavailable: reasonInspector},
	"worker_threads":      {exports: "BroadcastChannel MessageChannel MessagePort SHARE_ENV Worker getEnvironmentData isMainThread isMarkedAsUntransferable markAsUncloneable markAsUntransferable moveMessagePortToContext parentPort receiveMessageOnPort resourceLimits setEnvironmentData threadId workerData", unavailable: reasonProcesses},
	"zlib":                {exports: "BrotliCompress BrotliDecompress Deflate DeflateRaw Gunzip Gzip Inflate InflateRaw Unzip brotliCompress brotliCompressSync brotliDecompress brotliDecompressSync codes constants crc32 createBrotliCompress createBrotliDecompress createDeflate createDeflateRaw createGunzip createGzip createInflate createInflateRaw createUnzip deflate deflateRaw deflateRawSync deflateSync gunzip gunzipSync gzip gzipSync inflate inflateRaw inflateRawSync inflateSync unzip unzipSync"},
}

// NodeBuiltin returns the name of the module of Node that specifier imports,
// node:fs or fs for "fs", and whether plugins can use it: false for one
// that loads but whose functions throw. found is false for anything else.
func NodeBuiltin(specifier string) (name string, usable, found bool) {
	bare, prefixed := strings.CutPrefix(specifier, "node:")
	module, ok := nodeModules[bare]
	if !ok || (module.prefixed && !prefixed) {
		return "", false, false
	}
	return bare, module.unavailable == "", true
}

// NodeUnavailable says why plugins cannot use the module name, which
// NodeBuiltin found; "" when they can.
func NodeUnavailable(name string) string { return nodeModules[name].unavailable }

// nodeNames lists the modules for module.builtinModules and the loader, as
// [name, prefixed, unavailable reason].
var nodeNames = sync.OnceValue(func() [][]any {
	names := make([]string, 0, len(nodeModules))
	for name := range nodeModules {
		names = append(names, name)
	}
	slices.Sort(names)
	list := make([][]any, 0, len(names))
	for _, name := range names {
		module := nodeModules[name]
		list = append(list, []any{name, module.prefixed, module.unavailable})
	}
	return list
})

// compiledScript is a script compiled once per process, on first use.
type compiledScript struct {
	once   sync.Once
	script *moejs.Script
	err    error
}

// nodeScripts are the compiled factories, by module name.
var nodeScripts sync.Map

// nodeFactory returns the compiled factory of the module name: a module of
// nodeModules, or one of node/internal that they share.
func nodeFactory(name string) (*moejs.Script, error) {
	entry, _ := nodeScripts.LoadOrStore(name, &compiledScript{})
	compiled := entry.(*compiledScript)
	compiled.once.Do(func() {
		source, err := fs.ReadFile(nodeFiles, "node/"+name+".js")
		if err != nil {
			compiled.err = fmt.Errorf("node:%s has no factory: %w", name, err)
			return
		}
		compiled.script, compiled.err = moejs.CompileScript("node:"+name, string(source))
	})
	return compiled.script, compiled.err
}

// hasNodeFactory reports whether name has a factory of its own; a module
// that does nothing without one is built by node/internal/unavailable.js.
func hasNodeFactory(name string) bool {
	_, err := fs.Stat(nodeFiles, "node/"+name+".js")
	return err == nil
}

// nodeWrappers are the generated ES modules of the node: modules, by name.
var nodeWrappers sync.Map

type compiledModule struct {
	once   sync.Once
	module *moejs.Module
	err    error
}

// nodeWrapper returns the ES module that node:name loads.
func nodeWrapper(name string) (*moejs.Module, error) {
	entry, _ := nodeWrappers.LoadOrStore(name, &compiledModule{})
	compiled := entry.(*compiledModule)
	compiled.once.Do(func() {
		compiled.module, compiled.err = moejs.Compile("node:"+name, nodeWrapperSource(name))
	})
	return compiled.module, compiled.err
}

// nodeWrapperSource is the ES module of node:name: its default export is
// what require returns, as in Node, and its named exports the properties
// nodeModules lists.
func nodeWrapperSource(name string) string {
	var source strings.Builder
	fmt.Fprintf(&source, "const m = globalThis[Symbol.for(%q)](%q);\nexport default m;\n", nodeLoaderKey, name)
	if exports := strings.Fields(nodeModules[name].exports); len(exports) > 0 {
		fmt.Fprintf(&source, "export const { %s } = m;\n", strings.Join(exports, ", "))
	}
	return source.String()
}

// errNotNodeModule is what resolving an import other than a module of Node
// fails with.
var errNotNodeModule = errors.New("plugins import only the modules of Node; bundle the plugin and its packages into one file")

// resolveNode resolves an import of a plugin, which can only be a module of
// Node: the build bundles everything else.
func resolveNode(_ moejs.Referrer, specifier string) (*moejs.Module, error) {
	name, _, found := NodeBuiltin(specifier)
	if !found {
		if bare, ok := strings.CutPrefix(specifier, "node:"); ok {
			return nil, fmt.Errorf("Cannot import %q: Node has no module %s, or plugins do not get it", specifier, bare)
		}
		return nil, fmt.Errorf("Cannot import %q: %w", specifier, errNotNodeModule)
	}
	return nodeWrapper(name)
}

// importMeta fills import.meta of a plugin's module as Node does for a
// file: url, filename and dirname name the plugin's file in its install
// directory, and resolve(specifier) returns what an import of specifier
// loads.
func (loaded *program) importMeta(realm *moejs.Realm, module *moejs.Module, meta *moejs.Object) error {
	if strings.HasPrefix(module.Name(), "node:") {
		return nil
	}
	set := func(key string, value moejs.Value) {
		_, _ = meta.CreateDataProperty(realm, realm.KeyFromGoString(key), value)
	}
	filename := loaded.filename()
	location := fileURL(filename)
	set("url", moejs.String(location))
	set("filename", moejs.String(filename))
	set("dirname", moejs.String(filepath.Dir(filename)))
	resolve := realm.NewNativeFunction(realm.InternGoString("resolve"), 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		text, err := realm.ToString(moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), err
		}
		specifier := text.String()
		if name, _, found := NodeBuiltin(specifier); found {
			return moejs.String("node:" + name), nil
		}
		base, err := url.Parse(location)
		if err != nil {
			return moejs.Undefined(), err
		}
		reference, err := url.Parse(specifier)
		if err != nil || (reference.Scheme == "" && !strings.HasPrefix(specifier, ".") && !strings.HasPrefix(specifier, "/")) {
			return moejs.Undefined(), realm.TypeError("Cannot resolve %q: plugins import only the modules of Node", specifier)
		}
		return moejs.String(base.ResolveReference(reference).String()), nil
	})
	set("resolve", engine.ObjectValue(resolve))
	return nil
}

// filename is the path of the plugin's file, in its install directory when
// it has one.
func (loaded *program) filename() string {
	if loaded.dirs.install == "" {
		return filepath.Join(loaded.dirs.data, loaded.file)
	}
	return filepath.Join(loaded.dirs.install, loaded.file)
}

// fileURL is the file: URL of an absolute path.
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if goruntime.GOOS == "windows" && !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}
