// node:zlib for plugins: gzip, deflate and brotli, done by the host on whole
// buffers. A stream gathers what it receives and runs the codec when it
// ends, so it gives its output at the end rather than as it goes; what it
// gives is what Node's would.
(function (module, exports, require, host) {
  "use strict";
  const native = host.bind("zlib");
  const { invalidArgType } = require("internal/errors");
  const encoder = new TextEncoder();
  let BufferClass;
  const Buffer = () => (BufferClass ??= require("buffer").Buffer);

  const constants = Object.freeze({
    Z_NO_FLUSH: 0, Z_PARTIAL_FLUSH: 1, Z_SYNC_FLUSH: 2, Z_FULL_FLUSH: 3, Z_FINISH: 4, Z_BLOCK: 5,
    Z_OK: 0, Z_STREAM_END: 1, Z_NEED_DICT: 2, Z_ERRNO: -1, Z_STREAM_ERROR: -2, Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4, Z_BUF_ERROR: -5, Z_VERSION_ERROR: -6,
    Z_NO_COMPRESSION: 0, Z_BEST_SPEED: 1, Z_BEST_COMPRESSION: 9, Z_DEFAULT_COMPRESSION: -1,
    Z_FILTERED: 1, Z_HUFFMAN_ONLY: 2, Z_RLE: 3, Z_FIXED: 4, Z_DEFAULT_STRATEGY: 0, ZLIB_VERNUM: 4865,
    DEFLATE: 1, INFLATE: 2, GZIP: 3, GUNZIP: 4, DEFLATERAW: 5, INFLATERAW: 6, UNZIP: 7,
    BROTLI_DECODE: 8, BROTLI_ENCODE: 9,
    Z_MIN_WINDOWBITS: 8, Z_MAX_WINDOWBITS: 15, Z_DEFAULT_WINDOWBITS: 15,
    Z_MIN_CHUNK: 64, Z_MAX_CHUNK: Infinity, Z_DEFAULT_CHUNK: 16384,
    Z_MIN_MEMLEVEL: 1, Z_MAX_MEMLEVEL: 9, Z_DEFAULT_MEMLEVEL: 8,
    Z_MIN_LEVEL: -1, Z_MAX_LEVEL: 9, Z_DEFAULT_LEVEL: -1,
    BROTLI_OPERATION_PROCESS: 0, BROTLI_OPERATION_FLUSH: 1, BROTLI_OPERATION_FINISH: 2,
    BROTLI_OPERATION_EMIT_METADATA: 3,
    BROTLI_PARAM_MODE: 0, BROTLI_MODE_GENERIC: 0, BROTLI_MODE_TEXT: 1, BROTLI_MODE_FONT: 2,
    BROTLI_DEFAULT_MODE: 0, BROTLI_PARAM_QUALITY: 1, BROTLI_MIN_QUALITY: 0, BROTLI_MAX_QUALITY: 11,
    BROTLI_DEFAULT_QUALITY: 11, BROTLI_PARAM_LGWIN: 2, BROTLI_MIN_WINDOW_BITS: 10,
    BROTLI_MAX_WINDOW_BITS: 24, BROTLI_LARGE_MAX_WINDOW_BITS: 30, BROTLI_DEFAULT_WINDOW: 22,
    BROTLI_PARAM_LGBLOCK: 3, BROTLI_MIN_INPUT_BLOCK_BITS: 16, BROTLI_MAX_INPUT_BLOCK_BITS: 24,
    BROTLI_PARAM_DISABLE_LITERAL_CONTEXT_MODELING: 4, BROTLI_PARAM_SIZE_HINT: 5,
    BROTLI_PARAM_LARGE_WINDOW: 6, BROTLI_PARAM_NPOSTFIX: 7, BROTLI_PARAM_NDIRECT: 8,
    BROTLI_DECODER_RESULT_ERROR: 0, BROTLI_DECODER_RESULT_SUCCESS: 1,
    BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT: 2, BROTLI_DECODER_RESULT_NEEDS_MORE_OUTPUT: 3,
    BROTLI_DECODER_PARAM_DISABLE_RING_BUFFER_REALLOCATION: 0, BROTLI_DECODER_PARAM_LARGE_WINDOW: 1,
  });
  const codes = {};
  for (const name of ["Z_OK", "Z_STREAM_END", "Z_NEED_DICT", "Z_ERRNO", "Z_STREAM_ERROR", "Z_DATA_ERROR", "Z_MEM_ERROR", "Z_BUF_ERROR", "Z_VERSION_ERROR"]) {
    codes[name] = constants[name];
    codes[constants[name]] = name;
  }

  const bytes = (data) => {
    if (typeof data === "string") return encoder.encode(data);
    if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    throw invalidArgType("buffer", "of type string or an instance of Buffer, TypedArray, DataView, or ArrayBuffer", data);
  };
  // run runs the codec op over data with options: level, or brotli's
  // quality, and maxOutputLength.
  const run = (op, data, options = {}) => {
    const brotli = op.startsWith("brotli");
    const level = brotli ? options.params?.[constants.BROTLI_PARAM_QUALITY] : options.level;
    return Buffer().from(native.process(op, bytes(data), level, options.maxOutputLength));
  };

  const operations = {
    gzip: "gzip", gunzip: "gunzip", deflate: "deflate", inflate: "inflate",
    deflateRaw: "deflateRaw", inflateRaw: "inflateRaw", unzip: "unzip",
    brotliCompress: "brotliCompress", brotliDecompress: "brotliDecompress",
  };
  const zlib = { constants, codes };
  for (const [name, op] of Object.entries(operations)) {
    zlib[`${name}Sync`] = (buffer, options) => run(op, buffer, options);
    zlib[name] = (buffer, options, callback) => {
      if (typeof options === "function") [callback, options] = [options, {}];
      if (typeof callback !== "function") throw invalidArgType("callback", "of type function", callback);
      let result;
      try {
        result = run(op, buffer, options);
      } catch (error) {
        queueMicrotask(() => callback(error));
        return;
      }
      queueMicrotask(() => callback(null, result));
    };
  }

  // The stream classes load node:stream the first time a plugin makes one.
  const classNames = {
    Gzip: "gzip", Gunzip: "gunzip", Deflate: "deflate", Inflate: "inflate",
    DeflateRaw: "deflateRaw", InflateRaw: "inflateRaw", Unzip: "unzip",
    BrotliCompress: "brotliCompress", BrotliDecompress: "brotliDecompress",
  };
  let classes;
  const streamClasses = () => {
    if (classes !== undefined) return classes;
    const { Transform } = require("stream");
    class Zlib extends Transform {
      #op;
      #options;
      #chunks = [];
      constructor(op, options = {}) {
        super({ ...options, readableHighWaterMark: options.chunkSize, writableHighWaterMark: options.chunkSize });
        this.#op = op;
        this.#options = options;
        this.bytesWritten = 0;
        this._handle = {};
      }
      _transform(chunk, encoding, callback) {
        const data = bytes(typeof chunk === "string" ? Buffer().from(chunk, encoding) : chunk);
        this.#chunks.push(data.slice());
        this.bytesWritten += data.byteLength;
        callback();
      }
      _flush(callback) {
        try {
          const output = run(this.#op, Buffer().concat(this.#chunks), this.#options);
          this.#chunks = [];
          if (output.length > 0) this.push(output);
          callback();
        } catch (error) {
          callback(error);
        }
      }
      flush(kind, callback) {
        if (typeof kind === "function") callback = kind;
        if (typeof callback === "function") queueMicrotask(callback);
      }
      params(level, strategy, callback) {
        this.#options = { ...this.#options, level };
        if (typeof callback === "function") queueMicrotask(callback);
      }
      reset() {
        this.#chunks = [];
      }
      close(callback) {
        if (typeof callback === "function") this.once("close", callback);
        this.destroy();
      }
    }
    classes = {};
    for (const [className, op] of Object.entries(classNames)) {
      classes[className] = { [className]: class extends Zlib {
        constructor(options) {
          super(op, options);
        }
      } }[className];
    }
    return classes;
  };
  for (const [className] of Object.entries(classNames)) {
    const make = (options) => new (streamClasses()[className])(options);
    const constructor = { [className]: function (options) {
      return make(options);
    } }[className];
    Object.defineProperty(constructor, Symbol.hasInstance, {
      value: (value) => classes !== undefined && value instanceof classes[className],
    });
    zlib[className] = constructor;
    zlib[`create${className}`] = make;
  }
  zlib.crc32 = (data, value = 0) => native.crc32(bytes(data), value);
  module.exports = zlib;
})
