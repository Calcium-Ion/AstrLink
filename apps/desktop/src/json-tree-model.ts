/**
 * A captured JSON body as a tree the reviewer can fold. Captures are cut at
 * the audit size limit, so the parser keeps everything before the cut instead
 * of failing the whole document, and it runs in slices so a multi-MB body
 * does not hold the main thread.
 */

import { findOffsets } from "./find-model";

export type JsonNode =
  | JsonObjectNode
  | JsonArrayNode
  | { kind: "string"; text: string; complete: boolean }
  // Numbers keep their source text so large integers and exact decimals
  // read as sent.
  | { kind: "number"; text: string }
  | { kind: "literal"; text: "true" | "false" | "null" };

export interface JsonObjectNode {
  kind: "object";
  // Entries, not a map: duplicate keys stay visible as the provider got them.
  entries: [string, JsonNode][];
  complete: boolean;
}

export interface JsonArrayNode {
  kind: "array";
  items: JsonNode[];
  complete: boolean;
}

export type JsonContainerNode = JsonObjectNode | JsonArrayNode;

export interface JsonTreeParse {
  root: JsonNode | null;
  /** Offset where the text stops being JSON; null when it parsed. */
  errorAt: number | null;
  /** The text ends inside the document, as a truncated capture does. */
  cut: boolean;
}

export class JsonTreeCancelledError extends Error {
  constructor() {
    super("JSON parsing cancelled");
    this.name = "JsonTreeCancelledError";
  }
}

type ParserState =
  | "value"
  | "firstValue"
  | "key"
  | "firstKey"
  | "colon"
  | "after"
  | "done";

interface Frame {
  node: JsonContainerNode;
  key: string;
}

const NUMBER_PATTERN = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;
const PARTIAL_NUMBER_PATTERN = /-?\d*(?:\.\d*)?(?:[eE][+-]?\d*)?/y;
const LITERALS = ["true", "false", "null"] as const;
const DEADLINE_CHECK_INTERVAL = 512;

class JsonTreeParser {
  private readonly text: string;
  private pos = 0;
  private state: ParserState = "value";
  private readonly stack: Frame[] = [];
  private root: JsonNode | null = null;
  private errorAt: number | null = null;
  private cut = false;

  constructor(text: string) {
    this.text = text;
  }

  get progress(): number {
    return this.text.length === 0 ? 1 : this.pos / this.text.length;
  }

  result(): JsonTreeParse {
    return { root: this.root, errorAt: this.errorAt, cut: this.cut };
  }

  /** Parses until the text ends or the clock passes `deadline`; true when done. */
  run(deadline: number): boolean {
    let steps = 0;
    while (this.state !== "done") {
      this.step();
      steps += 1;
      // A slice that ends on the last step reports done on the next call.
      if (steps % DEADLINE_CHECK_INTERVAL === 0 && performance.now() > deadline)
        return false;
    }
    return true;
  }

  private step(): void {
    const text = this.text;
    this.skipWhitespace();
    if (this.pos >= text.length) {
      this.finish();
      return;
    }
    const char = text.charCodeAt(this.pos);
    switch (this.state) {
      case "firstValue":
        if (char === 0x5d /* ] */) {
          this.close();
          return;
        }
        this.readValue(char);
        return;
      case "value":
        this.readValue(char);
        return;
      case "firstKey":
        if (char === 0x7d /* } */) {
          this.close();
          return;
        }
        this.readKey(char);
        return;
      case "key":
        this.readKey(char);
        return;
      case "colon":
        if (char !== 0x3a /* : */) return this.fail();
        this.pos += 1;
        this.state = "value";
        return;
      case "after":
        this.readAfter(char);
        return;
    }
  }

  private finish(): void {
    if (this.stack.length > 0) this.cut = true;
    else if (this.root === null) this.errorAt = this.pos;
    this.state = "done";
  }

  private fail(): void {
    this.errorAt = this.pos;
    this.state = "done";
  }

  private skipWhitespace(): void {
    const text = this.text;
    let pos = this.pos;
    while (pos < text.length) {
      const char = text.charCodeAt(pos);
      if (char !== 0x20 && char !== 0x0a && char !== 0x0d && char !== 0x09)
        break;
      pos += 1;
    }
    this.pos = pos;
  }

  private readAfter(char: number): void {
    const top = this.stack.at(-1);
    if (!top) return this.fail();
    if (char === 0x2c /* , */) {
      this.pos += 1;
      this.state = top.node.kind === "array" ? "value" : "key";
      return;
    }
    if (
      (top.node.kind === "array" && char === 0x5d) ||
      (top.node.kind === "object" && char === 0x7d)
    ) {
      this.close();
      return;
    }
    this.fail();
  }

  private close(): void {
    const frame = this.stack.pop();
    if (frame) frame.node.complete = true;
    this.pos += 1;
    this.afterValue();
  }

  private afterValue(): void {
    if (this.stack.length > 0) {
      this.state = "after";
      return;
    }
    this.skipWhitespace();
    if (this.pos < this.text.length) this.fail();
    else this.state = "done";
  }

  private attach(node: JsonNode): void {
    const top = this.stack.at(-1);
    if (!top) this.root = node;
    else if (top.node.kind === "array") top.node.items.push(node);
    else top.node.entries.push([top.key, node]);
  }

  private readKey(char: number): void {
    if (char !== 0x22 /* " */) return this.fail();
    const key = this.readString();
    if (key === null) return;
    if (!key.complete) {
      // A key cut before its value has nothing to show.
      this.cut = true;
      this.state = "done";
      return;
    }
    this.stack.at(-1)!.key = key.text;
    this.state = "colon";
  }

  private readValue(char: number): void {
    if (char === 0x7b /* { */ || char === 0x5b /* [ */) {
      const node: JsonContainerNode =
        char === 0x7b
          ? { kind: "object", entries: [], complete: false }
          : { kind: "array", items: [], complete: false };
      this.attach(node);
      this.stack.push({ node, key: "" });
      this.pos += 1;
      this.state = char === 0x7b ? "firstKey" : "firstValue";
      return;
    }
    if (char === 0x22 /* " */) {
      const value = this.readString();
      if (value === null) return;
      this.attach({
        kind: "string",
        text: value.text,
        complete: value.complete,
      });
      if (!value.complete) {
        this.cut = true;
        this.state = "done";
        return;
      }
      this.afterValue();
      return;
    }
    if (char === 0x2d /* - */ || (char >= 0x30 && char <= 0x39)) {
      this.readNumber();
      return;
    }
    this.readLiteral();
  }

  private readNumber(): void {
    const text = this.text;
    const start = this.pos;
    NUMBER_PATTERN.lastIndex = start;
    const match = NUMBER_PATTERN.exec(text);
    const end = match ? start + match[0].length : start;
    if (this.stack.length > 0) {
      PARTIAL_NUMBER_PATTERN.lastIndex = start;
      const partial = PARTIAL_NUMBER_PATTERN.exec(text);
      // Digits running into the end of the capture may be missing more.
      if (partial && start + partial[0].length >= text.length) {
        this.pos = text.length;
        this.cut = true;
        this.state = "done";
        return;
      }
    }
    if (!match || end === start) return this.fail();
    this.attach({ kind: "number", text: match[0] });
    this.pos = end;
    this.afterValue();
  }

  private readLiteral(): void {
    const text = this.text;
    for (const literal of LITERALS) {
      if (text.startsWith(literal, this.pos)) {
        this.attach({ kind: "literal", text: literal });
        this.pos += literal.length;
        this.afterValue();
        return;
      }
      const rest = text.slice(this.pos, this.pos + literal.length);
      if (rest.length < literal.length && literal.startsWith(rest)) {
        this.pos = text.length;
        this.cut = true;
        this.state = "done";
        return;
      }
    }
    this.fail();
  }

  /**
   * Reads the string at the cursor. A string the text ends inside comes back
   * with what it had so far; null means it is not valid JSON.
   */
  private readString(): { text: string; complete: boolean } | null {
    const text = this.text;
    const start = this.pos;
    let search = start + 1;
    for (;;) {
      const quote = text.indexOf('"', search);
      if (quote === -1) {
        this.pos = text.length;
        return { text: decodePartial(text.slice(start + 1)), complete: false };
      }
      let backslashes = 0;
      while (text.charCodeAt(quote - 1 - backslashes) === 0x5c /* \ */)
        backslashes += 1;
      if (backslashes % 2 === 1) {
        search = quote + 1;
        continue;
      }
      const raw = text.slice(start + 1, quote);
      if (raw.indexOf("\\") === -1) {
        this.pos = quote + 1;
        return { text: raw, complete: true };
      }
      try {
        const decoded = JSON.parse(text.slice(start, quote + 1)) as string;
        this.pos = quote + 1;
        return { text: decoded, complete: true };
      } catch {
        this.pos = start;
        this.fail();
        return null;
      }
    }
  }
}

/** Decodes the escapes of a string the capture cut, dropping a split escape. */
function decodePartial(raw: string): string {
  if (raw.indexOf("\\") === -1) return raw;
  let body = raw.replace(/\\u[0-9a-fA-F]{0,3}$/, (match, offset: number) =>
    trailingBackslashes(raw, offset) % 2 === 0 ? "" : match,
  );
  if (trailingBackslashes(body, body.length) % 2 === 1)
    body = body.slice(0, -1);
  try {
    return JSON.parse(`"${body}"`) as string;
  } catch {
    return body;
  }
}

function trailingBackslashes(text: string, end: number): number {
  let count = 0;
  while (text.charCodeAt(end - 1 - count) === 0x5c) count += 1;
  return count;
}

export function parseJsonTree(text: string): JsonTreeParse {
  const parser = new JsonTreeParser(text);
  parser.run(Number.POSITIVE_INFINITY);
  return parser.result();
}

/** Parses in short slices, yielding between them so the UI stays responsive. */
export async function parseJsonTreeIncremental(
  text: string,
  options: {
    signal?: AbortSignal;
    onProgress?: (progress: number) => void;
    sliceMs?: number;
  } = {},
): Promise<JsonTreeParse> {
  const parser = new JsonTreeParser(text);
  const sliceMs = options.sliceMs ?? 12;
  for (;;) {
    if (options.signal?.aborted) throw new JsonTreeCancelledError();
    const done = parser.run(performance.now() + sliceMs);
    if (done) return parser.result();
    options.onProgress?.(parser.progress);
    await new Promise((resolve) => globalThis.setTimeout(resolve, 0));
  }
}

export function isJsonContainer(node: JsonNode): node is JsonContainerNode {
  return node.kind === "object" || node.kind === "array";
}

export function childCount(node: JsonContainerNode): number {
  return node.kind === "object" ? node.entries.length : node.items.length;
}

function childAt(
  node: JsonContainerNode,
  index: number,
): { key: string | null; value: JsonNode } {
  if (node.kind === "object") {
    const [key, value] = node.entries[index]!;
    return { key, value };
  }
  return { key: null, value: node.items[index]! };
}

export const JSON_TREE_ROOT = "$";
/** Children shown per step of a long object or array. */
export const JSON_CHILD_BATCH = 100;
/** Characters of an unfolded string rendered per step. */
export const JSON_STRING_CHUNK = 64 * 1024;
/** Strings longer than this, or spanning lines, start folded. */
const INLINE_STRING_LIMIT = 120;

export function childPath(parent: string, index: number): string {
  return `${parent}/${index}`;
}

/** Long or multi-line strings fold to a one-line preview. */
export function isFoldableString(text: string): boolean {
  return text.length > INLINE_STRING_LIMIT || text.includes("\n");
}

export type JsonTreeRow =
  | {
      type: "node";
      path: string;
      depth: number;
      /** Object key, or null for array items and the root. */
      name: string | null;
      /** Position in the parent array, for array items. */
      index: number | null;
      node: JsonNode;
      open: boolean;
      /** Characters shown of an unfolded string. */
      shownChars: number;
    }
  | {
      type: "close";
      path: string;
      depth: number;
      node: JsonContainerNode;
    }
  | {
      type: "more";
      path: string;
      depth: number;
      shown: number;
      total: number;
    };

/**
 * The visible rows for the open set. It only walks open containers and the
 * children already shown, so a folded 10k-item array costs one row.
 */
export function flattenJsonTree(
  root: JsonNode,
  open: ReadonlySet<string>,
  limits: ReadonlyMap<string, number>,
): JsonTreeRow[] {
  const rows: JsonTreeRow[] = [];
  const visit = (
    node: JsonNode,
    path: string,
    depth: number,
    name: string | null,
    index: number | null,
  ) => {
    const isOpen = open.has(path);
    rows.push({
      type: "node",
      path,
      depth,
      name,
      index,
      node,
      open: isOpen,
      shownChars:
        node.kind === "string" && isOpen
          ? (limits.get(path) ?? JSON_STRING_CHUNK)
          : 0,
    });
    if (!isOpen || !isJsonContainer(node)) return;
    const total = childCount(node);
    // An empty container already reads as `[]` or `{}` on its own row.
    if (total === 0 && node.complete) return;
    const shown = Math.min(total, limits.get(path) ?? JSON_CHILD_BATCH);
    for (let child = 0; child < shown; child += 1) {
      const { key, value } = childAt(node, child);
      visit(
        value,
        childPath(path, child),
        depth + 1,
        key,
        node.kind === "array" ? child : null,
      );
    }
    if (shown < total)
      rows.push({ type: "more", path, depth: depth + 1, shown, total });
    rows.push({ type: "close", path, depth, node });
  };
  visit(root, JSON_TREE_ROOT, 0, null, null);
  return rows;
}

/** Keys whose values are bulky definitions rather than the conversation. */
export const DEFAULT_FOLDED_KEYS: ReadonlySet<string> = new Set([
  "tools",
  "functions",
]);

export interface OpenPathOptions {
  /** Deepest container opened; the root is depth 0. */
  maxDepth?: number;
  /** Approximate rows the opened containers may add. */
  budget?: number;
  /** Containers under these object keys stay folded. */
  foldedKeys?: ReadonlySet<string>;
  /** Strings to unfold along with their ancestors, such as privacy hits. */
  reveal?: (text: string) => boolean;
  /** Most strings `reveal` may unfold. */
  revealLimit?: number;
}

/**
 * Which containers start open: breadth first, so the shape of a request
 * reads before any one message, down to a depth and a row budget that keep
 * the first render cheap.
 */
export function defaultOpenPaths(
  root: JsonNode,
  options: OpenPathOptions = {},
): Set<string> {
  const maxDepth = options.maxDepth ?? 4;
  const foldedKeys = options.foldedKeys ?? DEFAULT_FOLDED_KEYS;
  let budget = options.budget ?? 1000;
  const open = new Set<string>();
  const queue: { node: JsonNode; path: string; depth: number }[] = [
    { node: root, path: JSON_TREE_ROOT, depth: 0 },
  ];
  for (let head = 0; head < queue.length; head += 1) {
    const { node, path, depth } = queue[head]!;
    if (!isJsonContainer(node)) continue;
    const shown = Math.min(childCount(node), JSON_CHILD_BATCH);
    if (depth > 0 && shown + 1 > budget) continue;
    open.add(path);
    budget -= shown + 1;
    if (depth >= maxDepth) continue;
    for (let child = 0; child < shown; child += 1) {
      const { key, value } = childAt(node, child);
      if (!isJsonContainer(value)) continue;
      if (key !== null && foldedKeys.has(key)) continue;
      queue.push({
        node: value,
        path: childPath(path, child),
        depth: depth + 1,
      });
    }
  }
  if (options.reveal) revealStrings(root, open, options.reveal, options);
  return open;
}

/** Every container open, up to a row budget; strings stay folded. */
export function expandAllPaths(root: JsonNode, budget = 5000): Set<string> {
  return defaultOpenPaths(root, {
    maxDepth: Number.POSITIVE_INFINITY,
    budget,
    foldedKeys: new Set(),
  });
}

function revealStrings(
  root: JsonNode,
  open: Set<string>,
  reveal: (text: string) => boolean,
  options: OpenPathOptions,
): void {
  let remaining = options.revealLimit ?? 50;
  const stack: { node: JsonNode; path: string; ancestors: string[] }[] = [
    { node: root, path: JSON_TREE_ROOT, ancestors: [] },
  ];
  while (stack.length > 0 && remaining > 0) {
    const { node, path, ancestors } = stack.pop()!;
    if (node.kind === "string") {
      if (!reveal(node.text)) continue;
      for (const ancestor of ancestors) open.add(ancestor);
      if (isFoldableString(node.text)) open.add(path);
      remaining -= 1;
      continue;
    }
    if (!isJsonContainer(node)) continue;
    // Only children the first batch shows can be revealed without paging.
    const shown = Math.min(childCount(node), JSON_CHILD_BATCH);
    const next = [...ancestors, path];
    for (let child = shown - 1; child >= 0; child -= 1) {
      stack.push({
        node: childAt(node, child).value,
        path: childPath(path, child),
        ancestors: next,
      });
    }
  }
}

/** One place a find query appears in the tree. */
export interface JsonSearchHit {
  /** Path of the row the hit shows in. */
  path: string;
  /** In that row's object key, or in its value. */
  field: "key" | "value";
  /** Where the hit starts in the field's text. */
  offset: number;
  /** The value is a string that starts folded, so the hit needs it open. */
  folded: boolean;
}

export interface JsonSearchResult {
  hits: JsonSearchHit[];
  /** More hits exist past the limit. */
  capped: boolean;
}

/** A key as the tree prints it, without the quotes: what find matches. */
export function jsonKeyText(key: string): string {
  return JSON.stringify(key).slice(1, -1);
}

/**
 * Every hit of `matcher` in keys and values, in document order. Folded
 * containers, pages past the first batch and folded strings are searched
 * too; `revealJsonHit` opens the way to any one of them. Inline files are
 * skipped: a match inside base64 is noise, and the tree never shows it.
 */
export function searchJsonTree(
  root: JsonNode,
  matcher: RegExp,
  limit: number,
): JsonSearchResult {
  const hits: JsonSearchHit[] = [];
  let capped = false;
  // Asks for one hit past the limit, so a full page also says there is more.
  const collect = (
    path: string,
    field: JsonSearchHit["field"],
    text: string,
    folded: boolean,
  ) => {
    for (const offset of findOffsets(text, matcher, limit - hits.length + 1)) {
      if (hits.length === limit) {
        capped = true;
        return;
      }
      hits.push({ path, field, offset, folded });
    }
  };
  const stack: { node: JsonNode; path: string; key: string | null }[] = [
    { node: root, path: JSON_TREE_ROOT, key: null },
  ];
  while (stack.length > 0 && !capped) {
    const { node, path, key } = stack.pop()!;
    if (key !== null) collect(path, "key", jsonKeyText(key), false);
    if (isJsonContainer(node)) {
      for (let child = childCount(node) - 1; child >= 0; child -= 1) {
        const { key: childKey, value } = childAt(node, child);
        stack.push({
          node: value,
          path: childPath(path, child),
          key: childKey,
        });
      }
      continue;
    }
    if (node.kind === "string") {
      if (detectBase64(node.text)) continue;
      collect(path, "value", node.text, isFoldableString(node.text));
      continue;
    }
    collect(path, "value", node.text, false);
  }
  return { hits, capped };
}

/**
 * The open set and page limits with the way to `hit` cleared: every
 * container above it open and paged far enough to list it, and the folded
 * string it sits in opened past its end. The inputs come back unchanged,
 * by identity, when the hit was already on screen.
 */
export function revealJsonHit(
  hit: JsonSearchHit,
  length: number,
  open: ReadonlySet<string>,
  limits: ReadonlyMap<string, number>,
): { open: ReadonlySet<string>; limits: ReadonlyMap<string, number> } {
  let nextOpen: Set<string> | null = null;
  let nextLimits: Map<string, number> | null = null;
  const openPath = (path: string) => {
    if ((nextOpen ?? open).has(path)) return;
    nextOpen ??= new Set(open);
    nextOpen.add(path);
  };
  const reach = (path: string, needed: number, batch: number) => {
    if (needed <= ((nextLimits ?? limits).get(path) ?? batch)) return;
    nextLimits ??= new Map(limits);
    nextLimits.set(path, Math.ceil(needed / batch) * batch);
  };
  const segments = hit.path.split("/");
  for (let depth = 1; depth < segments.length; depth += 1) {
    const parent = segments.slice(0, depth).join("/");
    openPath(parent);
    reach(parent, Number(segments[depth]) + 1, JSON_CHILD_BATCH);
  }
  if (hit.field === "value" && hit.folded) {
    openPath(hit.path);
    reach(hit.path, hit.offset + length, JSON_STRING_CHUNK);
  }
  return { open: nextOpen ?? open, limits: nextLimits ?? limits };
}

const DATA_URL_PATTERN = /^data:([^;,]*)(?:;[^;,]*)*;base64,/;
const BASE64_SAMPLE_PATTERN = /^[A-Za-z0-9+/_-]+={0,2}$/;
const BASE64_MIN_LENGTH = 256;
const BASE64_SAMPLE = 4096;

export interface Base64Info {
  /** Media type from a data URL, if the string is one. */
  mediaType: string | null;
  /** Approximate decoded size. */
  bytes: number;
}

/**
 * Recognizes inline file content: a base64 data URL, or a long run of the
 * base64 alphabet with no spaces, which prose and URLs do not produce.
 */
export function detectBase64(text: string): Base64Info | null {
  if (text.length < BASE64_MIN_LENGTH) return null;
  const dataUrl = DATA_URL_PATTERN.exec(text.slice(0, 512));
  const payloadStart = dataUrl ? dataUrl[0].length : 0;
  const payloadLength = text.length - payloadStart;
  if (payloadLength <= 0) return null;
  const sample = text.slice(payloadStart, payloadStart + BASE64_SAMPLE);
  const tail =
    payloadLength > BASE64_SAMPLE ? text.slice(-BASE64_SAMPLE) : sample;
  if (!BASE64_SAMPLE_PATTERN.test(sample)) return null;
  if (tail !== sample && !BASE64_SAMPLE_PATTERN.test(tail)) return null;
  // Letters alone would also be a long identifier; base64 of binary data
  // mixes in digits or symbols.
  if (!dataUrl && !/[0-9+/]/.test(sample)) return null;
  return {
    mediaType: dataUrl ? dataUrl[1] || null : null,
    bytes: Math.floor((payloadLength * 3) / 4),
  };
}

/** A string on one line: newlines shown as ↵, clipped to `limit` characters. */
export function stringPreview(text: string, limit: number): string {
  const head = text.slice(0, limit + 1).replace(/\r?\n/g, "↵");
  return head.length > limit ? `${head.slice(0, limit)}…` : head;
}

/**
 * A folded container in one line, the way browser consoles show it:
 * `{role: "user", content: [2]}`. Nested containers show as `{…}` or their
 * length so the preview stays one level deep.
 */
export function containerPreview(node: JsonContainerNode, limit = 96): string {
  const [open, close] = node.kind === "object" ? ["{", "}"] : ["[", "]"];
  const total = childCount(node);
  let body = "";
  for (let index = 0; index < total; index += 1) {
    const { key, value } = childAt(node, index);
    const part = `${key === null ? "" : `${keyPreview(key)}: `}${valuePreview(value)}`;
    const next = body ? `${body}, ${part}` : part;
    if (next.length > limit) {
      body = body ? `${body}, …` : "…";
      break;
    }
    body = next;
  }
  if (!node.complete && total > 0 && !body.endsWith("…")) body += ", …";
  return `${open}${body}${close}`;
}

function keyPreview(key: string): string {
  return /^[A-Za-z_$][\w$]*$/.test(key) ? key : JSON.stringify(key);
}

function valuePreview(node: JsonNode): string {
  switch (node.kind) {
    case "object":
      return node.entries.length === 0 && node.complete ? "{}" : "{…}";
    case "array":
      return `[${node.items.length}]`;
    case "string":
      return detectBase64(node.text)
        ? '"base64…"'
        : JSON.stringify(stringPreview(node.text, 32));
    default:
      return node.text;
  }
}
