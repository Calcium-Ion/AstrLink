// node:buffer for plugins: moejs's (package node), with what Node's module
// has beside it: the bigint reads and writes of Buffer, copyBytesFrom,
// SlowBuffer and transcode.
(function (module, exports, require, host) {
  "use strict";
  const binding = host.official("buffer");
  const { Buffer } = binding;
  const { invalidArgType, outOfRange, coded } = require("internal/errors");
  const bytes = (input) =>
    ArrayBuffer.isView(input)
      ? new Uint8Array(input.buffer, input.byteOffset, input.byteLength)
      : new Uint8Array(input);
  const define = (target, name, value) =>
    Object.defineProperty(target, name, {
      value,
      writable: true,
      enumerable: true,
      configurable: true,
    });

  // checkOffset checks where 8 bytes are read or written, as Node does.
  const checkOffset = (buffer, offset) => {
    if (typeof offset !== "number")
      throw invalidArgType("offset", "of type number", offset);
    if (!Number.isInteger(offset))
      throw outOfRange("offset", "an integer", offset);
    if (buffer.length < 8)
      throw coded(RangeError, "ERR_BUFFER_OUT_OF_BOUNDS", "Attempt to access memory outside buffer bounds");
    if (offset < 0 || offset > buffer.length - 8)
      throw outOfRange("offset", `>= 0 and <= ${buffer.length - 8}`, offset);
  };
  const view = (buffer) => new DataView(buffer.buffer, buffer.byteOffset, buffer.byteLength);
  const bigintRange = {
    Int: [-(2n ** 63n), 2n ** 63n - 1n, "-(2n ** 63n) and < 2n ** 63n"],
    UInt: [0n, 2n ** 64n - 1n, ">= 0n and < 2n ** 64n"],
  };
  for (const kind of ["Int", "UInt"]) {
    for (const end of ["LE", "BE"]) {
      const little = end === "LE";
      const read = kind === "Int" ? DataView.prototype.getBigInt64 : DataView.prototype.getBigUint64;
      const write = kind === "Int" ? DataView.prototype.setBigInt64 : DataView.prototype.setBigUint64;
      const readName = `readBig${kind}64${end}`;
      const writeName = `writeBig${kind}64${end}`;
      if (typeof Buffer.prototype[readName] !== "function") {
        define(Buffer.prototype, readName, {
          [readName](offset = 0) {
            checkOffset(this, offset);
            return read.call(view(this), offset, little);
          },
        }[readName]);
      }
      if (typeof Buffer.prototype[writeName] !== "function") {
        define(Buffer.prototype, writeName, {
          [writeName](value, offset = 0) {
            if (typeof value !== "bigint")
              throw invalidArgType("value", "of type bigint", value);
            const [low, high, range] = bigintRange[kind];
            if (value < low || value > high)
              throw outOfRange("value", kind === "Int" ? `>= ${range}` : range, value);
            checkOffset(this, offset);
            write.call(view(this), offset, value, little);
            return offset + 8;
          },
        }[writeName]);
      }
      if (kind === "UInt") {
        for (const [alias, original] of [
          [`readBigUint64${end}`, readName],
          [`writeBigUint64${end}`, writeName],
        ]) {
          if (typeof Buffer.prototype[alias] !== "function")
            define(Buffer.prototype, alias, Buffer.prototype[original]);
        }
      }
    }
  }

  // Buffer.copyBytesFrom of Node 19.8.
  if (typeof Buffer.copyBytesFrom !== "function") {
    define(Buffer, "copyBytesFrom", (view, offset = 0, length) => {
      if (!ArrayBuffer.isView(view))
        throw invalidArgType("view", "an instance of TypedArray", view);
      const size = view.BYTES_PER_ELEMENT ?? 1;
      const end = length === undefined ? view.length : offset + length;
      return Buffer.from(
        bytes(view).slice(offset * size, Math.min(end, view.length) * size),
      );
    });
  }

  function SlowBuffer(size) {
    return Buffer.allocUnsafeSlow(size);
  }

  module.exports = {
    Buffer,
    SlowBuffer,
    INSPECT_MAX_BYTES: binding.INSPECT_MAX_BYTES,
    kMaxLength: binding.kMaxLength,
    kStringMaxLength: binding.kStringMaxLength,
    constants: binding.constants,
    atob: binding.atob,
    btoa: binding.btoa,
    isUtf8: binding.isUtf8,
    isAscii: binding.isAscii,
    transcode(source, fromEncoding, toEncoding) {
      return Buffer.from(Buffer.from(source).toString(fromEncoding), toEncoding);
    },
  };
})
