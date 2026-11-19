// node:async_hooks for plugins. moejs keeps no context across await, so
// AsyncLocalStorage keeps the store that run gave until the function it ran
// settles: within one call a plugin runs one flow at a time, and the store
// reaches its awaits. Flows that overlap in one call see the latest store.
(function (module, exports, require) {
  "use strict";
  const { validateFunction } = require("internal/errors");
  const storages = new Set();
  class AsyncLocalStorage {
    #store;
    #enabled = false;
    getStore() {
      return this.#enabled ? this.#store : undefined;
    }
    run(store, fn, ...args) {
      validateFunction(fn, "callback");
      const previous = [this.#enabled, this.#store];
      this.#enabled = true;
      this.#store = store;
      storages.add(this);
      const restore = () => {
        if (this.#store === store) [this.#enabled, this.#store] = previous;
      };
      let result;
      try {
        result = fn(...args);
      } catch (error) {
        restore();
        throw error;
      }
      if (result !== null && typeof result === "object" && typeof result.then === "function") result.then(restore, restore);
      else restore();
      return result;
    }
    exit(fn, ...args) {
      const [enabled, store] = [this.#enabled, this.#store];
      this.#enabled = false;
      try {
        return fn(...args);
      } finally {
        [this.#enabled, this.#store] = [enabled, store];
      }
    }
    enterWith(store) {
      this.#enabled = true;
      this.#store = store;
      storages.add(this);
    }
    disable() {
      this.#enabled = false;
      this.#store = undefined;
      storages.delete(this);
    }
    static bind(fn) {
      return fn;
    }
    static snapshot() {
      return (fn, ...args) => fn(...args);
    }
  }
  class AsyncResource {
    constructor(type) {
      this.type = type;
    }
    runInAsyncScope(fn, thisArg, ...args) {
      return fn.apply(thisArg, args);
    }
    bind(fn, thisArg) {
      return thisArg === undefined ? fn : fn.bind(thisArg);
    }
    emitDestroy() {
      return this;
    }
    asyncId() {
      return 1;
    }
    triggerAsyncId() {
      return 0;
    }
    static bind(fn, type, thisArg) {
      return thisArg === undefined ? fn : fn.bind(thisArg);
    }
  }
  module.exports = {
    AsyncLocalStorage,
    AsyncResource,
    createHook: () => ({ enable() {
      return this;
    }, disable() {
      return this;
    } }),
    executionAsyncId: () => 1,
    triggerAsyncId: () => 0,
    executionAsyncResource: () => ({}),
    asyncWrapProviders: {},
  };
})
