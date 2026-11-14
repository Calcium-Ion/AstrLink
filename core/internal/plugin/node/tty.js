// node:tty for plugins: a plugin's output is its log, never a terminal.
(function (module, exports, require) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "a plugin's output is its log, not a terminal";
  class ReadStream {
    constructor() {
      throw unavailable("tty.ReadStream", reason);
    }
  }
  class WriteStream {
    constructor() {
      throw unavailable("tty.WriteStream", reason);
    }
  }
  module.exports = { isatty: () => false, ReadStream, WriteStream };
})
