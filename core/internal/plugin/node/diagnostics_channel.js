// node:diagnostics_channel for plugins: named channels that publish to
// their subscribers, and tracing channels.
(function (module, exports, require) {
  "use strict";
  const { invalidArgType, validateFunction } = require("internal/errors");
  const channels = new Map();
  class Channel {
    #subscribers = [];
    #stores = new Map();
    constructor(name) {
      this.name = name;
    }
    get hasSubscribers() {
      return this.#subscribers.length > 0 || this.#stores.size > 0;
    }
    subscribe(subscription) {
      validateFunction(subscription, "subscription");
      this.#subscribers.push(subscription);
    }
    unsubscribe(subscription) {
      const index = this.#subscribers.indexOf(subscription);
      if (index === -1) return false;
      this.#subscribers.splice(index, 1);
      return true;
    }
    publish(message) {
      for (const subscriber of [...this.#subscribers]) {
        try {
          subscriber(message, this.name);
        } catch (error) {
          queueMicrotask(() => {
            throw error;
          });
        }
      }
    }
    bindStore(store, transform) {
      this.#stores.set(store, transform);
    }
    unbindStore(store) {
      return this.#stores.delete(store);
    }
    runStores(data, fn, thisArg, ...args) {
      let run = () => fn.apply(thisArg, args);
      for (const [store, transform] of this.#stores) {
        const next = run;
        run = () => store.run(transform ? transform(data) : data, next);
      }
      this.publish(data);
      return run();
    }
  }
  const channel = (name) => {
    if (typeof name !== "string" && typeof name !== "symbol")
      throw invalidArgType("channel", "of type string or symbol", name);
    let found = channels.get(name);
    if (found === undefined) channels.set(name, (found = new Channel(name)));
    return found;
  };
  class TracingChannel {
    constructor(nameOrChannels) {
      for (const event of ["start", "end", "asyncStart", "asyncEnd", "error"]) {
        this[event] = typeof nameOrChannels === "string" ? channel(`tracing:${nameOrChannels}:${event}`) : nameOrChannels[event];
      }
    }
    get hasSubscribers() {
      return ["start", "end", "asyncStart", "asyncEnd", "error"].some((event) => this[event].hasSubscribers);
    }
    subscribe(handlers) {
      for (const [event, handler] of Object.entries(handlers)) this[event]?.subscribe(handler);
    }
    unsubscribe(handlers) {
      let done = true;
      for (const [event, handler] of Object.entries(handlers)) done = (this[event]?.unsubscribe(handler) ?? false) && done;
      return done;
    }
    traceSync(fn, context = {}, thisArg, ...args) {
      this.start.publish(context);
      try {
        const result = fn.apply(thisArg, args);
        context.result = result;
        return result;
      } catch (error) {
        context.error = error;
        this.error.publish(context);
        throw error;
      } finally {
        this.end.publish(context);
      }
    }
    tracePromise(fn, context = {}, thisArg, ...args) {
      this.start.publish(context);
      try {
        const promise = Promise.resolve(fn.apply(thisArg, args));
        return promise.then(
          (result) => {
            context.result = result;
            this.asyncStart.publish(context);
            this.asyncEnd.publish(context);
            return result;
          },
          (error) => {
            context.error = error;
            this.error.publish(context);
            this.asyncStart.publish(context);
            this.asyncEnd.publish(context);
            throw error;
          },
        );
      } finally {
        this.end.publish(context);
      }
    }
    traceCallback(fn, position = -1, context = {}, thisArg, ...args) {
      const index = position < 0 ? args.length + position : position;
      const callback = args[index];
      validateFunction(callback, "callback");
      args[index] = (error, result) => {
        if (error) {
          context.error = error;
          this.error.publish(context);
        } else context.result = result;
        this.asyncStart.publish(context);
        try {
          return callback(error, result);
        } finally {
          this.asyncEnd.publish(context);
        }
      };
      return this.traceSync(fn, context, thisArg, ...args);
    }
  }
  module.exports = {
    Channel,
    channel,
    hasSubscribers: (name) => channels.get(name)?.hasSubscribers ?? false,
    subscribe: (name, subscription) => channel(name).subscribe(subscription),
    unsubscribe: (name, subscription) => channel(name).unsubscribe(subscription),
    tracingChannel: (nameOrChannels) => new TracingChannel(nameOrChannels),
  };
})
