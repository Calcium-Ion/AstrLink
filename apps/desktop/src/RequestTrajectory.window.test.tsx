// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hostMocks = vi.hoisted(() => ({
  invoke: vi.fn(),
  listen: vi.fn(),
  unlisten: vi.fn(),
  windowLabel: { current: "main" },
}));

vi.mock("@tauri-apps/api/core", () => ({ invoke: hostMocks.invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: hostMocks.listen }));
vi.mock("@tauri-apps/api/window", () => ({
  getCurrentWindow: () => ({ label: hostMocks.windowLabel.current }),
}));

import type { CopyFeedback } from "./copy-feedback";
import { RequestTrajectory } from "./RequestTrajectory";
import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";
import {
  useDetachedInspector,
  type DetachedInspector,
} from "./trajectory-inspector-window";

const copyFeedback: CopyFeedback = {
  activeKey: null,
  state: "idle",
  copy: () => {},
};

const record: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-07-25T10:00:00Z",
  completed_at: "2026-07-25T10:00:01Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_primary",
  service_id: "service_9bae092569a028b1f3f38d36",
  local_access_token_id: "token_01",
  http_status: 200,
  latency_ms: 120,
  usage: null,
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
    upstream_request_body_captured: false,
    upstream_response_content_captured: false,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: null,
  ...emptyTrajectoryFields,
  events: [
    {
      kind: "accepted",
      started_at: "2026-07-25T10:00:00Z",
      ended_at: "2026-07-25T10:00:00Z",
      status: "succeeded",
      summary: "gpt-4.1 · openai.responses",
      attempt_index: 0,
    },
    {
      kind: "upstream",
      started_at: "2026-07-25T10:00:00Z",
      ended_at: "2026-07-25T10:00:01Z",
      status: "succeeded",
      summary: "HTTP 200",
      attempt_index: 1,
    },
    {
      kind: "completed",
      started_at: "2026-07-25T10:00:01Z",
      ended_at: "2026-07-25T10:00:01Z",
      status: "succeeded",
      summary: "HTTP 200",
      attempt_index: 1,
    },
  ],
};

/** The phase each call to `command` carried, in order. */
function routedChips(command: string): string[] {
  return hostMocks.invoke.mock.calls
    .filter(([name]) => name === command)
    .map(
      ([, args]) =>
        (args as { selection: { row: { chip: string } } }).selection.row.chip,
    );
}

function invoked(command: string): boolean {
  return hostMocks.invoke.mock.calls.some(([name]) => name === command);
}

describe("RequestTrajectory in a window host", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    hostMocks.windowLabel.current = "main";
    hostMocks.invoke.mockResolvedValue("trajectory-inspector-1");
    hostMocks.listen.mockResolvedValue(hostMocks.unlisten);
    Object.assign(window, {
      __TAURI_INTERNALS__: {},
      __ASTRLINK_DESKTOP_PLATFORM__: "macos",
    });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    delete (window as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
    delete (window as { __ASTRLINK_DESKTOP_PLATFORM__?: string })
      .__ASTRLINK_DESKTOP_PLATFORM__;
  });

  const renderTrajectory = async (
    turns: RequestRecord[] = [record],
    childrenByRoot: Record<string, RequestRecord[]> = {},
  ) => {
    await act(async () => {
      root.render(
        <RequestTrajectory
          auditContent={null}
          auditError={null}
          auditLoading={false}
          childrenByRoot={childrenByRoot}
          copyFeedback={copyFeedback}
          onSelectRequest={() => {}}
          selectedRequestId={record.id}
          turns={turns}
          services={{
            [record.service_id!]: {
              id: record.service_id!,
              name: "Configured gateway",
              kind: "newapi",
            },
          }}
        />,
      );
    });
  };

  const clickRow = async (chip: string) => {
    const row = container.querySelector<HTMLButtonElement>(
      `[data-testid="trajectory-row"][data-chip="${chip}"]`,
    );
    if (!row) throw new Error(`Missing trajectory row: ${chip}`);
    await act(async () => {
      row.click();
    });
  };

  const clickPhase = async (chip: string) => {
    const phase = container.querySelector<HTMLButtonElement>(
      `[data-testid="trajectory-phase"][data-chip="${chip}"]`,
    );
    if (!phase) throw new Error(`Missing trajectory phase: ${chip}`);
    await act(async () => {
      phase.click();
    });
  };

  const inspectRow = async (chip: string) => {
    const opened = routedChips("show_trajectory_inspector").length;
    await clickRow(chip);
    expect(routedChips("show_trajectory_inspector")).toHaveLength(opened);
    const row = container.querySelector<HTMLElement>(
      `[data-testid="trajectory-row"][data-chip="${chip}"]`,
    )!;
    const card = row.closest('[data-testid="trajectory-call-card"]')!;
    const inspect = card.querySelector<HTMLButtonElement>(
      `[data-flow-preview-row-id="${row.getAttribute("data-row-id")}"] button`,
    );
    expect(inspect).not.toBeNull();
    expect(inspect?.textContent).toContain("查看调用");
    await act(async () => inspect!.click());
  };

  const highlightedChips = () =>
    [
      ...container.querySelectorAll(
        '[data-testid="trajectory-row"][data-highlighted="true"]',
      ),
    ].map((row) => row.getAttribute("data-chip"));

  it("leaves the list its full width and keeps no pane beside it", async () => {
    await renderTrajectory();

    expect(
      container.querySelectorAll('[data-testid="trajectory-row"]').length,
    ).toBeGreaterThan(0);
    expect(
      container.querySelector('[data-testid="trajectory-inspector"]'),
    ).toBeNull();
  });

  it("starts compact viewports with a folded timeline while keeping rows selectable across toggles", async () => {
    const media = new EventTarget() as MediaQueryList;
    Object.defineProperties(media, {
      matches: { value: true },
      media: { value: "(max-height: 680px), (max-width: 760px)" },
    });
    vi.spyOn(window, "matchMedia").mockReturnValue(media);
    await renderTrajectory();

    const timeline = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-timeline"]',
    )!;
    const panel = timeline.closest<HTMLElement>(
      '[data-slot="collapsible-content"]',
    )!;
    const toggle = container.querySelector<HTMLButtonElement>(
      'button[data-slot="collapsible-trigger"]',
    )!;
    const rowCount = () =>
      container.querySelectorAll('[data-testid="trajectory-row"]').length;
    const selectedChip = () =>
      container
        .querySelector('[data-testid="trajectory-row"][aria-current="true"]')
        ?.getAttribute("data-chip");
    const clientPhase = timeline.querySelector<HTMLElement>(
      '[data-testid="trajectory-phase"][data-chip="CLIENT"]',
    )!;
    const visibleRect = (left: number, width: number) =>
      panel.getAttribute("data-state") === "open"
        ? new DOMRect(left, 0, width, 12)
        : new DOMRect();
    vi.spyOn(timeline, "getBoundingClientRect").mockImplementation(() =>
      visibleRect(0, 100),
    );
    vi.spyOn(clientPhase, "getBoundingClientRect").mockImplementation(() =>
      visibleRect(140, 10),
    );

    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(panel.getAttribute("data-state")).toBe("closed");
    expect(rowCount()).toBe(3);
    await inspectRow("CLIENT");
    expect(selectedChip()).toBe("CLIENT");
    expect(timeline.scrollLeft).toBe(0);

    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(toggle.getAttribute("aria-controls")).toBe(panel.id);
    expect(panel.getAttribute("data-state")).toBe("open");
    expect(rowCount()).toBe(3);
    expect(selectedChip()).toBe("CLIENT");
    // Selection while hidden must wait for real geometry, then reveal the
    // phase whose right edge is 50 px beyond the reopened strip.
    expect(timeline.scrollLeft).toBe(50);
    await inspectRow("UPSTREAM");

    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(panel.getAttribute("data-state")).toBe("closed");
    expect(rowCount()).toBe(3);
    expect(selectedChip()).toBe("UPSTREAM");
    expect(container.querySelector('[data-testid="trajectory-timeline"]')).toBe(
      timeline,
    );
    await inspectRow("RESULT");
    expect(selectedChip()).toBe("RESULT");
    expect(routedChips("show_trajectory_inspector")).toEqual([
      "CLIENT",
      "UPSTREAM",
      "RESULT",
    ]);
  });

  it("names the provider and full outcome in upstream and retry row accessible names", async () => {
    const fullSummary = "Full upstream diagnostic · HTTP 200";
    const detailedRecord = {
      ...record,
      events: record.events.map((event) =>
        event.kind === "upstream" || event.kind === "completed"
          ? { ...event, summary: fullSummary }
          : event,
      ),
    };
    await renderTrajectory([detailedRecord], {
      [record.id]: [
        {
          ...detailedRecord,
          id: "request_retry",
          parent_request_id: record.id,
          attempt_index: 2,
        },
      ],
    });

    for (const chip of ["UPSTREAM", "RETRY"]) {
      const row = container.querySelector<HTMLElement>(
        `[data-testid="trajectory-row"][data-chip="${chip}"]`,
      )!;
      // The compact list removes repeated diagnostics, but the explicit
      // accessible name retains them along with information hidden on narrow screens.
      expect(row.textContent).not.toContain("Full upstream diagnostic");
      expect(row.getAttribute("aria-label")).toContain("Configured gateway");
      expect(row.getAttribute("aria-label")).toContain(fullSummary);
      expect(row.getAttribute("aria-label")).toContain("耗时 1.0 s");
    }
  });

  it("lists a retry inside the call it retried, in time order, with the root's number and each phase's own duration", async () => {
    const origin = Date.parse(record.started_at);
    const at = (ms: number) => new Date(origin + ms).toISOString();
    const call = (
      id: string,
      start: number,
      upstream: number,
      end: number,
    ): RequestRecord => ({
      ...record,
      id,
      turn_index: 1,
      started_at: at(start),
      completed_at: at(end),
      events: record.events.map((event) => ({
        ...event,
        started_at: at(
          event.kind === "accepted"
            ? start
            : event.kind === "upstream"
              ? upstream
              : end,
        ),
        ended_at: at(event.kind === "accepted" ? start : end),
      })),
    });
    const first = call("request_first", 0, 1600, 2000);
    // The failed attempt happened before the root's final one.
    const retry = {
      ...call("request_retry", 200, 300, 1500),
      parent_request_id: first.id,
    };
    const second = call("request_second", 3000, 3100, 3500);
    await renderTrajectory([first, second], { [first.id]: [retry] });

    const card = (id: string) =>
      container.querySelector<HTMLElement>(
        `[data-testid="trajectory-call-card"][data-request-id="${id}"]`,
      );
    expect(card(retry.id)).toBeNull();
    const stations = (id: string) => [
      ...card(id)!.querySelectorAll<HTMLElement>(
        '[data-testid="trajectory-row"]',
      ),
    ];
    // The root call holds its retry, ordered by when each phase began.
    expect(
      stations(first.id).map((station) => [
        station.getAttribute("data-request-id"),
        station.getAttribute("data-chip"),
        station.getAttribute("data-call-number"),
      ]),
    ).toEqual([
      [first.id, "CLIENT", "1"],
      [retry.id, "CLIENT", "1"],
      [retry.id, "RETRY", "1"],
      [retry.id, "RESULT", "1"],
      [first.id, "UPSTREAM", "1"],
      [first.id, "RESULT", "1"],
    ]);
    expect(
      stations(second.id).map((station) =>
        station.getAttribute("data-call-number"),
      ),
    ).toEqual(["2", "2", "2"]);
    // Each phase is timed on its own interval, never the call's window.
    const durations = (id: string) =>
      stations(id).map(
        (station) =>
          /耗时 ([^·]+)$/.exec(station.getAttribute("aria-label")!)![1],
      );
    expect(durations(first.id)).toEqual([
      "0 ms",
      "0 ms",
      "1.2 s",
      "0 ms",
      "400 ms",
      "0 ms",
    ]);
    expect(durations(second.id)).toEqual(["0 ms", "400 ms", "0 ms"]);
    // The folded route spends its width on the phases that took time.
    expect(
      stations(first.id).map((station) =>
        station.style.getPropertyValue("--journey-grow"),
      ),
    ).toEqual(["0", "0", "1200", "0", "400", "0"]);
    expect(card(first.id)!.querySelector(".thinking-loader")).toBeNull();
    expect(card(first.id)!.querySelector(".journey-retries")?.textContent).toBe(
      "1",
    );
  });

  it("keeps pending timing and animation tied to the observed phase and stops them on completion", async () => {
    vi.useFakeTimers();
    const origin = Date.parse(record.started_at);
    const at = (ms: number) => new Date(origin + ms).toISOString();
    vi.setSystemTime(origin + 1000);
    const pending: RequestRecord = {
      ...record,
      completed_at: null,
      status: "pending",
      http_status: null,
      events: [
        { ...record.events[0], ended_at: at(100) },
        {
          ...record.events[1],
          started_at: at(500),
          ended_at: null,
          status: "pending",
          summary: "Waiting for upstream",
        },
      ],
    };
    await renderTrajectory([pending]);
    const phase = (chip: string) =>
      container.querySelector<HTMLElement>(
        `[data-testid="trajectory-row"][data-chip="${chip}"]`,
      )!;
    const step = (chip: string) => phase(chip).closest("li")!;
    const callDuration = () =>
      container.querySelector(".journey-duration .sr-only")?.textContent;
    expect(phase("UPSTREAM").getAttribute("data-tone")).toBe("pending");
    expect(phase("UPSTREAM").getAttribute("aria-label")).toContain(
      "耗时 500 ms",
    );
    expect(phase("UPSTREAM").querySelector(".thinking-loader")).not.toBeNull();
    expect(phase("UPSTREAM").getAttribute("data-pending")).toBe("true");
    expect(step("UPSTREAM").querySelector(".journey-step-head")).not.toBeNull();
    expect(phase("CLIENT").querySelector(".thinking-loader")).toBeNull();
    expect(phase("CLIENT").hasAttribute("data-pending")).toBe(false);
    expect(callDuration()).toBe("1.0 s");

    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(phase("UPSTREAM").getAttribute("aria-label")).toContain(
      "耗时 1.5 s",
    );
    expect(callDuration()).toBe("2.0 s");
    // A finished phase in the active call keeps its own duration.
    expect(phase("CLIENT").getAttribute("aria-label")).toContain("耗时 100 ms");

    const completed: RequestRecord = {
      ...pending,
      completed_at: at(1800),
      status: "succeeded",
      http_status: 200,
      events: pending.events.map((event) =>
        event.kind === "upstream"
          ? {
              ...event,
              ended_at: at(1800),
              status: "succeeded",
              summary: "HTTP 200",
            }
          : event,
      ),
    };
    await renderTrajectory([completed]);
    expect(phase("UPSTREAM").getAttribute("data-tone")).toBe("ok");
    expect(phase("UPSTREAM").getAttribute("aria-label")).toContain(
      "耗时 1.3 s",
    );
    expect(phase("UPSTREAM").querySelector(".thinking-loader")).toBeNull();
    expect(phase("UPSTREAM").hasAttribute("data-pending")).toBe(false);
    expect(step("UPSTREAM").querySelector(".journey-step-head")).toBeNull();
    const stopped = {
      label: phase("UPSTREAM").getAttribute("aria-label"),
      duration: container.querySelector(".journey-duration")?.textContent,
    };
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(phase("UPSTREAM").getAttribute("aria-label")).toBe(stopped.label);
    expect(container.querySelector(".journey-duration")?.textContent).toBe(
      stopped.duration,
    );

    await renderTrajectory([
      {
        ...completed,
        events: completed.events.map((event) =>
          event.kind === "upstream" ? { ...event, ended_at: null } : event,
        ),
      },
    ]);
    // A missing end timestamp on a settled phase is unknown timing, not an
    // in-flight phase inferred from missing data.
    expect(phase("UPSTREAM").querySelector(".thinking-loader")).toBeNull();
    expect(phase("UPSTREAM").hasAttribute("data-pending")).toBe(false);
    expect(step("UPSTREAM").getAttribute("data-timing")).toBe("unknown");
    expect(phase("UPSTREAM").getAttribute("aria-label")).toContain("耗时 —");
  });

  it.each(["explicit", "echo_id", "fingerprint"] as const)(
    "shows the same continuation icon for %s without changing row navigation",
    async (kind) => {
      await renderTrajectory([
        { ...record, session_link: { kind, value: "previous-request" } },
      ]);
      const row = container.querySelector(
        '[data-testid="trajectory-row"][data-chip="CLIENT"]',
      );
      const mark = row?.querySelector<HTMLElement>(
        '[data-conversation-indicator="continuation"]',
      );
      expect(mark?.getAttribute("aria-label")).toBe("对话延续：会话粘性");
      expect(row?.getAttribute("aria-label")).toContain("对话延续：会话粘性");
      expect(mark?.getAttribute("title")).toBe("");
      expect(mark?.querySelector('[data-animated-icon="link"]')).not.toBeNull();
      expect(row?.textContent).not.toContain("对话");
      expect(row?.querySelector("button")).toBeNull();
      await act(async () => {
        mark!.dispatchEvent(
          new PointerEvent("pointerover", {
            bubbles: true,
            pointerType: "mouse",
          }),
        );
      });
      expect(document.querySelector('[role="tooltip"]')?.textContent).toBe(
        "对话延续：会话粘性",
      );
      await act(async () => {
        mark!.dispatchEvent(
          new PointerEvent("pointerout", {
            bubbles: true,
            pointerType: "mouse",
          }),
        );
      });
      expect(document.querySelector('[role="tooltip"]')).toBeNull();
      await act(async () => {
        mark!.click();
      });
      expect(invoked("show_trajectory_inspector")).toBe(false);
      expect(document.querySelector('[role="tooltip"]')?.textContent).toBe(
        "对话延续：会话粘性",
      );
      await act(async () => {
        mark!.dispatchEvent(
          new PointerEvent("pointerout", {
            bubbles: true,
            pointerType: "mouse",
          }),
        );
      });
      expect(document.querySelector('[role="tooltip"]')).not.toBeNull();
      await act(async () => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });
      expect(document.querySelector('[role="tooltip"]')).toBeNull();
      await inspectRow("CLIENT");
      expect(invoked("show_trajectory_inspector")).toBe(true);

      await renderTrajectory();
      expect(
        container.querySelector("[data-conversation-indicator]"),
      ).toBeNull();
      expect(
        container
          .querySelector('[data-testid="trajectory-row"][data-chip="CLIENT"]')
          ?.getAttribute("aria-label"),
      ).not.toContain("对话延续：会话粘性");
    },
  );

  it("jumps to a timeline phase in the list without opening a window", async () => {
    await renderTrajectory();

    await clickPhase("CLIENT");

    expect(invoked("show_trajectory_inspector")).toBe(false);
    const row = container.querySelector(
      '[data-testid="trajectory-row"][data-chip="CLIENT"]',
    );
    expect(row?.getAttribute("data-selected")).toBe("true");
    expect(highlightedChips()).toEqual(["CLIENT", "UPSTREAM", "RESULT"]);
  });

  it("reveals the actual timeline phase in the call details rather than its first phase", async () => {
    await renderTrajectory();
    const list = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-list"]',
    )!;
    vi.spyOn(list, "clientHeight", "get").mockReturnValue(400);

    for (const chip of ["CLIENT", "UPSTREAM", "RESULT"]) {
      await clickPhase(chip);
      const focused = container.querySelector<HTMLElement>(
        '[data-testid="trajectory-row"][data-selected="true"]',
      );
      expect(focused?.getAttribute("data-chip")).toBe(chip);
      expect(focused?.getAttribute("aria-pressed")).toBe("true");
      expect(
        container
          .querySelector('[data-testid="trajectory-call-card"]')
          ?.getAttribute("data-state"),
      ).toBe("open");
      expect(invoked("show_trajectory_inspector")).toBe(false);
    }
  });

  it("does not return to a revealed open call after the user scrolls and a poll arrives", async () => {
    vi.useFakeTimers();
    await renderTrajectory();
    const list = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-list"]',
    )!;
    vi.spyOn(list, "clientHeight", "get").mockReturnValue(400);
    await clickPhase("UPSTREAM");
    await act(async () => vi.advanceTimersByTimeAsync(1000));

    list.scrollTop = 160;
    await renderTrajectory([{ ...record, latency_ms: 240 }]);
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(list.scrollTop).toBe(160);
  });

  /**
   * Lays the list out as WebKit does: 400 px of viewport over the rows, the
   * offset clamped the moment the rows change, and resize observers told only
   * once layout settles, after the scroll event the clamp raised.
   */
  const layOutList = async () => {
    const observers: { notify: () => void; nodes: Set<Element> }[] = [];
    vi.stubGlobal(
      "ResizeObserver",
      class {
        private readonly nodes = new Set<Element>();
        constructor(notify: () => void) {
          observers.push({ notify, nodes: this.nodes });
        }
        observe(node: Element) {
          this.nodes.add(node);
        }
        unobserve(node: Element) {
          this.nodes.delete(node);
        }
        disconnect() {
          this.nodes.clear();
        }
      },
    );
    await renderTrajectory();
    const list = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-list"]',
    )!;
    let height = 1000;
    let top = 0;
    const clamp = (value: number) => Math.max(0, Math.min(value, height - 400));
    Object.defineProperties(list, {
      clientHeight: { configurable: true, get: () => 400 },
      scrollHeight: { configurable: true, get: () => height },
      scrollTop: {
        configurable: true,
        get: () => top,
        set: (value: number) => {
          top = clamp(value);
        },
      },
    });
    const settle = () =>
      act(async () => {
        for (const { notify, nodes } of observers) if (nodes.size) notify();
      });
    // The session opens at its end once the list has a height.
    await settle();
    return {
      top: () => top,
      moveTo: (next: number) => {
        top = clamp(next);
      },
      resize: (next: number) => {
        height = next;
        top = clamp(top);
      },
      settle,
      scroll: () =>
        act(async () => {
          list.dispatchEvent(new Event("scroll"));
        }),
      wheel: (deltaY: number) =>
        act(async () => {
          list.dispatchEvent(
            new WheelEvent("wheel", { deltaY, bubbles: true }),
          );
        }),
    };
  };

  it("keeps following the end when a folding call pulls the offset up before the next call grows", async () => {
    const list = await layOutList();
    expect(list.top()).toBe(600);

    // The open call folds and the browser clamps; the next call has grown
    // by the time that scroll event arrives, 50 px short of the end.
    list.resize(850);
    list.resize(900);
    await list.scroll();
    expect(list.top()).toBe(450);
    await list.settle();
    expect(list.top()).toBe(500);
  });

  it("leaves a reader who scrolled up in place and follows again once they are back at the end", async () => {
    const list = await layOutList();

    await list.wheel(-120);
    list.moveTo(480);
    await list.scroll();
    list.resize(1100);
    await list.settle();
    expect(list.top()).toBe(480);

    // A wheel down to the end follows, though the list grew before the
    // scroll event arrived.
    await list.wheel(1000);
    list.moveTo(700);
    list.resize(1150);
    await list.scroll();
    await list.settle();
    expect(list.top()).toBe(750);

    // A drag up over rows that kept their length leaves the end too.
    list.moveTo(300);
    await list.scroll();
    list.resize(1200);
    await list.settle();
    expect(list.top()).toBe(300);
  });

  it("opens another call on the phase being read and folds the open call from its line", async () => {
    const earlier = { ...record, turn_index: 1 };
    const latest = {
      ...record,
      id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      turn_index: 1,
      started_at: "2026-07-25T10:01:00Z",
    };
    await renderTrajectory([earlier, latest]);
    const card = (id: string) =>
      container.querySelector<HTMLElement>(
        `[data-testid="trajectory-call-card"][data-request-id="${id}"]`,
      )!;
    const trigger = (id: string) =>
      card(id).querySelector<HTMLButtonElement>(".journey-trigger")!;
    const station = (id: string, chip: string) =>
      card(id).querySelector<HTMLElement>(
        `[data-testid="trajectory-row"][data-chip="${chip}"]`,
      )!;

    // The latest call opens on its result; the earlier one is a single line.
    expect(card(latest.id).getAttribute("data-state")).toBe("open");
    expect(card(earlier.id).getAttribute("data-state")).toBe("closed");
    expect(trigger(earlier.id).getAttribute("aria-expanded")).toBe("false");
    expect(station(latest.id, "RESULT").getAttribute("data-selected")).toBe(
      "true",
    );

    await act(async () => station(latest.id, "UPSTREAM").click());
    await act(async () => trigger(earlier.id).click());
    expect(card(earlier.id).getAttribute("data-state")).toBe("open");
    expect(card(latest.id).getAttribute("data-state")).toBe("closed");
    expect(station(earlier.id, "UPSTREAM").getAttribute("data-selected")).toBe(
      "true",
    );
    expect(station(earlier.id, "UPSTREAM").getAttribute("aria-current")).toBe(
      "true",
    );

    // Its line folds it again without dropping the selection.
    await act(async () => trigger(earlier.id).click());
    expect(card(earlier.id).getAttribute("data-state")).toBe("closed");
    expect(trigger(earlier.id).getAttribute("aria-expanded")).toBe("false");
    expect(station(earlier.id, "UPSTREAM").getAttribute("aria-current")).toBe(
      "true",
    );
    await act(async () => trigger(earlier.id).click());
    expect(card(earlier.id).getAttribute("data-state")).toBe("open");
    expect(invoked("show_trajectory_inspector")).toBe(false);
  });

  it("walks the open call's steps with the arrow keys", async () => {
    await renderTrajectory();
    const selected = () =>
      container.querySelector<HTMLElement>(
        '[data-testid="trajectory-row"][data-selected="true"]',
      );
    expect(selected()?.getAttribute("data-chip")).toBe("RESULT");
    // Only the selected step takes a tab stop.
    expect(
      [
        ...container.querySelectorAll<HTMLElement>(
          '.journey-rail [data-testid="trajectory-row"]',
        ),
      ].map((step) => step.tabIndex),
    ).toEqual([-1, -1, 0]);
    const press = async (key: string) => {
      const target = selected()!;
      await act(async () => {
        target.dispatchEvent(
          new KeyboardEvent("keydown", { key, bubbles: true }),
        );
      });
    };

    await press("ArrowUp");
    expect(selected()?.getAttribute("data-chip")).toBe("UPSTREAM");
    expect(document.activeElement).toBe(selected());
    await press("Home");
    expect(selected()?.getAttribute("data-chip")).toBe("CLIENT");
    await press("ArrowUp");
    expect(selected()?.getAttribute("data-chip")).toBe("CLIENT");
    await press("End");
    expect(selected()?.getAttribute("data-chip")).toBe("RESULT");
    expect(invoked("show_trajectory_inspector")).toBe(false);
  });

  it.each([
    // The call ended inside the list, so what the pick grew follows it in.
    { listHeight: 400, scrolled: 42 },
    // The call already ran past the edge; the pick and its detail fit, so
    // nothing moves under the cursor.
    { listHeight: 300, scrolled: 0 },
  ])(
    "keeps a pick at the end of the list from pushing its call past the edge ($listHeight px)",
    async ({ listHeight, scrolled }) => {
      vi.useFakeTimers();
      await renderTrajectory();
      const list = container.querySelector<HTMLElement>(
        '[data-testid="trajectory-list"]',
      )!;
      let scrollTop = 0;
      Object.defineProperty(list, "scrollTop", {
        configurable: true,
        get: () => scrollTop,
        set: (value: number) => {
          scrollTop = value;
        },
      });
      vi.spyOn(list, "clientHeight", "get").mockReturnValue(listHeight);
      const upstreamOpen = () =>
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="UPSTREAM"][data-selected="true"]',
        ) !== null;
      // Content positions: the call starts at 100 and its upstream step at
      // 220; the step's detail adds 40 px to the step and to the call.
      const at = (top: number, height: number) =>
        new DOMRect(0, top - scrollTop, 800, height);
      vi.spyOn(
        HTMLElement.prototype,
        "getBoundingClientRect",
      ).mockImplementation(function (this: HTMLElement) {
        const grown = upstreamOpen() ? 40 : 0;
        if (this === list) return new DOMRect(0, 0, 800, listHeight);
        if (this.matches("[data-flow-id]")) return at(100, 290 + grown);
        if (
          this.matches("li.journey-step") &&
          this.querySelector('[data-chip="UPSTREAM"]')
        ) {
          return at(220, 28 + grown);
        }
        if (
          this.matches('[data-testid="trajectory-row"][data-chip="UPSTREAM"]')
        ) {
          return at(220, 28);
        }
        return new DOMRect();
      });
      vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(
        function (this: HTMLElement) {
          return this.matches('[data-testid="trajectory-row"]') ? 28 : 0;
        },
      );

      await act(async () =>
        container
          .querySelector<HTMLButtonElement>(
            '[data-testid="trajectory-row"][data-chip="UPSTREAM"]',
          )!
          .click(),
      );
      await act(async () => vi.advanceTimersByTimeAsync(1000));
      expect(upstreamOpen()).toBe(true);
      // 400 px list: the call ended at 390 and grew to 430, so it scrolls
      // until its end sits 12 px above the edge.
      expect(scrollTop).toBe(scrolled);
      expect(invoked("show_trajectory_inspector")).toBe(false);
    },
  );

  it("navigates to the first and latest call without opening an inspector", async () => {
    const latest = {
      ...record,
      id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      started_at: "2026-07-25T10:01:00Z",
    };
    await renderTrajectory([record, latest]);

    for (const [label, requestId] of [
      ["定位首次调用", record.id],
      ["定位最新调用", latest.id],
    ]) {
      const button = container.querySelector<HTMLButtonElement>(
        `button[aria-label="${label}"]`,
      );
      expect(button).not.toBeNull();
      await act(async () => button!.click());
      expect(
        container
          .querySelector('[data-testid="trajectory-row"][aria-current="true"]')
          ?.getAttribute("data-request-id"),
      ).toBe(requestId);
      expect(invoked("show_trajectory_inspector")).toBe(false);
    }
  });

  it("jumps from a lane bar without opening a window", async () => {
    await renderTrajectory();

    const bar = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-call"][data-lane="upstream"]',
    );
    if (!bar) throw new Error("Missing upstream lane bar");
    await act(async () => {
      bar.click();
    });

    expect(invoked("show_trajectory_inspector")).toBe(false);
    expect(
      container
        .querySelector('[data-testid="trajectory-row"][data-chip="UPSTREAM"]')
        ?.getAttribute("data-selected"),
    ).toBe("true");
    expect(highlightedChips()).toEqual(["CLIENT", "UPSTREAM", "RESULT"]);
  });

  it("keeps phase clicks local and inspects the phase shown by each view-call action", async () => {
    await renderTrajectory();

    // Selecting the last row on mount must not pop a window: that would steal
    // focus from the list the operator is reading.
    expect(invoked("show_trajectory_inspector")).toBe(false);

    await inspectRow("CLIENT");
    await inspectRow("UPSTREAM");

    // The payload is the row under the cursor, not the one selected before it.
    // Reading the selection back from state would always be one click behind.
    expect(routedChips("show_trajectory_inspector")).toEqual([
      "CLIENT",
      "UPSTREAM",
    ]);
    expect(
      hostMocks.invoke.mock.calls.find(
        ([command]) => command === "show_trajectory_inspector",
      )?.[1].selection.service,
    ).toEqual({
      id: record.service_id,
      name: "Configured gateway",
      kind: "newapi",
    });
  });

  it("follows a poll that replaced the record without opening a window", async () => {
    await renderTrajectory();
    await inspectRow("UPSTREAM");
    hostMocks.invoke.mockClear();

    // A poll hands down a fresh record for the same request, so the phase list
    // is rebuilt underneath the selection.
    await renderTrajectory([{ ...record, latency_ms: 240 }]);

    expect(routedChips("update_trajectory_inspector")).toContain("UPSTREAM");
    // A poll must never resurrect a window the operator closed, nor reach a
    // pinned one. The host decides that; this side only refuses to ask.
    expect(invoked("show_trajectory_inspector")).toBe(false);
  });

  it("keeps the window on the clicked call when a new turn lands", async () => {
    await renderTrajectory();
    await inspectRow("UPSTREAM");
    hostMocks.invoke.mockClear();

    const latest = {
      ...record,
      id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      started_at: "2026-07-25T10:01:00Z",
    };
    // The detail reload hands down fresh copies of the turns already listed.
    await renderTrajectory([structuredClone(record), latest]);

    // Nothing about the clicked call changed, so the window is left alone.
    expect(invoked("update_trajectory_inspector")).toBe(false);
    const selected = container.querySelector(
      '[data-testid="trajectory-row"][aria-current="true"]',
    );
    expect(selected?.getAttribute("data-request-id")).toBe(record.id);
    expect(selected?.getAttribute("data-chip")).toBe("UPSTREAM");
  });

  it("folds past turns and toggles one from its header without a window", async () => {
    const latest = {
      ...record,
      id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      started_at: "2026-07-25T10:01:00Z",
    };
    await renderTrajectory([record, latest]);

    const header = (requestId: string) =>
      container.querySelector<HTMLButtonElement>(
        `[data-testid="trajectory-row"][data-chip="TURN"][data-request-id="${requestId}"]`,
      )!;
    const phaseCount = (requestId: string) =>
      container.querySelectorAll(
        `[data-testid="trajectory-row"][data-request-id="${requestId}"]:not([data-chip="TURN"])`,
      ).length;

    expect(header(record.id).getAttribute("aria-expanded")).toBe("false");
    expect(header(latest.id).getAttribute("aria-expanded")).toBe("true");
    expect(phaseCount(record.id)).toBe(0);
    expect(phaseCount(latest.id)).toBe(3);

    await act(async () => header(record.id).click());
    expect(header(record.id).getAttribute("aria-expanded")).toBe("true");
    expect(phaseCount(record.id)).toBe(3);
    await act(async () => header(latest.id).click());
    expect(header(latest.id).getAttribute("aria-expanded")).toBe("false");
    expect(phaseCount(latest.id)).toBe(0);
    await act(async () => header(record.id).click());
    expect(phaseCount(record.id)).toBe(0);
    expect(invoked("show_trajectory_inspector")).toBe(false);

    // The timeline still reaches a folded call: it opens the turn to show it.
    const bar = container.querySelector<HTMLElement>(
      `[data-testid="trajectory-call"][data-lane="upstream"][data-request-id="${record.id}"]`,
    );
    if (!bar) throw new Error("Missing upstream lane bar");
    await act(async () => bar.click());
    expect(header(record.id).getAttribute("aria-expanded")).toBe("true");
    expect(
      container
        .querySelector('[data-testid="trajectory-row"][aria-current="true"]')
        ?.getAttribute("data-request-id"),
    ).toBe(record.id);
  });

  it("closes the following windows when the conversation is left", async () => {
    await renderTrajectory();
    await inspectRow("CLIENT");
    hostMocks.invoke.mockClear();

    await act(async () => {
      root.render(null);
    });

    // Pinned windows survive this: the host filters them out.
    expect(invoked("close_trajectory_inspectors")).toBe(true);
  });

  it("keeps the window shut when no row was ever clicked", async () => {
    await renderTrajectory();

    await act(async () => {
      root.render(null);
    });

    expect(invoked("show_trajectory_inspector")).toBe(false);
  });

  it("hands the list a stable handler so a re-render repaints no rows", async () => {
    // The row views are memoized on `onSelect`, which is built from this. A
    // fresh object per render would repaint all 1400 rows of a long trajectory.
    const seen: DetachedInspector[] = [];
    function Harness() {
      seen.push(useDetachedInspector(null));
      return null;
    }

    await act(async () => {
      root.render(<Harness />);
    });
    await act(async () => {
      root.render(<Harness />);
    });

    expect(seen.length).toBeGreaterThan(1);
    expect(new Set(seen).size).toBe(1);
  });

  it("docks the pane over the list when there is no window host", async () => {
    delete (window as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;

    await renderTrajectory();

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector).not.toBeNull();
    // No second window to pin, so the docked pane offers no pin control.
    expect(
      container.querySelector('[data-testid="trajectory-inspector-pin"]'),
    ).toBeNull();
    expect(hostMocks.invoke).not.toHaveBeenCalled();
  });

  it("finds the turns that mention a query and opens the one it lands on", async () => {
    const turn = (id: string, index: number, preview: string) => ({
      ...record,
      id,
      turn_index: index,
      input_preview: preview,
    });
    await renderTrajectory([
      turn("req_t1", 1, "Quote the first line"),
      turn("req_t2", 2, "Summarize the diff"),
      turn("req_t3", 3, "quote it again"),
    ]);
    const header = (id: string) =>
      container.querySelector<HTMLElement>(
        `[data-testid="trajectory-row"][data-row-id="${id}:turn"]`,
      )!;
    const calls = (id: string) =>
      container.querySelectorAll(
        `[data-testid="trajectory-row"][data-request-id="${id}"]:not([data-chip="TURN"])`,
      ).length;
    const count = () =>
      container.querySelector('[data-testid="find-count"]')?.textContent;
    // Only the latest turn starts open.
    expect(calls("req_t1")).toBe(0);
    expect(calls("req_t3")).toBeGreaterThan(0);

    // The search box is always in the list header.
    const input = container.querySelector<HTMLInputElement>(
      '[data-testid="find-input"]',
    )!;
    expect(input).not.toBeNull();
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "QUOTE");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });

    // Two turns mention it; find lands on the first and opens it.
    expect(count()).toBe("1/2");
    expect(header("req_t1").getAttribute("data-find-stop")).toBe("active");
    expect(header("req_t3").getAttribute("data-find-stop")).toBe("stop");
    expect(header("req_t2").hasAttribute("data-find-stop")).toBe(false);
    expect(calls("req_t1")).toBeGreaterThan(0);
    expect(
      header("req_t1").querySelector('[data-testid="find-mark"]')?.textContent,
    ).toBe("Quote");

    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });

    // The next stop takes over, and the turn find opened folds back.
    expect(count()).toBe("2/2");
    expect(header("req_t3").getAttribute("data-find-stop")).toBe("active");
    expect(calls("req_t1")).toBe(0);
    // Finding never opens the inspector on its own.
    expect(invoked("show_trajectory_inspector")).toBe(false);

    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    // Escape clears the search and every mark it left.
    expect(input.value).toBe("");
    expect(container.querySelector("[data-find-stop]")).toBeNull();
    expect(container.querySelector('[data-testid="find-mark"]')).toBeNull();
  });

  it("opens a matched call and shows the matching phase detail within a folded turn", async () => {
    const matched: RequestRecord = {
      ...record,
      id: "req_search_match",
      turn_index: 1,
      input_preview: "First task",
      events: record.events.map((event) =>
        event.kind === "upstream"
          ? { ...event, summary: "needle: vendor handshake" }
          : event,
      ),
    };
    const sibling: RequestRecord = {
      ...record,
      id: "req_search_sibling",
      turn_index: 1,
    };
    const latest: RequestRecord = {
      ...record,
      id: "req_search_latest",
      turn_index: 2,
    };
    await renderTrajectory([matched, sibling, latest]);
    const list = container.querySelector<HTMLElement>(
      '[data-testid="trajectory-list"]',
    )!;
    vi.spyOn(list, "clientHeight", "get").mockReturnValue(400);
    const card = () =>
      container.querySelector<HTMLElement>(
        `[data-testid="trajectory-call-card"][data-request-id="${matched.id}"]`,
      );
    expect(card()).toBeNull();
    const input = container.querySelector<HTMLInputElement>(
      '[data-testid="find-input"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "needle");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });

    expect(
      container.querySelector('[data-testid="find-count"]')?.textContent,
    ).toBe("1/1");
    expect(card()?.getAttribute("data-state")).toBe("open");
    expect(
      card()
        ?.querySelector('[data-testid="trajectory-row"][data-selected="true"]')
        ?.getAttribute("data-chip"),
    ).toBe("UPSTREAM");
    expect(
      card()?.querySelector(
        '[data-testid="trajectory-row"][data-selected="true"] [data-testid="find-mark"]',
      )?.textContent,
    ).toBe("needle");
    expect(invoked("show_trajectory_inspector")).toBe(false);
  });
});
