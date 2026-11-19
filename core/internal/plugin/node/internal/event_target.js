// EventTarget and Event are the web APIs' globals; CustomEvent, which Node
// has beside them, extends their Event.
(function (module) {
  "use strict";
  const { EventTarget, Event } = globalThis;

  class CustomEvent extends Event {
    #detail;
    constructor(type, init = {}) {
      super(type, init);
      this.#detail = init?.detail ?? null;
    }
    get detail() {
      return this.#detail;
    }
    get [Symbol.toStringTag]() {
      return "CustomEvent";
    }
  }

  module.exports = { EventTarget, Event, CustomEvent };
})
