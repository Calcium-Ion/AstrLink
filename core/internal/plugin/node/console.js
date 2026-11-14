// node:console for plugins: the global console, which writes to the
// plugin's log, and Console, which writes to the streams it is given.
(function (module, exports, require) {
  "use strict";
  const { format } = require("util");
  class Console {
    constructor(stdout, stderr) {
      if (stdout !== null && typeof stdout === "object" && typeof stdout.write !== "function") {
        ({ stdout, stderr } = stdout);
      }
      if (typeof stdout?.write !== "function")
        throw require("internal/errors").invalidArgType("stdout", "an instance of Writable", stdout);
      stderr ??= stdout;
      const out = (...args) => stdout.write(format(...args) + "\n");
      const err = (...args) => stderr.write(format(...args) + "\n");
      const counts = new Map();
      const timers = new Map();
      let indent = "";
      Object.assign(this, {
        log: (...args) => out(indent + format(...args)),
        info: (...args) => out(indent + format(...args)),
        debug: (...args) => out(indent + format(...args)),
        warn: (...args) => err(indent + format(...args)),
        error: (...args) => err(indent + format(...args)),
        trace: (...args) => err(indent + "Trace: " + format(...args)),
        dir: (value) => out(indent + require("util").inspect(value)),
        dirxml: (...args) => out(indent + format(...args)),
        table: (value) => out(indent + require("util").inspect(value)),
        assert: (condition, ...args) => {
          if (!condition) err(indent + "Assertion failed" + (args.length ? ": " + format(...args) : ""));
        },
        count: (label = "default") => {
          const count = (counts.get(label) ?? 0) + 1;
          counts.set(label, count);
          out(`${indent}${label}: ${count}`);
        },
        countReset: (label = "default") => counts.delete(label),
        group: (...label) => {
          if (label.length) out(indent + format(...label));
          indent += "  ";
        },
        groupCollapsed: (...label) => this.group(...label),
        groupEnd: () => {
          indent = indent.slice(2);
        },
        time: (label = "default") => timers.set(label, performance.now()),
        timeEnd: (label = "default") => {
          this.timeLog(label);
          timers.delete(label);
        },
        timeLog: (label = "default", ...args) => {
          const since = timers.get(label);
          if (since !== undefined) out(`${indent}${label}: ${(performance.now() - since).toFixed(3)}ms` + (args.length ? " " + format(...args) : ""));
        },
        clear: () => {},
      });
    }
  }
  const console = globalThis.console;
  module.exports = Object.assign(Object.create(null), console, { Console });
  for (const name of ["log", "info", "debug", "warn", "error", "trace", "dir", "dirxml", "table", "assert", "count", "countReset", "group", "groupCollapsed", "groupEnd", "time", "timeEnd", "timeLog", "clear"]) {
    if (typeof console[name] === "function") module.exports[name] = console[name].bind(console);
  }
  if (typeof console.Console !== "function") console.Console = Console;
})
