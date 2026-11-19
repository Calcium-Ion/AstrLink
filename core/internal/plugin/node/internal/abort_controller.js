// The abort-controller package that readable-stream requires: the globals.
(function (module) {
  "use strict";
  module.exports = {
    AbortController: globalThis.AbortController,
    AbortSignal: globalThis.AbortSignal,
    default: globalThis.AbortController,
  };
})
