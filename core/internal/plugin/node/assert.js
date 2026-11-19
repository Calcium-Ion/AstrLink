// node:assert for plugins: the assertions of Node, with deepStrictEqual on
// util.isDeepStrictEqual.
(function (module, exports, require) {
  "use strict";
  const util = require("util");
  const { invalidArgType } = require("internal/errors");

  class AssertionError extends Error {
    constructor(options = {}) {
      const { message, actual, expected, operator } = options;
      const generated = message === undefined;
      super(
        generated
          ? `${util.inspect(actual)} ${operator ?? "=="} ${util.inspect(expected)}`
          : String(message),
      );
      this.name = "AssertionError";
      this.code = "ERR_ASSERTION";
      this.actual = actual;
      this.expected = expected;
      this.operator = operator;
      this.generatedMessage = generated;
    }
  }
  const fail = (actual, expected, message, operator, defaultMessage) => {
    if (message instanceof Error) throw message;
    throw new AssertionError({
      message: message ?? defaultMessage,
      actual,
      expected,
      operator,
    });
  };

  // looseEqual is deepEqual's comparison: == for primitives, structure for
  // objects, prototypes ignored.
  const looseEqual = (a, b, seen = new Map()) => {
    if (a == b && (typeof a !== "object" || a === null || typeof b !== "object" || b === null)) return true;
    if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return Number.isNaN(a) && Number.isNaN(b);
    if (seen.get(a) === b) return true;
    seen.set(a, b);
    if (a instanceof Date && b instanceof Date) return a.getTime() === b.getTime();
    if (a instanceof RegExp && b instanceof RegExp) return String(a) === String(b);
    if (Array.isArray(a) !== Array.isArray(b)) return false;
    if (a instanceof Map && b instanceof Map) {
      if (a.size !== b.size) return false;
      for (const [key, value] of a) if (!b.has(key) || !looseEqual(value, b.get(key), seen)) return false;
      return true;
    }
    if (a instanceof Set && b instanceof Set) {
      if (a.size !== b.size) return false;
      for (const value of a) if (!b.has(value)) return false;
      return true;
    }
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    if (keysA.length !== keysB.length) return false;
    return keysA.every((key) => Object.prototype.hasOwnProperty.call(b, key) && looseEqual(a[key], b[key], seen));
  };

  // partialEqual is whether actual holds at least what expected does.
  const partialEqual = (actual, expected) => {
    if (typeof expected !== "object" || expected === null) return Object.is(actual, expected);
    if (typeof actual !== "object" || actual === null) return false;
    if (Array.isArray(expected)) {
      if (!Array.isArray(actual)) return false;
      let from = 0;
      return expected.every((item) => {
        const index = actual.findIndex((candidate, at) => at >= from && partialEqual(candidate, item));
        if (index === -1) return false;
        from = index + 1;
        return true;
      });
    }
    return Object.keys(expected).every((key) => key in actual && partialEqual(actual[key], expected[key]));
  };

  // matches checks a thrown error against what throws or rejects expects:
  // a class, a RegExp, a validation function, or an object of properties.
  const matches = (error, expected, message, operator) => {
    if (expected === undefined) return;
    if (typeof expected === "function") {
      if (expected.prototype !== undefined && error instanceof expected) return;
      if (Error.isPrototypeOf(expected) || expected === Error) {
        throw error;
      }
      if (expected.call({}, error) === true) return;
      fail(error, expected, message, operator, "The validation function is expected to return \"true\". Received false");
    }
    if (expected instanceof RegExp) {
      if (expected.test(String(error))) return;
      fail(error, expected, message, operator, `The input did not match the regular expression ${expected}. Input:\n\n${util.inspect(String(error))}\n`);
    }
    if (typeof expected === "object" && expected !== null) {
      for (const key of Object.keys(expected)) {
        const want = expected[key];
        const got = error?.[key];
        const ok = want instanceof RegExp && typeof got === "string" ? want.test(got) : util.isDeepStrictEqual(got, want);
        if (!ok) fail(error, expected, message, operator, `Expected values to be strictly deep-equal at "${key}"`);
      }
    }
  };
  const splitArgs = (expected, message) => (typeof expected === "string" ? [undefined, expected] : [expected, message]);

  function ok(value, message) {
    if (!value) fail(value, true, message, "==", "The expression evaluated to a falsy value");
  }
  const assert = ok;
  Object.assign(assert, {
    ok,
    AssertionError,
    fail(message = "Failed") {
      if (message instanceof Error) throw message;
      throw new AssertionError({ message, operator: "fail" });
    },
    equal(actual, expected, message) {
      if (!(actual == expected || (Number.isNaN(actual) && Number.isNaN(expected)))) fail(actual, expected, message, "==");
    },
    notEqual(actual, expected, message) {
      if (actual == expected || (Number.isNaN(actual) && Number.isNaN(expected))) fail(actual, expected, message, "!=");
    },
    strictEqual(actual, expected, message) {
      if (!Object.is(actual, expected)) fail(actual, expected, message, "strictEqual", `Expected values to be strictly equal:\n\n${util.inspect(actual)} !== ${util.inspect(expected)}\n`);
    },
    notStrictEqual(actual, expected, message) {
      if (Object.is(actual, expected)) fail(actual, expected, message, "notStrictEqual", `Expected "actual" to be strictly unequal to: ${util.inspect(expected)}`);
    },
    deepEqual(actual, expected, message) {
      if (!looseEqual(actual, expected)) fail(actual, expected, message, "deepEqual", "Expected values to be loosely deep-equal");
    },
    notDeepEqual(actual, expected, message) {
      if (looseEqual(actual, expected)) fail(actual, expected, message, "notDeepEqual", "Expected \"actual\" not to be loosely deep-equal to \"expected\"");
    },
    deepStrictEqual(actual, expected, message) {
      if (!util.isDeepStrictEqual(actual, expected))
        fail(actual, expected, message, "deepStrictEqual", `Expected values to be strictly deep-equal:\n${util.inspect(actual)}\n\nshould equal\n\n${util.inspect(expected)}`);
    },
    notDeepStrictEqual(actual, expected, message) {
      if (util.isDeepStrictEqual(actual, expected)) fail(actual, expected, message, "notDeepStrictEqual", "Expected \"actual\" not to be strictly deep-equal to \"expected\"");
    },
    partialDeepStrictEqual(actual, expected, message) {
      if (!partialEqual(actual, expected)) fail(actual, expected, message, "partialDeepStrictEqual", "Expected values to be partially and strictly deep-equal");
    },
    throws(fn, expected, message) {
      if (typeof fn !== "function") throw invalidArgType("fn", "of type function", fn);
      [expected, message] = splitArgs(expected, message);
      try {
        fn();
      } catch (error) {
        matches(error, expected, message, "throws");
        return;
      }
      fail(undefined, expected, message, "throws", "Missing expected exception.");
    },
    doesNotThrow(fn, expected, message) {
      [expected, message] = splitArgs(expected, message);
      try {
        fn();
      } catch (error) {
        fail(error, expected, message, "doesNotThrow", `Got unwanted exception.\nActual message: "${error?.message}"`);
      }
    },
    async rejects(promiseOrFn, expected, message) {
      [expected, message] = splitArgs(expected, message);
      try {
        await (typeof promiseOrFn === "function" ? promiseOrFn() : promiseOrFn);
      } catch (error) {
        matches(error, expected, message, "rejects");
        return;
      }
      fail(undefined, expected, message, "rejects", "Missing expected rejection.");
    },
    async doesNotReject(promiseOrFn, expected, message) {
      [expected, message] = splitArgs(expected, message);
      try {
        await (typeof promiseOrFn === "function" ? promiseOrFn() : promiseOrFn);
      } catch (error) {
        fail(error, expected, message, "doesNotReject", `Got unwanted rejection.\nActual message: "${error?.message}"`);
      }
    },
    match(string, regexp, message) {
      if (typeof string !== "string" || !regexp.test(string))
        fail(string, regexp, message, "match", `The input did not match the regular expression ${regexp}. Input:\n\n${util.inspect(string)}\n`);
    },
    doesNotMatch(string, regexp, message) {
      if (typeof string === "string" && regexp.test(string))
        fail(string, regexp, message, "doesNotMatch", `The input was expected to not match the regular expression ${regexp}. Input:\n\n${util.inspect(string)}\n`);
    },
    ifError(value) {
      if (value !== null && value !== undefined) {
        if (value instanceof Error) throw value;
        fail(value, null, undefined, "ifError", `ifError got unwanted exception: ${util.inspect(value)}`);
      }
    },
    CallTracker: class CallTracker {
      calls(fn = () => {}) {
        return fn;
      }
      report() {
        return [];
      }
      verify() {}
    },
  });

  // strict is assert with the strict comparisons under the loose names.
  function strict(value, message) {
    return ok(value, message);
  }
  Object.assign(strict, assert, {
    equal: assert.strictEqual,
    notEqual: assert.notStrictEqual,
    deepEqual: assert.deepStrictEqual,
    notDeepEqual: assert.notDeepStrictEqual,
  });
  strict.strict = strict;
  assert.strict = strict;
  module.exports = assert;
})
