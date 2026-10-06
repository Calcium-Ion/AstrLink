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
  getCurrentWindow: () => ({
    label: "trajectory-inspector-2",
    isFocused: async () => true,
    isFullscreen: async () => false,
    isMaximized: async () => false,
    onFocusChanged: async () => () => undefined,
    onResized: async () => () => undefined,
  }),
}));

const bridgeMocks = vi.hoisted(() => ({
  getRequestAuditContent: vi.fn(),
  getRawSealingStatus: vi.fn(),
  listenRawSealingChanged: vi.fn(),
}));
vi.mock("./bridge", () => bridgeMocks);

import type { RawSealingState } from "./raw-sealing-model";
import type { AuditContent, RequestRecord } from "./request-record-model";
import { emptyTrajectoryFields } from "./request-record-model";
import type { TrajectoryRow } from "./request-trajectory-model";
import { TrajectoryInspectorWindow } from "./TrajectoryInspectorWindow";
import { TRAJECTORY_INSPECTOR_TOUR_KEY } from "./TrajectoryInspectorTour";
import { findShortcutLabel } from "./find-model";
import { WindowChrome, WindowChromeProvider } from "./WindowChrome";
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
  view: "full",
  withheld: {},
  privacy_findings: [],
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

function sealing(
  unlocked: boolean,
  unlockExpiresAt: string | null = null,
): RawSealingState {
  return {
    raw_available: true,
    configured: true,
    password_set: true,
    password_required: false,
    envelopes: ["password"],
    key_verified: true,
    unlocked,
    unlock_expires_at: unlockExpiresAt,
    unlock_idle_seconds: 900,
    retry_after_seconds: 0,
    password_min_length: 8,
    password_max_length: 128,
    key_replaced: false,
  };
}

const rawContent: AuditContent = {
  ...auditContent,
  upstream_response_content: {
    media_type: "application/json",
    content: '{"secret":"raw-only"}',
    truncated: false,
    captured_bytes: 21,
    exposure: "raw",
  },
};

const lockedContent: AuditContent = {
  ...auditContent,
  upstream_response_content: null,
  withheld: {
    upstream_response_content: {
      reason: "raw_locked",
      raw_available: false,
      media_type: "application/json",
      truncated: false,
      captured_bytes: 21,
    },
  },
};

/** Tells the window, as the host does, that another window changed the unlock. */
async function announceSealingChange(): Promise<void> {
  const call = bridgeMocks.listenRawSealingChanged.mock.calls.at(-1);
  if (!call) throw new Error("The inspector window never watched the unlock");
  await act(async () => (call[0] as () => void)());
}

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
    // Most tests represent returning users who already took the tour.
    localStorage.setItem(TRAJECTORY_INSPECTOR_TOUR_KEY, "seen");
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
    bridgeMocks.getRawSealingStatus.mockResolvedValue({
      ...sealing(false),
      configured: false,
    });
    bridgeMocks.listenRawSealingChanged.mockResolvedValue(() => undefined);
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
    localStorage.removeItem(TRAJECTORY_INSPECTOR_TOUR_KEY);
    vi.useRealTimers();
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
      // The pin lives in the title bar, so the window renders with its chrome.
      root.render(
        <WindowChromeProvider>
          <WindowChrome platform="macos" />
          <TrajectoryInspectorWindow />
        </WindowChromeProvider>,
      );
    });
    await flush();
  };

  const clickPin = async () => {
    await act(async () => {
      pinButton(container).click();
    });
    await flush();
  };

  it("marks an identical upstream response and preserves stream failure with HTTP 200", async () => {
    const content =
      'data: {"type":"response.output_text.delta","delta":"Client reply"}\n\n';
    const part = {
      content,
      media_type: "text/event-stream",
      captured_bytes: content.length,
      truncated: false,
    };
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      error: {
        category: "upstream",
        code: "upstream_stream_interrupted",
        message: "Stream ended early",
        retryable: true,
      },
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      response_content: part,
      upstream_response_content: part,
    });
    hostState.current = {
      selection: { record: failed, row: { ...row, chip: "RESULT" } },
      pinned: false,
    };
    await render();
    const preview = container.querySelector(
      '[data-testid="audit-result-preview"]',
    );
    expect(preview?.textContent).toContain("Client reply");
    expect(
      container.querySelector('[data-testid="inspector-http"]')?.textContent,
    ).toBe("HTTP 200");
    // Without a provider error the gateway's code leads the one reason card.
    const diagnosis = preview?.querySelector(
      '[data-testid="inspector-diagnosis"]',
    );
    expect(diagnosis?.textContent).toContain("upstream_stream_interrupted");
    expect(diagnosis?.textContent).toContain("可重试");
    expect(diagnosis?.textContent).toContain("Stream ended early");
    expect(diagnosis?.textContent).toContain("不代表流式输出成功完成");
    expect(
      container.querySelectorAll('[data-testid="inspector-diagnosis"]'),
    ).toHaveLength(1);
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="UPSTREAM"]',
        )!
        .click();
    });
    await flush();
    expect(
      container.querySelector('[data-testid="inspector-same-response"]')
        ?.textContent,
    ).toContain("与客户端响应一致");
    expect(
      container.querySelector('[data-testid="audit-result-preview"]'),
    ).toBeNull();
    const body = container.querySelector(
      '[data-testid="inspector-upstream-body"]',
    );
    expect(body?.getAttribute("data-view")).toBe("response");
    // Stream bodies open as events; the original stays one toggle away.
    expect(body?.textContent).toContain("response.output_text.delta");
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
  });

  it("leads with the provider's error and keeps the gateway verdict as context", async () => {
    const content = [
      'data: {"type":"response.created","response":{"status":"in_progress"}}',
      'data: {"type":"response.failed","response":{"status":"failed","error":{"code":"rate_limit_exceeded","message":"Token rate limit exceeded"}}}',
      "",
    ].join("\n\n");
    const part = {
      content,
      media_type: "text/event-stream",
      captured_bytes: content.length,
      truncated: false,
    };
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      error: {
        category: "upstream",
        code: "upstream_stream_interrupted",
        message: "Stream ended early",
        retryable: true,
      },
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      upstream_http_meta: {
        method: "POST",
        url: "https://api.example.test/v1/responses",
        http_version: "HTTP/2",
        request_headers: [],
        response_status: 200,
        response_headers: [],
      },
      upstream_request_body: {
        content: '{"model":"gpt-4.1"}',
        media_type: "application/json",
        captured_bytes: 19,
        truncated: false,
      },
      response_content: part,
      upstream_response_content: part,
    });
    hostState.current = {
      selection: { record: failed, row: { ...row, chip: "RESULT" } },
      pinned: false,
    };
    await render();
    const diagnosis = container.querySelector(
      '[data-testid="inspector-diagnosis"]',
    );
    expect(diagnosis?.firstElementChild?.textContent).toBe(
      "rate_limit_exceeded",
    );
    expect(diagnosis?.textContent).toContain("Token rate limit exceeded");
    const verdict = diagnosis?.querySelector(
      '[data-testid="inspector-gateway-verdict"]',
    );
    expect(verdict?.textContent).toContain("upstream_stream_interrupted");
    expect(verdict?.textContent).toContain("可重试");
    // The HTTP note already explains an interrupted stream in the UI language.
    expect(verdict?.textContent).not.toContain("Stream ended early");
    expect(verdict?.getAttribute("title")).toBe("Stream ended early");
    // The reason replaces the empty-state card instead of sitting above it.
    expect(
      container.querySelector('[data-testid="audit-no-output"]')?.textContent,
    ).toBe("请求在产生输出前失败");
    expect(container.textContent).not.toContain("未捕获到回复或工具调用");

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="UPSTREAM"]',
        )!
        .click();
    });
    await flush();
    expect(
      container.querySelector('[data-testid="inspector-upstream-endpoint"]')
        ?.textContent,
    ).toBe("POST https://api.example.test/v1/responses");
    const body = () =>
      container.querySelector<HTMLElement>(
        '[data-testid="inspector-upstream-body"]',
      )!;
    expect(
      body().querySelector('[data-testid="inspector-diagnosis"]')?.textContent,
    ).toContain("rate_limit_exceeded");
    const view = (label: string) =>
      [
        ...container.querySelectorAll<HTMLButtonElement>(
          '[aria-label="上游内容"] button',
        ),
      ].find((button) => button.textContent === label)!;
    await act(async () => view("请求").click());
    expect(body().getAttribute("data-view")).toBe("request");
    expect(
      body().querySelector('[data-testid="inspector-diagnosis"]'),
    ).toBeNull();
    expect(body().textContent).toContain('"model": "gpt-4.1"');
    await act(async () => view("HTTP").click());
    expect(body().getAttribute("data-view")).toBe("http");
    expect(body().textContent).toContain(
      "POST https://api.example.test/v1/responses HTTP/2",
    );
  });

  it("keeps differing upstream bytes separate from the actual client output", async () => {
    const part = (content: string) => ({
      content,
      media_type: "text/plain",
      captured_bytes: content.length,
      truncated: false,
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      response_content: part("Restored client reply"),
      upstream_response_content: part("Upstream placeholder reply"),
    });
    hostState.current = { selection: { record, row }, pinned: false };
    await render();
    expect(
      container.querySelector('[data-testid="audit-raw"] pre')?.textContent,
    ).toBe("Upstream placeholder reply");
    expect(
      container.querySelector('[data-testid="inspector-same-response"]'),
    ).toBeNull();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="RESULT"]',
        )!
        .click(),
    );
    expect(
      container.querySelector('[data-testid="audit-result-preview"]')
        ?.textContent,
    ).toBe("Restored client reply");
    expect(container.textContent).not.toContain("Upstream placeholder reply");
  });

  it("shows the client body once, beside its request line and HTTP envelope", async () => {
    const content = '{"model":"gpt-4.1","input":"ping"}';
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      http_meta: {
        method: "POST",
        url: "/v1/responses",
        http_version: "HTTP/1.1",
        request_headers: [
          { name: "Content-Type", value: "application/json", redacted: false },
        ],
        response_status: 200,
        response_headers: [],
      },
      request_body: {
        content,
        media_type: "application/json",
        captured_bytes: content.length,
        truncated: false,
      },
    });
    hostState.current = {
      selection: { record, row: laterRow },
      pinned: false,
    };
    await render();

    const section = container.querySelector<HTMLElement>(
      '[data-testid="inspector-section"][data-chip="CLIENT"]',
    );
    expect(
      section?.querySelector('[data-testid="inspector-client-endpoint"]')
        ?.textContent,
    ).toBe("POST /v1/responses");
    expect(
      section?.querySelector('[data-testid="inspector-client-size"]')
        ?.textContent,
    ).toBe(`${content.length} B`);
    // One pane, no card inside a card repeating the title and size.
    expect(section?.textContent?.split("客户端请求体")).toHaveLength(1);
    expect(section?.textContent?.split(`${content.length} B`)).toHaveLength(2);
    const body = () =>
      section!.querySelector<HTMLElement>(
        '[data-testid="inspector-client-body"]',
      )!;
    expect(body().getAttribute("data-view")).toBe("request");
    expect(body().textContent).toContain('"input": "ping"');

    const http = [
      ...section!.querySelectorAll<HTMLButtonElement>(
        '[aria-label="客户端内容"] button',
      ),
    ].find((button) => button.textContent === "HTTP")!;
    await act(async () => http.click());
    expect(body().getAttribute("data-view")).toBe("http");
    expect(body().textContent).toContain("POST /v1/responses HTTP/1.1");
    expect(body().textContent).toContain("Content-Type");
  });

  it("keeps a search box on the body and steps between the hits", async () => {
    const content = JSON.stringify({
      model: "gpt-4.1",
      input: [{ text: "ping" }, { text: "PING again" }],
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      request_body: {
        content,
        media_type: "application/json",
        captured_bytes: content.length,
        truncated: false,
      },
    });
    hostState.current = {
      selection: { record, row: laterRow },
      pinned: false,
    };
    const scrolled = vi
      .spyOn(Element.prototype, "scrollIntoView")
      .mockImplementation(() => {});
    await render();

    const body = () =>
      container.querySelector<HTMLElement>(
        '[data-testid="inspector-client-body"]',
      )!;
    // The box is there before anyone asks for it, with its shortcut.
    const input = container.querySelector<HTMLInputElement>(
      '[data-testid="find-input"]',
    )!;
    expect(input).not.toBeNull();
    expect(input.disabled).toBe(false);
    expect(
      container.querySelector('[data-testid="find-bar"] kbd'),
    ).not.toBeNull();

    // ⌘F puts the cursor in it.
    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "f", metaKey: true }),
      );
    });
    expect(document.activeElement).toBe(input);

    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "ping");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await flush();

    const count = () =>
      container.querySelector('[data-testid="find-count"]')?.textContent;
    const activeHit = () =>
      body().querySelector('mark[data-find-active="true"]');
    expect(count()).toBe("1/2");
    expect(activeHit()?.textContent).toBe("ping");
    expect(scrolled).toHaveBeenCalled();

    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
    expect(count()).toBe("2/2");
    expect(activeHit()?.textContent).toBe("PING");

    // The original text is searched too, from the same bar.
    const raw = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "原文",
    )!;
    await act(async () => raw.click());
    await flush();
    expect(body().querySelector('[data-testid="audit-raw"]')).not.toBeNull();
    expect(count()).toBe("1/2");
    expect(activeHit()?.textContent).toBe("ping");

    // Escape clears the query; the box stays for the next search.
    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    expect(input.value).toBe("");
    expect(container.querySelector('[data-testid="find-count"]')).toBeNull();
    expect(body().querySelector('[data-testid="find-mark"]')).toBeNull();
    scrolled.mockRestore();
  });

  it("widens the window from its title bar and narrows it back", async () => {
    hostMocks.invoke.mockImplementation(
      async (command: string, args?: Record<string, unknown>) => {
        if (command === "trajectory_inspector_state") return hostState.current;
        if (command === "set_trajectory_inspector_wide") return args?.wide;
        return undefined;
      },
    );
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    const widen = () =>
      container.querySelector<HTMLButtonElement>(
        '[data-testid="trajectory-inspector-widen"]',
      )!;
    // Quiet and icon-only, like the pin beside it.
    expect(widen().getAttribute("aria-label")).toBe("变成宽窗");
    expect(widen().textContent).toBe("");
    expect(widen().getAttribute("aria-pressed")).toBe("false");
    expect(
      container.querySelector(
        '[data-testid="trajectory-inspector-edge-flash"]',
      ),
    ).toBeNull();

    await act(async () => widen().click());
    await flush();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_wide",
      { wide: true },
    );
    expect(widen().getAttribute("aria-pressed")).toBe("true");
    expect(widen().getAttribute("aria-label")).toBe("恢复窄窗");
    // The edges flash while the native frame stretches.
    expect(
      container.querySelector(
        '[data-testid="trajectory-inspector-edge-flash"]',
      ),
    ).not.toBeNull();

    await act(async () => widen().click());
    await flush();

    expect(hostMocks.invoke).toHaveBeenLastCalledWith(
      "set_trajectory_inspector_wide",
      { wide: false },
    );
    expect(widen().getAttribute("aria-pressed")).toBe("false");
  });

  it("tours widen, find and pin the first time it shows a call", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    localStorage.removeItem(TRAJECTORY_INSPECTOR_TOUR_KEY);
    const content = '{"input":"ping"}';
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      request_body: {
        content,
        media_type: "application/json",
        captured_bytes: content.length,
        truncated: false,
      },
    });
    const tour = () => document.querySelector('[data-slot="spotlight-tour"]');
    const spotlight = () =>
      document.querySelector<HTMLElement>("[data-tour-spotlight]")?.dataset
        .tourSpotlight;
    const button = (text: string) =>
      [...document.querySelectorAll("button")].find(
        (item) => item.textContent === text,
      )!;

    await render();
    // An empty window has nothing to tour yet.
    await act(async () => vi.advanceTimersByTime(1000));
    expect(tour()).toBeNull();

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();
    await act(async () => vi.advanceTimersByTime(600));

    expect(spotlight()).toBe("inspector-widen");
    expect(tour()?.querySelector("h3")?.textContent).toBe("一键变宽");
    expect(tour()?.textContent).toContain("1 / 3");
    expect(localStorage.getItem(TRAJECTORY_INSPECTOR_TOUR_KEY)).toBe("seen");

    await act(async () => button("下一步").click());
    expect(spotlight()).toBe("inspector-find");
    expect(tour()?.textContent).toContain(findShortcutLabel());

    await act(async () => button("下一步").click());
    expect(spotlight()).toBe("inspector-pin");

    await act(async () => button("知道了").click());
    await vi.waitFor(() => expect(tour()).toBeNull());
  });

  it("replays the tour, leaving out a search box the tab does not have", async () => {
    hostState.current = {
      selection: { record, row: { ...row, chip: "RESULT", lane: "client" } },
      pinned: false,
    };
    await render();
    expect(
      container
        .querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("RESULT");
    expect(
      document.querySelector('[data-tour-target="inspector-find"]'),
    ).toBeNull();

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="trajectory-inspector-tour"]',
        )!
        .click();
    });

    const tour = document.querySelector('[data-slot="spotlight-tour"]');
    expect(tour?.textContent).toContain("1 / 2");
    expect(
      document.querySelector<HTMLElement>("[data-tour-spotlight]")?.dataset
        .tourSpotlight,
    ).toBe("inspector-widen");
  });

  it("restores a widened window and admits a width the host refused", async () => {
    hostState.current = {
      selection: { record, row },
      pinned: false,
      wide: true,
    };
    hostMocks.invoke.mockImplementation(async (command: string) => {
      if (command === "trajectory_inspector_state") return hostState.current;
      throw new Error("a full-screen inspector window cannot change its width");
    });
    await render();

    const widen = () =>
      container.querySelector<HTMLButtonElement>(
        '[data-testid="trajectory-inspector-widen"]',
      )!;
    // A dev reload keeps the toggle in step with the window the host widened.
    expect(widen().getAttribute("aria-pressed")).toBe("true");

    await act(async () => widen().click());
    await flush();

    expect(widen().getAttribute("aria-pressed")).toBe("true");
  });

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
    // A window-level control: it belongs beside the traffic lights, drawn as a
    // pushpin rather than a map marker, not inside the call's own header.
    expect(
      pinButton(container).closest('[data-slot="window-accessory"]'),
    ).not.toBeNull();
    expect(inspector(container)?.contains(pinButton(container))).toBe(false);
    expect(
      pinButton(container).querySelector('[data-animated-icon="pin"]'),
    ).not.toBeNull();
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
    expect(
      inspector(container)?.querySelector('[aria-label="上游响应"]'),
    ).not.toBeNull();
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

  it("keeps the chosen tab when a poll pushes the same phase again", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    await act(async () => {
      inspector(container)
        ?.querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="RESULT"]',
        )
        ?.click();
    });

    // A new turn in the list hands down fresh copies of every record.
    await act(async () => {
      pushSelection(structuredClone({ row, record }));
    });
    await flush();

    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("RESULT");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();

    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("CLIENT");
  });

  it("freezes on its call once pinned and thaws when unpinned", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    // A following window keeps a quiet, icon-only pin in its title bar.
    expect(pinButton(container).getAttribute("aria-label")).toBe("置顶窗口");
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("false");
    expect(pinButton(container).textContent).toBe("");

    await clickPin();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_pinned",
      { pinned: true },
    );
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("true");
    // A floating window says so without a hover.
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("true");
    expect(pinButton(container).textContent).toBe("已置顶");

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
        {
          kind: "accepted",
          started_at: at(0),
          ended_at: at(0),
          status: "succeeded",
          summary: "gpt-4.1 · openai.responses",
          attempt_index: 0,
        },
        {
          kind: "routed",
          started_at: at(1),
          ended_at: at(1),
          status: "succeeded",
          summary: `native · ${serviceA}`,
          attempt_index: 1,
        },
        {
          kind: "upstream",
          started_at: at(1),
          ended_at: at(2),
          status: "failed",
          summary: "upstream_unavailable",
          attempt_index: 1,
        },
        {
          kind: "routed",
          started_at: at(2),
          ended_at: at(2),
          status: "failed",
          summary: `${serviceB} · credential_unavailable`,
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: at(3),
          ended_at: at(3),
          status: "failed",
          summary: "upstream_unavailable",
          attempt_index: 1,
        },
      ],
    };
    await render();

    await act(async () => {
      pushSelection({
        row: {
          ...row,
          id: `${failed.id}:routed`,
          chip: "ROUTE",
          lane: "gateway",
        },
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
          '[data-testid="routing-steps"] li',
        ),
      ].map((item) => [item.textContent, item.getAttribute("data-outcome")]),
    ).toEqual([
      ["已选用Primary · 原样转发", "selected"],
      ["无法使用Backup · 凭据不可用", "rejected"],
    ]);
    expect(inspector(container)?.textContent).toContain("路由过程");
    expect(inspector(container)?.textContent).not.toContain(
      "credential_unavailable",
    );
  });

  it("says why routing chose the provider and names the ones it skipped", async () => {
    const skipped = "service_aaaaaaaaaaaaaaaaaaaaaaaa";
    const deleted = "service_bbbbbbbbbbbbbbbbbbbbbbbb";
    const unlisted = "service_eeeeeeeeeeeeeeeeeeeeeeee";
    const routeRow: TrajectoryRow = {
      ...row,
      id: `${record.id}:routed`,
      chip: "ROUTE",
      lane: "gateway",
    };
    await render();

    await act(async () => {
      pushSelection({
        row: routeRow,
        record: {
          ...record,
          routing_decision: {
            selected: "failover",
            skipped: [
              { service_id: skipped, reason: "protocol_unsupported" },
              { service_id: unlisted, reason: "model_not_listed" },
              { service_id: deleted, reason: "disabled" },
            ],
          },
        },
        services: { [skipped]: { id: skipped, name: "mly" } },
      });
    });
    await flush();

    const steps = () =>
      [
        ...(inspector(container)?.querySelectorAll(
          '[data-testid="routing-steps"] li',
        ) ?? []),
      ].map((item) => [
        item.textContent,
        item.getAttribute("data-outcome"),
        item.getAttribute("data-code"),
      ]);
    // A provider missing from the list is still named, by its ID; one without
    // the model is no step at all.
    expect(steps()).toEqual([
      ["已跳过mly · 不支持该入口协议", "skipped", "protocol_unsupported"],
      [`已跳过${deleted} · 已停用`, "skipped", "disabled"],
      [
        `已选用${record.service_id} · 故障切换：此前尝试的 API 提供商失败或被拒绝`,
        "selected",
        null,
      ],
    ]);

    // Records from before the gateway explained its choice show only the route.
    await act(async () => {
      pushSelection({ row: routeRow, record });
    });
    await flush();
    expect(steps()).toEqual([[`已选用${record.service_id}`, "selected", null]]);
  });

  it("explains a call no provider could take, and what a paused provider is", async () => {
    const paused = "service_cccccccccccccccccccccccc";
    const disabled = "service_dddddddddddddddddddddddd";
    const unavailable: RequestRecord = {
      ...record,
      status: "failed",
      service_id: null,
      http_status: null,
      error: {
        category: "upstream",
        code: "upstream_unavailable",
        message: "all capable endpoints are temporarily unhealthy",
        retryable: true,
      },
      events: [
        {
          kind: "accepted",
          started_at: record.started_at,
          ended_at: record.started_at,
          status: "succeeded",
          summary: "claude-opus-5 · anthropic.messages",
          attempt_index: 0,
        },
        {
          kind: "completed",
          started_at: record.started_at,
          ended_at: record.started_at,
          status: "failed",
          summary: "all capable endpoints are temporarily unhealthy",
          attempt_index: 0,
        },
      ],
      routing_decision: {
        skipped: [
          { service_id: disabled, reason: "disabled" },
          { service_id: paused, reason: "circuit_open" },
        ],
      },
    };
    await render();

    await act(async () => {
      pushSelection({
        row: {
          ...row,
          id: `${unavailable.id}:routed`,
          chip: "ROUTE",
          lane: "gateway",
        },
        record: unavailable,
        services: { [paused]: { id: paused, name: "new-api" } },
      });
    });
    await flush();

    // Without a route event the call still gets a route tab.
    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="inspector-tab"]',
        ),
      ].map((tab) => tab.getAttribute("data-chip")),
    ).toEqual(["CLIENT", "ROUTE", "RESULT"]);
    const steps = inspector(container)!.querySelector(
      '[data-testid="routing-steps"]',
    );
    expect(
      [...steps!.querySelectorAll("li")].map((item) => item.textContent),
    ).toEqual([
      `已跳过${disabled} · 已停用`,
      "已跳过new-api · 连续失败，暂停使用中",
    ]);
    expect(steps?.textContent).toContain("冷却结束后先放行一次试探请求");
    expect(inspector(container)?.textContent).not.toContain("circuit_open");
  });

  it("lists the tools and fields the protocol conversion dropped", async () => {
    const routeRow: TrajectoryRow = {
      ...row,
      id: `${record.id}:routed`,
      chip: "ROUTE",
      lane: "gateway",
    };
    await render();

    await act(async () => {
      pushSelection({
        row: routeRow,
        record: {
          ...record,
          conversion_diagnostics: [
            {
              phase: "request",
              severity: "error",
              code: "unsupported_hosted_tool",
              path: "tools[0]",
              message:
                'OpenAI Chat Completions cannot represent hosted tool "local_shell"',
            },
            {
              phase: "request",
              severity: "warning",
              code: "unsupported_parallel_tool_control",
              path: "parallel_tool_calls",
              message: "",
            },
          ],
        },
      });
    });
    await flush();

    const details = inspector(container)?.querySelector(
      '[data-testid="conversion-diagnostics"]',
    );
    const groups = [...(details?.querySelectorAll("[data-group]") ?? [])];
    expect(groups.map((group) => group.getAttribute("data-group"))).toEqual([
      "tools",
      "fields",
    ]);
    expect(groups[0]?.textContent).toContain("转换时丢弃或改写的工具");
    expect(groups[0]?.textContent).toContain("请求 · tools[0]");
    expect(groups[0]?.textContent).toContain("可能影响结果");
    expect(groups[0]?.textContent).toContain('hosted tool "local_shell"');
    expect(groups[1]?.textContent).toContain("转换时丢弃或改写的字段");
    expect(groups[1]?.textContent).toContain("仅细节差异");
    // Without a message the code still says what was lost.
    expect(groups[1]?.textContent).toContain(
      "unsupported_parallel_tool_control",
    );

    await act(async () => {
      pushSelection({ row: routeRow, record });
    });
    await flush();
    expect(
      inspector(container)?.querySelector(
        '[data-testid="conversion-diagnostics"]',
      ),
    ).toBeNull();
  });

  it("uses the same icon and hint for continuation and provider stickiness", async () => {
    await render();
    await act(async () => {
      pushSelection({
        row: { ...row, chip: "ROUTE", lane: "gateway" },
        record: {
          ...record,
          session_link: { kind: "explicit", value: "previous-request" },
          routing_decision: { selected: "session_binding", skipped: [] },
        },
      });
    });
    await flush();
    const marks = inspector(container)?.querySelectorAll(
      "[data-conversation-indicator]",
    );
    expect(marks).toHaveLength(2);
    expect(
      Array.from(marks ?? [], (mark) => mark.getAttribute("aria-label")),
    ).toEqual(["对话延续：会话粘性", "对话延续：会话粘性"]);
    for (const mark of marks ?? []) {
      expect(mark.querySelector('[data-animated-icon="link"]')).not.toBeNull();
      expect(mark.textContent).toBe("");
    }
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

  it("says a withheld body waits for an unlock instead of capture", async () => {
    await render();
    const withheld = {
      reason: "raw_locked",
      raw_available: false,
      media_type: "application/json",
      truncated: false,
      captured_bytes: 64,
    } as const;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      withheld: {
        request_body: withheld,
        upstream_request_body: { ...withheld, reason: "privacy_redacted" },
      },
    });
    const captured: RequestRecord = {
      ...record,
      audit: {
        ...record.audit,
        request_body_captured: true,
        upstream_request_body_captured: true,
      },
    };
    const hint = (testId: string) =>
      container
        .querySelector(`[data-testid="${testId}"]`)
        ?.querySelector('[data-testid="inspector-missing-body"]')?.textContent;

    await act(async () => {
      pushSelection({ row: laterRow, record: captured });
    });
    await flush();
    expect(hint("inspector-client-body")).toBe(
      "原文已封存，输入原文口令解锁后才能查看。",
    );

    // The unlock opens here, so the operator is not sent to the main window.
    bridgeMocks.getRawSealingStatus.mockResolvedValue(sealing(false));
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="trajectory-inspector-unlock"]',
        )!
        .click();
    });
    await flush();
    expect(document.body.textContent).toContain(
      "解锁后可在本机查看已封存的原文",
    );

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="UPSTREAM"]',
        )!
        .click();
    });
    await flush();
    await act(async () =>
      [
        ...container.querySelectorAll<HTMLButtonElement>(
          '[aria-label="上游内容"] button',
        ),
      ]
        .find((button) => button.textContent === "请求")!
        .click(),
    );
    expect(hint("inspector-upstream-body")).toBe(
      "此部分已捕获，当前视图不显示。",
    );
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
      ...auditContent,
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
      ...auditContent,
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

  it("drops raw parts from a pinned window once another window locks", async () => {
    bridgeMocks.getRawSealingStatus.mockResolvedValue(sealing(true));
    bridgeMocks.getRequestAuditContent.mockResolvedValue(rawContent);
    hostState.current = { selection: { record, row }, pinned: true };
    await render();
    await flush();
    expect(inspector(container)?.textContent).toContain("raw-only");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);

    bridgeMocks.getRawSealingStatus.mockResolvedValue(sealing(false));
    let settle: (content: AuditContent) => void = () => undefined;
    bridgeMocks.getRequestAuditContent.mockReturnValue(
      new Promise<AuditContent>((resolve) => {
        settle = resolve;
      }),
    );
    await announceSealingChange();
    await flush();
    // The raw part leaves before Core answers the refetch.
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    expect(inspector(container)?.textContent).not.toContain("raw-only");

    await act(async () => settle(lockedContent));
    await flush();
    expect(inspector(container)?.textContent).not.toContain("raw-only");
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("true");

    // A lock that changes nothing more does not refetch again.
    await announceSealingChange();
    await flush();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);

    // An unlock elsewhere fills the locked part in.
    bridgeMocks.getRawSealingStatus.mockResolvedValue(sealing(true));
    bridgeMocks.getRequestAuditContent.mockResolvedValue(rawContent);
    await announceSealingChange();
    await flush();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(3);
    expect(inspector(container)?.textContent).toContain("raw-only");
  });

  it("drops raw parts when the unlock idles out without any event", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    try {
      const tick = async (ms: number) => {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(ms);
        });
      };
      const expiresAt = new Date(Date.now() + 60_000).toISOString();
      bridgeMocks.getRawSealingStatus.mockResolvedValue(
        sealing(true, expiresAt),
      );
      bridgeMocks.getRequestAuditContent.mockResolvedValue(rawContent);
      hostState.current = { selection: { record, row }, pinned: true };
      await act(async () => {
        root.render(<TrajectoryInspectorWindow />);
      });
      await tick(0);
      await tick(0);
      expect(inspector(container)?.textContent).toContain("raw-only");

      // Core locks on its own; the window reads the state after the expiry.
      bridgeMocks.getRawSealingStatus.mockResolvedValue(sealing(false));
      bridgeMocks.getRequestAuditContent.mockResolvedValue(lockedContent);
      await tick(59_000);
      expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
      await tick(3_000);
      await tick(0);
      expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
      expect(inspector(container)?.textContent).not.toContain("raw-only");
    } finally {
      vi.useRealTimers();
    }
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
