// @vitest-environment happy-dom

import { act, type ComponentType } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  getPluginHostSettings: vi.fn(),
  getPlugin: vi.fn(),
  updatePlugin: vi.fn(),
  callPluginUi: vi.fn(),
  warning: vi.fn(),
}));

vi.mock("./bridge", () => ({
  listPlugins: mocks.listPlugins,
  getPluginHostSettings: mocks.getPluginHostSettings,
  getPlugin: mocks.getPlugin,
  updatePlugin: mocks.updatePlugin,
  callPluginUi: mocks.callPluginUi,
}));
vi.mock("./notify", () => ({
  notify: {
    warning: mocks.warning,
    info: vi.fn(),
    success: vi.fn(),
    error: vi.fn(),
  },
}));

import {
  PluginSlot,
  PluginUiLoader,
  pluginClientURL,
  pluginOfScript,
  pluginUi,
  setPluginUiSafeMode,
  type PluginClientHost,
} from "./plugin-ui";

type Sdk = {
  registerSlot: (
    slot: string,
    component: ComponentType<{ context: unknown }>,
    options?: { title?: string },
  ) => () => void;
  call: (name: string, input?: unknown) => Promise<unknown>;
  t: (texts: Record<string, string>, vars?: Record<string, unknown>) => string;
};

let root: Root;
let container: HTMLDivElement;
/** What each plugin's code does once loaded, by key. */
let plugins: Record<string, (client: PluginClientHost) => void>;
let appended: string[];

function list(...entries: { key: string; version?: string }[]) {
  return {
    directory: "/data/plugins",
    plugins: entries.map(({ key, version }) => ({
      key,
      name: key.toUpperCase(),
      version: "1.0.0",
      source: "directory",
      enabled: true,
      state: "loaded",
      requested_capabilities: ["ui.code"],
      granted_capabilities: ["ui.code"],
      needs_network: false,
      ...(version ? { ui_version: version } : {}),
    })),
  };
}

async function settle() {
  await act(async () => {
    for (let index = 0; index < 5; index++) await Promise.resolve();
  });
}

describe("plugin interfaces", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    localStorage.clear();
    plugins = {};
    appended = [];
    mocks.getPluginHostSettings.mockResolvedValue({ web_ui: false });
    mocks.updatePlugin.mockResolvedValue({ config: {} });
    // The window "loads" a script by running what the plugin's code would.
    const append = HTMLHeadElement.prototype.append;
    vi.spyOn(document.head, "append").mockImplementation(function (
      this: HTMLHeadElement,
      ...nodes: (Node | string)[]
    ) {
      for (const node of nodes) {
        if (node instanceof HTMLScriptElement && node.dataset.plugin) {
          const key = node.dataset.plugin;
          appended.push(node.src);
          queueMicrotask(() => {
            const code = plugins[key] as
              | ((client: PluginClientHost) => void)
              | undefined;
            if (code) window.__astrlinkPlugins?.define(key, code);
            node.dispatchEvent(new Event(code ? "load" : "error"));
          });
          continue;
        }
        append.call(this, node);
      }
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    for (const key of Object.keys(pluginUi.get().loaded)) pluginUi.drop(key);
    vi.restoreAllMocks();
  });

  async function render() {
    await act(async () => {
      root.render(
        <>
          <PluginUiLoader isReady onNavigate={() => {}} sessionKey="core-1" />
          <PluginSlot name="overview.widget" />
        </>,
      );
    });
    await settle();
  }

  it("loads a plugin's code, which registers its components and calls its backend", async () => {
    mocks.listPlugins.mockResolvedValue(list({ key: "notes", version: "v1" }));
    mocks.callPluginUi.mockResolvedValue({ count: 3 });
    let sdk: Sdk | null = null;
    plugins.notes = (client) => {
      sdk = client.module("@astrlink/plugin/client") as Sdk;
      const react = client.module("react") as typeof import("react");
      expect(typeof react.useState).toBe("function");
      sdk.registerSlot(
        "overview.widget",
        () => <p data-testid="notes-widget">notes</p>,
        { title: "Notes" },
      );
    };
    await render();
    expect(appended).toEqual([
      "astrlink-plugin://localhost/notes/client.js?v=v1",
    ]);
    expect(
      container.querySelector('[data-testid="notes-widget"]')?.textContent,
    ).toBe("notes");
    expect(
      container
        .querySelector('[data-plugin="notes"]')
        ?.getAttribute("aria-label"),
    ).toBe("Notes");
    expect(await sdk!.call("count", { day: 1 })).toEqual({ count: 3 });
    expect(mocks.callPluginUi).toHaveBeenCalledWith("notes", "count", {
      day: 1,
    });
    expect(
      sdk!.t({ en: "{{n}} notes", "zh-CN": "{{n}} 条笔记" }, { n: 2 }),
    ).toBe("2 条笔记");
    expect(() => sdk!.registerSlot("nowhere", () => null)).toThrow(
      "unknown slot",
    );

    // Turned off in Core, its components go.
    mocks.listPlugins.mockResolvedValue(list({ key: "notes" }));
    await act(async () => pluginUi.refresh());
    await settle();
    expect(container.querySelector('[data-testid="notes-widget"]')).toBeNull();
  });

  it("keeps a failing component to itself and turns an interface off that keeps failing", async () => {
    mocks.listPlugins.mockResolvedValue(
      list({ key: "broken", version: "v1" }, { key: "fine", version: "v1" }),
    );
    vi.spyOn(console, "error").mockImplementation(() => {});
    plugins.broken = (client) => {
      const sdk = client.module("@astrlink/plugin/client") as Sdk;
      for (let index = 0; index < 3; index++)
        sdk.registerSlot("overview.widget", () => {
          throw new Error("kaput");
        });
    };
    plugins.fine = (client) => {
      (client.module("@astrlink/plugin/client") as Sdk).registerSlot(
        "overview.widget",
        () => <p data-testid="fine-widget">fine</p>,
      );
    };
    await render();
    await settle();
    // Three failures within a minute: the broken interface is turned off.
    expect(mocks.updatePlugin).toHaveBeenCalledWith("broken", {
      ui_enabled: false,
    });
    expect(mocks.warning).toHaveBeenCalledWith(
      expect.stringContaining("BROKEN"),
    );
    expect(
      pluginUi.get().entries.some((entry) => entry.plugin === "broken"),
    ).toBe(false);
    expect(
      container.querySelector('[data-testid="fine-widget"]'),
    ).not.toBeNull();
  });

  it("loads nothing in safe mode, and enters it after a start that did not finish loading", async () => {
    mocks.listPlugins.mockResolvedValue(list({ key: "notes", version: "v1" }));
    plugins.notes = (client) =>
      (client.module("@astrlink/plugin/client") as Sdk).registerSlot(
        "overview.widget",
        () => <p data-testid="notes-widget">notes</p>,
      );
    localStorage.setItem("astrlink:plugin-ui-loading", String(Date.now()));
    await render();
    expect(pluginUi.get().safeMode).toBe("crashed");
    expect(mocks.warning).toHaveBeenCalled();
    expect(appended).toEqual([]);

    await act(async () => setPluginUiSafeMode(false));
    await settle();
    expect(
      container.querySelector('[data-testid="notes-widget"]'),
    ).not.toBeNull();

    await act(async () => setPluginUiSafeMode(true));
    expect(container.querySelector('[data-testid="notes-widget"]')).toBeNull();
    expect(localStorage.getItem("astrlink:plugin-ui-safe-mode")).toBe("1");
    await act(async () => setPluginUiSafeMode(false));
    await settle();
  });

  it("names where the code loads from on each platform", () => {
    expect(pluginClientURL("notes", "abc")).toBe(
      "astrlink-plugin://localhost/notes/client.js?v=abc",
    );
    expect(
      pluginOfScript("http://astrlink-plugin.localhost/notes/client.js?v=1"),
    ).toBe("notes");
    expect(pluginOfScript("/control/v1/plugins/notes/client.js?v=1")).toBe(
      "notes",
    );
    expect(pluginOfScript("https://example.com/app.js")).toBeNull();
  });
});
