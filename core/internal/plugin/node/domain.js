// node:domain, deprecated in Node: domains that run functions, without
// catching their errors.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("events");
  class Domain extends EventEmitter {
    members = [];
    run(fn, ...args) {
      return fn.apply(this, args);
    }
    add(emitter) {
      this.members.push(emitter);
    }
    remove(emitter) {
      this.members = this.members.filter((each) => each !== emitter);
    }
    bind(fn) {
      return fn;
    }
    intercept(fn) {
      return (error, ...args) => {
        if (error) this.emit("error", error);
        else return fn(...args);
      };
    }
    enter() {}
    exit() {}
    dispose() {}
  }
  module.exports = { Domain, active: null, create: () => new Domain(), createDomain: () => new Domain() };
})
