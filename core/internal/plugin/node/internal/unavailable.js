// A module of Node that loads but does nothing for plugins: each export is
// a function that throws ERR_ACCESS_DENIED with the reason, so that a
// dependency that only imports the module still runs.
(function (module, exports, require) {
  "use strict";
  const { unavailable } = require("internal/errors");
  module.exports = (name, reason, names) => {
    const target = {};
    for (const key of names) {
      const stub = {
        [key]() {
          throw unavailable(`node:${name} ${key}`, reason);
        },
      }[key];
      target[key] = stub;
    }
    return target;
  };
})
