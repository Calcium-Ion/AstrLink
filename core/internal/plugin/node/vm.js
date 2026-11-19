// node:vm for plugins, as dsh-ptc-moejs (MIT) ported it to moejs: a script
// runs in the plugin's own realm, with the properties of its context object
// as global names, since a moejs runtime has one realm. Node's vm is no
// security boundary either. The timeout option is not enforced: the call's
// time limit stops a script that does not end.
(function (module, exports, require, host) {
"use strict";

const contexts = new WeakSet();

function invalidArgType(name, expected, value) {
  const error = new TypeError(`The "${name}" argument must be ${expected}. Received ${value === null ? 'null' : typeof value}`);
  error.code = 'ERR_INVALID_ARG_TYPE';
  return error;
}

// Evaluates code with the context's properties in scope. The eval is
// direct, so the script sees the with statement's object; the parameter
// names are unlikely to be a context's.
const evaluate = new Function('$moejsVmContext', '$moejsVmCode', 'with ($moejsVmContext) { return eval($moejsVmCode); }');

function createContext(contextObject = {}, options) {
  if (contextObject === null || typeof contextObject !== 'object') throw invalidArgType('contextObject', 'of type object', contextObject);
  if (options !== undefined && (options === null || typeof options !== 'object')) throw invalidArgType('options', 'of type object', options);
  contexts.add(contextObject);
  return contextObject;
}

function isContext(object) {
  if (object === null || typeof object !== 'object') throw invalidArgType('object', 'of type object', object);
  return contexts.has(object);
}

function checkContext(context) {
  if (context === null || typeof context !== 'object' || !contexts.has(context)) {
    throw invalidArgType('contextifiedObject', 'an vm.Context', context);
  }
}

class Script {
  #code;

  constructor(code, options) {
    if (options !== undefined && options !== null && typeof options !== 'object' && typeof options !== 'string') {
      throw invalidArgType('options', 'of type object', options);
    }
    this.#code = `${code}`;
    // Compiled now, so that a syntax error throws here, as in Node.
    new Function(this.#code);
  }

  runInContext(contextifiedObject, options) {
    checkContext(contextifiedObject);
    return Reflect.apply(evaluate, contextifiedObject, [contextifiedObject, this.#code]);
  }

  runInNewContext(contextObject, options) {
    return this.runInContext(createContext(contextObject), options);
  }

  runInThisContext(options) {
    return (0, eval)(this.#code);
  }

  createCachedData() {
    return new Uint8Array(0);
  }
}

function runInContext(code, contextifiedObject, options) {
  return new Script(code, options).runInContext(contextifiedObject, options);
}

function runInNewContext(code, contextObject, options) {
  return new Script(code, options).runInNewContext(contextObject, options);
}

function runInThisContext(code, options) {
  return new Script(code, options).runInThisContext(options);
}

function compileFunction(code, params = [], options = {}) {
  return new Function(...params, `${code}`);
}

const constants = {
  USE_MAIN_CONTEXT_DEFAULT_LOADER: Symbol('vm_dynamic_import_main_context_default'),
  DONT_CONTEXTIFY: Symbol('vm_context_no_contextify'),
};

module.exports = { Script, compileFunction, constants, createContext, isContext, runInContext, runInNewContext, runInThisContext };
})
