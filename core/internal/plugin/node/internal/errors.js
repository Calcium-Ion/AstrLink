// The errors of Node that the node: modules throw: their codes and the
// wording of their messages.
(function (module, exports, require) {
  "use strict";

  const shown = (value) => {
    try {
      return require("internal/inspect").inspect(value, { depth: -1 });
    } catch {
      return String(value);
    }
  };

  // received describes value as Node's ERR_INVALID_ARG_TYPE does.
  const received = (value) => {
    if (value == null) return ` Received ${value}`;
    if (typeof value === "function")
      return ` Received function ${value.name || "<anonymous>"}`;
    if (typeof value === "object") {
      const name = value.constructor?.name;
      return name
        ? ` Received an instance of ${name}`
        : ` Received ${shown(value)}`;
    }
    let text = shown(value);
    if (text.length > 28) text = `${text.slice(0, 25)}...`;
    return ` Received type ${typeof value} (${text})`;
  };

  const coded = (Base, code, message) => {
    const error = new Base(message);
    Object.defineProperty(error, "code", {
      value: code,
      enumerable: true,
      writable: true,
      configurable: true,
    });
    return error;
  };

  exports.coded = coded;
  exports.received = received;
  exports.invalidArgType = (name, expected, value) =>
    coded(
      TypeError,
      "ERR_INVALID_ARG_TYPE",
      `The "${name}" ${name.includes(".") ? "property" : "argument"} must be ${expected}.${received(value)}`,
    );
  exports.invalidArgValue = (name, value, reason = "is invalid") =>
    coded(
      TypeError,
      "ERR_INVALID_ARG_VALUE",
      `The ${name.includes(".") ? "property" : "argument"} '${name}' ${reason}. Received ${shown(value)}`,
    );
  exports.outOfRange = (name, range, value) =>
    coded(
      RangeError,
      "ERR_OUT_OF_RANGE",
      `The value of "${name}" is out of range. It must be ${range}. Received ${typeof value === "number" ? String(value) : shown(value)}`,
    );
  exports.missingArgs = (...names) =>
    coded(
      TypeError,
      "ERR_MISSING_ARGS",
      `The ${names.map((name) => `"${name}"`).join(" and ")} argument${names.length > 1 ? "s" : ""} must be specified`,
    );
  exports.notImplemented = (what) =>
    coded(
      Error,
      "ERR_METHOD_NOT_IMPLEMENTED",
      `The ${what} method is not implemented`,
    );
  exports.unavailable = (what, reason) =>
    coded(Error, "ERR_ACCESS_DENIED", `${what} is not available: ${reason}`);
  exports.abortError = (cause) => {
    const error = new Error("The operation was aborted");
    error.name = "AbortError";
    error.code = "ABORT_ERR";
    if (cause !== undefined) error.cause = cause;
    return error;
  };
  exports.validateFunction = (value, name) => {
    if (typeof value !== "function")
      throw exports.invalidArgType(name, "of type function", value);
  };
  exports.validateString = (value, name) => {
    if (typeof value !== "string")
      throw exports.invalidArgType(name, "of type string", value);
  };
  exports.validateObject = (value, name) => {
    if (value === null || typeof value !== "object" || Array.isArray(value))
      throw exports.invalidArgType(name, "of type object", value);
  };
  exports.validateAbortSignal = (signal, name) => {
    if (
      signal !== undefined &&
      (signal === null || typeof signal !== "object" || !("aborted" in signal))
    )
      throw exports.invalidArgType(name, "an instance of AbortSignal", signal);
  };
})
