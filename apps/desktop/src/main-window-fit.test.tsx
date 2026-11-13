// @vitest-environment happy-dom

import { StrictMode, act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UsageStatus } from "./usage-range";

const mocks = vi.hoisted(() => ({
  fit: vi.fn(),
  native: true,
  web: false,
}));
vi.mock("./bridge", () => ({
  fitMainWindowToContent: mocks.fit,
  hasNativeBridge: () => mocks.native,
}));
vi.mock("./edition", () => ({
  get isWebEdition() {
    return mocks.web;
  },
}));

import {
  INITIAL_WINDOW_FIT_CLAIM_MS,
  INITIAL_WINDOW_FIT_DEADLINE_MS,
  measureInitialWindowHeight,
  resetInitialWindowFitForTests,
  useInitialWindowFit,
} from "./main-window-fit";

const frames = new Map<number, FrameRequestCallback>();
let nextFrame = 1;
const observers: { callback: () => void; disconnected: boolean }[] = [];

function flushFrames(rounds = 10) {
  for (let round = 0; round < rounds; round += 1) {
    const due = [...frames.values()];
    frames.clear();
    for (const callback of due) callback(0);
  }
}

function resize() {
  for (const observer of observers) {
    if (!observer.disconnected) observer.callback();
  }
}

function Harness({
  modules,
  status,
}: {
  modules: string[];
  status: UsageStatus;
}) {
  const footerRef = useRef<HTMLDivElement>(null);
  useInitialWindowFit(footerRef, status);
  return (
    <div data-slot="overview-content">
      <div data-slot="modules">
        <ol>
          {modules.map((id) => (
            <li data-ordered-item={id} key={id}>
              <div data-slot="ordered-module-content">
                {id === "live-crew" ? (
                  <canvas data-slot="live-crew-canvas" />
                ) : null}
              </div>
            </li>
          ))}
        </ol>
      </div>
      <div data-slot="overview-footer" ref={footerRef} />
    </div>
  );
}

function setRect(element: Element | null, top: number, height: number) {
  if (!element) throw new Error("missing element");
  element.getBoundingClientRect = () =>
    ({
      top,
      bottom: top + height,
      height,
      left: 0,
      right: 900,
      width: 900,
      x: 0,
      y: top,
    }) as DOMRect;
}

/**
 * A 720-pixel window whose scroller starts 100 pixels down and shows 500:
 * usage 300 tall, a 12-pixel gap, the crew 290 tall with its canvas drawn
 * `canvasHeight` tall, then the next module.
 */
function layOut(container: HTMLElement, canvasHeight = 224, scrollTop = 0) {
  const scroller = container.querySelector<HTMLElement>(
    '[data-slot="overview-content"]',
  )!;
  Object.defineProperties(scroller, {
    clientHeight: { configurable: true, value: 500 },
    clientTop: { configurable: true, value: 0 },
    scrollTop: { configurable: true, value: scrollTop },
  });
  setRect(scroller, 100, 500);
  const items = container.querySelectorAll("[data-ordered-item]");
  setRect(items[0] ?? null, 100 - scrollTop, 300);
  setRect(items[1] ?? null, 412 - scrollTop, 290);
  if (items[2]) setRect(items[2], 714 - scrollTop, 260);
  const canvas = container.querySelector('[data-slot="live-crew-canvas"]');
  if (canvas) setRect(canvas, 450 - scrollTop, canvasHeight);
  setRect(
    container.querySelector('[data-slot="modules"]'),
    100 - scrollTop,
    items[2] ? 878 : 606,
  );
  return scroller;
}

describe("initial main window fit", () => {
  let container: HTMLDivElement;
  let root: Root;

  async function render(modules: string[], status: UsageStatus) {
    await act(async () => {
      root.render(
        <StrictMode>
          <Harness modules={modules} status={status} />
        </StrictMode>,
      );
    });
  }

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    resetInitialWindowFitForTests();
    mocks.fit.mockReset();
    mocks.fit.mockResolvedValue(true);
    mocks.native = true;
    mocks.web = false;
    frames.clear();
    observers.length = 0;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      const id = nextFrame++;
      frames.set(id, callback);
      return id;
    });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
    vi.stubGlobal(
      "ResizeObserver",
      class {
        entry = { callback: () => {}, disconnected: false };
        constructor(callback: () => void) {
          this.entry.callback = callback;
          observers.push(this.entry);
        }
        observe() {}
        unobserve() {}
        disconnect() {
          this.entry.disconnected = true;
        }
      },
    );
    Object.defineProperty(window, "innerHeight", {
      configurable: true,
      value: 720,
    });
    Object.defineProperty(window, "devicePixelRatio", {
      configurable: true,
      value: 1.25,
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("reports once the window height that ends where the third module starts", async () => {
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledTimes(1);
    expect(mocks.fit).toHaveBeenCalledWith(834, 1.25);

    resize();
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledTimes(1);
    expect(observers.every((observer) => observer.disconnected)).toBe(true);
  });

  it.each(["loading", "blocked", "error"] as const)(
    "waits while usage is %s",
    async (status) => {
      await render(["usage", "live-crew", "services"], status);
      layOut(container);
      flushFrames();
      resize();
      flushFrames();
      expect(mocks.fit).not.toHaveBeenCalled();

      await render(["usage", "live-crew", "services"], "ready");
      layOut(container);
      flushFrames();
      expect(mocks.fit).toHaveBeenCalledWith(834, 1.25);
    },
  );

  it("waits for the live crew to draw its canvas", async () => {
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container, 0);
    flushFrames();
    expect(mocks.fit).not.toHaveBeenCalled();

    layOut(container, 224);
    resize();
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledTimes(1);
    expect(mocks.fit).toHaveBeenCalledWith(834, 1.25);
  });

  it("does not resize again for a later overview", async () => {
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    await act(async () => root.unmount());
    root = createRoot(container);

    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledTimes(1);
  });

  it("abandons the fit when the first overview leaves before settling", async () => {
    await render(["usage", "live-crew", "services"], "loading");
    await act(async () => root.unmount());
    root = createRoot(container);

    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).not.toHaveBeenCalled();
  });

  it("never measures in the web edition or without the native bridge", async () => {
    mocks.web = true;
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(observers).toHaveLength(0);
    await act(async () => root.unmount());

    resetInitialWindowFitForTests();
    mocks.web = false;
    mocks.native = false;
    root = createRoot(container);
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(observers).toHaveLength(0);
    expect(mocks.fit).not.toHaveBeenCalled();
  });

  it("keeps the opening height when the overview settles too late", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container, 0);
    flushFrames();
    await act(async () => {
      vi.advanceTimersByTime(INITIAL_WINDOW_FIT_DEADLINE_MS);
    });
    expect(observers.every((observer) => observer.disconnected)).toBe(true);

    layOut(container, 224);
    resize();
    flushFrames();
    expect(mocks.fit).not.toHaveBeenCalled();
  });

  it("gives up when usage turns ready after the deadline", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    await render(["usage", "live-crew", "services"], "loading");
    vi.advanceTimersByTime(INITIAL_WINDOW_FIT_DEADLINE_MS);

    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).not.toHaveBeenCalled();
  });

  it("starts the deadline when a window opened hidden first shows", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    let visibility: DocumentVisibilityState = "hidden";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(
      () => visibility,
    );
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    await act(async () => {
      vi.advanceTimersByTime(INITIAL_WINDOW_FIT_DEADLINE_MS * 3);
    });
    expect(observers).toHaveLength(0);

    visibility = "visible";
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    layOut(container);
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledTimes(1);
    expect(mocks.fit).toHaveBeenCalledWith(834, 1.25);
  });

  it("leaves the window alone when the overview opens after onboarding", async () => {
    vi.spyOn(performance, "now").mockReturnValue(
      INITIAL_WINDOW_FIT_CLAIM_MS + 60_000,
    );
    await render(["usage", "live-crew", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(observers).toHaveLength(0);
    expect(mocks.fit).not.toHaveBeenCalled();
  });

  it("ends at the module list when only two modules show", async () => {
    await render(["usage", "live-crew"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledWith(826, 1.25);
  });

  it("measures as if the scroller were at the top", async () => {
    await render(["usage", "live-crew", "services"], "loading");
    const scroller = layOut(container, 224, 50);
    expect(measureInitialWindowHeight(scroller, 720)).toBe(834);
  });

  it.each([
    [["usage", "services", "live-crew"]],
    [["services", "usage", "live-crew"]],
    [["services", "tokens", "usage"]],
    [["live-crew", "services", "usage"]],
  ])(
    "leaves the opening height alone when %j lead the overview",
    async (modules) => {
      await render(modules, "ready");
      const scroller = layOut(container);
      flushFrames();
      expect(measureInitialWindowHeight(scroller, 720)).toBe("other-modules");
      expect(mocks.fit).not.toHaveBeenCalled();
      expect(observers.every((observer) => observer.disconnected)).toBe(true);

      // The attempt is spent; a later layout does not resize the window.
      await render(["usage", "live-crew", "services"], "ready");
      layOut(container);
      flushFrames();
      expect(mocks.fit).not.toHaveBeenCalled();
    },
  );

  it("fits the crew above the usage summary as well", async () => {
    await render(["live-crew", "usage", "services"], "ready");
    layOut(container);
    flushFrames();
    expect(mocks.fit).toHaveBeenCalledWith(834, 1.25);
  });
});
