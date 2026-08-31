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
    notifyMocks.success.mockReset();
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
    expect(container.textContent).toContain("一键安装调试 Skill 与 MCP");
    expect(container.textContent).toContain("Cursor");
    expect(container.textContent).toContain("已检测到");
    expect(container.textContent).toContain("Codex");
    expect(container.textContent).toContain("已安装");

    const install = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "安装 Skill 与 MCP",
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
          (button) => button.textContent === "安装 Skill 与 MCP",
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
});
