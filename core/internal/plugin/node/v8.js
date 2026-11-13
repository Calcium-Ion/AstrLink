// node:v8 for plugins: moejs is not V8. The heap statistics describe the
// call's memory limit, and serialize and deserialize round-trip values in a
// format of their own, not V8's.
(function (module, exports, require, host) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "plugins run in moejs, which has no V8 internals";
  const memory = () => host.bind("process").memory();
  // tag turns a value into JSON that keeps what JSON loses.
  const tag = (value) => {
    if (value === undefined) return { $: "u" };
    if (typeof value === "bigint") return { $: "b", v: String(value) };
    if (typeof value === "number" && !Number.isFinite(value)) return { $: "n", v: String(value) };
    if (value === null || typeof value !== "object") return value;
    if (value instanceof Date) return { $: "d", v: value.getTime() };
    if (value instanceof RegExp) return { $: "r", s: value.source, f: value.flags };
    if (value instanceof Map) return { $: "m", v: [...value].map(([key, item]) => [tag(key), tag(item)]) };
    if (value instanceof Set) return { $: "s", v: [...value].map(tag) };
    if (ArrayBuffer.isView(value)) return { $: "t", c: value.constructor.name, v: [...new Uint8Array(value.buffer, value.byteOffset, value.byteLength)] };
    if (value instanceof Error) return { $: "e", n: value.name, m: value.message, s: value.stack };
    if (Array.isArray(value)) return value.map(tag);
    const object = {};
    for (const key of Object.keys(value)) object[key] = tag(value[key]);
    return { $: "o", v: object };
  };
  const untag = (value) => {
    if (Array.isArray(value)) return value.map(untag);
    if (value === null || typeof value !== "object") return value;
    switch (value.$) {
      case "u":
        return undefined;
      case "b":
        return BigInt(value.v);
      case "n":
        return Number(value.v);
      case "d":
        return new Date(value.v);
      case "r":
        return new RegExp(value.s, value.f);
      case "m":
        return new Map(value.v.map(([key, item]) => [untag(key), untag(item)]));
      case "s":
        return new Set(value.v.map(untag));
      case "t": {
        const bytes = Uint8Array.from(value.v);
        if (value.c === "Buffer") return require("buffer").Buffer.from(bytes);
        const Type = globalThis[value.c] ?? Uint8Array;
        return Type === DataView ? new DataView(bytes.buffer) : new Type(bytes.buffer);
      }
      case "e": {
        const error = new Error(value.m);
        error.name = value.n;
        error.stack = value.s;
        return error;
      }
      case "o": {
        const object = {};
        for (const key of Object.keys(value.v)) object[key] = untag(value.v[key]);
        return object;
      }
    }
    return value;
  };
  module.exports = {
    cachedDataVersionTag: () => 0,
    getHeapStatistics() {
      const usage = memory();
      return {
        total_heap_size: usage.heapTotal, total_heap_size_executable: 0, total_physical_size: usage.heapTotal,
        total_available_size: Math.max(usage.heapTotal - usage.heapUsed, 0), used_heap_size: usage.heapUsed,
        heap_size_limit: usage.heapTotal, malloced_memory: 0, peak_malloced_memory: 0, does_zap_garbage: 0,
        number_of_native_contexts: 1, number_of_detached_contexts: 0, total_global_handles_size: 0,
        used_global_handles_size: 0, external_memory: 0,
      };
    },
    getHeapSpaceStatistics: () => [],
    getHeapCodeStatistics: () => ({ code_and_metadata_size: 0, bytecode_and_metadata_size: 0, external_script_source_size: 0, cpu_profiler_metadata_size: 0 }),
    serialize: (value) => require("buffer").Buffer.from(JSON.stringify(tag(value))),
    deserialize: (buffer) => untag(JSON.parse(new TextDecoder().decode(buffer))),
    setFlagsFromString() {},
    setHeapSnapshotNearHeapLimit() {},
    takeCoverage() {},
    stopCoverage() {},
    getHeapSnapshot() {
      throw unavailable("v8.getHeapSnapshot()", reason);
    },
    writeHeapSnapshot() {
      throw unavailable("v8.writeHeapSnapshot()", reason);
    },
  };
})
