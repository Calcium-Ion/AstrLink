import { formatCompactNumber } from "./format-compact-number";
import { i18n } from "./i18n";
import {
  convertUSD,
  displayCurrency,
  formatCurrency,
  smallestUnit,
} from "./currency-display";
import { formatUSD } from "./pricing-model";
import { formatDuration } from "./request-live-model";
import { breakdownShare } from "@/components/BreakdownRow";

import type {
  CacheMiss,
  ContextCompaction,
  ContextItem,
  ContextParts,
  FailedRepeats,
  MemoryWrite,
  RequestRecord,
  RequestSession,
  SessionContext,
} from "./request-record-model";
import { groupTurns } from "./request-trajectory-model";

/** The share of the limit at which the context meter turns to a warning. */
export const CONTEXT_WARNING_PERCENT = 85;

export interface ContextMeterValue {
  tokens: number;
  limit: number;
  /**
   * `compaction` when the limit is where the client was seen compacting,
   * `window` when it is the model's context window.
   */
  basis: "compaction" | "window";
  percent: number;
  warning: boolean;
}

/**
 * How full a conversation's context is: the newest main call's context over
 * the point where the client compacts. Before the first compaction the
 * model's window stands in for that point; without either there is no meter.
 */
export function contextMeterValue(
  session: Pick<
    RequestSession,
    "context_tokens" | "compaction_point_tokens" | "context_window_tokens"
  >,
): ContextMeterValue | null {
  const tokens = session.context_tokens;
  if (tokens == null) return null;
  const point = session.compaction_point_tokens;
  const window = session.context_window_tokens;
  const basis = point ? "compaction" : window ? "window" : null;
  const limit = point || window;
  if (!basis || !limit) return null;
  const percent = (tokens / limit) * 100;
  return {
    tokens,
    limit,
    basis,
    percent,
    warning: percent >= CONTEXT_WARNING_PERCENT,
  };
}

/** Where a root call sits, as the summary numbers it and in the whole run. */
export interface CallPlace {
  turnIndex: number | null;
  /** 1-based position within its turn. */
  index: number;
  /** 1-based position among the conversation's root calls. */
  ordinal: number;
}

export function callPlaces(
  turns: readonly RequestRecord[],
): ReadonlyMap<string, CallPlace> {
  const places = new Map<string, CallPlace>();
  let ordinal = 0;
  for (const group of groupTurns([...turns])) {
    group.records.forEach((record, position) => {
      ordinal += 1;
      places.set(record.id, {
        turnIndex: group.turnIndex,
        index: position + 1,
        ordinal,
      });
    });
  }
  return places;
}

/** What the summary's call list notes beside one call. */
export interface CallMarkers {
  /** The compaction right before this call, drawn as a divider above it. */
  compactionBefore?: ContextCompaction;
  /** The call wrote the summary a compaction kept. */
  summary?: boolean;
  /** Memory and rules files the call's history shows newly written. */
  memory?: MemoryWrite[];
}

const NO_MARKERS: ReadonlyMap<string, CallMarkers> = new Map();

const writeKey = (write: MemoryWrite) =>
  `${write.kind}\u0000${write.path ?? write.name}\u0000${write.title ?? ""}`;

/**
 * Notes for the summary's calls: compactions from the context view, and
 * from each call's own record the summary it asked for and the memory its
 * history shows written. A call that resent the body before it repeats the
 * writes; only the first shows them.
 */
export function callMarkers(
  context: SessionContext | undefined,
  turns: readonly RequestRecord[] = [],
): ReadonlyMap<string, CallMarkers> {
  if (!context && !turns.some((record) => record.context)) return NO_MARKERS;
  const markers = new Map<string, CallMarkers>();
  const add = (id: string, marker: CallMarkers) =>
    markers.set(id, { ...markers.get(id), ...marker });
  for (const compaction of context?.compactions ?? []) {
    add(compaction.request_id, { compactionBefore: compaction });
    if (compaction.summary_request_id) {
      add(compaction.summary_request_id, { summary: true });
    }
  }
  let previous = new Set<string>();
  for (const record of turns) {
    const carried = record.context;
    if (!carried || carried.helper) continue;
    if (carried.summary) add(record.id, { summary: true });
    const keys = new Set(carried.memory_writes.map(writeKey));
    const fresh = carried.memory_writes.filter(
      (write) => !previous.has(writeKey(write)),
    );
    if (fresh.length > 0) add(record.id, { memory: fresh });
    previous = keys;
  }
  return markers;
}

/** One source of a call's input, as its composition lists it. */
export interface CompositionLine {
  key: keyof ContextParts;
  tokens: number;
  /** Bar length against the largest source, as a percentage. */
  share: number;
}

const COMPOSITION_ORDER: readonly (keyof ContextParts)[] = [
  "history",
  "tool_results",
  "tools",
  "system",
  "memory",
];

/**
 * A call's input by source, largest first with what fits nowhere else last.
 * Sources the call did not send are left out.
 */
export function contextComposition(parts: ContextParts): CompositionLine[] {
  const lines = COMPOSITION_ORDER.map((key) => ({ key, tokens: parts[key] }))
    .filter((line) => line.tokens > 0)
    .sort((left, right) => right.tokens - left.tokens);
  if (parts.other > 0) lines.push({ key: "other", tokens: parts.other });
  const values = lines.map((line) => line.tokens);
  return lines.map((line) => ({
    ...line,
    share: breakdownShare(line.tokens, values),
  }));
}

/** How a carried item changed over the conversation up to one call. */
export type CarriedChange =
  | { kind: "unchanged" }
  /** Carried as it is now since this call; it read otherwise before. */
  | { kind: "changed"; ordinal: number }
  /** First carried at this call. */
  | { kind: "added"; ordinal: number }
  /** A list that gained this many entries since the conversation began. */
  | { kind: "grew"; entries: number };

export interface CarriedItem {
  item: ContextItem;
  change: CarriedChange;
}

const itemKey = (item: ContextItem) =>
  `${item.kind}\u0000${item.path ?? ""}\u0000${item.name ?? ""}`;

/**
 * The rules, memory and skills one main call carried, in the order it sent
 * them, each with how it changed since the conversation's first main call
 * that kept a record of what it carried. MCP tools are tool definitions and
 * stay out.
 */
export function carriedItems(
  turns: readonly RequestRecord[],
  context: SessionContext,
  requestId: string,
  places: ReadonlyMap<string, CallPlace>,
): CarriedItem[] {
  const main = new Set(context.calls.map((call) => call.request_id));
  const observed = turns.filter(
    (record) => main.has(record.id) && record.context,
  );
  const at = observed.findIndex((record) => record.id === requestId);
  if (at < 0) return [];
  const indexed = observed.slice(0, at + 1).map((record) => {
    const items = new Map<string, ContextItem>();
    for (const item of record.context!.items) items.set(itemKey(item), item);
    return items;
  });
  const ordinal = (position: number) =>
    places.get(observed[position]!.id)?.ordinal ?? position + 1;
  return observed[at]!.context!.items.flatMap((item) => {
    if (item.kind === "mcp") return [];
    const key = itemKey(item);
    // Walk back while the item reads the same. A call without a hash cannot
    // tell, so it is compared through to the nearest one that has it.
    let since = at;
    let known = item.hash;
    while (since > 0) {
      const before = indexed[since - 1]!.get(key);
      if (!before || (known && before.hash && before.hash !== known)) break;
      known ||= before.hash;
      since -= 1;
    }
    const first = indexed.findIndex((items) => items.has(key));
    let change: CarriedChange;
    if (since === first) {
      change =
        first === 0
          ? { kind: "unchanged" }
          : { kind: "added", ordinal: ordinal(first) };
    } else {
      const start = indexed[first]!.get(key)!.count;
      change =
        item.count != null && start != null && item.count > start
          ? { kind: "grew", entries: item.count - start }
          : { kind: "changed", ordinal: ordinal(since) };
    }
    return [{ item, change }];
  });
}

/** What a carried item is, as its line names it. */
export function carriedKind(
  item: Pick<ContextItem, "kind" | "scope" | "count" | "servers">,
): string {
  const t = i18n.t.bind(i18n);
  switch (item.kind) {
    case "rules":
      return item.scope === "global"
        ? t("sessionContext.carriedGlobalRules")
        : item.scope === "project"
          ? t("sessionContext.carriedProjectRules")
          : item.scope === "local"
            ? t("sessionContext.carriedLocalRules")
            : t("sessionContext.carriedRules");
    case "memory":
      return item.count != null
        ? t("sessionContext.carriedMemoryEntries", { count: item.count })
        : t("sessionContext.carriedMemory");
    case "skills":
      return t("sessionContext.carriedSkillCount", { count: item.count ?? 0 });
    case "mcp": {
      // The servers the tools come from name them better than a count does.
      const count = t("sessionContext.carriedToolCount", {
        count: item.count ?? 0,
      });
      return item.servers?.length
        ? [...item.servers, count].join(" · ")
        : count;
    }
  }
}

/** The name a carried item goes by: its file, or the catalog it is. */
export function carriedName(item: Pick<ContextItem, "kind" | "name">): string {
  const t = i18n.t.bind(i18n);
  if (item.kind === "skills") return t("sessionContext.carriedSkills");
  if (item.kind === "mcp") return t("sessionContext.carriedMcp");
  return item.name ?? t("sessionContext.carriedUnnamed");
}

/** A carried item's change, in a few words. */
export function carriedChangeText(change: CarriedChange): string {
  const t = i18n.t.bind(i18n);
  switch (change.kind) {
    case "unchanged":
      return t("sessionContext.carriedUnchanged");
    case "changed":
      return t("sessionContext.carriedChangedSince", {
        ordinal: change.ordinal,
      });
    case "added":
      return t("sessionContext.carriedAddedAt", { ordinal: change.ordinal });
    case "grew":
      return t("sessionContext.carriedGrew", { count: change.entries });
  }
}

const NANOS_PER_USD = 1_000_000_000n;

function nanos(amount: string): bigint {
  const [whole = "0", fraction = ""] = amount.split(".");
  return (
    BigInt(whole) * NANOS_PER_USD + BigInt(fraction.padEnd(9, "0").slice(0, 9))
  );
}

/** Sums decimal dollar amounts exactly; null when there are none. */
export function sumUSD(amounts: readonly string[]): string | null {
  if (amounts.length === 0) return null;
  const total = amounts.reduce((sum, amount) => sum + nanos(amount), 0n);
  const whole = total / NANOS_PER_USD;
  const fraction = (total % NANOS_PER_USD).toString().padStart(9, "0");
  return `${whole}.${fraction}`;
}

/** A cache miss's extra cost; a few tenths of a cent still read as present. */
export function formatExtraUSD(amount: string): string {
  const currency = displayCurrency();
  const value = convertUSD(Number(amount), currency);
  const unit = smallestUnit(currency);
  if (value > 0 && value < unit / 2)
    return `< ${formatCurrency(unit, currency.digits, currency)}`;
  return formatUSD(amount);
}

/** Round axis steps: 1, 2, 2.5 or 5 times a power of ten. */
export function contextAxis(
  peak: number,
  intervals = 4,
): { ceiling: number; step: number } {
  if (!(peak > 0)) return { ceiling: 1, step: 1 };
  const raw = peak / intervals;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const step =
    [1, 2, 2.5, 5, 10]
      .map((factor) => factor * magnitude)
      .find((candidate) => candidate >= raw) ?? 10 * magnitude;
  return { ceiling: Math.ceil(peak / step) * step, step };
}

/** One stretch of the curve between two compactions. */
export interface ContextRun {
  /** Positions in `SessionContext.calls`, oldest first. */
  from: number;
  to: number;
}

/** The context curve split where the client compacted. */
export function contextRuns(context: SessionContext): ContextRun[] {
  const starts = new Set(context.compactions.map((c) => c.request_id));
  const runs: ContextRun[] = [];
  context.calls.forEach((call, position) => {
    const last = runs[runs.length - 1];
    if (!last || starts.has(call.request_id)) {
      runs.push({ from: position, to: position });
    } else {
      last.to = position;
    }
  });
  return runs;
}

/** The call nearest to a share of the plot's width. */
export function nearestCall(count: number, share: number): number {
  if (count <= 1) return 0;
  const clamped = Math.min(1, Math.max(0, share));
  return Math.round(clamped * (count - 1));
}

/** Why one call missed the prompt cache, in a few words. */
export function cacheMissReason(miss: CacheMiss): string | null {
  const t = i18n.t.bind(i18n);
  switch (miss.reason) {
    case "provider_changed":
      return t("sessionContext.reasonProvider");
    case "model_changed":
      return t("sessionContext.reasonModel");
    case "expired":
      return t("sessionContext.reasonExpired", {
        idle: formatDuration(miss.idle_ms ?? 0),
      });
    case "prefix_changed":
      switch (miss.changed) {
        case undefined:
        case "":
          return null;
        case "system":
          return t("sessionContext.reasonSystem");
        case "tools":
          return t("sessionContext.reasonTools");
        // The core names the skills catalog, and a file without a name, by
        // its kind.
        case "skills":
          return t("sessionContext.reasonSkills");
        case "rules":
          return t("sessionContext.reasonRules");
        case "memory":
          return t("sessionContext.reasonMemory");
        default:
          return t("sessionContext.reasonFile", { file: miss.changed });
      }
    default:
      return null;
  }
}

/** What a cache miss cost, or how much it wrote again without a price. */
export function cacheMissCost(miss: CacheMiss): string {
  const t = i18n.t.bind(i18n);
  return miss.extra_usd != null
    ? t("sessionContext.extraCost", { amount: formatExtraUSD(miss.extra_usd) })
    : t("sessionContext.rewritten", {
        tokens: formatCompactNumber(miss.rewritten_tokens),
      });
}

/** The line under a call whose history shows a memory or rules file written. */
export function memoryWriteLine(write: MemoryWrite): string {
  const rules = write.kind === "rules";
  return write.title
    ? i18n.t(
        rules ? "sessionContext.rulesAdded" : "sessionContext.memoryAdded",
        { title: write.title },
      )
    : i18n.t(
        rules
          ? "sessionContext.rulesAddedUntitled"
          : "sessionContext.memoryAddedUntitled",
      );
}

/** The line a conversation shows while one tool call keeps failing. */
export function failedRepeatsText(repeats: FailedRepeats): string {
  return i18n.t(
    repeats.command
      ? "sessionContext.failedRepeatsCommand"
      : "sessionContext.failedRepeatsCall",
    { tool: repeats.tool, times: repeats.count },
  );
}

export function callPlaceLabel(place: CallPlace | undefined): string {
  if (!place) return "";
  return place.turnIndex === null
    ? i18n.t("sessionContext.callPlaceUnnumbered", { index: place.index })
    : i18n.t("sessionContext.callPlace", {
        turn: place.turnIndex,
        index: place.index,
      });
}
