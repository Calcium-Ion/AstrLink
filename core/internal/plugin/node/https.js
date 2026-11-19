// node:https for plugins: the client of node:http over https. TLS is the
// host's: the system's certificates, through the gateway's proxy, so the
// ca, cert, key and rejectUnauthorized options do nothing.
(function (module, exports, require) {
  "use strict";
  const http = require("http");
  class Agent extends http.Agent {
    constructor(options) {
      super(options);
      this.defaultPort = 443;
      this.protocol = "https:";
    }
  }
  const globalAgent = new Agent({ keepAlive: true, scheduling: "lifo", timeout: 5000 });
  const defaults = { protocol: "https:", port: 443, agent: globalAgent };
  module.exports = {
    Agent,
    Server: http.Server,
    globalAgent,
    request: (input, options, callback) => new http.ClientRequest(input, options, callback, defaults),
    get(input, options, callback) {
      const request = new http.ClientRequest(input, options, callback, defaults);
      request.end();
      return request;
    },
    createServer: http.createServer,
  };
})
