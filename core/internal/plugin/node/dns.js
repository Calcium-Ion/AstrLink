// node:dns for plugins: fetch and node:http resolve names in the host, and
// plugins do not look them up themselves, so that a dependency that only
// imports the module still runs.
(function (module, exports, require, host) {
  "use strict";
  const reason = "plugins do not resolve names; fetch and node:http do it in the host";
  const names = ["getServers", "lookup", "lookupService", "resolve", "resolve4", "resolve6", "resolveAny", "resolveCaa", "resolveCname", "resolveMx", "resolveNaptr", "resolveNs", "resolvePtr", "resolveSoa", "resolveSrv", "resolveTxt", "reverse", "setDefaultResultOrder", "setServers"];
  const stubs = require("internal/unavailable")("dns", reason, names);
  class Resolver {
    constructor() {
      Object.assign(this, stubs);
    }
    cancel() {}
  }
  stubs.getDefaultResultOrder = () => "verbatim";
  stubs.setDefaultResultOrder = () => {};
  module.exports = {
    ...stubs,
    Resolver,
    ADDRCONFIG: 1024,
    ALL: 256,
    V4MAPPED: 2048,
    NODATA: "ENODATA",
    NOTFOUND: "ENOTFOUND",
    get promises() {
      return require("dns/promises");
    },
  };
})
