// node:fs for plugins: the synchronous, callback and promise forms, file
// handles, streams, directories and glob. Every path is in the plugin's
// own directories: its data directory, which is the working directory and
// which it reads and writes, and its install directory, which it reads.
// Anywhere else is EACCES. moejs's node:fs does what it implements on the
// data directory (see the end); here the host does the work at once, and
// the callback and promise forms settle after it, as Node's would.
(function (module, exports, require, host) {
  "use strict";
  const native = host.bind("fs");
  const errors = require("internal/errors");
  const { invalidArgType, invalidArgValue, coded, notImplemented, unavailable } = errors;

  const constants = Object.freeze({ ...native.constants });
  const { O_RDONLY, O_WRONLY, O_RDWR, O_CREAT, O_EXCL, O_TRUNC, O_APPEND, O_SYNC, F_OK } = constants;
  const kType = Symbol("type");
  const kStats = Symbol("stats");
  const utf8 = new TextDecoder();
  const encoder = new TextEncoder();
  let BufferClass;
  const Buffer = () => (BufferClass ??= require("buffer").Buffer);

  // toPath reads a path: a string, bytes or a file: URL.
  const toPath = (path, name = "path") => {
    if (typeof path !== "string") {
      if (path instanceof URL) {
        if (path.protocol !== "file:")
          throw coded(TypeError, "ERR_INVALID_URL_SCHEME", "The URL must be of scheme file");
        path = require("url").fileURLToPath(path);
      } else if (ArrayBuffer.isView(path)) {
        path = utf8.decode(path);
      } else {
        throw invalidArgType(name, "of type string or an instance of Buffer or URL", path);
      }
    }
    if (path.includes("\u0000"))
      throw invalidArgValue(name, path, "must be a string, Uint8Array, or URL without null bytes");
    return path;
  };
  const toFd = (fd) => {
    if (!Number.isInteger(fd) || fd < 0)
      throw invalidArgType("fd", "of type number", fd);
    return fd;
  };
  const toFlags = (flags, fallback) => {
    if (flags === undefined || flags === null) flags = fallback;
    if (typeof flags === "number") return flags;
    switch (flags) {
      case "r":
        return O_RDONLY;
      case "rs":
      case "sr":
        return O_RDONLY | O_SYNC;
      case "r+":
        return O_RDWR;
      case "rs+":
      case "sr+":
        return O_RDWR | O_SYNC;
      case "w":
        return O_TRUNC | O_CREAT | O_WRONLY;
      case "wx":
      case "xw":
        return O_TRUNC | O_CREAT | O_WRONLY | O_EXCL;
      case "w+":
        return O_TRUNC | O_CREAT | O_RDWR;
      case "wx+":
      case "xw+":
        return O_TRUNC | O_CREAT | O_RDWR | O_EXCL;
      case "a":
        return O_APPEND | O_CREAT | O_WRONLY;
      case "ax":
      case "xa":
        return O_APPEND | O_CREAT | O_WRONLY | O_EXCL;
      case "as":
      case "sa":
        return O_APPEND | O_CREAT | O_WRONLY | O_SYNC;
      case "a+":
        return O_APPEND | O_CREAT | O_RDWR;
      case "ax+":
      case "xa+":
        return O_APPEND | O_CREAT | O_RDWR | O_EXCL;
      case "as+":
      case "sa+":
        return O_APPEND | O_CREAT | O_RDWR | O_SYNC;
    }
    throw invalidArgValue("flags", flags);
  };
  const toMode = (mode, fallback) => {
    if (mode === undefined || mode === null) return fallback;
    if (typeof mode === "string") {
      if (!/^[0-7]+$/.test(mode))
        throw invalidArgValue("mode", mode, "must be a 32-bit unsigned integer or an octal string");
      return parseInt(mode, 8);
    }
    if (typeof mode !== "number") throw invalidArgType("mode", "of type number", mode);
    return mode;
  };
  const toTime = (time, name) => {
    if (time instanceof Date) return time.getTime() / 1000;
    if (typeof time === "number") return time;
    if (typeof time === "string" && time !== "" && !Number.isNaN(Number(time))) return Number(time);
    if (typeof time === "bigint") return Number(time);
    throw invalidArgType(name, "of type number or an instance of Date", time);
  };
  const optionsOf = (value, defaults) => {
    if (value === undefined || value === null || typeof value === "function") return { ...defaults };
    if (typeof value === "string") return { ...defaults, encoding: value };
    if (typeof value !== "object") throw invalidArgType("options", "of type string or an instance of Object", value);
    return { ...defaults, ...value };
  };
  const isUtf8 = (encoding) => encoding === "utf8" || encoding === "utf-8" || encoding === "UTF8" || encoding === "UTF-8";
  // bytesOf is the bytes of data to write, a string in encoding.
  const bytesOf = (data, encoding = "utf8", name = "data") => {
    if (typeof data === "string") return isUtf8(encoding ?? "utf8") ? encoder.encode(data) : Buffer().from(data, encoding);
    if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    throw invalidArgType(name, "of type string or an instance of Buffer, TypedArray, or DataView", data);
  };
  // decode turns what the host read into what Node returns for encoding.
  const decode = (buffer, encoding) => {
    if (encoding === undefined || encoding === null || encoding === "buffer") return Buffer().from(buffer);
    if (isUtf8(encoding)) return utf8.decode(buffer);
    return Buffer().from(buffer).toString(encoding);
  };
  const name = (text, encoding) => (encoding === "buffer" ? Buffer().from(text) : text);

  class Stats {
    constructor(dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks, atimeMs, mtimeMs, ctimeMs, birthtimeMs) {
      Object.assign(this, { dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks, atimeMs, mtimeMs, ctimeMs, birthtimeMs });
      this.atime = new Date(atimeMs);
      this.mtime = new Date(mtimeMs);
      this.ctime = new Date(ctimeMs);
      this.birthtime = new Date(birthtimeMs);
    }
  }
  class BigIntStats {
    constructor(...values) {
      const names = ["dev", "mode", "nlink", "uid", "gid", "rdev", "blksize", "ino", "size", "blocks"];
      names.forEach((field, index) => (this[field] = BigInt(Math.trunc(values[index]))));
      const times = ["atime", "mtime", "ctime", "birthtime"];
      times.forEach((field, index) => {
        const ms = values[10 + index];
        this[`${field}Ms`] = BigInt(Math.trunc(ms));
        this[`${field}Ns`] = BigInt(Math.trunc(ms)) * 1000000n + BigInt(Math.round((ms % 1) * 1e6));
        this[field] = new Date(ms);
      });
    }
  }
  const typeChecks = {
    isFile() {
      return (Number(this.mode) & 0o170000) === 0o100000;
    },
    isDirectory() {
      return (Number(this.mode) & 0o170000) === 0o040000;
    },
    isSymbolicLink() {
      return (Number(this.mode) & 0o170000) === 0o120000;
    },
    isFIFO() {
      return (Number(this.mode) & 0o170000) === 0o010000;
    },
    isSocket() {
      return (Number(this.mode) & 0o170000) === 0o140000;
    },
    isCharacterDevice() {
      return (Number(this.mode) & 0o170000) === 0o020000;
    },
    isBlockDevice() {
      return (Number(this.mode) & 0o170000) === 0o060000;
    },
  };
  for (const [key, check] of Object.entries(typeChecks)) {
    Object.defineProperty(Stats.prototype, key, { value: check, writable: true, configurable: true });
    Object.defineProperty(BigIntStats.prototype, key, { value: check, writable: true, configurable: true });
  }
  const makeStats = (values, bigint) => (bigint ? new BigIntStats(...values) : new Stats(...values));

  class Dirent {
    constructor(name, type, parentPath) {
      this.name = name;
      this.parentPath = parentPath;
      this.path = parentPath;
      this[kType] = type;
    }
    isFile() {
      return this[kType] === constants.UV_DIRENT_FILE;
    }
    isDirectory() {
      return this[kType] === constants.UV_DIRENT_DIR;
    }
    isSymbolicLink() {
      return this[kType] === constants.UV_DIRENT_LINK;
    }
    isFIFO() {
      return this[kType] === constants.UV_DIRENT_FIFO;
    }
    isSocket() {
      return this[kType] === constants.UV_DIRENT_SOCKET;
    }
    isCharacterDevice() {
      return this[kType] === constants.UV_DIRENT_CHAR;
    }
    isBlockDevice() {
      return this[kType] === constants.UV_DIRENT_BLOCK;
    }
  }

  // The standard streams, fs.writeSync(1, ...) and fs.writeSync(2, ...),
  // write to the plugin's log.
  const standard = (fd) => (fd === 1 ? require("process").stdout : fd === 2 ? require("process").stderr : undefined);

  const readSync = (fd, buffer, offsetOrOptions, length, position) => {
    toFd(fd);
    let offset = offsetOrOptions;
    if (offsetOrOptions !== null && typeof offsetOrOptions === "object" && !ArrayBuffer.isView(offsetOrOptions)) {
      ({ offset = 0, length = buffer.byteLength - offset, position = null } = offsetOrOptions);
    }
    if (!ArrayBuffer.isView(buffer)) throw invalidArgType("buffer", "an instance of Buffer, TypedArray, or DataView", buffer);
    offset ??= 0;
    length ??= buffer.byteLength - offset;
    if (typeof position === "bigint") position = Number(position);
    if (length === 0) return 0;
    if (fd === 0) return 0;
    const view = new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength);
    return native.read(fd, view, offset, length, position === null || position === undefined || position < 0 ? -1 : position);
  };
  const writeSync = (fd, buffer, offsetOrOptions, length, position) => {
    toFd(fd);
    let bytes;
    if (typeof buffer === "string") {
      // writeSync(fd, string, position, encoding)
      bytes = bytesOf(buffer, typeof length === "string" ? length : "utf8");
      position = offsetOrOptions;
    } else {
      if (!ArrayBuffer.isView(buffer)) throw invalidArgType("buffer", "of type string or an instance of Buffer, TypedArray, or DataView", buffer);
      let offset = offsetOrOptions;
      if (offsetOrOptions !== null && typeof offsetOrOptions === "object") {
        ({ offset = 0, length = buffer.byteLength - offset, position = null } = offsetOrOptions);
      }
      offset ??= 0;
      length ??= buffer.byteLength - offset;
      bytes = new Uint8Array(buffer.buffer, buffer.byteOffset + offset, length);
    }
    const stdio = standard(fd);
    if (stdio !== undefined) {
      stdio.write(bytes);
      return bytes.byteLength;
    }
    return native.write(fd, bytes, position === null || position === undefined || position < 0 ? -1 : position);
  };
  // readAll reads an open file to its end.
  const readAll = (fd) => {
    const chunks = [];
    let total = 0;
    for (;;) {
      const chunk = new Uint8Array(64 * 1024);
      const read = native.read(fd, chunk, 0, chunk.byteLength, -1);
      if (read === 0) break;
      chunks.push(read === chunk.byteLength ? chunk : chunk.subarray(0, read));
      total += read;
    }
    const whole = new Uint8Array(total);
    let at = 0;
    for (const chunk of chunks) {
      whole.set(chunk, at);
      at += chunk.byteLength;
    }
    return whole.buffer;
  };
  const writeAll = (fd, bytes) => {
    let at = 0;
    while (at < bytes.byteLength) at += native.write(fd, bytes.subarray(at), -1);
  };

  const sync = {
    accessSync(path, mode = F_OK) {
      native.access(toPath(path), mode);
    },
    existsSync(path) {
      try {
        return native.exists(toPath(path));
      } catch {
        return false;
      }
    },
    statSync(path, options) {
      try {
        return makeStats(native.stat(toPath(path), false), options?.bigint);
      } catch (error) {
        if (options?.throwIfNoEntry === false && error.code === "ENOENT") return undefined;
        throw error;
      }
    },
    lstatSync(path, options) {
      try {
        return makeStats(native.stat(toPath(path), true), options?.bigint);
      } catch (error) {
        if (options?.throwIfNoEntry === false && error.code === "ENOENT") return undefined;
        throw error;
      }
    },
    fstatSync(fd, options) {
      return makeStats(native.fstat(toFd(fd)), options?.bigint);
    },
    statfsSync(path, options) {
      const [type, bsize, blocks, bfree, bavail, files, ffree] = native.statfs(toPath(path));
      const convert = options?.bigint ? BigInt : Number;
      return { type: convert(type), bsize: convert(bsize), blocks: convert(blocks), bfree: convert(bfree), bavail: convert(bavail), files: convert(files), ffree: convert(ffree) };
    },
    readFileSync(path, options) {
      const { encoding, flag } = optionsOf(options, { encoding: null, flag: "r" });
      if (typeof path === "number") return decode(readAll(toFd(path)), encoding);
      const flags = toFlags(flag, "r");
      if (flags !== O_RDONLY) {
        const fd = native.open(toPath(path), flags, 0o666);
        try {
          return decode(readAll(fd), encoding);
        } finally {
          native.close(fd);
        }
      }
      return decode(native.readFile(toPath(path)), encoding);
    },
    writeFileSync(file, data, options) {
      const { encoding, mode, flag } = optionsOf(options, { encoding: "utf8", mode: 0o666, flag: "w" });
      if (ArrayBuffer.isView(data) === false && typeof data !== "string" && !(data instanceof ArrayBuffer)) {
        if (data !== null && typeof data === "object" && (typeof data[Symbol.iterator] === "function"))
          data = Buffer().concat([...data].map((chunk) => Buffer().from(bytesOf(chunk, encoding))));
      }
      const bytes = bytesOf(data, encoding);
      if (typeof file === "number") {
        const stdio = standard(file);
        if (stdio !== undefined) {
          stdio.write(bytes);
          return;
        }
        writeAll(file, bytes);
        return;
      }
      native.writeFile(toPath(file, "file"), bytes, toFlags(flag, "w"), toMode(mode, 0o666));
    },
    appendFileSync(path, data, options) {
      const merged = optionsOf(options, { encoding: "utf8", mode: 0o666, flag: "a" });
      sync.writeFileSync(path, data, merged);
    },
    openSync(path, flags, mode) {
      return native.open(toPath(path), toFlags(flags, "r"), toMode(mode, 0o666));
    },
    closeSync(fd) {
      native.close(toFd(fd));
    },
    readSync,
    writeSync,
    readvSync(fd, buffers, position) {
      let total = 0;
      for (const buffer of buffers) {
        const read = readSync(fd, buffer, 0, buffer.byteLength, position === null || position === undefined ? null : position + total);
        total += read;
        if (read < buffer.byteLength) break;
      }
      return total;
    },
    writevSync(fd, buffers, position) {
      let total = 0;
      for (const buffer of buffers)
        total += writeSync(fd, buffer, 0, buffer.byteLength, position === null || position === undefined ? null : position + total);
      return total;
    },
    ftruncateSync(fd, length = 0) {
      native.ftruncate(toFd(fd), length);
    },
    truncateSync(path, length = 0) {
      if (typeof path === "number") return native.ftruncate(path, length);
      native.truncate(toPath(path), length);
    },
    fsyncSync(fd) {
      native.fsync(toFd(fd));
    },
    fdatasyncSync(fd) {
      native.fsync(toFd(fd));
    },
    readdirSync(path, options) {
      const { encoding, withFileTypes, recursive } = optionsOf(options, { encoding: "utf8", withFileTypes: false, recursive: false });
      const entries = native.readdir(toPath(path), Boolean(withFileTypes), Boolean(recursive));
      if (withFileTypes) return entries.map(([entry, type, parent]) => new Dirent(name(entry, encoding), type, parent));
      return entries.map((entry) => name(entry, encoding));
    },
    mkdirSync(path, options) {
      const { recursive, mode } = typeof options === "number" || typeof options === "string" ? { mode: options } : optionsOf(options, { recursive: false, mode: 0o777 });
      return native.mkdir(toPath(path), Boolean(recursive), toMode(mode, 0o777));
    },
    mkdtempSync(prefix, options) {
      const { encoding } = optionsOf(options, { encoding: "utf8" });
      return name(native.mkdtemp(toPath(prefix, "prefix")), encoding);
    },
    rmSync(path, options) {
      const { recursive, force } = optionsOf(options, { recursive: false, force: false });
      native.rm(toPath(path), Boolean(recursive), Boolean(force));
    },
    rmdirSync(path, options) {
      native.rmdir(toPath(path), Boolean(options?.recursive));
    },
    unlinkSync(path) {
      native.unlink(toPath(path));
    },
    renameSync(oldPath, newPath) {
      native.rename(toPath(oldPath, "oldPath"), toPath(newPath, "newPath"));
    },
    copyFileSync(src, dest, mode = 0) {
      native.copyFile(toPath(src, "src"), toPath(dest, "dest"), mode);
    },
    cpSync(src, dest, options) {
      const { recursive, force, errorOnExist, filter } = optionsOf(options, { recursive: false, force: true, errorOnExist: false });
      if (filter !== undefined) throw notImplemented("fs.cp() filter option");
      native.cp(toPath(src, "src"), toPath(dest, "dest"), Boolean(recursive), Boolean(force), Boolean(errorOnExist));
    },
    linkSync(existingPath, newPath) {
      native.link(toPath(existingPath, "existingPath"), toPath(newPath, "newPath"));
    },
    symlinkSync(target, path) {
      native.symlink(toPath(target, "target"), toPath(path));
    },
    readlinkSync(path, options) {
      const { encoding } = optionsOf(options, { encoding: "utf8" });
      return name(native.readlink(toPath(path)), encoding);
    },
    realpathSync(path, options) {
      const { encoding } = optionsOf(options, { encoding: "utf8" });
      return name(native.realpath(toPath(path)), encoding);
    },
    utimesSync(path, atime, mtime) {
      native.utimes(toPath(path), toTime(atime, "atime"), toTime(mtime, "mtime"));
    },
    lutimesSync(path, atime, mtime) {
      native.utimes(toPath(path), toTime(atime, "atime"), toTime(mtime, "mtime"));
    },
    futimesSync(fd, atime, mtime) {
      native.futimes(toFd(fd), toTime(atime, "atime"), toTime(mtime, "mtime"));
    },
    chmodSync(path, mode) {
      native.chmod(toPath(path), toMode(mode));
    },
    lchmodSync(path, mode) {
      native.chmod(toPath(path), toMode(mode));
    },
    fchmodSync(fd, mode) {
      native.fchmod(toFd(fd), toMode(mode));
    },
    chownSync(path, uid, gid) {
      native.chown(toPath(path), uid, gid, false);
    },
    lchownSync(path, uid, gid) {
      native.chown(toPath(path), uid, gid, true);
    },
    fchownSync() {
      throw notImplemented("fs.fchownSync()");
    },
    opendirSync(path, options) {
      return new Dir(toPath(path), sync.readdirSync(path, { ...optionsOf(options, {}), withFileTypes: true }));
    },
    globSync(pattern, options = {}) {
      const patterns = Array.isArray(pattern) ? pattern : [pattern];
      const cwd = options.cwd === undefined ? require("process").cwd() : toPath(options.cwd, "options.cwd");
      const windows = require("process").platform === "win32";
      const glob = require("internal/glob");
      const matchers = patterns.map((each) => glob.regExp(each, windows));
      const exclude = options.exclude;
      const separator = windows ? "\\" : "/";
      const found = [];
      for (const entry of native.readdir(cwd, true, true)) {
        const [entryName, type, parent] = entry;
        const full = parent + separator + entryName;
        const relative = full.slice(cwd.length + 1);
        const slashed = windows ? relative.replace(/\\/g, "/") : relative;
        if (!matchers.some((matcher) => matcher.test(slashed))) continue;
        const dirent = new Dirent(entryName, type, parent);
        if (typeof exclude === "function" && exclude(options.withFileTypes ? dirent : relative)) continue;
        if (Array.isArray(exclude) && exclude.some((each) => glob.matches(slashed, each, windows))) continue;
        found.push(options.withFileTypes ? dirent : relative);
      }
      return found;
    },
  };

  class Dir {
    #path;
    #entries;
    #closed = false;
    constructor(path, entries) {
      this.#path = path;
      this.#entries = entries;
    }
    get path() {
      return this.#path;
    }
    #check() {
      if (this.#closed) throw coded(Error, "ERR_DIR_CLOSED", "Directory handle was closed");
    }
    readSync() {
      this.#check();
      return this.#entries.shift() ?? null;
    }
    read(callback) {
      if (typeof callback === "function") {
        let result;
        let failure = null;
        try {
          result = this.readSync();
        } catch (error) {
          failure = error;
        }
        queueMicrotask(() => callback(failure, result));
        return;
      }
      return new Promise((resolve) => resolve(this.readSync()));
    }
    closeSync() {
      this.#check();
      this.#closed = true;
    }
    close(callback) {
      if (typeof callback === "function") {
        let failure = null;
        try {
          this.closeSync();
        } catch (error) {
          failure = error;
        }
        queueMicrotask(() => callback(failure));
        return;
      }
      return new Promise((resolve) => resolve(this.closeSync()));
    }
    async *entries() {
      try {
        for (let entry = this.readSync(); entry !== null; entry = this.readSync()) yield entry;
      } finally {
        if (!this.#closed) this.closeSync();
      }
    }
    [Symbol.asyncIterator]() {
      return this.entries();
    }
    [Symbol.asyncDispose ?? Symbol.for("nodejs.asyncDispose")]() {
      return this.#closed ? Promise.resolve() : this.close();
    }
  }

  // The callback forms run the synchronous one and call back afterwards.
  const later = (callback, ...args) => queueMicrotask(() => callback(...args));
  const callbackForm = (run, names = ["result"]) =>
    function (...args) {
      const callback = args.pop();
      if (typeof callback !== "function") throw invalidArgType("cb", "of type function", callback);
      let result;
      try {
        result = run(...args);
      } catch (error) {
        later(callback, error);
        return;
      }
      if (names.length === 0) later(callback, null);
      else later(callback, null, result);
    };

  const callbacks = {};
  for (const [syncName, run] of Object.entries(sync)) {
    const base = syncName.slice(0, -4);
    if (base === "exists" || base === "glob") continue;
    callbacks[base] = callbackForm(run);
  }
  callbacks.exists = (path, callback) => later(callback, sync.existsSync(path));
  callbacks.read = function (fd, buffer, offset, length, position, callback) {
    const args = [...arguments];
    callback = args.pop();
    if (typeof callback !== "function") throw invalidArgType("cb", "of type function", callback);
    if (args.length < 2 || !ArrayBuffer.isView(args[1])) {
      // read(fd, [options,] callback): a 16 KiB buffer unless given one.
      const options = args[1] ?? {};
      const target = options.buffer ?? Buffer().alloc(16384);
      args.splice(1, 1, target, options);
    }
    try {
      const read = readSync(...args);
      later(callback, null, read, args[1]);
    } catch (error) {
      later(callback, error);
    }
  };
  callbacks.write = function (fd, buffer, ...rest) {
    const callback = rest.pop();
    if (typeof callback !== "function") throw invalidArgType("cb", "of type function", callback);
    try {
      const written = writeSync(fd, buffer, ...rest);
      later(callback, null, written, buffer);
    } catch (error) {
      later(callback, error);
    }
  };
  callbacks.glob = (pattern, options, callback) => {
    if (typeof options === "function") {
      callback = options;
      options = {};
    }
    try {
      later(callback, null, sync.globSync(pattern, options));
    } catch (error) {
      later(callback, error);
    }
  };
  callbacks.realpath.native = callbacks.realpath;
  sync.realpathSync.native = sync.realpathSync;

  // The streams load node:stream the first time a plugin makes one.
  let streams;
  const streamClasses = () => {
    if (streams !== undefined) return streams;
    const { Readable, Writable } = require("stream");
    class ReadStream extends Readable {
      constructor(path, options) {
        options = optionsOf(options, { flags: "r", mode: 0o666, autoClose: true, emitClose: true, highWaterMark: 64 * 1024 });
        super({ highWaterMark: options.highWaterMark, encoding: options.encoding, emitClose: options.emitClose, signal: options.signal });
        this.path = options.fd === undefined || options.fd === null ? toPath(path) : undefined;
        this.fd = options.fd?.fd ?? options.fd ?? null;
        this.flags = options.flags;
        this.mode = options.mode;
        this.start = options.start;
        this.end = options.end ?? Infinity;
        this.pos = this.start;
        this.bytesRead = 0;
        this.autoClose = options.autoClose;
        this.pending = this.fd === null;
      }
      _construct(callback) {
        if (this.fd !== null) return callback();
        try {
          this.fd = native.open(this.path, toFlags(this.flags, "r"), toMode(this.mode, 0o666));
          this.pending = false;
          this.emit("open", this.fd);
          this.emit("ready");
          callback();
        } catch (error) {
          callback(error);
        }
      }
      _read(size) {
        let length = size;
        if (this.pos !== undefined) length = Math.min(size, this.end - this.pos + 1);
        else if (this.end !== Infinity) length = Math.min(size, this.end - this.bytesRead + 1);
        if (length <= 0) return this.push(null);
        const chunk = Buffer().allocUnsafe(length);
        try {
          const read = native.read(this.fd, chunk, 0, length, this.pos === undefined ? -1 : this.pos);
          if (this.pos !== undefined) this.pos += read;
          this.bytesRead += read;
          this.push(read === 0 ? null : read === length ? chunk : chunk.subarray(0, read));
        } catch (error) {
          this.destroy(error);
        }
      }
      _destroy(error, callback) {
        if (this.fd !== null && this.autoClose) {
          try {
            native.close(this.fd);
          } catch {}
          this.fd = null;
        }
        callback(error);
      }
      close(callback) {
        if (typeof callback === "function") this.once("close", () => callback(null));
        this.destroy();
      }
    }
    class WriteStream extends Writable {
      constructor(path, options) {
        options = optionsOf(options, { flags: "w", mode: 0o666, autoClose: true, emitClose: true, encoding: "utf8" });
        super({ highWaterMark: options.highWaterMark, decodeStrings: true, defaultEncoding: options.encoding, emitClose: options.emitClose, signal: options.signal });
        this.path = options.fd === undefined || options.fd === null ? toPath(path) : undefined;
        this.fd = options.fd?.fd ?? options.fd ?? null;
        this.flags = options.flags;
        this.mode = options.mode;
        this.start = options.start;
        this.pos = this.start;
        this.bytesWritten = 0;
        this.autoClose = options.autoClose;
        this.pending = this.fd === null;
      }
      _construct(callback) {
        if (this.fd !== null) return callback();
        try {
          this.fd = native.open(this.path, toFlags(this.flags, "w"), toMode(this.mode, 0o666));
          this.pending = false;
          this.emit("open", this.fd);
          this.emit("ready");
          callback();
        } catch (error) {
          callback(error);
        }
      }
      _write(chunk, encoding, callback) {
        try {
          const bytes = bytesOf(chunk, encoding);
          let at = 0;
          while (at < bytes.byteLength) {
            at += native.write(this.fd, bytes.subarray(at), this.pos === undefined ? -1 : this.pos + at);
          }
          if (this.pos !== undefined) this.pos += at;
          this.bytesWritten += at;
          callback();
        } catch (error) {
          callback(error);
        }
      }
      _destroy(error, callback) {
        if (this.fd !== null && this.autoClose) {
          try {
            native.close(this.fd);
          } catch {}
          this.fd = null;
        }
        callback(error);
      }
      close(callback) {
        if (typeof callback === "function") this.once("close", () => callback(null));
        if (!this.writableEnded) this.end();
        else this.destroy();
      }
      destroySoon() {
        this.end();
      }
    }
    streams = { ReadStream, WriteStream };
    return streams;
  };
  function ReadStream(path, options) {
    return new (streamClasses().ReadStream)(path, options);
  }
  function WriteStream(path, options) {
    return new (streamClasses().WriteStream)(path, options);
  }
  Object.defineProperty(ReadStream, Symbol.hasInstance, {
    value: (value) => streams !== undefined && value instanceof streams.ReadStream,
  });
  Object.defineProperty(WriteStream, Symbol.hasInstance, {
    value: (value) => streams !== undefined && value instanceof streams.WriteStream,
  });

  // promises: the promise forms, and FileHandle.
  const promised = (run) =>
    async function (...args) {
      return run(...args);
    };
  class FileHandle extends require("events") {
    #fd;
    constructor(fd) {
      super();
      this.#fd = fd;
    }
    get fd() {
      return this.#fd;
    }
    #open() {
      if (this.#fd === -1) throw coded(Error, "EBADF", "EBADF: bad file descriptor, file handle is closed");
      return this.#fd;
    }
    async read(buffer, offset, length, position) {
      if (buffer === undefined || (buffer !== null && typeof buffer === "object" && !ArrayBuffer.isView(buffer))) {
        const options = buffer ?? {};
        buffer = options.buffer ?? Buffer().alloc(16384);
        ({ offset = 0, length = buffer.byteLength - offset, position = null } = options);
      }
      const bytesRead = readSync(this.#open(), buffer, offset ?? 0, length ?? buffer.byteLength - (offset ?? 0), position);
      return { bytesRead, buffer };
    }
    async readv(buffers, position) {
      return { bytesRead: sync.readvSync(this.#open(), buffers, position), buffers };
    }
    async write(buffer, ...rest) {
      const bytesWritten = writeSync(this.#open(), buffer, ...rest);
      return { bytesWritten, buffer };
    }
    async writev(buffers, position) {
      return { bytesWritten: sync.writevSync(this.#open(), buffers, position), buffers };
    }
    async readFile(options) {
      const { encoding } = optionsOf(options, { encoding: null });
      return decode(readAll(this.#open()), encoding);
    }
    async writeFile(data, options) {
      const { encoding } = optionsOf(options, { encoding: "utf8" });
      writeAll(this.#open(), bytesOf(data, encoding));
    }
    async appendFile(data, options) {
      return this.writeFile(data, options);
    }
    async stat(options) {
      return sync.fstatSync(this.#open(), options);
    }
    async truncate(length = 0) {
      native.ftruncate(this.#open(), length);
    }
    async sync() {
      native.fsync(this.#open());
    }
    async datasync() {
      native.fsync(this.#open());
    }
    async chmod(mode) {
      native.fchmod(this.#open(), toMode(mode));
    }
    async chown() {
      throw notImplemented("filehandle.chown()");
    }
    async utimes(atime, mtime) {
      native.futimes(this.#open(), toTime(atime, "atime"), toTime(mtime, "mtime"));
    }
    async close() {
      if (this.#fd === -1) return;
      const fd = this.#fd;
      this.#fd = -1;
      native.close(fd);
      this.emit("close");
    }
    createReadStream(options) {
      return new (streamClasses().ReadStream)(undefined, { ...options, fd: this.#open(), autoClose: false });
    }
    createWriteStream(options) {
      return new (streamClasses().WriteStream)(undefined, { ...options, fd: this.#open(), autoClose: false });
    }
    async *readLines(options) {
      const text = await this.readFile({ encoding: options?.encoding ?? "utf8" });
      const lines = text.split(/\r?\n/);
      if (lines.at(-1) === "") lines.pop();
      yield* lines;
    }
    [Symbol.asyncDispose ?? Symbol.for("nodejs.asyncDispose")]() {
      return this.close();
    }
  }
  const promises = { constants };
  for (const [syncName, run] of Object.entries(sync)) {
    const base = syncName.slice(0, -4);
    if (base === "exists" || base.startsWith("f") && base !== "fstat" || base === "opendir" || base === "readv" || base === "writev" || base === "read" || base === "write" || base === "close" || base === "open") continue;
    promises[base] = promised(run);
  }
  delete promises.fstat;
  promises.open = async (path, flags, mode) => new FileHandle(sync.openSync(path, flags, mode));
  promises.opendir = async (path, options) => sync.opendirSync(path, options);
  promises.readFile = async (path, options) => (path instanceof FileHandle ? path.readFile(options) : sync.readFileSync(path, options));
  promises.writeFile = async (path, data, options) => {
    if (path instanceof FileHandle) return path.writeFile(data, options);
    if (data !== null && typeof data === "object" && typeof data[Symbol.asyncIterator] === "function" && !ArrayBuffer.isView(data)) {
      const chunks = [];
      for await (const chunk of data) chunks.push(Buffer().from(bytesOf(chunk, optionsOf(options, { encoding: "utf8" }).encoding)));
      data = Buffer().concat(chunks);
    }
    return sync.writeFileSync(path, data, options);
  };
  promises.appendFile = async (path, data, options) => (path instanceof FileHandle ? path.appendFile(data, options) : sync.appendFileSync(path, data, options));
  promises.glob = async function* (pattern, options) {
    yield* sync.globSync(pattern, options);
  };
  promises.watch = () => {
    throw unavailable("fs.promises.watch()", "plugins cannot watch files");
  };

  const watch = () => {
    throw unavailable("fs.watch()", "plugins cannot watch files");
  };

  // moejs's node:fs (package node) does the operations it implements on
  // the paths of the data directory, in its own pool of goroutines; those
  // of the install directory, and what it does not implement, stay here.
  // The host gives it to apiVersion 2 plugins on systems with POSIX paths.
  const official = host.official("fs");
  const officialPromises = host.official("fs/promises");
  const exported = { ...callbacks, ...sync };
  const exportedPromises = promises;
  if (official !== undefined && officialPromises !== undefined) {
    const pathModule = require("path");
    const dataDir = host.data;
    const inData = (value) => {
      if (typeof value !== "string" && !(value instanceof URL) && !ArrayBuffer.isView(value)) return false;
      let path;
      try {
        path = toPath(value);
      } catch {
        return false;
      }
      const resolved = pathModule.resolve(dataDir, path);
      return resolved === dataDir || resolved.startsWith(dataDir + "/");
    };
    const onePath = (args) => inData(args[0]);
    const twoPaths = (args) => inData(args[0]) && inData(args[1]);
    // It has no bigint stats.
    const statPath = (args) => inData(args[0]) && !args[1]?.bigint;
    const globPaths = (args) => {
      const options = args[1] !== null && typeof args[1] === "object" ? args[1] : {};
      if (!inData(options.cwd ?? dataDir)) return false;
      const patterns = Array.isArray(args[0]) ? args[0] : [args[0]];
      return patterns.every((pattern) => typeof pattern === "string" && (!pathModule.isAbsolute(pattern) || inData(pattern)));
    };
    const route = (ours, theirs, when) => {
      if (typeof ours !== "function" || typeof theirs !== "function") return ours;
      const routed = {
        [ours.name](...args) {
          return when(args) ? theirs.apply(this, args) : ours.apply(this, args);
        },
      }[ours.name];
      Object.defineProperty(routed, "length", { value: ours.length });
      return routed;
    };
    const routes = {
      access: onePath, appendFile: onePath, exists: onePath, lstat: statPath, mkdir: onePath,
      readFile: onePath, readdir: onePath, readlink: onePath, realpath: onePath, rm: onePath,
      rmdir: onePath, stat: statPath, unlink: onePath, writeFile: onePath,
      copyFile: twoPaths, cp: twoPaths, rename: twoPaths, glob: globPaths,
    };
    // A write of a known size counts against the quota first, as the
    // host's own do; the host refuses the rest once the directory is full.
    const sizeOf = (data, options) => {
      if (typeof data === "string") {
        const encoding = typeof options === "string" ? options : options?.encoding;
        return Buffer().byteLength(data, encoding ?? "utf8");
      }
      return ArrayBuffer.isView(data) ? data.byteLength : 0;
    };
    const counted = (write, form) =>
      typeof write !== "function"
        ? write
        : function (...args) {
            const options = typeof args[2] === "function" ? undefined : args[2];
            try {
              native.reserve(sizeOf(args[1], options), toPath(args[0]));
            } catch (error) {
              if (form === "promise") return Promise.reject(error);
              if (form === "callback") {
                const callback = args.at(-1);
                if (typeof callback !== "function") throw invalidArgType("cb", "of type function", callback);
                later(callback, error);
                return undefined;
              }
              throw error;
            }
            return write.apply(this, args);
          };
    for (const [name, when] of Object.entries(routes)) {
      const sized = name === "writeFile" || name === "appendFile";
      const theirs = (write, form) => (sized ? counted(write, form) : write);
      exported[name] = route(callbacks[name], theirs(official[name], "callback"), when);
      exported[`${name}Sync`] = route(sync[`${name}Sync`], theirs(official[`${name}Sync`], "sync"), when);
      if (name !== "exists") exportedPromises[name] = route(promises[name], theirs(officialPromises[name], "promise"), when);
    }
    exported.realpath.native = exported.realpath;
    exported.realpathSync.native = exported.realpathSync;
    for (const [ours, theirs] of [[Stats, official.Stats], [Dirent, official.Dirent]]) {
      if (typeof theirs !== "function") continue;
      Object.defineProperty(ours, Symbol.hasInstance, {
        value: (value) => Function.prototype[Symbol.hasInstance].call(ours, value) || value instanceof theirs,
      });
    }
  }

  module.exports = {
    ...exported,
    constants,
    F_OK: constants.F_OK,
    R_OK: constants.R_OK,
    W_OK: constants.W_OK,
    X_OK: constants.X_OK,
    Stats,
    Dirent,
    Dir,
    ReadStream,
    WriteStream,
    FileHandle,
    promises: exportedPromises,
    createReadStream: (path, options) => new (streamClasses().ReadStream)(path, options),
    createWriteStream: (path, options) => new (streamClasses().WriteStream)(path, options),
    watch,
    watchFile: () => {
      throw unavailable("fs.watchFile()", "plugins cannot watch files");
    },
    unwatchFile() {},
    openAsBlob: async () => {
      throw notImplemented("fs.openAsBlob()");
    },
  };
})
