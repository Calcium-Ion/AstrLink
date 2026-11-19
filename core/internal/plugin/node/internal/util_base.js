// node:util for plugins: dsh-ptc-moejs's port of Node's util (MIT), with
// util.parseArgs and util.styleText added in node/util_extra.js.
(function (module, exports, require, host) {
"use strict";

const timers = require('internal/timers');
const { defineProperty, getOwnPropertyDescriptor, getOwnPropertyDescriptors, getOwnPropertySymbols, getPrototypeOf, keys } = Object;
const objectToString = Object.prototype.toString;
const typedArrayPrototype = getPrototypeOf(Uint8Array.prototype);
const typedArrayTag = getOwnPropertyDescriptor(typedArrayPrototype, Symbol.toStringTag).get;
const AsyncFunction = (async () => {}).constructor;
const GeneratorFunction = (function* () {}).constructor;
const AsyncGeneratorFunction = (async function* () {}).constructor;

function invalidArgType(name, expected, value) {
  const shown = value === null || value === undefined ? `${value}`
    : typeof value === 'function' ? `function ${value.name}`
      : typeof value === 'object' ? `an instance of ${value.constructor?.name ?? 'Object'}`
        : `type ${typeof value} (${inspect(value)})`;
  const error = new TypeError(`The "${name}" argument must be of type ${expected}. Received ${shown}`);
  error.code = 'ERR_INVALID_ARG_TYPE';
  return error;
}

function brand(fn) {
  return (value) => {
    try {
      fn(value);
      return true;
    } catch {
      return false;
    }
  };
}

function tag(value) {
  return objectToString.call(value).slice(8, -1);
}

function typedArrayOf(name) {
  return (value) => types.isTypedArray(value) && typedArrayTag.call(value) === name;
}

const regExpSource = getOwnPropertyDescriptor(RegExp.prototype, 'source').get;
const arrayBufferByteLength = getOwnPropertyDescriptor(ArrayBuffer.prototype, 'byteLength').get;
const dataViewByteLength = getOwnPropertyDescriptor(DataView.prototype, 'byteLength').get;

const types = {
  isDate: brand(value => Date.prototype.getTime.call(value)),
  isRegExp: value => value !== RegExp.prototype && brand(value => regExpSource.call(value))(value),
  isMap: brand(value => Map.prototype.has.call(value, undefined)),
  isSet: brand(value => Set.prototype.has.call(value, undefined)),
  isWeakMap: brand(value => WeakMap.prototype.has.call(value, {})),
  isWeakSet: brand(value => WeakSet.prototype.has.call(value, {})),
  isPromise: value => value instanceof Promise,
  isTypedArray: value => typeof value === 'object' && value !== null && typedArrayTag.call(value) !== undefined,
  isUint8Array: typedArrayOf('Uint8Array'),
  isUint8ClampedArray: typedArrayOf('Uint8ClampedArray'),
  isUint16Array: typedArrayOf('Uint16Array'),
  isUint32Array: typedArrayOf('Uint32Array'),
  isInt8Array: typedArrayOf('Int8Array'),
  isInt16Array: typedArrayOf('Int16Array'),
  isInt32Array: typedArrayOf('Int32Array'),
  isFloat32Array: typedArrayOf('Float32Array'),
  isFloat64Array: typedArrayOf('Float64Array'),
  isBigInt64Array: typedArrayOf('BigInt64Array'),
  isBigUint64Array: typedArrayOf('BigUint64Array'),
  isArrayBuffer: brand(value => arrayBufferByteLength.call(value)),
  isAnyArrayBuffer: brand(value => arrayBufferByteLength.call(value)),
  isArrayBufferView: value => ArrayBuffer.isView(value),
  isDataView: brand(value => dataViewByteLength.call(value)),
  isAsyncFunction: value => typeof value === 'function' && (value instanceof AsyncFunction || value instanceof AsyncGeneratorFunction),
  isGeneratorFunction: value => typeof value === 'function' && (value instanceof GeneratorFunction || value instanceof AsyncGeneratorFunction),
  isGeneratorObject: value => typeof value === 'object' && value !== null && tag(value) === 'Generator',
  isNativeError: value => value instanceof Error,
  isNumberObject: value => typeof value === 'object' && brand(value => Number.prototype.valueOf.call(value))(value),
  isStringObject: value => typeof value === 'object' && brand(value => String.prototype.valueOf.call(value))(value),
  isBooleanObject: value => typeof value === 'object' && brand(value => Boolean.prototype.valueOf.call(value))(value),
  isBigIntObject: value => typeof value === 'object' && brand(value => BigInt.prototype.valueOf.call(value))(value),
  isSymbolObject: value => typeof value === 'object' && brand(value => Symbol.prototype.valueOf.call(value))(value),
  isBoxedPrimitive: value => types.isNumberObject(value) || types.isStringObject(value) || types.isBooleanObject(value)
    || types.isBigIntObject(value) || types.isSymbolObject(value),
  isArgumentsObject: value => typeof value === 'object' && value !== null && tag(value) === 'Arguments',
  isMapIterator: value => typeof value === 'object' && value !== null && tag(value) === 'Map Iterator',
  isSetIterator: value => typeof value === 'object' && value !== null && tag(value) === 'Set Iterator',
  isModuleNamespaceObject: value => typeof value === 'object' && value !== null && getPrototypeOf(value) === null && tag(value) === 'Module',
};

// util.inspect, and util.format's check for %s: ports of Node's.
const { inspect, hasBuiltInToString } = require('internal/inspect');

function formatNumber(number, options) {
  return inspect(number, options);
}

function formatBigInt(bigint, options) {
  return inspect(bigint, options);
}

let circularErrorMessage;
function tryStringify(arg) {
  try {
    return JSON.stringify(arg);
  } catch (err) {
    if (circularErrorMessage === undefined) {
      try {
        const a = {};
        a.a = a;
        JSON.stringify(a);
      } catch (circularError) {
        circularErrorMessage = circularError.message.split('\n', 1)[0];
      }
    }
    if (err.name === 'TypeError' && err.message.split('\n', 1)[0] === circularErrorMessage) return '[Circular]';
    throw err;
  }
}

// Node's formatWithOptionsInternal.
function formatWithOptionsInternal(inspectOptions, args) {
  const first = args[0];
  let a = 0;
  let str = '';
  let join = '';
  if (typeof first === 'string') {
    if (args.length === 1) return first;
    let tempStr;
    let lastPos = 0;
    for (let i = 0; i < first.length - 1; i++) {
      if (first.charCodeAt(i) === 37) { // '%'
        const nextChar = first.charCodeAt(++i);
        if (a + 1 !== args.length) {
          switch (nextChar) {
            case 115: { // 's'
              const tempArg = args[++a];
              if (typeof tempArg === 'number') {
                tempStr = formatNumber(tempArg, inspectOptions);
              } else if (typeof tempArg === 'bigint') {
                tempStr = formatBigInt(tempArg, inspectOptions);
              } else if (typeof tempArg !== 'object' || tempArg === null || !hasBuiltInToString(tempArg)) {
                tempStr = String(tempArg);
              } else {
                tempStr = inspect(tempArg, { ...inspectOptions, compact: 3, colors: false, depth: 0 });
              }
              break;
            }
            case 106: // 'j'
              tempStr = tryStringify(args[++a]);
              break;
            case 100: { // 'd'
              const tempNum = args[++a];
              if (typeof tempNum === 'bigint') tempStr = formatBigInt(tempNum, inspectOptions);
              else if (typeof tempNum === 'symbol') tempStr = 'NaN';
              else tempStr = formatNumber(Number(tempNum), inspectOptions);
              break;
            }
            case 79: // 'O'
              tempStr = inspect(args[++a], inspectOptions);
              break;
            case 111: // 'o'
              tempStr = inspect(args[++a], { ...inspectOptions, showHidden: true, showProxy: true, depth: 4 });
              break;
            case 105: { // 'i'
              const tempInteger = args[++a];
              if (typeof tempInteger === 'bigint') tempStr = formatBigInt(tempInteger, inspectOptions);
              else if (typeof tempInteger === 'symbol') tempStr = 'NaN';
              else tempStr = formatNumber(Number.parseInt(tempInteger), inspectOptions);
              break;
            }
            case 102: { // 'f'
              const tempFloat = args[++a];
              if (typeof tempFloat === 'symbol') tempStr = 'NaN';
              else tempStr = formatNumber(Number.parseFloat(tempFloat), inspectOptions);
              break;
            }
            case 99: // 'c'
              a += 1;
              tempStr = '';
              break;
            case 37: // '%'
              str += first.slice(lastPos, i);
              lastPos = i + 1;
              continue;
            default: // Not a placeholder.
              continue;
          }
          if (lastPos !== i - 1) str += first.slice(lastPos, i - 1);
          str += tempStr;
          lastPos = i + 1;
        } else if (nextChar === 37) {
          str += first.slice(lastPos, i);
          lastPos = i + 1;
        }
      }
    }
    if (lastPos !== 0) {
      a++;
      join = ' ';
      if (lastPos < first.length) str += first.slice(lastPos);
    }
  }
  while (a < args.length) {
    const value = args[a];
    str += join;
    str += typeof value !== 'string' ? inspect(value, inspectOptions) : value;
    join = ' ';
    a++;
  }
  return str;
}

/** Node's util.format: %s, %d, %i, %f, %j, %o, %O, %c and %%. */
function format(...args) {
  return formatWithOptionsInternal(undefined, args);
}

function formatWithOptions(inspectOptions, ...args) {
  if (inspectOptions === null || typeof inspectOptions !== 'object') throw invalidArgType('inspectOptions', 'object', inspectOptions);
  return formatWithOptionsInternal(inspectOptions, args);
}

function ownKeys(value) {
  return [...keys(value), ...getOwnPropertySymbols(value).filter(symbol => getOwnPropertyDescriptor(value, symbol).enumerable)];
}

function bytesEqual(a, b) {
  if (a.byteLength !== b.byteLength) return false;
  for (let i = 0; i < a.byteLength; i++) if (a[i] !== b[i]) return false;
  return true;
}

function deepEqual(a, b, memo) {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (getPrototypeOf(a) !== getPrototypeOf(b) || tag(a) !== tag(b)) return false;
  let seen = memo.get(a);
  if (seen?.has(b)) return true;
  if (seen === undefined) memo.set(a, seen = new Set());
  seen.add(b);
  if (Array.isArray(a)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if ((i in a) !== (i in b) || !deepEqual(a[i], b[i], memo)) return false;
    }
  } else if (types.isDate(a)) {
    if (!types.isDate(b) || a.getTime() !== b.getTime()) return false;
  } else if (types.isRegExp(a)) {
    if (!types.isRegExp(b) || a.source !== b.source || a.flags !== b.flags || a.lastIndex !== b.lastIndex) return false;
  } else if (types.isBoxedPrimitive(a)) {
    if (!types.isBoxedPrimitive(b) || !Object.is(a.valueOf(), b.valueOf())) return false;
  } else if (a instanceof Error) {
    if (a.message !== b.message || a.name !== b.name) return false;
  } else if (types.isTypedArray(a)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!Object.is(a[i], b[i])) return false;
  } else if (types.isArrayBuffer(a)) {
    if (!bytesEqual(new Uint8Array(a), new Uint8Array(b))) return false;
  } else if (types.isDataView(a)) {
    if (!bytesEqual(new Uint8Array(a.buffer, a.byteOffset, a.byteLength), new Uint8Array(b.buffer, b.byteOffset, b.byteLength))) return false;
  } else if (types.isMap(a)) {
    if (a.size !== b.size) return false;
    const rest = [];
    for (const [key, value] of a) {
      if (typeof key !== 'object' || key === null) {
        if (!b.has(key) || !deepEqual(value, b.get(key), memo)) return false;
      } else {
        rest.push([key, value]);
      }
    }
    const candidates = [...b].filter(([key]) => typeof key === 'object' && key !== null);
    for (const [key, value] of rest) {
      const index = candidates.findIndex(([other, otherValue]) => deepEqual(key, other, memo) && deepEqual(value, otherValue, memo));
      if (index === -1) return false;
      candidates.splice(index, 1);
    }
  } else if (types.isSet(a)) {
    if (a.size !== b.size) return false;
    const rest = [];
    for (const value of a) {
      if (typeof value !== 'object' || value === null) {
        if (!b.has(value)) return false;
      } else {
        rest.push(value);
      }
    }
    const candidates = [...b].filter(value => typeof value === 'object' && value !== null);
    for (const value of rest) {
      const index = candidates.findIndex(other => deepEqual(value, other, memo));
      if (index === -1) return false;
      candidates.splice(index, 1);
    }
  } else if (types.isWeakMap(a) || types.isWeakSet(a) || types.isPromise(a)) {
    return false;
  }
  const keysA = ownKeys(a);
  const keysB = ownKeys(b);
  if (keysA.length !== keysB.length) return false;
  for (const key of keysA) {
    if (!Object.prototype.propertyIsEnumerable.call(b, key) || !deepEqual(a[key], b[key], memo)) return false;
  }
  return true;
}

/** Node's util.isDeepStrictEqual, for the values programs build. */
function isDeepStrictEqual(a, b) {
  return deepEqual(a, b, new Map());
}

const kCustomPromisify = Symbol.for('nodejs.util.promisify.custom');

function promisifiedTimer(original) {
  if (original === timers.setTimeout) {
    return (delay, value) => new Promise(resolve => { timers.setTimeout(resolve, delay, value); });
  }
  if (original === timers.setImmediate) {
    return value => new Promise(resolve => { timers.setImmediate(resolve, value); });
  }
  return undefined;
}

function promisify(original) {
  if (typeof original !== 'function') throw invalidArgType('original', 'function', original);
  const custom = original[kCustomPromisify] ?? promisifiedTimer(original);
  if (custom !== undefined) {
    if (typeof custom !== 'function') throw invalidArgType('util.promisify.custom', 'function', custom);
    return defineProperty(custom, kCustomPromisify, { value: custom, enumerable: false, writable: false, configurable: true });
  }
  function fn(...args) {
    return new Promise((resolve, reject) => {
      Reflect.apply(original, this, [...args, (error, value) => { if (error) reject(error); else resolve(value); }]);
    });
  }
  Object.setPrototypeOf(fn, getPrototypeOf(original));
  defineProperty(fn, kCustomPromisify, { value: fn, enumerable: false, writable: false, configurable: true });
  const descriptors = getOwnPropertyDescriptors(original);
  for (const key of Reflect.ownKeys(descriptors)) {
    try {
      defineProperty(fn, key, descriptors[key]);
    } catch {}
  }
  return fn;
}
promisify.custom = kCustomPromisify;

function callbackify(original) {
  if (typeof original !== 'function') throw invalidArgType('original', 'function', original);
  function callbackified(...args) {
    const callback = args.pop();
    if (typeof callback !== 'function') throw invalidArgType('last argument', 'function', callback);
    const done = (error, value) => { queueMicrotask(() => { callback(error, value); }); };
    Reflect.apply(original, this, args).then(value => { done(null, value); }, (reason) => {
      if (!reason) {
        const error = new Error('Promise was rejected with a falsy value');
        error.code = 'ERR_FALSY_VALUE_REJECTION';
        error.reason = reason;
        reason = error;
      }
      done(reason);
    });
  }
  const descriptors = getOwnPropertyDescriptors(original);
  if (typeof descriptors.length.value === 'number') descriptors.length.value++;
  if (typeof descriptors.name.value === 'string') descriptors.name.value += 'Callbackified';
  Object.defineProperties(callbackified, { length: descriptors.length, name: descriptors.name });
  return callbackified;
}

function inherits(ctor, superCtor) {
  if (typeof ctor !== 'function') throw invalidArgType('ctor', 'function', ctor);
  if (typeof superCtor !== 'function') throw invalidArgType('superCtor', 'function', superCtor);
  if (superCtor.prototype === undefined) throw invalidArgType('superCtor.prototype', 'object', superCtor.prototype);
  defineProperty(ctor, 'super_', { value: superCtor, writable: true, configurable: true });
  Object.setPrototypeOf(ctor.prototype, superCtor.prototype);
}

function deprecate(fn, message, code) {
  if (typeof fn !== 'function') throw invalidArgType('fn', 'function', fn);
  let warned = false;
  function deprecated(...args) {
    if (!warned) {
      warned = true;
      globalThis.console?.error(`${code === undefined ? '' : `[${code}] `}DeprecationWarning: ${message}`);
    }
    return new.target === undefined ? Reflect.apply(fn, this, args) : Reflect.construct(fn, args, new.target);
  }
  Object.setPrototypeOf(deprecated, fn);
  if (fn.prototype) deprecated.prototype = fn.prototype;
  return deprecated;
}

function debuglog() {
  const logger = () => {};
  logger.enabled = false;
  return logger;
}
const debug = debuglog;

// The pattern of Node's stripVTControlCharacters.
const ansi = /[\u001B\u009B][[\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\d/#&.:=?%@~_]+)*|[a-zA-Z\d]+(?:;[-a-zA-Z\d/#&.:=?%@~_]*)*)?\u0007)|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-ntqry=><~]))/g;

function stripVTControlCharacters(text) {
  if (typeof text !== 'string') throw invalidArgType('str', 'string', text);
  return text.replace(ansi, '');
}

function toUSVString(text) {
  return String(text).toWellFormed();
}

function isArray(value) {
  return Array.isArray(value);
}

const TextEncoder = globalThis.TextEncoder;
const TextDecoder = globalThis.TextDecoder;

module.exports = {
  callbackify, debug, debuglog, deprecate, format, formatWithOptions, inherits, inspect, isArray,
  isDeepStrictEqual, promisify, stripVTControlCharacters, TextDecoder, TextEncoder, toUSVString, types,
};
})
