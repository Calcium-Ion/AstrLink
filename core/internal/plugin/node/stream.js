// node:stream for plugins: the readable-stream package, Node's streams as
// a package (internal/vendor/stream.js), whose module is the legacy Stream
// with the classes and functions on it, as Node's is, and the adapters to
// the web streams.
(function (module, exports, require) {
  "use strict";
  const Stream = require("internal/vendor/stream");

  // duplexPair of Node 22.6: two duplexes, each reading what the other
  // writes.
  if (typeof Stream.duplexPair !== "function") {
    Stream.duplexPair = (options) => {
      const sides = [];
      const side = (index) =>
        new Stream.Duplex({
          ...options,
          read() {},
          write(chunk, encoding, callback) {
            sides[1 - index].push(chunk, encoding);
            callback();
          },
          final(callback) {
            sides[1 - index].push(null);
            callback();
          },
        });
      sides.push(side(0), side(1));
      return sides;
    };
  }
  // The adapters between Node's streams and the web's of Node 17, on the
  // web streams of moejs.
  const { Readable, Writable } = Stream;
  if (typeof globalThis.ReadableStream === "function") {
    const { invalidArgType } = require("internal/errors");
    const { Buffer } = require("buffer");
    const encoder = new TextEncoder();
    Readable.fromWeb = function fromWeb(readableStream, options = {}) {
      if (!(readableStream instanceof ReadableStream))
        throw invalidArgType("readableStream", "an instance of ReadableStream", readableStream);
      const reader = readableStream.getReader();
      let closed = false;
      reader.closed.then(
        () => (closed = true),
        () => (closed = true),
      );
      return new Readable({
        objectMode: options.objectMode,
        highWaterMark: options.highWaterMark,
        encoding: options.encoding,
        signal: options.signal,
        read() {
          reader.read().then(
            ({ value, done }) => {
              if (done) this.push(null);
              else if (!options.objectMode && value instanceof Uint8Array)
                this.push(Buffer.from(value.buffer, value.byteOffset, value.byteLength));
              else this.push(value);
            },
            (error) => this.destroy(error),
          );
        },
        destroy(error, callback) {
          if (closed) return callback(error);
          reader.cancel(error ?? undefined).then(
            () => callback(error),
            () => callback(error),
          );
        },
      });
    };
    Readable.toWeb = function toWeb(streamReadable, options = {}) {
      return new ReadableStream(
        {
          start(controller) {
            streamReadable.pause();
            streamReadable.on("data", (chunk) => {
              controller.enqueue(typeof chunk === "string" ? encoder.encode(chunk) : chunk);
              if (controller.desiredSize <= 0) streamReadable.pause();
            });
            streamReadable.once("end", () => controller.close());
            streamReadable.once("error", (error) => controller.error(error));
          },
          pull() {
            streamReadable.resume();
          },
          cancel(reason) {
            streamReadable.destroy(reason);
          },
        },
        options.strategy,
      );
    };
    Writable.fromWeb = function fromWeb(writableStream, options = {}) {
      if (!(writableStream instanceof WritableStream))
        throw invalidArgType("writableStream", "an instance of WritableStream", writableStream);
      const writer = writableStream.getWriter();
      return new Writable({
        objectMode: options.objectMode,
        highWaterMark: options.highWaterMark,
        decodeStrings: options.decodeStrings,
        signal: options.signal,
        write(chunk, encoding, callback) {
          writer.write(chunk).then(() => callback(), callback);
        },
        final(callback) {
          writer.close().then(() => callback(), callback);
        },
        destroy(error, callback) {
          writer.abort(error ?? undefined).then(
            () => callback(error),
            () => callback(error),
          );
        },
      });
    };
    Writable.toWeb = function toWeb(streamWritable) {
      return new WritableStream({
        write(chunk) {
          return new Promise((resolve, reject) =>
            streamWritable.write(chunk, (error) => (error ? reject(error) : resolve())),
          );
        },
        close() {
          return new Promise((resolve) => streamWritable.end(resolve));
        },
        abort(reason) {
          streamWritable.destroy(reason);
        },
      });
    };
  }
  module.exports = Stream;
})
