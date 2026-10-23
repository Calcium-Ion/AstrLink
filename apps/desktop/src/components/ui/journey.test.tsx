// @vitest-environment happy-dom

import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  JourneyCall,
  JourneyEmblem,
  JourneyRail,
  JourneySparkline,
  JourneySparkStop,
  JourneyStack,
  JourneyStep,
  RollingText,
} from "./journey";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

function render(node: ReactNode) {
  act(() => root.render(node));
}

describe("JourneyCall", () => {
  it("lifts only while open and replays the jump glow when its key changes", () => {
    const call = (open: boolean, flashKey: number | null) => (
      <JourneyCall open={open} flashKey={flashKey} running={open}>
        <span>call</span>
      </JourneyCall>
    );
    render(call(false, null));
    const node = container.querySelector<HTMLElement>(".journey-call")!;
    expect(node.getAttribute("data-state")).toBe("closed");
    expect(node.hasAttribute("data-running")).toBe(false);
    expect(node.querySelector(".journey-flash")).toBeNull();

    render(call(true, 1));
    expect(node.getAttribute("data-state")).toBe("open");
    expect(node.getAttribute("data-running")).toBe("true");
    const glow = node.querySelector(".journey-flash");
    expect(glow?.getAttribute("aria-hidden")).toBe("true");

    // The same key keeps the glow that already played.
    render(call(true, 1));
    expect(node.querySelector(".journey-flash")).toBe(glow);
    render(call(true, 2));
    expect(node.querySelector(".journey-flash")).not.toBe(glow);
  });

  it("delays an entrance only while it is entering", () => {
    render(
      <JourneyCall open={false} entering enterDelayMs={120}>
        <span />
      </JourneyCall>,
    );
    const node = container.querySelector<HTMLElement>(".journey-call")!;
    expect(node.getAttribute("data-entering")).toBe("true");
    expect(node.style.getPropertyValue("--journey-delay")).toBe("120ms");
    render(
      <JourneyCall open={false}>
        <span />
      </JourneyCall>,
    );
    expect(node.hasAttribute("data-entering")).toBe(false);
  });
});

describe("JourneyEmblem", () => {
  it("keeps a single hidden mark through status changes", () => {
    render(<JourneyEmblem status="pending" />);
    const emblem = container.querySelector(".journey-emblem")!;
    expect(emblem.getAttribute("aria-hidden")).toBe("true");
    expect(emblem.getAttribute("data-status")).toBe("pending");
    render(<JourneyEmblem status="ok" />);
    expect(emblem.getAttribute("data-status")).toBe("ok");
    expect(emblem.querySelector(".journey-check")).not.toBeNull();
  });

  it("draws incomplete data as unknown rather than as an outcome", () => {
    render(<JourneyEmblem status="unknown" />);
    const emblem = container.querySelector(".journey-emblem")!;
    expect(emblem.querySelector(".journey-check")).toBeNull();
    expect(
      emblem.querySelector('[data-animated-icon="circle-help"]'),
    ).not.toBeNull();
  });
});

describe("RollingText", () => {
  const rolled = () =>
    [...container.querySelectorAll(".journey-rolling-char[data-roll]")].map(
      (node) => node.textContent,
    );

  it("rolls only the characters that changed and reads as one string", () => {
    render(<RollingText text="9.8 s" />);
    expect(rolled()).toEqual([]);
    expect(container.querySelector(".sr-only")?.textContent).toBe("9.8 s");
    expect(
      [...container.querySelectorAll(".journey-rolling-char")].every(
        (node) => node.getAttribute("aria-hidden") === "true",
      ),
    ).toBe(true);

    render(<RollingText text="9.9 s" />);
    expect(rolled()).toEqual(["9"]);
    // A re-render of the same text keeps the roll it started.
    render(<RollingText text="9.9 s" />);
    expect(rolled()).toEqual(["9"]);

    // Keys run from the right, so the unit keeps its place as digits grow.
    const unit = container.querySelector(".journey-rolling-char:last-child");
    render(<RollingText text="10.0 s" />);
    expect(rolled()).toEqual(["1", "0", "0"]);
    expect(container.querySelector(".journey-rolling-char:last-child")).toBe(
      unit,
    );
  });
});

describe("JourneySparkline", () => {
  it("rises between lanes only where the route changes lane", () => {
    render(
      <JourneySparkline share={0.5}>
        <JourneySparkStop lane={0} fromLane={null} grow={16} aria-label="a" />
        <JourneySparkStop lane={1} fromLane={0} grow={300} aria-label="b" />
        <JourneySparkStop
          lane={1}
          fromLane={1}
          grow={0}
          unknown
          aria-label="c"
        />
        <JourneySparkStop
          lane={2}
          fromLane={1}
          grow={1200}
          pending
          mark
          markClassName="bg-primary"
          aria-label="d"
        />
      </JourneySparkline>,
    );
    const line = container.querySelector<HTMLElement>(".journey-spark")!;
    expect(line.style.getPropertyValue("--journey-share")).toBe("0.5");
    const stops = [
      ...container.querySelectorAll<HTMLButtonElement>(".journey-spark-stop"),
    ];
    expect(
      stops.map((stop) => !!stop.querySelector(".journey-spark-rise")),
    ).toEqual([false, true, false, true]);
    expect(
      stops.map((stop) => stop.style.getPropertyValue("--journey-grow")),
    ).toEqual(["16", "300", "0", "1200"]);
    expect(stops[2]!.getAttribute("data-timing")).toBe("unknown");
    expect(stops[3]!.getAttribute("data-pending")).toBe("true");
    expect(
      stops[3]!.querySelector(".journey-spark-dot.bg-primary"),
    ).not.toBeNull();
    expect(stops[3]!.querySelector(".journey-spark-head")).not.toBeNull();
    expect(stops[0]!.querySelector(".journey-spark-dot")).toBeNull();
    expect(stops.every((stop) => stop.type === "button")).toBe(true);
  });
});

describe("JourneyStep", () => {
  const step = (selected: boolean, onClick = () => {}) => (
    <JourneyStep
      lane={2}
      fromLane={1}
      toLane={1}
      index={3}
      dwell={0.5}
      wait="upstream"
      selected={selected}
      selectionId="selection"
      markerClassName="bg-warning"
      aria-label="Upstream"
      onClick={onClick}
      detail={selected ? <button type="button">Inspect</button> : null}
    >
      <span>Upstream</span>
    </JourneyStep>
  );

  it("carries its node inside its button and its detail beside it", () => {
    const onClick = vi.fn();
    render(<JourneyRail>{step(true, onClick)}</JourneyRail>);
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Upstream"]',
    )!;
    expect(
      button.querySelector(".journey-step-node.bg-warning"),
    ).not.toBeNull();
    expect(
      button.querySelector(".journey-step-in path")?.getAttribute("d"),
    ).toBe("M34 0C34 7 48 7 48 14");
    // The detail holds its own actions, so it is never inside the button.
    const detail = container.querySelector(".journey-step-detail")!;
    expect(button.contains(detail)).toBe(false);
    expect(detail.querySelector("button")?.textContent).toBe("Inspect");
    const item = button.closest("li")!;
    expect(item.getAttribute("data-wait")).toBe("upstream");
    expect(item.style.getPropertyValue("--journey-dwell")).toBe("0.5");
    expect(item.querySelector(".journey-step-surface")).not.toBeNull();
    act(() => button.click());
    expect(onClick).toHaveBeenCalledTimes(1);

    render(<JourneyRail>{step(false)}</JourneyRail>);
    expect(container.querySelector(".journey-step-surface")).toBeNull();
    expect(container.querySelector(".journey-step-detail")).toBeNull();
  });

  it("hangs the first step from above and leaves the last step open below", () => {
    render(
      <JourneyRail animate>
        <JourneyStep
          lane={0}
          fromLane={null}
          toLane={null}
          index={0}
          selectionId="selection"
          pending
          markerKind="live"
        >
          <span>Client</span>
        </JourneyStep>
      </JourneyRail>,
    );
    const item = container.querySelector("li")!;
    expect(item.querySelector(".journey-step-lead")).not.toBeNull();
    expect(item.querySelector(".journey-step-in")).toBeNull();
    // A step still running keeps its rail growing below it.
    expect(item.querySelector(".journey-step-out")).not.toBeNull();
    expect(item.querySelector(".journey-step-head")).not.toBeNull();
    expect(
      container.querySelector(".journey-body")?.getAttribute("data-animate"),
    ).toBe("true");
  });

  it("keeps one selection surface without motion when motion is reduced", () => {
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query: string) =>
        ({
          matches: query.includes("prefers-reduced-motion"),
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
          addListener: () => {},
          removeListener: () => {},
          onchange: null,
          dispatchEvent: () => false,
        }) as MediaQueryList,
    );
    render(<JourneyRail>{step(true)}</JourneyRail>);
    expect(container.querySelectorAll(".journey-step-surface")).toHaveLength(1);
    render(<JourneyRail>{step(false)}</JourneyRail>);
    expect(container.querySelectorAll(".journey-step-surface")).toHaveLength(0);
  });
});

describe("JourneyStack", () => {
  it("shows up to six outcomes and counts the rest", () => {
    render(
      <JourneyStack
        marks={["ok", "failed", "ok", "pending", "ok", "blocked", "ok", "ok"]}
      />,
    );
    const marks = [...container.querySelectorAll(".journey-stack-mark")];
    expect(marks.map((mark) => mark.getAttribute("data-status"))).toEqual([
      "ok",
      "failed",
      "ok",
      "pending",
      "ok",
      "blocked",
    ]);
    expect(container.querySelector(".journey-stack-more")?.textContent).toBe(
      "+2",
    );
    expect(
      container.querySelector(".journey-stack")?.getAttribute("aria-hidden"),
    ).toBe("true");
  });
});
