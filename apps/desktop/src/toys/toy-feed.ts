// The activity every toy window reads: the host polls once for all of them
// and pushes it here; a page that just loaded asks for the last read.
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";

import { applyLocale, isLocale } from "../i18n";
import { readCrew, stagePassed } from "../live-crew-model";
import { parseDesktopToys } from "../preferences-model";
import { parseRequestActivity } from "../request-record-model";
import { cfg } from "./toy-config";
import { syncPace } from "./toy-kit";
import { Sample } from "./toy-sample";

export const FEED_EVENT = "toys:feed";
export const PREFS_EVENT = "toys:prefs";
export const POINTER_EVENT = "toys:pointer";
export const FRAMES_EVENT = "toys:frames";
export const IDLE_EVENT = "toys:idle";
export const SCREEN_EVENT = "toys:screen";

/** Today's tokens as last read; null until the first read. */
export const feed: { tokens: number | null } = { tokens: null };

export interface FeedHandlers {
  /** Today's tokens passed a stage, with milestones on. */
  milestone?: (stage: number) => void;
  prefs?: () => void;
  /** The other apps' windows and the Dock, for the desktop pet. */
  frames?: (frames: unknown) => void;
  /**
   * Seconds without key or mouse input, and how many make you away; the host
   * waits longer while the AI works and you may be watching it.
   */
  idle?: (idle: IdleReading) => void;
  /** A pet in your hand took her window onto another screen. */
  screen?: (move: ScreenMove) => void;
}

/**
 * A pet window's move to another screen: add `dx`, `dy` to a point in its
 * old points to get the same spot in its new ones; `w`, `h` is its new size.
 */
export interface ScreenMove {
  dx: number;
  dy: number;
  w: number;
  h: number;
}

export interface IdleReading {
  idle: number;
  away: number;
}

let lastTokens: number | null = null;
/** Debug builds can play the sample conversations instead of the gateway's. */
let sample: Sample | null = null;
let sampleTimer = 0;

function playSample(on: boolean, handlers: FeedHandlers) {
  if (on === Boolean(sample)) return;
  window.clearInterval(sampleTimer);
  sample = on ? new Sample() : null;
  if (!sample) return;
  const play = sample;
  let tick = 0;
  sampleTimer = window.setInterval(() => {
    play.step(0.25);
    tick += 1;
    if (tick % 4 !== 0) return;
    syncPace(play.reading());
    // A day squeezed into 80 seconds, so milestones come round.
    const k = (play.T % 80) / 80;
    const tokens = k <= 0 ? 0 : Math.round(3e4 * (2e9 / 3e4) ** k);
    const passed = stagePassed(lastTokens, tokens);
    lastTokens = tokens;
    feed.tokens = tokens;
    if (passed !== null && cfg.prefs.milestone) handlers.milestone?.(passed);
  }, 250);
}

function acceptFeed(payload: unknown, handlers: FeedHandlers) {
  if (typeof payload !== "object" || payload === null) return;
  const {
    activity,
    tokens,
    sample: wantSample,
  } = payload as {
    activity?: unknown;
    tokens?: unknown;
    sample?: unknown;
  };
  playSample(wantSample === true, handlers);
  if (sample) return;
  if (activity) {
    try {
      syncPace(readCrew(parseRequestActivity(activity)));
    } catch (error) {
      console.error("Ignoring unreadable desktop toy activity", error);
    }
  }
  if (typeof tokens === "number" && Number.isFinite(tokens)) {
    const passed = stagePassed(lastTokens, tokens);
    lastTokens = tokens;
    feed.tokens = tokens;
    if (passed !== null && cfg.prefs.milestone) handlers.milestone?.(passed);
  }
}

function acceptPrefs(payload: unknown, handlers: FeedHandlers) {
  if (typeof payload !== "object" || payload === null) return;
  const { locale, ...toys } = payload as Record<string, unknown>;
  try {
    cfg.prefs = parseDesktopToys(toys, "$.toys");
  } catch (error) {
    console.error("Ignoring unreadable desktop toy preferences", error);
    return;
  }
  if (isLocale(locale)) void applyLocale(locale);
  handlers.prefs?.();
}

/** The host's idle reading, or null if it is not one. */
export function readIdle(payload: unknown): IdleReading | null {
  if (typeof payload !== "object" || payload === null) return null;
  const { idle, away } = payload as { idle?: unknown; away?: unknown };
  return typeof idle === "number" &&
    Number.isFinite(idle) &&
    typeof away === "number" &&
    Number.isFinite(away)
    ? { idle, away }
    : null;
}

/** The host's screen move, or null if it is not one. */
export function readScreenMove(payload: unknown): ScreenMove | null {
  if (typeof payload !== "object" || payload === null) return null;
  const { dx, dy, w, h } = payload as Partial<
    Record<keyof ScreenMove, unknown>
  >;
  const nums = [dx, dy, w, h];
  return nums.every((n) => typeof n === "number" && Number.isFinite(n)) &&
    (w as number) > 0 &&
    (h as number) > 0
    ? { dx: dx as number, dy: dy as number, w: w as number, h: h as number }
    : null;
}

/** Follows the host's feed and preferences; resolves once the last read is in. */
export async function startFeed(handlers: FeedHandlers = {}) {
  if (!isTauri()) return () => {};
  const stops: UnlistenFn[] = await Promise.all([
    listen<unknown>(FEED_EVENT, ({ payload }) => acceptFeed(payload, handlers)),
    listen<unknown>(PREFS_EVENT, ({ payload }) =>
      acceptPrefs(payload, handlers),
    ),
  ]);
  const onFrames = handlers.frames;
  if (onFrames)
    stops.push(
      await listen<unknown>(FRAMES_EVENT, ({ payload }) => onFrames(payload)),
    );
  const onIdle = handlers.idle;
  if (onIdle)
    stops.push(
      await listen<unknown>(IDLE_EVENT, ({ payload }) => {
        const idle = readIdle(payload);
        if (idle) onIdle(idle);
      }),
    );
  const onScreen = handlers.screen;
  if (onScreen)
    stops.push(
      await listen<unknown>(SCREEN_EVENT, ({ payload }) => {
        const move = readScreenMove(payload);
        if (move) onScreen(move);
      }),
    );
  try {
    const snapshot = await invoke<{
      prefs: unknown;
      feed: unknown;
      frames: unknown;
    }>("toys_snapshot");
    acceptPrefs(snapshot.prefs, handlers);
    acceptFeed(snapshot.feed, handlers);
    if (snapshot.frames) onFrames?.(snapshot.frames);
  } catch (error) {
    console.error("Unable to read the desktop toys", error);
  }
  return () => stops.forEach((stop) => stop());
}

/**
 * Tells the host which parts of this window hover or take clicks; `hold`
 * keeps the whole window taking the mouse, and `carry` says a pet is in your
 * hand, so the window follows the mouse onto another screen.
 */
export function reportHits(
  rects: Array<{ x: number; y: number; w: number; h: number; click?: boolean }>,
  hold = false,
  carry = hold,
) {
  if (!isTauri()) return;
  void invoke("toys_hit", { rects, hold, carry }).catch(() => undefined);
}

export async function saveToys(patch: Partial<typeof cfg.prefs>) {
  cfg.prefs = { ...cfg.prefs, ...patch };
  await invoke("update_desktop_toys", { input: cfg.prefs });
}
