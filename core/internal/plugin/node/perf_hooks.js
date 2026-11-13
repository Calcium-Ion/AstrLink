// node:perf_hooks for plugins: performance with marks and measures, and
// observers of them. A plugin has no event loop of its own to measure, so
// the histograms stay empty.
(function (module, exports, require, host) {
  "use strict";
  const entries = [];
  const observers = new Set();
  class PerformanceEntry {
    constructor(name, entryType, startTime, duration, detail) {
      this.name = name;
      this.entryType = entryType;
      this.startTime = startTime;
      this.duration = duration;
      this.detail = detail ?? null;
    }
    toJSON() {
      return { name: this.name, entryType: this.entryType, startTime: this.startTime, duration: this.duration, detail: this.detail };
    }
  }
  class PerformanceMark extends PerformanceEntry {
    constructor(name, options = {}) {
      super(String(name), "mark", options.startTime ?? performance.now(), 0, options.detail);
    }
  }
  class PerformanceMeasure extends PerformanceEntry {}
  const record = (entry) => {
    entries.push(entry);
    if (entries.length > 1000) entries.shift();
    for (const observer of observers) observer._notify(entry);
    return entry;
  };
  const timeOf = (value) => {
    if (value === undefined) return undefined;
    if (typeof value === "number") return value;
    const mark = [...entries].reverse().find((entry) => entry.entryType === "mark" && entry.name === value);
    if (mark === undefined) {
      const error = new SyntaxError(`The "${value}" performance mark has not been set`);
      error.code = "ERR_INVALID_PERFORMANCE_MARK";
      throw error;
    }
    return mark.startTime;
  };
  const base = globalThis.performance;
  const performance = Object.assign(Object.create(null), {
    now: () => base.now(),
    timeOrigin: base.timeOrigin ?? host.timeOrigin,
    mark: (name, options) => record(new PerformanceMark(name, options)),
    measure(name, start, end) {
      let detail;
      if (start !== null && typeof start === "object") {
        ({ start, end, detail } = start);
      }
      const from = timeOf(start) ?? 0;
      const to = timeOf(end) ?? performance.now();
      return record(new PerformanceMeasure(String(name), "measure", from, to - from, detail));
    },
    clearMarks: (name) => remove("mark", name),
    clearMeasures: (name) => remove("measure", name),
    clearResourceTimings() {},
    getEntries: () => [...entries],
    getEntriesByName: (name, type) => entries.filter((entry) => entry.name === name && (type === undefined || entry.entryType === type)),
    getEntriesByType: (type) => entries.filter((entry) => entry.entryType === type),
    eventLoopUtilization: () => ({ idle: 0, active: 0, utilization: 0 }),
    timerify: (fn) => fn,
    setResourceTimingBufferSize() {},
    toJSON: () => ({ timeOrigin: performance.timeOrigin, eventLoopUtilization: performance.eventLoopUtilization() }),
    nodeTiming: { name: "node", entryType: "node", startTime: 0, duration: 0 },
  });
  const remove = (type, name) => {
    for (let index = entries.length - 1; index >= 0; index--) {
      if (entries[index].entryType === type && (name === undefined || entries[index].name === name)) entries.splice(index, 1);
    }
  };
  class PerformanceObserver {
    #callback;
    #types = new Set();
    #buffer = [];
    #scheduled = false;
    static supportedEntryTypes = ["mark", "measure"];
    constructor(callback) {
      require("internal/errors").validateFunction(callback, "callback");
      this.#callback = callback;
    }
    observe(options = {}) {
      for (const type of options.entryTypes ?? [options.type]) if (type) this.#types.add(type);
      observers.add(this);
    }
    disconnect() {
      observers.delete(this);
      this.#types.clear();
    }
    takeRecords() {
      return this.#buffer.splice(0);
    }
    _notify(entry) {
      if (!this.#types.has(entry.entryType)) return;
      this.#buffer.push(entry);
      if (this.#scheduled) return;
      this.#scheduled = true;
      queueMicrotask(() => {
        this.#scheduled = false;
        const list = this.takeRecords();
        this.#callback({ getEntries: () => list, getEntriesByName: (name) => list.filter((entry) => entry.name === name), getEntriesByType: (type) => list.filter((entry) => entry.entryType === type) }, this);
      });
    }
  }
  const histogram = () => ({
    min: 0, max: 0, mean: 0, stddev: 0, count: 0, exceeds: 0, percentiles: new Map(),
    percentile: () => 0, reset() {}, enable: () => true, disable: () => true, record() {}, recordDelta() {},
  });
  module.exports = {
    performance,
    PerformanceEntry,
    PerformanceMark,
    PerformanceMeasure,
    PerformanceObserver,
    constants: { NODE_PERFORMANCE_GC_MAJOR: 4, NODE_PERFORMANCE_GC_MINOR: 1, NODE_PERFORMANCE_GC_INCREMENTAL: 8, NODE_PERFORMANCE_GC_WEAKCB: 16 },
    monitorEventLoopDelay: histogram,
    createHistogram: histogram,
  };
})
