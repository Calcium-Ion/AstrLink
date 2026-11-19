// node:timers/promises for plugins, after dsh-ptc-moejs (MIT): the ref
// option works as in Node, and an aborted signal rejects with an AbortError.
(function (module, exports, require) {
  "use strict";
  const timers = require("internal/timers");
  const { invalidArgType, abortError, validateAbortSignal } = require("internal/errors");

  const validateOptions = (options) => {
    if (options === undefined) return {};
    if (options === null || typeof options !== "object")
      throw invalidArgType("options", "of type object", options);
    validateAbortSignal(options.signal, "options.signal");
    if (options.ref !== undefined && typeof options.ref !== "boolean")
      throw invalidArgType("options.ref", "of type boolean", options.ref);
    return options;
  };

  // wait resolves with value once set fires, unless signal aborts first.
  const wait = (set, clear, value, options) => {
    try {
      const { ref, signal } = validateOptions(options);
      if (signal?.aborted) return Promise.reject(abortError(signal.reason));
      return new Promise((resolve, reject) => {
        const onAbort = () => {
          clear(timer);
          reject(abortError(signal.reason));
        };
        const timer = set(() => {
          signal?.removeEventListener("abort", onAbort);
          resolve(value);
        });
        if (ref === false) timer.unref();
        signal?.addEventListener("abort", onAbort, { once: true });
      });
    } catch (error) {
      return Promise.reject(error);
    }
  };

  function setTimeout(delay, value, options) {
    return wait((fire) => timers.setTimeout(fire, delay), timers.clearTimeout, value, options);
  }

  function setImmediate(value, options) {
    return wait((fire) => timers.setImmediate(fire), timers.clearImmediate, value, options);
  }

  async function* setInterval(delay, value, options) {
    const { ref, signal } = validateOptions(options);
    if (signal?.aborted) throw abortError(signal.reason);
    let due = 0;
    let wake;
    let aborted = false;
    const interval = timers.setInterval(() => {
      due++;
      wake?.();
      wake = undefined;
    }, delay);
    if (ref === false) interval.unref();
    const onAbort = () => {
      aborted = true;
      wake?.();
    };
    signal?.addEventListener("abort", onAbort, { once: true });
    try {
      for (;;) {
        if (due === 0 && !aborted) await new Promise((resolve) => (wake = resolve));
        if (aborted) throw abortError(signal.reason);
        for (; due > 0; due--) yield value;
      }
    } finally {
      timers.clearInterval(interval);
      signal?.removeEventListener("abort", onAbort);
    }
  }

  const scheduler = {
    wait: (delay, options) => setTimeout(delay, undefined, options),
    yield: () => setImmediate(),
  };

  module.exports = { setTimeout, setImmediate, setInterval, scheduler };
})
