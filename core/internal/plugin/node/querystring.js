// node:querystring for plugins: Node's query string parser and formatter.
(function (module, exports, require) {
  "use strict";
  const escape = (text) => encodeURIComponent(text);
  const unescapeBuffer = (text, decodeSpaces) => {
    const { Buffer } = require("buffer");
    const bytes = [];
    for (let index = 0; index < text.length; index++) {
      const char = text[index];
      if (char === "+" && decodeSpaces) bytes.push(32);
      else if (char === "%" && /^[0-9a-f]{2}$/i.test(text.slice(index + 1, index + 3))) {
        bytes.push(parseInt(text.slice(index + 1, index + 3), 16));
        index += 2;
      } else for (const byte of new TextEncoder().encode(char)) bytes.push(byte);
    }
    return Buffer.from(bytes);
  };
  const unescape = (text, decodeSpaces) => {
    try {
      return decodeURIComponent(text);
    } catch {
      return unescapeBuffer(text, decodeSpaces).toString();
    }
  };
  const primitive = (value) => {
    if (typeof value === "string") return value;
    if ((typeof value === "number" && Number.isFinite(value)) || typeof value === "bigint" || typeof value === "boolean") return String(value);
    return "";
  };
  function stringify(object, sep = "&", eq = "=", options) {
    const encode = options?.encodeURIComponent ?? escape;
    if (object === null || typeof object !== "object") return "";
    const parts = [];
    for (const key of Object.keys(object)) {
      const value = object[key];
      const name = encode(primitive(key)) + eq;
      if (Array.isArray(value)) for (const each of value) parts.push(name + encode(primitive(each)));
      else parts.push(name + encode(primitive(value)));
    }
    return parts.join(sep);
  }
  function parse(text, sep = "&", eq = "=", options) {
    const result = Object.create(null);
    if (typeof text !== "string" || text.length === 0) return result;
    const maxKeys = options?.maxKeys ?? 1000;
    const decode = options?.decodeURIComponent ?? unescape;
    let pairs = text.split(sep);
    if (maxKeys > 0) pairs = pairs.slice(0, maxKeys);
    for (const pair of pairs) {
      if (pair === "") continue;
      const at = pair.indexOf(eq);
      const rawKey = at === -1 ? pair : pair.slice(0, at);
      const rawValue = at === -1 ? "" : pair.slice(at + eq.length);
      const key = decode(rawKey.replace(/\+/g, " "), true);
      const value = decode(rawValue.replace(/\+/g, " "), true);
      if (!(key in result)) result[key] = value;
      else if (Array.isArray(result[key])) result[key].push(value);
      else result[key] = [result[key], value];
    }
    return result;
  }
  module.exports = { parse, stringify, decode: parse, encode: stringify, escape, unescape, unescapeBuffer };
})
