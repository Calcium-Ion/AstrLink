// node:util for plugins: dsh-ptc-moejs's port (internal/util_base.js), with
// parseArgs, styleText, the error names of the system, and the old type
// checks that Node 22 still has.
(function (module, exports, require) {
  "use strict";
  const util = require("internal/util_base");
  const { invalidArgType, coded, validateAbortSignal } = require("internal/errors");

  const tag = (value) => Object.prototype.toString.call(value).slice(8, -1);
  util.types = {
    ...util.types,
    isProxy: () => false,
    isExternal: () => false,
    isSharedArrayBuffer: (value) => tag(value) === "SharedArrayBuffer",
    isKeyObject: (value) =>
      value !== null &&
      typeof value === "object" &&
      tag(value) === "KeyObject",
    isCryptoKey: (value) => value instanceof globalThis.CryptoKey,
  };
  util.types.isAnyArrayBuffer = (value) =>
    util.types.isArrayBuffer(value) || util.types.isSharedArrayBuffer(value);

  // The old checks, deprecated but still in Node 22.
  Object.assign(util, {
    isBoolean: (value) => typeof value === "boolean",
    isBuffer: (value) => globalThis.Buffer?.isBuffer(value) ?? false,
    isDate: (value) => util.types.isDate(value),
    isError: (value) => util.types.isNativeError(value) || value instanceof Error,
    isFunction: (value) => typeof value === "function",
    isNull: (value) => value === null,
    isNullOrUndefined: (value) => value == null,
    isNumber: (value) => typeof value === "number",
    isObject: (value) => value !== null && typeof value === "object",
    isPrimitive: (value) =>
      value === null || (typeof value !== "object" && typeof value !== "function"),
    isRegExp: (value) => util.types.isRegExp(value),
    isString: (value) => typeof value === "string",
    isSymbol: (value) => typeof value === "symbol",
    isUndefined: (value) => value === undefined,
    _extend(target, source) {
      if (source === null || typeof source !== "object") return target;
      for (const key of Object.keys(source)) target[key] = source[key];
      return target;
    },
  });

  const errnoNames = {
    1: ["EPERM", "operation not permitted"],
    2: ["ENOENT", "no such file or directory"],
    5: ["EIO", "i/o error"],
    9: ["EBADF", "bad file descriptor"],
    11: ["EAGAIN", "resource temporarily unavailable"],
    13: ["EACCES", "permission denied"],
    16: ["EBUSY", "resource busy or locked"],
    17: ["EEXIST", "file already exists"],
    18: ["EXDEV", "cross-device link not permitted"],
    20: ["ENOTDIR", "not a directory"],
    21: ["EISDIR", "illegal operation on a directory"],
    22: ["EINVAL", "invalid argument"],
    24: ["EMFILE", "too many open files"],
    28: ["ENOSPC", "no space left on device"],
    30: ["EROFS", "read-only file system"],
    36: ["ENAMETOOLONG", "name too long"],
    38: ["ENOSYS", "function not implemented"],
    39: ["ENOTEMPTY", "directory not empty"],
    40: ["ELOOP", "too many symbolic links encountered"],
    104: ["ECONNRESET", "connection reset by peer"],
    110: ["ETIMEDOUT", "connection timed out"],
    111: ["ECONNREFUSED", "connection refused"],
  };
  util.getSystemErrorName = (errno) => {
    if (typeof errno !== "number") throw invalidArgType("err", "of type number", errno);
    return errnoNames[-errno]?.[0] ?? `Unknown system error ${errno}`;
  };
  util.getSystemErrorMap = () =>
    new Map(Object.entries(errnoNames).map(([errno, entry]) => [-Number(errno), entry]));
  util.getSystemErrorMessage = (errno) => errnoNames[-errno]?.[1] ?? `Unknown system error ${errno}`;

  util.aborted = (signal, resource) => {
    validateAbortSignal(signal, "signal");
    if (resource === null || (typeof resource !== "object" && typeof resource !== "function"))
      throw invalidArgType("resource", "of type object", resource);
    if (signal.aborted) return Promise.resolve();
    return new Promise((resolve) =>
      signal.addEventListener("abort", () => resolve(), { once: true }),
    );
  };

  // styleText formats text with the ANSI codes of util.inspect.colors.
  const styles = {
    reset: [0, 0], bold: [1, 22], dim: [2, 22], italic: [3, 23], underline: [4, 24],
    blink: [5, 25], inverse: [7, 27], hidden: [8, 28], strikethrough: [9, 29],
    doubleunderline: [21, 24], framed: [51, 54], overlined: [53, 55],
    black: [30, 39], red: [31, 39], green: [32, 39], yellow: [33, 39], blue: [34, 39],
    magenta: [35, 39], cyan: [36, 39], white: [37, 39], gray: [90, 39], grey: [90, 39],
    blackBright: [90, 39], redBright: [91, 39], greenBright: [92, 39],
    yellowBright: [93, 39], blueBright: [94, 39], magentaBright: [95, 39],
    cyanBright: [96, 39], whiteBright: [97, 39],
    bgBlack: [40, 49], bgRed: [41, 49], bgGreen: [42, 49], bgYellow: [43, 49],
    bgBlue: [44, 49], bgMagenta: [45, 49], bgCyan: [46, 49], bgWhite: [47, 49],
    bgGray: [100, 49], bgGrey: [100, 49], bgBlackBright: [100, 49], bgRedBright: [101, 49],
    bgGreenBright: [102, 49], bgYellowBright: [103, 49], bgBlueBright: [104, 49],
    bgMagentaBright: [105, 49], bgCyanBright: [106, 49], bgWhiteBright: [107, 49],
  };
  util.styleText = (format, text, options) => {
    if (typeof text !== "string") throw invalidArgType("text", "of type string", text);
    // The log a plugin writes to has no colors, as a stream that is not a
    // terminal; validateStream: false asks for the codes anyway.
    const colors = options?.validateStream === false;
    let open = "";
    let close = "";
    for (const name of Array.isArray(format) ? format : [format]) {
      const style = styles[name];
      if (style === undefined)
        throw coded(
          TypeError,
          "ERR_INVALID_ARG_VALUE",
          `The argument 'format' must be one of: ${Object.keys(styles).join(", ")}. Received ${JSON.stringify(name)}`,
        );
      open += `\u001b[${style[0]}m`;
      close = `\u001b[${style[1]}m` + close;
    }
    return colors ? open + text + close : text;
  };

  // parseArgs of Node: options of type string or boolean, short and long,
  // with = or the next argument, --, multiple and default; positionals when
  // allowed; tokens when asked.
  util.parseArgs = (config = {}) => {
    const args = config.args ?? require("process").argv.slice(2);
    const options = config.options ?? {};
    const strict = config.strict ?? true;
    const allowPositionals = config.allowPositionals ?? !strict;
    const allowNegative = config.allowNegative ?? false;
    const values = Object.create(null);
    const positionals = [];
    const tokens = [];
    const shorts = new Map();
    for (const [name, option] of Object.entries(options)) {
      if (option.short !== undefined) shorts.set(option.short, name);
    }
    const fail = (code, message) => coded(TypeError, code, message);
    const store = (name, value, index, raw, inline) => {
      const option = options[name];
      if (option === undefined) {
        if (strict)
          throw fail("ERR_PARSE_ARGS_UNKNOWN_OPTION", `Unknown option '${raw}'`);
      } else if (option.type === "string" && typeof value !== "string") {
        if (strict)
          throw fail(
            "ERR_PARSE_ARGS_INVALID_OPTION_VALUE",
            `Option '${raw} <value>' argument missing`,
          );
      } else if (option.type === "boolean" && typeof value === "string" && strict) {
        throw fail(
          "ERR_PARSE_ARGS_INVALID_OPTION_VALUE",
          `Option '${raw}' does not take an argument`,
        );
      }
      tokens.push({ kind: "option", name, rawName: raw, index, value, inlineValue: inline });
      if (option?.multiple) (values[name] ??= []).push(value);
      else values[name] = value;
    };
    for (let index = 0; index < args.length; index++) {
      const arg = args[index];
      if (arg === "--") {
        tokens.push({ kind: "option-terminator", index });
        for (const rest of args.slice(index + 1)) {
          positionals.push(rest);
          tokens.push({ kind: "positional", index: ++index, value: rest });
        }
        break;
      }
      if (arg.startsWith("--") && arg.length > 2) {
        let [name, value] = arg.slice(2).split(/=(.*)/s);
        const inline = value !== undefined;
        if (allowNegative && name.startsWith("no-") && options[name.slice(3)]?.type === "boolean") {
          store(name.slice(3), false, index, arg, false);
          continue;
        }
        if (!inline && options[name]?.type === "string" && index + 1 < args.length)
          value = args[++index];
        store(name, inline || options[name]?.type === "string" ? value : true, index, `--${name}`, inline);
        continue;
      }
      if (arg.startsWith("-") && arg.length > 1) {
        for (let at = 1; at < arg.length; at++) {
          const short = arg[at];
          const name = shorts.get(short) ?? short;
          if (options[name]?.type === "string") {
            const rest = arg.slice(at + 1);
            const value = rest !== "" ? rest : args[++index];
            store(name, value, index, `-${short}`, rest !== "");
            break;
          }
          store(name, true, index, `-${short}`, false);
        }
        continue;
      }
      if (!allowPositionals)
        throw fail(
          "ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL",
          `Unexpected argument '${arg}'. This command does not take positional arguments`,
        );
      positionals.push(arg);
      tokens.push({ kind: "positional", index, value: arg });
    }
    for (const [name, option] of Object.entries(options)) {
      if (values[name] === undefined && option.default !== undefined)
        values[name] = option.default;
    }
    const result = { values, positionals };
    if (config.tokens) result.tokens = tokens;
    return result;
  };

  util.inspect.custom ??= Symbol.for("nodejs.util.inspect.custom");
  module.exports = util;
})
