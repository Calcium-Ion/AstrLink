import {
  useEffect,
  useState,
  useSyncExternalStore,
  type RefObject,
} from "react";

import { fitMainWindowToContent, hasNativeBridge } from "./bridge";
import { isWebEdition } from "./edition";
import type { UsageStatus } from "./usage-range";

/**
 * How long the first overview may take to settle once the window shows.
 * Later than this the window keeps its opening height rather than jumping
 * while someone uses it.
 */
export const INITIAL_WINDOW_FIT_DEADLINE_MS = 8_000;

/**
 * How soon after the page loads the overview has to open. An overview first
 * shown later, after onboarding, leaves the window as the operator has had
 * it all along.
 */
export const INITIAL_WINDOW_FIT_CLAIM_MS = 10_000;

type Attempt = { owner: object; deadline: number | null; finished: boolean };

/**
 * One attempt per page load, claimed by the overview that opens the window.
 * An overview mounted later, after the first one left, never resizes it.
 */
let attempt: Attempt | null = null;

export function resetInitialWindowFitForTests(): void {
  attempt = null;
}

/**
 * The two modules the opening window is fitted to: the usage summary and
 * the live crew's scene, in either order. Any other pair on top leaves the
 * opening height alone.
 */
const FITTED_MODULES = ["usage", "live-crew"];

/**
 * The window's inner height, in whole CSS pixels, that ends right where the
 * overview's third module (or the footer) begins, so the usage summary and
 * the live crew fit and nothing of the next row shows. Measured against the
 * scroller as if it were scrolled to the top, so the chrome around it needs
 * no fixed numbers. Null while the first two modules are not laid out yet;
 * `"other-modules"` when they are not that pair.
 */
export function measureInitialWindowHeight(
  scroller: HTMLElement,
  innerHeight: number,
): number | "other-modules" | null {
  const list = scroller
    .querySelector('[data-slot="ordered-module-content"]')
    ?.closest("[data-ordered-item]")?.parentElement;
  if (!list) return null;
  const items = Array.from(list.children).filter(
    (item): item is HTMLElement =>
      item instanceof HTMLElement &&
      item.hasAttribute("data-ordered-item") &&
      item.getBoundingClientRect().height > 0,
  );
  const fitted = items.slice(0, 2);
  const last = fitted.at(-1);
  if (!last || fitted.length < FITTED_MODULES.length) return null;
  if (
    !FITTED_MODULES.every((id) =>
      fitted.some((item) => item.getAttribute("data-ordered-item") === id),
    )
  ) {
    return "other-modules";
  }
  // The crew sizes its canvas from its own width a frame after mounting.
  const crew = fitted.find(
    (item) => item.getAttribute("data-ordered-item") === "live-crew",
  );
  if (
    !(
      (crew
        ?.querySelector('[data-slot="live-crew-canvas"]')
        ?.getBoundingClientRect().height ?? 0) > 0
    )
  ) {
    return null;
  }
  const next = items[fitted.length];
  const footer = scroller.querySelector('[data-slot="overview-footer"]');
  let target: number;
  if (next) {
    target = next.getBoundingClientRect().top;
  } else if (footer?.previousElementSibling) {
    // The footer is pushed to the bottom of a taller window; where it
    // starts in a window that ends at it is right below the module list.
    target = footer.previousElementSibling.getBoundingClientRect().bottom;
  } else {
    target =
      last.getBoundingClientRect().bottom +
      (Number.parseFloat(getComputedStyle(scroller).paddingBottom) || 0);
  }
  const visibleBottom =
    scroller.getBoundingClientRect().top +
    scroller.clientTop +
    scroller.clientHeight;
  const height = Math.floor(
    innerHeight + target + scroller.scrollTop - visibleBottom,
  );
  return Number.isFinite(height) && height > 0 ? height : null;
}

function subscribeVisibility(onChange: () => void): () => void {
  document.addEventListener("visibilitychange", onChange);
  return () => document.removeEventListener("visibilitychange", onChange);
}

function useDocumentVisible(): boolean {
  return useSyncExternalStore(
    subscribeVisibility,
    () => document.visibilityState !== "hidden",
    () => true,
  );
}

/**
 * Shortens the opening main window to the overview's usage summary and live
 * crew when those two lead the page, once their size settles: the usage
 * panel shows data (a loading, blocked or failed panel has another height),
 * the live crew has drawn, and two samples two animation frames apart
 * agree. Other modules on top leave the window as it opened. The host only
 * shrinks the window it placed at startup and the operator left alone; a
 * window too short for both modules keeps its height.
 */
export function useInitialWindowFit(
  anchor: RefObject<HTMLElement | null>,
  usageStatus: UsageStatus,
): void {
  const ready = usageStatus === "ready";
  const visible = useDocumentVisible();
  // One identity for this overview, kept across Strict Mode's effect rerun.
  const [owner] = useState(() => ({}));
  useEffect(() => {
    if (isWebEdition || !hasNativeBridge()) return;
    attempt ??= {
      owner,
      deadline: null,
      finished: performance.now() > INITIAL_WINDOW_FIT_CLAIM_MS,
    };
    const current = attempt;
    if (current.owner !== owner || current.finished) return;
    // A window started hidden at login is first seen when it shows, and
    // draws nothing before; its time to settle starts then.
    if (!visible) return;
    current.deadline ??= Date.now() + INITIAL_WINDOW_FIT_DEADLINE_MS;
    const deadlineAt = current.deadline;
    const remaining = deadlineAt - Date.now();
    if (remaining <= 0) {
      current.finished = true;
      return;
    }
    const scroller = anchor.current?.closest<HTMLElement>(
      '[data-slot="overview-content"]',
    );
    if (!ready || !scroller) return;

    let frame = 0;
    let previous: number | null = null;
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => schedule());
    const stop = () => {
      cancelAnimationFrame(frame);
      frame = 0;
      observer?.disconnect();
      clearTimeout(deadline);
    };
    const giveUp = () => {
      current.finished = true;
      stop();
    };
    const deadline = setTimeout(giveUp, remaining);
    const sample = () => {
      if (Date.now() >= deadlineAt) {
        giveUp();
        return;
      }
      const height = measureInitialWindowHeight(scroller, window.innerHeight);
      if (height === "other-modules") {
        giveUp();
        return;
      }
      if (height === null) {
        // Wait for the resize that lays the modules out.
        previous = null;
        return;
      }
      if (height !== previous) {
        previous = height;
        schedule();
        return;
      }
      current.finished = true;
      stop();
      fitMainWindowToContent(height, window.devicePixelRatio).catch(() => {});
    };
    function schedule() {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          frame = 0;
          sample();
        });
      });
    }
    observer?.observe(scroller);
    for (const child of Array.from(scroller.children)) {
      observer?.observe(child);
    }
    schedule();
    return stop;
  }, [anchor, owner, ready, visible]);
}
