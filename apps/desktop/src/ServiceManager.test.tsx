// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  beginServiceAuthorization: vi.fn(),
  cancelServiceAuthorization: vi.fn(),
  cancelServiceTest: vi.fn(),
  clearServiceRisk: vi.fn(),
  completeServiceAuthorization: vi.fn(),
  createService: vi.fn(),
  deleteService: vi.fn(),
  getService: vi.fn(),
  getServiceOrder: vi
    .fn()
    .mockResolvedValue({ service_ids: [], etag: '"order"' }),
  updateServiceOrder: vi.fn(),
  getServicePackages: vi.fn(),
  updateServicePackageStrategy: vi.fn(),
  PACKAGE_STRATEGIES: ["auto", "soonest_reset", "most_remaining"],
  getRoutingSettings: vi.fn(),
  getServiceAuthorization: vi.fn(),
  getServiceUsage: vi.fn(),
  getServiceResetCredits: vi.fn(),
  getUsageSummary: vi.fn(),
  listServiceRiskEvents: vi.fn(),
  logoutService: vi.fn(),
  resetServiceUsage: vi.fn(),
  openAuthorizationURL: vi.fn(),
  probeDraftServiceModels: vi.fn(),
  probeServiceModels: vi.fn(),
  probeServiceProxy: vi.fn(),
  testService: vi.fn(),
  SERVICE_TEST_CANCELLED_ERROR: "service_test_cancelled",
  SERVICE_TEST_ALREADY_RUNNING_ERROR: "service_test_already_running",
  SERVICE_TEST_INVALID_ID_ERROR: "service_test_invalid_id",
  updateService: vi.fn(),
  listTerminalApps: vi.fn(),
  getClientConfigStatus: vi.fn(),
  openServiceTerminal: vi.fn(),
  pickTerminalDirectory: vi.fn(),
  getClaudeCLI: vi.fn().mockResolvedValue({
    installed: true,
    supported: true,
    version: "2.1.300",
    minimum_version: "2.1.248",
    install_available: false,
    installing: false,
  }),
  installClaudeCLI: vi.fn(),
  openExternalURL: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const pricingMocks = vi.hoisted(() => ({
  getBillingSummary: vi.fn(),
  getQuotaEstimates: vi.fn(),
}));
vi.mock("./pricing-bridge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./pricing-bridge")>()),
  ...pricingMocks,
}));

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { defaultFailurePolicy } from "./failure-policy-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import { settleClosedLayer } from "./lib/test-layer-settle";
import { ServiceManager, type ServiceManagerProps } from "./ServiceManager";
import { PROTOCOL_MODE_GUIDE_KEY } from "./ProtocolModeHelp";
import { SERVICE_EDITOR_TOUR_KEY } from "./ServiceEditorTour";
import { SERVICE_ORDER_GUIDE_KEY } from "./ServiceOrderHelp";
import { parseService, type Service, type ServiceKind } from "./service-model";
import { httpServicePreset } from "./service-presets";
import { WorkspaceSnapshotProvider } from "./workspace-snapshots";
import type { SubscriptionUsage } from "./subscription-usage-model";

const timestamp = "2026-07-28T12:00:00Z";
const etag = `"sha256:${"a".repeat(64)}"`;

const codexService: Service = {
  id: "service_codex_personal",
  name: "Codex personal",
  kind: "codex_subscription",
  enabled: true,
  models: [],
  capabilities: [
    { protocol: "openai.responses", mode: "native", streaming: true },
  ],
  subscription: {
    provider: "openai_codex",
    status: "disconnected",
    authorization_boundary:
      "Logout is local-only: AstrLink removes locally stored credentials; remote revocation is unavailable.",
  },
  created_at: timestamp,
  updated_at: timestamp,
};

const secondCodexService: Service = {
  ...codexService,
  id: "service_codex_work",
  name: "Codex work",
};

const gatewayService: Service = {
  id: "service_gateway",
  name: "new-api",
  kind: "newapi",
  enabled: true,
  models: ["gpt-5"],
  capabilities: [
    { protocol: "openai.responses", mode: "delegated", streaming: true },
    { protocol: "openai.models", mode: "delegated", streaming: false },
  ],
  http: {
    base_url: "https://gateway.example/v1",
    auth: { scheme: "bearer" },
    credential_ref: "local://service/service_gateway",
  },
  created_at: timestamp,
  updated_at: timestamp,
};

const backupGatewayService: Service = {
  ...gatewayService,
  id: "service_backup",
  name: "backup",
  http: {
    ...gatewayService.http!,
    base_url: "https://backup.example/v1",
    credential_ref: "local://service/service_backup",
  },
};

/** An ISO time `hours` from now; quota reset times in the past roll over. */
/** A 5-hour reset as a clock time: today, or a weekday after midnight. */
const fiveHourReset = /^(\S+ )?\d{2}:\d{2} 重置$/;

function hoursFromNow(hours: number): string {
  return new Date(Date.now() + hours * 3_600_000 + 60_000).toISOString();
}

/** A primary-button pointer event of the one pointer the tests drag with. */
function pointer(type: string, clientX: number, clientY: number) {
  return new PointerEvent(type, {
    bubbles: true,
    button: 0,
    clientX,
    clientY,
    pointerId: 1,
    pointerType: "mouse",
  });
}

async function chooseServiceKind(option: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    'button[aria-label="API 提供商类型"]',
  );
  if (!trigger) throw new Error("Missing API provider type picker");
  await act(async () => {
    trigger.click();
    await Promise.resolve();
  });
  const card = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find(
    (candidate) =>
      candidate.querySelector('[data-slot="dialog-picker-label"]')
        ?.textContent === option,
  );
  if (!card) throw new Error(`Missing API provider type: ${option}`);
  await act(async () => {
    card.click();
    await Promise.resolve();
  });
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
  ].find((candidate) => {
    const label = candidate.cloneNode(true) as HTMLElement;
    // Decorative brand SVGs contain titles that are not part of the option name.
    label
      .querySelectorAll('[aria-hidden="true"]')
      .forEach((icon) => icon.remove());
    return label.textContent?.trim() === option;
  });
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

async function openServiceOverflow(name: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[aria-label="更多 ${name} 操作"]`,
  );
  if (!trigger) throw new Error(`Missing overflow menu: ${name}`);
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
}

async function chooseMenuItem(label: string): Promise<void> {
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!item) throw new Error(`Missing menu item: ${label}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

async function openModelActions(): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    'button[aria-label="更多模型操作"]',
  );
  if (!trigger) throw new Error("Missing model actions menu");
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
}

function previewModelCheckbox(model: string): HTMLButtonElement {
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!dialog) throw new Error("Missing model preview dialog");
  const row = [...dialog.querySelectorAll<HTMLElement>("label")].find(
    (label) => label.querySelector("code")?.textContent === model,
  );
  const checkbox = row?.querySelector<HTMLButtonElement>('[role="checkbox"]');
  if (!checkbox) throw new Error(`Missing preview model: ${model}`);
  return checkbox;
}

async function togglePreviewModel(model: string): Promise<void> {
  const checkbox = previewModelCheckbox(model);
  await act(async () => {
    checkbox.click();
    await Promise.resolve();
  });
}

function previewModelRow(model: string): HTMLElement {
  const checkbox = previewModelCheckbox(model);
  const row = checkbox.closest("label");
  if (!row) throw new Error(`Missing preview row: ${model}`);
  return row;
}

function previewModelIds(): string[] {
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!dialog) throw new Error("Missing model preview dialog");
  return [...dialog.querySelectorAll("label code")].map(
    (code) => code.textContent ?? "",
  );
}

async function clickButton(label: string): Promise<void> {
  const button = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent === label,
  );
  if (!button) throw new Error(`Missing button: ${label}`);
  await act(async () => {
    button.click();
    await Promise.resolve();
  });
}

function applySelectedButton(): HTMLButtonElement {
  const apply = [...document.querySelectorAll("button")].find((button) =>
    button.textContent?.startsWith("应用所选模型"),
  );
  if (!apply) throw new Error("Missing apply button");
  return apply;
}

function billingSummary(
  byService: { service_id: string; amount_usd: string; requests: number }[],
) {
  const amounts = (amount_usd: string, requests: number) => ({
    amount_usd,
    priced: requests,
    unpriced: 0,
    pending: 0,
    revalued: 0,
    requests,
  });
  return {
    ...amounts("0", 0),
    from: "2026-07-21T12:00:00Z",
    to: timestamp,
    by_model: [],
    by_token: [],
    by_service: byService.map((group) => ({
      ...amounts(group.amount_usd, group.requests),
      service_id: group.service_id,
    })),
  };
}

describe("ServiceManager", () => {
  let root: Root;
  let container: HTMLDivElement;

  beforeEach(() => {
    // Existing editor/action tests represent returning users.
    localStorage.setItem(SERVICE_ORDER_GUIDE_KEY, "seen");
    localStorage.setItem(PROTOCOL_MODE_GUIDE_KEY, "seen");
    localStorage.setItem(SERVICE_EDITOR_TOUR_KEY, "seen");
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
    });
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.getServiceResetCredits.mockResolvedValue({
      available_count: 1,
      credits: [{}],
    });
    bridgeMocks.getUsageSummary.mockResolvedValue({ by_service: [] });
    // Without a package list every service is its own card; tests that need
    // stacked cards return packages of their own.
    bridgeMocks.getServicePackages.mockResolvedValue([]);
    pricingMocks.getBillingSummary.mockResolvedValue(billingSummary([]));
    pricingMocks.getQuotaEstimates.mockResolvedValue([]);
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  /** A card or tray account by the name it is announced with. */
  function packageCard(name: string): HTMLElement {
    const card = container.querySelector<HTMLElement>(
      `[data-slot="package-card"][aria-label="${name}"]`,
    );
    if (!card) throw new Error(`Missing card: ${name}`);
    return card;
  }

  /** The grid's top-level card names in display order. */
  function cardNames(): (string | null)[] {
    return [...container.querySelectorAll('[data-testid="package-card"]')].map(
      (card) => card.getAttribute("aria-label"),
    );
  }

  /** The status line, bar and caption of a card's quota area. */
  function quotaOf(card: HTMLElement) {
    const summary = card.querySelector<HTMLElement>(
      '[data-slot="quota-summary"]',
    );
    if (!summary) throw new Error("Missing quota summary");
    const bar = summary.querySelector<HTMLElement>('[data-slot="quota-bar"]');
    const status = summary.querySelector('[data-slot="quota-status"]')!;
    return {
      // The dot, the status, then a reset time on two-window cards.
      status: status.children[1]?.textContent,
      statusEnd:
        status.querySelector('[data-slot="quota-status-end"]')?.textContent ??
        undefined,
      tone: status.getAttribute("data-tone"),
      bar: bar?.dataset.bar,
      barLabel: bar?.getAttribute("aria-label") ?? null,
      caption: [
        ...summary.querySelectorAll('[data-slot="quota-caption"] > span'),
      ].map((part) => part.textContent),
      busy: summary.getAttribute("aria-busy") === "true",
    };
  }

  it("opens a terminal for one provider from its menu once the gateway runs", async () => {
    bridgeMocks.listTerminalApps.mockResolvedValue([
      { id: "terminal", name: "Terminal" },
    ]);
    bridgeMocks.getClientConfigStatus.mockResolvedValue({
      clients: [],
      wsl_unchecked: [],
      wsl_localhost: false,
      cc_switch_wsl: { claude: null, codex: null },
    });
    const render = (inferenceURL: string) =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          inferenceURL={inferenceURL}
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    // Without a gateway address the terminal would have nothing to reach.
    await act(async () => render(""));
    await openServiceOverflow(gatewayService.name);
    const item = [...document.querySelectorAll('[role="menuitem"]')].find(
      (candidate) => candidate.textContent === "在终端打开…",
    );
    expect(item?.getAttribute("aria-disabled")).toBe("true");
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    await settleClosedLayer();

    await act(async () => render("http://127.0.0.1:18317"));
    await openServiceOverflow(gatewayService.name);
    await chooseMenuItem("在终端打开…");
    await act(async () => {
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    });
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      `在终端打开「${gatewayService.name}」`,
    );
    expect(bridgeMocks.listTerminalApps).toHaveBeenCalledOnce();
  });

  it("shows per-provider cache and TPS, changes period and refreshes unavailable statistics", async () => {
    bridgeMocks.getUsageSummary.mockResolvedValueOnce({
      by_service: [
        {
          id: gatewayService.id,
          performance: {
            cache_hit_rate: 0.26,
            output_tokens_per_second: 49,
            cache_samples: 2,
            speed_samples: 3,
          },
        },
      ],
    });
    const render = () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          services={[
            gatewayService,
            { ...gatewayService, id: "service_empty", name: "No usage" },
          ]}
          protocols={[]}
          view={{ kind: "list" }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      );
    await act(async () => render());
    // Cards keep cache and TPS behind one icon whose name carries the values.
    const meters = () =>
      ["new-api", "No usage"].map((name) =>
        container
          .querySelector(
            `[aria-label="${name}"] [data-testid="service-performance"]`,
          )!
          .getAttribute("aria-label"),
      );
    expect(meters()).toEqual([
      "new-api：缓存命中率 26.0%，TPS 49.0 tok/s",
      "No usage：缓存命中率 —，TPS —",
    ]);
    expect(bridgeMocks.getUsageSummary).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getUsageSummary).toHaveBeenLastCalledWith(
      expect.objectContaining({ preset: "7d" }),
    );

    bridgeMocks.getUsageSummary.mockResolvedValueOnce({
      by_service: [
        {
          id: gatewayService.id,
          performance: {
            cache_hit_rate: 0,
            output_tokens_per_second: null,
            cache_samples: 1,
            speed_samples: 0,
          },
        },
      ],
    });
    await chooseOption("统计区间", "近 30 天");
    expect(bridgeMocks.getUsageSummary).toHaveBeenLastCalledWith(
      expect.objectContaining({ preset: "30d" }),
    );
    expect(meters()[0]).toBe("new-api：缓存命中率 0.0%，TPS —");

    bridgeMocks.getUsageSummary.mockRejectedValueOnce(new Error("Unavailable"));
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('button[aria-label="刷新列表"]')!
        .click(),
    );
    expect(meters()[0]).toBe("new-api：统计加载失败");
  });

  it("tests the selected saved provider without changing its configuration", async () => {
    let finish!: (value: unknown) => void;
    bridgeMocks.testService.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          services={[gatewayService]}
          protocols={[]}
          view={{ kind: "list" }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('button[aria-label="测试 new-api"]')!
        .click(),
    );
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.textContent).toContain("额度");
    expect(
      dialog.querySelector<HTMLInputElement>('input[aria-label="测试模型"]')!
        .value,
    ).toBe("gpt-5");
    const start = [...dialog.querySelectorAll("button")].find(
      (button) => button.textContent === "开始测试",
    )!;
    await act(async () => {
      start.click();
      start.click();
    });
    expect(bridgeMocks.testService).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.testService).toHaveBeenCalledWith(
      "service_gateway",
      {
        protocol: "openai.responses",
        model: "gpt-5",
        stream: true,
      },
      expect.any(String),
    );
    expect(start.textContent).toBe("取消测试");
    expect(start.disabled).toBe(false);
    expect(
      dialog.querySelector<HTMLInputElement>('input[aria-label="测试模型"]')!
        .disabled,
    ).toBe(true);
    await act(async () =>
      finish({
        service_id: "service_gateway",
        protocol: "openai.responses",
        model: "gpt-5",
        stream: true,
        ok: true,
        status_code: 200,
        duration_ms: 124,
        output: "**OK**",
      }),
    );
    expect(dialog.textContent).toContain("测试成功");
    expect(dialog.textContent).toContain("0.12 s");
    expect(dialog.textContent).toContain("HTTP 200");
    await act(async () => {
      await vi.dynamicImportSettled();
    });
    expect(
      dialog.querySelector('[data-slot="markdown-content"] strong')
        ?.textContent,
    ).toBe("OK");
    bridgeMocks.testService.mockResolvedValueOnce({
      service_id: "service_gateway",
      protocol: "openai.responses",
      model: "gpt-5",
      stream: true,
      ok: false,
      status_code: 429,
      duration_ms: 50,
      output: "",
      error_code: "upstream_error",
      message: "Quota exceeded",
    });
    await act(async () => start.click());
    expect(dialog.textContent).toContain("测试失败");
    expect(dialog.textContent).toContain("Quota exceeded");
    expect(dialog.querySelector("pre")).toBeNull();
    bridgeMocks.testService.mockRejectedValueOnce(
      new Error("Core unavailable"),
    );
    await act(async () => start.click());
    expect(dialog.textContent).toContain("Core unavailable");
    expect(start.disabled).toBe(false);
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });

  it("requires a connected subscription before testing", async () => {
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          services={[codexService]}
          protocols={[]}
          view={{ kind: "list" }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          'button[aria-label="测试 Codex personal"]',
        )!
        .click(),
    );
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.textContent).toContain("请先登录此订阅");
    expect(
      [...dialog.querySelectorAll("button")].find(
        (button) => button.textContent === "开始测试",
      )!.disabled,
    ).toBe(true);
    expect(
      dialog.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled,
    ).toBe(true);
    expect(bridgeMocks.testService).not.toHaveBeenCalled();
  });

  it("keeps WebSocket off the cards and saves an explicit Codex opt-out", async () => {
    const saved = { ...codexService, responses_websocket_enabled: false };
    bridgeMocks.getService.mockResolvedValue({ service: codexService, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: saved, etag });
    const onServiceSaved = vi.fn();
    const props = {
      isReady: true,
      protocols: [],
      services: [codexService, gatewayService],
      catalogStatus: "ready" as const,
      catalogError: null,
      onRefresh: vi.fn(),
      onViewChange: vi.fn(),
      onServiceSaved,
      onServiceRemoved: vi.fn(),
      onDirtyChange: vi.fn(),
    };
    await act(async () =>
      root.render(<ServiceManager {...props} view={{ kind: "list" }} />),
    );
    expect(
      container.querySelector('[role="switch"][aria-label*="WebSocket"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-testid="service-list-scroller"]')
        ?.textContent,
    ).not.toContain("WebSocket");
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
    await act(async () =>
      root.render(
        <ServiceManager
          {...props}
          view={{ kind: "edit", serviceId: codexService.id }}
        />,
      ),
    );
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="Responses WebSocket"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      codexService.id,
      etag,
      expect.objectContaining({ responses_websocket_enabled: false }),
    );
    expect(onServiceSaved).toHaveBeenCalledWith(saved);
  });

  it("trusts a provider with privacy and badges it in the list", async () => {
    const trusted = { ...gatewayService, privacy_trusted: true };
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: trusted, etag });
    const onServiceSaved = vi.fn();
    const props = {
      isReady: true,
      protocols: [],
      catalogStatus: "ready" as const,
      catalogError: null,
      onRefresh: vi.fn(),
      onViewChange: vi.fn(),
      onServiceSaved,
      onServiceRemoved: vi.fn(),
      onDirtyChange: vi.fn(),
    };
    await act(async () =>
      root.render(
        <ServiceManager
          {...props}
          services={[codexService, gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      ),
    );
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="信任此提供商"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain(
      "隐私保护不会改动发往此提供商的请求。",
    );
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      expect.objectContaining({ privacy_trusted: true }),
    );
    expect(onServiceSaved).toHaveBeenCalledWith(trusted);

    await act(async () =>
      root.render(
        <ServiceManager
          {...props}
          services={[codexService, trusted]}
          view={{ kind: "list" }}
        />,
      ),
    );
    const badges = container.querySelectorAll(
      '[data-testid="privacy-trusted-badge"]',
    );
    expect(badges).toHaveLength(1);
    expect(
      badges[0]
        ?.closest('[data-testid="package-card"]')
        ?.getAttribute("aria-label"),
    ).toBe(trusted.name);
    expect(badges[0]?.getAttribute("title")).toBe(
      "发往此提供商的请求不经过隐私保护",
    );
    expect(badges[0]?.textContent).toBe("发往此提供商的请求不经过隐私保护");
  });

  it("waits for saved priority before showing cards on every page entry", async () => {
    for (let entry = 0; entry < 2; entry++) {
      let resolveOrder!: (value: unknown) => void;
      bridgeMocks.getServiceOrder.mockReturnValueOnce(
        new Promise((resolve) => {
          resolveOrder = resolve;
        }),
      );
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            services={[backupGatewayService, gatewayService]}
            protocols={[]}
            view={{ kind: "list" }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
      expect(
        container.querySelectorAll('[data-testid="package-card"]'),
      ).toHaveLength(0);
      await act(async () =>
        resolveOrder({
          service_ids: [gatewayService.id, backupGatewayService.id],
          etag: '"saved-order"',
        }),
      );
      expect(cardNames()).toEqual([
        gatewayService.name,
        backupGatewayService.name,
      ]);
      await act(async () => root.render(null));
    }
  });

  it("opens the provider order guide only once there are two providers", async () => {
    localStorage.removeItem(SERVICE_ORDER_GUIDE_KEY);
    const orderGuide = () =>
      [...document.querySelectorAll('[data-slot="dialog-content"]')].find(
        (dialog) => dialog.textContent?.includes("拖动卡片调整提供商先后"),
      );
    const render = (services: Service[]) =>
      act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            services={services}
            protocols={[]}
            view={{ kind: "list" }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );

    await render([gatewayService]);
    await vi.waitFor(() => expect(cardNames()).toEqual([gatewayService.name]));
    expect(orderGuide()).toBeUndefined();
    expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBeNull();

    await render([gatewayService, backupGatewayService]);
    await vi.waitFor(() => expect(orderGuide()).toBeDefined());
    expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBe("seen");
  });

  it("drags a card from its body and cancels with Escape without saving", async () => {
    bridgeMocks.getServiceOrder.mockResolvedValueOnce({
      service_ids: [gatewayService.id, backupGatewayService.id],
      etag: '"order"',
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          services={[gatewayService, backupGatewayService]}
          protocols={[]}
          view={{ kind: "list" }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    const card = packageCard("new-api");
    // A press on a control inside the card is that control's click.
    await act(async () =>
      card
        .querySelector('[role="switch"]')!
        .dispatchEvent(pointer("pointerdown", 10, 10)),
    );
    await act(async () => window.dispatchEvent(pointer("pointermove", 40, 10)));
    expect(container.querySelector('[data-sorting="true"]')).toBeNull();
    await act(async () => window.dispatchEvent(pointer("pointerup", 40, 10)));

    await act(async () =>
      card
        .querySelector('[data-slot="quota-summary"]')!
        .dispatchEvent(pointer("pointerdown", 10, 10)),
    );
    await act(async () => window.dispatchEvent(pointer("pointermove", 12, 11)));
    // Below the threshold a press is still a click.
    expect(container.querySelector('[data-sorting="true"]')).toBeNull();
    await act(async () => window.dispatchEvent(pointer("pointermove", 40, 10)));
    expect(
      container
        .querySelector('[data-sorting="true"]')
        ?.getAttribute("aria-label"),
    ).toBe("中转站 / API 密钥的顺序");
    expect(packageCard("new-api").dataset.dragging).toBe("true");
    expect(packageCard("backup").dataset.dragging).toBeUndefined();
    await act(async () =>
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
    );
    expect(container.querySelector('[data-sorting="true"]')).toBeNull();
    expect(packageCard("new-api").dataset.dragging).toBeUndefined();
    await act(async () => window.dispatchEvent(pointer("pointerup", 40, 10)));
    expect(cardNames()).toEqual(["new-api", "backup"]);
    expect(bridgeMocks.updateServiceOrder).not.toHaveBeenCalled();
  });

  describe("packages", () => {
    const plusA: Service = {
      ...codexService,
      id: "service_codex_a",
      name: "Codex A",
      models: ["gpt-5"],
      subscription: {
        ...codexService.subscription!,
        status: "connected",
        account_hint: "7a1c***22",
      },
    };
    const plusB: Service = {
      ...plusA,
      id: "service_codex_b",
      name: "Codex B",
      models: ["gpt-5", "gpt-5-codex"],
      subscription: { ...plusA.subscription!, account_hint: "e9d0***5b" },
    };
    const claude: Service = {
      ...codexService,
      id: "service_claude",
      name: "Claude personal",
      kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "connected" },
    };
    const plusPackage = {
      id: "subscription:codex:plus",
      kind: "subscription",
      provider: "openai_codex",
      plan_type: "plus",
      service_ids: [plusA.id, plusB.id],
      strategy: "auto",
    };
    const claudePackage = {
      id: "subscription:claude:pro",
      kind: "subscription",
      provider: "claude_code",
      plan_type: "pro",
      service_ids: [claude.id],
      strategy: "auto",
    };
    const gatewayPackage = {
      id: `service:${gatewayService.id}`,
      kind: "service",
      service_ids: [gatewayService.id],
      strategy: "auto",
    };

    /** Codex A has 70% of its 5 hours left; Codex B used them up for 2 hours. */
    function usageOf(id: string): SubscriptionUsage {
      const fetched_at = new Date().toISOString();
      const windows = (primary: number, secondary: number, hours: number) => ({
        primary: {
          used_percent: primary,
          limit_window_seconds: 18_000,
          reset_at: hoursFromNow(hours),
        },
        secondary: {
          used_percent: secondary,
          limit_window_seconds: 604_800,
          reset_at: hoursFromNow(100),
        },
      });
      if (id === plusA.id)
        return {
          service_id: id,
          fetched_at,
          plan_type: "plus",
          limit_reached: false,
          ...windows(30, 10, 3),
        };
      if (id === plusB.id)
        return {
          service_id: id,
          fetched_at,
          plan_type: "plus",
          limit_reached: true,
          ...windows(100, 40, 2),
        };
      if (id === claude.id)
        return {
          service_id: id,
          fetched_at,
          plan_type: "pro",
          limit_reached: false,
          ...windows(20, 50, 4),
        };
      return {
        service_id: id,
        fetched_at,
        limit_reached: false,
        quota: {
          unlimited: false,
          used_usd: "7.5",
          remaining_usd: "2.5",
          total_usd: "10",
        },
      };
    }

    async function flush() {
      for (let step = 0; step < 4; step++)
        await act(async () => {
          await Promise.resolve();
        });
    }

    /** Renders the list with every service in Core's saved order. */
    async function renderPackages(
      services: Service[],
      props: Partial<ServiceManagerProps> = {},
    ) {
      bridgeMocks.getServiceOrder.mockResolvedValueOnce({
        service_ids: services.map((service) => service.id),
        etag: '"order"',
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={services}
            view={{ kind: "list" }}
            {...props}
          />,
        ),
      );
      await flush();
    }

    function tray() {
      return container.querySelector<HTMLElement>(
        '[data-testid="package-tray"]',
      );
    }

    function expandButton(name: string) {
      const button = packageCard(name).querySelector<HTMLButtonElement>(
        '[data-testid="package-expand"]',
      );
      if (!button) throw new Error(`Missing expand button: ${name}`);
      return button;
    }

    function strategyOption(label: string) {
      const option = [
        ...(tray()?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ??
          []),
      ].find((button) => button.textContent === label);
      if (!option) throw new Error(`Missing strategy: ${label}`);
      return option;
    }

    function strategyHint() {
      return tray()?.querySelector('[data-testid="package-strategy-hint"]')
        ?.textContent;
    }

    it("stacks a plan's accounts into one card that opens a tray below its row", async () => {
      bridgeMocks.getServicePackages.mockResolvedValue([
        plusPackage,
        gatewayPackage,
      ]);
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      const onViewChange = vi.fn();
      await renderPackages([plusA, plusB, gatewayService], { onViewChange });

      expect(cardNames()).toEqual(["Codex Plus", "new-api"]);
      const stack = packageCard("Codex Plus");
      expect(stack.dataset.stacked).toBe("true");
      expect(stack.textContent).toContain("2 个账户");
      const badge = stack.querySelector('[data-slot="package-count"]');
      expect(badge?.textContent).toBe("2");
      expect(badge?.getAttribute("aria-label")).toBe("2 个账户");
      // Codex B is used up, so only Codex A counts as live; the average
      // still includes B's empty window.
      expect(quotaOf(stack)).toEqual({
        status: "1 个号有额度，平均剩 35%",
        tone: "positive",
        bar: "flat",
        barLabel: "剩余 35%",
        caption: ["1 个号用完 · 最早 2 小时后恢复"],
        busy: false,
      });
      // The footer keeps the cost and the tray toggle; the page header
      // already names the period.
      expect(stack.textContent).not.toContain("个模型");
      expect(stack.textContent).not.toContain("近 7 天");
      expect(stack.querySelector('[role="switch"]')).toBeNull();
      expect(tray()).toBeNull();

      await act(async () => expandButton("Codex Plus").click());
      const opened = tray();
      if (!opened) throw new Error("Missing package tray");
      expect(opened.getAttribute("aria-label")).toBe("Codex Plus 的账户");
      expect(
        opened.closest('[data-testid="package-section-subscription"]'),
      ).not.toBeNull();
      expect(expandButton("Codex Plus").getAttribute("aria-expanded")).toBe(
        "true",
      );
      expect(expandButton("Codex Plus").textContent).toBe("收起");
      const accounts = [
        ...opened.querySelectorAll('[data-testid="package-account"]'),
      ];
      expect(
        accounts.map((account) => account.getAttribute("aria-label")),
      ).toEqual(["Codex A", "Codex B"]);
      expect(
        accounts.map(
          (account) =>
            account.querySelector('[data-testid="subscription-plan"]')
              ?.textContent,
        ),
      ).toEqual(["Plus", "Plus"]);
      // Two windows: the status is the 5 hours, the caption the week.
      expect(quotaOf(packageCard("Codex A"))).toMatchObject({
        status: "5 小时 剩 70%",
        statusEnd: expect.stringMatching(fiveHourReset),
      });
      expect(quotaOf(packageCard("Codex B"))).toMatchObject({
        status: "5 小时 已用完",
        tone: "critical",
        bar: "nested",
        caption: [
          expect.stringMatching(/^每周 剩 60% · \S+ \d{2}:\d{2} 重置$/),
        ],
      });
      expect(
        packageCard("Codex B")
          .querySelector('[role="switch"]')
          ?.getAttribute("aria-label"),
      ).toBe("启用 Codex B");

      expect(strategyOption("自动").getAttribute("aria-checked")).toBe("true");
      expect(strategyHint()).toBe(
        "先用快要重置的号，剩 10% 或更少的号排到后面。",
      );
      const add = opened.querySelector<HTMLButtonElement>(
        '[data-slot="package-add-slot"]',
      );
      expect(add?.textContent).toBe("再登录一个 Codex 账户");
      await act(async () => add?.click());
      expect(onViewChange).toHaveBeenCalledWith({
        kind: "create",
        serviceKind: "codex_subscription",
      });

      await act(async () => expandButton("Codex Plus").click());
      expect(tray()).toBeNull();
      expect(expandButton("Codex Plus").getAttribute("aria-expanded")).toBe(
        "false",
      );
      expect(expandButton("Codex Plus").textContent).toBe("展开");
    });

    it("shows what a whole window holds on accounts and the package's sum", async () => {
      // A switched-off account stays out of the package's sum.
      const plusC: Service = {
        ...plusB,
        id: "service_codex_c",
        name: "Codex C",
        enabled: false,
      };
      bridgeMocks.getServicePackages.mockResolvedValue([
        { ...plusPackage, service_ids: [plusA.id, plusB.id, plusC.id] },
        claudePackage,
      ]);
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      // Codex B has no estimate yet; it takes Codex A's, as they share a plan.
      pricingMocks.getQuotaEstimates.mockResolvedValue([
        {
          service_id: plusA.id,
          windows: [
            { kind: "primary", total_usd: "40.00" },
            { kind: "secondary", total_usd: "300.00" },
          ],
        },
        {
          service_id: claude.id,
          windows: [{ kind: "secondary", total_usd: "512.40" }],
        },
      ]);
      await renderPackages([plusA, plusB, plusC, claude]);

      const basis = "按官方 API 价格估算：近 7 天的调用花费 ÷ 已用比例";
      const estimate = (card: HTMLElement) =>
        card.querySelector<HTMLElement>('[data-testid="quota-estimate"]');
      // Codex B is used up: it adds its whole window and nothing left. The
      // status keeps the share; the estimate takes the caption's end, so the
      // disabled count moves in.
      const stack = packageCard("Codex Plus");
      expect(quotaOf(stack).status).toBe("1 个号有额度，平均剩 35%");
      expect(quotaOf(stack).caption).toEqual([
        "1 个号用完 · 最早 2 小时后恢复 · 1 个停用",
        "预估总额 $80",
      ]);
      expect(estimate(stack)?.title).toBe(
        `还剩约 $28\n2 个账户合计，各按最先用完的窗口\n${basis}`,
      );
      // Only Claude's weekly window has an estimate: what is left is unknown.
      const single = packageCard("Claude personal");
      expect(quotaOf(single).status).toBe("5 小时 剩 80%");
      expect(estimate(single)).toBeNull();

      await act(async () => expandButton("Codex Plus").click());
      const own = packageCard("Codex A");
      expect(quotaOf(own).status).toBe("5 小时 剩 70%");
      // The caption is the week beside the estimate; hovering lists every
      // window.
      expect(quotaOf(own).caption).toEqual([
        expect.stringMatching(/^每周 剩 90% · \S+ \d{2}:\d{2} 重置$/),
        "预估总额 $40",
      ]);
      expect(
        own.querySelector<HTMLElement>('[data-slot="quota-caption"] [title]')
          ?.title,
      ).toBe("5 小时 剩 70% · 3 小时后\n每周 剩 90% · 4 天后");
      expect(estimate(own)?.title).toBe(
        `5 小时预估总额 $40，剩约 $28\n每周预估总额 $300，剩约 $270\n${basis}`,
      );
      // A used-up account still shows what its window holds.
      expect(quotaOf(packageCard("Codex B")).status).toBe("5 小时 已用完");
      expect(estimate(packageCard("Codex B"))?.textContent).toBe(
        "预估总额 $40",
      );
    });

    it("saves the tray's strategy and rolls back a failed save", async () => {
      let saved = "auto";
      bridgeMocks.getServicePackages.mockImplementation(async () => [
        { ...plusPackage, strategy: saved },
      ]);
      bridgeMocks.updateServicePackageStrategy.mockImplementation(
        async (id: string, strategy: string) => {
          saved = strategy;
          return { ...plusPackage, id, strategy };
        },
      );
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      await renderPackages([plusA, plusB]);
      await act(async () => expandButton("Codex Plus").click());
      expect(
        tray()
          ?.querySelector('[data-slot="segmented-control"]')
          ?.getAttribute("aria-label"),
      ).toBe("Codex Plus 先用哪个号");

      await act(async () => strategyOption("额度剩余最多优先").click());
      await flush();
      expect(bridgeMocks.updateServicePackageStrategy).toHaveBeenCalledWith(
        "subscription:codex:plus",
        "most_remaining",
      );
      expect(
        strategyOption("额度剩余最多优先").getAttribute("aria-checked"),
      ).toBe("true");
      expect(strategyHint()).toBe("先用剩得最多的号，各个号用得更平均。");
      expect(tray()?.querySelector('[role="alert"]')).toBeNull();

      // Tauri rejects with the error text itself.
      bridgeMocks.updateServicePackageStrategy.mockRejectedValueOnce(
        "Core is not running",
      );
      const reads = bridgeMocks.getServicePackages.mock.calls.length;
      await act(async () => strategyOption("最近快重置优先").click());
      await flush();
      expect(bridgeMocks.updateServicePackageStrategy).toHaveBeenLastCalledWith(
        "subscription:codex:plus",
        "soonest_reset",
      );
      expect(
        strategyOption("额度剩余最多优先").getAttribute("aria-checked"),
      ).toBe("true");
      expect(strategyHint()).toBe("先用剩得最多的号，各个号用得更平均。");
      expect(tray()?.querySelector('[role="alert"]')?.textContent).toBe(
        "没保存成功，仍按原来的方式选号。Core is not running",
      );
      // The package may have changed meanwhile, so the list is read again.
      expect(bridgeMocks.getServicePackages).toHaveBeenCalledTimes(reads + 1);
      expect(tray()).not.toBeNull();
    });

    it("drags a package as one card and keeps its accounts together", async () => {
      bridgeMocks.getServicePackages.mockResolvedValue([
        plusPackage,
        claudePackage,
        gatewayPackage,
      ]);
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      bridgeMocks.updateServiceOrder.mockImplementationOnce(
        async (service_ids: string[]) => ({ service_ids, etag: '"order2"' }),
      );
      // Core's order splits the plan: Claude sits between the Codex accounts.
      await renderPackages([plusA, claude, plusB, gatewayService]);
      expect(cardNames()).toEqual(["Codex Plus", "Claude personal", "new-api"]);

      const place = (id: string, left: number) => {
        const item = container.querySelector<HTMLElement>(
          `[data-grid-item="${id}"]`,
        );
        if (!item) throw new Error(`Missing grid item: ${id}`);
        item.getBoundingClientRect = () =>
          ({
            left,
            right: left + 200,
            top: 0,
            bottom: 150,
            x: left,
            y: 0,
            width: 200,
            height: 150,
            toJSON: () => ({}),
          }) as DOMRect;
      };
      place(plusPackage.id, 0);
      place(claudePackage.id, 212);

      await act(async () =>
        packageCard("Claude personal")
          .querySelector('[data-slot="quota-summary"]')!
          .dispatchEvent(pointer("pointerdown", 300, 50)),
      );
      await act(async () =>
        window.dispatchEvent(pointer("pointermove", 310, 50)),
      );
      expect(packageCard("Claude personal").dataset.dragging).toBe("true");
      await act(async () =>
        window.dispatchEvent(pointer("pointermove", 100, 50)),
      );
      expect(cardNames()).toEqual(["Claude personal", "Codex Plus", "new-api"]);
      expect(bridgeMocks.updateServiceOrder).not.toHaveBeenCalled();
      await act(async () =>
        window.dispatchEvent(pointer("pointerup", 100, 50)),
      );
      await flush();

      expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledTimes(1);
      expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledWith(
        [claude.id, plusA.id, plusB.id, gatewayService.id],
        '"order"',
      );
      expect(cardNames()).toEqual(["Claude personal", "Codex Plus", "new-api"]);
      expect(container.querySelector('[data-sorting="true"]')).toBeNull();
    });

    it("moves a package with arrow keys on its grip", async () => {
      bridgeMocks.getServicePackages.mockResolvedValue([
        plusPackage,
        claudePackage,
        gatewayPackage,
      ]);
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      bridgeMocks.updateServiceOrder.mockImplementationOnce(
        async (service_ids: string[]) => ({ service_ids, etag: '"order2"' }),
      );
      await renderPackages([plusA, claude, plusB, gatewayService]);
      const grip = container.querySelector<HTMLButtonElement>(
        `[data-grid-item="${plusPackage.id}"] [data-grid-grip]`,
      );
      if (!grip) throw new Error("Missing package grip");
      expect(grip.getAttribute("aria-label")).toBe(
        "移动 Codex Plus，当前第 1 位",
      );
      grip.focus();
      await act(async () =>
        grip.dispatchEvent(
          new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
        ),
      );
      await flush();
      expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
        [claude.id, plusA.id, plusB.id, gatewayService.id],
        '"order"',
      );
      expect(cardNames()).toEqual(["Claude personal", "Codex Plus", "new-api"]);
      expect(document.activeElement?.closest("[data-grid-item]")).toBe(
        container.querySelector(`[data-grid-item="${plusPackage.id}"]`),
      );
      expect(document.activeElement?.getAttribute("aria-label")).toBe(
        "移动 Codex Plus，当前第 2 位",
      );
    });

    it("finds a package by an account hint or a model only one account has", async () => {
      bridgeMocks.getServicePackages.mockResolvedValue([
        plusPackage,
        gatewayPackage,
      ]);
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      await renderPackages([plusA, plusB, gatewayService]);
      const search = container.querySelector<HTMLInputElement>(
        'input[aria-label="搜索 API 提供商"]',
      );
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      if (!search || !setter) throw new Error("Missing service search");
      const type = (value: string) =>
        act(async () => {
          setter.call(search, value);
          search.dispatchEvent(new Event("input", { bubbles: true }));
        });

      await type("e9d0");
      expect(cardNames()).toEqual(["Codex Plus"]);
      await type("gpt-5-codex");
      expect(cardNames()).toEqual(["Codex Plus"]);
      await type("gpt-5");
      expect(cardNames()).toEqual(["Codex Plus", "new-api"]);
      await type("Codex B");
      expect(cardNames()).toEqual(["Codex Plus"]);
    });

    it("closes the tray when the package is re-derived once its plan is read", async () => {
      let planRead = false;
      let release = () => {};
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      const unknownPackage = {
        id: "subscription:codex:unknown",
        kind: "subscription",
        provider: "openai_codex",
        service_ids: [plusA.id, plusB.id],
        strategy: "auto",
      };
      bridgeMocks.getServicePackages.mockImplementation(async () =>
        planRead ? [plusPackage] : [unknownPackage],
      );
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) => {
        await gate;
        return usageOf(id);
      });
      await renderPackages([plusA, plusB]);
      expect(cardNames()).toEqual(["Codex · 套餐未读到"]);
      expect(quotaOf(packageCard("Codex · 套餐未读到"))).toMatchObject({
        status: "正在读取额度…",
        busy: true,
      });
      await act(async () => expandButton("Codex · 套餐未读到").click());
      expect(tray()?.getAttribute("aria-label")).toBe(
        "Codex · 套餐未读到 的账户",
      );

      // Core files the accounts under their plan after the first usage read.
      planRead = true;
      release();
      await flush();
      await flush();
      expect(cardNames()).toEqual(["Codex Plus"]);
      expect(tray()).toBeNull();
      expect(expandButton("Codex Plus").getAttribute("aria-expanded")).toBe(
        "false",
      );
      expect(quotaOf(packageCard("Codex Plus")).status).toBe(
        "1 个号有额度，平均剩 35%",
      );
    });

    it("shows single cards and a retry when the package list fails", async () => {
      // Every read fails until the retry; a usage read also asks again.
      let failing = true;
      bridgeMocks.getServicePackages.mockImplementation(async () => {
        if (failing) throw "GET /control/v1/service-packages returned 503";
        return [plusPackage];
      });
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
        usageOf(id),
      );
      await renderPackages([plusA, plusB]);
      const warning = container.querySelector<HTMLElement>(
        '[data-testid="service-packages-error"]',
      );
      expect(warning?.textContent).toContain(
        "套餐分组没读到，账户先按单张卡显示。GET /control/v1/service-packages returned 503",
      );
      expect(cardNames()).toEqual(["Codex A", "Codex B"]);

      failing = false;
      await clickButton("重试");
      await flush();
      expect(
        container.querySelector('[data-testid="service-packages-error"]'),
      ).toBeNull();
      expect(cardNames()).toEqual(["Codex Plus"]);
    });

    it("draws unlimited keys and balance-only accounts with a striped bar", async () => {
      const unlimited: Service = {
        ...gatewayService,
        id: "service_unlimited",
        name: "Unlimited key",
        http: {
          ...gatewayService.http!,
          credential_ref: "local://service/service_unlimited",
        },
      };
      const grok: Service = {
        ...codexService,
        id: "service_grok",
        name: "Grok personal",
        kind: "grok_subscription",
        subscription: { provider: "xai_grok", status: "connected" },
      };
      pricingMocks.getBillingSummary.mockResolvedValue(
        billingSummary([
          { service_id: unlimited.id, amount_usd: "6.29", requests: 4 },
        ]),
      );
      bridgeMocks.getServiceUsage.mockImplementation(
        async (id: string): Promise<SubscriptionUsage> => ({
          service_id: id,
          fetched_at: new Date().toISOString(),
          ...(id === unlimited.id
            ? { quota: { unlimited: true, used_usd: "6.29" } }
            : {
                credits: {
                  has_credits: true,
                  unlimited: false,
                  balance: "$43.20",
                },
              }),
        }),
      );
      await renderPackages([grok, unlimited]);

      expect(quotaOf(packageCard("Unlimited key"))).toEqual({
        status: "不限额度",
        tone: "positive",
        bar: "empty",
        barLabel: null,
        caption: ["已用 $6.29", "没有总额"],
        busy: false,
      });
      expect(quotaOf(packageCard("Grok personal"))).toEqual({
        status: "余额 $43.20",
        tone: "positive",
        bar: "empty",
        barLabel: null,
        caption: ["只报余额", "没有总额"],
        busy: false,
      });
      const cost = (name: string) =>
        packageCard(name).querySelector('[data-testid="package-cost"]');
      expect(cost("Unlimited key")?.textContent).toBe("$6.29");
      expect(cost("Unlimited key")?.getAttribute("aria-label")).toBe(
        "Unlimited key 近 7 天费用 $6.29，查看明细",
      );
      // No billed request in the period costs nothing.
      expect(cost("Grok personal")?.textContent).toBe("$0.00");
    });
  });

  async function openEditorTab(
    tab: "connection" | "models" | "protocols" | "failure",
  ): Promise<void> {
    const trigger = container.querySelector<HTMLButtonElement>(
      `[data-testid="service-editor-tab-${tab}"]`,
    );
    if (!trigger) throw new Error(`missing editor tab: ${tab}`);
    await act(async () => {
      trigger.click();
      await Promise.resolve();
    });
  }

  it("saves instance proxy authentication before starting subscription login", async () => {
    const claude: Service = {
      ...codexService,
      id: "service_claude_proxy",
      kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "disconnected" },
    };
    bridgeMocks.createService.mockResolvedValue({ service: claude, etag });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: {
        id: "authorization_proxy",
        provider: "claude_code",
        status: "pending",
        flow: "authorization_code",
        authorization_url: "https://claude.com/cai/oauth/authorize",
        service_id: claude.id,
      },
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      ),
    );
    await chooseServiceKind("Claude Code 订阅");
    await chooseOption("代理模式", "自定义代理");
    for (const [label, value] of [
      ["代理地址", "socks5://127.0.0.1:1080"],
      ["代理用户名（可选）", "proxy-user"],
      ["代理密码（可选）", "proxy-secret"],
    ]) {
      const input = container.querySelector<HTMLInputElement>(
        `input[aria-label="${label}"]`,
      )!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(input, value);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({
        proxy: {
          mode: "custom",
          url: "socks5://127.0.0.1:1080",
          credential: { username: "proxy-user", password: "proxy-secret" },
        },
      }),
    );
    expect(bridgeMocks.createService.mock.invocationCallOrder[0]).toBeLessThan(
      bridgeMocks.beginServiceAuthorization.mock.invocationCallOrder[0],
    );
  });

  it("keeps stored proxy authentication when editing and sends null to restore inheritance", async () => {
    const service: Service = {
      ...gatewayService,
      proxy: {
        mode: "custom",
        url: "http://proxy.example:8080",
        credential_ref: `local://service-proxy/${gatewayService.id}`,
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service, etag });
    bridgeMocks.updateService.mockResolvedValue({ service, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[service]}
          view={{ kind: "edit", serviceId: service.id }}
        />,
      ),
    );
    expect(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="代理密码（可选）"]',
      )?.value,
    ).toBe("");
    expect(container.textContent).toContain("保留已保存认证");
    await chooseOption("代理模式", "继承全局");
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      service.id,
      etag,
      expect.objectContaining({ proxy: null }),
    );
  });

  it.each([
    {
      url: "socks5://user:secret@127.0.0.1:1080",
      username: "user",
      password: "secret",
      credential: { username: "user", password: "secret" },
    },
    {
      url: "socks5://user:@127.0.0.1:1080",
      username: "user",
      password: "",
      credential: { username: "user", password: "" },
    },
    {
      url: "socks5://127.0.0.1:1080",
      username: "",
      password: "",
      credential: undefined,
    },
  ])(
    "tests and saves a proxy draft with password '$password'",
    async ({ url, username, password, credential }) => {
      const service: Service = {
        ...gatewayService,
        proxy: {
          mode: "custom",
          url: "socks5://127.0.0.1:1080",
          credential_ref: `local://service-proxy/${gatewayService.id}`,
        },
      };
      bridgeMocks.getService.mockResolvedValue({ service, etag });
      bridgeMocks.updateService.mockResolvedValue({ service, etag });
      bridgeMocks.probeServiceProxy.mockResolvedValue({
        latency_ms: 23,
        status_code: 401,
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[service]}
            view={{ kind: "edit", serviceId: service.id }}
          />,
        ),
      );
      const address = container.querySelector<HTMLInputElement>(
        'input[aria-label="代理地址"]',
      )!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(address, url);
        address.dispatchEvent(new Event("input", { bubbles: true }));
      });
      expect(address.value).toBe("socks5://127.0.0.1:1080");
      expect(
        container.querySelector<HTMLInputElement>(
          'input[aria-label="代理用户名（可选）"]',
        )!.value,
      ).toBe(username);
      expect(
        container.querySelector<HTMLInputElement>(
          'input[aria-label="代理密码（可选）"]',
        )!.value,
      ).toBe(password);
      const proxy = {
        mode: "custom",
        url: "socks5://127.0.0.1:1080",
        ...(credential ? { credential } : {}),
      };
      const testButton = [...container.querySelectorAll("button")].find(
        (button) => button.textContent === "测试连通性",
      )!;
      await act(async () => testButton.click());
      expect(bridgeMocks.probeServiceProxy).toHaveBeenCalledWith({
        service_id: service.id,
        proxy,
        target_url: service.http!.base_url,
      });
      expect(bridgeMocks.updateService).not.toHaveBeenCalled();
      expect(container.textContent).toContain("已收到 HTTP 401 响应");
      await act(async () =>
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          ),
      );
      expect(bridgeMocks.updateService).toHaveBeenCalledWith(
        service.id,
        etag,
        expect.objectContaining({ proxy }),
      );
    },
  );

  it.each([
    {
      choice: null,
      mode: "official",
      flow: "claude_cli",
      authorizationURL: "https://claude.ai/oauth/authorize",
    },
    {
      choice: "直接连接",
      mode: "legacy",
      flow: "authorization_code",
      authorizationURL: "https://claude.com/cai/oauth/authorize",
    },
  ] as const)(
    "creates a $mode Claude subscription and signs in with $flow",
    async ({ choice, mode, flow, authorizationURL }) => {
      const claude: Service = {
        ...codexService,
        id: "service_claude",
        name: "Claude Code",
        kind: "claude_subscription",
        subscription: { provider: "claude_code", status: "disconnected", mode },
      };
      bridgeMocks.createService.mockResolvedValue({ service: claude, etag });
      bridgeMocks.beginServiceAuthorization.mockResolvedValue({
        kind: "session",
        session: {
          id: "authorization_claude",
          provider: "claude_code",
          status: "pending",
          flow,
          authorization_url: authorizationURL,
          service_id: claude.id,
          created_at: timestamp,
          updated_at: timestamp,
          expires_at: "2099-09-18T00:00:00Z",
        },
      });
      bridgeMocks.getServiceAuthorization.mockReturnValue(
        new Promise(() => {}),
      );
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[]}
            view={{ kind: "create", serviceKind: "codex_subscription" }}
          />,
        ),
      );
      await chooseServiceKind("Claude Code 订阅");
      expect(
        container.querySelector<HTMLInputElement>("#service-name")?.value,
      ).toBe("Claude Code");
      expect(
        container.querySelector('[role="radio"][aria-label="Device Code"]'),
      ).toBeNull();
      const official = container.querySelector<HTMLElement>(
        '[role="radio"][aria-label="通过 Claude Code"]',
      );
      expect(official?.getAttribute("aria-checked")).toBe("true");
      expect(container.textContent).toContain("使用 Claude Code 2.1.300。");
      if (choice) {
        await act(async () =>
          container
            .querySelector<HTMLElement>(
              `[role="radio"][aria-label="${choice}"]`,
            )!
            .click(),
        );
        expect(container.textContent).not.toContain("使用 Claude Code");
      }
      await act(async () =>
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          ),
      );
      expect(bridgeMocks.createService).toHaveBeenCalledWith({
        name: "Claude Code",
        kind: "claude_subscription",
        subscription_mode: mode,
        enabled: true,
        responses_websocket_enabled: false,
        models: [],
      });
      expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
        "service_claude",
        flow,
      );
    },
  );

  it("signs an official Claude account in again with the pasted code", async () => {
    const claude = parseService({
      ...codexService,
      id: "service_claude_official",
      name: "Claude Code",
      kind: "claude_subscription",
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
      subscription: {
        provider: "claude_code",
        status: "needs_reauth",
        mode: "official",
      },
    });
    const pending = {
      id: "authorization_claude",
      provider: "claude_code",
      status: "pending",
      flow: "claude_cli",
      authorization_url: "https://claude.ai/oauth/authorize",
      service_id: claude.id,
      created_at: timestamp,
      updated_at: timestamp,
      expires_at: "2099-09-18T00:00:00Z",
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: pending,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(pending);
    bridgeMocks.completeServiceAuthorization.mockResolvedValue({
      ...pending,
      status: "completed",
      authorization_url: undefined,
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[claude]}
          view={{ kind: "list" }}
        />,
      ),
    );
    await openServiceOverflow("Claude Code");
    await chooseMenuItem("登录");
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(
      dialog
        .querySelector('[role="radio"][aria-label="通过 Claude Code"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
    expect(
      dialog.querySelector('[role="radio"][aria-label="Claude Code 授权"]'),
    ).toBeNull();
    const start = [...dialog.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    )!;
    await act(async () => start.click());
    await act(async () => {});
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      claude.id,
      "claude_cli",
    );
    expect(bridgeMocks.openAuthorizationURL).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("登录 Claude Code");
    expect(document.body.textContent).toContain("在打开的页面登录 Claude 账号");
    const code = document.querySelector<HTMLInputElement>(
      "#claude-authorization-code",
    )!;
    expect(code.placeholder).toBe("");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(code, "pasted-code#state");
      code.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () =>
      code
        .closest("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.completeServiceAuthorization).toHaveBeenCalledWith(
      claude.id,
      "authorization_claude",
      "pasted-code#state",
    );
  });

  it.each(["create", "list"] as const)(
    "offers Antigravity browser authorization from the %s view",
    async (view) => {
      const service = parseService({
        ...codexService,
        id: "service_antigravity",
        name: "Antigravity",
        kind: "antigravity_subscription",
        capabilities: [
          {
            protocol: "google.generate_content",
            mode: "native",
            streaming: true,
          },
          { protocol: "google.models", mode: "native", streaming: false },
          { protocol: "openai.models", mode: "native", streaming: false },
        ],
        subscription: { provider: "antigravity", status: "disconnected" },
      });
      const authorizationURL = "https://accounts.google.com/o/oauth2/v2/auth";
      bridgeMocks.createService.mockResolvedValue({ service, etag });
      bridgeMocks.beginServiceAuthorization.mockResolvedValue({
        kind: "session",
        session: {
          id: "authorization_antigravity",
          provider: "antigravity",
          status: "pending",
          flow: "browser",
          authorization_url: authorizationURL,
          service_id: service.id,
          expires_at: "2099-09-18T00:00:00Z",
        },
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={view === "list" ? [service] : []}
            view={
              view === "list"
                ? { kind: "list" }
                : { kind: "create", serviceKind: "codex_subscription" }
            }
          />,
        ),
      );
      if (view === "create") {
        await chooseServiceKind("Antigravity 订阅");
        expect(
          container.querySelector<HTMLInputElement>("#service-name")?.value,
        ).toBe("Antigravity");
      } else {
        expect(container.textContent).toContain("Antigravity OAuth");
        await openServiceOverflow("Antigravity");
        await chooseMenuItem("登录");
      }
      expect(
        document.querySelector('[role="radio"][aria-label="Device Code"]'),
      ).toBeNull();
      expect(
        document.querySelector('[role="radio"][value="browser"]'),
      ).not.toBeNull();
      if (view === "create") {
        await act(async () =>
          container
            .querySelector("form")!
            .dispatchEvent(
              new Event("submit", { bubbles: true, cancelable: true }),
            ),
        );
        expect(bridgeMocks.createService).toHaveBeenCalledWith({
          name: "Antigravity",
          kind: "antigravity_subscription",
          enabled: true,
          responses_websocket_enabled: false,
          models: [],
        });
      } else {
        const start = [...document.querySelectorAll("button")].find(
          (button) => button.textContent === "开始登录",
        );
        expect(start?.disabled).toBe(false);
        await act(async () => start!.click());
      }
      expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
        service.id,
        "browser",
      );
      // The native begin command opens the browser; React reports its result.
      expect(notifyMocks.success).toHaveBeenCalledWith(
        expect.stringContaining("Antigravity"),
      );
    },
  );

  it("creates a Grok subscription that only offers Device Code", async () => {
    const grok: Service = {
      ...codexService,
      id: "service_grok",
      name: "Grok 订阅",
      kind: "grok_subscription",
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        { protocol: "openai.chat", mode: "native", streaming: true },
      ],
      subscription: { provider: "xai_grok", status: "disconnected" },
    };
    bridgeMocks.createService.mockResolvedValue({ service: grok, etag });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: {
        id: "authorization_grok",
        provider: "xai_grok",
        status: "pending",
        flow: "device_code",
        device_code: {
          verification_url:
            "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE",
          user_code: "GROK-CODE",
        },
        service_id: grok.id,
        created_at: timestamp,
        updated_at: timestamp,
        expires_at: "2099-09-18T00:00:00Z",
      },
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_grok",
      provider: "xai_grok",
      status: "pending",
      flow: "device_code",
      device_code: {
        verification_url:
          "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE",
        user_code: "GROK-CODE",
      },
      service_id: grok.id,
      created_at: timestamp,
      updated_at: timestamp,
      expires_at: "2099-09-18T00:00:00Z",
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      ),
    );
    await chooseServiceKind("Grok 订阅");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Grok 订阅");
    expect(
      container.querySelector('[role="radio"][aria-label="Device Code"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[role="radio"][aria-label="浏览器 OAuth"]'),
    ).toBeNull();
    expect(container.textContent).toContain("Device Code 登录");
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.createService).toHaveBeenCalledWith({
      name: "Grok 订阅",
      kind: "grok_subscription",
      enabled: true,
      responses_websocket_enabled: false,
      models: [],
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_grok",
      "device_code",
    );
  });

  it("signs a Grok subscription in with a Device Code dialog that never mentions OpenAI", async () => {
    const grok: Service = {
      ...codexService,
      id: "service_grok",
      name: "Grok 订阅",
      kind: "grok_subscription",
      subscription: { provider: "xai_grok", status: "disconnected" },
    };
    const session = {
      id: "authorization_grok",
      provider: "xai_grok",
      status: "pending",
      flow: "device_code",
      device_code: {
        verification_url:
          "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE",
        user_code: "GROK-CODE",
      },
      service_id: grok.id,
      created_at: timestamp,
      updated_at: timestamp,
      expires_at: "2099-09-18T00:00:00Z",
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(session);
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[grok]}
          view={{ kind: "list" }}
        />,
      ),
    );
    expect(container.textContent).toContain("xAI Grok OAuth");
    await openServiceOverflow("Grok 订阅");
    await chooseMenuItem("登录");
    const choice = document.querySelector('[role="dialog"]');
    expect(
      choice?.querySelector('[role="radio"][aria-label="Device Code"]'),
    ).not.toBeNull();
    expect(
      choice?.querySelector('[role="radio"][aria-label="浏览器 OAuth"]'),
    ).toBeNull();
    expect(choice?.textContent).not.toContain("OpenAI");
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    expect(start?.hasAttribute("disabled")).toBe(false);
    await act(async () => start!.click());
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_grok",
      "device_code",
    );
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("GROK-CODE");
    expect(dialog?.textContent).toContain("xAI 登录页面");
    expect(dialog?.textContent).not.toContain("OpenAI");
  });

  const copilotService: Service = {
    ...codexService,
    id: "service_copilot",
    name: "GitHub Copilot",
    kind: "copilot_subscription",
    capabilities: [
      { protocol: "anthropic.messages", mode: "native", streaming: true },
      { protocol: "openai.responses", mode: "native", streaming: true },
      { protocol: "openai.chat", mode: "native", streaming: true },
      { protocol: "openai.models", mode: "native", streaming: false },
    ],
    subscription: { provider: "github_copilot", status: "disconnected" },
  };
  const copilotSession = {
    id: "authorization_copilot",
    provider: "github_copilot",
    status: "pending",
    flow: "device_code",
    device_code: {
      verification_url: "https://github.com/login/device",
      user_code: "COPI-LOT1",
    },
    service_id: "service_copilot",
    created_at: timestamp,
    updated_at: timestamp,
    expires_at: "2099-09-18T00:00:00Z",
  };

  function copilotRiskDialog(): Element | null {
    return (
      [...document.querySelectorAll('[role="alertdialog"]')].find((dialog) =>
        dialog.textContent?.includes("连接 GitHub Copilot 有封号风险"),
      ) ?? null
    );
  }

  async function answerCopilotRisk(label: string): Promise<void> {
    const button = [
      ...(copilotRiskDialog()?.querySelectorAll("button") ?? []),
    ].find((candidate) => candidate.textContent === label);
    if (!button) throw new Error(`Missing Copilot risk button: ${label}`);
    await act(async () => button.click());
  }

  it("confirms the Copilot account risk before creating and signing in", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: copilotService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: copilotSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(copilotSession);
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      ),
    );
    await chooseServiceKind("GitHub Copilot 订阅");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("GitHub Copilot");
    expect(
      container.querySelector('[role="radio"][aria-label="Device Code"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[role="radio"][aria-label="浏览器 OAuth"]'),
    ).toBeNull();
    const submit = () =>
      act(async () =>
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          ),
      );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="service-editor-tab-redirects"]',
        )!
        .click(),
    );
    // Built-in rows show before the account lists any model.
    expect(container.querySelectorAll("[data-redirect-row]")).toHaveLength(12);
    expect(container.textContent).toContain("claude-fable-5-2");
    await submit();
    expect(copilotRiskDialog()?.textContent).toContain("封禁 GitHub 账号");
    expect(bridgeMocks.createService).not.toHaveBeenCalled();
    await answerCopilotRisk("取消");
    expect(bridgeMocks.createService).not.toHaveBeenCalled();
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();

    await submit();
    await answerCopilotRisk("我已了解风险，继续");
    expect(bridgeMocks.createService).toHaveBeenCalledWith({
      name: "GitHub Copilot",
      kind: "copilot_subscription",
      enabled: true,
      responses_websocket_enabled: false,
      models: [],
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_copilot",
      "device_code",
    );
  });

  it("signs a Copilot subscription in only after the risk confirmation", async () => {
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: copilotSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(copilotSession);
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[copilotService]}
          view={{ kind: "list" }}
        />,
      ),
    );
    expect(container.textContent).toContain("GitHub Copilot OAuth");
    await openServiceOverflow("GitHub Copilot");
    await chooseMenuItem("登录");
    const choice = document.querySelector('[role="dialog"]');
    expect(
      choice?.querySelector('[role="radio"][aria-label="浏览器 OAuth"]'),
    ).toBeNull();
    expect(choice?.textContent).not.toContain("OpenAI");
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => start!.click());
    expect(copilotRiskDialog()).not.toBeNull();
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    await answerCopilotRisk("我已了解风险，继续");
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_copilot",
      "device_code",
    );
    const dialog = [...document.querySelectorAll('[role="dialog"]')].find(
      (candidate) => candidate.textContent?.includes("COPI-LOT1"),
    );
    expect(dialog?.textContent).toContain("GitHub 设备授权页");
    expect(dialog?.textContent).toContain("OpenCode");
    expect(dialog?.textContent).not.toContain("OpenAI");
  });

  it("confirms the Droid account risk before creating and signing in", async () => {
    const droidService: Service = {
      ...copilotService,
      id: "service_droid",
      name: "Droid",
      kind: "droid_subscription",
      subscription: { provider: "factory_droid", status: "disconnected" },
    };
    const droidSession = {
      ...copilotSession,
      id: "authorization_droid",
      provider: "factory_droid",
      device_code: {
        verification_url: "https://auth.factory.ai/device?user_code=DROI-D001",
        user_code: "DROI-D001",
      },
      service_id: "service_droid",
    };
    bridgeMocks.createService.mockResolvedValue({
      service: droidService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: droidSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(droidSession);
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      ),
    );
    await chooseServiceKind("Droid 订阅");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Droid");
    expect(
      container.querySelector('[role="radio"][aria-label="Device Code"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("以 Droid CLI 的身份");
    const submit = () =>
      act(async () =>
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          ),
      );
    await submit();
    const riskDialog = () =>
      [...document.querySelectorAll('[role="alertdialog"]')].find((dialog) =>
        dialog.textContent?.includes("连接 Droid 有封号风险"),
      ) ?? null;
    expect(riskDialog()?.textContent).toContain("Droid 的自我介绍");
    expect(riskDialog()?.textContent).not.toContain("GitHub");
    expect(bridgeMocks.createService).not.toHaveBeenCalled();
    const confirm = [...(riskDialog()?.querySelectorAll("button") ?? [])].find(
      (button) => button.textContent === "我已了解风险，继续",
    );
    await act(async () => confirm!.click());
    expect(bridgeMocks.createService).toHaveBeenCalledWith({
      name: "Droid",
      kind: "droid_subscription",
      enabled: true,
      responses_websocket_enabled: false,
      models: [],
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_droid",
      "device_code",
    );
  });

  it("signs a Droid subscription in with a Factory device dialog after the risk confirmation", async () => {
    const droidService: Service = {
      ...copilotService,
      id: "service_droid",
      name: "Droid",
      kind: "droid_subscription",
      subscription: { provider: "factory_droid", status: "disconnected" },
    };
    const droidSession = {
      ...copilotSession,
      id: "authorization_droid",
      provider: "factory_droid",
      device_code: {
        verification_url: "https://auth.factory.ai/device?user_code=DROI-D001",
        user_code: "DROI-D001",
      },
      service_id: "service_droid",
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: droidSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(droidSession);
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[droidService]}
          view={{ kind: "list" }}
        />,
      ),
    );
    expect(container.textContent).toContain("Factory Droid OAuth");
    await openServiceOverflow("Droid");
    await chooseMenuItem("登录");
    const choice = document.querySelector('[role="dialog"]');
    expect(
      choice?.querySelector('[role="radio"][aria-label="Device Code"]'),
    ).not.toBeNull();
    expect(
      choice?.querySelector('[role="radio"][aria-label="浏览器 OAuth"]'),
    ).toBeNull();
    expect(choice?.textContent).toContain("Factory 登录页");
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => start!.click());
    const riskDialog = [
      ...document.querySelectorAll('[role="alertdialog"]'),
    ].find((dialog) => dialog.textContent?.includes("连接 Droid 有封号风险"));
    expect(riskDialog).not.toBeUndefined();
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    const confirm = [...(riskDialog?.querySelectorAll("button") ?? [])].find(
      (button) => button.textContent === "我已了解风险，继续",
    );
    await act(async () => confirm!.click());
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      "service_droid",
      "device_code",
    );
    const dialog = [...document.querySelectorAll('[role="dialog"]')].find(
      (candidate) => candidate.textContent?.includes("DROI-D001"),
    );
    expect(dialog?.textContent).toContain("Factory 登录页面");
    expect(dialog?.textContent).not.toContain("OpenAI");
    expect(dialog?.textContent).not.toContain("GitHub");
  });

  it("lists Copilot's built-in redirects with its own rules and saves a turned-off row", async () => {
    const copilot: Service = {
      ...copilotService,
      models: ["claude-fable-6.0", "claude-sonnet-4.6"],
      model_redirects: [
        { from: "claude-fable-6-0", to: "claude-fable-6.0", enabled: true },
      ],
      subscription: { provider: "github_copilot", status: "connected" },
    };
    bridgeMocks.getService.mockResolvedValue({ service: copilot, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: copilot, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[copilot]}
          view={{ kind: "edit", serviceId: copilot.id }}
        />,
      ),
    );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="service-editor-tab-redirects"]',
        )!
        .click(),
    );
    expect(container.querySelectorAll("[data-redirect-row]")).toHaveLength(13);
    const sonnet = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="启用 claude-sonnet-4-6 的重定向"]',
    );
    expect(sonnet?.getAttribute("aria-checked")).toBe("true");
    await act(async () => sonnet!.click());
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      copilot.id,
      etag,
      expect.objectContaining({
        model_redirects: [
          { from: "claude-fable-6-0", to: "claude-fable-6.0", enabled: true },
          {
            from: "claude-sonnet-4-6",
            to: "claude-sonnet-4.6",
            enabled: false,
          },
        ],
      }),
    );
  });

  it("submits a Claude code only to its active service and clears the input", async () => {
    const claude: Service = {
      ...codexService,
      id: "service_claude",
      name: "Claude Code",
      kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "disconnected" },
    };
    const session = {
      id: "authorization_claude",
      provider: "claude_code",
      status: "pending",
      flow: "authorization_code",
      authorization_url: "https://claude.com/cai/oauth/authorize",
      service_id: claude.id,
      created_at: timestamp,
      updated_at: timestamp,
      expires_at: "2099-09-18T00:00:00Z",
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(session);
    bridgeMocks.completeServiceAuthorization.mockResolvedValue({
      ...session,
      status: "completed",
      authorization_url: undefined,
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[claude]}
          view={{ kind: "list" }}
        />,
      ),
    );
    await openServiceOverflow("Claude Code");
    await chooseMenuItem("登录");
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    expect(start).toBeDefined();
    await act(async () => start!.click());
    const input = document.querySelector<HTMLInputElement>(
      "#claude-authorization-code",
    )!;
    expect(input.type).toBe("password");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "code-secret#state");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () =>
      input
        .closest("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.completeServiceAuthorization).toHaveBeenCalledWith(
      "service_claude",
      "authorization_claude",
      "code-secret#state",
    );
    expect(document.querySelector("#claude-authorization-code")).toBeNull();
    expect(document.body.textContent).not.toContain("code-secret");
  });

  it("renders multiple Codex subscriptions and a gateway as cards in two sections", async () => {
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[codexService, gatewayService, secondCodexService]}
          view={{ kind: "list" }}
        />,
      );
    });

    const section = (name: string) =>
      [
        ...container.querySelectorAll(
          `[data-testid="package-section-${name}"] [data-testid="package-card"]`,
        ),
      ].map((card) => card.getAttribute("aria-label"));
    expect(section("subscription")).toEqual(["Codex personal", "Codex work"]);
    expect(section("relay")).toEqual(["new-api"]);
    expect(
      packageCard("new-api").querySelector('[aria-label="New API"]'),
    ).not.toBeNull();
    expect(
      container.querySelectorAll(
        '[data-testid="package-card"] [aria-label="Codex 订阅"]',
      ),
    ).toHaveLength(2);
    // The relay's line under its name is the host; the full address is its title.
    const host = [...packageCard("new-api").querySelectorAll("div")].find(
      (node) => node.textContent === "gateway.example",
    );
    expect(host?.getAttribute("title")).toBe("https://gateway.example/v1");
    expect(container.textContent).not.toContain("Logout is local-only");
    // Only the New API key has a quota to read; both Codex rows are disconnected.
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(
      gatewayService.id,
      { fresh: false },
    );
  });

  it("combines status and search filters and clears both from an empty result", async () => {
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService, { ...codexService, enabled: false }]}
          view={{ kind: "list" }}
        />,
      );
    });
    const disabledFilter = [
      ...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]'),
    ].find((button) => button.textContent?.startsWith("已停用"));
    if (!disabledFilter) throw new Error("Missing disabled filter");
    await act(async () => disabledFilter.click());
    expect(disabledFilter.getAttribute("aria-checked")).toBe("true");
    expect(cardNames()).toEqual(["Codex personal"]);

    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索 API 提供商"]',
    );
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!search || !setter) throw new Error("Missing service search");
    await act(async () => {
      setter.call(search, "gateway.example");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(cardNames()).toEqual([]);
    const clear = [
      ...container.querySelectorAll<HTMLButtonElement>("button"),
    ].find((button) => button.textContent === "清除筛选");
    if (!clear) throw new Error("Missing clear filters");
    await act(async () => clear.click());
    expect(search.value).toBe("");
    expect(cardNames()).toEqual(["Codex personal", "new-api"]);

    await act(async () => {
      setter.call(search, "gateway.example");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(cardNames()).toEqual(["new-api"]);
  });

  it.each([true, false])(
    "reorders services in the enabled=%s status filter without moving hidden services",
    async (enabled) => {
      const services = Array.from(
        { length: 5 },
        (_, index): Service => ({
          ...gatewayService,
          id: `service_${index}`,
          name: `Gateway ${index}`,
          enabled: index % 2 === 1 ? enabled : !enabled,
        }),
      );
      bridgeMocks.getServiceOrder.mockResolvedValueOnce({
        service_ids: services.map((service) => service.id),
        etag,
      });
      bridgeMocks.updateServiceOrder.mockImplementationOnce(
        async (service_ids) => ({ service_ids, etag }),
      );
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            services={services}
            protocols={[]}
            view={{ kind: "list" }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
      const statusFilter = [
        ...container.querySelectorAll<HTMLButtonElement>(
          'button[role="radio"]',
        ),
      ].find((button) =>
        button.textContent?.startsWith(enabled ? "已启用" : "已停用"),
      )!;
      await act(async () => statusFilter.click());
      const handle = container.querySelector<HTMLButtonElement>(
        '[data-grid-item="service:service_3"] [data-grid-grip]',
      )!;
      expect(handle.disabled).toBe(false);
      expect(handle.getAttribute("aria-label")).toBe(
        "移动 Gateway 3，当前第 2 位",
      );
      await act(async () =>
        handle.dispatchEvent(
          new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }),
        ),
      );
      expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
        ["service_0", "service_3", "service_2", "service_1", "service_4"],
        etag,
      );
      expect(cardNames()).toEqual(["Gateway 3", "Gateway 1"]);
      // Focus stays on the moved card's grip for the next key.
      expect(document.activeElement?.closest("[data-grid-item]")).toBe(
        container.querySelector('[data-grid-item="service:service_3"]'),
      );
      const all = [
        ...container.querySelectorAll<HTMLButtonElement>(
          'button[role="radio"]',
        ),
      ].find((button) => button.textContent?.startsWith("全部"))!;
      await act(async () => all.click());
      expect(cardNames()).toEqual([
        "Gateway 0",
        "Gateway 3",
        "Gateway 2",
        "Gateway 1",
        "Gateway 4",
      ]);
    },
  );

  it("searches model names with the status filter, saves the visible order and clears both", async () => {
    const services: Service[] = [
      {
        ...gatewayService,
        id: "service_other_model",
        name: "GPT-5 name only",
        models: ["claude-sonnet-4-5"],
      },
      { ...gatewayService, id: "service_first", name: "First gateway" },
      {
        ...gatewayService,
        id: "service_disabled",
        enabled: false,
        models: ["claude-sonnet-4-5"],
      },
      {
        ...gatewayService,
        id: "service_second",
        name: "Second gateway",
        models: ["gpt-5.4", "claude-sonnet-4-5"],
      },
      { ...codexService, name: "GPT-5 unconfigured" },
    ];
    bridgeMocks.getServiceOrder.mockResolvedValueOnce({
      service_ids: services.map((service) => service.id),
      etag,
    });
    bridgeMocks.updateServiceOrder.mockImplementationOnce(
      async (service_ids) => ({ service_ids, etag }),
    );
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          services={services}
          protocols={[]}
          view={{ kind: "list" }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索 API 提供商"]',
    )!;
    expect(search.placeholder).toBe("搜索名称、地址或账户");
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!;
    await act(async () => {
      setter.call(search, "  Claude-Sonnet  ");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(cardNames()).toEqual([
      "GPT-5 name only",
      "new-api",
      "Second gateway",
    ]);
    const enabled = [
      ...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]'),
    ].find((button) => button.textContent?.startsWith("已启用"))!;
    await act(async () => enabled.click());
    expect(cardNames()).toEqual(["GPT-5 name only", "Second gateway"]);
    const handle = container.querySelector<HTMLButtonElement>(
      '[data-grid-item="service:service_second"] [data-grid-grip]',
    )!;
    expect(handle.disabled).toBe(false);
    await act(async () =>
      handle.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }),
      ),
    );
    // The hidden cards keep their slots; the two visible ones swap.
    expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
      [
        "service_second",
        "service_first",
        "service_disabled",
        "service_other_model",
        codexService.id,
      ],
      etag,
    );
    expect(cardNames()).toEqual(["Second gateway", "GPT-5 name only"]);
    await act(async () => {
      setter.call(search, "no-such-model");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(cardNames()).toEqual([]);
    await clickButton("清除筛选");
    expect(search.value).toBe("");
    expect(
      [...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]')]
        .find((button) => button.textContent?.startsWith("全部"))
        ?.getAttribute("aria-checked"),
    ).toBe("true");
    expect(cardNames()).toEqual([
      "GPT-5 unconfigured",
      "Second gateway",
      "First gateway",
      "new-api",
      "GPT-5 name only",
    ]);
  });

  it.each(["ready", "error"] as const)(
    "renders the list while quotas load independently, including %s, and preserves settled data on refresh",
    async (outcome) => {
      const connected = [codexService, secondCodexService].map((service) => ({
        ...service,
        subscription: {
          ...service.subscription!,
          status: "connected" as const,
        },
      }));
      const requests = connected.map(() => {
        let resolve!: (value: SubscriptionUsage) => void;
        let reject!: (cause: Error) => void;
        const promise = new Promise<SubscriptionUsage>((done, fail) => {
          resolve = done;
          reject = fail;
        });
        return { promise, resolve, reject };
      });
      for (const request of requests) {
        bridgeMocks.getServiceUsage.mockImplementationOnce(
          () => request.promise,
        );
      }
      const renderList = async (show: boolean) =>
        act(async () =>
          root.render(
            <WorkspaceSnapshotProvider sessionKey="quota-session">
              {show ? (
                <ServiceManager
                  catalogError={null}
                  catalogStatus="ready"
                  isReady
                  onDirtyChange={() => {}}
                  onRefresh={() => {}}
                  onServiceRemoved={() => {}}
                  onServiceSaved={() => {}}
                  onViewChange={() => {}}
                  protocols={[]}
                  services={connected}
                  view={{ kind: "list" }}
                />
              ) : null}
            </WorkspaceSnapshotProvider>,
          ),
        );
      const busy = () =>
        container.querySelectorAll('[data-slot="quota-summary"][aria-busy]');
      const bars = () =>
        container.querySelectorAll(
          '[data-slot="quota-bar"]:not([data-bar="empty"])',
        );
      await renderList(true);
      expect(cardNames()).toEqual(["Codex personal", "Codex work"]);
      expect(busy()).toHaveLength(2);
      const usage = (index: number): SubscriptionUsage => ({
        service_id: connected[index].id,
        fetched_at: timestamp,
        secondary: { used_percent: 86, limit_window_seconds: 604_800 },
      });
      const initialCards = [
        ...container.querySelectorAll('[data-testid="package-card"]'),
      ];
      await act(async () => requests[0].resolve(usage(0)));
      expect([
        ...container.querySelectorAll('[data-testid="package-card"]'),
      ]).toEqual(initialCards);
      expect(busy()).toHaveLength(1);
      // A slow quota request must not hold back either provider card.
      expect(cardNames()).toEqual(["Codex personal", "Codex work"]);
      const log = vi.spyOn(console, "error").mockImplementation(() => {});
      try {
        await act(async () => {
          if (outcome === "ready") requests[1].resolve(usage(1));
          else requests[1].reject(new Error("Quota temporarily unavailable"));
        });
        expect(cardNames()).toEqual(["Codex personal", "Codex work"]);
        expect(busy()).toHaveLength(0);
        expect(bars()).toHaveLength(outcome === "ready" ? 2 : 1);
        if (outcome === "error")
          expect(quotaOf(packageCard("Codex work")).status).toBe("额度读不到");

        // Re-entry and manual refresh both keep the settled snapshot visible.
        await renderList(false);
        for (let index = 0; index < 4; index++) {
          bridgeMocks.getServiceUsage.mockImplementationOnce(
            () => new Promise(() => {}),
          );
        }
        await renderList(true);
        const card = container.querySelector('[data-testid="package-card"]');
        expect(card).not.toBeNull();
        expect(busy()).toHaveLength(0);
        expect(bars()).toHaveLength(outcome === "ready" ? 2 : 1);
        await act(async () =>
          container
            .querySelector<HTMLButtonElement>('button[aria-label="刷新列表"]')!
            .click(),
        );
        expect(container.querySelector('[data-testid="package-card"]')).toBe(
          card,
        );
        expect(busy()).toHaveLength(0);
        expect(bars()).toHaveLength(outcome === "ready" ? 2 : 1);
        if (outcome === "error")
          expect(quotaOf(packageCard("Codex work")).status).toBe("额度读不到");
      } finally {
        log.mockRestore();
      }
    },
  );

  it.each(["ready", "error"] as const)(
    "retries only the failed quota, prevents duplicate requests, and handles %s",
    async (outcome) => {
      const connected = [codexService, secondCodexService].map((service) => ({
        ...service,
        subscription: {
          ...service.subscription!,
          status: "connected" as const,
        },
      }));
      let resolveOther!: (usage: SubscriptionUsage) => void;
      let resolveRetry!: (usage: SubscriptionUsage) => void;
      let rejectRetry!: (cause: Error) => void;
      bridgeMocks.getServiceUsage
        .mockRejectedValueOnce(new Error("Quota temporarily unavailable"))
        .mockImplementationOnce(
          () =>
            new Promise<SubscriptionUsage>((resolve) => {
              resolveOther = resolve;
            }),
        )
        .mockImplementationOnce(
          () =>
            new Promise<SubscriptionUsage>((resolve, reject) => {
              resolveRetry = resolve;
              rejectRetry = reject;
            }),
        );
      const onRefresh = vi.fn();
      const logged = vi.spyOn(console, "error").mockImplementation(() => {});
      try {
        await act(async () => {
          root.render(
            <ServiceManager
              catalogError={null}
              catalogStatus="ready"
              isReady
              onDirtyChange={() => {}}
              onRefresh={onRefresh}
              onServiceRemoved={() => {}}
              onServiceSaved={() => {}}
              onViewChange={() => {}}
              protocols={[]}
              services={connected}
              view={{ kind: "list" }}
            />,
          );
        });
        const failedCard = packageCard("Codex personal");
        expect(quotaOf(failedCard)).toMatchObject({
          status: "额度读不到",
          tone: "neutral",
          bar: undefined,
          caption: ["Codex 订阅：Quota temporarily unavailable", "重试"],
        });
        expect(
          failedCard
            .querySelector('[data-slot="quota-caption"] [title]')
            ?.getAttribute("title"),
        ).toBe("Quota temporarily unavailable");
        const retry = () =>
          [
            ...failedCard.querySelectorAll<HTMLButtonElement>(
              '[data-slot="quota-caption"] button',
            ),
          ][0];
        const refresh = retry()!;
        expect(refresh.textContent).toBe("重试");
        await act(async () => {
          refresh.click();
          refresh.click();
        });
        expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(3);
        expect(bridgeMocks.getServiceUsage).toHaveBeenLastCalledWith(
          connected[0].id,
          { fresh: true },
        );
        expect(onRefresh).not.toHaveBeenCalled();
        expect(refresh.disabled).toBe(true);
        expect(refresh.textContent).toBe("刷新中…");
        expect(quotaOf(failedCard).status).toBe("额度读不到");

        // Retrying one card must not invalidate another card's in-flight request.
        await act(async () => {
          resolveOther({
            service_id: connected[1].id,
            fetched_at: timestamp,
            secondary: { used_percent: 20, limit_window_seconds: 604_800 },
          });
          if (outcome === "ready") {
            resolveRetry({
              service_id: connected[0].id,
              fetched_at: timestamp,
              secondary: { used_percent: 30, limit_window_seconds: 604_800 },
            });
          } else {
            rejectRetry(new Error("Provider is still unavailable"));
          }
        });
        expect(
          container.querySelectorAll(
            '[data-slot="quota-bar"]:not([data-bar="empty"])',
          ),
        ).toHaveLength(outcome === "ready" ? 2 : 1);
        if (outcome === "ready") {
          expect(quotaOf(failedCard).status).not.toContain("额度读不到");
          expect(retry()).toBeUndefined();
        } else {
          expect(failedCard.textContent).toContain(
            "Provider is still unavailable",
          );
          expect(retry()?.disabled).toBe(false);
          expect(retry()?.textContent).toBe("重试");
        }
      } finally {
        logged.mockRestore();
      }
    },
  );

  it("shows the provider plan quota on Kimi and New API key cards", async () => {
    const kimi: Service = {
      id: "service_kimi_plan",
      name: "Kimi Coding",
      kind: "kimi_coding",
      enabled: true,
      models: ["kimi-k2-thinking"],
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
      ],
      http: {
        base_url: "https://api.kimi.com/coding",
        auth: { scheme: "anthropic_api_key" },
        credential_ref: "local://service/service_kimi_plan",
      },
      created_at: timestamp,
      updated_at: timestamp,
    };
    bridgeMocks.getServiceUsage.mockImplementation(async (id: string) =>
      id === kimi.id
        ? {
            service_id: kimi.id,
            fetched_at: new Date().toISOString(),
            limit_reached: false,
            primary: {
              used_percent: 25,
              limit_window_seconds: 18_000,
              reset_at: hoursFromNow(4),
            },
            secondary: {
              used_percent: 10,
              limit_window_seconds: 604_800,
              reset_at: hoursFromNow(60),
            },
          }
        : {
            service_id: gatewayService.id,
            fetched_at: new Date().toISOString(),
            limit_reached: false,
            quota: {
              unlimited: false,
              used_usd: "7.5",
              remaining_usd: "2.5",
              total_usd: "10",
            },
          },
    );

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[kimi, gatewayService, codexService]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    // The coding plan and the New API key query usage; the Codex row is
    // disconnected.
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(2);
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(kimi.id, {
      fresh: false,
    });
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(
      gatewayService.id,
      { fresh: false },
    );
    // A coding plan sits with the subscriptions; its two windows nest in one bar.
    expect(
      packageCard("Kimi Coding").closest(
        '[data-testid="package-section-subscription"]',
      ),
    ).not.toBeNull();
    expect(quotaOf(packageCard("Kimi Coding"))).toEqual({
      status: "5 小时 剩 75%",
      statusEnd: expect.stringMatching(fiveHourReset),
      tone: "positive",
      bar: "nested",
      barLabel: "5 小时 剩 75% · 每周 剩 90%",
      caption: [expect.stringMatching(/^每周 剩 90% · \S+ \d{2}:\d{2} 重置$/)],
      busy: false,
    });
    expect(quotaOf(packageCard("new-api"))).toEqual({
      status: "还剩 $2.50",
      tone: "positive",
      bar: "flat",
      barLabel: "剩余 25%",
      caption: ["共 $10.00", "25%"],
      busy: false,
    });
    expect(
      container.querySelector('[data-testid="subscription-plan"]'),
    ).toBeNull();
    await openServiceOverflow("Kimi Coding");
    expect(
      document.querySelector('[data-testid="subscription-usage-reset"]'),
    ).toBeNull();
  });

  it("flags a failed toolbar refresh instead of passing the last key quota off as current", async () => {
    bridgeMocks.getServiceUsage.mockResolvedValueOnce({
      service_id: gatewayService.id,
      fetched_at: timestamp,
      limit_reached: false,
      quota: {
        unlimited: false,
        used_usd: "0",
        remaining_usd: "300",
        total_usd: "300",
      },
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const card = packageCard("new-api");
    expect(quotaOf(card).status).toBe("还剩 $300.00");
    const toolbarRefresh = container.querySelector<HTMLButtonElement>(
      'button[aria-label="刷新列表"]',
    )!;
    expect(toolbarRefresh.querySelector(".animate-spin")).toBeNull();

    let reject!: (cause: Error) => void;
    bridgeMocks.getServiceUsage.mockImplementationOnce(
      () =>
        new Promise((_, fail) => {
          reject = fail;
        }),
    );
    await act(async () => toolbarRefresh.click());
    expect(bridgeMocks.getServiceUsage).toHaveBeenLastCalledWith(
      gatewayService.id,
      { fresh: true },
    );
    // The card keeps its numbers while the lookup runs; the toolbar shows it.
    expect(toolbarRefresh.querySelector(".animate-spin")).not.toBeNull();
    expect(quotaOf(card).status).toBe("还剩 $300.00");

    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await act(async () =>
        reject(
          new Error(
            `GET /control/v1/services/service_gateway/usage?refresh=1 returned 502 Bad Gateway: {"error":{"code":"subscription_usage_failed","message":"new-api usage unavailable: status 401","details":[{"reason":"key_rejected"}]}}`,
          ),
        ),
      );
    } finally {
      log.mockRestore();
    }
    expect(toolbarRefresh.querySelector(".animate-spin")).toBeNull();
    // The old balance is not shown as if it were current.
    expect(quotaOf(card)).toMatchObject({
      status: "额度读不到",
      tone: "neutral",
      bar: undefined,
    });
    expect(card.textContent).not.toContain("$300.00");
    const caption = card.querySelector('[data-slot="quota-caption"]')!;
    expect(caption.textContent).toContain("New API 拒绝了这把密钥");
    expect(caption.querySelector("[title]")?.getAttribute("title")).toBe(
      "subscription_usage_failed: new-api usage unavailable: status 401",
    );
    expect(caption.querySelector("button")?.textContent).toBe("重试");
  });

  it.each([
    ["codex_subscription", "openai_codex", "pro", "Pro"],
    ["claude_subscription", "claude_code", "pro", "Pro"],
    ["grok_subscription", "xai_grok", "supergrok_heavy", "SuperGrok Heavy"],
  ] as const)(
    "shows the %s plan badge",
    async (kind, provider, planType, label) => {
      const connected: Service = {
        ...codexService,
        kind,
        subscription: { provider, status: "connected" },
      };
      bridgeMocks.getServiceUsage.mockResolvedValue({
        service_id: connected.id,
        fetched_at: timestamp,
        plan_type: planType,
        primary: { used_percent: 25 },
      });
      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[connected]}
            view={{ kind: "list" }}
          />,
        );
      });
      expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(connected.id, {
        fresh: false,
      });
      expect(
        container.querySelector('[data-testid="subscription-plan"]')
          ?.textContent,
      ).toBe(label);
      await openServiceOverflow("Codex personal");
      expect(
        document.querySelector('[data-testid="subscription-usage-reset"]'),
      ).toBeNull();
    },
  );

  it("shows rolling quota on a connected Codex card and its resets in the footer", async () => {
    const connected: Service = {
      ...codexService,
      enabled: false,
      subscription: {
        provider: "openai_codex",
        status: "connected",
        account_hint: "b03f***80",
      },
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: connected.id,
      fetched_at: new Date().toISOString(),
      plan_type: "plus",
      limit_reached: false,
      primary: {
        used_percent: 34,
        limit_window_seconds: 18_000,
        reset_at: hoursFromNow(2),
      },
      secondary: {
        used_percent: 12,
        limit_window_seconds: 604_800,
        reset_at: hoursFromNow(144),
      },
      additional_rate_limits: [
        {
          limit_name: "GPT-5.3-Codex-Spark",
          primary: { used_percent: 0, limit_window_seconds: 18_000 },
          secondary: { used_percent: 0, limit_window_seconds: 604_800 },
        },
        {
          limit_name: "gpt-reserve",
          primary: { used_percent: 0, limit_window_seconds: 604_800 },
        },
      ],
      rate_limit_reset_credits: { available_count: 2 },
    });
    bridgeMocks.getServiceResetCredits.mockResolvedValue({
      available_count: 2,
      credits: [{}, {}],
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[
            connected,
            {
              ...gatewayService,
              id: "service_compatible",
              name: "compatible",
              kind: "openai_compatible",
            },
          ]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(connected.id, {
      fresh: false,
    });
    const card = packageCard("Codex personal");
    expect(
      card.querySelector('[data-testid="subscription-plan"]')?.textContent,
    ).toBe("Plus");
    // The account is turned off, so its dot is grey while the numbers stay.
    expect(quotaOf(card)).toEqual({
      status: "5 小时 剩 66%",
      statusEnd: expect.stringMatching(fiveHourReset),
      tone: "neutral",
      bar: "nested",
      barLabel: "5 小时 剩 66% · 每周 剩 88%",
      caption: [expect.stringMatching(/^每周 剩 88% · \S+ \d{2}:\d{2} 重置$/)],
      busy: false,
    });
    expect(
      card.querySelectorAll('[data-slot="quota-bar"] [data-part]'),
    ).toHaveLength(2);
    // Extra per-model limits stay in the editor, not on the card.
    expect(card.textContent).not.toContain("GPT-5.3-Codex-Spark");
    expect(card.textContent).not.toContain("gpt-reserve");
    // The footer offers the resets; the menu no longer does.
    const reset = card.querySelector<HTMLButtonElement>(
      '[data-testid="subscription-usage-reset"]',
    );
    expect(reset?.textContent).toBe("2");
    expect(reset?.getAttribute("aria-label")).toBe("重置额度（剩 2 次）");
    await openServiceOverflow("Codex personal");
    expect(document.body.textContent).not.toContain("重置额度");
  });

  it("confirms a manual reset from the card footer", async () => {
    const connected: Service = {
      ...codexService,
      subscription: {
        provider: "openai_codex",
        status: "connected",
        account_hint: "b03f***80",
      },
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: connected.id,
      fetched_at: new Date().toISOString(),
      plan_type: "pro",
      primary: {
        used_percent: 34,
        limit_window_seconds: 18_000,
        reset_at: hoursFromNow(2),
      },
      additional_rate_limits: [
        {
          limit_name: "GPT-5.3-Codex-Spark",
          primary: { used_percent: 0, limit_window_seconds: 18_000 },
        },
      ],
      rate_limit_reset_credits: { available_count: 1 },
    });
    bridgeMocks.resetServiceUsage.mockResolvedValue({
      service_id: connected.id,
      outcome: "reset",
      windows_reset: 2,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[connected]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    const reset = packageCard(
      "Codex personal",
    ).querySelector<HTMLButtonElement>(
      '[data-testid="subscription-usage-reset"]',
    );
    if (!reset) throw new Error("missing reset button");
    await act(async () => reset.click());
    expect(bridgeMocks.resetServiceUsage).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("重置这个账户的额度？");
    expect(document.body.textContent).toContain("将消耗 1 次官方额度重置");
    const confirm = [
      ...document.querySelectorAll<HTMLButtonElement>("button"),
    ].find((button) => button.textContent?.trim() === "重置");
    if (!confirm) throw new Error("missing reset confirm");
    await act(async () => {
      confirm.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.resetServiceUsage).toHaveBeenCalledWith(connected.id);
    expect(bridgeMocks.getServiceUsage).toHaveBeenLastCalledWith(connected.id, {
      fresh: true,
    });
    expect(notifyMocks.success).toHaveBeenCalledWith("额度已重置。");
  });

  it("names resets about to expire on the footer button", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 10, 3, 12, 0));
    try {
      const today: Service = {
        ...codexService,
        subscription: { provider: "openai_codex", status: "connected" },
      };
      const soon: Service = {
        ...today,
        id: "service_codex_soon",
        name: "Codex soon",
      };
      bridgeMocks.getServiceUsage.mockImplementation(async (id: string) => ({
        service_id: id,
        fetched_at: new Date().toISOString(),
        plan_type: "pro",
        secondary: {
          used_percent: 17,
          limit_window_seconds: 604_800,
          reset_at: hoursFromNow(100),
        },
        rate_limit_reset_credits: { available_count: 3 },
      }));
      const at = (day: number, hour: number) =>
        new Date(2026, 10, day, hour, 0).toISOString();
      bridgeMocks.getServiceResetCredits.mockImplementation(
        async (id: string) => ({
          available_count: 3,
          credits:
            id === today.id
              ? // Two within three days; the first goes tonight.
                [
                  { expires_at: at(5, 9) },
                  { expires_at: at(3, 18) },
                  { expires_at: at(20, 9) },
                ]
              : // Tomorrow evening is more than a day away.
                [{ expires_at: at(4, 18) }, {}, {}],
        }),
      );

      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[today, soon]}
            view={{ kind: "list" }}
          />,
        );
      });
      for (let i = 0; i < 4; i++)
        await act(async () => {
          await Promise.resolve();
        });

      const reset = (name: string) =>
        packageCard(name).querySelector<HTMLButtonElement>(
          '[data-testid="subscription-usage-reset"]',
        );
      expect(reset("Codex personal")?.textContent).toBe("2 次今天到期");
      expect(reset("Codex personal")?.className).toContain("bg-danger-wash");
      expect(reset("Codex personal")?.getAttribute("aria-label")).toBe(
        "重置额度（剩 3 次），2 次今天到期",
      );
      expect(reset("Codex soon")?.textContent).toBe("1 次明天到期");
      expect(reset("Codex soon")?.className).toContain("bg-warning-wash");
    } finally {
      vi.useRealTimers();
    }
  });

  it("logs and shows the usage failure instead of swallowing it", async () => {
    const connected: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "connected" },
    };
    const logged = vi.spyOn(console, "error").mockImplementation(() => {});
    bridgeMocks.getServiceUsage.mockRejectedValue(
      new Error(
        `GET /control/v1/services/${connected.id}/usage returned 502 Bad Gateway: {"error":{"code":"subscription_usage_failed","message":"codex usage unavailable: status 403"}}`,
      ),
    );

    try {
      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[connected]}
            view={{ kind: "list" }}
          />,
        );
        await Promise.resolve();
      });
      await act(async () => {
        await Promise.resolve();
      });

      expect(logged).toHaveBeenCalled();
      expect(String(logged.mock.calls[0]?.[0])).toContain(
        "AstrLink failed to load subscription usage",
      );
      const card = packageCard("Codex personal");
      expect(quotaOf(card).status).toBe("额度读不到");
      expect(card.textContent).toContain(
        "subscription_usage_failed: codex usage unavailable: status 403",
      );
    } finally {
      logged.mockRestore();
    }
  });

  it.each([
    ["deepseek", "DeepSeek API"],
    ["qwen", "通义千问（百炼） API"],
    ["moonshot", "Kimi（Moonshot） API"],
    ["glm", "智谱 GLM API"],
    ["minimax", "MiniMax API"],
    ["doubao", "豆包（火山方舟） API"],
    ["xai", "xAI（Grok） API"],
    ["gemini", "Gemini API"],
  ] as const)(
    "creates %s with API credentials and preserves its kind on reload",
    async (kind, label) => {
      const preset = httpServicePreset(kind);
      // These presets cannot list models, so they need some typed in.
      const listsModels = !["qwen", "glm", "doubao"].includes(kind);
      const service = parseService({
        ...gatewayService,
        name: preset.defaultName,
        kind,
        models: [`${kind}-model`],
        http: {
          base_url: preset.baseURL,
          auth: { scheme: preset.authScheme },
          credential_ref: "local://service/service_gateway",
        },
        capabilities: preset.capabilities,
      });
      bridgeMocks.createService.mockResolvedValue({ service, etag });
      bridgeMocks.probeDraftServiceModels.mockResolvedValue({
        service_id: "",
        protocol: "openai.models",
        model_ids: [`${kind}-model`],
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[]}
            view={{ kind: "create", serviceKind: "codex_subscription" }}
          />,
        ),
      );
      await chooseServiceKind(label);
      expect(
        container.querySelector<HTMLInputElement>("#service-name")?.value,
      ).toBe(preset.defaultName);
      await openEditorTab("protocols");
      const protocolRows = [
        ...container.querySelectorAll('[data-testid="service-capability-row"]'),
      ];
      const entryRow = protocolRows.find((row) =>
        row.textContent?.includes(
          kind === "gemini" ? "OpenAI Chat Completions" : "Anthropic Messages",
        ),
      );
      expect(
        entryRow
          ?.querySelector('[role="switch"]')
          ?.getAttribute("aria-checked"),
      ).toBe("true");
      expect(entryRow?.textContent).toContain(
        kind === "gemini" ? "Gemini Generate Content" : "原样转发",
      );
      await openEditorTab("connection");
      const secret = container.querySelector<HTMLInputElement>(
        'input[type="password"]',
      );
      if (!secret) throw new Error("missing API key input");
      await act(async () => {
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(secret, "test-api-key");
        secret.dispatchEvent(new Event("input", { bubbles: true }));
      });
      const submit = container.querySelector<HTMLButtonElement>(
        '[data-testid="service-submit"]',
      );
      expect(submit?.disabled).toBe(false);
      await act(async () => {
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          );
      });
      await settleSave();
      if (!listsModels) {
        expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
        expect(bridgeMocks.createService).not.toHaveBeenCalled();
        expect(modelFetchNotice()?.textContent).toContain(
          "这个提供商不能从上游获取模型列表",
        );
        await clickButton("手动填写模型");
        await typeInto(
          container.querySelector<HTMLInputElement>(
            'input[aria-label="待添加模型 ID"]',
          )!,
          `${kind}-model`,
        );
        await clickButton("添加");
        await submitServiceForm();
        await settleSave();
      }
      expect(bridgeMocks.createService).toHaveBeenCalledWith(
        expect.objectContaining({
          kind,
          name: preset.defaultName,
          models: [`${kind}-model`],
          capabilities: preset.capabilities,
          http: {
            base_url: preset.baseURL,
            auth: { scheme: preset.authScheme },
            credential: { secret: "test-api-key" },
          },
        }),
      );
      if (kind === "gemini") {
        expect(
          bridgeMocks.createService.mock.calls[0]?.[0].capabilities,
        ).toContainEqual({
          protocol: "openai.chat",
          mode: "native",
          streaming: true,
          convert_to: "google.generate_content",
        });
      }
      expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
      await act(async () =>
        container
          .querySelector<HTMLButtonElement>(
            '[data-testid="service-editor-tab-models"]',
          )!
          .click(),
      );
      const fetchButton = [...container.querySelectorAll("button")].find(
        (button) => button.textContent === "获取模型列表",
      );
      expect(Boolean(fetchButton)).toBe(
        !["qwen", "glm", "doubao"].includes(kind),
      );
    },
  );

  it("lets a preset pick its vendor site instead of typing an address", async () => {
    const preset = httpServicePreset("glm_coding");
    const global = "https://api.z.ai/api/coding/paas/v4";
    const service = parseService({
      ...gatewayService,
      name: preset.defaultName,
      kind: "glm_coding",
      models: preset.models,
      http: {
        base_url: global,
        auth: { scheme: "bearer" },
        credential_ref: "local://service/service_gateway",
      },
      capabilities: preset.capabilities,
    });
    bridgeMocks.createService.mockResolvedValue({ service, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      ),
    );
    await chooseServiceKind("GLM Coding Plan");
    expect(container.querySelector('[aria-label="认证方式"]')).toBeNull();
    const addressInput = () =>
      container.querySelector<HTMLInputElement>('input[type="url"]');
    expect(addressInput()).toBeNull();
    expect(container.textContent).toContain(`请求发往 ${preset.baseURL}`);

    await clickButton("国际站");
    expect(container.textContent).toContain(`请求发往 ${global}`);
    await clickButton("自定义地址");
    expect(addressInput()?.value).toBe(global);
    await clickButton("国际站");
    expect(addressInput()).toBeNull();

    const secret = container.querySelector<HTMLInputElement>(
      'input[type="password"]',
    );
    if (!secret) throw new Error("missing API key input");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(secret, "test-api-key");
      secret.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        );
    });
    expect(bridgeMocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "glm_coding",
        http: {
          base_url: global,
          auth: { scheme: "bearer" },
          credential: { secret: "test-api-key" },
        },
      }),
    );
  });

  it.each([
    ["https://api.minimax.cn/anthropic", "国内站"],
    ["https://proxy.example/minimax", "自定义地址"],
  ])("keeps the saved address %s and marks it as %s", async (baseURL, site) => {
    const saved: Service = {
      ...gatewayService,
      id: "service_minimax_plan",
      name: "MiniMax Coding Plan",
      kind: "minimax_coding",
      http: {
        base_url: baseURL,
        auth: { scheme: "bearer" },
        credential_ref: "local://service/service_minimax_plan",
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service: saved, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[saved]}
          view={{ kind: "edit", serviceId: saved.id }}
        />,
      );
      await Promise.resolve();
    });

    const selected = [
      ...container.querySelectorAll<HTMLButtonElement>(
        '[data-slot="segmented-control-item"]',
      ),
    ].find((item) => item.dataset.state === "on");
    expect(selected?.textContent).toBe(site);
    const input =
      container.querySelector<HTMLInputElement>('input[type="url"]');
    if (site === "自定义地址") {
      expect(input?.value).toBe(baseURL);
    } else {
      expect(input).toBeNull();
      expect(container.textContent).toContain(`请求发往 ${baseURL}`);
    }
  });

  it("shows each provider's host under its name and the full address on hover", async () => {
    const plan = (id: string, baseURL: string): Service => ({
      ...gatewayService,
      id,
      name: id,
      kind: "minimax_coding",
      http: {
        base_url: baseURL,
        auth: { scheme: "bearer" },
        credential_ref: `local://service/${id}`,
      },
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[
            plan("service_minimax_site", "https://api.minimax.io/v1"),
            plan("service_minimax_proxy", "https://proxy.example/minimax"),
            gatewayService,
          ]}
          view={{ kind: "list" }}
        />,
      );
    });

    expect(
      ["service_minimax_site", "service_minimax_proxy", "new-api"].map(
        (name) => {
          const line = packageCard(name).querySelector('[title^="https://"]');
          return [line?.textContent, line?.getAttribute("title")];
        },
      ),
    ).toEqual([
      ["api.minimax.io", "https://api.minimax.io/v1"],
      ["proxy.example", "https://proxy.example/minimax"],
      ["gateway.example", "https://gateway.example/v1"],
    ]);
  });

  it("creates a Codex service through the unified add form and targets its OAuth", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: codexService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: {
        id: "authorization_01",
        provider: "openai_codex",
        status: "pending",
        flow: "browser",
        service_id: codexService.id,
        authorization_url: "https://auth.example/oauth/authorize",
        expires_at: "2026-07-28T12:10:00Z",
        created_at: timestamp,
        updated_at: timestamp,
      },
    });
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      );
    });
    const name = container.querySelector<HTMLInputElement>("#service-name");
    if (!name) throw new Error("missing service name input");
    const submitButton = container.querySelector<HTMLButtonElement>(
      '[data-testid="service-submit"]',
    );
    expect(submitButton?.disabled).toBe(true);
    const browserFlow = container.querySelector<HTMLButtonElement>(
      '[role="radio"][aria-label="浏览器 OAuth"]',
    );
    if (!browserFlow) throw new Error("missing browser login choice");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(name, "Personal Codex");
      name.dispatchEvent(new Event("input", { bubbles: true }));
      browserFlow.click();
    });
    expect(submitButton?.disabled).toBe(false);
    const form = container.querySelector<HTMLFormElement>("form");
    if (!form) throw new Error("missing service form");
    await act(async () => {
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.createService).toHaveBeenCalledWith({
      name: "Personal Codex",
      kind: "codex_subscription",
      enabled: true,
      responses_websocket_enabled: true,
      models: [],
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "browser",
    );
    expect(saved).toHaveBeenCalledWith(codexService);
    expect(changed).toHaveBeenCalledWith({ kind: "list" });
  });

  it("asks for the provider type before opening the add editor", async () => {
    const changed = vi.fn();
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={changed}
          protocols={[]}
          services={[]}
          view={{ kind: "list" }}
        />,
      ),
    );
    const add = container.querySelector<HTMLButtonElement>(
      'button[aria-label="添加 API 提供商"]',
    );
    if (!add) throw new Error("missing add provider button");
    await act(async () => {
      add.click();
      await Promise.resolve();
    });

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("选择 API 提供商类型");
    expect(dialog?.querySelector('[aria-current="true"]')).toBeNull();
    expect(changed).not.toHaveBeenCalled();
    expect(
      [
        ...document.querySelectorAll(
          '[role="dialog"] [data-slot="dialog-picker-label"]',
        ),
      ].map((label) => label.textContent),
    ).toEqual(expect.arrayContaining(["New API", "Magpie"]));
    const card = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
    ].find(
      (candidate) =>
        candidate.querySelector('[data-slot="dialog-picker-label"]')
          ?.textContent === "New API",
    );
    if (!card) throw new Error("missing New API card");
    await act(async () => {
      card.click();
      await Promise.resolve();
    });

    expect(changed).toHaveBeenCalledWith({
      kind: "create",
      serviceKind: "newapi",
    });
  });

  it("opens the add editor on the picked provider type", async () => {
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "newapi" }}
        />,
      ),
    );

    expect(
      container.querySelector('[aria-label="API 提供商类型"]')?.textContent,
    ).toContain("New API");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("New API");
    expect(
      container.querySelector<HTMLInputElement>(
        '[data-testid="service-form"] input[type="url"]',
      ),
    ).not.toBeNull();
  });

  it("keeps the selected service kind when Core protocol capabilities refresh", async () => {
    const view = { kind: "create", serviceKind: "codex_subscription" } as const;
    const onDirtyChange = vi.fn();
    const onRefresh = vi.fn();
    const onServiceRemoved = vi.fn();
    const onServiceSaved = vi.fn();
    const onViewChange = vi.fn();
    const render = (
      protocols: Array<{
        id: string;
        phase: "alpha" | "post_alpha";
        primary: boolean;
        streaming: boolean;
      }>,
    ) =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={onDirtyChange}
          onRefresh={onRefresh}
          onServiceRemoved={onServiceRemoved}
          onServiceSaved={onServiceSaved}
          onViewChange={onViewChange}
          protocols={protocols}
          services={[]}
          view={view}
        />,
      );

    await act(async () => render([]));
    await chooseServiceKind("Anthropic API");
    const kind = container.querySelector<HTMLButtonElement>(
      '[aria-label="API 提供商类型"]',
    );
    expect(kind?.textContent).toContain("Anthropic API");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Anthropic API");

    await act(async () =>
      render([
        {
          id: "anthropic.messages",
          phase: "alpha",
          primary: false,
          streaming: true,
        },
      ]),
    );

    expect(kind?.textContent).toContain("Anthropic API");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Anthropic API");
  });

  it("shows the saved key's last characters when editing a provider", async () => {
    bridgeMocks.getService.mockResolvedValue({
      service: {
        ...gatewayService,
        http: { ...gatewayService.http!, credential_hint: "…wxyz" },
      },
      etag,
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("connection");
    const secret = container.querySelector<HTMLInputElement>(
      'input[aria-label="API Key（留空保留）"]',
    );
    expect(secret?.value).toBe("");
    expect(secret?.placeholder).toBe("已保存密钥 …wxyz，留空则继续使用");
  });

  it("previews upstream models and reuses the saved credential for an edited service", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: gatewayService.id,
      protocol: "openai.models",
      model_ids: ["gpt-4.1", "gpt-5"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledWith({
      service_id: gatewayService.id,
      kind: "newapi",
      http: {
        base_url: gatewayService.http?.base_url,
        auth: { scheme: "bearer" },
      },
      protocol: "openai.models",
    });
    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
    // Only the already allow-listed model opens checked; the discovery is opt-in.
    expect(previewModelCheckbox("gpt-5").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(previewModelCheckbox("gpt-4.1").getAttribute("aria-checked")).toBe(
      "false",
    );
    expect(applySelectedButton().textContent).toBe("应用所选模型（1）");

    // The checklist is the filtered result, so search opens no suggestion list.
    const search = document.querySelector<HTMLInputElement>(
      'input[aria-label="搜索上游模型"]',
    );
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!search || !setter) throw new Error("Missing upstream model search");
    await act(async () => {
      setter.call(search, "4.1");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(previewModelIds()).toEqual(["gpt-4.1"]);
    expect(search.getAttribute("role")).toBeNull();
    expect(document.querySelector('[role="listbox"]')).toBeNull();
    await togglePreviewModel("gpt-4.1");
    const clear = document.querySelector<HTMLButtonElement>(
      'button[aria-label="清除搜索"]',
    );
    if (!clear) throw new Error("Missing clear search");
    await act(async () => clear.click());
    expect(search.value).toBe("");
    expect(previewModelIds()).toEqual(["gpt-4.1", "gpt-5"]);

    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（2）");
    await act(async () => apply.click());
    expect(container.textContent).toContain("2 个模型");
  });

  it("preselects every model when the service has no allowlist yet", async () => {
    const emptyGateway: Service = { ...gatewayService, models: [] };
    bridgeMocks.getService.mockResolvedValue({ service: emptyGateway, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: emptyGateway.id,
      protocol: "openai.models",
      model_ids: ["gpt-4.1", "gpt-5"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[emptyGateway]}
          view={{ kind: "edit", serviceId: emptyGateway.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(previewModelCheckbox("gpt-4.1").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(previewModelCheckbox("gpt-5").getAttribute("aria-checked")).toBe(
      "true",
    );
    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（2）");
    await act(async () => apply.click());
    expect(container.textContent).toContain("2 个模型");
  });

  it("merges New API discovery results and keeps successful results after a partial failure", async () => {
    const multiProtocolGateway: Service = {
      ...gatewayService,
      models: ["current-model"],
      capabilities: [
        ...gatewayService.capabilities,
        { protocol: "google.models", mode: "delegated", streaming: false },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({
      service: multiProtocolGateway,
      etag,
    });
    bridgeMocks.probeDraftServiceModels.mockImplementation(
      ({ protocol }: { protocol: string }) =>
        protocol === "openai.models"
          ? Promise.resolve({
              service_id: gatewayService.id,
              protocol,
              model_ids: ["openai-model"],
            })
          : Promise.reject(new Error("Gemini 上游暂不可用")),
    );

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[multiProtocolGateway]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledTimes(2);
    expect(document.body.textContent).toContain("部分协议获取失败");
    expect(document.body.textContent).toContain("current-model");
    expect(document.body.textContent).toContain("openai-model");
    // A failed protocol may still serve the model, so absence is inconclusive.
    expect(
      document.querySelector('[data-testid="model-preview-missing"]')
        ?.textContent,
    ).toContain("部分协议获取失败，它们可能仍可用");
    expect(previewModelRow("current-model").textContent).toContain(
      "上游未返回",
    );
    expect(
      previewModelCheckbox("current-model").getAttribute("aria-checked"),
    ).toBe("true");
    expect(
      previewModelCheckbox("openai-model").getAttribute("aria-checked"),
    ).toBe("false");
    const apply = applySelectedButton();
    await act(async () => apply.click());
    expect(container.textContent).toContain("1 个模型");
  });

  it("flags allow-listed models the upstream no longer returns", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5", "retired-model"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-5", "gpt-5.1"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    await clickButton("获取模型列表");

    expect(
      document.querySelector('[data-testid="model-preview-missing"]')
        ?.textContent,
    ).toContain("1 个已配置模型不在上游列表中");
    expect(previewModelRow("retired-model").textContent).toContain(
      "上游未返回",
    );
    expect(previewModelRow("gpt-5").textContent).not.toContain("上游未返回");
    expect(previewModelRow("gpt-5.1").textContent).not.toContain("上游未返回");
    // Kept by default so fetching never silently drops a configured model.
    expect(
      previewModelCheckbox("retired-model").getAttribute("aria-checked"),
    ).toBe("true");

    await clickButton("只看这些");
    expect(previewModelIds()).toEqual(["retired-model"]);
    await clickButton("取消勾选这些");
    expect(
      previewModelCheckbox("retired-model").getAttribute("aria-checked"),
    ).toBe("false");
    await clickButton("显示全部");
    expect(previewModelIds()).toEqual(["gpt-5", "gpt-5.1", "retired-model"]);
    expect(previewModelCheckbox("gpt-5").getAttribute("aria-checked")).toBe(
      "true",
    );

    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（1）");
    await act(async () => apply.click());
    expect(container.textContent).toContain("1 个模型");
    expect(
      container.querySelector('[data-testid="service-models-missing"]'),
    ).toBeNull();
  });

  it("keeps marking a retained model the upstream did not return", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5", "retired-model"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-5"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    expect(
      container.querySelector('[data-testid="service-models-missing"]'),
    ).toBeNull();
    await clickButton("获取模型列表");
    await act(async () => applySelectedButton().click());

    expect(container.textContent).toContain("2 个模型");
    expect(
      container.querySelector('[data-testid="service-models-missing"]')
        ?.textContent,
    ).toContain("1 个模型不在最近一次获取的上游列表中");
    const rows = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="service-model-row"]',
      ),
    ];
    const rowText = (model: string) =>
      rows.find((row) => row.querySelector("code")?.textContent === model)
        ?.textContent;
    expect(rowText("retired-model")).toContain("上游未返回");
    expect(rowText("gpt-5")).not.toContain("上游未返回");

    // The marker describes the fetched connection, not a new address.
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    const setBaseURL = async (value: string) => {
      await openEditorTab("connection");
      const baseURL =
        container.querySelector<HTMLInputElement>('input[type="url"]');
      if (!baseURL) throw new Error("missing base URL input");
      await act(async () => {
        valueSetter.call(baseURL, value);
        baseURL.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await openEditorTab("models");
    };
    await setBaseURL("https://other.example/v1");
    expect(
      container.querySelector('[data-testid="service-models-missing"]'),
    ).toBeNull();
    await setBaseURL(gatewayService.http!.base_url);
    expect(
      container.querySelector('[data-testid="service-models-missing"]'),
    ).not.toBeNull();

    await clickButton("删除这些（1）");
    expect(document.body.textContent).toContain("删除上游未返回的模型？");
    await clickButton("确认删除");
    expect(container.textContent).toContain("1 个模型");
    expect(
      container.querySelector('[data-testid="service-models-missing"]'),
    ).toBeNull();
  });

  it("groups allow-listed models and supports search plus clear", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["claude-opus-4-7", "claude-sonnet-4-5", "gpt-5", "gpt-5-mini"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("claude-opus");
    expect(container.textContent).toContain("claude-sonnet");
    expect(container.textContent).toContain("4 个模型");
    expect(container.textContent).toContain("1 类 · 3 组 · 4 个模型");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(4);
    expect(container.textContent).toContain("claude-opus-4-7");

    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索已配置模型"]',
    );
    if (!search) throw new Error("missing model search");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(search, "sonnet");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.textContent).toContain("匹配 1 / 4");
    expect(container.textContent).toContain("claude-sonnet-4-5");
    expect(container.textContent).not.toContain("claude-opus-4-7");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(1);

    // Clearing the list sits in the menu beside the select button.
    await openModelActions();
    await chooseMenuItem("清空");
    await settleClosedLayer();
    expect(document.body.textContent).toContain("清空支持模型？");
    const confirm = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "确认删除",
    );
    await act(async () => confirm?.click());
    expect(container.textContent).toContain(
      "还没有模型 · API 提供商不会参与路由",
    );
    expect(container.textContent).toContain("0 个模型");
  });

  it("deletes a model from the allowlist instead of remembering it as disabled", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5", "gpt-5-mini"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: listed,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("2 个模型");
    const chips = [
      ...container.querySelectorAll('[data-testid="service-model-row"]'),
    ];
    expect(chips).toHaveLength(2);

    const remove = container.querySelector<HTMLButtonElement>(
      'button[aria-label="删除 gpt-5-mini"]',
    );
    await act(async () => remove?.click());
    expect(container.textContent).toContain("1 个模型");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(1);

    const form = container.querySelector("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({
        models: ["gpt-5"],
      }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
  });

  it("adds a model by hand to the allowlist", async () => {
    const listed: Service = {
      ...gatewayService,
      models: [],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: listed,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain(
      "还没有模型 · API 提供商不会参与路由",
    );

    const open = container.querySelector<HTMLButtonElement>(
      'button[aria-label="添加模型"]',
    );
    await act(async () => open?.click());
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="待添加模型 ID"]',
    );
    if (!input) throw new Error("missing model input");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(input, "gpt-4o");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const add = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "添加",
    );
    await act(async () => add?.click());

    expect(container.textContent).toContain("1 个模型");

    const form = container.querySelector("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({ models: ["gpt-4o"] }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
  });

  it("lets a probe re-select a model that is not on the current allowlist", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5"],
      capabilities: [
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-4o", "gpt-5", "gpt-5-codex"],
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    const discover = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      discover?.click();
      await Promise.resolve();
    });

    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
    expect(document.body.textContent).toContain("gpt-4o");
    expect(document.body.textContent).not.toContain("此前已停用");
    expect(previewModelCheckbox("gpt-4o").getAttribute("aria-checked")).toBe(
      "false",
    );

    const selectAll = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "全选",
    );
    await act(async () => {
      selectAll?.click();
      await Promise.resolve();
    });
    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（3）");
    await act(async () => apply.click());

    expect(container.textContent).toContain("3 个模型");
    expect(
      container.querySelector('[data-testid="service-models-filter-disabled"]'),
    ).toBeNull();
  });

  async function typeInto(
    input: HTMLInputElement | HTMLTextAreaElement,
    value: string,
  ): Promise<void> {
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        input instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement.prototype
          : HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function addModelsByHand(models: string[]): Promise<void> {
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('button[aria-label="添加模型"]')!
        .click(),
    );
    for (const model of models) {
      await typeInto(
        container.querySelector<HTMLInputElement>(
          'input[aria-label="待添加模型 ID"]',
        )!,
        model,
      );
      await clickButton("添加");
    }
  }

  /** Lets a save run through its fetch, create and refresh calls. */
  async function settleSave(): Promise<void> {
    await act(async () => {
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    });
  }

  function modelFetchNotice(): HTMLElement | null {
    return container.querySelector<HTMLElement>(
      '[data-testid="service-model-fetch-notice"]',
    );
  }

  function activeEditorTab(): string | null | undefined {
    return container
      .querySelector('[role="tab"][data-state="active"]')
      ?.getAttribute("data-testid");
  }

  async function submitServiceForm(): Promise<void> {
    await act(async () => {
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        );
      await Promise.resolve();
    });
  }

  async function chooseFormat(model: string, option: string): Promise<void> {
    const trigger = container.querySelector<HTMLButtonElement>(
      `button[aria-label="指定 ${model} 发给上游的格式"]`,
    );
    if (!trigger) throw new Error(`Missing format menu: ${model}`);
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
      ...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]'),
    ].find((candidate) => candidate.textContent?.trim() === option);
    if (!item) throw new Error(`Missing format option: ${option}`);
    await act(async () => {
      item.click();
      await Promise.resolve();
    });
    await settleClosedLayer();
  }

  /** The tag a model with a format of its own shows beside its name. */
  function formatShown(model: string): string | undefined {
    const row = [
      ...container.querySelectorAll('[data-testid="service-model-row"]'),
    ].find(
      (candidate) => candidate.querySelector("code")?.textContent === model,
    );
    return row
      ?.querySelector('[data-testid="service-model-format"]')
      ?.textContent?.trim();
  }

  const vertexService: Service = {
    ...gatewayService,
    kind: "vertex",
    name: "Vertex AI",
    models: ["gemini-test"],
    http: {
      base_url: "https://aiplatform.googleapis.com",
      auth: { scheme: "google_service_account" },
      vertex: { project_id: "vertex-test", location: "global" },
      credential_ref: "local://service/service_gateway",
    },
    capabilities: httpServicePreset("vertex").capabilities,
  };
  const vertexJSON = JSON.stringify(
    {
      type: "service_account",
      project_id: "vertex-test",
      client_email: "test@vertex-test.iam.gserviceaccount.com",
      private_key: "synthetic-key-for-ui-only",
    },
    null,
    2,
  );

  it("saves and reopens Vertex entry choices and cleared model formats without catalog repinning", async () => {
    let saved: Service = {
      ...vertexService,
      model_protocols: [
        { model: "gemini-test", protocol: "google.generate_content" },
      ],
    };
    bridgeMocks.getService.mockImplementation(async () => ({
      service: saved,
      etag,
    }));
    bridgeMocks.updateService.mockImplementation(async (_id, _etag, patch) => {
      saved = { ...saved, ...patch, http: { ...saved.http, ...patch.http } };
      return { service: saved, etag };
    });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      protocol: "openai.models",
      model_ids: ["gemini-test", "claude-test"],
      model_protocols: [
        { model: "gemini-test", protocol: "google.generate_content" },
        { model: "claude-test", protocol: "anthropic.messages" },
      ],
    });
    const formats = [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "google.generate_content",
    ];
    const render = async (key: string) => {
      await act(async () =>
        root.render(
          <ServiceManager
            key={key}
            catalogError={null}
            catalogStatus="ready"
            isReady
            conversionEngine={{
              name: "relaykit",
              version: null,
              available: true,
              edges: formats.flatMap((from) =>
                formats
                  .filter((to) => to !== from)
                  .map((to) => ({
                    from,
                    to,
                    quality: "good" as const,
                    streaming: true,
                  })),
              ),
            }}
            protocols={[]}
            services={[saved]}
            view={{ kind: "edit", serviceId: saved.id }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
    };
    await render("initial");
    await openEditorTab("models");
    expect(document.body.textContent).not.toContain("从官方目录拉取");
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="Vertex 模型配置说明"]')!
        .click(),
    );
    expect(document.body.textContent).toContain("从官方目录拉取");
    await act(async () =>
      document.activeElement!.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      ),
    );
    await settleClosedLayer();
    await chooseFormat("gemini-test", "按入口协议设置");
    await addModelsByHand(["meta/llama-test-maas"]);
    expect(formatShown("meta/llama-test-maas")).toBeUndefined();
    await clickButton("获取模型列表");
    await clickButton("全选");
    await act(async () => applySelectedButton().click());
    await settleClosedLayer();
    expect(formatShown("gemini-test")).toBeUndefined();
    expect(formatShown("claude-test")).toBeUndefined();
    await chooseFormat("claude-test", "Messages");
    await openEditorTab("protocols");
    const responses = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="service-capability-row"]',
      ),
    ].find((row) => row.textContent?.includes("/v1/responses"))!;
    await act(async () =>
      responses.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
    );
    expect(responses.textContent).toContain("转换为 Gemini Generate Content");
    for (const [target, label] of [
      ["google.generate_content", "Gemini Generate Content"],
      ["anthropic.messages", "Anthropic Messages"],
      ["openai.chat", "OpenAI Chat Completions"],
    ]) {
      await chooseOption("OpenAI Responses 本地转换", `转换为 ${label}`);
      await settleClosedLayer();
      await submitServiceForm();
      expect(
        saved.capabilities.find((row) => row.protocol === "openai.responses")
          ?.convert_to,
      ).toBe(target);
      await render(`saved-${target}`);
      await openEditorTab("protocols");
      expect(
        container.querySelector('[aria-label="OpenAI Responses 本地转换"]')
          ?.textContent,
      ).toContain(`转换为 ${label}`);
    }
    await chooseOption(
      "OpenAI Chat Completions 本地转换",
      "转换为 Gemini Generate Content",
    );
    await settleClosedLayer();
    await chooseOption("OpenAI Chat Completions 本地转换", "原样转发");
    await settleClosedLayer();
    await submitServiceForm();
    expect(saved.model_protocols).toEqual([
      { model: "claude-test", protocol: "anthropic.messages" },
    ]);
    expect(
      saved.capabilities.find((row) => row.protocol === "openai.chat")
        ?.convert_to,
    ).toBeUndefined();
    await render("reopened");
    await openEditorTab("protocols");
    expect(
      container.querySelector('[aria-label="OpenAI Responses 本地转换"]')
        ?.textContent,
    ).toContain("转换为 OpenAI Chat Completions");
    expect(
      container.querySelector('[aria-label="OpenAI Chat Completions 本地转换"]')
        ?.textContent,
    ).toContain("原样转发");
    await openEditorTab("models");
    expect(formatShown("gemini-test")).toBeUndefined();
    expect(formatShown("claude-test")).toBe("Messages 格式");
    await clickButton("获取模型列表");
    await act(async () => applySelectedButton().click());
    await settleClosedLayer();
    await submitServiceForm();
    expect(saved.model_protocols).toEqual([
      { model: "claude-test", protocol: "anthropic.messages" },
    ]);
  });

  it("restricts Vertex conversion choices to available native targets", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: vertexService, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          conversionEngine={{
            name: "relaykit",
            version: null,
            available: true,
            edges: [
              {
                from: "openai.responses",
                to: "google.generate_content",
                quality: "good",
                streaming: true,
              },
              {
                from: "openai.chat",
                to: "openai.responses",
                quality: "good",
                streaming: true,
              },
            ],
          }}
          protocols={[]}
          services={[vertexService]}
          view={{ kind: "edit", serviceId: vertexService.id }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    await openEditorTab("protocols");
    const responses = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="service-capability-row"]',
      ),
    ].find((row) => row.textContent?.includes("/v1/responses"))!;
    await act(async () =>
      responses.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
    );
    await act(async () =>
      responses
        .querySelector<HTMLButtonElement>('[role="combobox"]')!
        .dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        ),
    );
    const options = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ];
    expect(options).toHaveLength(3);
    expect(
      options.some((option) => option.textContent?.includes("原样转发")),
    ).toBe(false);
    expect(
      options
        .filter((option) => option.getAttribute("aria-disabled") !== "true")
        .map((option) => option.textContent),
    ).toEqual(["转换为 Gemini Generate Content"]);
    await act(async () =>
      options
        .find((option) => option.getAttribute("aria-disabled") !== "true")!
        .click(),
    );
    await settleClosedLayer();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[aria-label="OpenAI Chat Completions 本地转换"]',
        )!
        .dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        ),
    );
    expect(
      [...document.querySelectorAll('[role="option"]')].some((option) =>
        option.textContent?.includes("Responses"),
      ),
    ).toBe(false);
  });

  it.each(["paste", "file"])(
    "creates Vertex with a service account from %s and a generated regional URL",
    async (source) => {
      bridgeMocks.createService.mockResolvedValue({
        service: vertexService,
        etag,
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            protocols={[]}
            services={[]}
            view={{ kind: "create", serviceKind: "vertex" }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
      await openEditorTab("connection");
      expect(container.querySelector('input[type="url"]')).toBeNull();
      const secret = container.querySelector<HTMLTextAreaElement>(
        'textarea[aria-label="服务账号 JSON"]',
      )!;
      await typeInto(secret, "{broken");
      await submitServiceForm();
      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      if (source === "paste") {
        await typeInto(secret, vertexJSON);
      } else {
        const fileInput =
          container.querySelector<HTMLInputElement>('input[type="file"]')!;
        Object.defineProperty(fileInput, "files", {
          value: [
            new File([vertexJSON], "service-account.json", {
              type: "application/json",
            }),
          ],
        });
        await act(async () => {
          fileInput.dispatchEvent(new Event("change", { bubbles: true }));
        });
      }
      expect(
        container.querySelector('textarea[aria-label="服务账号 JSON"]'),
      ).toBeNull();
      expect(container.textContent).toContain(
        "test@vertex-test.iam.gserviceaccount.com",
      );
      expect(container.textContent).not.toContain("synthetic-key-for-ui-only");
      expect(
        container.querySelector<HTMLInputElement>(
          'input[aria-label="Google Cloud 项目 ID"]',
        )!.value,
      ).toBe("vertex-test");
      await typeInto(
        container.querySelector<HTMLInputElement>('input[aria-label="地区"]')!,
        "eu",
      );
      await openEditorTab("models");
      expect(
        container.querySelector('[aria-label="Vertex 模型配置说明"]'),
      ).not.toBeNull();
      await addModelsByHand([
        "gemini-test",
        "claude-test@20250101",
        "meta/llama-test-maas",
      ]);
      await submitServiceForm();
      expect(bridgeMocks.createService).toHaveBeenCalledWith(
        expect.objectContaining({
          kind: "vertex",
          http: {
            base_url: "https://aiplatform.eu.rep.googleapis.com",
            auth: { scheme: "google_service_account" },
            vertex: { project_id: "vertex-test", location: "eu" },
            credential: { secret: vertexJSON },
          },
        }),
      );
    },
  );

  it("keeps the URL and implicit project when editing a saved Vertex proxy", async () => {
    const legacy: Service = {
      ...vertexService,
      http: {
        base_url: "https://proxy.example/vertex",
        auth: { scheme: "google_service_account" },
        credential_ref: "local://service/service_gateway",
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service: legacy, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: legacy, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          protocols={[]}
          services={[legacy]}
          view={{ kind: "edit", serviceId: legacy.id }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      ),
    );
    await openEditorTab("connection");
    expect(
      container.querySelector<HTMLInputElement>('input[type="url"]')!.value,
    ).toBe("https://proxy.example/vertex");
    expect(
      container.querySelector('[aria-label="Google Cloud 项目 ID"]'),
    ).toBeNull();
    await submitServiceForm();
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      legacy.id,
      etag,
      expect.objectContaining({
        http: {
          base_url: "https://proxy.example/vertex",
          auth: { scheme: "google_service_account" },
        },
      }),
    );
  });

  it.each(["create", "edit"] as const)(
    "%s Express providers require a new key and use Gemini conversions",
    async (mode) => {
      bridgeMocks.getService.mockResolvedValue({
        service: vertexService,
        etag,
      });
      bridgeMocks.updateService.mockResolvedValue({
        service: vertexService,
        etag,
      });
      bridgeMocks.createService.mockResolvedValue({
        service: vertexService,
        etag,
      });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            protocols={[]}
            conversionEngine={{
              name: "relaykit",
              version: null,
              available: true,
              edges: [
                {
                  from: "openai.responses",
                  to: "google.generate_content",
                  quality: "good",
                  streaming: true,
                },
                {
                  from: "openai.chat",
                  to: "google.generate_content",
                  quality: "good",
                  streaming: true,
                },
              ],
            }}
            services={mode === "edit" ? [vertexService] : []}
            view={
              mode === "edit"
                ? { kind: "edit", serviceId: vertexService.id }
                : { kind: "create", serviceKind: "vertex" }
            }
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
      await openEditorTab("connection");
      await clickButton("Express API Key");
      expect(
        container.querySelector('[aria-label="Google Cloud 项目 ID"]'),
      ).toBeNull();
      expect(container.querySelector('[aria-label="地区"]')).toBeNull();
      await submitServiceForm();
      expect(bridgeMocks.updateService).not.toHaveBeenCalled();
      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="password"]')!,
        "express-test-key",
      );
      await openEditorTab("models");
      expect(container.textContent).not.toContain("获取模型列表");
      if (mode === "create") await addModelsByHand(["gemini-test"]);
      await submitServiceForm();
      const payload =
        mode === "edit"
          ? bridgeMocks.updateService.mock.calls[0]?.[2]
          : bridgeMocks.createService.mock.calls[0]?.[0];
      expect(payload.http).toEqual({
        base_url: "https://aiplatform.googleapis.com",
        auth: { scheme: "google_api_key" },
        credential: { secret: "express-test-key" },
        ...(mode === "edit" ? { vertex: null } : {}),
      });
      expect(payload.capabilities).toEqual([
        {
          protocol: "google.generate_content",
          mode: "native",
          streaming: true,
        },
        {
          protocol: "openai.responses",
          mode: "native",
          streaming: true,
          convert_to: "google.generate_content",
        },
        {
          protocol: "openai.chat",
          mode: "native",
          streaming: true,
          convert_to: "google.generate_content",
        },
      ]);
    },
  );

  it("discovers Vertex models with saved credentials and edits or removes them without exposing JSON", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: vertexService, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: vertexService,
      etag,
    });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      protocol: "openai.models",
      model_ids: ["gemini-test"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          protocols={[]}
          services={[vertexService]}
          view={{ kind: "edit", serviceId: vertexService.id }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("connection");
    expect(
      container.querySelector('textarea[aria-label="服务账号 JSON"]'),
    ).toBeNull();
    expect(container.textContent).toContain("已保存服务账号密钥");
    await typeInto(
      container.querySelector<HTMLInputElement>('input[aria-label="地区"]')!,
      "us-east5",
    );
    await openEditorTab("models");
    await clickButton("获取模型列表");
    expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledWith({
      service_id: vertexService.id,
      kind: "vertex",
      protocol: "openai.models",
      http: {
        base_url: "https://us-east5-aiplatform.googleapis.com",
        auth: { scheme: "google_service_account" },
        vertex: { project_id: "vertex-test", location: "us-east5" },
      },
    });
    await act(async () => applySelectedButton().click());
    await settleClosedLayer();
    await submitServiceForm();
    expect(bridgeMocks.updateService).toHaveBeenLastCalledWith(
      vertexService.id,
      etag,
      expect.objectContaining({
        http: {
          base_url: "https://us-east5-aiplatform.googleapis.com",
          auth: { scheme: "google_service_account" },
          vertex: { project_id: "vertex-test", location: "us-east5" },
        },
      }),
    );
    await act(async () => {
      root.render(
        <ServiceManager
          key="reopened"
          catalogError={null}
          catalogStatus="ready"
          isReady
          protocols={[]}
          services={[vertexService]}
          view={{ kind: "edit", serviceId: vertexService.id }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("connection");
    const removal = [...container.querySelectorAll("label")]
      .find((label) => label.textContent?.includes("删除已保存的服务账号凭据"))!
      .querySelector<HTMLButtonElement>('[role="checkbox"]')!;
    await act(async () => removal.click());
    await submitServiceForm();
    expect(bridgeMocks.updateService).toHaveBeenLastCalledWith(
      vertexService.id,
      etag,
      expect.objectContaining({
        http: expect.objectContaining({ credential: null }),
      }),
    );
  });

  it.each(["project", "location", "proxy", "replace", "remove"])(
    "discards an in-flight Vertex catalog after changing %s",
    async (changed) => {
      let finish!: (value: { protocol: string; model_ids: string[] }) => void;
      bridgeMocks.getService.mockResolvedValue({
        service: vertexService,
        etag,
      });
      bridgeMocks.probeDraftServiceModels.mockReturnValue(
        new Promise((resolve) => {
          finish = resolve;
        }),
      );
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            protocols={[]}
            services={[vertexService]}
            view={{ kind: "edit", serviceId: vertexService.id }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
          />,
        ),
      );
      await openEditorTab("models");
      await clickButton("获取模型列表");
      await openEditorTab("connection");
      if (changed === "proxy") {
        await chooseOption("代理模式", "强制直连");
      } else if (changed === "remove") {
        const removal = [...container.querySelectorAll("label")]
          .find((label) =>
            label.textContent?.includes("删除已保存的服务账号凭据"),
          )!
          .querySelector<HTMLButtonElement>('[role="checkbox"]')!;
        await act(async () => removal.click());
      } else {
        if (changed === "replace") await clickButton("更换");
        const label =
          changed === "project"
            ? "Google Cloud 项目 ID"
            : changed === "location"
              ? "地区"
              : "服务账号 JSON";
        await typeInto(
          container.querySelector<HTMLInputElement | HTMLTextAreaElement>(
            `[aria-label="${label}"]`,
          )!,
          changed === "project"
            ? "other-project"
            : changed === "location"
              ? "us-east5"
              : vertexJSON,
        );
      }
      await act(async () =>
        finish({ protocol: "openai.models", model_ids: ["gemini-stale"] }),
      );
      expect(document.body.textContent).not.toContain(
        "选择 API 提供商支持的模型",
      );
      expect(document.body.textContent).not.toContain("gemini-stale");
    },
  );

  it("saves a new provider's upstream formats for the models it keeps", async () => {
    const preset = httpServicePreset("opencode_go");
    const created = parseService({
      ...gatewayService,
      name: preset.defaultName,
      kind: "opencode_go",
      models: ["gemini-4-argon-contributor", "glm-5.3"],
      http: {
        base_url: preset.baseURL,
        auth: { scheme: "bearer" },
        credential_ref: "local://service/service_gateway",
      },
      capabilities: preset.capabilities,
    });
    bridgeMocks.createService.mockResolvedValue({ service: created, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "opencode_go" }}
        />,
      ),
    );
    await openEditorTab("models");
    await addModelsByHand(["gemini-4-argon-contributor", "glm-5.3", "kimi-k3"]);
    expect(formatShown("gemini-4-argon-contributor")).toBeUndefined();
    await chooseFormat("glm-5.3", "Messages");
    await chooseFormat("kimi-k3", "Responses");
    // A deleted model takes its format with it.
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('button[aria-label="删除 kimi-k3"]')!
        .click(),
    );
    await openEditorTab("connection");
    await typeInto(
      container.querySelector<HTMLInputElement>('input[type="password"]')!,
      "test-api-key",
    );
    await submitServiceForm();

    expect(bridgeMocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "opencode_go",
        models: ["gemini-4-argon-contributor", "glm-5.3"],
        model_protocols: [{ model: "glm-5.3", protocol: "anthropic.messages" }],
        http: {
          base_url: "https://opencode.ai/inference/go/v1",
          auth: { scheme: "bearer" },
          credential: { secret: "test-api-key" },
        },
      }),
    );
  });

  it.each([
    ["leaves unchanged formats out of the patch", null, undefined],
    [
      "sends the whole table after a change",
      ["gpt-5.5", "Responses"],
      [
        { model: "claude-opus-5-5", protocol: "anthropic.messages" },
        { model: "gpt-5.5", protocol: "openai.responses" },
      ],
    ],
    [
      "clears the table with an empty list",
      ["claude-opus-5-5", "按入口协议设置"],
      [],
    ],
  ] as const)("%s when editing", async (_name, change, expected) => {
    const saved: Service = {
      ...gatewayService,
      models: ["claude-opus-5-5", "gpt-5.5"],
      model_protocols: [
        { model: "claude-opus-5-5", protocol: "anthropic.messages" },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: saved, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: saved, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[saved]}
          view={{ kind: "edit", serviceId: saved.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    expect(formatShown("claude-opus-5-5")).toBe("Messages 格式");
    expect(formatShown("gpt-5.5")).toBeUndefined();
    if (change) await chooseFormat(change[0], change[1]);
    await submitServiceForm();

    const patch = bridgeMocks.updateService.mock.calls[0]?.[2];
    if (expected === undefined) {
      expect(patch).not.toHaveProperty("model_protocols");
    } else {
      expect(patch?.model_protocols).toEqual(expected);
    }
  });

  it("fills formats from a fetched list for new models and keeps chosen ones", async () => {
    const saved: Service = {
      ...gatewayService,
      models: ["gpt-5.5"],
      model_protocols: [{ model: "gpt-5.5", protocol: "openai.chat" }],
    };
    bridgeMocks.getService.mockResolvedValue({ service: saved, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: saved, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: saved.id,
      protocol: "openai.models",
      model_ids: ["claude-opus-5-5", "gemini-3.1-pro", "gpt-5.5"],
      model_protocols: [
        { model: "claude-opus-5-5", protocol: "anthropic.messages" },
        { model: "gpt-5.5", protocol: "openai.responses" },
      ],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[saved]}
          view={{ kind: "edit", serviceId: saved.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    await clickButton("获取模型列表");
    await clickButton("全选");
    await act(async () => applySelectedButton().click());

    expect(formatShown("claude-opus-5-5")).toBe("Messages 格式");
    expect(formatShown("gpt-5.5")).toBe("Chat 格式");
    expect(formatShown("gemini-3.1-pro")).toBeUndefined();
    await submitServiceForm();
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).toMatchObject({
      models: ["claude-opus-5-5", "gemini-3.1-pro", "gpt-5.5"],
      model_protocols: [
        { model: "claude-opus-5-5", protocol: "anthropic.messages" },
        { model: "gpt-5.5", protocol: "openai.chat" },
      ],
    });
  });

  describe("saving a new provider without models", () => {
    const compatibleURL = "https://api.example.com/v1";
    const compatible: Service = {
      ...gatewayService,
      id: "service_compatible",
      name: "OpenAI 兼容",
      kind: "openai_compatible",
      models: ["claude-opus-5-5", "gpt-5.5"],
      capabilities: httpServicePreset("openai_compatible").capabilities,
      http: {
        base_url: compatibleURL,
        auth: { scheme: "bearer" },
        credential_ref: "local://service/service_compatible",
      },
    };
    const draftProbe = {
      kind: "openai_compatible",
      http: {
        base_url: compatibleURL,
        auth: { scheme: "bearer" },
        credential: { secret: "test-api-key" },
      },
      protocol: "openai.models",
    };
    const fetched = {
      service_id: "",
      protocol: "openai.models",
      model_ids: ["gpt-5.5", "claude-opus-5-5", "gpt-5.5"],
      model_protocols: [
        { model: "claude-opus-5-5", protocol: "anthropic.messages" },
      ],
    };
    const unauthorized = new Error(
      'POST /control/v1/service-model-probes returned 502 Bad Gateway: {"error":{"code":"upstream_model_discovery_failed","message":"upstream model discovery failed (HTTP 401)","retryable":false,"details":[{"reason":"upstream_unauthorized"}]},"request_id":"req_probe"}',
    );

    async function renderCreate(serviceKind: ServiceKind): Promise<void> {
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[]}
            view={{ kind: "create", serviceKind }}
          />,
        ),
      );
    }

    async function fillCompatibleConnection(): Promise<void> {
      await renderCreate("openai_compatible");
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="url"]')!,
        compatibleURL,
      );
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="password"]')!,
        "test-api-key",
      );
    }

    function footerHint(): string | null | undefined {
      return container.querySelector('[data-testid="service-submit"]')
        ?.parentElement?.previousElementSibling?.textContent;
    }

    it("fetches the upstream models and creates the provider with them", async () => {
      bridgeMocks.probeDraftServiceModels.mockResolvedValue(fetched);
      bridgeMocks.createService.mockResolvedValue({
        service: compatible,
        etag,
      });
      await fillCompatibleConnection();
      expect(footerHint()).toBe("保存时会从上游获取模型");

      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledOnce();
      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledWith(
        draftProbe,
      );
      expect(bridgeMocks.createService).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService.mock.calls[0]?.[0]).toMatchObject({
        kind: "openai_compatible",
        models: ["claude-opus-5-5", "gpt-5.5"],
        model_protocols: [
          { model: "claude-opus-5-5", protocol: "anthropic.messages" },
        ],
        http: draftProbe.http,
      });
      expect(
        bridgeMocks.probeDraftServiceModels.mock.invocationCallOrder[0],
      ).toBeLessThan(bridgeMocks.createService.mock.invocationCallOrder[0]!);
      expect(notifyMocks.success).toHaveBeenCalledWith(
        "API 提供商已添加，已从上游获取 2 个模型。",
      );
    });

    it("keeps the provider unsaved when the key is rejected, then retries or takes typed models", async () => {
      bridgeMocks.probeDraftServiceModels.mockRejectedValue(unauthorized);
      bridgeMocks.createService.mockResolvedValue({
        service: compatible,
        etag,
      });
      await fillCompatibleConnection();
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      expect(activeEditorTab()).toBe("service-editor-tab-models");
      const notice = modelFetchNotice();
      expect(notice?.getAttribute("role")).toBe("alert");
      expect(notice?.textContent).toContain(
        "API 提供商未保存：没有从上游获取到模型。上游拒绝了 API Key（HTTP 401）。",
      );
      expect(notice?.textContent).not.toContain("returned 502");

      // Fetching again succeeds and creates the provider with the list.
      bridgeMocks.probeDraftServiceModels.mockResolvedValueOnce(fetched);
      await clickButton("重新获取");
      await settleSave();
      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledTimes(2);
      expect(bridgeMocks.createService).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService.mock.calls[0]?.[0].models).toEqual([
        "claude-opus-5-5",
        "gpt-5.5",
      ]);
    });

    it("opens the add row from the notice and creates typed models without fetching", async () => {
      bridgeMocks.probeDraftServiceModels.mockRejectedValue(unauthorized);
      bridgeMocks.createService.mockResolvedValue({
        service: compatible,
        etag,
      });
      await fillCompatibleConnection();
      await submitServiceForm();
      await settleSave();
      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledOnce();

      await clickButton("手动填写模型");
      const input = container.querySelector<HTMLInputElement>(
        'input[aria-label="待添加模型 ID"]',
      );
      expect(input).not.toBeNull();
      expect(document.activeElement).toBe(input);
      await typeInto(input!, "my-model");
      await clickButton("添加");
      // A listed model answers the notice.
      expect(modelFetchNotice()).toBeNull();

      await submitServiceForm();
      await settleSave();
      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService.mock.calls[0]?.[0].models).toEqual([
        "my-model",
      ]);
    });

    it.each([
      [
        "a timeout",
        () =>
          Promise.reject(
            new Error(
              'POST /control/v1/service-model-probes returned 504 Gateway Timeout: {"error":{"code":"upstream_model_discovery_timeout","message":"upstream model discovery timed out","retryable":true},"request_id":"req_probe"}',
            ),
          ),
        "上游没有及时响应。请稍后重试，或检查网络和代理。",
      ],
      [
        "an empty list",
        () => Promise.resolve({ ...fetched, model_ids: [] }),
        "上游没有返回任何模型",
      ],
    ])("keeps the provider unsaved after %s", async (_name, probe, reason) => {
      bridgeMocks.probeDraftServiceModels.mockImplementation(probe);
      await fillCompatibleConnection();
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      expect(activeEditorTab()).toBe("service-editor-tab-models");
      expect(modelFetchNotice()?.textContent).toContain(reason);
      expect(
        [...modelFetchNotice()!.querySelectorAll("button")].map(
          (button) => button.textContent,
        ),
      ).toEqual(["重新获取", "手动填写模型"]);
    });

    it("clears the notice when the connection changes", async () => {
      bridgeMocks.probeDraftServiceModels.mockRejectedValue(unauthorized);
      await fillCompatibleConnection();
      await submitServiceForm();
      await settleSave();
      expect(modelFetchNotice()).not.toBeNull();

      await openEditorTab("connection");
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="password"]')!,
        "another-key",
      );
      await openEditorTab("models");
      expect(modelFetchNotice()).toBeNull();
    });

    it("leaves the next editor usable when the operator leaves during the fetch", async () => {
      let answer: (value: typeof fetched) => void = () => {};
      bridgeMocks.probeDraftServiceModels.mockReturnValueOnce(
        new Promise<typeof fetched>((resolve) => {
          answer = resolve;
        }),
      );
      await fillCompatibleConnection();
      await submitServiceForm();
      const submit = () =>
        container.querySelector<HTMLButtonElement>(
          '[data-testid="service-submit"]',
        );
      expect(submit()?.textContent).toBe("正在获取模型…");

      const renderView = (view: ServiceManagerProps["view"]) =>
        act(async () =>
          root.render(
            <ServiceManager
              catalogError={null}
              catalogStatus="ready"
              isReady
              onDirtyChange={() => {}}
              onRefresh={() => {}}
              onServiceRemoved={() => {}}
              onServiceSaved={() => {}}
              onViewChange={() => {}}
              protocols={[]}
              services={[]}
              view={view}
            />,
          ),
        );
      await renderView({ kind: "list" });
      await renderView({ kind: "create", serviceKind: "openai_compatible" });
      expect(submit()?.textContent).toBe("保存 API 提供商");
      expect(submit()?.disabled).toBe(false);

      // The abandoned fetch answers late; it neither creates nor relocks.
      await act(async () => answer(fetched));
      await settleSave();
      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      expect(submit()?.disabled).toBe(false);
      expect(
        container.querySelector<HTMLFieldSetElement>(
          '[data-testid="service-form"] fieldset',
        )?.disabled,
      ).toBe(false);
    });

    it("saves a manually fetched selection with no second fetch", async () => {
      bridgeMocks.probeDraftServiceModels.mockResolvedValue(fetched);
      bridgeMocks.createService.mockResolvedValue({
        service: compatible,
        etag,
      });
      await fillCompatibleConnection();
      await openEditorTab("models");
      await clickButton("获取模型列表");
      await togglePreviewModel("claude-opus-5-5");
      await act(async () => applySelectedButton().click());
      await settleClosedLayer();
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService.mock.calls[0]?.[0]).toMatchObject({
        models: ["gpt-5.5"],
      });
      expect(bridgeMocks.createService.mock.calls[0]?.[0]).not.toHaveProperty(
        "model_protocols",
      );
      expect(notifyMocks.success).toHaveBeenCalledWith(
        "API 提供商已添加，新请求立即可用。API Key 只保存在本机凭据库中。",
      );
    });

    it("saves typed models or a preset's own models without fetching", async () => {
      bridgeMocks.createService.mockResolvedValue({
        service: compatible,
        etag,
      });
      await fillCompatibleConnection();
      await openEditorTab("models");
      await addModelsByHand(["typed-model"]);
      expect(footerHint()).not.toBe("保存时会从上游获取模型");
      await submitServiceForm();
      await settleSave();
      expect(bridgeMocks.createService.mock.calls[0]?.[0].models).toEqual([
        "typed-model",
      ]);

      await renderCreate("kimi_coding");
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="password"]')!,
        "test-api-key",
      );
      await submitServiceForm();
      await settleSave();
      expect(bridgeMocks.createService.mock.calls[1]?.[0]).toMatchObject({
        kind: "kimi_coding",
        models: httpServicePreset("kimi_coding").models,
      });
      expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
    });

    it("blocks a provider that cannot list models with only the manual action", async () => {
      await renderCreate("qwen");
      await typeInto(
        container.querySelector<HTMLInputElement>('input[type="password"]')!,
        "test-api-key",
      );
      expect(footerHint()).not.toBe("保存时会从上游获取模型");
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
      expect(bridgeMocks.createService).not.toHaveBeenCalled();
      expect(activeEditorTab()).toBe("service-editor-tab-models");
      expect(modelFetchNotice()?.textContent).toContain(
        "这个提供商不能从上游获取模型列表",
      );
      expect(
        [...modelFetchNotice()!.querySelectorAll("button")].map(
          (button) => button.textContent,
        ),
      ).toEqual(["手动填写模型"]);
    });

    it("updates an existing provider without models exactly as before", async () => {
      const empty: Service = { ...compatible, models: [] };
      bridgeMocks.getService.mockResolvedValue({ service: empty, etag });
      bridgeMocks.updateService.mockResolvedValue({ service: empty, etag });
      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[empty]}
            view={{ kind: "edit", serviceId: empty.id }}
          />,
        );
        await Promise.resolve();
      });
      expect(footerHint()).not.toBe("保存时会从上游获取模型");
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
      expect(bridgeMocks.updateService).toHaveBeenCalledOnce();
      expect(bridgeMocks.updateService.mock.calls[0]?.[2]).toMatchObject({
        models: [],
      });
    });

    it("creates a subscription before any model fetch", async () => {
      bridgeMocks.createService.mockResolvedValue({
        service: codexService,
        etag,
      });
      bridgeMocks.beginServiceAuthorization.mockResolvedValue({
        kind: "session",
        session: {
          id: "authorization_01",
          provider: "openai_codex",
          status: "pending",
          flow: "browser",
          service_id: codexService.id,
          authorization_url: "https://auth.example/oauth/authorize",
          expires_at: "2026-07-28T12:10:00Z",
          created_at: timestamp,
          updated_at: timestamp,
        },
      });
      await renderCreate("codex_subscription");
      expect(footerHint()).not.toBe("保存时会从上游获取模型");
      await act(async () =>
        container
          .querySelector<HTMLButtonElement>(
            '[role="radio"][aria-label="浏览器 OAuth"]',
          )!
          .click(),
      );
      await submitServiceForm();
      await settleSave();

      expect(bridgeMocks.createService).toHaveBeenCalledOnce();
      expect(bridgeMocks.createService.mock.calls[0]?.[0].models).toEqual([]);
      expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
      expect(bridgeMocks.probeServiceModels).not.toHaveBeenCalled();
    });
  });

  it("lets OpenCode's chat entry rows follow each model's format", async () => {
    const saved: Service = {
      ...gatewayService,
      id: "service_opencode_go",
      name: "OpenCode Go",
      kind: "opencode_go",
      models: ["gemini-4-argon-contributor", "glm-5.3"],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        { protocol: "openai.chat", mode: "native", streaming: true },
        { protocol: "anthropic.messages", mode: "native", streaming: true },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
      http: {
        base_url: "https://opencode.ai/zen/go/v1",
        auth: { scheme: "bearer" },
        credential_ref: "local://service/service_opencode_go",
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service: saved, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: saved, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          conversionEngine={{
            name: "relaykit",
            version: null,
            available: true,
            edges: [
              {
                from: "google.generate_content",
                to: "openai.responses",
                quality: "fair",
                streaming: true,
              },
              {
                from: "anthropic.messages",
                to: "openai.chat",
                quality: "fair",
                streaming: true,
              },
            ],
          }}
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[saved]}
          view={{ kind: "edit", serviceId: saved.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("protocols");
    expect(container.textContent).toContain("请求会按各模型的上游格式转发。");
    const row = (label: string) =>
      [
        ...container.querySelectorAll<HTMLElement>(
          '[data-testid="service-capability-row"]',
        ),
      ].find(
        (item) => item.querySelector("span.font-medium")?.textContent === label,
      )!;
    const messages = row("Anthropic Messages");
    expect(messages.textContent).toContain("按模型格式转发");
    expect(messages.querySelector('button[role="combobox"]')).toBeNull();

    const gemini = row("Gemini Generate Content");
    const toggle = gemini.querySelector<HTMLButtonElement>('[role="switch"]')!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await act(async () => toggle.click());
    expect(gemini.textContent).toContain("按模型格式转发");
    expect(gemini.querySelector('button[role="combobox"]')).toBeNull();
    await submitServiceForm();

    expect(bridgeMocks.updateService.mock.calls[0]?.[2].capabilities).toEqual([
      ...saved.capabilities,
      { protocol: "google.generate_content", mode: "native", streaming: true },
    ]);
  });

  it("collapses large allow-lists until a group or search expands them", async () => {
    const models = Array.from({ length: 12 }, (_, index) => {
      const family = index < 6 ? "claude-opus" : "claude-sonnet";
      return `${family}-4-${index}`;
    });
    const listed: Service = { ...gatewayService, models };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("claude");
    expect(container.textContent).toContain("12 个模型");
    expect(container.textContent).toContain("1 类 · 2 组 · 12 个模型");
    expect(container.textContent).toContain("展开全部");
    expect(container.textContent).not.toContain("claude-opus");
    expect(container.textContent).not.toContain("claude-opus-4-0");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(0);

    const expandClaude = [...container.querySelectorAll("button")].find(
      (button) =>
        button.dataset.testid === "service-model-category-toggle" &&
        button.textContent?.includes("claude"),
    );
    await act(async () => expandClaude?.click());
    expect(container.textContent).toContain("claude-opus");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(0);

    const expandOpus = [...container.querySelectorAll("button")].find(
      (button) =>
        button.dataset.testid === "service-model-group-toggle" &&
        button.textContent?.includes("claude-opus"),
    );
    await act(async () => expandOpus?.click());
    expect(container.textContent).toContain("claude-opus-4-0");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(6);
    expect(container.textContent).not.toContain("claude-sonnet-4-6");
    expect(container.textContent).toContain("折叠全部");

    const foldAll = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "折叠全部",
    );
    await act(async () => foldAll?.click());
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(0);
    expect(container.textContent).toContain("展开全部");

    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索已配置模型"]',
    );
    if (!search) throw new Error("missing model search");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(search, "sonnet-4-6");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.textContent).toContain("claude-sonnet-4-6");
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(1);
  });

  it("keeps a saved API key when connection settings change without explicit removal", async () => {
    // Only a custom provider exposes its auth scheme.
    const customService: Service = { ...gatewayService, kind: "custom" };
    const updatedService: Service = {
      ...customService,
      http: {
        ...customService.http!,
        auth: { scheme: "none" },
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service: customService, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: updatedService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[customService]}
          view={{ kind: "edit", serviceId: customService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("connection");
    await chooseOption("认证方式", "无需认证");
    const form = container.querySelector<HTMLFormElement>("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      customService.id,
      etag,
      {
        name: customService.name,
        enabled: true,
        responses_websocket_enabled: false,
        privacy_trusted: false,
        failure_policy: null,
        models: ["gpt-5"],
        http: {
          base_url: customService.http?.base_url,
          auth: { scheme: "none" },
          // A custom provider always patches its model list path; an empty
          // field clears a previously saved one.
          model_list_path: null,
        },
        capabilities: customService.capabilities.map((capability) => ({
          ...capability,
          mode: "native" as const,
        })),
      },
    );
  });

  it("keeps a newly created Codex service when login cannot start", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: codexService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockRejectedValue(
      new Error("Device Code login is unavailable"),
    );
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[]}
          view={{ kind: "create", serviceKind: "codex_subscription" }}
        />,
      );
    });
    const deviceFlow = container.querySelector<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="新 API 提供商登录方式"] [role="radio"][aria-label="Device Code"]',
    );
    await act(async () => deviceFlow?.click());
    const form = container.querySelector<HTMLFormElement>("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.createService).toHaveBeenCalled();
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "device_code",
    );
    expect(saved).toHaveBeenCalledWith(codexService);
    expect(changed).toHaveBeenCalledWith({ kind: "list" });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[codexService]}
          view={{ kind: "list" }}
        />,
      );
    });
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "订阅 API 提供商已添加，可稍后从 API 提供商列表重新登录。",
    );
  });

  it("asks for a login method and shows Device Code after browser fallback", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const deviceSession = {
      id: "authorization_device",
      provider: "openai_codex",
      status: "pending",
      flow: "device_code",
      service_id: codexService.id,
      device_code: {
        verification_url: "https://auth.openai.com/codex/device",
        user_code: "ABCD-EFGH",
      },
      expires_at: "2026-07-28T12:15:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: deviceSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(deviceSession);

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[codexService]}
          view={{ kind: "list" }}
        />,
      );
    });
    await openServiceOverflow(codexService.name);
    await chooseMenuItem("登录");
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    const methodInputs = document.querySelectorAll<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="登录方式"] [role="radio"]',
    );
    expect(methodInputs).toHaveLength(2);
    expect(
      [...methodInputs].every(
        (input) => input.getAttribute("aria-checked") === "false",
      ),
    ).toBe(true);

    await act(async () => methodInputs[0]?.click());
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => {
      start?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "browser",
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "回调端口 1455 和 1457 均不可用，已切换为 Device Code 登录。",
    );
    expect(document.body.textContent).toContain("ABCD-EFGH");

    const copy = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "复制验证码",
    );
    await act(async () => {
      copy?.click();
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith("ABCD-EFGH");

    const reopen = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "重新打开登录页面",
    );
    await act(async () => {
      reopen?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.openAuthorizationURL).toHaveBeenCalledWith(
      "https://auth.openai.com/codex/device",
    );

    const cancel = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "取消登录",
    );
    await act(async () => {
      cancel?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.cancelServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
    );
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await openServiceOverflow(codexService.name);
    await chooseMenuItem("登录");
    const explicitDevice = document.querySelector<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="登录方式"] [role="radio"][aria-label="Device Code"]',
    );
    await act(async () => explicitDevice?.click());
    const restart = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => {
      restart?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenLastCalledWith(
      codexService.id,
      "device_code",
    );
    expect(document.body.textContent).not.toContain("1455 和 1457 均不可用");
  });

  it("keeps a cancelled login choice on its own service while closing", async () => {
    const claude: Service = {
      ...codexService,
      id: "service_claude",
      name: "Claude Code",
      kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "disconnected" },
    };
    const removeDialogAnimations = installDialogAnimations();
    try {
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[claude]}
            view={{ kind: "list" }}
          />,
        ),
      );
      await openServiceOverflow("Claude Code");
      await chooseMenuItem("登录");
      const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
      const cancel = [...(dialog?.querySelectorAll("button") ?? [])].find(
        (button) => button.textContent === "取消",
      );
      await act(async () => cancel?.click());

      expect(dialog?.isConnected).toBe(true);
      expect(dialog?.getAttribute("data-state")).toBe("closed");
      expect(dialog?.textContent).toContain("登录“Claude Code”");
      expect(dialog?.textContent).not.toContain("OpenAI");
      const methods = dialog?.querySelectorAll('[role="radio"]') ?? [];
      expect(methods).toHaveLength(1);
      expect(methods[0]?.getAttribute("aria-checked")).toBe("true");

      await finishExitAnimations();
      expect(document.querySelector('[role="dialog"]')).toBeNull();
      expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    } finally {
      removeDialogAnimations();
    }
  });

  it("lands the editor on the models tab when the view asks for it", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    const changed = vi.fn();
    const props = {
      catalogError: null,
      catalogStatus: "ready" as const,
      isReady: true,
      onDirtyChange: () => {},
      onRefresh: () => {},
      onServiceRemoved: () => {},
      onServiceSaved: () => {},
      onViewChange: changed,
      protocols: [],
      services: [gatewayService],
    };
    await act(async () => {
      root.render(
        <ServiceManager
          {...props}
          view={{ kind: "edit", serviceId: gatewayService.id, tab: "models" }}
        />,
      );
      await Promise.resolve();
    });
    expect(
      container
        .querySelector('[data-testid="service-editor-tab-models"]')
        ?.getAttribute("data-state"),
    ).toBe("active");
    expect(
      container
        .querySelector('[data-testid="service-editor-tab-connection"]')
        ?.getAttribute("data-state"),
    ).toBe("inactive");
  });

  it("keeps a card's footer to its cost and switch", async () => {
    const changed = vi.fn();
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={changed}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const card = packageCard(gatewayService.name);
    // The page header names the period; the editor lists the models.
    expect(card.textContent).not.toContain("个模型");
    expect(card.textContent).not.toContain("近 7 天");
    expect(
      card.querySelector('[data-testid="package-cost"]')?.textContent,
    ).toBe("$0.00");
    expect(
      card.querySelector(
        `[role="switch"][aria-label="启用 ${gatewayService.name}"]`,
      ),
    ).not.toBeNull();
    // A relay has no quota resets.
    expect(
      card.querySelector('[data-testid="subscription-usage-reset"]'),
    ).toBeNull();
    expect(changed).not.toHaveBeenCalled();
  });

  it("toggles a service from the list without opening the editor", async () => {
    const disabledService: Service = { ...gatewayService, enabled: false };
    bridgeMocks.getService.mockResolvedValue({
      service: gatewayService,
      etag,
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: disabledService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const toggle = container.querySelector<HTMLButtonElement>(
      `[role="switch"][aria-label="启用 ${gatewayService.name}"]`,
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("true");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.getService).toHaveBeenCalledWith(gatewayService.id);
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      { enabled: false },
    );
    expect(saved).toHaveBeenCalledWith(disabledService);
    expect(changed).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "API 提供商已停用，新请求立即生效。",
    );
  });

  it("reenables a stopped service from the list", async () => {
    const disabledService: Service = { ...gatewayService, enabled: false };
    bridgeMocks.getService.mockResolvedValue({
      service: disabledService,
      etag,
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: gatewayService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[disabledService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const toggle = container.querySelector<HTMLButtonElement>(
      `[role="switch"][aria-label="启用 ${gatewayService.name}"]`,
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("已停用");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      { enabled: true },
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "API 提供商已启用，新请求立即生效。",
    );
  });

  it("uses an in-app confirmation before deleting a service", async () => {
    bridgeMocks.getService.mockResolvedValue({
      service: gatewayService,
      etag,
    });
    bridgeMocks.deleteService.mockResolvedValue(undefined);
    const removed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={removed}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    await openServiceOverflow(gatewayService.name);
    await chooseMenuItem("删除");
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(bridgeMocks.deleteService).not.toHaveBeenCalled();

    const confirm = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "确认",
    );
    await act(async () => {
      confirm?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
    );
    expect(removed).toHaveBeenCalledWith(gatewayService.id);
  });

  it("warns before deleting the last provider a limited token may use", async () => {
    const token = {
      id: "token_zhang",
      name: "张三",
      hint: "astr_…K8Q2",
      source: "user" as const,
      created_at: "2026-11-10T00:00:00Z",
    };
    await act(async () => {
      root.render(
        <ServiceManager
          accessTokens={[
            {
              ...token,
              service_scope: "selected",
              service_ids: [gatewayService.id],
            },
            {
              ...token,
              id: "token_team",
              name: "研发组",
              service_scope: "selected",
              service_ids: [gatewayService.id, "svc_other"],
            },
            {
              ...token,
              id: "token_all",
              name: "默认令牌",
              service_scope: "all",
              service_ids: [],
            },
          ]}
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[
            gatewayService,
            { ...gatewayService, id: "svc_other", name: "Other" },
          ]}
          view={{ kind: "list" }}
        />,
      );
    });
    await openServiceOverflow(gatewayService.name);
    await chooseMenuItem("删除");
    const text = document.querySelector('[role="alertdialog"]')?.textContent;
    expect(text).toContain("令牌“张三”没有可用的提供商");
    expect(text).not.toContain("研发组");
    expect(text).not.toContain("默认令牌");
  });

  it("configures local conversion instead of native versus delegated", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    expect(
      [...container.querySelectorAll("summary")].some((item) =>
        item.textContent?.includes("API 能力"),
      ),
    ).toBe(false);
    await openEditorTab("protocols");
    expect(container.textContent).toContain("接受的入口协议与格式转换");
    expect(
      container.querySelectorAll('[data-testid="service-capability-row"]')
        .length,
    ).toBeGreaterThan(0);
    expect(container.textContent).toContain("原样转发");
    expect(container.textContent).toContain("本地格式转换尚未启用");
    expect(container.textContent).not.toContain("由网关路由");
    expect(container.textContent).not.toContain("原生协议");
    expect(container.textContent).not.toContain("上游实际协议");
  });

  it.each([false, true])(
    "defaults HTTP Responses to Chat and permits passthrough (passthrough=%s)",
    async (passthrough) => {
      const listed: Service = {
        ...gatewayService,
        kind: "openai_compatible",
        capabilities: [
          { protocol: "openai.chat", mode: "native", streaming: true },
        ],
      };
      bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
      bridgeMocks.updateService.mockResolvedValue({ service: listed, etag });
      await act(async () =>
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            conversionEngine={{
              name: "relaykit",
              version: null,
              available: true,
              edges: [
                {
                  from: "openai.responses",
                  to: "openai.chat",
                  quality: "good",
                  streaming: true,
                },
              ],
            }}
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[listed]}
            view={{ kind: "edit", serviceId: listed.id }}
          />,
        ),
      );
      await openEditorTab("protocols");
      for (const label of [
        "Codex、OpenAI SDK 使用",
        "Claude Code 使用",
        "Gemini CLI 使用",
        "OpenCode、OpenClaw 及多数兼容工具使用",
      ])
        expect(container.textContent).toContain(label);
      const row = [
        ...container.querySelectorAll<HTMLElement>(
          '[data-testid="service-capability-row"]',
        ),
      ].find(
        (item) =>
          item.querySelector("span.font-medium")?.textContent ===
          "OpenAI Responses",
      )!;
      await act(async () =>
        row.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
      );
      expect(row.textContent).toContain("转换为 OpenAI Chat Completions");
      if (passthrough)
        await chooseOption("OpenAI Responses 本地转换", "原样转发");
      await act(async () =>
        container
          .querySelector("form")!
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          ),
      );
      expect(bridgeMocks.updateService).toHaveBeenCalledWith(
        listed.id,
        etag,
        expect.objectContaining({
          capabilities: [
            ...listed.capabilities,
            {
              protocol: "openai.responses",
              mode: "native",
              streaming: true,
              ...(passthrough ? {} : { convert_to: "openai.chat" }),
            },
          ],
        }),
      );
    },
  );

  it("passes through a protocol the preset upstream serves natively", async () => {
    const listed: Service = {
      ...gatewayService,
      kind: "minimax_coding",
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: listed, etag });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          conversionEngine={{
            name: "relaykit",
            version: null,
            available: true,
            edges: [
              {
                from: "openai.responses",
                to: "anthropic.messages",
                quality: "fair",
                streaming: true,
              },
            ],
          }}
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      ),
    );
    await openEditorTab("protocols");
    const row = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="service-capability-row"]',
      ),
    ].find(
      (item) =>
        item.querySelector("span.font-medium")?.textContent ===
        "OpenAI Responses",
    )!;
    await act(async () =>
      row.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
    );
    expect(row.querySelector('button[role="combobox"]')?.textContent).toContain(
      "原样转发",
    );
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({
        capabilities: [
          ...listed.capabilities,
          { protocol: "openai.responses", mode: "native", streaming: true },
        ],
      }),
    );
  });

  it("shows conversion quality on a flattened protocol row when the engine is available", async () => {
    const converting: Service = {
      ...gatewayService,
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        {
          protocol: "anthropic.messages",
          mode: "native",
          streaming: true,
          convert_to: "openai.chat",
        },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: converting, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          conversionEngine={{
            name: "relaykit",
            version: "v0.1.1",
            available: true,
            edges: [
              {
                from: "anthropic.messages",
                to: "openai.chat",
                quality: "fair",
                streaming: true,
              },
              {
                from: "openai.responses",
                to: "openai.chat",
                quality: "fair",
                streaming: true,
              },
            ],
          }}
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[converting]}
          view={{ kind: "edit", serviceId: converting.id }}
        />,
      );
      await Promise.resolve();
    });

    await openEditorTab("protocols");
    expect(container.textContent).not.toContain("上游实际协议");
    expect(container.textContent).toContain("转换质量一般");
    expect(
      container.querySelector('[data-testid="apply-upstream-protocol"]'),
    ).toBeNull();
  });

  it("keeps models and protocols on separate editor tabs", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["claude-opus-4-7", "gpt-5"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });

    expect(container.querySelector("h1")?.textContent).toBe("编辑 API 提供商");
    expect(container.textContent).toContain("返回 API 提供商列表");
    expect(container.textContent).not.toContain("服务类型不可变");
    expect(
      container.querySelector('[aria-label="API 提供商类型"]')?.textContent,
    ).toContain("New API");
    expect(
      container.querySelector('[aria-label="API 提供商类型"]')?.textContent,
    ).not.toContain("new-api");
    expect(
      container.querySelector('[data-testid="service-editor-tab-connection"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="service-editor-tab-panel"]')
        ?.className,
    ).toContain("overflow-y-auto");
    expect(
      [
        ...container.querySelectorAll(
          '[data-testid="service-editor-tab-panel"]',
        ),
      ].every((panel) => panel.className.includes("pr-4")),
    ).toBe(true);
    expect(container.textContent).toContain("API 地址");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-testid="service-capability-row"]'),
    ).toBeNull();
    expect(container.textContent).not.toContain("接受的入口协议与格式转换");

    expect(
      container.querySelector('[data-testid="service-form"]')?.className,
    ).toContain("overflow-y-clip");

    await openEditorTab("models");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).not.toBeNull();
    expect(container.textContent).not.toContain("API 地址");
    expect(
      [
        ...container.querySelectorAll(
          '[data-testid="service-editor-tab-panel"]',
        ),
      ].every((panel) => panel.className.includes("pr-4")),
    ).toBe(true);

    await openEditorTab("protocols");
    expect(
      container.querySelectorAll('[data-testid="service-capability-row"]')
        .length,
    ).toBeGreaterThan(0);
    expect(container.textContent).toContain("接受的入口协议与格式转换");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).toBeNull();
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(0);
    expect(
      container.querySelector('[data-testid="service-editor-tab-panel"]')
        ?.className,
    ).toContain("overflow-y-auto");
  });

  it("limits Codex subscription conversions to Responses egress", async () => {
    const native: Service["capabilities"] = [
      { protocol: "openai.responses", mode: "native", streaming: true },
      {
        protocol: "openai.responses.compact",
        mode: "native",
        streaming: false,
      },
      { protocol: "openai.models", mode: "native", streaming: false },
    ];
    const listed: Service = { ...codexService, capabilities: native };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: listed, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          conversionEngine={{
            name: "relaykit",
            version: "v0.1.1",
            available: true,
            edges: [
              {
                from: "openai.chat",
                to: "openai.responses",
                quality: "good",
                streaming: true,
              },
              {
                from: "openai.chat",
                to: "anthropic.messages",
                quality: "fair",
                streaming: true,
              },
            ],
          }}
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });

    await openEditorTab("protocols");
    const row = (label: string) =>
      [
        ...container.querySelectorAll<HTMLElement>(
          '[data-testid="service-capability-row"]',
        ),
      ].find(
        (candidate) =>
          candidate.querySelector("span.font-medium")?.textContent === label,
      );
    expect(container.textContent).toContain(
      "只能以 OpenAI Responses 格式发给上游",
    );
    const responses = row("OpenAI Responses")!;
    const responsesSwitch = responses.querySelector('[role="switch"]')!;
    expect(responsesSwitch.getAttribute("aria-checked")).toBe("true");
    expect(responsesSwitch.hasAttribute("disabled")).toBe(true);
    expect(responses.textContent).toContain("订阅原生支持");
    expect(row("OpenAI Legacy Completions")).toBeUndefined();
    expect(row("Gemini Models")).toBeUndefined();
    const anthropic = row("Anthropic Messages")!;
    expect(
      anthropic.querySelector('[role="switch"]')!.hasAttribute("disabled"),
    ).toBe(true);

    await act(async () => {
      row("OpenAI Chat Completions")!
        .querySelector<HTMLButtonElement>('[role="switch"]')!
        .click();
      await Promise.resolve();
    });
    const trigger = document.querySelector<HTMLButtonElement>(
      'button[role="combobox"][aria-label="OpenAI Chat Completions 本地转换"]',
    )!;
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
    expect(
      [...document.querySelectorAll<HTMLElement>('[role="option"]')].map(
        (option) => option.textContent?.trim(),
      ),
    ).toEqual(["转换为 OpenAI Responses"]);
    await act(async () => {
      document.querySelector<HTMLElement>('[role="option"]')!.click();
      await Promise.resolve();
    });

    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({
        capabilities: [
          ...native,
          {
            protocol: "openai.chat",
            mode: "native",
            streaming: true,
            convert_to: "openai.responses",
          },
        ],
      }),
    );
  });

  it("probes saved Codex models through the connected subscription", async () => {
    const listed: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "connected" },
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-5", "gpt-5-codex"],
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.probeServiceModels).toHaveBeenCalledWith(
      listed.id,
      "openai.models",
    );
    expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
  });

  it("imports Codex models after login completes", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
      models: ["gpt-5"],
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "completed",
      flow: "browser",
      service_id: authorizing.id,
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    bridgeMocks.getService.mockResolvedValue({ service: authorizing, etag });
    bridgeMocks.probeServiceModels.mockResolvedValue({
      service_id: authorizing.id,
      protocol: "openai.models",
      model_ids: ["gpt-4o", "gpt-5", "gpt-5-codex"],
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: {
        ...authorizing,
        models: ["gpt-4o", "gpt-5", "gpt-5-codex"],
      },
      etag: `"sha256:${"b".repeat(64)}"`,
    });
    const onRefresh = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={onRefresh}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(bridgeMocks.probeServiceModels).toHaveBeenCalledWith(
        authorizing.id,
        "openai.models",
      );
    });
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      authorizing.id,
      etag,
      expect.objectContaining({
        models: ["gpt-4o", "gpt-5", "gpt-5-codex"],
      }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "“Codex personal”已登录，已获取 3 个模型。",
    );
    expect(onRefresh).toHaveBeenCalled();
  });

  it("does not import Codex models when login ends without completing", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "cancelled",
      flow: "browser",
      service_id: authorizing.id,
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    const onRefresh = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={onRefresh}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(onRefresh).toHaveBeenCalled();
    });
    expect(bridgeMocks.probeServiceModels).not.toHaveBeenCalled();
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });

  it("warns when login succeeds but Codex model import fails", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "completed",
      flow: "browser",
      service_id: authorizing.id,
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    bridgeMocks.getService.mockResolvedValue({ service: authorizing, etag });
    bridgeMocks.probeServiceModels.mockRejectedValue(
      new Error("codex models returned status 401"),
    );

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(notifyMocks.warning).toHaveBeenCalledWith(
        "codex models returned status 401",
      );
    });
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });
  it("inherits global settings until an optional service exception is enabled", async () => {
    const policy = { ...defaultFailurePolicy(), max_retries: 3 };
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      default_failure_policy: policy,
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
    });
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: gatewayService,
      etag,
    });
    await act(async () =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      ),
    );
    await openEditorTab("failure");
    const toggle = [
      ...container.querySelectorAll<HTMLButtonElement>('button[role="switch"]'),
    ].find((button) =>
      button
        .closest("label")
        ?.textContent?.includes("为这个 API 提供商单独设置"),
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("默认跟随路由页面的默认策略");
    expect(container.textContent).not.toContain("遇到这些错误时");
    await act(async () => toggle.click());
    const retries = container.querySelector<HTMLInputElement>(
      '[data-testid="service-editor-tab-panel"][data-state="active"] input[type="number"]',
    );
    expect(retries?.value).toBe("3");
    await act(async () => toggle.click());
    expect(
      container.querySelector('[data-testid="service-form"]')?.className,
    ).toContain("overflow-y-clip");
    await openEditorTab("models");
    await openEditorTab("failure");
    expect(
      container.querySelector(
        '[data-testid="service-editor-tab-panel"][data-state="active"]',
      )?.className,
    ).toContain("overflow-y-auto");
    await act(async () => {
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        );
    });
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      expect.objectContaining({ failure_policy: null }),
    );
  });

  describe("upstream risk", () => {
    const hour = 3_600_000;
    const suspendedClaude: Service = {
      id: "service_claude_personal",
      name: "Claude personal",
      kind: "claude_subscription",
      enabled: true,
      models: [],
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
      ],
      subscription: {
        provider: "claude_code",
        status: "disconnected",
        risk: {
          state: "suspended",
          code: "organization_disabled",
          message: "This organization has been disabled.",
          http_status: 403,
          observed_at: timestamp,
          occurrences: 2,
        },
      },
      created_at: timestamp,
      updated_at: timestamp,
    };
    const coolingCodex = (pausedUntil: string): Service => ({
      ...codexService,
      subscription: {
        provider: "openai_codex",
        status: "disconnected",
        risk: {
          state: "cooling",
          code: "rate_limit_5h",
          observed_at: timestamp,
          paused_until: pausedUntil,
        },
      },
    });

    async function renderList(
      services: Service[],
      props: {
        catalogStatus?: "ready" | "loading";
        onRefresh?: () => void;
        onServiceSaved?: (service: Service) => void;
      } = {},
    ): Promise<void> {
      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus={props.catalogStatus ?? "ready"}
            isReady
            onDirtyChange={() => {}}
            onRefresh={props.onRefresh ?? (() => {})}
            onServiceRemoved={() => {}}
            onServiceSaved={props.onServiceSaved ?? (() => {})}
            onViewChange={() => {}}
            protocols={[]}
            services={services}
            view={{ kind: "list" }}
          />,
        );
        await Promise.resolve();
      });
      await act(async () => {
        await Promise.resolve();
      });
    }

    function riskBadge(state: "suspended" | "cooling"): HTMLButtonElement {
      const badge = container.querySelector<HTMLButtonElement>(
        `button[data-testid="service-risk-badge"][data-risk-state="${state}"]`,
      );
      if (!badge) throw new Error(`missing ${state} risk badge`);
      return badge;
    }

    function buttonWithText(
      scope: ParentNode,
      text: string,
    ): HTMLButtonElement {
      const button = [
        ...scope.querySelectorAll<HTMLButtonElement>("button"),
      ].find((candidate) => candidate.textContent?.trim() === text);
      if (!button) throw new Error(`missing button: ${text}`);
      return button;
    }

    it("badges paused and cooling subscriptions and summarizes suspended accounts", async () => {
      const disabledSuspended: Service = {
        ...suspendedClaude,
        id: "service_claude_work",
        name: "Claude work",
        enabled: false,
      };
      const elapsed: Service = {
        ...coolingCodex(new Date(Date.now() - hour).toISOString()),
        id: "service_codex_elapsed",
        name: "Codex elapsed",
      };
      await renderList([
        suspendedClaude,
        coolingCodex(new Date(Date.now() + 3.5 * hour).toISOString()),
        disabledSuspended,
        elapsed,
        gatewayService,
      ]);

      expect(riskBadge("suspended").textContent).toBe("已暂停调度");
      expect(riskBadge("cooling").textContent).toBe("冷却中 · 3 小时后重置");
      expect(
        riskBadge("suspended").querySelector('[data-tone="negative"]'),
      ).not.toBeNull();
      expect(
        riskBadge("cooling").querySelector('[data-tone="pending"]'),
      ).not.toBeNull();
      // The disabled account keeps its badge; the elapsed cooling does not.
      expect(
        container.querySelectorAll('[data-testid="service-risk-badge"]'),
      ).toHaveLength(3);
      expect(
        packageCard("Codex elapsed").querySelector(
          '[data-testid="service-risk-badge"]',
        ),
      ).toBeNull();

      const status = packageCard(suspendedClaude.name).querySelector(
        '[data-slot="quota-status"]',
      );
      expect(status?.textContent).toBe("待登录 · 组织已被上游停用");
      expect(status?.getAttribute("data-tone")).toBe("critical");

      const summary = container.querySelector<HTMLElement>(
        '[data-testid="service-risk-summary"]',
      );
      expect(summary?.textContent).toBe("1 个订阅账号因上游风控已暂停调度。");
      expect(bridgeMocks.listServiceRiskEvents).not.toHaveBeenCalled();
    });

    it("omits the risk summary when no enabled account is suspended", async () => {
      await renderList([
        coolingCodex(new Date(Date.now() + hour).toISOString()),
        gatewayService,
      ]);
      expect(
        container.querySelector('[data-testid="service-risk-summary"]'),
      ).toBeNull();
    });

    it("shows the reauthorization reason in the status label", async () => {
      await renderList([
        {
          ...codexService,
          subscription: {
            provider: "openai_codex",
            status: "needs_reauth",
            last_error: {
              code: "refresh_failed",
              message: "Refresh token was revoked.",
            },
          },
        },
      ]);
      expect(
        packageCard(codexService.name).querySelector(
          '[data-slot="quota-status"]',
        )?.textContent,
      ).toBe("需要重新登录：Refresh token was revoked.");
    });

    it("loads risk events lazily and restores scheduling after confirmation", async () => {
      const onRefresh = vi.fn();
      const onServiceSaved = vi.fn();
      const cleared: Service = {
        ...suspendedClaude,
        subscription: { provider: "claude_code", status: "disconnected" },
      };
      bridgeMocks.listServiceRiskEvents.mockResolvedValue([
        {
          id: 9,
          service_id: suspendedClaude.id,
          kind: "suspended",
          code: "organization_disabled",
          message: "This organization has been disabled.",
          http_status: 403,
          observed_at: timestamp,
        },
        {
          id: 8,
          service_id: suspendedClaude.id,
          kind: "cleared",
          observed_at: "2026-07-28T11:00:00Z",
        },
      ]);
      bridgeMocks.clearServiceRisk.mockResolvedValue({
        service: cleared,
        etag,
      });
      await renderList([suspendedClaude], { onRefresh, onServiceSaved });

      await act(async () => {
        riskBadge("suspended").click();
        await Promise.resolve();
      });
      const popover = document.querySelector<HTMLElement>(
        '[data-testid="service-risk-popover"]',
      );
      if (!popover) throw new Error("missing risk popover");
      expect(bridgeMocks.listServiceRiskEvents).toHaveBeenCalledWith(
        suspendedClaude.id,
        20,
      );
      expect(popover.textContent).toContain("组织已被上游停用");
      expect(
        popover.querySelector('[data-testid="service-risk-message"]')
          ?.textContent,
      ).toBe("This organization has been disabled.");
      expect(popover.textContent).toContain("HTTP 状态403");
      expect(popover.textContent).toContain("触发次数2");
      expect(popover.textContent).not.toContain("恢复时间");
      const events = popover.querySelector<HTMLElement>(
        '[data-testid="service-risk-events"]',
      );
      expect(events?.querySelectorAll("li")).toHaveLength(2);
      expect(events?.textContent).toContain("暂停调度");
      expect(events?.textContent).toContain("组织已被上游停用 · HTTP 403");
      expect(events?.textContent).toContain("已恢复调度");

      await act(async () => {
        buttonWithText(popover, "恢复调度").click();
        await Promise.resolve();
      });
      expect(bridgeMocks.clearServiceRisk).not.toHaveBeenCalled();
      const dialog = document.querySelector<HTMLElement>(
        '[role="alertdialog"]',
      );
      if (!dialog) throw new Error("missing restore confirmation");
      expect(dialog.textContent).toContain("恢复这个账号的调度？");
      expect(dialog.textContent).toContain("下一次请求会再次暂停调度");

      await act(async () => {
        buttonWithText(dialog, "恢复调度").click();
        await Promise.resolve();
      });
      expect(bridgeMocks.clearServiceRisk).toHaveBeenCalledWith(
        suspendedClaude.id,
      );
      expect(onServiceSaved).toHaveBeenCalledWith(cleared);
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(notifyMocks.success).toHaveBeenCalledWith(
        "已恢复“Claude personal”的调度。",
      );
    });

    it("keeps risk event and restore failures visible", async () => {
      const logged = vi.spyOn(console, "error").mockImplementation(() => {});
      try {
        bridgeMocks.listServiceRiskEvents
          .mockRejectedValueOnce(new Error("GET risk-events returned 503"))
          .mockResolvedValueOnce([]);
        bridgeMocks.clearServiceRisk.mockRejectedValueOnce(
          new Error("POST risk/clear returned 409 Conflict"),
        );
        const onRefresh = vi.fn();
        await renderList(
          [coolingCodex(new Date(Date.now() + hour).toISOString())],
          { onRefresh },
        );

        await act(async () => {
          riskBadge("cooling").click();
          await Promise.resolve();
        });
        const popover = document.querySelector<HTMLElement>(
          '[data-testid="service-risk-popover"]',
        );
        if (!popover) throw new Error("missing risk popover");
        expect(popover.textContent).toContain("已达 5 小时用量上限");
        expect(popover.textContent).toContain("恢复时间");
        expect(popover.textContent).toContain("无法读取风控事件。");
        expect(logged).toHaveBeenCalled();

        await act(async () => {
          buttonWithText(popover, "重试").click();
          await Promise.resolve();
        });
        expect(bridgeMocks.listServiceRiskEvents).toHaveBeenCalledTimes(2);
        expect(popover.textContent).toContain("暂无风控事件。");

        await act(async () => {
          buttonWithText(popover, "恢复调度").click();
          await Promise.resolve();
        });
        const dialog = document.querySelector<HTMLElement>(
          '[role="alertdialog"]',
        );
        if (!dialog) throw new Error("missing restore confirmation");
        await act(async () => {
          buttonWithText(dialog, "恢复调度").click();
          await Promise.resolve();
        });
        expect(bridgeMocks.clearServiceRisk).toHaveBeenCalledWith(
          codexService.id,
        );
        expect(onRefresh).not.toHaveBeenCalled();
        expect(
          container.querySelector('[role="alert"]')?.textContent,
        ).toContain("POST risk/clear returned 409 Conflict");
      } finally {
        logged.mockRestore();
      }
    });

    it("refreshes subscriptions every 30 seconds only while the page is visible", async () => {
      const intervals = vi.spyOn(window, "setInterval");
      const cleared = vi.spyOn(window, "clearInterval");
      const visibility = vi
        .spyOn(document, "visibilityState", "get")
        .mockReturnValue("visible");
      const onRefresh = vi.fn();
      try {
        await renderList([gatewayService], { onRefresh });
        expect(intervals.mock.calls.some(([, delay]) => delay === 30_000)).toBe(
          false,
        );

        await renderList([suspendedClaude, gatewayService], { onRefresh });
        const polls = intervals.mock.calls.filter(
          ([, delay]) => delay === 30_000,
        );
        expect(polls).toHaveLength(1);
        const tick = polls[0][0] as () => void;
        const timer = intervals.mock.results[
          intervals.mock.calls.indexOf(polls[0])
        ].value as number;

        act(() => tick());
        expect(onRefresh).toHaveBeenCalledTimes(1);

        visibility.mockReturnValue("hidden");
        act(() => tick());
        expect(onRefresh).toHaveBeenCalledTimes(1);

        visibility.mockReturnValue("visible");
        await renderList([suspendedClaude, gatewayService], {
          catalogStatus: "loading",
          onRefresh,
        });
        act(() => tick());
        expect(onRefresh).toHaveBeenCalledTimes(1);
        // Re-rendering the same list must not stack another interval.
        expect(
          intervals.mock.calls.filter(([, delay]) => delay === 30_000),
        ).toHaveLength(1);

        await act(async () => root.unmount());
        expect(cleared).toHaveBeenCalledWith(timer);
        root = createRoot(container);
      } finally {
        intervals.mockRestore();
        cleared.mockRestore();
        visibility.mockRestore();
      }
    });
  });
});
