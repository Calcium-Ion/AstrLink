// node:sqlite for plugins: DatabaseSync and StatementSync of Node 22, on the
// SQLite the gateway carries. A database file is in the plugin's own
// directories, as node:fs paths are; one in the install directory opens
// read-only. A database stays open across calls for as long as the runtime
// that opened it.
(function (module, exports, require, host) {
  "use strict";
  const native = host.bind("sqlite");
  const { invalidArgType, coded, notImplemented, unavailable } = require("internal/errors");
  const kConstruct = Symbol("construct");

  const pathOf = (path) => {
    if (typeof path === "string") return path;
    if (path instanceof URL) return require("url").fileURLToPath(path);
    if (ArrayBuffer.isView(path)) return new TextDecoder().decode(path);
    throw invalidArgType("path", "a string, Uint8Array, or URL without null bytes", path);
  };
  const notOpen = () => coded(Error, "ERR_INVALID_STATE", "database is not open");

  class DatabaseSync {
    #id = 0;
    #path;
    #options;
    constructor(path, options = {}) {
      if (new.target === undefined) throw coded(TypeError, "ERR_CONSTRUCT_CALL_REQUIRED", "Cannot call constructor without `new`");
      this.#path = pathOf(path);
      if (options === null || typeof options !== "object") throw invalidArgType("options", "an object", options);
      this.#options = options;
      if (options.open !== false) this.open();
    }
    open() {
      if (this.#id !== 0) throw coded(Error, "ERR_INVALID_STATE", "database is already open");
      const options = this.#options;
      this.#id = native.open(this.#path, Boolean(options.readOnly), options.enableForeignKeyConstraints !== false, options.timeout);
    }
    close() {
      if (this.#id === 0) throw notOpen();
      native.close(this.#id);
      this.#id = 0;
    }
    get isOpen() {
      return this.#id !== 0;
    }
    get isTransaction() {
      if (this.#id === 0) throw notOpen();
      return false;
    }
    exec(sql) {
      if (this.#id === 0) throw notOpen();
      if (typeof sql !== "string") throw invalidArgType("sql", "a string", sql);
      native.exec(this.#id, sql);
    }
    prepare(sql) {
      if (this.#id === 0) throw notOpen();
      if (typeof sql !== "string") throw invalidArgType("sql", "a string", sql);
      return new StatementSync(kConstruct, this.#id, native.prepare(this.#id, sql), sql);
    }
    location(name = "main") {
      if (this.#id === 0) throw notOpen();
      return name === "main" ? native.location(this.#id) : null;
    }
    function() {
      throw notImplemented("database.function()");
    }
    aggregate() {
      throw notImplemented("database.aggregate()");
    }
    createSession() {
      throw notImplemented("database.createSession()");
    }
    applyChangeset() {
      throw notImplemented("database.applyChangeset()");
    }
    enableLoadExtension() {
      throw unavailable("database.enableLoadExtension()", "plugins cannot load native code");
    }
    loadExtension() {
      throw unavailable("database.loadExtension()", "plugins cannot load native code");
    }
    [Symbol.dispose ?? Symbol.for("nodejs.dispose")]() {
      if (this.#id !== 0) this.close();
    }
  }

  class StatementSync {
    #db;
    #id;
    #sql;
    #bigints = false;
    #arrays = false;
    constructor(token, db, id, sql) {
      if (token !== kConstruct) throw coded(TypeError, "ERR_ILLEGAL_CONSTRUCTOR", "Illegal constructor");
      this.#db = db;
      this.#id = id;
      this.#sql = sql;
    }
    // parameters splits the arguments into named ones, an object first,
    // and positional ones.
    #parameters(args) {
      const first = args[0];
      if (first !== null && typeof first === "object" && !ArrayBuffer.isView(first)) return [args.slice(1), first];
      return [args, null];
    }
    #query(args, limit) {
      const [positional, named] = this.#parameters(args);
      const [rows, blobs] = native.query(this.#db, this.#id, positional, named, limit, this.#arrays, this.#bigints);
      if (blobs) {
        for (const row of rows) {
          for (const key of Object.keys(row)) {
            if (row[key] instanceof ArrayBuffer) row[key] = new Uint8Array(row[key]);
          }
        }
      }
      return rows;
    }
    run(...args) {
      const [positional, named] = this.#parameters(args);
      const result = native.run(this.#db, this.#id, positional, named);
      if (this.#bigints) return { changes: BigInt(result.changes), lastInsertRowid: BigInt(result.lastInsertRowid) };
      return result;
    }
    get(...args) {
      return this.#query(args, 1)[0];
    }
    all(...args) {
      return this.#query(args, 0);
    }
    iterate(...args) {
      return this.#query(args, 0)[Symbol.iterator]();
    }
    columns() {
      return native.columns(this.#db, this.#id).map((name) => ({ column: null, database: null, name, table: null, type: null }));
    }
    setReadBigInts(enabled) {
      this.#bigints = Boolean(enabled);
    }
    setReturnArrays(enabled) {
      this.#arrays = Boolean(enabled);
    }
    setAllowBareNamedParameters() {}
    setAllowUnknownNamedParameters() {}
    get sourceSQL() {
      return this.#sql;
    }
    get expandedSQL() {
      return this.#sql;
    }
  }

  module.exports = {
    DatabaseSync,
    StatementSync,
    constants: Object.freeze({ SQLITE_CHANGESET_OMIT: 0, SQLITE_CHANGESET_REPLACE: 1, SQLITE_CHANGESET_ABORT: 2 }),
  };
})
