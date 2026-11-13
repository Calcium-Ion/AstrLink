// node:http for plugins: the client, on the host's fetch, so that a package
// written for Node's http, such as an SDK with a Node adapter, sends its
// requests the way fetch does: through the gateway's proxy, with the
// plugin's network declaration, and only while an apiVersion 2 hook runs.
// Agents keep no sockets, and the headers the transport sets are its own.
// Servers are not available: plugins serve requests through the local
// endpoints of the plugin API.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("events");
  const { Readable, Writable } = require("stream");
  const { Buffer } = require("buffer");
  const errors = require("internal/errors");
  const { unavailable, coded } = errors;

  const STATUS_CODES = {
    100: "Continue", 101: "Switching Protocols", 102: "Processing", 103: "Early Hints",
    200: "OK", 201: "Created", 202: "Accepted", 203: "Non-Authoritative Information", 204: "No Content",
    205: "Reset Content", 206: "Partial Content", 207: "Multi-Status", 208: "Already Reported", 226: "IM Used",
    300: "Multiple Choices", 301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified",
    305: "Use Proxy", 307: "Temporary Redirect", 308: "Permanent Redirect",
    400: "Bad Request", 401: "Unauthorized", 402: "Payment Required", 403: "Forbidden", 404: "Not Found",
    405: "Method Not Allowed", 406: "Not Acceptable", 407: "Proxy Authentication Required", 408: "Request Timeout",
    409: "Conflict", 410: "Gone", 411: "Length Required", 412: "Precondition Failed", 413: "Payload Too Large",
    414: "URI Too Long", 415: "Unsupported Media Type", 416: "Range Not Satisfiable", 417: "Expectation Failed",
    418: "I'm a Teapot", 421: "Misdirected Request", 422: "Unprocessable Entity", 423: "Locked",
    424: "Failed Dependency", 425: "Too Early", 426: "Upgrade Required", 428: "Precondition Required",
    429: "Too Many Requests", 431: "Request Header Fields Too Large", 451: "Unavailable For Legal Reasons",
    500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable",
    504: "Gateway Timeout", 505: "HTTP Version Not Supported", 506: "Variant Also Negotiates",
    507: "Insufficient Storage", 508: "Loop Detected", 509: "Bandwidth Limit Exceeded", 510: "Not Extended",
    511: "Network Authentication Required",
  };
  const METHODS = [
    "ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD", "LINK", "LOCK", "M-SEARCH",
    "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL", "MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST",
    "PROPFIND", "PROPPATCH", "PURGE", "PUT", "QUERY", "REBIND", "REPORT", "SEARCH", "SOURCE",
    "SUBSCRIBE", "TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE",
  ];
  // The headers the transport sets, which the request leaves to it.
  const transport = new Set(["host", "content-length", "connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade", "expect", "proxy-connection"]);
  const tokenPattern = /^[\^_`a-zA-Z\-0-9!#$%&'*+.|~]+$/;
  const validateHeaderName = (name) => {
    if (typeof name !== "string" || !tokenPattern.test(name))
      throw coded(TypeError, "ERR_INVALID_HTTP_TOKEN", `Header name must be a valid HTTP token ["${name}"]`);
  };
  const validateHeaderValue = (name, value) => {
    if (value === undefined)
      throw coded(TypeError, "ERR_HTTP_INVALID_HEADER_VALUE", `Invalid value "${value}" for header "${name}"`);
    if (/[^\t\x20-\x7e\x80-\xff]/.test(String(value)))
      throw coded(TypeError, "ERR_INVALID_CHAR", `Invalid character in header content ["${name}"]`);
  };

  class Agent extends EventEmitter {
    constructor(options = {}) {
      super();
      this.options = { path: null, ...options };
      this.keepAlive = Boolean(options.keepAlive);
      this.keepAliveMsecs = options.keepAliveMsecs ?? 1000;
      this.maxSockets = options.maxSockets ?? Infinity;
      this.maxFreeSockets = options.maxFreeSockets ?? 256;
      this.maxTotalSockets = options.maxTotalSockets ?? Infinity;
      this.totalSocketCount = 0;
      this.scheduling = options.scheduling ?? "lifo";
      this.sockets = Object.create(null);
      this.freeSockets = Object.create(null);
      this.requests = Object.create(null);
      this.defaultPort = 80;
      this.protocol = "http:";
    }
    getName(options = {}) {
      return `${options.host || "localhost"}:${options.port || ""}:${options.localAddress || ""}${options.family ? `:${options.family}` : ""}`;
    }
    createConnection() {
      throw unavailable("agent.createConnection()", "plugins cannot open sockets");
    }
    keepSocketAlive() {
      return false;
    }
    reuseSocket() {}
    destroy() {}
  }
  const globalAgent = new Agent({ keepAlive: true, scheduling: "lifo", timeout: 5000 });

  // Socket is what req.socket and res.socket are: an emitter with the
  // methods code calls on a socket, and no connection behind it.
  class Socket extends EventEmitter {
    constructor(host, port, encrypted) {
      super();
      Object.assign(this, {
        remoteAddress: host, remotePort: Number(port), remoteFamily: host.includes(":") ? "IPv6" : "IPv4",
        localAddress: "127.0.0.1", localPort: 0, encrypted: encrypted || undefined,
        readable: true, writable: true, destroyed: false, connecting: false, bytesRead: 0, bytesWritten: 0,
      });
    }
    setTimeout(ms, callback) {
      if (typeof callback === "function") this.once("timeout", callback);
      return this;
    }
    setNoDelay() {
      return this;
    }
    setKeepAlive() {
      return this;
    }
    ref() {
      return this;
    }
    unref() {
      return this;
    }
    address() {
      return { address: this.localAddress, family: "IPv4", port: 0 };
    }
    destroy() {
      this.destroyed = true;
      return this;
    }
    end() {
      return this;
    }
    write() {
      return true;
    }
    getPeerCertificate() {
      return {};
    }
    getProtocol() {
      return this.encrypted ? "TLSv1.3" : null;
    }
    get authorized() {
      return Boolean(this.encrypted);
    }
  }

  // failure is the error of Node for what fetch failed with.
  const failure = (error, host, port) => {
    const reason = error?.cause?.code;
    const code = reason === "timeout" ? "ETIMEDOUT" : reason === "denied" ? "EACCES" : reason === "too_large" ? "ERR_HTTP_RESPONSE_TOO_LARGE" : reason === "budget" ? "ETIMEDOUT" : reason === "network" ? "ECONNREFUSED" : undefined;
    if (code === undefined) return error;
    const wrapped = new Error(`${error.message} (${host}:${port})`, { cause: error });
    wrapped.code = code;
    wrapped.syscall = "connect";
    wrapped.address = host;
    wrapped.port = Number(port);
    return wrapped;
  };

  class ClientRequest extends Writable {
    #headers = new Map();
    #chunks = [];
    #controller = new AbortController();
    #sent = false;
    #timer;
    constructor(input, options, callback, defaults = { protocol: "http:", port: 80, agent: globalAgent }) {
      // The request lives on after its body is written, until the response
      // has come.
      super({ autoDestroy: false });
      if (typeof input === "string" || input instanceof URL) {
        const fromURL = require("url").urlToHttpOptions(input instanceof URL ? input : new URL(input));
        if (typeof options === "function") [callback, options] = [options, {}];
        options = { ...fromURL, ...options };
      } else {
        if (typeof options === "function") callback = options;
        options = { ...input };
      }
      const agent = options.agent === false ? new Agent() : options.agent ?? defaults.agent;
      this.agent = agent;
      const protocol = options.protocol ?? defaults.protocol;
      if (protocol !== defaults.protocol)
        throw coded(TypeError, "ERR_INVALID_PROTOCOL", `Protocol "${protocol}" not supported. Expected "${defaults.protocol}"`);
      this.protocol = protocol;
      this.method = String(options.method ?? "GET").toUpperCase();
      this.host = options.hostname ?? options.host ?? "localhost";
      if (this.host.startsWith("[") && this.host.endsWith("]")) this.host = this.host.slice(1, -1);
      this.port = options.port ?? options.defaultPort ?? agent?.defaultPort ?? defaults.port;
      this.path = options.path ?? "/";
      if (/[\u0000- ]/.test(this.path))
        throw coded(TypeError, "ERR_UNESCAPED_CHARACTERS", "Request path contains unescaped characters");
      this.aborted = false;
      this.reusedSocket = false;
      this.maxHeadersCount = null;
      this.finished = false;
      this.socket = this.connection = new Socket(this.host, this.port, protocol === "https:");
      const headers = options.headers;
      if (Array.isArray(headers)) {
        for (let index = 0; index + 1 < headers.length; index += 2) this.setHeader(headers[index], headers[index + 1]);
      } else if (headers) {
        for (const [name, value] of Object.entries(headers)) if (value !== undefined) this.setHeader(name, value);
      }
      if (options.auth && !this.hasHeader("authorization"))
        this.setHeader("Authorization", `Basic ${Buffer.from(options.auth).toString("base64")}`);
      if (typeof callback === "function") this.once("response", callback);
      const signal = options.signal;
      if (signal) {
        if (signal.aborted) queueMicrotask(() => this.destroy(errors.abortError(signal.reason)));
        else signal.addEventListener("abort", () => this.destroy(errors.abortError(signal.reason)), { once: true });
      }
      if (options.timeout !== undefined) this.setTimeout(options.timeout);
      queueMicrotask(() => {
        if (!this.destroyed) this.emit("socket", this.socket);
      });
    }
    setHeader(name, value) {
      if (this.#sent) throw coded(Error, "ERR_HTTP_HEADERS_SENT", "Cannot set headers after they are sent to the client");
      validateHeaderName(name);
      validateHeaderValue(name, value);
      this.#headers.set(name.toLowerCase(), [name, value]);
      return this;
    }
    appendHeader(name, value) {
      const existing = this.getHeader(name);
      return this.setHeader(name, existing === undefined ? value : [].concat(existing, value));
    }
    getHeader(name) {
      return this.#headers.get(String(name).toLowerCase())?.[1];
    }
    getHeaders() {
      const headers = Object.create(null);
      for (const [key, [, value]] of this.#headers) headers[key] = value;
      return headers;
    }
    getHeaderNames() {
      return [...this.#headers.keys()];
    }
    getRawHeaderNames() {
      return [...this.#headers.values()].map(([name]) => name);
    }
    hasHeader(name) {
      return this.#headers.has(String(name).toLowerCase());
    }
    removeHeader(name) {
      if (this.#sent) throw coded(Error, "ERR_HTTP_HEADERS_SENT", "Cannot remove headers after they are sent to the client");
      this.#headers.delete(String(name).toLowerCase());
    }
    get headersSent() {
      return this.#sent;
    }
    flushHeaders() {}
    setNoDelay() {}
    setSocketKeepAlive() {}
    setTimeout(ms, callback) {
      if (typeof callback === "function") this.once("timeout", callback);
      clearTimeout(this.#timer);
      if (ms > 0) {
        this.#timer = setTimeout(() => {
          this.emit("timeout");
          this.socket.emit("timeout");
        }, ms);
      }
      return this;
    }
    abort() {
      if (this.aborted) return;
      this.aborted = true;
      queueMicrotask(() => this.emit("abort"));
      this.destroy();
    }
    _write(chunk, encoding, callback) {
      this.#chunks.push(typeof chunk === "string" ? Buffer.from(chunk, encoding) : Buffer.from(chunk));
      callback();
    }
    _final(callback) {
      this.#send();
      callback();
    }
    _destroy(error, callback) {
      this.#controller.abort(error ?? errors.abortError());
      clearTimeout(this.#timer);
      callback(error);
    }
    async #send() {
      this.#sent = true;
      this.finished = true;
      const headers = [];
      for (const [key, [name, value]] of this.#headers) {
        if (transport.has(key)) continue;
        headers.push([name, Array.isArray(value) ? value.join(", ") : String(value)]);
      }
      const body = this.#chunks.length > 0 ? Buffer.concat(this.#chunks) : undefined;
      const host = this.host.includes(":") ? `[${this.host}]` : this.host;
      const standard = this.protocol === "https:" ? 443 : 80;
      const url = `${this.protocol}//${host}${Number(this.port) === standard ? "" : `:${this.port}`}${this.path}`;
      let response;
      try {
        response = await fetch(url, {
          method: this.method,
          headers,
          body: body === undefined || ((this.method === "GET" || this.method === "HEAD") && body.length === 0) ? undefined : body,
          signal: this.#controller.signal,
        });
      } catch (error) {
        if (!this.destroyed) this.destroy(failure(error, this.host, this.port));
        return;
      }
      clearTimeout(this.#timer);
      if (this.destroyed) return;
      const incoming = new IncomingMessage(this.socket, response, this);
      this.res = incoming;
      incoming.once("end", () => this.destroy());
      if (this.listenerCount("response") === 0) incoming.resume();
      this.emit("response", incoming);
      incoming._take(response);
    }
  }

  class IncomingMessage extends Readable {
    constructor(socket, response, request) {
      super();
      this.socket = this.connection = socket;
      this.req = request;
      this.httpVersion = "1.1";
      this.httpVersionMajor = 1;
      this.httpVersionMinor = 1;
      this.complete = false;
      this.aborted = false;
      this.url = "";
      this.method = null;
      this.statusCode = response?.status ?? null;
      this.statusMessage = response?.statusText || STATUS_CODES[this.statusCode] || "";
      this.headers = {};
      this.headersDistinct = {};
      this.rawHeaders = [];
      this.trailers = {};
      this.trailersDistinct = {};
      this.rawTrailers = [];
      if (response) {
        for (const [name, value] of response.headers) {
          if (name === "set-cookie") {
            const cookies = response.headers.getSetCookie();
            this.headers[name] = cookies;
            this.headersDistinct[name] = cookies;
            for (const cookie of cookies) this.rawHeaders.push(name, cookie);
            continue;
          }
          this.headers[name] = value;
          this.headersDistinct[name] = [value];
          this.rawHeaders.push(name, value);
        }
      }
    }
    async _take(response) {
      try {
        const data = await response.arrayBuffer();
        if (data.byteLength > 0) {
          this.socket.bytesRead += data.byteLength;
          this.push(Buffer.from(data));
        }
        this.complete = true;
        this.push(null);
      } catch (error) {
        this.destroy(error);
      }
    }
    _read() {}
    setTimeout(ms, callback) {
      if (typeof callback === "function") this.once("timeout", callback);
      return this;
    }
  }

  const noServers = () => unavailable("node:http servers", "plugins cannot open sockets or servers; a plugin serves requests through the local endpoints of the plugin API");
  class Server extends EventEmitter {
    constructor() {
      super();
      throw noServers();
    }
  }
  class OutgoingMessage extends Writable {}
  class ServerResponse extends OutgoingMessage {
    constructor() {
      super();
      throw noServers();
    }
  }

  module.exports = {
    Agent,
    ClientRequest,
    IncomingMessage,
    OutgoingMessage,
    Server,
    ServerResponse,
    METHODS,
    STATUS_CODES,
    globalAgent,
    maxHeaderSize: 16384,
    request: (input, options, callback) => new ClientRequest(input, options, callback),
    get(input, options, callback) {
      const request = new ClientRequest(input, options, callback);
      request.end();
      return request;
    },
    createServer() {
      throw noServers();
    },
    setMaxIdleHTTPParsers() {},
    validateHeaderName,
    validateHeaderValue,
  };
})
