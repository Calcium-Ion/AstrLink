import { useEffect, useState } from "react";

export const LIVE_CLOCK_INTERVAL_MS = 100;

/**
 * A clock that only runs while `active`, so it can live next to the text it
 * feeds instead of at the top of the page.
 *
 * The records page used to hold one 10 Hz clock in `RequestRecords` state and
 * hand `nowMs` down to every session row, every trajectory row and every
 * timeline phase. A session with 95 turns re-rendered ~5000 nodes ten times a
 * second just to advance one duration label. Subscribing per subtree keeps the
 * repaint to the node that actually shows a moving number, and a settled
 * record pays for no timer at all.
 */
export function useLiveClock(
  active: boolean,
  intervalMs: number = LIVE_CLOCK_INTERVAL_MS,
): number {
  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    if (!active) return;
    setNowMs(Date.now());
    const timer = window.setInterval(() => setNowMs(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [active, intervalMs]);

  return nowMs;
}
