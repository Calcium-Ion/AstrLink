// node:net for plugins: the checks of IP addresses work; sockets and
// servers are not available, so that a dependency that only imports the
// module still runs.
(function (module, exports, require, host) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "plugins cannot open sockets or servers; use fetch, node:http or node:https to reach the network";
  const v4 = /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;
  const isIPv4 = (input) => v4.test(String(input));
  const isIPv6 = (input) => {
    let text = String(input);
    const zone = text.indexOf("%");
    if (zone !== -1) text = text.slice(0, zone);
    if (!/^[0-9a-fA-F:.]+$/.test(text) || !text.includes(":")) return false;
    let groups = text.split(":");
    const last = groups.at(-1);
    let width = 8;
    if (last.includes(".")) {
      if (!isIPv4(last)) return false;
      groups = groups.slice(0, -1);
      width = 6;
    }
    const doubled = text.split("::").length - 1;
    if (doubled > 1) return false;
    const filled = groups.filter((group) => group !== "");
    if (filled.some((group) => group.length > 4)) return false;
    if (doubled === 1) return filled.length < width;
    return groups.length === width && filled.length === width;
  };
  const isIP = (input) => (isIPv4(input) ? 4 : isIPv6(input) ? 6 : 0);
  const denied = (what) => () => {
    throw unavailable(`net.${what}`, reason);
  };
  class Socket {
    constructor() {
      throw unavailable("net.Socket", reason);
    }
  }
  class Server {
    constructor() {
      throw unavailable("net.Server", reason);
    }
  }
  class BlockList {
    #rules = [];
    addAddress(address) {
      this.#rules.push((candidate) => candidate === address);
    }
    addRange(start, end) {
      this.#rules.push((candidate) => candidate >= start && candidate <= end);
    }
    addSubnet() {}
    check(address) {
      return this.#rules.some((rule) => rule(address));
    }
    get rules() {
      return [];
    }
  }
  class SocketAddress {
    constructor(options = {}) {
      this.address = options.address ?? "127.0.0.1";
      this.family = options.family ?? "ipv4";
      this.port = options.port ?? 0;
      this.flowlabel = options.flowlabel ?? 0;
    }
  }
  module.exports = {
    isIP,
    isIPv4,
    isIPv6,
    BlockList,
    SocketAddress,
    Socket,
    Server,
    Stream: Socket,
    connect: denied("connect()"),
    createConnection: denied("createConnection()"),
    createServer: denied("createServer()"),
    getDefaultAutoSelectFamily: () => true,
    setDefaultAutoSelectFamily() {},
    getDefaultAutoSelectFamilyAttemptTimeout: () => 250,
    setDefaultAutoSelectFamilyAttemptTimeout() {},
  };
})
