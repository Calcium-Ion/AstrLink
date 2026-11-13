// node:readline for plugins: Interface reads the lines of a stream, as
// events, with question, and as an async iterator. There is no terminal:
// the cursor functions write nothing.
(function (module, exports, require) {
  "use strict";
  const EventEmitter = require("events");
  class Interface extends EventEmitter {
    #input;
    #output;
    #buffer = "";
    #closed = false;
    #paused = false;
    #questions = [];
    #prompt = "> ";
    #decoder = new TextDecoder();
    #onData = (chunk) => this.#take(typeof chunk === "string" ? chunk : this.#decoder.decode(chunk, { stream: true }));
    #onEnd = () => {
      if (this.#buffer !== "") this.#line(this.#buffer);
      this.#buffer = "";
      this.close();
    };
    constructor(input, output, completer, terminal) {
      super();
      if (input !== null && typeof input === "object" && typeof input.on !== "function" && input.input !== undefined) {
        ({ input, output } = input);
      }
      this.#input = input;
      this.#output = output;
      this.terminal = Boolean(terminal);
      this.line = "";
      this.cursor = 0;
      input?.on("data", this.#onData);
      input?.on("end", this.#onEnd);
      input?.on("close", this.#onEnd);
    }
    #take(text) {
      this.#buffer += text;
      for (;;) {
        const index = this.#buffer.search(/\r?\n|\r(?!$)/);
        if (index === -1) break;
        const match = /\r?\n|\r/.exec(this.#buffer.slice(index));
        const line = this.#buffer.slice(0, index);
        this.#buffer = this.#buffer.slice(index + match[0].length);
        this.#line(line);
      }
    }
    #line(line) {
      const question = this.#questions.shift();
      if (question) question(line);
      else this.emit("line", line);
    }
    get closed() {
      return this.#closed;
    }
    setPrompt(prompt) {
      this.#prompt = prompt;
    }
    getPrompt() {
      return this.#prompt;
    }
    prompt() {
      this.#output?.write?.(this.#prompt);
    }
    question(query, options, callback) {
      if (typeof options === "function") callback = options;
      this.#output?.write?.(query);
      this.#questions.push((answer) => callback?.(answer));
    }
    write(data) {
      if (typeof data === "string") this.#take(data);
    }
    pause() {
      this.#paused = true;
      this.#input?.pause?.();
      this.emit("pause");
      return this;
    }
    resume() {
      this.#paused = false;
      this.#input?.resume?.();
      this.emit("resume");
      return this;
    }
    close() {
      if (this.#closed) return;
      this.#closed = true;
      this.#input?.removeListener?.("data", this.#onData);
      this.#input?.removeListener?.("end", this.#onEnd);
      this.#input?.removeListener?.("close", this.#onEnd);
      this.emit("close");
    }
    getCursorPos() {
      return { rows: 0, cols: 0 };
    }
    [Symbol.asyncIterator]() {
      return lines(this);
    }
    [Symbol.dispose ?? Symbol.for("nodejs.dispose")]() {
      this.close();
    }
  }
  async function* lines(face) {
    const queue = [];
    let wake;
    let done = face.closed;
    face.on("line", (line) => {
      queue.push(line);
      wake?.();
    });
    face.on("close", () => {
      done = true;
      wake?.();
    });
    for (;;) {
      while (queue.length > 0) yield queue.shift();
      if (done) return;
      await new Promise((resolve) => (wake = resolve));
      wake = undefined;
    }
  }
  const nothing = (stream, ...args) => {
    const callback = args.find((arg) => typeof arg === "function");
    if (callback) queueMicrotask(callback);
    return true;
  };
  module.exports = {
    Interface,
    createInterface: (input, output, completer, terminal) => new Interface(input, output, completer, terminal),
    clearLine: nothing,
    clearScreenDown: nothing,
    cursorTo: nothing,
    moveCursor: nothing,
    emitKeypressEvents() {},
    get promises() {
      return require("readline/promises");
    },
  };
})
