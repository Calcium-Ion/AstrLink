// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  pendingPluginApproval: vi.fn(),
  decidePluginApproval: vi.fn(),
  getPlugin: vi.fn(),
  listenPluginApprovalChanged: vi.fn(),
}));

vi.mock("./bridge", () => mocks);

import { PluginApprovalWindow } from "./PluginApprovalWindow";

let container: HTMLDivElement;
let root: Root;

const pending = {
  id: "approval-1",
  key: "gate",
  approval: {
    plugin: "gate",
    name: "Gate",
    enable: true,
    added_capabilities: ["request.gate", "network", "ui.code"],
    added_origins: ["http://192.0.2.7:80"],
    plaintext: ["http://192.0.2.7:80"],
  },
};

async function render() {
  await act(async () => {
    root.render(<PluginApprovalWindow />);
  });
  await act(async () => {});
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll("button")].find(
    (item) => item.textContent?.trim() === label,
  );
  if (!found) throw new Error(`Missing button ${label}`);
  return found;
}

describe("PluginApprovalWindow", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    mocks.pendingPluginApproval.mockResolvedValue(pending);
    mocks.decidePluginApproval.mockResolvedValue(undefined);
    mocks.getPlugin.mockRejectedValue(new Error("unavailable"));
    mocks.listenPluginApprovalChanged.mockResolvedValue(() => {});
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("lists what enabling grants and keeps only what is left ticked", async () => {
    await render();
    const text = document.body.textContent ?? "";
    expect(text).toContain("启用「Gate」");
    expect(text).toContain("拒绝请求");
    // Interface code and plain HTTP are spelt out.
    expect(text).toContain("界面代码和 AstrLink 界面同等权限");
    expect(text).toContain("http://192.0.2.7:80 · 明文 HTTP");
    const network = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("网络"));
    if (!network) throw new Error("Missing the network capability");
    await act(async () => network.click());
    await act(async () => button("启用").click());
    expect(mocks.decidePluginApproval).toHaveBeenCalledWith(
      "approval-1",
      true,
      ["request.gate", "ui.code"],
    );
  });

  it("denies without sending capabilities", async () => {
    await render();
    await act(async () => button("拒绝").click());
    expect(mocks.decidePluginApproval).toHaveBeenCalledWith(
      "approval-1",
      false,
      undefined,
    );
  });

  it("shows the review of an install and confirms it as a whole", async () => {
    mocks.pendingPluginApproval.mockResolvedValue({
      id: "approval-2",
      key: "powercontext",
      approval: {
        plugin: "powercontext",
        name: "Memory",
        install: {
          token: "token",
          expires_at: "2026-10-23T13:00:00Z",
          source: {
            kind: "github",
            repository: "acme/memory",
            url: "https://github.com/acme/memory",
            ref: "v2.0.0",
            ref_kind: "tag",
            commit: "0123456789abcdef0123456789abcdef01234567",
            committed_at: "2026-10-01T12:00:00Z",
          },
          key: "powercontext",
          name: "Memory",
          version: "2.0.0",
          api_version: 2,
          capabilities: ["network", "ui.code"],
          hooks: ["prepareTurn"],
          client: true,
          plugin_bytes: 4096,
          dependencies: [{ name: "zod", version: "3.22.4", bytes: 600000 }],
          dependency_bytes: 600000,
          replaces: { origin: "builtin", name: "PowerContext 记忆" },
        },
      },
    });
    await render();
    const text = document.body.textContent ?? "";
    expect(text).toContain("安装「Memory」");
    expect(text).toContain("acme/memory");
    expect(text).toContain("标签 v2.0.0");
    expect(text).toContain("将替换内置插件「PowerContext 记忆」");
    expect(text).toContain("带界面代码");
    expect(text).toContain("依赖 1 个");
    expect(text).not.toContain("0123456789abcdef");
    // No capability to untick: an install grants nothing by itself.
    expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(0);
    await act(async () => button("安装").click());
    expect(mocks.decidePluginApproval).toHaveBeenCalledWith(
      "approval-2",
      true,
      undefined,
    );
  });

  it("says so when nothing waits", async () => {
    mocks.pendingPluginApproval.mockResolvedValue(null);
    await render();
    expect(document.body.textContent).toContain("没有等待确认的插件修改");
  });
});
