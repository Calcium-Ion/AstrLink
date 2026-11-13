// node:timers for plugins: the timers that are also the globals. They fire
// while the host runs a hook of an apiVersion 2 plugin.
(function (module, exports, require) {
  "use strict";
  const timers = require("internal/timers");
  module.exports = {
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    setInterval: timers.setInterval,
    clearInterval: timers.clearInterval,
    setImmediate: timers.setImmediate,
    clearImmediate: timers.clearImmediate,
    get promises() {
      return require("timers/promises");
    },
  };
})
