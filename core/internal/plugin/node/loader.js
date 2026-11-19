// The loader of the node: modules and the globals of Node that every plugin
// runtime gets: require, process, Buffer, global, CustomEvent, __dirname and
// __filename. The timers, which return Node's Timeout objects, are the
// loop's. A module, and a global that needs one, loads the first time code
// reaches it. The script's value is a function the host calls with host,
// the host's functions for the modules; nothing else reaches it.
"use strict";
(function (host) {
  const { defineProperty, freeze } = Object;

  // modules: name → { exports, loaded }, the modules this runtime loaded.
  const modules = new Map();
  // names: name → { prefixed, unavailable, exports, own }, from the host.
  const names = new Map();
  for (const [name, prefixed, unavailable, exports, own] of host.names)
    names.set(name, { prefixed, unavailable, exports, own });

  // The packages the modules are built from require each other by their
  // npm names.
  const aliases = {
    "process/": "process",
    "string_decoder/": "string_decoder",
    "safe-buffer": "buffer",
    "abort-controller": "internal/abort_controller",
    "node:process": "process",
  };

  const load = (name) => {
    const loaded = modules.get(name);
    if (loaded !== undefined) return loaded.exports;
    if (name === "internal/loader") return loader;
    const record = { id: name, exports: {}, loaded: false };
    modules.set(name, record);
    try {
      const info = names.get(name);
      if (info !== undefined && info.unavailable && !info.own) {
        record.exports = load("internal/unavailable")(
          name,
          info.unavailable,
          info.exports,
        );
      } else {
        const factory = host.factory(name);
        factory.call(record.exports, record, record.exports, internal, host);
      }
    } catch (error) {
      modules.delete(name);
      throw error;
    }
    record.loaded = true;
    return record.exports;
  };
  const internal = (id) =>
    load(aliases[id] ?? (id.startsWith("node:") ? id.slice(5) : id));

  // builtin returns the name of the module of Node id names, undefined for
  // anything else.
  const builtin = (id) => {
    const prefixed = id.startsWith("node:");
    const bare = prefixed ? id.slice(5) : id;
    const info = names.get(bare);
    if (info === undefined || (info.prefixed && !prefixed)) return undefined;
    return bare;
  };
  const notFound = (id) => {
    const error = new Error(
      `Cannot find module '${id}': plugins require only the modules of Node, and the build bundles the plugin's own files and packages`,
    );
    error.code = "MODULE_NOT_FOUND";
    return error;
  };
  const checkId = (id) => {
    if (typeof id !== "string") {
      const error = new TypeError(
        `The "id" argument must be of type string. Received ${id === null ? "null" : typeof id}`,
      );
      error.code = "ERR_INVALID_ARG_TYPE";
      throw error;
    }
    if (id === "") {
      const error = new TypeError(
        "The argument 'id' must be a non-empty string. Received ''",
      );
      error.code = "ERR_INVALID_ARG_VALUE";
      throw error;
    }
  };
  function require(id) {
    checkId(id);
    const name = builtin(id);
    if (name === undefined) throw notFound(id);
    return load(name);
  }
  const resolve = (id) => {
    checkId(id);
    const name = builtin(id);
    if (name === undefined) throw notFound(id);
    return id;
  };
  resolve.paths = () => null;
  require.resolve = resolve;
  require.cache = Object.create(null);
  require.extensions = Object.create(null);
  require.main = undefined;

  const builtinModules = freeze(
    [...names]
      .filter(([, info]) => !info.prefixed)
      .map(([name]) => name),
  );
  const loader = freeze({
    load,
    require,
    builtin,
    builtinModules,
    filename: host.filename,
    dirname: host.dirname,
  });

  const define = (name, value) =>
    defineProperty(globalThis, name, {
      value,
      writable: true,
      configurable: true,
      enumerable: false,
    });
  const lazy = (name, get) =>
    defineProperty(globalThis, name, {
      configurable: true,
      enumerable: false,
      get() {
        const value = get();
        define(name, value);
        return value;
      },
      set(value) {
        define(name, value);
      },
    });

  defineProperty(globalThis, Symbol.for("astrlink.node"), { value: load });
  define("global", globalThis);
  define("require", require);
  define("__filename", host.filename);
  define("__dirname", host.dirname);
  lazy("process", () => load("process"));
  lazy("Buffer", () => load("buffer").Buffer);
  lazy("CustomEvent", () => load("internal/event_target").CustomEvent);

  // node:timers has the timers of the globals.
  const timers = {
    setTimeout: globalThis.setTimeout,
    setInterval: globalThis.setInterval,
    setImmediate: globalThis.setImmediate,
    clearTimeout: globalThis.clearTimeout,
    clearInterval: globalThis.clearInterval,
    clearImmediate: globalThis.clearImmediate,
  };
  modules.set("internal/timers", { exports: freeze(timers), loaded: true });

  // The console methods of Node the web console leaves out.
  const console = globalThis.console;
  if (console && typeof console === "object") {
    const counts = new Map();
    const started = new Map();
    let indent = "";
    const log = console.log;
    const extra = {
      assert(condition, ...data) {
        if (!condition) console.error("Assertion failed", ...data);
      },
      count(label = "default") {
        const count = (counts.get(label) ?? 0) + 1;
        counts.set(label, count);
        log(`${label}: ${count}`);
      },
      countReset(label = "default") {
        counts.delete(label);
      },
      dir: (value) => log(value),
      dirxml: (...data) => log(...data),
      group(...label) {
        if (label.length > 0) log(indent + label.join(" "));
        indent += "  ";
      },
      groupCollapsed(...label) {
        extra.group(...label);
      },
      groupEnd() {
        indent = indent.slice(2);
      },
      table: (data) => log(data),
      time(label = "default") {
        started.set(label, host.now());
      },
      timeEnd(label = "default") {
        extra.timeLog(label);
        started.delete(label);
      },
      timeLog(label = "default", ...data) {
        const since = started.get(label);
        if (since !== undefined)
          log(`${label}: ${(host.now() - since).toFixed(3)}ms`, ...data);
      },
      clear() {},
    };
    for (const [name, value] of Object.entries(extra)) {
      if (typeof console[name] !== "function") console[name] = value;
    }
  }
  if (globalThis.performance && !("timeOrigin" in globalThis.performance)) {
    globalThis.performance.timeOrigin = host.timeOrigin;
  }
});
