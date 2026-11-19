// node:url for plugins: the URL and URLSearchParams of the host, the
// conversions between file: URLs and paths, and the legacy url.parse,
// url.format and url.resolve of Node.
(function (module, exports, require) {
  "use strict";
  const { invalidArgType, coded } = require("internal/errors");
  const nodeProcess = require("process");
  const windows = nodeProcess.platform === "win32";

  // Url is what the legacy url.parse returns.
  function Url() {
    this.protocol = null;
    this.slashes = null;
    this.auth = null;
    this.host = null;
    this.port = null;
    this.hostname = null;
    this.hash = null;
    this.search = null;
    this.query = null;
    this.pathname = null;
    this.path = null;
    this.href = null;
  }
  const slashed = new Set(["http:", "https:", "ftp:", "gopher:", "file:", "ws:", "wss:"]);
  const hostless = new Set(["javascript:", "javascript"]);
  // The characters url.parse escapes rather than leaves in the URL.
  const autoEscape = /[\t\n\r "'<>\\^`{|}]/g;
  const escapeChar = (char) => (char === "\\" ? "/" : encodeURIComponent(char));

  function parse(url, parseQueryString = false, slashesDenoteHost = false) {
    if (url instanceof Url) return url;
    if (typeof url !== "string") throw invalidArgType("url", "of type string", url);
    const result = new Url();
    let rest = url.trim();
    // Backslashes before the query are forward slashes, as Node reads them.
    const queryAt = rest.search(/[?#]/);
    const head = queryAt === -1 ? rest : rest.slice(0, queryAt);
    rest = head.replace(/\\/g, "/") + (queryAt === -1 ? "" : rest.slice(queryAt));
    const protocol = /^[a-z0-9.+-]+:/i.exec(rest);
    let lowerProtocol;
    if (protocol) {
      lowerProtocol = protocol[0].toLowerCase();
      result.protocol = lowerProtocol;
      rest = rest.slice(protocol[0].length);
    }
    let slashes = false;
    if (slashesDenoteHost || protocol || /^\/\/[^@/]+@[^@/]+/.test(rest)) {
      slashes = rest.startsWith("//");
      if (slashes && !(lowerProtocol && hostless.has(lowerProtocol))) {
        rest = rest.slice(2);
        result.slashes = true;
      }
    }
    if (!hostless.has(lowerProtocol) && (slashes || (lowerProtocol && !slashed.has(lowerProtocol)))) {
      let hostEnd = rest.search(/[/?#]/);
      if (hostEnd === -1) hostEnd = rest.length;
      const at = rest.lastIndexOf("@", hostEnd);
      if (at !== -1) {
        result.auth = decodeURIComponent(rest.slice(0, at));
        rest = rest.slice(at + 1);
        hostEnd -= at + 1;
      }
      let host = rest.slice(0, hostEnd);
      rest = rest.slice(hostEnd);
      const port = /:([0-9]*)$/.exec(host);
      if (port) {
        if (port[1] !== "") result.port = port[1];
        host = host.slice(0, -port[0].length);
      }
      let hostname = host;
      const ipv6 = hostname.startsWith("[") && hostname.endsWith("]");
      if (ipv6) hostname = hostname.slice(1, -1);
      hostname = hostname.toLowerCase();
      if (!ipv6 && /[^\x00-\x7f]/.test(hostname)) hostname = require("punycode").toASCII(hostname);
      result.hostname = hostname;
      result.host = (ipv6 ? `[${hostname}]` : hostname) + (result.port ? `:${result.port}` : "");
      if (ipv6 && rest[0] !== "/") rest = "/" + rest;
    }
    rest = rest.replace(autoEscape, escapeChar);
    const hash = rest.indexOf("#");
    if (hash !== -1) {
      result.hash = rest.slice(hash);
      rest = rest.slice(0, hash);
    }
    const question = rest.indexOf("?");
    if (question !== -1) {
      result.search = rest.slice(question);
      result.query = rest.slice(question + 1);
      if (parseQueryString) result.query = require("querystring").parse(result.query);
      rest = rest.slice(0, question);
    } else if (parseQueryString) {
      result.search = "";
      result.query = Object.create(null);
    }
    if (rest !== "") result.pathname = rest;
    if (slashed.has(lowerProtocol) && result.hostname && !result.pathname) result.pathname = "/";
    if (result.pathname || result.search) result.path = (result.pathname ?? "") + (result.search ?? "");
    result.href = format(result);
    return result;
  }

  function format(url, options) {
    if (typeof url === "string") url = parse(url);
    else if (url instanceof URL) {
      const { auth = true, fragment = true, search = true, unicode = false } = options ?? {};
      let text = url.protocol;
      if (url.host) {
        text += "//";
        if (auth && (url.username || url.password)) text += url.username + (url.password ? ":" + url.password : "") + "@";
        text += unicode ? require("punycode").toUnicode(url.hostname) + (url.port ? ":" + url.port : "") : url.host;
      }
      text += url.pathname;
      if (search) text += url.search;
      if (fragment) text += url.hash;
      return text;
    } else if (url === null || typeof url !== "object") {
      throw invalidArgType("urlObject", "one of type object or string", url);
    }
    let auth = url.auth ?? "";
    if (auth) auth = encodeURIComponent(auth).replace(/%3A/i, ":") + "@";
    let protocol = url.protocol ?? "";
    let pathname = url.pathname ?? "";
    let hash = url.hash ?? "";
    let host = "";
    let query = "";
    if (url.host) host = auth + url.host;
    else if (url.hostname) {
      host = auth + (url.hostname.includes(":") && !url.hostname.startsWith("[") ? `[${url.hostname}]` : url.hostname);
      if (url.port) host += ":" + url.port;
    }
    if (url.query !== null && typeof url.query === "object") query = require("querystring").stringify(url.query);
    let search = url.search || (query && "?" + query) || "";
    if (protocol && !protocol.endsWith(":")) protocol += ":";
    if (url.slashes || ((!protocol || slashed.has(protocol)) && host !== "")) {
      host = "//" + host;
      if (pathname && pathname[0] !== "/") pathname = "/" + pathname;
    } else if (!host && (protocol === "file:" || (url.slashes && protocol))) {
      host = "//";
    }
    if (hash && hash[0] !== "#") hash = "#" + hash;
    if (search && search[0] !== "?") search = "?" + search;
    pathname = pathname.replace(/[?#]/g, encodeURIComponent);
    search = search.replace("#", "%23");
    return protocol + host + pathname + search + hash;
  }

  function resolve(from, to) {
    const resolved = new URL(to, new URL(from, "resolve://"));
    if (resolved.protocol === "resolve:") {
      const { pathname, search, hash } = resolved;
      return pathname + search + hash;
    }
    return resolved.toString();
  }

  function fileURLToPath(url, options) {
    if (typeof url === "string") url = new URL(url);
    else if (!(url instanceof URL)) throw invalidArgType("path", "of type string or an instance of URL", url);
    if (url.protocol !== "file:") throw coded(TypeError, "ERR_INVALID_URL_SCHEME", "The URL must be of scheme file");
    const asWindows = options?.windows ?? windows;
    const pathname = url.pathname;
    if (asWindows) {
      if (/%2f|%5c/i.test(pathname))
        throw coded(TypeError, "ERR_INVALID_FILE_URL_PATH", "File URL path must not include encoded \\ or / characters");
      const path = decodeURIComponent(pathname.replace(/\//g, "\\"));
      if (url.hostname !== "") return `\\\\${url.hostname}${path}`;
      const letter = path.codePointAt(1) | 0x20;
      if (letter < 0x61 || letter > 0x7a || path[2] !== ":")
        throw coded(TypeError, "ERR_INVALID_FILE_URL_PATH", "File URL path must be absolute");
      return path.slice(1);
    }
    if (url.hostname !== "")
      throw coded(TypeError, "ERR_INVALID_FILE_URL_HOST", `File URL host must be "localhost" or empty on ${nodeProcess.platform}`);
    if (/%2f/i.test(pathname))
      throw coded(TypeError, "ERR_INVALID_FILE_URL_PATH", "File URL path must not include encoded / characters");
    return decodeURIComponent(pathname);
  }

  function pathToFileURL(filepath, options) {
    if (typeof filepath !== "string") throw invalidArgType("path", "of type string", filepath);
    const asWindows = options?.windows ?? windows;
    const path = require("path")[asWindows ? "win32" : "posix"];
    if (asWindows && filepath.startsWith("\\\\")) {
      const [, hostname, ...rest] = filepath.slice(2).split("\\");
      const url = new URL("file://");
      url.hostname = hostname;
      url.pathname = "/" + rest.map(encodeURIComponent).join("/");
      return url;
    }
    let resolved = path.resolve(filepath);
    if ((filepath.endsWith("/") || (asWindows && filepath.endsWith("\\"))) && !resolved.endsWith(path.sep)) resolved += "/";
    if (asWindows) resolved = "/" + resolved.replace(/\\/g, "/");
    const encoded = resolved
      .replace(/%/g, "%25")
      .replace(/\t/g, "%09")
      .replace(/\n/g, "%0A")
      .replace(/\r/g, "%0D")
      .replace(/ /g, "%20")
      .replace(/"/g, "%22")
      .replace(/#/g, "%23")
      .replace(/\?/g, "%3F")
      .replace(/\[/g, "%5B")
      .replace(/\]/g, "%5D")
      .replace(/\^/g, "%5E")
      .replace(/\|/g, "%7C")
      .replace(/~/g, "%7E");
    return new URL(`file://${encoded}`);
  }

  function urlToHttpOptions(url) {
    const options = {
      protocol: url.protocol,
      hostname: url.hostname.startsWith("[") ? url.hostname.slice(1, -1) : url.hostname,
      hash: url.hash,
      search: url.search,
      pathname: url.pathname,
      path: `${url.pathname || ""}${url.search || ""}`,
      href: url.href,
    };
    if (url.port !== "") options.port = Number(url.port);
    if (url.username || url.password) options.auth = `${decodeURIComponent(url.username)}:${decodeURIComponent(url.password)}`;
    return options;
  }

  module.exports = {
    URL,
    URLSearchParams,
    Url,
    parse,
    format,
    resolve,
    resolveObject: (from, to) => parse(resolve(typeof from === "string" ? from : format(from), to)),
    fileURLToPath,
    pathToFileURL,
    urlToHttpOptions,
    domainToASCII: (domain) => {
      try {
        return require("punycode").toASCII(String(domain).toLowerCase());
      } catch {
        return "";
      }
    },
    domainToUnicode: (domain) => {
      try {
        return require("punycode").toUnicode(String(domain).toLowerCase());
      } catch {
        return "";
      }
    },
  };
})
