// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  deleteRequestRecord: vi.fn(),
  getAuditSettings: vi.fn(),
  getRequestAuditContent: vi.fn(),
  getRequestRecord: vi.fn(),
  listRequestRecordChildren: vi.fn(),
  listRequestRecords: vi.fn(),
  purgeRequestRecords: vi.fn(),
  updateAuditSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { RequestRecords } from "./RequestRecords";
import type { RequestRecord } from "./request-record-model";
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
    cached_input_tokens: 4,
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
    fallback_count: 0,
  },
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
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [secondRecord, firstRecord],
      next_cursor: "cursor-1",
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

  it("renders a full-width two-line log stream instead of a wide table", async () => {
    await renderRecords();

    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    expect(container.querySelector(".workspace-card .page-header")).toBeNull();
    expect(container.querySelector(".records-table")).toBeNull();
    expect(container.querySelectorAll(".record-row")).toHaveLength(2);
    expect(container.textContent).toContain("gpt-4.1");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("10 → 20 Token");
    expect(container.textContent).toContain("upstream · upstream_unavailable");
  });

  it("nests earlier attempts as numbered child requests under the final record", async () => {
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
    bridgeMocks.listRequestRecords.mockResolvedValueOnce({
      items: [root],
      next_cursor: null,
    });
    bridgeMocks.listRequestRecordChildren.mockResolvedValueOnce({
      items: children,
      next_cursor: null,
    });

    await renderRecords();
    expect(
      container.querySelector(`[data-record-id="${root.id}"]`)?.textContent,
    ).toContain("最后一次记录");

    await act(async () => {
      exactButton("子请求 2 条").click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.listRequestRecordChildren).toHaveBeenCalledWith(root.id);
    const renderedChildren = container.querySelectorAll(
      ".record-group__children .record-row",
    );
    expect(renderedChildren).toHaveLength(2);
    expect(renderedChildren[0].textContent).toContain("子请求 1");
    expect(renderedChildren[1].textContent).toContain("子请求 2");
    expect(exactButton("收起子请求")).not.toBeNull();
  });

  it("filters locally so a later status update can still reach the row", async () => {
    await renderRecords();
    bridgeMocks.listRequestRecords.mockClear();

    const statusSelect = container.querySelectorAll("select")[0];
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLSelectElement.prototype,
        "value",
      )?.set;
      setter?.call(statusSelect, "failed");
      statusSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });

    expect(bridgeMocks.listRequestRecords).not.toHaveBeenCalled();
    expect(
      container.querySelector(`[data-record-id="${secondRecord.id}"]`),
    ).not.toBeNull();
    expect(
      container.querySelector(`[data-record-id="${firstRecord.id}"]`),
    ).toBeNull();
  });

  it("passes the stable cursor when loading earlier records", async () => {
    await renderRecords();
    bridgeMocks.listRequestRecords.mockClear();
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await act(async () => {
      exactButton("加载更早记录").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.listRequestRecords).toHaveBeenCalledWith({
      limit: 50,
      cursor: "cursor-1",
    });
  });

  it("auto-decrypts on detail open, shows everything on one page, and caches across back-navigation", async () => {
    await renderRecords();

    await act(async () => {
      (
        container.querySelector(
          `[data-record-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    // One page: metadata, HTTP envelope, request body and response together.
    expect(container.querySelector("h1")?.textContent).toBe("记录详情");
    expect(container.textContent).toContain("身份");
    expect(container.textContent).toContain("指标");
    expect(container.textContent).toContain("隐私还原");
    expect(container.textContent).toContain("映射数4");
    expect(container.textContent).toContain("已还原5");
    expect(container.textContent).toContain("安全降级0");
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
          `[data-record-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    expect(container.textContent).toContain('"prompt": "secret"');
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);

    // Explicit clear drops the cache and returns to the monitor.
    await act(async () => {
      exactButton("清除已解密内容").click();
    });
    expect(container.querySelector("h1")?.textContent).toBe("请求记录");
    bridgeMocks.getRequestAuditContent.mockClear();
    await act(async () => {
      (
        container.querySelector(
          `[data-record-id="${firstRecord.id}"]`,
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
          `[data-record-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => await Promise.resolve());
    await act(async () => await Promise.resolve());

    expect(container.textContent).toContain("此记录未捕获 HTTP 元数据");
    expect(container.textContent).toContain("未捕获");
  });

  it("preserves filters, scroll offset, selection and focus across drill-down", async () => {
    const requestAnimationFrame = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((callback) => {
        callback(0);
        return 1;
      });
    await renderRecords();
    const statusSelect = container.querySelectorAll("select")[0];
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLSelectElement.prototype,
        "value",
      )?.set;
      setter?.call(statusSelect, "succeeded");
      statusSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const scroller = container.querySelector(".records-monitor__scroll");
    if (!(scroller instanceof HTMLDivElement)) {
      throw new Error("Missing monitor scroller");
    }
    scroller.scrollTop = 180;
    scroller.dispatchEvent(new Event("scroll", { bubbles: true }));

    await act(async () => {
      (
        container.querySelector(
          `[data-record-id="${firstRecord.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    await act(async () => buttonContaining("实时监控").click());

    expect((container.querySelectorAll("select")[0] as HTMLSelectElement).value).toBe(
      "succeeded",
    );
    expect(scroller.scrollTop).toBe(180);
    expect(document.activeElement?.getAttribute("data-record-id")).toBe(
      firstRecord.id,
    );
    expect(requestAnimationFrame).toHaveBeenCalled();
  });

  it("uses a second in-app step for capture risk and never calls window.confirm", async () => {
    await renderRecords();

    await act(async () => {
      exactButton("审计设置").click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    const checkbox = [...container.querySelectorAll('input[type="checkbox"]')].find(
      (input) => input.closest("label")?.textContent?.includes("请求体捕获"),
    );
    if (!(checkbox instanceof HTMLInputElement)) {
      throw new Error("Missing request body capture checkbox");
    }
    await act(async () => checkbox.click());
    await act(async () => exactButton("保存").click());

    expect(container.querySelector('[role="dialog"]')?.textContent).toContain(
      "确认开启正文捕获",
    );
    await act(async () => {
      exactButton("确认开启").click();
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(bridgeMocks.updateAuditSettings).toHaveBeenCalledWith({
      request_body_enabled: true,
      audit_risk_acknowledged: true,
    });
    expect(window.confirm).not.toHaveBeenCalled();
    expect(container.textContent).toContain("审计设置已保存。");
  });

  it("purges records through two in-app dialogs", async () => {
    await renderRecords();
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [],
      next_cursor: null,
    });

    await act(async () => exactButton("清理…").click());
    await act(async () => exactButton("执行清理").click());
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain(
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
    expect(container.textContent).toContain("已删除 2 条记录、1 个加密内容块。");
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
    bridgeMocks.listRequestRecords.mockResolvedValueOnce({
      items: [pending],
      next_cursor: null,
    });
    await renderRecords();
    await act(async () => {
      (
        container.querySelector(
          `[data-record-id="${pending.id}"]`,
        ) as HTMLButtonElement
      ).click();
    });
    expect(container.textContent).toContain("进行中");

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
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [newer, completed],
      next_cursor: null,
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(container.textContent).toContain("成功");
    expect(container.textContent).toContain("2.0 s");

    await act(async () => buttonContaining("实时监控").click());
    expect(buttonContaining("1 条新记录").textContent).toContain(
      "1 条新记录",
    );
    await act(async () => buttonContaining("1 条新记录").click());
    const rows = [...container.querySelectorAll(".record-row")];
    expect(rows[0].textContent).toContain("gpt-new");
  });

  it("queues new rows when scrolled away and prepends directly when following the top", async () => {
    vi.useFakeTimers();
    bridgeMocks.listRequestRecords.mockResolvedValueOnce({
      items: [firstRecord],
      next_cursor: null,
    });
    await renderRecords();
    const scroller = container.querySelector(".records-monitor__scroll");
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
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [queuedRecord, firstRecord],
      next_cursor: null,
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(
      container.querySelector(`[data-record-id="${queuedRecord.id}"]`),
    ).toBeNull();
    expect(buttonContaining("1 条新记录")).not.toBeNull();
    await act(async () => buttonContaining("1 条新记录").click());
    expect(
      container.querySelector(`[data-record-id="${queuedRecord.id}"]`),
    ).not.toBeNull();

    scroller.scrollTop = 0;
    scroller.dispatchEvent(new Event("scroll", { bubbles: true }));
    const followingRecord: RequestRecord = {
      ...queuedRecord,
      id: "req_followingeeeeeeeeeeeeeeeeeeeeeeeee",
      started_at: "2026-07-25T10:03:00Z",
      requested_model: "gpt-following",
    };
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [followingRecord, queuedRecord, firstRecord],
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
    bridgeMocks.listRequestRecords.mockRejectedValue(new Error("offline"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3100);
    });

    expect(container.textContent).toContain("实时同步暂时中断");
    expect(container.textContent).toContain("gpt-4.1");
  });
});
