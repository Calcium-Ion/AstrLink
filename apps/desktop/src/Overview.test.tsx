// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AccessTokenCatalog } from "./AccessTokenManager";
import type { AppSnapshot } from "./core-model";
import { Overview, type ServiceCatalog } from "./Overview";
import type { Service } from "./service-model";
import {
  emptyTodayUsageSummary,
  type TodayUsageState,
} from "./today-usage";

const readySnapshot: AppSnapshot = {
  app_version: "0.1.0",
  phase: "ready",
  pid: 42,
  ready: {
    event: "ready",
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    inference_url: "http://127.0.0.1:8317",
    control_url: "http://127.0.0.1:43117",
  },
  health: { status: "ok" },
  version: {
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    build_commit: "unknown",
  },
  capabilities: {
    protocol_contract_version: "v1",
    protocols: [
      {
        id: "openai.responses",
        phase: "alpha",
        primary: true,
        streaming: true,
      },
    ],
    plan_types: [],
    conversion_engine: {
      name: "relaykit",
      version: null,
      available: false,
      edges: [],
    },
  },
  last_error: null,
  recovery_attempt: 0,
  recovery_scheduled_in_ms: null,
};

const gateway: Service = {
  id: "service_gateway_01",
  name: "Primary gateway",
  kind: "newapi",
  enabled: true,
  models: ["gpt-5"],
  capabilities: [
    {
      protocol: "openai.responses",
      mode: "delegated",
      streaming: true,
    },
  ],
  http: {
    base_url: "https://gateway.example",
    auth: { scheme: "bearer" },
  },
  created_at: "2026-07-28T08:00:00Z",
  updated_at: "2026-07-28T08:00:00Z",
};

const readyCatalog: ServiceCatalog = {
  status: "ready",
  items: [gateway],
  error: null,
  stale: false,
};

const readyTokens: AccessTokenCatalog = {
  status: "ready",
  items: [
    {
      id: "token_01",
      name: "VS Code",
      hint: "astr_…K8Q2",
      created_at: "2026-07-24T10:30:00Z",
    },
  ],
  error: null,
  stale: false,
};

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

describe("Overview", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
  });

  async function renderOverview(
    overrides: {
      catalog?: ServiceCatalog;
      todayUsage?: TodayUsageState;
      onOpenRecords?: () => void;
      onOpenService?: (serviceId: string) => void;
    } = {},
  ): Promise<{
    onOpenRecords: ReturnType<typeof vi.fn>;
    onOpenService: ReturnType<typeof vi.fn>;
  }> {
    const onOpenRecords = overrides.onOpenRecords
      ? vi.fn(overrides.onOpenRecords)
      : vi.fn();
    const onOpenService = overrides.onOpenService
      ? vi.fn(overrides.onOpenService)
      : vi.fn();
    await act(async () => {
      root.render(
        <Overview
          catalog={overrides.catalog ?? readyCatalog}
          copyError={null}
          copyFeedback={null}
          isNativeApp
          isReady
          isRestarting={false}
          onAddService={() => undefined}
          onCopy={() => undefined}
          onManageServices={() => undefined}
          onManageTokens={() => undefined}
          onOpenRecords={onOpenRecords}
          onOpenService={onOpenService}
          onRefreshServices={() => undefined}
          onRefreshTodayUsage={() => undefined}
          onRestart={() => undefined}
          snapshot={readySnapshot}
          todayUsage={
            overrides.todayUsage ?? {
              status: "ready",
              summary: emptyTodayUsageSummary(),
              error: null,
            }
          }
          tokenCatalog={readyTokens}
        />,
      );
    });
    return { onOpenRecords, onOpenService };
  }

  it("leads with today's consumption and keeps cost as a placeholder", async () => {
    await renderOverview({
      todayUsage: {
        status: "ready",
        summary: {
          ...emptyTodayUsageSummary(),
          requests: 4,
          failed_requests: 2,
          input_tokens: 100,
          output_tokens: 20,
          total_tokens: 120,
          cache_read_tokens: 40,
          by_service: [
            {
              id: "service_gateway_01",
              requests: 4,
              input_tokens: 100,
              output_tokens: 20,
              total_tokens: 120,
            },
          ],
          by_model: [
            {
              id: "gpt-4o",
              requests: 3,
              input_tokens: 80,
              output_tokens: 16,
              total_tokens: 96,
            },
            {
              id: null,
              requests: 1,
              input_tokens: 20,
              output_tokens: 4,
              total_tokens: 24,
            },
          ],
        },
        error: null,
      },
    });

    expect(container.textContent).toContain("查看今日消耗、构成与本地接入。");
    expect(container.textContent).toContain("今日消耗");
    expect(container.textContent).toContain("请求数");
    expect(container.textContent).toContain("总 Token");
    expect(container.textContent).toContain("输入 / 输出");
    expect(container.textContent).toContain("缓存命中");
    expect(container.textContent).toContain("100 / 20");
    expect(container.textContent).toContain("40%");
    expect(container.textContent).toContain("预估费用");
    expect(container.textContent).toContain("金额估算稍后提供");
    expect(container.textContent).toContain("按服务");
    expect(container.textContent).toContain("按模型");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("gpt-4o");
    expect(container.textContent).toContain("未知模型");
    expect(container.textContent).not.toContain("上游服务");
    expect(container.textContent).not.toContain("连接 AstrLink");

    const costRow = [...container.querySelectorAll("div")].find((node) =>
      node.textContent?.includes("预估费用"),
    );
    expect(costRow?.textContent).toContain("—");
  });

  it("shows an empty model state when today has no successful requests", async () => {
    await renderOverview();

    expect(container.textContent).toContain("今天还没有成功请求");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("0 次");
  });

  it("opens a catalog service from the usage breakdown", async () => {
    const { onOpenService } = await renderOverview();
    const serviceRow = [...document.querySelectorAll("button")].find((candidate) =>
      candidate.textContent?.includes("Primary gateway"),
    );
    if (!(serviceRow instanceof HTMLButtonElement)) {
      throw new Error("Missing service usage row");
    }

    await act(async () => {
      serviceRow.click();
    });

    expect(onOpenService).toHaveBeenCalledWith("service_gateway_01");
  });

  it("opens request records from the failed-request chip", async () => {
    const { onOpenRecords } = await renderOverview({
      todayUsage: {
        status: "ready",
        summary: {
          ...emptyTodayUsageSummary(),
          failed_requests: 3,
        },
        error: null,
      },
    });

    await act(async () => {
      button("3 次失败").click();
    });
    expect(onOpenRecords).toHaveBeenCalledTimes(1);
  });
});
