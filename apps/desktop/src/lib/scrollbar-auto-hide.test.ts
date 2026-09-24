// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { initializeScrollbarAutoHide } from "./scrollbar-auto-hide";

describe("native scrollbar activity", () => {
  let stop: () => void;
  let forcedColors: boolean;
  let reducedMotion: boolean;

  class PaintAnimation {
    onfinish: (() => void) | null = null;
    oncancel: (() => void) | null = null;
    cancel = vi.fn(() => this.oncancel?.());
  }

  const paint = vi.fn<HTMLElement["animate"]>(
    () => new PaintAnimation() as unknown as Animation,
  );
  const originalAnimate = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "animate",
  );

  function scrollport(tag = "div", parent = document.body) {
    const element = document.createElement(tag);
    parent.append(element);
    return element;
  }

  beforeEach(() => {
    forcedColors = false;
    reducedMotion = false;
    paint.mockClear();
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query) =>
        ({
          get matches() {
            return query === "(forced-colors: active)"
              ? forcedColors
              : reducedMotion;
          },
        }) as MediaQueryList,
    );
    Object.defineProperty(HTMLElement.prototype, "animate", {
      configurable: true,
      value: paint,
    });
    stop = initializeScrollbarAutoHide();
  });

  afterEach(() => {
    stop?.();
    document.body.replaceChildren();
    vi.restoreAllMocks();
    if (originalAnimate) {
      Object.defineProperty(HTMLElement.prototype, "animate", originalAnimate);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "animate");
    }
  });

  it("handles non-bubbling scrolls only on their source, including new portal textareas", () => {
    const outer = scrollport();
    const inner = scrollport("div", outer);
    inner.dispatchEvent(new Event("scroll"));
    expect(paint.mock.contexts).toEqual([inner]);

    const portal = scrollport();
    const textarea = scrollport("textarea", portal);
    textarea.dispatchEvent(new Event("scroll"));
    expect(paint.mock.contexts).toEqual([inner, textarea]);
  });

  it("restarts the idle period on further scrolling without hiding another scrollport", () => {
    const first = scrollport();
    const second = scrollport();
    first.dispatchEvent(new Event("scroll"));
    second.dispatchEvent(new Event("scroll"));
    const firstPaint = paint.mock.results[0].value as PaintAnimation;
    const secondPaint = paint.mock.results[1].value as PaintAnimation;
    first.dispatchEvent(new Event("scroll"));
    const restarted = paint.mock.results[2].value as PaintAnimation;
    expect(firstPaint.cancel).toHaveBeenCalledOnce();
    expect(secondPaint.cancel).not.toHaveBeenCalled();
    // A delayed cancellation event from the old animation must not lose the
    // current animation, which still needs cancellation during cleanup.
    firstPaint.oncancel?.();
    stop();
    expect(restarted.cancel).toHaveBeenCalledOnce();
    expect(secondPaint.cancel).toHaveBeenCalledOnce();
  });

  it("releases completed or detached scrollports and removes its listener", () => {
    const finished = scrollport();
    finished.dispatchEvent(new Event("scroll"));
    const completed = paint.mock.results[0].value as PaintAnimation;
    completed.onfinish?.();
    const detached = scrollport();
    detached.dispatchEvent(new Event("scroll"));
    const pending = paint.mock.results[1].value as PaintAnimation;
    detached.remove();
    stop();
    expect(completed.cancel).not.toHaveBeenCalled();
    expect(pending.cancel).toHaveBeenCalledOnce();
    finished.dispatchEvent(new Event("scroll"));
    expect(paint).toHaveBeenCalledTimes(2);
  });

  it("leaves Radix scrollbars and system high-contrast scrollbars to their own controls", () => {
    const radix = scrollport();
    radix.setAttribute("data-radix-scroll-area-viewport", "");
    radix.dispatchEvent(new Event("scroll"));
    expect(paint).not.toHaveBeenCalled();
    forcedColors = true;
    const native = scrollport();
    native.dispatchEvent(new Event("scroll"));
    expect(paint).not.toHaveBeenCalled();
    forcedColors = false;
    native.dispatchEvent(new Event("scroll"));
    expect(paint).toHaveBeenCalledOnce();
  });

  it("keeps the idle delay but skips interpolation when reduced motion is requested", () => {
    reducedMotion = true;
    scrollport().dispatchEvent(new Event("scroll"));
    const [frames, options] = vi.mocked(HTMLElement.prototype.animate).mock
      .calls[0];
    expect(options).toEqual({ duration: 1000 });
    expect(frames).toEqual([
      { "--native-scrollbar-opacity": "1", offset: 0 },
      { "--native-scrollbar-opacity": "1", offset: 1 },
      { "--native-scrollbar-opacity": "0", offset: 1 },
    ]);
  });
});
