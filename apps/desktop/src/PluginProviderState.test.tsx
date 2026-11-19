// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getPluginProviderState: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { parsePluginProviderState } from "./plugin-model";
import { PluginProviderStateField } from "./PluginProviderState";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.clearAllMocks();
});

const read = parsePluginProviderState({
  balance: {
    amount: 12.5,
    currency: "USD",
    windows: [{ name: { en: "Daily", "zh-CN": "每日" }, used: 3, limit: 10 }],
  },
  updated_at: "2026-11-13T08:00:00Z",
});

describe("PluginProviderStateField", () => {
  it("shows the balance and its windows, and reads it again on request", async () => {
    bridgeMocks.getPluginProviderState.mockResolvedValue(read);
    await act(async () =>
      root.render(
        <PluginProviderStateField
          pluginKey="vendor"
          readsBalance
          serviceId="svc_1"
        />,
      ),
    );
    const field = container.querySelector(
      '[data-testid="plugin-provider-state"]',
    );
    expect(field?.textContent).toContain("12.5");
    expect(field?.textContent).toContain("每日");
    expect(field?.textContent).toContain("3 / 10");
    expect(bridgeMocks.getPluginProviderState).toHaveBeenLastCalledWith(
      "vendor",
      { service_id: "svc_1", refresh: false },
    );
    const refresh = container.querySelector("button");
    await act(async () => refresh?.click());
    expect(bridgeMocks.getPluginProviderState).toHaveBeenLastCalledWith(
      "vendor",
      { service_id: "svc_1", refresh: true },
    );
  });

  it("says when the plugin pauses a provider that reads no balance", async () => {
    bridgeMocks.getPluginProviderState.mockResolvedValue(
      parsePluginProviderState({
        resting: true,
        notice: "Daily quota used up",
      }),
    );
    await act(async () =>
      root.render(
        <PluginProviderStateField
          pluginKey="vendor"
          readsBalance={false}
          serviceId="svc_1"
        />,
      ),
    );
    const field = container.querySelector(
      '[data-testid="plugin-provider-state"]',
    );
    expect(field?.textContent).toContain("已暂停");
    expect(field?.textContent).toContain("Daily quota used up");
    expect(container.querySelector("button")).toBeNull();
  });

  it("shows nothing for a provider the plugin neither reads nor pauses", async () => {
    bridgeMocks.getPluginProviderState.mockResolvedValue(
      parsePluginProviderState({}),
    );
    await act(async () =>
      root.render(
        <PluginProviderStateField
          pluginKey="vendor"
          readsBalance={false}
          serviceId="svc_1"
        />,
      ),
    );
    expect(container.textContent).toBe("");
  });
});
