// @vitest-environment happy-dom

import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const controls = vi.hoisted(() => ({
  start: vi.fn(),
  stop: vi.fn(),
  set: vi.fn(),
}));
vi.mock("motion/react", () => ({ useAnimation: () => controls }));

import { createAnimatedIcon } from "./create-animated-icon";
import { Button } from "@/components/ui/button";

const Icon = createAnimatedIcon("test", () => <path d="M4 12h16" />);
const SequenceIcon = createAnimatedIcon(
  "sequence",
  () => <path d="M4 12h16" />,
  {
    animate: ["first", "second"],
  },
);

describe("animated icon interaction", () => {
  let container: HTMLDivElement;
  let root: Root;
  let media: MediaQueryList;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    controls.start.mockResolvedValue(undefined);
    media = Object.assign(new EventTarget(), {
      matches: false,
    }) as MediaQueryList;
    vi.spyOn(window, "matchMedia").mockReturnValue(media);
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.restoreAllMocks();
  });

  it("preserves a direct SVG, size, stroke, refs, and consumer handlers", async () => {
    const ref = createRef<SVGSVGElement>();
    const click = vi.fn();
    await act(async () =>
      root.render(
        <Icon
          ref={ref}
          size={18}
          strokeWidth={1.6}
          className="size-4"
          aria-label="Status"
          onClick={click}
        />,
      ),
    );
    const svg = container.firstElementChild!;
    expect(svg.tagName).toBe("svg");
    expect(ref.current).toBe(svg);
    expect(svg.getAttribute("width")).toBe("18");
    expect(svg.getAttribute("height")).toBe("18");
    expect(svg.getAttribute("stroke-width")).toBe("1.6");
    expect(svg.getAttribute("aria-label")).toBe("Status");
    expect(svg.getAttribute("aria-hidden")).toBeNull();
    expect(svg.classList.contains("size-4")).toBe(true);
    await act(async () =>
      svg.dispatchEvent(new MouseEvent("click", { bubbles: true })),
    );
    expect(click).toHaveBeenCalledOnce();
  });

  it("animates through the whole button and retains animation until hover and focus both leave", async () => {
    await act(async () =>
      root.render(
        <Button>
          <Icon />
          Action
        </Button>,
      ),
    );
    const button = container.querySelector("button")!;
    expect(
      button.querySelector(":scope > svg")?.getAttribute("aria-hidden"),
    ).toBe("true");
    expect(controls.start).not.toHaveBeenCalled();
    await act(async () => button.dispatchEvent(new MouseEvent("mouseenter")));
    expect(controls.start).toHaveBeenLastCalledWith("animate");
    await act(async () => button.focus());
    await act(async () => button.dispatchEvent(new MouseEvent("mouseleave")));
    expect(controls.start).toHaveBeenCalledTimes(1);
    await act(async () => button.blur());
    expect(controls.start).toHaveBeenLastCalledWith("normal");
    await act(async () => button.focus());
    expect(controls.start).toHaveBeenLastCalledWith("animate");
  });

  it.each(["disabled", "aria-disabled"])(
    "does not animate a %s control",
    async (attribute) => {
      await act(async () =>
        root.render(
          <Button
            disabled={attribute === "disabled"}
            aria-disabled={attribute === "aria-disabled"}
          >
            <Icon />
          </Button>,
        ),
      );
      const button = container.querySelector("button")!;
      await act(async () => {
        button.dispatchEvent(new MouseEvent("mouseenter"));
        button.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
      });
      expect(controls.start).not.toHaveBeenCalled();
    },
  );

  it("suppresses reduced motion and immediately resets when the preference changes", async () => {
    Object.assign(media, { matches: true });
    await act(async () =>
      root.render(
        <Button>
          <Icon />
        </Button>,
      ),
    );
    const button = container.querySelector("button")!;
    await act(async () => button.dispatchEvent(new MouseEvent("mouseenter")));
    expect(controls.start).not.toHaveBeenCalled();
    await act(async () => {
      Object.assign(media, { matches: false });
      media.dispatchEvent(new Event("change"));
    });
    expect(controls.start).toHaveBeenLastCalledWith("animate");
    await act(async () => {
      Object.assign(media, { matches: true });
      media.dispatchEvent(new Event("change"));
    });
    expect(controls.set).toHaveBeenLastCalledWith("normal");
    expect(controls.stop).toHaveBeenCalled();
  });

  it("does not add hover animation to a loading indicator", async () => {
    await act(async () =>
      root.render(
        <Button>
          <Icon animateOnHover={false} />
        </Button>,
      ),
    );
    const button = container.querySelector("button")!;
    await act(async () => {
      button.dispatchEvent(new MouseEvent("mouseenter"));
      button.focus();
    });
    expect(controls.start).not.toHaveBeenCalled();
  });

  it("cancels later sequence steps and removes listeners on unmount", async () => {
    let complete: () => void = () => {};
    controls.start.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          complete = resolve;
        }),
    );
    await act(async () =>
      root.render(
        <Button>
          <SequenceIcon />
        </Button>,
      ),
    );
    const button = container.querySelector("button")!;
    await act(async () => button.dispatchEvent(new MouseEvent("mouseenter")));
    expect(controls.start).toHaveBeenLastCalledWith("first");
    await act(async () => root.render(null));
    await act(async () => {
      complete();
      button.dispatchEvent(new MouseEvent("mouseenter"));
    });
    expect(controls.start).toHaveBeenCalledTimes(1);
  });
});
