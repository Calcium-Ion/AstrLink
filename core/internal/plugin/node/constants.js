// node:constants, deprecated in Node: the constants of node:os, node:fs and
// node:crypto in one object.
(function (module, exports, require) {
  "use strict";
  const os = require("os").constants;
  module.exports = Object.assign(
    Object.create(null),
    os.dlopen,
    os.errno,
    os.priority,
    os.signals,
    require("fs").constants,
    require("crypto").constants,
  );
})
