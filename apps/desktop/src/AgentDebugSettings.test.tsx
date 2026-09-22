// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getAgentDebugStatus: vi.fn(),
  installAgentDebug: vi.fn(),
  uninstallAgentDebug: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { applyLocale } from "./i18n";
import { AgentDebugSettings } from "./AgentDebugSettings";

const status = {
  canonical_skill: false,
  mcp_binary: false,
  mcp_command: "/tmp/astrlink-mcp",
  tools: [
    {
      id: "cursor" as const,
      detected: true,
      skill_installed: false,
      mcp_installed: false,
    },
    {
      id: "claude" as const,
      detected: false,
      skill_installed: false,
      mcp_installed: false,
    },
    {
      id: "codex" as const,
      detected: true,
      skill_installed: true,
      mcp_installed: true,
    },
    {
      id: "grok" as const,
      detected: true,
      skill_installed: false,
      mcp_installed: false,
    },
  ],
  preview_paths: ["/tmp/.agents/skills/astrlink-debug"],
};

describe("AgentDebugSettings", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getAgentDebugStatus.mockReset().mockResolvedValue(status);
    bridge.installAgentDebug.mockReset().mockResolvedValue({
      version: 1,
      bundle: "astrlink-debug",
      bundle_version: "0.1.0",
      installed_at_unix: 1,
      mcp_binary: "/tmp/astrlink-mcp",
      files: status.preview_paths,
    });
    bridge.uninstallAgentDebug.mockReset().mockResolvedValue(undefined);
    notifyMocks.success.mockReset();
    notifyMocks.error.mockReset();
  });

  afterEach(async () => {
    await applyLocale("zh-CN");
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows detected tools and confirms install", async () => {
    await act(async () => {
      root.render(<AgentDebugSettings />);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(container.querySelector("h1")?.textContent).toBe("Agent 工具");
    expect(container.textContent).toContain("工具接入");
    expect(container.textContent).toContain("Cursor");
    expect(container.textContent).toContain("已检测到");
    expect(container.textContent).toContain("Codex");
    expect(container.textContent).toContain("已安装");
    expect(container.textContent).toContain("Grok Build");
    expect(container.textContent).toContain("1 / 3");

    const install = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "补全安装",
    );
    if (!install) throw new Error("missing install button");
    await act(async () => {
      install.click();
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("/tmp/.agents/skills/astrlink-debug");

    const dialog = document.querySelector("[role='alertdialog']");
    const confirm = dialog
      ? [...dialog.querySelectorAll("button")].find(
          (button) => button.textContent === "补全安装",
        )
      : undefined;
    if (!confirm) throw new Error("missing confirm");
    await act(async () => {
      confirm.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(bridge.installAgentDebug).toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalled();
  });

  it("identifies a missing component and refreshes after repair", async () => {
    const partial = {
      ...status,
      mcp_binary: true,
      tools: [{ id: "codex", detected: true, skill_installed: true, mcp_installed: false }],
    };
    bridge.getAgentDebugStatus.mockResolvedValueOnce(partial).mockResolvedValue({
      ...partial,
      tools: [{ ...partial.tools[0], mcp_installed: true }],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    const row = [...container.querySelectorAll("tbody tr")].find((row) => row.textContent?.includes("Codex"));
    expect(row?.querySelectorAll("td")[1]?.textContent).toBe("已安装");
    expect(row?.querySelectorAll("td")[2]?.textContent).toBe("未安装");
    await act(async () => button("补全安装").click());
    await act(async () => button("补全安装", document.querySelector("[role='alertdialog']")!).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledTimes(1);
    expect(button("重新安装").disabled).toBe(false);
    expect(container.textContent).toContain("1 / 1");
  });

  it("offers a retry after status failure and prevents installing without detected tools", async () => {
    bridge.getAgentDebugStatus.mockRejectedValueOnce(new Error("Status unavailable"));
    await act(async () => root.render(<AgentDebugSettings />));
    expect(container.querySelector("[role='alert']")?.textContent).toBe("Status unavailable");
    expect(button("安装工具").disabled).toBe(true);
    expect(container.textContent).not.toContain("检查中");
    bridge.getAgentDebugStatus.mockResolvedValue({ ...status, canonical_skill: false, mcp_binary: false, tools: [] });
    await act(async () => button("重新检测").click());
    expect(container.querySelector("[role='alert']")).toBeNull();
    expect(container.textContent).toContain("暂未检测到支持的工具");
    expect(button("安装工具").disabled).toBe(true);
  });

  it("copies the diagnostic prompt and keeps uninstall behind confirmation", async () => {
    const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("复制示例").click());
    expect(copy).toHaveBeenCalledWith(expect.stringContaining("使用 astrlink-debug"));
    expect(button("已复制")).toBeTruthy();
    await act(async () => button("卸载").click());
    expect(bridge.uninstallAgentDebug).not.toHaveBeenCalled();
    const dialog = document.querySelector("[role='alertdialog']")!;
    await act(async () => button("卸载", dialog).click());
    expect(bridge.uninstallAgentDebug).toHaveBeenCalledTimes(1);
    copy.mockRestore();
  });

  function button(text: string, scope: ParentNode = container): HTMLButtonElement {
    const found = [...scope.querySelectorAll("button")].find((item) => item.textContent === text);
    if (!found) throw new Error(`Missing button: ${text}`);
    return found;
  }
});
