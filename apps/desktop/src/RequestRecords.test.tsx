// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  deleteRequestRecord: vi.fn(),
  getAuditSettings: vi.fn(),
  getCoreStatus: vi.fn(),
  getPreferences: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  getRawSealingStatus: vi.fn(),
  getRequestAuditContent: vi.fn(),
  getRequestRecord: vi.fn(),
  getRequestSession: vi.fn(),
  getRoutingSettings: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  listRequestRecordChildren: vi.fn(),
  listRequestRecords: vi.fn(),
  listRequestSessions: vi.fn(),
  listenRawSealingChanged: vi.fn(async () => () => {}),
  lockRaw: vi.fn(),
  purgeRequestRecords: vi.fn(),
  saveTextFile: vi.fn(),
  setRawPassword: vi.fn(),
  unlockRaw: vi.fn(),
  updateAuditSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

// Records which response parts reach the detail view's preview parser.
const previewParts = vi.hoisted(() => ({ parsed: [] as string[] }));
vi.mock("./response-preview-model", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("./response-preview-model")>();
  return {
    ...actual,
    parseResponsePreview: (
      ...args: Parameters<typeof actual.parseResponsePreview>
    ) => {
      previewParts.parsed.push(args[0].content);
      return actual.parseResponsePreview(...args);
    },
  };
});

import type { AuditSettings } from "./audit-settings-model";
import type { RawSealingState } from "./raw-sealing-model";
import { RequestRecords } from "./RequestRecords";
import { WorkspaceSnapshotProvider } from "./workspace-snapshots";
import {
  displayRequestStatus,
  emptyTrajectoryFields,
  type RequestRecord,
  type RequestSession,
} from "./request-record-model";
import type { RoutableService } from "./service-model";
import type { RequestService } from "./request-service-model";
import { i18n } from "./i18n";

const service: RoutableService & RequestService = {
  id: "service_01",
  name: "Primary gateway",
  kind: "newapi",
  enabled: true,
  models: ["gpt-4.1"],
  capabilities: [
    {
      protocol: "openai.responses",
      mode: "delegated",
      streaming: true,
    },
  ],
};

function rawSealing(overrides: Partial<RawSealingState> = {}): RawSealingState {
  return {
    raw_available: true,
    configured: true,
    password_set: true,
    // No password protects the key.
    password_required: overrides.password_set === false,
    envelopes: ["password"],
    key_verified: true,
    unlocked: false,
    unlock_expires_at: null,
    unlock_idle_seconds: 900,
    retry_after_seconds: 0,
    password_min_length: 8,
    password_max_length: 128,
    key_replaced: false,
    ...overrides,
  };
}

const emptyAudit = {
  request_body_captured: false,
  response_content_captured: false,
  request_body_truncated: false,
  response_content_truncated: false,
  upstream_request_body_captured: false,
  upstream_response_content_captured: false,
  upstream_request_body_truncated: false,
  upstream_response_content_truncated: false,
};

const firstRecord: RequestRecord = {
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
  service_id: "service_01",
  local_access_token_id: "token_01",
  http_status: 200,
  latency_ms: 120,
  usage: {
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    cache_read_tokens: 4,
  },
  error: null,
  audit: {
    ...emptyAudit,
    request_body_captured: true,
    response_content_captured: true,
  },
  privacy_restore: {
    enabled: true,
    mapping_count: 4,
    restored_count: 5,
    visible_restored_count: 5,
    tool_argument_restored_count: 0,
    fallback_count: 0,
    hits: [
      { kind: "email", count: 2 },
      { kind: "phone", count: 1 },
    ],
  },
  ...emptyTrajectoryFields,
};

const secondRecord: RequestRecord = {
  ...firstRecord,
  id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  started_at: "2026-07-25T11:00:00Z",
  completed_at: "2026-07-25T11:00:02Z",
  status: "failed",
  requested_model: "gpt-4.1-mini",
  http_status: 502,
  error: {
    category: "upstream",
    code: "upstream_unavailable",
    message: "gateway unavailable",
    retryable: true,
  },
  audit: { ...emptyAudit },
};

/** A sealing status as the host returns it. */
function rawStatus(overrides: Partial<RawSealingState> = {}) {
  return rawSealing(overrides);
}

const rawUnconfigured: Partial<RawSealingState> = {
  raw_available: false,
  configured: false,
  password_set: false,
  envelopes: [],
  key_verified: false,
};

function captureSwitch(): HTMLButtonElement {
  const toggle = document.querySelector<HTMLButtonElement>(
    '[role="switch"][aria-label="请求和响应捕获"]',
  );
  if (!(toggle instanceof HTMLButtonElement)) {
    throw new Error("Missing request/response capture switch");
  }
  return toggle;
}

function proofDialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>(
    '[data-slot="proof-confirm-dialog"]',
  );
}

async function typeInto(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function typeNewRawPassword(password: string) {
  const inputs = [
    ...document.querySelectorAll<HTMLInputElement>(
      'input[autocomplete="new-password"]',
    ),
  ];
  expect(inputs).toHaveLength(2);
  for (const input of inputs) await typeInto(input, password);
}

async function flush(times = 3) {
  for (let index = 0; index < times; index += 1) {
    await act(async () => await Promise.resolve());
  }
}

function sessionFromRecord(
  record: RequestRecord,
  overrides: Partial<RequestSession> = {},
): RequestSession {
  return {
    id: record.session_id ?? record.id,
    title: record.input_preview ?? record.requested_model ?? "未命名会话",
    started_at: record.started_at,
    last_started_at: record.started_at,
    completed_at: record.completed_at,
    duration_ms:
      record.latency_ms ??
      (record.completed_at
        ? Date.parse(record.completed_at) - Date.parse(record.started_at)
        : 0),
    active_request_starts:
      record.status === "pending" &&
      record.latency_ms === null &&
      !record.completed_at
        ? [record.started_at]
        : [],
    turn_count: 1,
    call_count: 1 + record.child_count,
    status: displayRequestStatus(record.status, record.http_status),
    requested_model: record.requested_model,
    input_protocol: record.input_protocol,
    service_id: record.service_id,
    local_access_token_id: record.local_access_token_id,
    ...overrides,
  };
}

function sessionDetail(record: RequestRecord) {
  const session = sessionFromRecord(record);
  return { ...session, turns: [record] };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, reject, resolve };
}

function exactButton(
  label: string,
  root: ParentNode = document,
): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

function buttonContaining(
  label: string,
  root: ParentNode = document,
): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button containing: ${label}`);
  }
  return match;
}

/** Option text as read aloud; decorative marks may carry their own SVG titles. */
function optionText(option: Element): string | undefined {
  const copy = option.cloneNode(true) as Element;
  copy
    .querySelectorAll('[aria-hidden="true"]')
    .forEach((node) => node.remove());
  return copy.textContent?.trim();
}

/** Opens a dropdown menu by its trigger label and returns the named item. */
async function menuItem(
  triggerLabel: string,
  itemText: string,
): Promise<HTMLElement> {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[aria-label="${triggerLabel}"]`,
  );
  if (!trigger) throw new Error(`Missing menu trigger: ${triggerLabel}`);
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
  ].find((candidate) => candidate.textContent?.trim() === itemText);
  if (!item) throw new Error(`Missing menu item: ${itemText}`);
  return item;
}

async function chooseOption(label: string, option: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[role="combobox"][aria-label="${label}"]`,
  );
  if (!trigger) throw new Error(`Missing select trigger: ${label}`);
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => optionText(candidate) === option);
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

describe("RequestRecords", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;

  beforeEach(() => {
    // Poll only when a test advances time, even on slow CI runners.
    vi.useFakeTimers();
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    window.confirm = vi.fn(() => true);
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(secondRecord), sessionFromRecord(firstRecord)],
      next_cursor: "cursor-1",
    });
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [secondRecord, firstRecord],
      next_cursor: "cursor-1",
    });
    bridgeMocks.getRequestSession.mockImplementation(
      async (sessionId: string) => {
        const record = [firstRecord, secondRecord].find(
          (item) => (item.session_id ?? item.id) === sessionId,
        );
        if (!record) throw new Error(`missing session ${sessionId}`);
        return sessionDetail(record);
      },
    );
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: false,
      response_content_enabled: false,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
    });
    bridgeMocks.getRawSealingStatus.mockResolvedValue(rawSealing());
    bridgeMocks.getCoreStatus.mockResolvedValue({
      app_version: "0.9.0",
      version: { core_version: "0.9.0", build_commit: "abc1234" },
    });
    bridgeMocks.getPrivacyPolicy.mockResolvedValue({
      policy: {
        enabled: true,
        detector: "local_model",
        local_model_id: "model_01",
        request_action: "redact",
        response_restore: true,
        restore_tool_arguments: true,
        skip_tool_declarations: false,
        inspect_additional_tools: false,
      },
    });
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValue({
      items: [{ id: "model_01", name: "Privacy Filter", variant_name: "Q4" }],
    });
    bridgeMocks.getPreferences.mockResolvedValue({
      values: {
        response_start_timeout_seconds: 120,
        max_concurrent_inspections: 1,
        max_request_body_mib: 32,
      },
    });
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      strategy: "priority",
      max_attempts: 3,
    });
    bridgeMocks.updateAuditSettings.mockImplementation(async (patch) => ({
      request_body_enabled: false,
      response_content_enabled: false,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
      ...patch,
    }));
    bridgeMocks.purgeRequestRecords.mockResolvedValue({
      deleted_records: 2,
      deleted_audit_blobs: 1,
    });
    bridgeMocks.deleteRequestRecord.mockResolvedValue(undefined);
    bridgeMocks.saveTextFile.mockResolvedValue(
      `/tmp/astrlink-${firstRecord.id}.txt`,
    );
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      withheld: {},
      http_meta: {
        method: "POST",
        url: "/v1/responses?stream=true",
        http_version: "HTTP/1.1",
        request_headers: [
          {
            name: "authorization",
            value: "Bearer <redacted:51 chars>",
            redacted: true,
          },
          { name: "content-type", value: "application/json", redacted: false },
        ],
        response_status: 200,
        response_headers: [
          { name: "x-request-id", value: "req_upstream_1", redacted: false },
        ],
      },
      request_body: {
        media_type: "application/json",
        content: '{"prompt":"secret"}',
        truncated: false,
        captured_bytes: 19,
      },
      response_content: {
        media_type: "text/plain",
        content: "hello",
        truncated: true,
        captured_bytes: 5,
      },
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    vi.useRealTimers();
    vi.restoreAllMocks();
    container.remove();
  });

  // These tests read the trace; the conversation default has its own test.
  const renderRecords = async (session = "session-1", services = [service]) => {
    await act(async () => {
      reactRoot.render(
        <RequestRecords
          accessTokens={[]}
          accessTokensReady
          coreSessionKey={session}
          initialDetailTab="trajectory"
          services={services}
          isReady
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
  };

  it.each([
    ["claude_code", "Claude Code", ""],
    ["pi", "Pi", ""],
    ["deepseek_harness", "DeepSeek Harness", ""],
    // Clients without a published mark get a monogram, not the unknown robot.
    ["droid", "Droid", "D"],
  ] as const)(
    "shows %s beside each session, independently of the model brand",
    async (clientType, clientName, monogram) => {
      bridgeMocks.listRequestSessions.mockResolvedValue({
        items: [
          sessionFromRecord(firstRecord, { client_type: clientType }),
          sessionFromRecord(secondRecord),
        ],
        next_cursor: null,
      });
      await renderRecords();
      const rows = container.querySelectorAll(
        '[data-testid="request-session-row"]',
      );
      expect(rows[0].querySelector('[role="img"]')?.getAttribute("title")).toBe(
        `客户端：${clientName}`,
      );
      expect(rows[1].querySelector('[role="img"]')?.getAttribute("title")).toBe(
        "客户端：未知客户端",
      );
      // Brand marks keep their own colour; only the unknown fallback is muted.
      expect(
        rows[0]
          .querySelector('[role="img"]')
          ?.classList.contains("text-foreground"),
      ).toBe(true);
      expect(
        rows[1]
          .querySelector('[role="img"]')
          ?.classList.contains("text-muted-foreground"),
      ).toBe(true);
      const mark = rows[0].querySelector('[role="img"]');
      if (monogram) {
        expect(mark?.querySelector("svg")).toBeNull();
        expect(mark?.textContent).toBe(monogram);
      } else {
        expect(mark?.querySelector("svg")).not.toBeNull();
      }
      expect(rows[0].textContent).toContain("gpt-4.1");
    },
  );

  it("shows explicit reasoning effort beside the model and omits it for legacy sessions", async () => {
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [
        sessionFromRecord(firstRecord, { reasoning_effort: "high" }),
        sessionFromRecord(secondRecord),
      ],
      next_cursor: null,
    });
    await renderRecords();
    const rows = container.querySelectorAll(
      '[data-testid="request-session-row"]',
    );
    const badge = rows[0]?.querySelector('[aria-label="思考强度: high"]');
    expect(badge?.textContent).toBe("high");
    expect(badge?.parentElement?.textContent).toContain("gpt-4.1");
    expect(rows[1]?.querySelector('[aria-label^="思考强度:"]')).toBeNull();
  });

  it("renders a session stream instead of a wide table", async () => {
    await renderRecords();

    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    expect(
      container.querySelectorAll('[data-slot="page-header"]'),
    ).toHaveLength(1);
    expect(container.querySelector("table")).toBeNull();
    expect(
      container.querySelectorAll('[data-testid="request-session-row"]'),
    ).toHaveLength(2);
    expect(container.textContent).toContain("gpt-4.1");
    expect(container.textContent).toContain("/v1/responses");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("1 轮");
    expect(container.textContent).not.toContain("次调用");
    expect(container.textContent).toContain("120 ms");
    expect(container.textContent).not.toMatch(/\d{3,}m /);
    const provider = container.querySelector(
      `[aria-label="${i18n.t("records.provider")}: Primary gateway"]`,
    );
    expect(provider?.querySelector('[aria-label="New API"]')).not.toBeNull();
    expect(provider?.getAttribute("title")).toContain(service.id);
  });

  it("mounts what fits the window first and the rest of a long list frame by frame", async () => {
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(400);
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const sessions = Array.from({ length: 36 }, (_, index) => {
      // Two date groups of 18, newest first, seconds apart within each day
      // so no time zone splits a group.
      const startedAt = new Date(
        Date.parse("2026-07-25T06:00:30Z") -
          (index % 18) * 1000 -
          (index < 18 ? 0 : 24 * 60 * 60_000),
      ).toISOString();
      return sessionFromRecord({
        ...firstRecord,
        id: `req_long_${String(index).padStart(2, "0")}`,
        session_id: `sess_long_${String(index).padStart(2, "0")}`,
        started_at: startedAt,
      });
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: sessions,
      next_cursor: null,
    });
    await renderRecords();
    const rowIds = () =>
      [
        ...container.querySelectorAll<HTMLElement>(
          '[data-testid="request-session-row"]',
        ),
      ].map((row) => row.dataset.sessionId);
    const groupCounts = () =>
      [...container.querySelectorAll('[role="feed"] section')].map(
        (section) => section.querySelector('[data-slot="badge"]')?.textContent,
      );

    // 400px fits ten of the shortest rows.
    expect(rowIds()).toEqual(sessions.slice(0, 10).map((entry) => entry.id));
    expect(groupCounts()).toEqual(["18 条"]);

    while (frames.length > 0) {
      await act(async () => frames.shift()?.(performance.now()));
    }
    expect(rowIds()).toEqual(sessions.map((entry) => entry.id));
    expect(groupCounts()).toEqual(["18 条", "18 条"]);
  });

  it("distinguishes pending selection, an unrouted result, exhausted providers and a removed provider", async () => {
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [
        sessionFromRecord(firstRecord, {
          id: "pending",
          service_id: null,
          status: "pending",
        }),
        sessionFromRecord(firstRecord, {
          id: "blocked",
          service_id: null,
          status: "blocked",
        }),
        sessionFromRecord(firstRecord, {
          id: "failed",
          service_id: null,
          status: "failed",
        }),
        sessionFromRecord(firstRecord, {
          id: "removed",
          service_id: "service_removed",
        }),
      ],
      next_cursor: null,
    });
    await renderRecords();
    const labels = [
      ...container.querySelectorAll(
        '[data-testid="request-session-row"] [data-testid="request-service-label"]',
      ),
    ].map((node) => node.textContent);
    expect(labels).toEqual([
      `${i18n.t("records.provider")}${i18n.t("records.selectingService")}`,
      `${i18n.t("records.provider")}${i18n.t("records.noService")}`,
      `${i18n.t("records.provider")}${i18n.t("records.allServicesFailed")}`,
      `${i18n.t("records.provider")}service_removed`,
    ]);
  });

  it("shows cumulative runtime in both the list and detail across a long idle gap", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-28T12:00:00Z"));
    const last: RequestRecord = {
      ...firstRecord,
      id: "request_after_idle",
      started_at: "2026-07-27T01:00:00Z",
      completed_at: "2026-07-27T01:00:03Z",
      latency_ms: 3000,
    };
    const summary = sessionFromRecord(firstRecord, {
      last_started_at: last.started_at,
      completed_at: last.completed_at,
      turn_count: 2,
      call_count: 3,
      duration_ms: 3620,
      tool_duration_ms: 19800,
      average_ttft_ms: 2200,
      output_tokens_per_second: 131.25,
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [summary],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue({
      ...summary,
      turns: [firstRecord, last],
    });
    await renderRecords();
    const row = container.querySelector<HTMLButtonElement>(
      '[data-testid="request-session-row"]',
    )!;
    expect(row.textContent).toContain("2 轮 · 3 次调用 · 3.6 s");
    await act(async () => {
      row.click();
    });
    await act(async () => await Promise.resolve());
    const duration = () =>
      [...document.querySelectorAll("dt")].find(
        (node) => node.textContent === i18n.t("records.modelDuration"),
      )?.nextElementSibling?.textContent;
    expect(duration()).toBe("3.6 s");
    const stats = document.querySelector(
      '[data-testid="session-performance"]',
    )!;
    expect(stats.textContent).toContain("≈ 19.8 s");
    expect(stats.textContent).toContain("2.2 s");
    expect(stats.textContent).toContain("131.3 tok/s");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(duration()).toBe("3.6 s");
  });

  it("ticks only the active call and stops after the completion poll", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-28T12:00:10Z"));
    const summary = sessionFromRecord(firstRecord, {
      completed_at: null,
      status: "pending",
      last_started_at: "2026-07-28T12:00:00Z",
      duration_ms: 12_000,
      active_request_starts: ["2026-07-28T12:00:00Z"],
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [summary],
      next_cursor: null,
    });
    await renderRecords();
    const runtime = () =>
      container.querySelector('[data-testid="request-session-row"]')
        ?.textContent;
    expect(runtime()).toContain("22.0 s");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(runtime()).toContain("23.0 s");
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [
        {
          ...summary,
          status: "succeeded",
          completed_at: "2026-07-28T12:00:11Z",
          duration_ms: 23_000,
          active_request_starts: [],
        },
      ],
      next_cursor: null,
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(runtime()).toContain("23.0 s");
  });

  it("opens a session trajectory and shows retry children as RETRY rows", async () => {
    const root: RequestRecord = {
      ...firstRecord,
      attempt_index: 3,
      child_count: 2,
    };
    const children: RequestRecord[] = [
      {
        ...secondRecord,
        id: "req_childaaaaaaaaaaaaaaaaaaaaaaaaa",
        parent_request_id: root.id,
        attempt_index: 1,
        child_count: 0,
        service_id: "service_backup",
      },
      {
        ...secondRecord,
        id: "req_childbbbbbbbbbbbbbbbbbbbbbbbbb",
        parent_request_id: root.id,
        attempt_index: 2,
        child_count: 0,
      },
    ];
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(root)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(root),
      turns: [root],
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: children,
      next_cursor: null,
    });

    await renderRecords("session-1", [
      service,
      { ...service, id: "service_backup", name: "Backup gateway" },
    ]);
    expect(container.textContent).toContain("1 轮 · 3 次调用");
    expect(
      container.querySelector(
        `[aria-label="${i18n.t("records.latestProvider")}: Primary gateway"]`,
      ),
    ).not.toBeNull();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${root.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.listRequestRecordChildren).toHaveBeenCalledWith(root.id);
    // Children arrive through an effect-driven fetch after the session opens;
    // poll instead of counting microtask turns, which slow runners exceed.
    await vi.waitFor(() => expect(container.textContent).toContain("重试"));
    expect(container.textContent).toContain("子请求 1");
    expect(container.textContent).toContain("子请求 2");
    expect(
      container.querySelectorAll('[data-testid="trajectory-row"]').length,
    ).toBeGreaterThan(0);
    const retryProvider = container.querySelector(
      `[data-testid="trajectory-row"][data-chip="RETRY"][data-request-id="${children[0].id}"] [data-testid="request-service-label"]`,
    );
    expect(retryProvider?.textContent).toContain("Backup gateway");
    expect(
      container.querySelector(
        '[data-testid="trajectory-row"][data-chip="UPSTREAM"] [data-testid="request-service-label"]',
      )?.textContent,
    ).toContain("Primary gateway");

    bridgeMocks.getRequestAuditContent.mockClear();
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="RETRY"]',
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledWith(
      children[0].id,
    );
    expect(
      container.querySelector(
        '[data-testid="trajectory-inspector"] [data-testid="request-service-label"]',
      )?.textContent,
    ).toContain("Backup gateway");
  });

  it("copies skill diagnostic metadata without captured bodies", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const root: RequestRecord = {
      ...firstRecord,
      input_preview: "给这个仓库适配一下",
      events: [
        {
          kind: "completed",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "succeeded",
          summary: "succeeded",
          attempt_index: 1,
        },
      ],
    };
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(root)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(root),
      turns: [root],
    });

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${root.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    const copyItem = await menuItem("更多操作", "复制诊断信息");
    expect(copyItem.title).toContain("不含请求/响应正文");
    await act(async () => {
      copyItem.click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0]?.[0] as string;
    expect(copied).toContain("astrlink-debug");
    expect(copied).toContain(root.id);
    expect(copied).toContain("给这个仓库适配一下");
    expect(copied).toContain('"kind": "completed"');
    expect(copied).not.toContain("secret");
    expect(copied).not.toContain("hello");
    expect(copied).not.toContain("Bearer");
  });

  it("shows a model redirect in the list, the trajectory, the inspector and the diagnostic", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const redirect = { from: "gpt-4.1", to: "claude-sonnet-4-5" };
    // A legacy record: the trajectory synthesizes the REDIRECT step.
    const root: RequestRecord = { ...firstRecord, model_redirect: redirect };
    const session = sessionFromRecord(root, { model_redirect: redirect });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [session],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue({
      ...session,
      turns: [root],
    });

    await renderRecords();
    const label = container.querySelector(
      '[data-testid="request-session-row"] [data-redirected-to="claude-sonnet-4-5"]',
    );
    // Both models keep their brand mark; the arrow is spoken as words.
    expect(label?.textContent).toBe(
      "OpenAIgpt-4.1→重定向到Claudeclaude-sonnet-4-5",
    );
    expect(
      [...(label?.querySelectorAll('[aria-hidden="true"]') ?? [])].some(
        (element) => element.textContent === "→",
      ),
    ).toBe(true);
    expect(label?.querySelector(".sr-only")?.textContent).toBe("重定向到");

    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${root.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    const chips = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="trajectory-row"]',
      ),
    ].map((row) => row.dataset.chip);
    expect(chips.slice(0, 3)).toEqual(["CLIENT", "REDIRECT", "POLICY"]);
    const redirectRow = container.querySelector<HTMLButtonElement>(
      '[data-testid="trajectory-row"][data-chip="REDIRECT"]',
    );
    expect(redirectRow?.textContent).toContain("重定向");
    expect(
      redirectRow?.querySelector('[data-redirected-to="claude-sonnet-4-5"]')
        ?.textContent,
    ).toContain("gpt-4.1");

    await act(async () => {
      redirectRow?.click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    const redirectInspector = inspector?.querySelector(
      '[data-testid="redirect-inspector"]',
    );
    expect(inspector?.textContent).toContain("模型重定向");
    expect(
      redirectInspector?.querySelector(
        '[data-testid="inspector-requested-model"]',
      )?.textContent,
    ).toBe("客户端请求模型OpenAIgpt-4.1");
    expect(
      redirectInspector?.querySelector(
        '[data-testid="inspector-redirect-target"]',
      )?.textContent,
    ).toBe("重定向到Claudeclaude-sonnet-4-5");
    expect(redirectInspector?.textContent).toContain("原模型名");

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="ROUTE"]',
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    expect(
      container.querySelector(
        '[data-testid="trajectory-inspector"] [data-testid="inspector-redirect-target"]',
      )?.textContent,
    ).toBe("重定向到Claudeclaude-sonnet-4-5");
    expect(
      container.querySelector(
        '[data-testid="trajectory-inspector"] [data-testid="redirect-inspector"]',
      ),
    ).toBeNull();

    const copyItem = await menuItem("更多操作", "复制诊断信息");
    await act(async () => {
      copyItem.click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    const copied = writeText.mock.calls[0]?.[0] as string;
    const fence = "```json\n";
    const payload = JSON.parse(
      copied.slice(
        copied.indexOf(fence) + fence.length,
        copied.lastIndexOf("}") + 1,
      ),
    );
    expect(payload.session.requested_model).toBe("gpt-4.1");
    expect(payload.session.model_redirect).toStrictEqual(redirect);
    expect(payload.records[0].model_redirect).toStrictEqual(redirect);
  });

  // The detail carries every root turn of the conversation, and the children
  // fetch is one request per root that retried. Tying either to the session
  // list poll meant a 95-turn conversation re-downloaded and rebuilt its whole
  // trajectory every second while the operator read it.
  it("stops reloading a settled conversation while the list keeps polling", async () => {
    vi.useFakeTimers();
    const root: RequestRecord = { ...firstRecord, child_count: 1 };
    const summary = sessionFromRecord(root);
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [summary],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue({
      ...summary,
      turns: [root],
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [
        {
          ...secondRecord,
          id: "req_settledchildaaaaaaaaaaaaaaaaaa",
          parent_request_id: root.id,
          attempt_index: 1,
          child_count: 0,
        },
      ],
      next_cursor: null,
    });

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${root.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    expect(
      container.querySelector('[data-testid="trajectory-row"]'),
    ).not.toBeNull();

    bridgeMocks.getRequestSession.mockClear();
    bridgeMocks.listRequestRecordChildren.mockClear();
    bridgeMocks.listRequestSessions.mockClear();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });

    expect(bridgeMocks.listRequestSessions).toHaveBeenCalled();
    expect(bridgeMocks.getRequestSession).not.toHaveBeenCalled();
    expect(bridgeMocks.listRequestRecordChildren).not.toHaveBeenCalled();
  });

  // A 95-turn conversation is roughly 1400 phase rows. Mounting them all is
  // what made the trajectory tab unusable, so past a threshold the list is
  // windowed and only a screenful plus overscan reaches the DOM.
  it("windows a long trajectory instead of mounting every row", async () => {
    const sessionId = "sess_windowed_aaaaaaaaaaaaaaaaaa";
    const firstStartedAt = Date.parse("2026-07-25T10:00:00Z");
    const turns: RequestRecord[] = Array.from({ length: 90 }, (_, index) => ({
      ...firstRecord,
      id: `req_windowed_${String(index).padStart(19, "0")}`,
      session_id: sessionId,
      turn_index: index + 1,
      started_at: new Date(firstStartedAt + index * 4000).toISOString(),
      completed_at: new Date(
        firstStartedAt + index * 4000 + 2000,
      ).toISOString(),
    }));
    const summary = sessionFromRecord(turns[0]!, {
      id: sessionId,
      last_started_at: turns[turns.length - 1]!.started_at,
      completed_at: turns[turns.length - 1]!.completed_at,
      turn_count: turns.length,
      call_count: turns.length,
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [summary],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue({ ...summary, turns });

    // happy-dom has no layout, and the windowing math reads `offsetHeight` for
    // the viewport and for each row. Give it the two numbers a browser would.
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(
      function offsetHeight(this: HTMLElement) {
        if (this.dataset.testid === "trajectory-list") return 600;
        return this.dataset.index === undefined ? 0 : 32;
      },
    );

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${sessionId}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const mounted = container.querySelectorAll(
      '[data-testid="trajectory-row"]',
    );
    // Past turns fold, so 90 headers and the latest turn's 6 phases are
    // listed: 96 rows. A 600px viewport of 32px rows plus overscan mounts
    // fewer than that.
    expect(mounted.length).toBeGreaterThan(10);
    expect(mounted.length).toBeLessThan(96);

    // The scroll range still covers every listed row, so the scrollbar and
    // the call-aligned strip sync keep telling the truth.
    const spacer = container.querySelector(
      '[data-testid="trajectory-list"] > ol',
    ) as HTMLElement;
    expect(Number.parseFloat(spacer.style.height)).toBeGreaterThan(96 * 30);

    // happy-dom reports clientHeight 0, so the open-to-latest scroll stays
    // put. Selection is still resolved from the row model, and the inspector
    // opens on the newest row even though that row is offscreen.
    expect(
      container.querySelector('[data-testid="trajectory-inspector"]'),
    ).not.toBeNull();

    // The first turn is folded and at the top of the window; opening it
    // mounts its phases there.
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="TURN"]',
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });
    expect(
      container
        .querySelector('[data-testid="trajectory-inspector"]')
        ?.getAttribute("data-focus-chip"),
    ).toBe("POLICY");
  });

  it("opens a long trajectory on the latest call in the list", async () => {
    const sessionId = "sess_open_latest_aaaaaaaaaaaaaaa";
    const firstStartedAt = Date.parse("2026-07-25T10:00:00Z");
    const turns: RequestRecord[] = Array.from({ length: 90 }, (_, index) => ({
      ...firstRecord,
      id: `req_open_latest_${String(index).padStart(16, "0")}`,
      session_id: sessionId,
      turn_index: index + 1,
      started_at: new Date(firstStartedAt + index * 4000).toISOString(),
      completed_at: new Date(
        firstStartedAt + index * 4000 + 2000,
      ).toISOString(),
    }));
    const last = turns[turns.length - 1]!;
    const first = turns[0]!;
    const summary = sessionFromRecord(first, {
      id: sessionId,
      last_started_at: last.started_at,
      completed_at: last.completed_at,
      turn_count: turns.length,
      call_count: turns.length,
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [summary],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue({ ...summary, turns });

    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(
      function offsetHeight(this: HTMLElement) {
        if (this.dataset.testid === "trajectory-list") return 600;
        return this.dataset.index === undefined ? 0 : 32;
      },
    );
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(
      function clientHeight(this: HTMLElement) {
        if (this.dataset.testid === "trajectory-list") return 600;
        return 0;
      },
    );
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(
      function scrollHeight(this: HTMLElement) {
        if (this.dataset.testid === "trajectory-list") return 630 * 32;
        return 0;
      },
    );
    vi.spyOn(HTMLElement.prototype, "scrollTo").mockImplementation(
      function scrollTo(this: HTMLElement, arg?: ScrollToOptions | number) {
        if (typeof arg === "object" && arg.top !== undefined) {
          this.scrollTop = arg.top;
        }
      },
    );

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${sessionId}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    expect(
      container.querySelector(
        `[data-testid="trajectory-row"][data-row-id="${last.id}:turn"]`,
      ),
    ).not.toBeNull();
    expect(
      container.querySelector(
        `[data-testid="trajectory-row"][data-row-id="${first.id}:turn"]`,
      ),
    ).toBeNull();
  });

  it("collapses a long idle gap on the timeline and selects a phase from a segment", async () => {
    const sessionId = "sess_timeline_gap_aaaaaaaaaaaaaaaa";
    const turn1: RequestRecord = {
      ...firstRecord,
      id: "req_timeline_gap_1aaaaaaaaaaaaaaaaaaaa",
      session_id: sessionId,
      turn_index: 1,
      started_at: "2026-07-25T10:00:00Z",
      completed_at: "2026-07-25T10:00:02Z",
    };
    const turn2: RequestRecord = {
      ...firstRecord,
      id: "req_timeline_gap_2aaaaaaaaaaaaaaaaaaaa",
      session_id: sessionId,
      turn_index: 2,
      started_at: "2026-07-25T10:05:02Z",
      completed_at: "2026-07-25T10:05:22Z",
    };
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [
        sessionFromRecord(turn1, {
          last_started_at: turn2.started_at,
          completed_at: turn2.completed_at,
          turn_count: 2,
          call_count: 2,
        }),
      ],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(turn1, {
        last_started_at: turn2.started_at,
        completed_at: turn2.completed_at,
        turn_count: 2,
        call_count: 2,
      }),
      turns: [turn1, turn2],
    });

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${sessionId}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const gaps = [
      ...container.querySelectorAll('[data-testid="trajectory-gap"]'),
    ];
    expect(gaps).toHaveLength(1);
    expect(gaps[0]?.getAttribute("title")).toContain("5m 00s");

    const calls = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="trajectory-call"]',
      ),
    ];
    expect(
      new Set(calls.map((node) => node.getAttribute("data-request-id"))),
    ).toEqual(new Set([turn1.id, turn2.id]));

    // Minimum widths scale with duration against the session's own knee, which
    // is the median of the 2 s and 20 s call here, so the strip stays
    // proportional even once it outgrows the viewport and scrolls sideways.
    // The 2 s call is above the 6px clickable floor once the knee is 96px.
    const minWidthOf = (requestId: string) =>
      Number.parseFloat(
        calls
          .find((node) => node.getAttribute("data-request-id") === requestId)!
          .style.minWidth.replace("px", ""),
      );
    expect(minWidthOf(turn1.id)).toBeCloseTo(17.45, 1);
    expect(minWidthOf(turn2.id)).toBeCloseTo(153.37, 1);

    const header = container.querySelector(
      '[data-testid="trajectory-timeline"]',
    )?.previousElementSibling;
    expect(header?.textContent).toContain("客户端");
    expect(header?.textContent).toContain("网关");
    expect(header?.textContent).toContain("上游");
    expect(
      container.querySelector(
        '[data-testid="trajectory-phase"][data-chip="CLIENT"]',
      ),
    ).not.toBeNull();
    expect(
      container.querySelector(
        '[data-testid="trajectory-phase"][data-chip="POLICY"]',
      ),
    ).not.toBeNull();
    expect(
      container.querySelector(
        '[data-testid="trajectory-phase"][data-chip="UPSTREAM"]',
      ),
    ).not.toBeNull();

    const labels = [
      ...container.querySelectorAll('[data-testid="trajectory-turn-label"]'),
    ];
    expect(labels.map((label) => label.textContent)).toEqual(["1", "2"]);

    bridgeMocks.getRequestAuditContent.mockClear();
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-phase"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    const policyRow = container.querySelector(
      '[data-testid="trajectory-row"][data-chip="POLICY"]',
    );
    expect(policyRow?.getAttribute("data-selected")).toBe("true");
    expect(
      [...container.querySelectorAll('[data-testid="inspector-tab"]')].map(
        (tab) => tab.getAttribute("data-chip"),
      ),
    ).toEqual(["CLIENT", "POLICY", "ROUTE", "UPSTREAM", "RESTORE", "RESULT"]);
    expect(
      container
        .querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("POLICY");
    expect(
      container
        .querySelector('[data-testid="inspector-tab"][data-chip="POLICY"]')
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    expect(
      [
        ...container.querySelectorAll(
          '[data-testid="trajectory-row"][data-highlighted="true"]',
        ),
      ].map((row) => [
        row.getAttribute("data-request-id"),
        row.getAttribute("data-chip"),
      ]),
    ).toEqual([
      [turn1.id, "CLIENT"],
      [turn1.id, "POLICY"],
      [turn1.id, "ROUTE"],
      [turn1.id, "UPSTREAM"],
      [turn1.id, "RESTORE"],
      [turn1.id, "RESULT"],
    ]);
    expect(
      container
        .querySelector('[data-testid="trajectory-inspector"]')
        ?.getAttribute("data-focus-chip"),
    ).toBe("POLICY");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledWith(turn1.id);

    await act(async () => {
      (labels[1] as HTMLButtonElement).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    const afterTurn = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(afterTurn?.getAttribute("data-focus-chip")).toBe("TURN");
    expect(afterTurn?.getAttribute("data-request-id")).toBe(turn2.id);
  });

  it("lets the trajectory list scroll and paints HTTP 403 as a failure", async () => {
    const forbidden: RequestRecord = {
      ...firstRecord,
      http_status: 403,
      events: [
        {
          kind: "accepted",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.started_at,
          status: "succeeded",
          summary: "gpt-5.6-sol · openai.responses",
          attempt_index: 0,
        },
        {
          kind: "upstream",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "succeeded",
          summary: "HTTP 403",
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: firstRecord.completed_at ?? firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "succeeded",
          summary: "HTTP 403",
          attempt_index: 1,
        },
      ],
    };
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(forbidden)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(forbidden),
      turns: [forbidden],
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await renderRecords();
    expect(
      container.querySelector('[data-testid="request-session-row"]')
        ?.textContent,
    ).toContain("失败");
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${forbidden.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const list = container.querySelector('[data-testid="trajectory-list"]');
    expect(list?.className).toContain("overflow-y-auto");
    const failed = [
      ...container.querySelectorAll(
        '[data-testid="trajectory-row"][data-tone="failed"]',
      ),
    ];
    expect(failed.length).toBe(2);
    expect(failed.some((row) => row.textContent?.includes("结果"))).toBe(true);
    expect(
      failed.some((row) => row.querySelector(".bg-destructive") !== null),
    ).toBe(true);
    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector?.getAttribute("data-focus-chip")).toBe("RESULT");
    const http = inspector?.querySelector('[data-testid="inspector-http"]');
    expect(http?.textContent).toContain("HTTP 403");
    expect(http?.className).toContain("text-destructive");
    expect(
      container.querySelector('[data-testid="record-status"]')?.textContent,
    ).toContain("失败");
  });

  it("attributes a cancelled HTTP 200 stream to the client, not upstream", async () => {
    const aborted: RequestRecord = {
      ...firstRecord,
      status: "cancelled",
      recovery: {
        delay_ms: 0,
        stop_reason: "cancelled",
        upstream_model: "glm-5.3-flash",
      },
      events: [
        {
          kind: "accepted",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.started_at,
          status: "succeeded",
          summary: "glm-5.3-flash · anthropic.messages",
          attempt_index: 0,
        },
        {
          kind: "upstream",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "cancelled",
          summary: "HTTP 200 · 23973 → 151",
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: firstRecord.completed_at ?? firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "cancelled",
          summary: "HTTP 200 · 23973 → 151",
          attempt_index: 1,
        },
      ],
    };
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(aborted)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(aborted),
      turns: [aborted],
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${aborted.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    // The result row only turns cancelled once the session detail resolves;
    // poll so slow runners do not read the row while it still shows ok.
    const result = await vi.waitFor(() => {
      const row = container.querySelector(
        '[data-testid="trajectory-row"][data-chip="RESULT"]',
      );
      expect(row?.getAttribute("data-tone")).toBe("cancelled");
      return row;
    });
    const upstream = container.querySelector(
      '[data-testid="trajectory-row"][data-chip="UPSTREAM"]',
    );
    expect(upstream?.getAttribute("data-tone")).toBe("ok");
    expect(upstream?.textContent).toContain("HTTP 200");
    expect(upstream?.textContent).not.toContain("客户端断开");
    expect(result?.getAttribute("data-tone")).toBe("cancelled");
    expect(result?.textContent).toContain("客户端断开");
    expect(
      container.querySelector('[data-testid="trajectory-cancel-note"]')
        ?.textContent,
    ).toContain("问题在客户端");
  });

  it("labels a stopped agent loop as interrupted, not cancelled", async () => {
    const stopped = sessionFromRecord(firstRecord, {
      status: "interrupted",
      turn_count: 3,
      call_count: 12,
    });
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [stopped],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...stopped,
      turns: [firstRecord],
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await renderRecords();
    expect(
      container.querySelector('[data-testid="request-session-row"]')
        ?.textContent,
    ).toContain("已中断");

    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${stopped.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    // The badge sits beside session-scoped metrics, so it must report the
    // conversation outcome rather than the selected call's own status.
    expect(
      container.querySelector('[data-testid="record-status"]')?.textContent,
    ).toContain("已中断");
    expect(firstRecord.status).toBe("succeeded");
  });

  it("paints a privacy-blocked session differently from in-progress or interrupted", async () => {
    const blocked = sessionFromRecord(firstRecord, { status: "blocked" });
    const interrupted = sessionFromRecord(secondRecord, {
      status: "interrupted",
      id: "sess_interrupted_aaaaaaaaaaaaaaaa",
    });
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [blocked, interrupted],
      next_cursor: null,
    });

    await renderRecords();

    const blockedRow = container.querySelector(
      `[data-session-id="${blocked.id}"]`,
    );
    const interruptedRow = container.querySelector(
      `[data-session-id="${interrupted.id}"]`,
    );
    expect(blockedRow?.textContent).toContain("已拦截");
    expect(blockedRow?.querySelector('[data-tone="blocked"]')).not.toBeNull();
    expect(blockedRow?.querySelector('[data-tone="pending"]')).toBeNull();
    expect(
      interruptedRow?.querySelector('[data-tone="pending"]'),
    ).not.toBeNull();
    expect(interruptedRow?.querySelector('[data-tone="blocked"]')).toBeNull();
  });

  it("opens a side inspector for the selected call without moving the list", async () => {
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const list = container.querySelector(
      '[data-testid="trajectory-list"]',
    ) as HTMLOListElement;
    list.scrollTop = 48;
    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector).not.toBeNull();
    expect(inspector?.textContent).toContain("客户端响应");

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });

    const after = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(after?.getAttribute("data-focus-chip")).toBe("POLICY");
    expect(after?.getAttribute("data-request-id")).toBe(firstRecord.id);
    expect(
      [...after!.querySelectorAll('[data-testid="inspector-tab"]')].map((tab) =>
        tab.getAttribute("data-chip"),
      ),
    ).toEqual(["CLIENT", "POLICY", "ROUTE", "UPSTREAM", "RESTORE", "RESULT"]);
    expect(
      after
        ?.querySelector('[data-testid="inspector-tab"][data-chip="POLICY"]')
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    expect(
      after
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("POLICY");
    expect(
      after
        ?.querySelector('[data-testid="policy-inspector"]')
        ?.getAttribute("data-decision"),
    ).toBe("redact");
    expect(
      after?.querySelector('[data-testid="policy-outcome"]')?.textContent,
    ).toBe("已脱敏替换 4 处邮箱 ×2电话 ×1");
    expect(after?.textContent).not.toContain("客户端响应");
    expect(after?.textContent).not.toContain("上游响应");
    // Capture is off here: the redacted request says so once, in its pane.
    expect(
      after
        ?.querySelector('[data-testid="inspector-policy-body"]')
        ?.querySelector('[data-testid="inspector-missing-body"]')?.textContent,
    ).toContain("未捕获");
    expect(list.scrollTop).toBe(48);

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="RESULT"]',
        ) as HTMLButtonElement
      ).click();
    });
    const afterResult = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(
      afterResult
        ?.querySelector('[data-testid="inspector-tab"][data-chip="RESULT"]')
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    expect(
      afterResult
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("RESULT");
    expect(afterResult?.textContent).toContain("客户端响应");
    expect(afterResult?.textContent).not.toContain("上游响应");
  });

  it("lists recorded POLICY hits even when the captured body has no placeholders", async () => {
    const bulky = `{"input":"alice@example.com","pad":"${"x".repeat(80)}"}`;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: {
        media_type: "text/plain",
        content: "hello",
        truncated: false,
        captured_bytes: 5,
      },
      upstream_http_meta: null,
      upstream_request_body: {
        media_type: "application/json",
        content: bulky,
        truncated: false,
        captured_bytes: bulky.length,
      },
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    const hits = inspector?.querySelector('[data-testid="privacy-hits"]');
    expect(hits?.textContent).toContain("邮箱 ×2");
    expect(hits?.textContent).toContain("电话 ×1");
    expect(hits?.textContent).not.toContain("alice@");
    expect(hits?.querySelector('[data-testid="privacy-mark"]')).toBeNull();
    const pane = inspector?.querySelector('[aria-label="脱敏后请求"]');
    expect(pane?.textContent).toContain("脱敏后请求");
    expect(
      pane?.querySelector('[data-testid="inspector-policy-body"]')?.textContent,
    ).toContain("xxxxxxxx");
  });

  it("highlights captured placeholders and jumps from a recorded hit", async () => {
    const body = `{"input":"alice@example.com <PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>"}`;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: {
        media_type: "application/json",
        content: body,
        truncated: false,
        captured_bytes: body.length,
      },
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    const mark = inspector?.querySelector<HTMLElement>(
      '[data-testid="inspector-policy-body"] [data-testid="privacy-mark"][data-kind="email"]',
    );
    if (!mark) throw new Error("Missing email privacy mark");
    expect(mark.textContent).toBe("<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>");
    expect(mark.textContent).not.toContain("alice@");
    const scrollIntoView = vi.fn();
    mark.scrollIntoView = scrollIntoView;

    const emailHit = inspector?.querySelector<HTMLButtonElement>(
      '[data-testid="privacy-hits"] button[data-kind="email"]',
    );
    if (!emailHit) throw new Error("Missing email privacy hit");
    await act(async () => {
      emailHit.click();
    });
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
  });

  // A natural stand-in is indistinguishable from a real value by eye, so this
  // panel is the only place an operator can see what was substituted and
  // whether it made it back.
  it("names unrestored natural stand-ins and splits the restore channels", async () => {
    const body =
      `{"output":"mail redacted-a1b2c3d4e5f6@private.invalid ` +
      `call +1-555-555-0142"}`;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: {
        media_type: "application/json",
        content: body,
        truncated: false,
        captured_bytes: body.length,
      },
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="RESTORE"]',
        ) as HTMLButtonElement
      ).click();
    });

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    const restore = inspector?.querySelector(
      '[data-testid="inspector-section"][data-chip="RESTORE"]',
    );
    expect(
      restore?.querySelector('[data-testid="restore-channels"]')?.textContent,
    ).toBe("可见文本 5 · 工具参数 0");
    const hits = restore?.querySelector('[data-testid="privacy-hits"]');
    expect(hits?.textContent).toContain("邮箱 ×1");
    expect(hits?.textContent).toContain("电话 ×1");
    expect(hits?.textContent).toContain(
      "redacted-a1b2c3d4e5f6@private.invalid",
    );
  });

  it("counts a legacy redaction that has no recorded hit kinds", async () => {
    const legacy: RequestRecord = {
      ...firstRecord,
      privacy_restore: {
        enabled: true,
        mapping_count: 4,
        restored_count: 5,
        visible_restored_count: 0,
        tool_argument_restored_count: 0,
        fallback_count: 0,
      },
    };
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(legacy)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(legacy),
      turns: [legacy],
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${legacy.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(
      inspector?.querySelector('[data-testid="policy-outcome"]')?.textContent,
    ).toBe("已脱敏替换 4 处");
    expect(inspector?.querySelector('[data-testid="privacy-hits"]')).toBeNull();
  });

  it("shows 未命中 without repeating the client body when the policy allowed the request", async () => {
    const allowed: RequestRecord = {
      ...firstRecord,
      privacy_restore: null,
      events: [
        {
          kind: "accepted",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.started_at,
          status: "succeeded",
          summary: "gpt-4.1 · openai.responses",
          attempt_index: 1,
        },
        {
          kind: "privacy",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.started_at,
          status: "succeeded",
          summary: "allow",
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: firstRecord.started_at,
          ended_at: firstRecord.completed_at,
          status: "succeeded",
          summary: "HTTP 200",
          attempt_index: 1,
        },
      ],
    };
    const body = '{"input":"hello"}';
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: allowed.id,
      withheld: {},
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: body,
        truncated: false,
        captured_bytes: body.length,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: {
        media_type: "application/json",
        content: body,
        truncated: false,
        captured_bytes: body.length,
      },
      upstream_response_content: null,
    });
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(allowed)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValueOnce({
      ...sessionFromRecord(allowed),
      turns: [allowed],
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${allowed.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="POLICY"]',
        ) as HTMLButtonElement
      ).click();
    });

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(
      inspector?.querySelector('[data-testid="policy-outcome"]')?.textContent,
    ).toBe("未命中");
    expect(
      inspector?.querySelector('[data-testid="policy-hint"]')?.textContent,
    ).toContain("请求体见「客户端」");
    expect(
      inspector?.querySelector('[data-testid="inspector-policy-body"]'),
    ).toBeNull();
    expect(inspector?.textContent).not.toContain("hello");
    expect(inspector?.textContent).not.toContain(`${body.length} B`);
  });

  it("explains missing capture in the inspector", async () => {
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: secondRecord.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${secondRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector?.textContent).not.toContain("正在解密内容");
    expect(inspector?.textContent).toContain("未捕获");
    expect(inspector?.textContent).toContain("请求和响应捕获");
  });

  it("shows a pending client request body instead of the enable-capture hint", async () => {
    const pending: RequestRecord = {
      ...firstRecord,
      completed_at: null,
      status: "pending",
      http_status: null,
      latency_ms: null,
      audit: { ...emptyAudit, request_body_captured: true },
    };
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(pending)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue(sessionDetail(pending));
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: pending.id,
      withheld: {},
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"model":"gpt-4.1","input":"live body"}',
        truncated: false,
        captured_bytes: 40,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${pending.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="CLIENT"]',
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());

    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector?.textContent).toContain("live body");
    expect(inspector?.textContent).not.toContain("请求和响应捕获");
  });

  it("says in-progress content is not ready yet instead of asking to enable capture", async () => {
    const pending: RequestRecord = {
      ...firstRecord,
      completed_at: null,
      status: "pending",
      http_status: null,
      latency_ms: null,
      audit: { ...emptyAudit, request_body_captured: true },
    };
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(pending)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockResolvedValue(sessionDetail(pending));
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: pending.id,
      withheld: {},
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"ok":true}',
        truncated: false,
        captured_bytes: 11,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${pending.id}"]`,
        ) as HTMLButtonElement
      ).click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="RESULT"]',
        ) as HTMLButtonElement
      ).click();
    });

    const hint = container.querySelector(
      '[data-testid="inspector-missing-body"]',
    );
    expect(hint?.textContent).toContain("进行中");
    expect(hint?.textContent).not.toContain("请求和响应捕获");
  });

  it("refetches audit when a pending record's captured flags flip", async () => {
    vi.useFakeTimers();
    const pending: RequestRecord = {
      ...firstRecord,
      completed_at: null,
      status: "pending",
      http_status: null,
      latency_ms: null,
      audit: { ...emptyAudit },
    };
    const captured: RequestRecord = {
      ...pending,
      audit: { ...emptyAudit, request_body_captured: true },
    };
    let latestTurn = pending;
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(pending)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockImplementation(
      async (sessionId: string) => {
        if (sessionId === pending.id) return sessionDetail(latestTurn);
        throw new Error(`missing session ${sessionId}`);
      },
    );
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: pending.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${pending.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => {
      (
        container.querySelector(
          '[data-testid="trajectory-row"][data-chip="CLIENT"]',
        ) as HTMLButtonElement
      )?.click();
    });
    expect(
      container.querySelector('[data-testid="inspector-missing-body"]')
        ?.textContent,
    ).toContain("进行中");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);

    latestTurn = captured;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: pending.id,
      withheld: {},
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"input":"now captured"}',
        truncated: false,
        captured_bytes: 24,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector?.textContent).toContain("now captured");
  });

  it("filters locally so a later status update can still reach the row", async () => {
    await renderRecords();
    bridgeMocks.listRequestSessions.mockClear();

    await chooseOption("状态筛选", "失败");

    expect(bridgeMocks.listRequestSessions).not.toHaveBeenCalled();
    expect(
      container.querySelector(`[data-session-id="${secondRecord.id}"]`),
    ).not.toBeNull();
    expect(
      container.querySelector(`[data-session-id="${firstRecord.id}"]`),
    ).toBeNull();
  });

  it("offers the clients that sent traffic and filters by them locally", async () => {
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [
        sessionFromRecord(firstRecord, { client_type: "codex" }),
        sessionFromRecord(secondRecord),
      ],
      next_cursor: null,
    });
    await renderRecords();
    bridgeMocks.listRequestSessions.mockClear();

    await act(async () => {
      document
        .querySelector('button[role="combobox"][aria-label="客户端筛选"]')
        ?.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
      await Promise.resolve();
    });
    expect(
      [...document.querySelectorAll('[role="option"]')].map(optionText),
    ).toEqual(["全部", "Codex", "未知客户端"]);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
      await Promise.resolve();
    });

    await chooseOption("客户端筛选", "Codex");
    expect(bridgeMocks.listRequestSessions).not.toHaveBeenCalled();
    expect(
      container.querySelector(`[data-session-id="${firstRecord.id}"]`),
    ).not.toBeNull();
    expect(
      container.querySelector(`[data-session-id="${secondRecord.id}"]`),
    ).toBeNull();

    await chooseOption("客户端筛选", "未知客户端");
    expect(
      container.querySelector(`[data-session-id="${firstRecord.id}"]`),
    ).toBeNull();
    expect(
      container.querySelector(`[data-session-id="${secondRecord.id}"]`),
    ).not.toBeNull();
  });

  it("passes the stable cursor when loading earlier records", async () => {
    await renderRecords();
    bridgeMocks.listRequestSessions.mockClear();
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await act(async () => {
      exactButton("加载更早记录").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.listRequestSessions).toHaveBeenCalledWith({
      limit: 50,
      kind: "inference",
      cursor: "cursor-1",
    });
  });

  it("defaults to model calls and separates compact discovery rows with their own pagination and polling", async () => {
    vi.useFakeTimers();
    const discovery = sessionFromRecord(firstRecord, {
      id: "req_discovery",
      title: "未命名会话",
      input_protocol: "openai.models",
      requested_model: null,
      service_id: null,
    });
    const failedDiscovery = {
      ...discovery,
      id: "req_discovery_failed",
      input_protocol: "google.models",
      status: "failed" as const,
    };
    bridgeMocks.listRequestSessions.mockImplementation(async (query) => ({
      items:
        query.kind === "inference"
          ? [sessionFromRecord(firstRecord)]
          : query.kind === "discovery"
            ? [discovery, failedDiscovery]
            : [sessionFromRecord(firstRecord), discovery, failedDiscovery],
      next_cursor: query.cursor ? null : `${query.kind ?? "all"}-cursor`,
    }));
    bridgeMocks.getRequestSession.mockResolvedValue({
      ...discovery,
      turns: [
        { ...firstRecord, id: discovery.id, input_protocol: "openai.models" },
      ],
    });
    await renderRecords();
    expect(bridgeMocks.listRequestSessions).toHaveBeenCalledWith({
      limit: 50,
      kind: "inference",
    });
    expect(
      container.querySelectorAll('[data-testid="request-session-row"]'),
    ).toHaveLength(1);
    await act(async () => exactButton("模型获取").focus());
    const rows = container.querySelectorAll(
      '[data-testid="request-session-row"]',
    );
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("获取模型列表");
    expect(rows[0].textContent).toContain("GET /v1/models");
    expect(rows[0].textContent).not.toMatch(
      /未命名会话|未指定模型|正在选择服务|轮/,
    );
    expect(rows[1].textContent).toContain("失败");
    await act(async () => {
      exactButton("加载更早记录").click();
    });
    expect(bridgeMocks.listRequestSessions).toHaveBeenLastCalledWith({
      limit: 50,
      kind: "discovery",
      cursor: "discovery-cursor",
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(bridgeMocks.listRequestSessions).toHaveBeenLastCalledWith({
      limit: 50,
      kind: "discovery",
    });
    await act(async () => {
      (rows[0] as HTMLButtonElement).click();
    });
    expect(bridgeMocks.getRequestSession).toHaveBeenCalledWith(discovery.id);
    await act(async () => {
      exactButton("实时监控").click();
    });
    await act(async () => exactButton("全部").focus());
    expect(
      container.querySelectorAll('[data-testid="request-session-row"]'),
    ).toHaveLength(3);
    expect(bridgeMocks.listRequestSessions).toHaveBeenLastCalledWith({
      limit: 50,
      kind: undefined,
    });
  });

  it("discards a previous kind's in-flight page after switching views", async () => {
    const pendingPage = deferred<{
      items: RequestSession[];
      next_cursor: string | null;
    }>();
    bridgeMocks.listRequestSessions.mockReturnValueOnce(pendingPage.promise);
    await renderRecords();
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    await act(async () => exactButton("模型获取").focus());
    await act(async () =>
      pendingPage.resolve({
        items: [sessionFromRecord(firstRecord)],
        next_cursor: "old-cursor",
      }),
    );
    expect(
      container.querySelectorAll('[data-testid="request-session-row"]'),
    ).toHaveLength(0);
    expect(container.textContent).toContain("没有匹配的模型获取请求");
    expect(container.textContent).not.toContain("加载更早记录");
  });

  it("auto-decrypts on detail open, tabs metadata vs content, and caches across back-navigation", async () => {
    await renderRecords();

    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    expect(container.querySelector("h1")?.textContent).toBe("gpt-4.1");
    expect(container.textContent).toContain("追踪");
    expect(container.textContent).toContain("/v1/responses");
    expect(container.textContent).toContain("轮次");
    expect(container.textContent).toContain("客户端");
    expect(container.textContent).not.toContain(
      "POST /v1/responses?stream=true",
    );

    await act(async () => {
      exactButton("内容").click();
    });
    await act(async () => await Promise.resolve());
    expect(container.textContent).toContain("POST /v1/responses?stream=true");
    expect(container.textContent).toContain("Bearer <redacted:51 chars>");
    expect(container.textContent).toContain('"prompt": "secret"');
    expect(container.textContent).toContain("hello");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
    // The manual-decrypt gate is gone.
    expect(container.textContent).not.toContain("解密并审查内容");
    expect(container.textContent).not.toContain("内容不会自动解密");

    // Back to monitor and reopen: served from cache, no second decrypt.
    await act(async () => {
      buttonContaining("实时监控").click();
    });
    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => {
      exactButton("内容").click();
    });
    expect(container.textContent).toContain('"prompt": "secret"');
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);

    // Explicit clear drops the cache and returns to the monitor.
    await act(async () => {
      exactButton("审计").click();
    });
    await act(async () => {
      exactButton("清除已解密内容").click();
    });
    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    bridgeMocks.getRequestAuditContent.mockClear();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
  });

  it("opens on the conversation and remembers a switch to the trace", async () => {
    const openSession = async (record: RequestRecord) => {
      await act(async () => {
        (
          container.querySelector(
            `[data-session-id="${record.session_id ?? record.id}"]`,
          ) as HTMLButtonElement
        ).click();
        await Promise.resolve();
      });
      await flush();
    };
    await act(async () => {
      reactRoot.render(
        <WorkspaceSnapshotProvider sessionKey="session-1">
          <RequestRecords
            accessTokens={[]}
            accessTokensReady
            coreSessionKey="session-1"
            services={[service]}
            isReady
          />
        </WorkspaceSnapshotProvider>,
      );
      await Promise.resolve();
    });
    await flush();
    await openSession(firstRecord);
    const tabs = [
      ...container.querySelectorAll<HTMLButtonElement>(
        `[aria-label="${i18n.t("records.sections")}"] [role="tab"]`,
      ),
    ].map((tab) => tab.textContent?.trim());
    expect(tabs.slice(0, 2)).toEqual(["对话", "追踪"]);
    expect(
      container.querySelector('[data-testid="request-conversation"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="conversation-outline-toggle"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="trajectory-list"]'),
    ).toBeNull();
    await act(async () => {
      exactButton("追踪").click();
      await Promise.resolve();
    });
    await flush();
    expect(
      container.querySelector('[data-testid="trajectory-list"]'),
    ).not.toBeNull();
    await act(async () => {
      buttonContaining("实时监控").click();
      await Promise.resolve();
    });
    await openSession(secondRecord);
    expect(
      container.querySelector('[data-testid="trajectory-list"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="request-conversation"]'),
    ).toBeNull();
  });

  it("shows a friendly message for records without http metadata", async () => {
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => {
      exactButton("内容").click();
    });
    await act(async () => await Promise.resolve());

    expect(container.textContent).toContain("此记录未捕获 HTTP 元数据");
    expect(container.textContent).toContain("未捕获");
  });

  it("closes the copy menu with Escape while keeping the detail open", async () => {
    await renderRecords();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          `[data-session-id="${firstRecord.id}"]`,
        )!
        .click();
    });
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          'button[aria-label="复制与导出选项"]',
        )!
        .dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
    });
    expect(document.querySelector('[role="menu"]')).not.toBeNull();
    await act(async () => {
      document.querySelector('[role="menu"]')!.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
    });
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(container.querySelector("#request-detail-heading")).not.toBeNull();
  });

  it("exports the decrypted bundle as a txt file from the copy split button", async () => {
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const trigger = container.querySelector<HTMLButtonElement>(
      'button[aria-label="复制与导出选项"]',
    );
    if (!trigger) throw new Error("Missing export menu");
    await act(async () => {
      trigger.dispatchEvent(
        new PointerEvent("pointerdown", {
          bubbles: true,
          button: 0,
          pointerType: "mouse",
        }),
      );
      await Promise.resolve();
    });

    const item = [
      ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
    ].find((candidate) => candidate.textContent?.trim() === "导出为 TXT 文件");
    if (!item) throw new Error("Missing export menu item");
    await act(async () => {
      item.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.saveTextFile).toHaveBeenCalledTimes(1);
    const [filename, content] = bridgeMocks.saveTextFile.mock.calls[0] ?? [];
    expect(filename).toBe(`astrlink-${firstRecord.id}.txt`);
    expect(content).toContain('{"prompt":"secret"}');
    expect(content).toContain("hello");
    expect(content).toContain("已截断");
    // The file alone has to carry what a diagnosis needs.
    expect(content).toContain("版本: 应用 0.9.0 · 核心 0.9.0 · 提交 abc1234");
    expect(content).toContain("执行轨迹");
    expect(content).toContain("同会话请求");
    expect(content).toContain(
      "隐私保护: 已开启 · 检测方式: local_model · 本地模型: Privacy Filter · Q4",
    );
    expect(content).toContain(
      "跳过函数调用检查: 已关闭 · 跳过 additional_tools 检查: 已开启",
    );
    expect(content).toContain("响应开始超时: 120 秒 · 并发检测数: 1");
    expect(content).toContain("内容捕获: 请求体 已关闭");
    expect(content).toContain("机器可读诊断（JSON）");
    expect(content).not.toContain("# AstrLink");
    expect(content).not.toContain("## ");
    expect(content).not.toContain("```");
    expect(notifyMocks.success).toHaveBeenCalledWith(
      `已导出 /tmp/astrlink-${firstRecord.id}.txt`,
    );
  });

  it("does not toast when the save dialog is cancelled", async () => {
    bridgeMocks.saveTextFile.mockResolvedValue(null);
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    const trigger = container.querySelector<HTMLButtonElement>(
      'button[aria-label="复制与导出选项"]',
    );
    if (!trigger) throw new Error("Missing export menu");
    await act(async () => {
      trigger.dispatchEvent(
        new PointerEvent("pointerdown", {
          bubbles: true,
          button: 0,
          pointerType: "mouse",
        }),
      );
      await Promise.resolve();
    });

    const item = [
      ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
    ].find((candidate) => candidate.textContent?.trim() === "导出为 TXT 文件");
    if (!item) throw new Error("Missing export menu item");
    await act(async () => {
      item.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.saveTextFile).toHaveBeenCalledTimes(1);
    expect(notifyMocks.success).not.toHaveBeenCalled();
    expect(notifyMocks.error).not.toHaveBeenCalled();
  });

  it("preserves filters, scroll offset, selection and focus across drill-down", async () => {
    const requestAnimationFrame = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((callback) => {
        callback(0);
        return 1;
      });
    await renderRecords();
    await chooseOption("状态筛选", "完成");
    const scroller = container.querySelector(
      '[data-testid="request-records-scroll"]',
    );
    if (!(scroller instanceof HTMLDivElement)) {
      throw new Error("Missing monitor scroller");
    }
    scroller.scrollTop = 180;
    scroller.dispatchEvent(new Event("scroll", { bubbles: true }));

    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => buttonContaining("实时监控").click());

    expect(
      document.querySelector('[aria-label="状态筛选"]')?.textContent,
    ).toContain("完成");
    expect(scroller.scrollTop).toBe(180);
    expect(document.activeElement?.getAttribute("data-session-id")).toBe(
      firstRecord.id,
    );
    expect(requestAnimationFrame).toHaveBeenCalled();
  });

  it("saves MiB inputs as bytes without changing body capture", async () => {
    await renderRecords();
    await act(async () => exactButton("审计设置").click());
    const dialog = document.querySelector('[role="dialog"]')!;
    const inputs = [
      ...dialog.querySelectorAll<HTMLInputElement>('input[type="number"]'),
    ];
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!;
    await act(async () => {
      for (const [index, value] of ["1.5", "64", "3650", "365"].entries()) {
        setValue.call(inputs[index], value);
        inputs[index].dispatchEvent(new Event("input", { bubbles: true }));
      }
      dialog.querySelector<HTMLButtonElement>('[role="switch"]')!.click();
    });
    await act(async () => exactButton("保存", dialog).click());
    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      http_meta_enabled: false,
      request_body_max_bytes: 1_572_864,
      response_content_max_bytes: 67_108_864,
      metadata_retention_days: 3650,
      content_retention_days: 365,
    });
    expect(dialog.textContent).toContain("审计设置已保存");
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("preserves byte-exact limits when only retention changes", async () => {
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: false,
      response_content_enabled: false,
      http_meta_enabled: true,
      request_body_max_bytes: 1_048_577,
      response_content_max_bytes: 4_194_305,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
    });
    await renderRecords();
    await act(async () => exactButton("审计设置").click());
    const dialog = document.querySelector('[role="dialog"]')!;
    const inputs = [
      ...dialog.querySelectorAll<HTMLInputElement>('input[type="number"]'),
    ];
    expect(Number(inputs[0].value) * 1_048_576).toBe(1_048_577);
    expect(Number(inputs[1].value) * 1_048_576).toBe(4_194_305);
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(inputs[2], "60");
      inputs[2].dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => exactButton("保存", dialog).click());
    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      metadata_retention_days: 60,
    });
  });

  it("does not treat the capture switch as off while audit settings are loading", async () => {
    const pending = deferred<AuditSettings>();
    bridgeMocks.getAuditSettings.mockReturnValueOnce(pending.promise);

    await act(async () => {
      reactRoot.render(
        <RequestRecords
          accessTokens={[]}
          accessTokensReady
          coreSessionKey="session-1"
          services={[service]}
          isReady
        />,
      );
      await Promise.resolve();
    });

    expect(
      document.querySelector('[role="switch"][aria-label="请求和响应捕获"]'),
    ).toBeNull();
    expect(container.textContent).toContain("请求和响应捕获");

    await act(async () => {
      pending.resolve({
        request_body_enabled: true,
        response_content_enabled: true,
        http_meta_enabled: true,
        request_body_max_bytes: 4096,
        response_content_max_bytes: 8192,
        metadata_retention_days: 30,
        content_retention_days: 7,
        agent_raw_access_enabled: true,
      });
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(
      document
        .querySelector('[role="switch"][aria-label="请求和响应捕获"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("uses a second in-app step for capture risk and never calls window.confirm", async () => {
    await renderRecords();
    await act(async () => await Promise.resolve());

    const toggle = document.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="请求和响应捕获"]',
    );
    if (!(toggle instanceof HTMLButtonElement)) {
      throw new Error("Missing request/response capture switch");
    }
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await act(async () => toggle.click());

    expect(
      document.querySelector('[role="alertdialog"]')?.textContent,
    ).toContain("确认开启正文捕获");
    await act(async () => {
      exactButton("确认开启").click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      request_body_enabled: true,
      response_content_enabled: true,
      audit_risk_acknowledged: true,
    });
    expect(window.confirm).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith("已开启请求和响应捕获。");
    expect(
      document
        .querySelector('[role="switch"][aria-label="请求和响应捕获"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("sets a raw password in the capture confirmation when no raw key exists", async () => {
    bridgeMocks.getRawSealingStatus.mockResolvedValue(
      rawSealing(rawUnconfigured),
    );
    bridgeMocks.setRawPassword
      .mockRejectedValueOnce(
        new Error(
          'POST /v1/raw-sealing/password returned 400 Bad Request: {"error":{"code":"validation_failed"}}',
        ),
      )
      .mockResolvedValueOnce({
        outcome: "sealing",
        status: rawStatus(),
        reset: null,
      });
    await renderRecords();
    await flush(1);

    await act(async () => {
      captureSwitch().click();
      await Promise.resolve();
    });
    await flush();

    const dialog = proofDialog();
    expect(dialog?.textContent).toContain("确认开启正文捕获");
    expect(dialog?.textContent).toContain("开启前先设置原文保护");
    expect(exactButton("确认开启", dialog!).disabled).toBe(true);

    await typeNewRawPassword("correct horse");
    await act(async () => {
      exactButton("确认开启", dialog!).click();
      await Promise.resolve();
    });
    await flush();
    expect(bridgeMocks.setRawPassword).toHaveBeenLastCalledWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(proofDialog()?.textContent).toContain("口令不符合长度要求");
    expect(bridgeMocks.updateAuditSettings).not.toHaveBeenCalled();
    expect(
      [
        ...document.querySelectorAll<HTMLInputElement>(
          'input[autocomplete="new-password"]',
        ),
      ].map((input) => input.value),
    ).toEqual(["", ""]);

    await typeNewRawPassword("correct horse battery");
    await act(async () => {
      exactButton("确认开启", proofDialog()!).click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.setRawPassword).toHaveBeenLastCalledWith(
      "set",
      "correct horse battery",
      undefined,
    );
    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledExactlyOnceWith({
      request_body_enabled: true,
      response_content_enabled: true,
      audit_risk_acknowledged: true,
    });
    expect(notifyMocks.success).toHaveBeenCalledWith("已开启请求和响应捕获。");
    expect(notifyMocks.success).not.toHaveBeenCalledWith("已设置口令");
    expect(proofDialog()).toBeNull();
    expect(captureSwitch().getAttribute("aria-checked")).toBe("true");
    expect(document.querySelector('[data-slot="raw-password-dot"]')).toBeNull();
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("keeps capture off when the raw sealing state cannot be read", async () => {
    bridgeMocks.getRawSealingStatus.mockRejectedValue(
      new Error("GET /v1/raw-sealing returned 503 Service Unavailable"),
    );
    await renderRecords();
    await flush(1);

    await act(async () => {
      captureSwitch().click();
      await Promise.resolve();
    });
    await flush();

    expect(proofDialog()).toBeNull();
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(bridgeMocks.updateAuditSettings).not.toHaveBeenCalled();
    expect(notifyMocks.error).toHaveBeenCalledWith(
      "无法读取原文封存状态，正文捕获保持关闭，请重试。",
    );
    expect(captureSwitch().getAttribute("aria-checked")).toBe("false");
  });

  it("does not decide capture on a sealing state it could not refresh", async () => {
    // The page read a raw key earlier; it may have been reset since.
    let readable = true;
    bridgeMocks.getRawSealingStatus.mockImplementation(async () => {
      if (!readable) {
        throw new Error("GET /v1/raw-sealing returned 503 Service Unavailable");
      }
      return rawSealing();
    });
    await renderRecords();
    await flush(1);
    readable = false;

    await act(async () => {
      captureSwitch().click();
      await Promise.resolve();
    });
    await flush();

    expect(proofDialog()).toBeNull();
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(bridgeMocks.updateAuditSettings).not.toHaveBeenCalled();
    expect(notifyMocks.error).toHaveBeenCalledWith(
      "无法读取原文封存状态，正文捕获保持关闭，请重试。",
    );
    expect(captureSwitch().getAttribute("aria-checked")).toBe("false");
  });

  it("leaves capture off when the raw password step is cancelled", async () => {
    bridgeMocks.getRawSealingStatus.mockResolvedValue(
      rawSealing(rawUnconfigured),
    );
    await renderRecords();
    await flush(1);

    await act(async () => {
      captureSwitch().click();
      await Promise.resolve();
    });
    await flush();
    await typeNewRawPassword("correct horse");
    await act(async () => exactButton("取消", proofDialog()!).click());
    await flush(1);

    expect(bridgeMocks.setRawPassword).not.toHaveBeenCalled();
    expect(bridgeMocks.updateAuditSettings).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith("已取消开启正文捕获。");
    expect(captureSwitch().getAttribute("aria-checked")).toBe("false");
  });

  it("points a capturing user without a raw password to one", async () => {
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: true,
      response_content_enabled: true,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
    });
    bridgeMocks.getRawSealingStatus.mockResolvedValue(
      rawSealing(rawUnconfigured),
    );
    bridgeMocks.setRawPassword.mockResolvedValue({
      outcome: "sealing",
      status: rawStatus(),
      reset: null,
    });
    await renderRecords();
    await flush(1);

    const settingsButton = exactButton("审计设置");
    expect(
      settingsButton.querySelector('[data-slot="raw-password-dot"]'),
    ).not.toBeNull();
    expect(settingsButton.title).toBe(
      "设置完成前，新请求的原文不会保存。请求转发不受影响。",
    );

    await act(async () => settingsButton.click());
    await flush();
    let panel = document.querySelector<HTMLElement>(
      '[data-slot="raw-password-panel"]',
    )!;
    expect(panel.querySelector('[data-slot="raw-password-missing"]')).not.toBe(
      null,
    );
    expect(
      panel.querySelector<HTMLButtonElement>('[role="switch"]')?.disabled,
    ).toBe(true);

    await act(async () => exactButton("开始设置", panel).click());
    await flush(1);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(proofDialog()?.textContent).toContain("保护请求原文");

    await typeNewRawPassword("correct horse");
    await act(async () => {
      exactButton("设置口令并继续", proofDialog()!).click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(notifyMocks.success).toHaveBeenCalledWith("已设置口令");
    expect(bridgeMocks.updateAuditSettings).not.toHaveBeenCalled();
    panel = document.querySelector<HTMLElement>(
      '[role="dialog"] [data-slot="raw-password-panel"]',
    )!;
    expect(
      panel.querySelector('[data-slot="raw-password-state"]')?.textContent,
    ).toBe("口令");
    expect(
      panel.querySelector('[data-slot="raw-password-missing"]'),
    ).toBeNull();
    const agentSwitch =
      panel.querySelector<HTMLButtonElement>('[role="switch"]')!;
    expect(agentSwitch.disabled).toBe(false);
    expect(agentSwitch.getAttribute("aria-checked")).toBe("true");
    expect(
      exactButton("审计设置").querySelector('[data-slot="raw-password-dot"]'),
    ).toBeNull();
  });

  it("saves the agent raw access switch with the audit settings", async () => {
    await renderRecords();
    await act(async () => exactButton("审计设置").click());
    await flush();
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    const agentSwitch = dialog.querySelector<HTMLButtonElement>(
      '[data-slot="raw-password-panel"] [role="switch"]',
    )!;
    expect(agentSwitch.getAttribute("aria-checked")).toBe("true");

    await act(async () => agentSwitch.click());
    await act(async () => exactButton("保存", dialog).click());
    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      agent_raw_access_enabled: false,
    });
  });

  it("shows a raw-locked part, unlocks it and locks it again", async () => {
    const empty = {
      request_id: firstRecord.id,
      http_meta: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    };
    bridgeMocks.getRequestAuditContent
      .mockResolvedValueOnce({
        ...empty,
        request_body: null,
        withheld: {
          request_body: {
            reason: "raw_locked",
            raw_available: true,
            media_type: "application/json",
            truncated: false,
            captured_bytes: 2048,
          },
        },
      })
      .mockResolvedValueOnce({
        ...empty,
        request_body: {
          media_type: "application/json",
          content: '{"prompt":"sealed secret"}',
          truncated: false,
          captured_bytes: 26,
        },
        withheld: {},
      });
    // Core keeps the unlock session; later status reads report it.
    let unlocked = false;
    bridgeMocks.getRawSealingStatus.mockImplementation(async () =>
      rawSealing({ unlocked }),
    );
    bridgeMocks.unlockRaw.mockImplementation(async () => {
      unlocked = true;
      return {
        outcome: "sealing",
        status: rawStatus({ unlocked }),
        reset: null,
      };
    });
    bridgeMocks.lockRaw.mockImplementation(async () => {
      unlocked = false;
      return rawStatus({ unlocked });
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await flush();
    await act(async () => exactButton("内容").click());
    await flush(1);

    const withheld = container.querySelector<HTMLElement>(
      '[data-slot="audit-part-withheld"]',
    );
    expect(withheld?.textContent).toContain("已锁定");
    expect(withheld?.textContent).toContain(
      "原文已封存，输入原文口令解锁后才能查看。",
    );

    await act(async () => {
      exactButton("解锁").click();
      await Promise.resolve();
    });
    await flush();
    expect(proofDialog()?.textContent).toContain("解锁原文");
    expect(proofDialog()?.textContent).toContain("15 分钟无操作会自动锁定");
    await typeInto(
      proofDialog()!.querySelector<HTMLInputElement>('input[type="password"]')!,
      "correct horse",
    );
    await act(async () => {
      exactButton("解锁", proofDialog()!).click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.unlockRaw).toHaveBeenCalledExactlyOnceWith({
      kind: "password",
      password: "correct horse",
    });
    expect(notifyMocks.success).toHaveBeenCalledWith("原文已解锁");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain("sealed secret");
    expect(
      container.querySelector('[data-slot="audit-part-withheld"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-slot="raw-unlocked"]')?.textContent,
    ).toContain("15 分钟无操作后自动锁定");

    await act(async () => {
      exactButton("锁定").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.lockRaw).toHaveBeenCalledOnce();
    expect(notifyMocks.success).toHaveBeenCalledWith("原文已锁定");
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(3);
    expect(container.querySelector('[data-slot="raw-unlocked"]')).toBeNull();
  });

  it("never caches unlocked raw parts and drops them when the unlock ends", async () => {
    vi.setSystemTime(new Date("2026-09-29T10:00:00Z"));
    const empty = {
      request_id: firstRecord.id,
      http_meta: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    };
    let unlocked = true;
    bridgeMocks.getRawSealingStatus.mockImplementation(async () =>
      rawSealing(
        unlocked
          ? { unlocked, unlock_expires_at: "2026-09-29T10:15:00Z" }
          : { unlocked },
      ),
    );
    bridgeMocks.getRequestAuditContent.mockImplementation(async () =>
      unlocked
        ? {
            ...empty,
            request_body: {
              media_type: "application/json",
              content: '{"prompt":"sealed secret"}',
              truncated: false,
              captured_bytes: 26,
              exposure: "raw",
            },
            withheld: {},
          }
        : {
            ...empty,
            request_body: null,
            withheld: {
              request_body: {
                reason: "raw_locked",
                raw_available: true,
                media_type: "application/json",
                truncated: false,
                captured_bytes: 26,
              },
            },
          },
    );
    const openContent = async () => {
      await act(async () => {
        (
          container.querySelector(
            `[data-session-id="${firstRecord.id}"]`,
          ) as HTMLButtonElement
        ).click();
      });
      await flush();
      await act(async () => exactButton("内容").click());
      await flush(1);
    };
    await renderRecords();
    await openContent();
    expect(container.textContent).toContain("sealed secret");

    // Reopening reads Core again instead of a cached raw part.
    await act(async () => buttonContaining("实时监控").click());
    await openContent();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);

    // Core ends the idle unlock; the page checks when it was due to end.
    unlocked = false;
    await act(async () => {
      vi.advanceTimersByTime(15 * 60 * 1000 + 2000);
      await Promise.resolve();
    });
    await flush();
    expect(container.textContent).not.toContain("sealed secret");
    expect(
      container.querySelector('[data-slot="audit-part-withheld"]')?.textContent,
    ).toContain("已锁定");
  });

  it("drops raw parts a lock ends while the detail view is closed", async () => {
    vi.setSystemTime(new Date("2026-09-29T10:00:00Z"));
    let unlocked = true;
    bridgeMocks.getRawSealingStatus.mockImplementation(async () =>
      rawSealing(
        unlocked
          ? { unlocked, unlock_expires_at: "2026-09-29T10:15:00Z" }
          : { unlocked },
      ),
    );
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"prompt":"sealed secret"}',
        truncated: false,
        captured_bytes: 26,
        exposure: "raw",
      },
      // The detail view opens on the trajectory, whose inspector starts on
      // the last phase: the response.
      response_content: {
        media_type: "application/json",
        content: '{"output_text":"sealed secret"}',
        truncated: false,
        captured_bytes: 31,
        exposure: "raw",
      },
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
      withheld: {},
    });
    const openDetail = async () => {
      await act(async () => {
        (
          container.querySelector(
            `[data-session-id="${firstRecord.id}"]`,
          ) as HTMLButtonElement
        ).click();
      });
      await flush();
    };
    await renderRecords();
    await openDetail();
    expect(container.textContent).toContain("sealed secret");
    await act(async () => buttonContaining("实时监控").click());

    // The unlock ends while the list is showing.
    unlocked = false;
    await act(async () => {
      vi.advanceTimersByTime(15 * 60 * 1000 + 2000);
      await Promise.resolve();
    });
    await flush();

    // Reopening must not hand the old raw part to the detail view, even for
    // the one render before the new read starts.
    bridgeMocks.getRequestAuditContent.mockImplementation(
      () => new Promise(() => {}),
    );
    previewParts.parsed = [];
    await openDetail();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    expect(previewParts.parsed.join("\n")).not.toContain("sealed secret");
    expect(container.textContent).not.toContain("sealed secret");
  });

  it("asks for raw protection instead of an unlock no proof can open", async () => {
    bridgeMocks.getRawSealingStatus.mockResolvedValue(
      rawSealing({
        configured: false,
        password_set: false,
        password_required: true,
        envelopes: [],
      }),
    );
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
      withheld: {
        request_body: {
          reason: "raw_locked",
          raw_available: true,
          media_type: "application/json",
          truncated: false,
          captured_bytes: 64,
        },
      },
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await flush();
    await act(async () => exactButton("内容").click());
    await flush(1);

    await act(async () => {
      exactButton("解锁").click();
      await Promise.resolve();
    });
    await flush();

    // Only the raw password unlocks raw content, so setting one is left.
    const dialog = proofDialog();
    expect(dialog?.textContent).toContain("保护请求原文");
    expect(exactButton("设置口令并继续", dialog!)).toBeTruthy();
    expect(
      dialog?.querySelector('input[autocomplete="current-password"]'),
    ).toBe(null);
    expect(bridgeMocks.unlockRaw).not.toHaveBeenCalled();
  });

  it("keeps raw content locked when the sealing state cannot be read", async () => {
    let readable = true;
    bridgeMocks.getRawSealingStatus.mockImplementation(async () => {
      if (!readable) {
        throw new Error("GET /v1/raw-sealing returned 503 Service Unavailable");
      }
      return rawSealing();
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
      withheld: {
        request_body: {
          reason: "raw_locked",
          raw_available: true,
          media_type: "application/json",
          truncated: false,
          captured_bytes: 64,
        },
      },
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await flush();
    await act(async () => exactButton("内容").click());
    await flush(1);
    readable = false;

    await act(async () => {
      exactButton("解锁").click();
      await Promise.resolve();
    });
    await flush();

    expect(proofDialog()).toBeNull();
    expect(bridgeMocks.unlockRaw).not.toHaveBeenCalled();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
    expect(notifyMocks.error).toHaveBeenCalledWith(
      "无法读取原文封存状态，原文保持锁定，请重试。",
    );

    // Asking again reads the state again.
    readable = true;
    await act(async () => {
      exactButton("解锁").click();
      await Promise.resolve();
    });
    await flush();
    expect(proofDialog()?.textContent).toContain("解锁原文");
  });

  it("turns off request and response capture from the header switch", async () => {
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: true,
      response_content_enabled: true,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
    });
    await renderRecords();
    await act(async () => await Promise.resolve());

    const toggle = document.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="请求和响应捕获"]',
    );
    if (!(toggle instanceof HTMLButtonElement)) {
      throw new Error("Missing request/response capture switch");
    }
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => {
      toggle.click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      request_body_enabled: false,
      response_content_enabled: false,
    });
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(notifyMocks.success).toHaveBeenCalledWith("已关闭请求和响应捕获。");
  });

  it("purges records through two in-app dialogs", async () => {
    await renderRecords();
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await act(async () => exactButton("清理…").click());
    await act(async () => exactButton("执行清理").click());
    expect(
      document.querySelector('[role="alertdialog"]')?.textContent,
    ).toContain("确定清空全部");
    await act(async () => {
      exactButton("确定清理").click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.purgeRequestRecords).toHaveBeenCalledWith({
      scope: "all",
    });
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "已删除 2 条记录、1 个加密内容块。",
    );
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("updates a pending detail in place while queuing new requests off-monitor", async () => {
    vi.useFakeTimers();
    const pending: RequestRecord = {
      ...firstRecord,
      completed_at: null,
      status: "pending",
      http_status: null,
      latency_ms: null,
      usage: null,
      audit: { ...emptyAudit },
    };
    const completed: RequestRecord = {
      ...pending,
      completed_at: "2026-07-25T10:00:02Z",
      status: "succeeded",
      http_status: 200,
      latency_ms: 2000,
      usage: { input_tokens: 4, output_tokens: 8, total_tokens: 12 },
    };
    const newer: RequestRecord = {
      ...pending,
      id: "req_cccccccccccccccccccccccccccccccc",
      started_at: "2026-07-25T10:01:00Z",
      requested_model: "gpt-new",
    };
    let latestTurn = pending;
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(pending)],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockImplementation(
      async (sessionId: string) => {
        if (sessionId === pending.id) return sessionDetail(latestTurn);
        if (sessionId === newer.id) return sessionDetail(newer);
        throw new Error(`missing session ${sessionId}`);
      },
    );
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-session-id="${pending.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    expect(container.textContent).toContain("进行中");

    latestTurn = completed;
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(newer), sessionFromRecord(completed)],
      next_cursor: null,
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(container.textContent).toContain("完成");

    await act(async () => buttonContaining("实时监控").click());
    expect(buttonContaining("1 条新记录").textContent).toContain("1 条新记录");
    await act(async () => buttonContaining("1 条新记录").click());
    const rows = [
      ...container.querySelectorAll('[data-testid="request-session-row"]'),
    ];
    expect(rows[0].textContent).toContain("gpt-new");
  });

  it("queues new rows when scrolled away and prepends directly when following the top", async () => {
    vi.useFakeTimers();
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(firstRecord)],
      next_cursor: null,
    });
    await renderRecords();
    const scroller = container.querySelector(
      '[data-testid="request-records-scroll"]',
    );
    if (!(scroller instanceof HTMLDivElement)) {
      throw new Error("Missing monitor scroller");
    }
    scroller.scrollTop = 120;
    scroller.dispatchEvent(new Event("scroll", { bubbles: true }));
    const queuedRecord: RequestRecord = {
      ...firstRecord,
      id: "req_queuedddddddddddddddddddddddddddd",
      started_at: "2026-07-25T10:02:00Z",
      requested_model: "gpt-queued",
    };
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(queuedRecord), sessionFromRecord(firstRecord)],
      next_cursor: null,
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(
      container.querySelector(`[data-session-id="${queuedRecord.id}"]`),
    ).toBeNull();
    expect(buttonContaining("1 条新记录")).not.toBeNull();
    await act(async () => buttonContaining("1 条新记录").click());
    expect(
      container.querySelector(`[data-session-id="${queuedRecord.id}"]`),
    ).not.toBeNull();

    scroller.scrollTop = 0;
    scroller.dispatchEvent(new Event("scroll", { bubbles: true }));
    const followingRecord: RequestRecord = {
      ...queuedRecord,
      id: "req_followingeeeeeeeeeeeeeeeeeeeeeeeee",
      started_at: "2026-07-25T10:03:00Z",
      requested_model: "gpt-following",
    };
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [
        sessionFromRecord(followingRecord),
        sessionFromRecord(queuedRecord),
        sessionFromRecord(firstRecord),
      ],
      next_cursor: null,
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(buttonContaining("gpt-following")).not.toBeNull();
    expect(
      [...container.querySelectorAll("button")].some((button) =>
        button.textContent?.includes("条新记录"),
      ),
    ).toBe(false);
  });

  it("keeps old rows visible after three consecutive poll failures", async () => {
    vi.useFakeTimers();
    await renderRecords();
    bridgeMocks.listRequestSessions.mockRejectedValue(new Error("offline"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3100);
    });

    expect(container.textContent).toContain("实时同步暂时中断");
    expect(container.textContent).toContain("gpt-4.1");
  });

  it("hides control-plane transport URLs on the first list failure", async () => {
    bridgeMocks.listRequestSessions.mockRejectedValue(
      new Error(
        "GET /control/v1/request-sessions?limit=50 failed: error sending request for url (http://127.0.0.1:62240/control/v1/request-sessions?limit=50)",
      ),
    );
    await renderRecords();

    expect(container.textContent).toContain("无法读取请求记录");
    expect(container.textContent).toContain("控制面暂时连不上，正在重试。");
    expect(container.textContent).not.toContain("127.0.0.1");
    expect(container.textContent).not.toContain("error sending request");
  });

  it("clears the full-page list error after a later poll succeeds", async () => {
    vi.useFakeTimers();
    bridgeMocks.listRequestSessions.mockRejectedValueOnce(
      new Error(
        "GET /control/v1/request-sessions?limit=50 failed: error sending request for url (http://127.0.0.1:1/control/v1/request-sessions?limit=50)",
      ),
    );
    await renderRecords();
    expect(container.textContent).toContain("无法读取请求记录");

    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [sessionFromRecord(firstRecord)],
      next_cursor: null,
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(container.textContent).not.toContain("无法读取请求记录");
    expect(container.textContent).toContain("gpt-4.1");
  });
});
