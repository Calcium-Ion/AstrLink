// node:cluster for plugins: a plugin is never a worker and starts none.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("events");
  const { unavailable } = require("internal/errors");
  const reason = "plugins cannot start processes or threads, which could read and write any file";
  class Worker extends EventEmitter {
    constructor() {
      super();
      throw unavailable("cluster.Worker", reason);
    }
  }
  const cluster = new EventEmitter();
  Object.assign(cluster, {
    Worker,
    isMaster: true,
    isPrimary: true,
    isWorker: false,
    workers: {},
    settings: {},
    schedulingPolicy: 2,
    SCHED_NONE: 1,
    SCHED_RR: 2,
    fork() {
      throw unavailable("cluster.fork()", reason);
    },
    setupPrimary() {},
    setupMaster() {},
    disconnect(callback) {
      if (typeof callback === "function") queueMicrotask(callback);
    },
  });
  module.exports = cluster;
})
