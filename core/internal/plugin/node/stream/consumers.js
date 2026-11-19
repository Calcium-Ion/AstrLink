// node:stream/consumers: a readable stream or async iterable read whole.
(function (module, exports, require) {
  "use strict";
  const { notImplemented } = require("internal/errors");
  async function buffer(stream) {
    const { Buffer } = require("buffer");
    const chunks = [];
    for await (const chunk of stream)
      chunks.push(typeof chunk === "string" ? Buffer.from(chunk) : chunk);
    return Buffer.concat(chunks);
  }
  async function text(stream) {
    const decoder = new TextDecoder();
    let out = "";
    for await (const chunk of stream)
      out += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
    return out + decoder.decode();
  }
  async function json(stream) {
    return JSON.parse(await text(stream));
  }
  async function arrayBuffer(stream) {
    const whole = await buffer(stream);
    return whole.buffer.slice(whole.byteOffset, whole.byteOffset + whole.byteLength);
  }
  async function blob() {
    throw notImplemented("stream/consumers.blob()");
  }
  module.exports = { arrayBuffer, blob, buffer, json, text };
})
