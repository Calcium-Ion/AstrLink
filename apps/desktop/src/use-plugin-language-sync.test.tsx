// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({ updatePluginHostSettings: vi.fn() }));
vi.mock("./bridge", () => bridge);

import { applyLocale } from "./i18n";
import { usePluginLanguageSync } from "./use-plugin-language-sync";

function Probe({ sessionKey }: { sessionKey: string | null }) {
  usePluginLanguageSync(sessionKey);
  return null;
}

let root: Root, container: HTMLDivElement;
beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.resetAllMocks();
  bridge.updatePluginHostSettings.mockResolvedValue({});
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  await applyLocale("zh-CN");
});

it("tells Core the interface language when it starts and when it changes", async () => {
  await act(async () => root.render(<Probe sessionKey={null} />));
  expect(bridge.updatePluginHostSettings).not.toHaveBeenCalled();
  await act(async () => root.render(<Probe sessionKey="core-1" />));
  expect(bridge.updatePluginHostSettings).toHaveBeenLastCalledWith({
    language: "zh-CN",
  });
  await act(async () => applyLocale("ja"));
  expect(bridge.updatePluginHostSettings).toHaveBeenLastCalledWith({
    language: "ja",
  });
  // A restarted Core hears it again.
  await act(async () => root.render(<Probe sessionKey="core-2" />));
  expect(bridge.updatePluginHostSettings).toHaveBeenCalledTimes(3);
});

it("keeps a Core that refuses the language from breaking the page", async () => {
  bridge.updatePluginHostSettings.mockRejectedValue(new Error("no plugins"));
  await act(async () => root.render(<Probe sessionKey="core-1" />));
  expect(bridge.updatePluginHostSettings).toHaveBeenCalledTimes(1);
});
