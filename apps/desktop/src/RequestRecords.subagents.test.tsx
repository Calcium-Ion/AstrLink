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
  getRequestActivity: vi.fn(),
  listenRawSealingChanged: vi.fn(async () => () => {}),
  lockRaw: vi.fn(),
  purgeRequestRecords: vi.fn(),
  saveTextFile: vi.fn(),
  setRawPassword: vi.fn(),
  unlockRaw: vi.fn(),
  updateAuditSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);
vi.mock("./notify", () => ({
  notify: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

import { RequestRecords } from "./RequestRecords";
import {
  emptyTrajectoryFields,
  type RequestActivityCall,
  type RequestRecord,
  type RequestSession,
  type RequestSessionDetail,
} from "./request-record-model";
import type { RoutableService } from "./service-model";
import type { RequestService } from "./request-service-model";
import { settleClosedLayer } from "./lib/test-layer-settle";

const service: RoutableService & RequestService = {
  id: "service_01",
  name: "Primary gateway",
  kind: "newapi",
  enabled: true,
  models: ["k3.1-pro"],
  capabilities: [
    { protocol: "anthropic.messages", mode: "delegated", streaming: true },
  ],
};

const NOW = "2026-10-24T12:00:10Z";

function session(overrides: Partial<RequestSession>): RequestSession {
  return {
    id: "session_main",
    client_type: "claude_code",
    title: "Refactor the request router",
    started_at: "2026-10-24T11:58:00Z",
    last_started_at: "2026-10-24T11:59:00Z",
    completed_at: "2026-10-24T11:59:02Z",
    duration_ms: 2000,
    active_request_starts: [],
    turn_count: 1,
    call_count: 3,
    status: "succeeded",
    requested_model: "k3.1-pro",
    input_protocol: "anthropic.messages",
    service_id: "service_01",
    local_access_token_id: null,
    ...overrides,
  };
}

function record(sessionId: string): RequestRecord {
  return {
    id: `req_${sessionId}`,
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-10-24T11:59:00Z",
    completed_at: "2026-10-24T11:59:02Z",
    status: "succeeded",
    input_protocol: "anthropic.messages",
    requested_model: "k3.1-pro",
    streaming: true,
    route_id: null,
    service_id: "service_01",
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 2000,
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
    session_id: sessionId,
  };
}

// The conversation waits on its Agent tool while two subagents work: one
// still running, one that finished a while ago.
const parent = session({
  id: "session_main",
  output: { kind: "tool", tools: ["Agent"] },
  subagent_count: 2,
});
const working = session({
  id: "session_child_working",
  parent_session_id: "session_main",
  title: "Find every reader of the session header",
  started_at: "2026-10-24T11:59:01Z",
  last_started_at: "2026-10-24T12:00:05Z",
  completed_at: null,
  status: "pending",
  active_request_starts: ["2026-10-24T12:00:05Z"],
});
const finished = session({
  id: "session_child_finished",
  parent_session_id: "session_main",
  title: "List the lane components",
  started_at: "2026-10-24T11:58:30Z",
  last_started_at: "2026-10-24T11:58:40Z",
  completed_at: "2026-10-24T11:58:45Z",
});
const unrelated = session({
  id: "session_other",
  title: "Earlier question",
  started_at: "2026-10-24T09:00:00Z",
  last_started_at: "2026-10-24T09:00:00Z",
  completed_at: "2026-10-24T09:00:03Z",
});

function detail(item: RequestSession): RequestSessionDetail {
  return {
    ...item,
    turns: [record(item.id)],
    ...(item.id === parent.id ? { subagents: [finished, working] } : {}),
  };
}

describe("RequestRecords subagents", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(NOW));
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    const all = [working, parent, finished, unrelated];
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: all,
      next_cursor: null,
    });
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getRequestSession.mockImplementation(async (id: string) => {
      const item = all.find((candidate) => candidate.id === id);
      if (!item) throw new Error(`missing session ${id}`);
      return detail(item);
    });
    bridgeMocks.getRequestActivity.mockResolvedValue({
      now: NOW,
      calls: [
        {
          session_id: "session_main",
          started_at: "2026-10-24T11:59:00Z",
          completed_at: "2026-10-24T11:59:02Z",
          status: "succeeded",
          output: { kind: "tool", tools: ["Agent"] },
        },
        {
          session_id: "session_child_working",
          parent_session_id: "session_main",
          started_at: "2026-10-24T12:00:05Z",
          completed_at: null,
          status: "pending",
        },
      ] satisfies RequestActivityCall[],
    });
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
    bridgeMocks.getRawSealingStatus.mockResolvedValue({
      raw_available: false,
      configured: false,
      password_set: false,
      password_required: false,
      envelopes: [],
      key_verified: false,
      unlocked: false,
      unlock_expires_at: null,
      unlock_idle_seconds: 900,
      retry_after_seconds: 0,
      password_min_length: 8,
      password_max_length: 128,
      key_replaced: false,
    });
    bridgeMocks.getCoreStatus.mockResolvedValue({
      app_version: "0.9.0",
      version: { core_version: "0.9.0", build_commit: "abc1234" },
    });
    bridgeMocks.getPrivacyPolicy.mockResolvedValue({
      policy: { enabled: false, detector: "regex", request_action: "redact" },
    });
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValue({ items: [] });
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
    bridgeMocks.listRequestRecordChildren.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: "req_session_main",
      withheld: {},
      http_meta: null,
      request_body: null,
      response_content: null,
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

  async function renderRecords() {
    await act(async () => {
      reactRoot.render(
        <RequestRecords
          accessTokens={[]}
          accessTokensReady
          coreSessionKey="session-1"
          initialDetailTab="trajectory"
          services={[service]}
          isReady
        />,
      );
      await Promise.resolve();
    });
    for (let index = 0; index < 4; index += 1) {
      await act(async () => await Promise.resolve());
    }
  }

  it("hangs a working subagent under the conversation that started it", async () => {
    await renderRecords();
    const active = container.querySelector('[data-testid="active-sessions"]')!;
    const rows = [
      ...active.querySelectorAll<HTMLElement>("[data-session-id]"),
    ].map((row) => [row.dataset.testid, row.dataset.sessionId]);
    // Only the subagent still at work gets a row; it follows its parent.
    expect(rows).toEqual([
      ["request-session-row", "session_main"],
      ["subagent-session-row", "session_child_working"],
    ]);
    expect(active.querySelector('[data-slot="badge"]')?.textContent).toBe(
      "1 条",
    );
    const lane = active.querySelector('[data-session-id="session_main"]')!;
    expect(lane.textContent).toContain("2 个子代理");
    const subagent = active.querySelector(
      '[data-testid="subagent-session-row"]',
    )!;
    expect(subagent.textContent).toContain(
      "Find every reader of the session header",
    );
    expect(subagent.querySelector('[title="子代理"]')).not.toBeNull();
    // The board counts the conversation once, with its subagent.
    expect(
      container.querySelector('[data-testid="activity-conversations"]')
        ?.textContent,
    ).toBe("1个对话");
    // A finished subagent folds into its conversation instead of a row.
    expect(
      container.querySelector('[data-session-id="session_child_finished"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-session-id="session_other"]'),
    ).not.toBeNull();
  });

  it("folds subagents into their conversation's history row", async () => {
    const idleParent = { ...parent, output: undefined };
    const idleChild = {
      ...working,
      status: "succeeded" as const,
      completed_at: "2026-10-24T11:59:30Z",
      active_request_starts: [],
    };
    const orphan = session({
      id: "session_orphan",
      parent_session_id: "session_elsewhere",
      title: "Subagent of a conversation on another page",
      started_at: "2026-10-24T08:00:00Z",
      last_started_at: "2026-10-24T08:00:00Z",
      completed_at: "2026-10-24T08:00:02Z",
    });
    vi.setSystemTime(new Date("2026-10-24T13:00:00Z"));
    bridgeMocks.getRequestActivity.mockResolvedValue({
      now: "2026-10-24T13:00:00Z",
      calls: [],
    });
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [idleChild, idleParent, finished, unrelated, orphan],
      next_cursor: null,
    });
    await renderRecords();
    const rows = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="request-session-row"]',
      ),
    ];
    expect(rows.map((row) => row.dataset.sessionId)).toEqual([
      "session_main",
      "session_other",
      "session_orphan",
    ]);
    expect(rows[0].textContent).toContain("2 个子代理");
    expect(rows[1].textContent).not.toContain("子代理");
  });

  it("reaches each subagent from its conversation's details and back", async () => {
    await renderRecords();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[data-session-id="session_main"]')!
        .click(),
    );
    for (let index = 0; index < 4; index += 1) {
      await act(async () => await Promise.resolve());
    }
    const trigger = document.querySelector<HTMLButtonElement>(
      '[data-testid="session-subagents"]',
    );
    expect(trigger?.textContent).toContain("2 个子代理");
    await act(async () => {
      trigger!.dispatchEvent(
        new PointerEvent("pointerdown", {
          bubbles: true,
          button: 0,
          pointerType: "mouse",
        }),
      );
      await Promise.resolve();
    });
    const items = [
      ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
    ];
    expect(items.map((item) => item.dataset.sessionId)).toEqual([
      "session_child_finished",
      "session_child_working",
    ]);
    await act(async () => {
      items[1].click();
      await Promise.resolve();
    });
    // Radix hands focus back one timer tick after the menu closes.
    await act(async () => {
      const settling = settleClosedLayer();
      await vi.advanceTimersByTimeAsync(0);
      await settling;
    });
    for (let index = 0; index < 4; index += 1) {
      await act(async () => await Promise.resolve());
    }
    expect(bridgeMocks.getRequestSession).toHaveBeenLastCalledWith(
      "session_child_working",
    );
    const back = document.querySelector<HTMLButtonElement>(
      '[data-testid="parent-session"]',
    );
    expect(back?.textContent).toContain("上级对话");
    await act(async () => back!.click());
    for (let index = 0; index < 4; index += 1) {
      await act(async () => await Promise.resolve());
    }
    expect(
      document.querySelector('[data-testid="session-subagents"]'),
    ).not.toBeNull();

    // Deleting the conversation takes its subagents along, and says so.
    await act(async () => {
      document
        .querySelector<HTMLButtonElement>(
          '[data-testid="record-more-actions"]',
        )!
        .dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
      await Promise.resolve();
    });
    await act(async () => {
      document
        .querySelector<HTMLElement>('[data-testid="delete-session"]')!
        .click();
      await Promise.resolve();
    });
    await act(async () => {
      const settling = settleClosedLayer();
      await vi.advanceTimersByTimeAsync(0);
      await settling;
    });
    expect(
      document.querySelector('[role="alertdialog"]')?.textContent,
    ).toContain("将永久删除 3 次调用、2 个子代理和加密审计内容。");
  });
});
