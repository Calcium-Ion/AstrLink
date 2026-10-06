/**
 * Find-in-content shared by the trajectory list and the inspector's captured
 * bodies: one way to read a query, find its hits, and say where they are.
 */

/** What a view asks of find: the query, and which hit to bring into view. */
export interface FindRequest {
  /** As typed; a blank query asks for nothing. */
  query: string;
  /** Zero-based hit, in document order, to bring into view. */
  active: number;
  /** Bumped on every step, so stepping onto the same hit scrolls it back. */
  seq: number;
  /** Receives the hit count once the view has searched. */
  onResult: (result: FindResult) => void;
}

export interface FindResult {
  count: number;
  /** More hits exist than were counted. */
  capped: boolean;
}

/** Most hits counted in one body; past this the counter reads "1000+". */
export const FIND_HIT_LIMIT = 1000;

/**
 * The query as a case-insensitive pattern, or null for a blank one. Unicode
 * case folding keeps each hit as long as the query, so offsets found here
 * line up with the text that is shown.
 */
export function findMatcher(query: string): RegExp | null {
  const needle = findNeedle(query);
  if (!needle) return null;
  return new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "giu");
}

/** The part of a query find looks for: surrounding spaces are typing noise. */
export function findNeedle(query: string): string {
  return query.trim();
}

/** Where `matcher` hits in `text`, without overlaps, at most `limit` of them. */
export function findOffsets(
  text: string,
  matcher: RegExp,
  limit = Number.POSITIVE_INFINITY,
): number[] {
  const offsets: number[] = [];
  if (limit <= 0) return offsets;
  matcher.lastIndex = 0;
  for (let match = matcher.exec(text); match; match = matcher.exec(text)) {
    offsets.push(match.index);
    if (offsets.length >= limit) break;
    // A zero-length match cannot happen for a non-blank needle, but a stuck
    // cursor would hang the window, so step past one anyway.
    if (match[0].length === 0) matcher.lastIndex += 1;
  }
  matcher.lastIndex = 0;
  return offsets;
}

export function findMatches(text: string, matcher: RegExp): boolean {
  matcher.lastIndex = 0;
  const found = matcher.test(text);
  matcher.lastIndex = 0;
  return found;
}

/** The hit `step` moves to, wrapping at either end. */
export function stepFind(active: number, count: number, step: 1 | -1): number {
  if (count <= 0) return 0;
  return (((active + step) % count) + count) % count;
}

/** Whether a key press is the platform's find shortcut (⌘F, or Ctrl+F). */
export function isFindShortcut(event: KeyboardEvent): boolean {
  return (
    (event.metaKey || event.ctrlKey) &&
    !event.altKey &&
    !event.shiftKey &&
    event.key.toLowerCase() === "f"
  );
}

/** Whether a key press steps through hits (⌘G / ⇧⌘G, or Ctrl+G). */
export function findStepShortcut(event: KeyboardEvent): 1 | -1 | null {
  if (!(event.metaKey || event.ctrlKey) || event.altKey) return null;
  if (event.key.toLowerCase() !== "g") return null;
  return event.shiftKey ? -1 : 1;
}

export function findShortcutLabel(): string {
  const mac =
    typeof navigator !== "undefined" &&
    /Mac|iPhone|iPad/.test(navigator.platform);
  return mac ? "⌘F" : "Ctrl+F";
}
