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
    delete (window as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
    delete (window as { __ASTRLINK_DESKTOP_PLATFORM__?: string })
      .__ASTRLINK_DESKTOP_PLATFORM__;
  });

  const renderTrajectory = async (turns: RequestRecord[] = [record]) => {
    await act(async () => {
      root.render(
        <RequestTrajectory
          auditContent={null}
          auditError={null}
          auditLoading={false}
          childrenByRoot={{}}
          copyFeedback={copyFeedback}
          onSelectRequest={() => {}}
          selectedRequestId={record.id}
          turns={turns}
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
      const button = container.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
      expect(button).not.toBeNull();
      await act(async () => button!.click());
      expect(container.querySelector('[data-testid="trajectory-row"][aria-current="true"]')?.getAttribute("data-request-id")).toBe(requestId);
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

  it("routes each click to the window host carrying the row just clicked", async () => {
    await renderTrajectory();

    // Selecting the last row on mount must not pop a window: that would steal
    // focus from the list the operator is reading.
    expect(invoked("show_trajectory_inspector")).toBe(false);

    await clickRow("CLIENT");
    await clickRow("UPSTREAM");

    // The payload is the row under the cursor, not the one selected before it.
    // Reading the selection back from state would always be one click behind.
    expect(routedChips("show_trajectory_inspector")).toEqual([
      "CLIENT",
      "UPSTREAM",
    ]);
  });

  it("follows a poll that replaced the record without opening a window", async () => {
    await renderTrajectory();
    await clickRow("UPSTREAM");
    hostMocks.invoke.mockClear();

    // A poll hands down a fresh record for the same request, so the phase list
    // is rebuilt underneath the selection.
    await renderTrajectory([{ ...record, latency_ms: 240 }]);

    expect(routedChips("update_trajectory_inspector")).toContain("UPSTREAM");
    // A poll must never resurrect a window the operator closed, nor reach a
    // pinned one. The host decides that; this side only refuses to ask.
    expect(invoked("show_trajectory_inspector")).toBe(false);
  });

  it("closes the following windows when the conversation is left", async () => {
    await renderTrajectory();
    await clickRow("CLIENT");
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
});
