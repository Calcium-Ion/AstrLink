// node:events for plugins: the events package (internal/vendor/events.js),
// with what Node's module has beside EventEmitter and once.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("internal/vendor/events");
  const { abortError, validateAbortSignal } = require("internal/errors");
  const errorMonitor = Symbol("events.errorMonitor");
  const captureRejectionSymbol = Symbol.for("nodejs.rejection");

  // An 'error' goes to the errorMonitor listeners first, as in Node.
  const emit = EventEmitter.prototype.emit;
  EventEmitter.prototype.emit = function (type, ...args) {
    if (type === "error" && this._events?.[errorMonitor] !== undefined)
      emit.call(this, errorMonitor, ...args);
    return emit.call(this, type, ...args);
  };

  const listenersOf = (target, name) =>
    typeof target.listeners === "function" ? target.listeners(name) : [];

  // on returns an async iterator of the arguments of each event name
  // emits, ending with an error event or the signal.
  function on(emitter, event, options = {}) {
    const signal = options?.signal;
    validateAbortSignal(signal, "options.signal");
    if (signal?.aborted) throw abortError(signal.reason);
    const queue = [];
    const waiting = [];
    let failure = null;
    let finished = false;
    const close = options?.close ?? [];
    const handler = (...args) => {
      const next = waiting.shift();
      if (next) next.resolve({ value: args, done: false });
      else queue.push(args);
    };
    const fail = (error) => {
      finished = true;
      const next = waiting.shift();
      if (next) next.reject(error);
      else failure = error;
      iterator.return();
    };
    const end = () => {
      finished = true;
      for (const next of waiting.splice(0)) next.resolve({ value: undefined, done: true });
    };
    const add = (name, fn) =>
      typeof emitter.on === "function"
        ? emitter.on(name, fn)
        : emitter.addEventListener(name, fn);
    const remove = (name, fn) =>
      typeof emitter.removeListener === "function"
        ? emitter.removeListener(name, fn)
        : emitter.removeEventListener(name, fn);
    add(event, handler);
    if (event !== "error" && typeof emitter.on === "function") add("error", fail);
    for (const name of close) add(name, end);
    const abort = () => fail(abortError(signal?.reason));
    signal?.addEventListener("abort", abort, { once: true });
    const iterator = {
      next() {
        if (queue.length > 0) return Promise.resolve({ value: queue.shift(), done: false });
        if (failure) {
          const error = failure;
          failure = null;
          return Promise.reject(error);
        }
        if (finished) return Promise.resolve({ value: undefined, done: true });
        return new Promise((resolve, reject) => waiting.push({ resolve, reject }));
      },
      return() {
        remove(event, handler);
        remove("error", fail);
        for (const name of close) remove(name, end);
        signal?.removeEventListener("abort", abort);
        finished = true;
        for (const next of waiting.splice(0)) next.resolve({ value: undefined, done: true });
        return Promise.resolve({ value: undefined, done: true });
      },
      throw(error) {
        fail(error);
        return Promise.reject(error);
      },
      [Symbol.asyncIterator]() {
        return this;
      },
    };
    return iterator;
  }

  class EventEmitterAsyncResource extends EventEmitter {
    constructor(options = {}) {
      super(options);
      this.asyncResource = { type: options?.name ?? new.target.name };
    }
    emitDestroy() {}
    get asyncId() {
      return 0;
    }
    get triggerAsyncId() {
      return 0;
    }
  }

  Object.assign(EventEmitter, {
    on,
    errorMonitor,
    captureRejectionSymbol,
    captureRejections: false,
    usingDomains: false,
    EventEmitterAsyncResource,
    getEventListeners: (target, name) => listenersOf(target, name),
    getMaxListeners: (emitter) =>
      typeof emitter.getMaxListeners === "function"
        ? emitter.getMaxListeners()
        : EventEmitter.defaultMaxListeners,
    setMaxListeners(n = EventEmitter.defaultMaxListeners, ...targets) {
      if (targets.length === 0) EventEmitter.defaultMaxListeners = n;
      for (const target of targets)
        if (typeof target.setMaxListeners === "function") target.setMaxListeners(n);
    },
    addAbortListener(signal, listener) {
      if (signal.aborted) queueMicrotask(() => listener());
      else signal.addEventListener("abort", listener, { once: true });
      return {
        [Symbol.dispose ?? Symbol.for("nodejs.dispose")]() {
          signal.removeEventListener("abort", listener);
        },
      };
    },
  });
  module.exports = EventEmitter;
})
