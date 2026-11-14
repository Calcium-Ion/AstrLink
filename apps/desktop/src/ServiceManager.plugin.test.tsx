// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createService: vi.fn(),
  getService: vi.fn(),
  getServiceOrder: vi
    .fn()
    .mockResolvedValue({ service_ids: [], etag: '"order"' }),
  updateServiceOrder: vi.fn(),
  getRoutingSettings: vi.fn(),
  getServiceUsage: vi.fn(),
  getServiceResetCredits: vi.fn(),
  getUsageSummary: vi.fn(),
  listServiceRiskEvents: vi.fn(),
  listPlugins: vi.fn(),
  probeDraftServiceModels: vi.fn(),
  probeServiceModels: vi.fn(),
  probeServiceProxy: vi.fn(),
  updateService: vi.fn(),
  listTerminalApps: vi.fn(),
  getClientConfigStatus: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);
vi.mock("./notify", () => ({
  notify: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

import {
  pluginProviderChoices,
  ServiceKindPickerDialog,
  ServiceManager,
  type PluginProviderChoice,
} from "./ServiceManager";
import type { PluginList } from "./plugin-model";

const plugins: PluginList = {
  directory: "/data/plugins",
  plugins: [
    {
      key: "acme",
      name: "Acme",
      version: "1.0.0",
      source: "directory",
      enabled: true,
      state: "loaded",
      requested_capabilities: ["provider"],
      granted_capabilities: ["provider"],
      needs_network: false,
      providers: [
        {
          id: "acme",
          name: "Acme AI",
          base_url: "https://api.acme.example/v1",
          auth: { scheme: "none" },
          protocols: [{ protocol: "openai.chat", stream: true, sync: true }],
          models: ["acme-1"],
        },
      ],
    },
    {
      key: "dormant",
      name: "Dormant",
      version: "1.0.0",
      source: "directory",
      enabled: false,
      state: "loaded",
      requested_capabilities: ["provider"],
      granted_capabilities: [],
      needs_network: false,
      providers: [
        {
          id: "dormant",
          name: "Dormant AI",
          auth: { scheme: "bearer" },
          protocols: [{ protocol: "openai.chat", stream: false, sync: true }],
        },
      ],
    },
  ],
};

/** A task plugin that can also drive another new-api gateway with it. */
const vertex: PluginList["plugins"][number] = {
  key: "vertex",
  name: "Vertex AI",
  version: "1.0.0",
  source: "directory",
  enabled: true,
  state: "loaded",
  kind: "task",
  icon: "data:image/svg+xml;base64,PHN2Zy8+",
  requested_capabilities: [],
  granted_capabilities: [],
  needs_network: false,
  task: {
    models: ["veo-3.0-generate-001"],
    fetch_mode: "per_task",
    auth: "oauth2_jwt",
    new_api: true,
    protocols: [{ name: "openai_video" }],
    routes: [],
  },
  providers: [
    {
      id: "task",
      name: "Vertex AI",
      auth: { scheme: "bearer" },
      protocols: [
        { protocol: "openai.videos", stream: false, sync: true },
        { protocol: "plugin.native", stream: false, sync: true },
      ],
      models: ["veo-3.0-generate-001"],
    },
  ],
};

let container: HTMLDivElement;
let root: Root;

async function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("plugin providers", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.listPlugins.mockResolvedValue(plugins);
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("offers the providers of enabled plugins granted provider", () => {
    const choices = pluginProviderChoices(plugins);
    expect(choices.map((choice) => choice.provider.name)).toEqual(["Acme AI"]);
    expect(choices[0].pluginName).toBe("Acme");
  });

  it("offers a task plugin's provider, granted or not, with its icon", async () => {
    bridgeMocks.listPlugins.mockResolvedValue({
      ...plugins,
      plugins: [...plugins.plugins, vertex],
    });
    expect(
      pluginProviderChoices({
        ...plugins,
        plugins: [...plugins.plugins, vertex],
      }).map((choice) => choice.plugin),
    ).toEqual(["acme", "vertex"]);
    await act(async () =>
      root.render(
        <ServiceKindPickerDialog
          onOpenChange={() => {}}
          onSelect={() => {}}
          open
        />,
      ),
    );
    await act(async () => {});
    const option = [...document.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("由「Vertex AI」插件转接"),
    );
    expect(
      option?.querySelector('[data-slot="plugin-icon"]')?.getAttribute("src"),
    ).toBe(vertex.icon);
  });

  it("starts a task plugin's provider without protocols to choose, and saves the gateway it drives", async () => {
    const choice = pluginProviderChoices({
      ...plugins,
      plugins: [vertex],
    })[0];
    bridgeMocks.createService.mockImplementation(async (input) => ({
      etag: '"1"',
      service: {
        ...input,
        id: "service_vertex",
        enabled: true,
        http: { ...input.http, credential_ref: "local://service_vertex" },
        created_at: "2026-10-25T12:00:00Z",
        updated_at: "2026-10-25T12:00:00Z",
      },
    }));
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
          view={{ kind: "create", serviceKind: "plugin", plugin: choice }}
        />,
      ),
    );
    await act(async () => {});
    const protocolsTab = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
    ].find((tab) => tab.textContent?.includes("入口协议"));
    expect(protocolsTab?.disabled).toBe(true);
    // Its own vendor takes a service account; another gateway its token.
    expect(container.textContent).toContain("服务账号 JSON");
    expect(container.textContent).not.toContain("Responses WebSocket");
    const upstream = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="上游是另一个 New API 网关"]',
    );
    if (!upstream) throw new Error("no switch for another gateway");
    await act(async () => upstream.click());
    expect(container.textContent).not.toContain("服务账号 JSON");
    const address = [
      ...container.querySelectorAll<HTMLInputElement>("input"),
    ].find((input) => input.placeholder.startsWith("https://"));
    if (address) await typeInto(address, "https://gateway.example");
    const key = container.querySelector<HTMLInputElement>(
      'input[aria-label="API Key"]',
    );
    if (!key) throw new Error("no key field");
    await typeInto(key, "sk-gateway");
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "plugin",
        name: "Vertex AI",
        plugin: { key: "vertex", provider: "task", upstream: "new_api" },
        http: expect.objectContaining({
          base_url: "https://gateway.example",
          credential: { secret: "sk-gateway" },
        }),
      }),
    );
  });

  it("lists them in the add-provider picker", async () => {
    const onSelect = vi.fn();
    await act(async () =>
      root.render(
        <ServiceKindPickerDialog
          onOpenChange={() => {}}
          onSelect={onSelect}
          open
        />,
      ),
    );
    await act(async () => {});
    const option = [...document.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("Acme AI"),
    );
    if (!option) throw new Error("the picker offers no Acme AI");
    expect(option.textContent).toContain("Acme");
    await act(async () => option.click());
    expect(onSelect).toHaveBeenCalledWith(
      "plugin",
      expect.objectContaining({ plugin: "acme" }),
    );
  });

  it("starts a provider as the plugin declares it and saves which plugin adapts it", async () => {
    const choice: PluginProviderChoice = pluginProviderChoices(plugins)[0];
    bridgeMocks.createService.mockImplementation(async (input) => ({
      etag: '"1"',
      service: {
        ...input,
        id: "service_acme",
        enabled: true,
        http: { ...input.http, credential_ref: undefined },
        created_at: "2026-10-23T12:00:00Z",
        updated_at: "2026-10-23T12:00:00Z",
      },
    }));
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
          view={{ kind: "create", serviceKind: "plugin", plugin: choice }}
        />,
      ),
    );
    await act(async () => {});
    const inputs = [...container.querySelectorAll<HTMLInputElement>("input")];
    expect(inputs.some((input) => input.value === "Acme AI")).toBe(true);
    expect(
      inputs.some((input) => input.value === "https://api.acme.example/v1"),
    ).toBe(true);
    await act(async () =>
      container
        .querySelector("form")!
        .dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        ),
    );
    expect(bridgeMocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "plugin",
        name: "Acme AI",
        plugin: { key: "acme", provider: "acme" },
        models: ["acme-1"],
        http: expect.objectContaining({
          base_url: "https://api.acme.example/v1",
          auth: { scheme: "none" },
        }),
        capabilities: [
          { protocol: "openai.chat", mode: "native", streaming: true },
        ],
      }),
    );
  });
});
