// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  deleteRequestRecord: vi.fn(),
  getAuditSettings: vi.fn(),
  getRequestAuditContent: vi.fn(),
  getRequestRecord: vi.fn(),
  getRequestSession: vi.fn(),
  listRequestRecordChildren: vi.fn(),
  listRequestRecords: vi.fn(),
  listRequestSessions: vi.fn(),
  purgeRequestRecords: vi.fn(),
  saveTextFile: vi.fn(),
  updateAuditSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import type { AuditSettings } from "./audit-settings-model";
import { RequestRecords } from "./RequestRecords";
import {
  displayRequestStatus,
  emptyTrajectoryFields,
  type RequestRecord,
  type RequestSession,
} from "./request-record-model";
import type { RoutableService } from "./service-model";

const service: RoutableService = {
  id: "service_01",
  name: "Primary gateway",
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
  const item = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(
    (candidate) => candidate.textContent?.trim() === option,
  );
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
    bridgeMocks.getRequestSession.mockImplementation(async (sessionId: string) => {
      const record = [firstRecord, secondRecord].find(
        (item) => (item.session_id ?? item.id) === sessionId,
      );
      if (!record) throw new Error(`missing session ${sessionId}`);
      return sessionDetail(record);
    });
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: false,
      response_content_enabled: false,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
    });
    bridgeMocks.updateAuditSettings.mockImplementation(async (patch) => ({
      request_body_enabled: false,
      response_content_enabled: false,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
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

  const renderRecords = async (session = "session-1") => {
    await act(async () => {
      reactRoot.render(
        <RequestRecords
          coreSessionKey={session}
          services={[service]}
          isReady
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
  };

  it("renders a session stream instead of a wide table", async () => {
    await renderRecords();

    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    expect(container.querySelectorAll('[data-slot="page-header"]')).toHaveLength(1);
    expect(container.querySelector("table")).toBeNull();
    expect(container.querySelectorAll('[data-testid="request-session-row"]')).toHaveLength(2);
    expect(container.textContent).toContain("gpt-4.1");
    expect(container.textContent).toContain("/v1/responses");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("1 轮");
    expect(container.textContent).not.toContain("次调用");
    expect(container.textContent).toContain("1.0 s");
    expect(container.textContent).toContain("2.0 s");
    expect(container.textContent).not.toMatch(/\d{3,}m /);
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

    await renderRecords();
    expect(container.textContent).toContain("1 轮 · 3 次调用");
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
    expect(container.textContent).toContain("RETRY");
    expect(container.textContent).toContain("子请求 1");
    expect(container.textContent).toContain("子请求 2");
    expect(container.querySelectorAll('[data-testid="trajectory-row"]').length).toBeGreaterThan(0);

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
      ...container.querySelectorAll('[data-testid="trajectory-row"][data-tone="failed"]'),
    ];
    expect(failed.length).toBe(2);
    expect(failed.some((row) => row.textContent?.includes("RESULT"))).toBe(true);
    expect(
      failed.some((row) => row.querySelector(".bg-destructive") !== null),
    ).toBe(true);
    const inspector = container.querySelector(
      '[data-testid="trajectory-inspector"]',
    );
    expect(inspector?.getAttribute("data-chip")).toBe("RESULT");
    const http = inspector?.querySelector('[data-testid="inspector-http"]');
    expect(http?.textContent).toContain("HTTP 403");
    expect(http?.className).toContain("text-destructive");
    expect(
      container.querySelector('[data-testid="record-status"]')?.textContent,
    ).toContain("失败");
  });

  it("opens a side inspector for the selected phase without moving the list", async () => {
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
    expect(after?.getAttribute("data-chip")).toBe("POLICY");
    expect(after?.textContent).toContain("命中");
    expect(after?.textContent).toContain("邮箱 ×2");
    expect(after?.textContent).toContain("电话 ×1");
    expect(after?.querySelector('[data-testid="redacted-request-details"]')).toBeNull();
    expect(list.scrollTop).toBe(48);
  });

  it("lists recorded POLICY hits even when the captured body has no placeholders", async () => {
    const bulky = `{"input":"alice@example.com","pad":"${"x".repeat(80)}"}`;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
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
    const details = inspector?.querySelector(
      '[data-testid="redacted-request-details"]',
    ) as HTMLDetailsElement | null;
    expect(details).not.toBeNull();
    expect(details?.open).toBe(false);
    expect(details?.textContent).toContain("脱敏后请求");
    expect(details?.textContent).toContain("xxxxxxxx");
  });

  it("highlights captured placeholders and jumps from a recorded hit", async () => {
    const body = `{"input":"alice@example.com <PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>"}`;
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
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
    const details = inspector?.querySelector(
      '[data-testid="redacted-request-details"]',
    ) as HTMLDetailsElement | null;
    expect(details?.open).toBe(false);
    const mark = inspector?.querySelector(
      '[data-testid="privacy-mark"][data-kind="email"]',
    );
    expect(mark?.textContent).toBe("<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>");
    expect(mark?.textContent).not.toContain("alice@");

    await act(async () => {
      (
        inspector?.querySelector(
          '[data-testid="privacy-hits"] button[data-kind="email"]',
        ) as HTMLButtonElement
      ).click();
    });
    expect(details?.open).toBe(true);
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
    expect(
      inspector?.querySelector('[data-testid="restore-channels"]')?.textContent,
    ).toBe("可见文本 5 · 工具参数 0");
    const hits = inspector?.querySelector('[data-testid="privacy-hits"]');
    expect(hits?.textContent).toContain("邮箱 ×1");
    expect(hits?.textContent).toContain("电话 ×1");
    expect(hits?.textContent).toContain("redacted-a1b2c3d4e5f6@private.invalid");
  });

  it("shows 未命中 when a POLICY row has no recorded hit kinds", async () => {
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
    expect(inspector?.textContent).toContain("未命中");
    expect(inspector?.querySelector('[data-testid="privacy-hits"]')).toBeNull();
  });

  it("explains missing capture in the inspector", async () => {
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: secondRecord.id,
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
      cursor: "cursor-1",
    });
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
    expect(container.textContent).toContain("Turns");
    expect(container.textContent).toContain("CLIENT");
    expect(container.textContent).not.toContain("POST /v1/responses?stream=true");

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

  it("shows a friendly message for records without http metadata", async () => {
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: firstRecord.id,
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
      'button[aria-label="导出请求记录"]',
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

    const item = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(
      (candidate) => candidate.textContent?.trim() === "导出为 TXT 文件",
    );
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
      'button[aria-label="导出请求记录"]',
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

    const item = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(
      (candidate) => candidate.textContent?.trim() === "导出为 TXT 文件",
    );
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
    await chooseOption("状态筛选", "成功");
    const scroller = container.querySelector('[data-testid="request-records-scroll"]');
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

    expect(document.querySelector('[aria-label="状态筛选"]')?.textContent).toContain("成功");
    expect(scroller.scrollTop).toBe(180);
    expect(document.activeElement?.getAttribute("data-session-id")).toBe(
      firstRecord.id,
    );
    expect(requestAnimationFrame).toHaveBeenCalled();
  });

  it("does not treat the capture switch as off while audit settings are loading", async () => {
    const pending = deferred<AuditSettings>();
    bridgeMocks.getAuditSettings.mockReturnValueOnce(pending.promise);

    await act(async () => {
      reactRoot.render(
        <RequestRecords
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

    expect(document.querySelector('[role="alertdialog"]')?.textContent).toContain(
      "确认开启正文捕获",
    );
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

  it("turns off request and response capture from the header switch", async () => {
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: true,
      response_content_enabled: true,
      http_meta_enabled: true,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
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
    expect(document.querySelector('[role="alertdialog"]')?.textContent).toContain(
      "确定清空全部",
    );
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
    bridgeMocks.getRequestSession.mockImplementation(async (sessionId: string) => {
      if (sessionId === pending.id) return sessionDetail(latestTurn);
      if (sessionId === newer.id) return sessionDetail(newer);
      throw new Error(`missing session ${sessionId}`);
    });
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
    expect(container.textContent).toContain("成功");

    await act(async () => buttonContaining("实时监控").click());
    expect(buttonContaining("1 条新记录").textContent).toContain(
      "1 条新记录",
    );
    await act(async () => buttonContaining("1 条新记录").click());
    const rows = [...container.querySelectorAll('[data-testid="request-session-row"]')];
    expect(rows[0].textContent).toContain("gpt-new");
  });

  it("queues new rows when scrolled away and prepends directly when following the top", async () => {
    vi.useFakeTimers();
    bridgeMocks.listRequestSessions.mockResolvedValueOnce({
      items: [sessionFromRecord(firstRecord)],
      next_cursor: null,
    });
    await renderRecords();
    const scroller = container.querySelector('[data-testid="request-records-scroll"]');
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
