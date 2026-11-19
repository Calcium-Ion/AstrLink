// node:inspector for plugins: no debugger is attached, and none can be.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("events");
  const { unavailable } = require("internal/errors");
  const reason = "plugins have no debugger to reach";
  class Session extends EventEmitter {
    connect() {
      throw unavailable("inspector.Session", reason);
    }
    connectToMainThread() {
      throw unavailable("inspector.Session", reason);
    }
    post() {
      throw unavailable("inspector.Session", reason);
    }
    disconnect() {}
  }
  module.exports = {
    Session,
    console: globalThis.console,
    url: () => undefined,
    open() {
      throw unavailable("inspector.open()", reason);
    },
    close() {},
    waitForDebugger() {
      throw unavailable("inspector.waitForDebugger()", reason);
    },
  };
})
