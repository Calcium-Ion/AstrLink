// node:process for plugins, and the process global. The working directory
// and HOME are the plugin's data directory; env holds only what the host
// gives every plugin, never the gateway's own variables. stdout and stderr
// write to the plugin's log. A plugin cannot exit, kill or change the
// directory of the gateway's process.
(function (module, exports, require, host) {
  "use strict";
  const EventEmitter = require("events");
  const { unavailable, invalidArgType } = require("internal/errors");
  const native = host.bind("process");
  const info = native.info();

  const process = new EventEmitter();
  const version = info.version;
  const denied = (what, reason) => () => {
    throw unavailable(what, reason);
  };

  const hrtime = (previous) => {
    const [seconds, nanoseconds] = native.hrtime();
    if (previous === undefined) return [seconds, nanoseconds];
    if (!Array.isArray(previous))
      throw invalidArgType("time", "an instance of Array", previous);
    let s = seconds - previous[0];
    let ns = nanoseconds - previous[1];
    if (ns < 0) {
      s -= 1;
      ns += 1e9;
    }
    return [s, ns];
  };
  hrtime.bigint = () => {
    const [seconds, nanoseconds] = native.hrtime();
    return BigInt(seconds) * 1000000000n + BigInt(nanoseconds);
  };

  const memoryUsage = () => native.memory();
  memoryUsage.rss = () => native.memory().rss;

  // writer returns the write of stdout or stderr, which logs what it is
  // given at level.
  const writer = (level) => (chunk, encoding, callback) => {
    if (typeof encoding === "function") {
      callback = encoding;
      encoding = undefined;
    }
    const text =
      typeof chunk === "string"
        ? chunk
        : new TextDecoder().decode(
            ArrayBuffer.isView(chunk)
              ? new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength)
              : new Uint8Array(chunk),
          );
    native.write(level, text);
    if (typeof callback === "function") queueMicrotask(() => callback(null));
    return true;
  };
  const stream = (fd, level) => {
    const { Writable } = require("stream");
    const write = writer(level);
    const output = new Writable({
      write(chunk, encoding, callback) {
        write(chunk, encoding, callback);
      },
    });
    output.write = (chunk, encoding, callback) => write(chunk, encoding, callback);
    Object.assign(output, { fd, isTTY: false, columns: 80, rows: 24, _type: "pipe" });
    output.getColorDepth = () => 1;
    output.hasColors = () => false;
    return output;
  };
  let stdout;
  let stderr;
  let stdin;

  const warning = (warning, type, code) => {
    if (typeof type === "object" && type !== null) {
      ({ type, code } = type);
    }
    const error =
      typeof warning === "string" ? new Error(warning) : warning;
    if (typeof warning === "string") error.name = type || "Warning";
    if (code !== undefined) error.code = code;
    native.write("warn", `${code ? `[${code}] ` : ""}${error.name}: ${error.message}`);
    queueMicrotask(() => process.emit("warning", error));
  };

  Object.assign(process, {
    title: "astrlink",
    version,
    versions: {
      node: version.slice(1),
      acorn: "8.14.0",
      ada: "2.9.2",
      modules: "127",
      napi: "10",
      uv: "1.51.0",
      v8: "12.4.254.21-node.33",
      zlib: "1.3.0.1-motley",
    },
    arch: info.arch,
    platform: info.platform,
    release: { name: "node", lts: "Jod" },
    argv: ["node", info.filename],
    argv0: "node",
    execArgv: [],
    execPath: "node",
    pid: info.pid,
    ppid: info.ppid,
    env: info.env,
    exitCode: undefined,
    config: { target_defaults: {}, variables: {} },
    features: { inspector: false, ipv6: true, tls: false, typescript: false },
    allowedNodeEnvironmentFlags: new Set(),
    connected: false,
    cwd: () => info.cwd,
    chdir: denied(
      "process.chdir()",
      "a plugin's working directory is its data directory",
    ),
    exit: denied(
      "process.exit()",
      "a plugin runs in the gateway's process; throw an error or return instead",
    ),
    abort: denied("process.abort()", "a plugin runs in the gateway's process"),
    kill: denied("process.kill()", "plugins cannot signal processes"),
    reallyExit: denied("process.reallyExit()", "a plugin runs in the gateway's process"),
    dlopen: denied("process.dlopen()", "plugins cannot load native modules"),
    binding: denied("process.binding()", "plugins cannot reach Node's internals"),
    _linkedBinding: denied("process._linkedBinding()", "plugins cannot reach Node's internals"),
    nextTick(callback, ...args) {
      if (typeof callback !== "function")
        throw invalidArgType("callback", "of type function", callback);
      queueMicrotask(() => callback(...args));
    },
    hrtime,
    uptime: () => native.uptime(),
    memoryUsage,
    cpuUsage: (previous) => ({
      user: 0 - (previous?.user ?? 0),
      system: 0 - (previous?.system ?? 0),
    }),
    resourceUsage: () => ({
      userCPUTime: 0,
      systemCPUTime: 0,
      maxRSS: 0,
      sharedMemorySize: 0,
      unsharedDataSize: 0,
      unsharedStackSize: 0,
      minorPageFault: 0,
      majorPageFault: 0,
      swappedOut: 0,
      fsRead: 0,
      fsWrite: 0,
      ipcSent: 0,
      ipcReceived: 0,
      signalsCount: 0,
      voluntaryContextSwitches: 0,
      involuntaryContextSwitches: 0,
    }),
    umask: () => 0o022,
    emitWarning: warning,
    getBuiltinModule(id) {
      const loader = require("internal/loader");
      const name = loader.builtin(String(id));
      return name === undefined ? undefined : loader.load(name);
    },
    hasUncaughtExceptionCaptureCallback: () => false,
    setUncaughtExceptionCaptureCallback() {},
    setSourceMapsEnabled() {},
  });
  if (info.platform !== "win32") {
    const os = () => host.bind("os").info();
    Object.assign(process, {
      getuid: () => os().uid,
      geteuid: () => os().uid,
      getgid: () => os().gid,
      getegid: () => os().gid,
      getgroups: () => [os().gid],
    });
  }
  Object.defineProperties(process, {
    stdout: {
      get: () => (stdout ??= stream(1, "info")),
      enumerable: true,
      configurable: true,
    },
    stderr: {
      get: () => (stderr ??= stream(2, "warn")),
      enumerable: true,
      configurable: true,
    },
    stdin: {
      get() {
        if (stdin === undefined) {
          const { Readable } = require("stream");
          stdin = new Readable({
            read() {
              this.push(null);
            },
          });
          Object.assign(stdin, { fd: 0, isTTY: false });
        }
        return stdin;
      },
      enumerable: true,
      configurable: true,
    },
    [Symbol.toStringTag]: { value: "process", configurable: true },
  });
  module.exports = process;
})
