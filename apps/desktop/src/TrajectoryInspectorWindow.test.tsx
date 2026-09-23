// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hostMocks = vi.hoisted(() => ({
  invoke: vi.fn(),
  listen: vi.fn(),
  unlisten: vi.fn(),
}));

vi.mock("@tauri-apps/api/core", () => ({ invoke: hostMocks.invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: hostMocks.listen }));
vi.mock("@tauri-apps/api/window", () => ({
  getCurrentWindow: () => ({ label: "trajectory-inspector-2" }),
}));

const bridgeMocks = vi.hoisted(() => ({
  getRequestAuditContent: vi.fn(),
}));
vi.mock("./bridge", () => bridgeMocks);

import type { AuditContent, RequestRecord } from "./request-record-model";
import { emptyTrajectoryFields } from "./request-record-model";
import type { TrajectoryRow } from "./request-trajectory-model";
import { TrajectoryInspectorWindow } from "./TrajectoryInspectorWindow";
import {
  detachedInspectorEnabled,
  isTrajectoryInspectorWindow,
  type TrajectoryInspectorSelection,
  type TrajectoryInspectorWindowState,
} from "./trajectory-inspector-window";

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
    upstream_response_content_captured: true,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: null,
  ...emptyTrajectoryFields,
};

const row: TrajectoryRow = {
  id: `${record.id}:upstream`,
  requestId: record.id,
  chip: "UPSTREAM",
  summary: "HTTP 200",
  result: "成功",
  status: "succeeded",
  tone: "ok",
  startedAt: record.started_at,
  endedAt: record.completed_at,
  lane: "upstream",
  child: false,
  turnIndex: 1,
};

const laterRow: TrajectoryRow = {
  ...row,
  id: `${record.id}:accepted`,
  chip: "CLIENT",
  summary: "gpt-4.1 · openai.responses",
  lane: "client",
};

const auditContent: AuditContent = {
  request_id: record.id,
  http_meta: null,
  request_body: null,
  response_content: null,
  upstream_http_meta: null,
  upstream_request_body: null,
  upstream_response_content: {
    media_type: "application/json",
    content: '{"ok":true}',
    truncated: false,
    captured_bytes: 11,
  },
};

/** What the host reports when this window pulls its state on mount. */
const hostState: { current: TrajectoryInspectorWindowState } = {
  current: { selection: null, pinned: false },
};

function pushSelection(selection: TrajectoryInspectorSelection): void {
  const call = hostMocks.listen.mock.calls.find(
    ([name]) => name === "trajectory-inspector:select",
  );
  if (!call) throw new Error("The inspector window never subscribed");
  (call[1] as (event: { payload: TrajectoryInspectorSelection }) => void)({
    payload: selection,
  });
}

function inspector(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(
    '[data-testid="trajectory-inspector"]',
  );
}

function pinButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>(
    '[data-testid="trajectory-inspector-pin"]',
  );
  if (!button) throw new Error("The inspector window has no pin control");
  return button;
}

describe("TrajectoryInspectorWindow", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    hostState.current = { selection: null, pinned: false };
    hostMocks.listen.mockResolvedValue(hostMocks.unlisten);
    hostMocks.invoke.mockImplementation(
      async (command: string, args?: Record<string, unknown>) => {
        if (command === "trajectory_inspector_state") return hostState.current;
        if (command === "set_trajectory_inspector_pinned") return args?.pinned;
        return undefined;
      },
    );
    bridgeMocks.getRequestAuditContent.mockResolvedValue(auditContent);
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

  const flush = async () => {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  };

  const render = async () => {
    await act(async () => {
      root.render(<TrajectoryInspectorWindow />);
    });
    await flush();
  };

  const clickPin = async () => {
    await act(async () => {
      pinButton(container).click();
    });
    await flush();
  };

  it("never asks itself to open another inspector window", () => {
    expect(isTrajectoryInspectorWindow()).toBe(true);
    expect(detachedInspectorEnabled()).toBe(false);
  });

  it("waits for a selection before it shows a call", async () => {
    await render();

    expect(inspector(container)).toBeNull();
    expect(container.textContent).toContain("尚未选择链路");
    // Pulled rather than announced: subscribing first and then asking leaves no
    // window in which the host has already sent the phase to nobody.
    expect(hostMocks.listen).toHaveBeenCalled();
    expect(hostMocks.invoke).toHaveBeenCalledWith("trajectory_inspector_state");
  });

  it("restores the phase the host had already stored for it", async () => {
    // How a pinned window comes back after the dev host reloads every webview:
    // its React state is gone but the host still knows what it froze on.
    hostState.current = { selection: { row, record }, pinned: true };

    await render();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );
    expect(inspector(container)?.getAttribute("data-request-id")).toBe(
      record.id,
    );
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("true");
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("true");
  });

  it("shows the pushed call and decrypts its own audit content", async () => {
    await render();

    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );
    expect(inspector(container)?.getAttribute("data-request-id")).toBe(
      record.id,
    );
    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="inspector-tab"]',
        ),
      ].map((tab) => tab.getAttribute("data-chip")),
    ).toEqual(["CLIENT", "ROUTE", "UPSTREAM", "RESULT"]);
    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-tab"][data-chip="UPSTREAM"]')
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("UPSTREAM");
    expect(inspector(container)?.textContent).toContain("上游响应");
    expect(inspector(container)?.textContent).not.toContain("客户端请求体");
    expect(
      inspector(container)?.querySelector('[data-testid="inspector-http"]')
        ?.textContent,
    ).toBe("HTTP 200");
    // The body is fetched here rather than forwarded, so captured text never
    // crosses the event channel.
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledWith(record.id);
    expect(inspector(container)?.textContent).toContain('"ok": true');
  });

  it("freezes on its call once pinned and thaws when unpinned", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    await clickPin();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_pinned",
      { pinned: true },
    );
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("true");

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();

    // The host stops routing here, and a stray push is refused anyway, so the
    // two sides cannot disagree about what a pinned window shows.
    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );

    await clickPin();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_pinned",
      { pinned: false },
    );

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "CLIENT",
    );
  });

  it("puts the pin back when the host refuses to float the window", async () => {
    hostMocks.invoke.mockImplementation(async (command: string) => {
      if (command === "trajectory_inspector_state") return hostState.current;
      throw new Error("the window level cannot be changed");
    });
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    await clickPin();

    // A button left reading "pinned" over a window that still follows the list
    // is worse than one that admits the pin did not take.
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("false");
  });

  it("lists every provider the call tried under one route tab, by name", async () => {
    const serviceA = "service_aaaaaaaaaaaaaaaaaaaaaaaa";
    const serviceB = "service_bbbbbbbbbbbbbbbbbbbbbbbb";
    const at = (second: number) => `2026-07-25T10:00:0${second}Z`;
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      service_id: serviceA,
      http_status: null,
      error: {
        category: "upstream",
        code: "upstream_unavailable",
        message: "unexpected EOF",
        retryable: true,
      },
      events: [
        { kind: "accepted", started_at: at(0), ended_at: at(0), status: "succeeded", summary: "gpt-4.1 · openai.responses", attempt_index: 0 },
        { kind: "routed", started_at: at(1), ended_at: at(1), status: "succeeded", summary: `native · ${serviceA}`, attempt_index: 1 },
        { kind: "upstream", started_at: at(1), ended_at: at(2), status: "failed", summary: "upstream_unavailable", attempt_index: 1 },
        { kind: "routed", started_at: at(2), ended_at: at(2), status: "failed", summary: `${serviceB} · credential_unavailable`, attempt_index: 1 },
        { kind: "completed", started_at: at(3), ended_at: at(3), status: "failed", summary: "upstream_unavailable", attempt_index: 1 },
      ],
    };
    await render();

    await act(async () => {
      pushSelection({
        row: { ...row, id: `${failed.id}:routed`, chip: "ROUTE", lane: "gateway" },
        record: failed,
        services: {
          [serviceA]: { id: serviceA, name: "Primary" },
          [serviceB]: { id: serviceB, name: "Backup" },
        },
      });
    });
    await flush();

    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="inspector-tab"]',
        ),
      ].map((tab) => tab.getAttribute("data-chip")),
    ).toEqual(["CLIENT", "ROUTE", "UPSTREAM", "RESULT"]);
    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="route-attempts"] li',
        ),
      ].map((item) => [item.textContent, item.getAttribute("data-tone")]),
    ).toEqual([
      ["native · Primary", "ok"],
      ["Backup · credential_unavailable", "failed"],
    ]);
    expect(inspector(container)?.textContent).toContain("尝试过的 API 提供商");
  });

  it("has no close button of its own, because the window frame owns that", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });

    expect(
      container.querySelector('[data-testid="trajectory-inspector-close"]'),
    ).toBeNull();
  });

  it("refetches audit when a pending record's captured flags flip", async () => {
    await render();
    const pendingRecord: RequestRecord = {
      ...record,
      status: "pending",
      completed_at: null,
      http_status: null,
      latency_ms: null,
      audit: {
        ...record.audit,
        request_body_captured: false,
        upstream_response_content_captured: false,
      },
    };
    const clientRow: TrajectoryRow = {
      ...laterRow,
      status: "pending",
      tone: "pending",
      endedAt: null,
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: record.id,
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });

    await act(async () => {
      pushSelection({ row: clientRow, record: pendingRecord });
    });
    await flush();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
    expect(
      inspector(container)?.querySelector(
        '[data-testid="inspector-missing-body"]',
      )?.textContent,
    ).toContain("进行中");

    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: record.id,
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"input":"live"}',
        truncated: false,
        captured_bytes: 16,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await act(async () => {
      pushSelection({
        row: clientRow,
        record: {
          ...pendingRecord,
          audit: { ...pendingRecord.audit, request_body_captured: true },
        },
      });
    });
    await flush();

    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    expect(inspector(container)?.textContent).toContain("live");
  });

  it("reports a broken audit key instead of the raw transport error", async () => {
    bridgeMocks.getRequestAuditContent.mockRejectedValue(
      new Error("control API returned 409"),
    );
    await render();

    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "审计密钥",
    );
  });
});
