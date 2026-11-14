// node:module for plugins: createRequire, whose require reaches the
// modules of Node only, as the global require does; the build bundles the
// plugin's own files and packages.
(function (module, exports, require) {
  "use strict";
  const loader = require("internal/loader");
  const { notImplemented } = require("internal/errors");
  function Module(id = "", parent) {
    this.id = id;
    this.path = loader.dirname;
    this.exports = {};
    this.filename = null;
    this.loaded = false;
    this.children = [];
    this.paths = [];
  }
  const isBuiltin = (name) => loader.builtin(String(name)) !== undefined;
  Object.assign(Module, {
    Module,
    builtinModules: loader.builtinModules,
    isBuiltin,
    createRequire: () => loader.require,
    register() {
      throw notImplemented("module.register()");
    },
    syncBuiltinESMExports() {},
    findSourceMap: () => undefined,
    enableCompileCache: () => ({ status: 3, message: "Disabled" }),
    getCompileCacheDir: () => undefined,
    flushCompileCache() {},
    constants: { compileCacheStatus: { FAILED: 0, ENABLED: 1, ALREADY_ENABLED: 2, DISABLED: 3 } },
    wrap: (script) => `(function (exports, require, module, __filename, __dirname) { ${script}\n});`,
    _cache: Object.create(null),
    _extensions: Object.create(null),
    _pathCache: Object.create(null),
    globalPaths: [],
  });
  module.exports = Module;
})
