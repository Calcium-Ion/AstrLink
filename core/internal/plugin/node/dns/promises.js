// node:dns/promises: as node:dns, which plugins do not use.
(function (module, exports, require) {
  "use strict";
  const dns = require("dns");
  const promises = {};
  for (const [name, value] of Object.entries(dns)) {
    if (name === "promises" || typeof value !== "function" || name === "Resolver") continue;
    promises[name] = async (...args) => value(...args);
  }
  promises.Resolver = dns.Resolver;
  promises.getDefaultResultOrder = dns.getDefaultResultOrder;
  promises.setDefaultResultOrder = dns.setDefaultResultOrder;
  module.exports = promises;
})
