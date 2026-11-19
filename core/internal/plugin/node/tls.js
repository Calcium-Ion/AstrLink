// node:tls for plugins: TLS is the host's, through node:https and fetch;
// sockets and servers are not available, so that a dependency that only
// imports the module still runs.
(function (module, exports, require) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "plugins cannot open sockets or servers; node:https and fetch speak TLS through the host";
  const denied = (what) => () => {
    throw unavailable(`tls.${what}`, reason);
  };
  class TLSSocket {
    constructor() {
      throw unavailable("tls.TLSSocket", reason);
    }
  }
  class Server {
    constructor() {
      throw unavailable("tls.Server", reason);
    }
  }
  class SecureContext {
    constructor() {
      throw unavailable("tls.SecureContext", reason);
    }
  }
  module.exports = {
    CLIENT_RENEG_LIMIT: 3,
    CLIENT_RENEG_WINDOW: 600,
    DEFAULT_CIPHERS: "TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256",
    DEFAULT_ECDH_CURVE: "auto",
    DEFAULT_MIN_VERSION: "TLSv1.2",
    DEFAULT_MAX_VERSION: "TLSv1.3",
    rootCertificates: Object.freeze([]),
    getCiphers: () => ["tls_aes_256_gcm_sha384", "tls_chacha20_poly1305_sha256", "tls_aes_128_gcm_sha256"],
    checkServerIdentity: () => undefined,
    connect: denied("connect()"),
    createServer: denied("createServer()"),
    createSecureContext: denied("createSecureContext()"),
    SecureContext,
    Server,
    TLSSocket,
  };
})
