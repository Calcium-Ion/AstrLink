// node:readline/promises: Interface with question returning a promise.
(function (module, exports, require) {
  "use strict";
  const readline = require("readline");
  class Interface extends readline.Interface {
    question(query, options) {
      return new Promise((resolve) => super.question(query, options, resolve));
    }
  }
  class Readline {
    constructor(stream) {
      this.stream = stream;
    }
    clearLine() {
      return this;
    }
    clearScreenDown() {
      return this;
    }
    cursorTo() {
      return this;
    }
    moveCursor() {
      return this;
    }
    commit() {
      return Promise.resolve();
    }
    rollback() {
      return this;
    }
  }
  module.exports = {
    Interface,
    Readline,
    createInterface: (input, output, completer, terminal) => new Interface(input, output, completer, terminal),
  };
})
