// What AstrLink adds to every plugin runtime over the web APIs of moejs
// (URL, Headers, Request, Response, the streams, AbortController, Event,
// performance) and what moejs has itself (atob, btoa, queueMicrotask,
// TextEncoder, TextDecoder, structuredClone): console, crypto,
// astrlink.textStream, and fetch on the host's terms. The timers are the
// loop's. The script's value is a function the host calls with its
// primitives, host; nothing else reaches them, and the global object keeps
// no trace of them.
"use strict";
(function (host) {
  const define = (name, value) =>
    Object.defineProperty(globalThis, name, {
      value,
      writable: true,
      configurable: true,
      enumerable: false,
    });
  const { defineProperty, getPrototypeOf } = Object;
  const encoder = new TextEncoder();

  const toBytes = (data, what) => {
    if (data instanceof ArrayBuffer) return new Uint8Array(data.slice(0));
    if (ArrayBuffer.isView(data))
      return new Uint8Array(
        data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength),
      );
    if (typeof data === "string") return encoder.encode(data);
    throw new TypeError(
      `${what} must be a string, an ArrayBuffer or a typed array`,
    );
  };

  // The promises of the web APIs settle in tasks of their own; relay hands
  // what they settle with to a promise that settles in a task of the
  // host's, so that what awaits it runs as the call's script time.
  // rejected, when given, sees a rejection's reason first.
  const PromiseClass = Promise;
  const then = Promise.prototype.then;
  const relay = (promise, rejected) =>
    new PromiseClass((resolve, reject) => {
      then.call(
        promise,
        (value) => host.later(resolve, value),
        (reason) => {
          if (rejected) rejected(reason);
          host.later(reject, reason);
        },
      );
    });
  const relayMethods = (prototype, names) => {
    if (!prototype) return;
    for (const name of names) {
      const original = prototype[name];
      if (typeof original !== "function") continue;
      const relayed = {
        [name](...args) {
          return relay(original.apply(this, args));
        },
      }[name];
      defineProperty(relayed, "length", { value: original.length });
      defineProperty(prototype, name, {
        value: relayed,
        writable: true,
        enumerable: true,
        configurable: true,
      });
    }
  };

  // fetch is the web API's, on the host's terms: init.credential names a
  // slot of meta.credentials the host writes into the request, and
  // init.timeoutMs bounds it. A request the host refused or that failed on
  // its terms has the reason as its error's cause.code: denied, timeout,
  // network, too_large or budget.
  const webFetch = globalThis.fetch;
  const RequestClass = globalThis.Request;
  const fetch = function fetch(input, init = undefined) {
    let request, id;
    try {
      id = host.fetch(init?.credential, init?.timeoutMs);
      request = new RequestClass(input, init);
      const headers = request.headers;
      headers.set("x-astrlink-fetch", id);
      if (init?.credential !== undefined)
        headers.set("x-astrlink-credential", String(init.credential));
      if (init?.timeoutMs !== undefined && Number(init.timeoutMs) > 0)
        headers.set("x-astrlink-timeout", String(Math.ceil(Number(init.timeoutMs))));
    } catch (error) {
      return PromiseClass.reject(error);
    }
    return relay(webFetch(request), (error) => {
      const reason = host.failure(id);
      if (reason === undefined || error === null || typeof error !== "object") return;
      if (error.cause === null || typeof error.cause !== "object") error.cause = {};
      error.cause.code = reason;
      error.message = `fetch failed: ${reason}`;
    });
  };
  relayMethods(globalThis.Response?.prototype, ["arrayBuffer", "bytes", "json", "text"]);
  relayMethods(globalThis.Request?.prototype, ["arrayBuffer", "bytes", "json", "text"]);
  relayMethods(globalThis.ReadableStream?.prototype, ["cancel", "pipeTo"]);
  relayMethods(globalThis.ReadableStreamDefaultReader?.prototype, ["cancel", "read"]);
  relayMethods(globalThis.WritableStream?.prototype, ["abort", "close"]);
  relayMethods(globalThis.WritableStreamDefaultWriter?.prototype, ["abort", "close", "write"]);
  if (typeof globalThis.ReadableStream === "function") {
    const iterator = new ReadableStream()[Symbol.asyncIterator]();
    relayMethods(getPrototypeOf(iterator), ["next", "return"]);
  }

  const formatValue = (value) => {
    switch (typeof value) {
      case "string":
        return value;
      case "undefined":
        return "undefined";
      case "function":
        return `[Function ${value.name || "anonymous"}]`;
      case "symbol":
        return value.toString();
      case "bigint":
        return `${value}n`;
      case "object":
        if (value === null) return "null";
        if (value instanceof Error)
          return value.stack || `${value.name}: ${value.message}`;
        try {
          const text = JSON.stringify(value, (_, item) =>
            typeof item === "bigint" ? `${item}n` : item,
          );
          return text === undefined ? String(value) : text;
        } catch {
          return String(value);
        }
      default:
        return String(value);
    }
  };
  const writer =
    (level) =>
    (...args) =>
      host.log(level, args.map(formatValue).join(" "));
  const console = {
    log: writer("info"),
    info: writer("info"),
    debug: writer("debug"),
    trace: writer("debug"),
    warn: writer("warn"),
    error: writer("warn"),
  };

  const hashName = (algorithm) => {
    const name = typeof algorithm === "string" ? algorithm : algorithm?.name;
    return String(name).toUpperCase();
  };
  const keys = new WeakMap();
  class CryptoKey {
    constructor(algorithm, extractable, usages) {
      this.type = "secret";
      this.algorithm = algorithm;
      this.extractable = extractable;
      this.usages = usages;
    }
    get [Symbol.toStringTag]() {
      return "CryptoKey";
    }
  }
  const hmacKey = (key) => {
    const bytes = keys.get(key);
    if (!bytes)
      throw new TypeError(
        "the key is not an HMAC key of crypto.subtle.importKey",
      );
    return bytes;
  };
  const subtle = {
    async digest(algorithm, data) {
      return Uint8Array.fromBase64(
        host.digest(hashName(algorithm), toBytes(data, "the data").toBase64()),
      ).buffer;
    },
    async importKey(format, keyData, algorithm, extractable, usages) {
      if (format !== "raw")
        throw new TypeError("crypto.subtle.importKey supports the raw format");
      if (hashName(algorithm) !== "HMAC")
        throw new TypeError("crypto.subtle.importKey supports HMAC keys");
      const hash = hashName(algorithm.hash);
      const key = new CryptoKey(
        { name: "HMAC", hash: { name: hash } },
        Boolean(extractable),
        [...usages],
      );
      keys.set(key, toBytes(keyData, "the key"));
      return key;
    },
    async sign(algorithm, key, data) {
      if (hashName(algorithm) !== "HMAC")
        throw new TypeError("crypto.subtle.sign supports HMAC");
      const signature = host.hmac(
        key.algorithm.hash.name,
        hmacKey(key).toBase64(),
        toBytes(data, "the data").toBase64(),
      );
      return Uint8Array.fromBase64(signature).buffer;
    },
    async verify(algorithm, key, signature, data) {
      const expected = new Uint8Array(await subtle.sign(algorithm, key, data));
      const given = toBytes(signature, "the signature");
      if (expected.length !== given.length) return false;
      let difference = 0;
      for (let index = 0; index < expected.length; index++)
        difference |= expected[index] ^ given[index];
      return difference === 0;
    },
  };
  const crypto = {
    getRandomValues(array) {
      if (
        !ArrayBuffer.isView(array) ||
        array instanceof Float32Array ||
        array instanceof Float64Array ||
        array instanceof DataView
      ) {
        throw new TypeError(
          "crypto.getRandomValues takes an integer typed array",
        );
      }
      if (array.byteLength > 65536)
        throw new RangeError(
          "crypto.getRandomValues fills at most 65536 bytes",
        );
      new Uint8Array(array.buffer, array.byteOffset, array.byteLength).set(
        Uint8Array.fromBase64(host.randomBytes(array.byteLength)),
      );
      return array;
    },
    randomUUID() {
      return host.randomUUID();
    },
    subtle,
  };

  // ---- astrlink.textStream: the reply's text across protocols ----

  // escapePattern turns a literal into a pattern that matches it.
  const escapePattern = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

  // replacer builds the streaming replacement of options: the pattern of
  // every replacement, how to replace a match, and how much text a match may
  // still need from what has not come yet.
  const replacer = (options) => {
    const rules = (options.replace ?? []).map(([from, to]) => {
      if (typeof from === "string") {
        if (from === "")
          throw new TypeError(
            "textStream: a replacement needs text to replace",
          );
        return {
          source: escapePattern(from),
          flags: "",
          to,
          longest: from.length,
        };
      }
      if (from instanceof RegExp) {
        return {
          source: from.source,
          flags: from.flags,
          to,
          longest: options.maxMatch ?? 64,
        };
      }
      throw new TypeError(
        "textStream: replace takes [string or RegExp, replacement] pairs",
      );
    });
    if (rules.length === 0) return null;
    const flags = new Set(["g"]);
    for (const rule of rules)
      for (const flag of rule.flags)
        if ("imsuv".includes(flag)) flags.add(flag);
    const pattern = new RegExp(
      rules.map((rule) => `(${rule.source})`).join("|"),
      [...flags].join(""),
    );
    // Each rule's groups start after those of the rules before it.
    let group = 1;
    for (const rule of rules) {
      rule.group = group;
      group += 1 + new RegExp(`${rule.source}|`).exec("").length - 1;
    }
    const hold = Math.max(0, ...rules.map((rule) => rule.longest - 1));
    const replace = (match) => {
      const rule = rules.find(
        (candidate) => match[candidate.group] !== undefined,
      );
      if (!rule) return match[0];
      return typeof rule.to === "function"
        ? String(rule.to(match[rule.group]))
        : String(rule.to);
    };
    return { pattern, hold, replace };
  };

  // flush replaces what text can be replaced for sure and returns it with
  // the rest, which a match may still run into; final takes everything.
  const flush = (rules, text, final) => {
    if (!rules) return [text, ""];
    const limit = final ? text.length : text.length - rules.hold;
    if (limit <= 0) return ["", text];
    let out = "";
    let position = 0;
    rules.pattern.lastIndex = 0;
    let match;
    while ((match = rules.pattern.exec(text)) !== null) {
      if (match[0] === "") {
        rules.pattern.lastIndex++;
        continue;
      }
      const end = match.index + match[0].length;
      if (match.index >= limit || (!final && end === text.length)) break;
      out += text.slice(position, match.index) + rules.replace(match);
      position = end;
    }
    const cut = Math.max(limit, position);
    if (position < cut) out += text.slice(position, cut);
    return [out, text.slice(cut)];
  };

  // The text of each protocol's stream events: where a piece of the reply
  // is, and the events that end one.
  const streams = {
    "openai.chat": (event) => {
      const choice = event.data?.choices?.[0];
      if (!choice || typeof choice !== "object") return null;
      const content = choice.delta?.content;
      return {
        key: "c" + (choice.index ?? 0),
        text: typeof content === "string" ? content : "",
        final: choice.finish_reason != null,
        write: (text) => {
          const data = structuredClone(event.data);
          data.choices[0].delta = { ...data.choices[0].delta, content: text };
          return [{ ...event, data }];
        },
      };
    },
    "anthropic.messages": (event) => {
      const data = event.data;
      if (
        data?.type === "content_block_delta" &&
        data.delta?.type === "text_delta"
      ) {
        return {
          key: "b" + data.index,
          text: data.delta.text ?? "",
          write: (text) => [
            { ...event, data: { ...data, delta: { ...data.delta, text } } },
          ],
        };
      }
      if (data?.type === "content_block_stop") {
        return {
          key: "b" + data.index,
          text: "",
          final: true,
          write: (text) => [
            ...(text
              ? [
                  {
                    event: "content_block_delta",
                    data: {
                      type: "content_block_delta",
                      index: data.index,
                      delta: { type: "text_delta", text },
                    },
                  },
                ]
              : []),
            event,
          ],
        };
      }
      return null;
    },
    "openai.responses": (event, state) => {
      const data = event.data;
      const type = data?.type ?? event.event;
      const keyOf = (item, content) => `r${item}:${content}`;
      if (type === "response.output_text.delta") {
        return {
          key: keyOf(data.item_id, data.content_index),
          text: data.delta ?? "",
          write: (text) => [{ ...event, data: { ...data, delta: text } }],
        };
      }
      if (type === "response.output_text.done") {
        const key = keyOf(data.item_id, data.content_index);
        return {
          key,
          text: "",
          final: true,
          write: (text, full) => [
            ...(text
              ? [
                  {
                    event: "response.output_text.delta",
                    data: {
                      type: "response.output_text.delta",
                      item_id: data.item_id,
                      output_index: data.output_index,
                      content_index: data.content_index,
                      delta: text,
                    },
                  },
                ]
              : []),
            { ...event, data: { ...data, text: full } },
          ],
        };
      }
      // The events that repeat a whole text get what was sent instead.
      const whole = (part, item, content) => {
        const full = state.full?.[keyOf(item, content)];
        return full !== undefined && part?.type === "output_text"
          ? { ...part, text: full }
          : part;
      };
      if (type === "response.content_part.done") {
        return {
          passthrough: [
            {
              ...event,
              data: {
                ...data,
                part: whole(data.part, data.item_id, data.content_index),
              },
            },
          ],
        };
      }
      if (
        type === "response.output_item.done" &&
        Array.isArray(data.item?.content)
      ) {
        const content = data.item.content.map((part, index) =>
          whole(part, data.item.id, index),
        );
        return {
          passthrough: [
            { ...event, data: { ...data, item: { ...data.item, content } } },
          ],
        };
      }
      if (
        type === "response.completed" &&
        Array.isArray(data.response?.output)
      ) {
        const output = data.response.output.map((item) =>
          Array.isArray(item.content)
            ? {
                ...item,
                content: item.content.map((part, index) =>
                  whole(part, item.id, index),
                ),
              }
            : item,
        );
        return {
          passthrough: [
            {
              ...event,
              data: { ...data, response: { ...data.response, output } },
            },
          ],
        };
      }
      return null;
    },
    "google.generate_content": (event) => {
      const candidate = event.data?.candidates?.[0];
      const parts = candidate?.content?.parts;
      if (!Array.isArray(parts)) return null;
      const texts = parts.filter(
        (part) => typeof part.text === "string" && !part.thought,
      );
      return {
        key: "g" + (candidate.index ?? 0),
        text: texts.map((part) => part.text).join(""),
        final: candidate.finishReason != null,
        write: (text) => {
          const data = structuredClone(event.data);
          const kept = data.candidates[0].content.parts.filter(
            (part) => typeof part.text !== "string" || part.thought,
          );
          data.candidates[0].content.parts = [{ text }, ...kept];
          return [{ ...event, data }];
        },
      };
    },
  };

  // textStream makes the stream hooks of a plugin that only changes the
  // reply's visible text: map changes each piece as it comes; replace
  // replaces strings or patterns, also where a piece ends inside one, by
  // holding back as many characters as the longest one may need
  // (maxMatch for a pattern, 64 by default) until the text ends.
  const textStream = (options = {}) => {
    const rules = replacer(options);
    const map = typeof options.map === "function" ? options.map : null;
    const change = (text) => (map && text ? String(map(text)) : text);
    const onStreamEvent = (ctx, event, state) => {
      state = state ?? { pending: {}, full: {} };
      const read = streams[ctx?.protocol]?.(event, state);
      if (!read) return { events: [event], state };
      if (read.passthrough) return { events: read.passthrough, state };
      const previous = state.pending[read.key] ?? "";
      const [out, rest] = flush(
        rules,
        previous + read.text,
        Boolean(read.final),
      );
      const sent = change(out);
      state.pending[read.key] = rest;
      state.full[read.key] = (state.full[read.key] ?? "") + sent;
      if (
        !read.final &&
        out === read.text &&
        rest === "" &&
        previous === "" &&
        sent === read.text
      ) {
        return { events: [event], state };
      }
      return { events: read.write(sent, state.full[read.key]), state };
    };
    const onStreamEnd = (ctx, state) => {
      if (!state) return { events: [] };
      const events = [];
      for (const [key, pending] of Object.entries(state.pending)) {
        if (!pending) continue;
        const [out] = flush(rules, pending, true);
        const text = change(out);
        if (!text) continue;
        // What a stream that ended without its closing event still held.
        if (ctx?.protocol === "openai.chat")
          events.push({
            data: {
              choices: [
                { index: Number(key.slice(1)), delta: { content: text } },
              ],
            },
          });
        else if (ctx?.protocol === "anthropic.messages")
          events.push({
            event: "content_block_delta",
            data: {
              type: "content_block_delta",
              index: Number(key.slice(1)),
              delta: { type: "text_delta", text },
            },
          });
        else if (ctx?.protocol === "google.generate_content")
          events.push({
            data: {
              candidates: [{ content: { role: "model", parts: [{ text }] } }],
            },
          });
      }
      return { events };
    };
    return { onStreamEvent, onStreamEnd };
  };

  if (globalThis.astrlink && typeof globalThis.astrlink === "object") {
    Object.defineProperty(globalThis.astrlink, "textStream", {
      value: textStream,
      enumerable: true,
    });
  }

  define("fetch", fetch);
  define("console", console);
  define("CryptoKey", CryptoKey);
  define("crypto", crypto);
});
