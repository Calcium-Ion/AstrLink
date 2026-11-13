// node:worker_threads for plugins: a plugin runs on the main thread and
// starts no others, which could read and write any file.
(function (module, exports, require) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "plugins cannot start processes or threads, which could read and write any file";
  const refuse = (what) =>
    class {
      constructor() {
        throw unavailable(`worker_threads.${what}`, reason);
      }
    };
  const environment = new Map();
  module.exports = {
    isMainThread: true,
    parentPort: null,
    workerData: null,
    threadId: 0,
    resourceLimits: {},
    SHARE_ENV: Symbol.for("nodejs.worker_threads.SHARE_ENV"),
    Worker: refuse("Worker"),
    MessageChannel: refuse("MessageChannel"),
    MessagePort: refuse("MessagePort"),
    BroadcastChannel: refuse("BroadcastChannel"),
    getEnvironmentData: (key) => environment.get(key),
    setEnvironmentData: (key, value) => (value === undefined ? environment.delete(key) : environment.set(key, value)),
    markAsUntransferable() {},
    isMarkedAsUntransferable: () => false,
    markAsUncloneable() {},
    moveMessagePortToContext() {
      throw unavailable("worker_threads.moveMessagePortToContext()", reason);
    },
    receiveMessageOnPort: () => undefined,
  };
})
