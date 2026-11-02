// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ClaudeCLIStatus } from "./subscription-model";

const mocks = vi.hoisted(() => ({
  getClaudeCLI: vi.fn(),
  installClaudeCLI: vi.fn(),
  openExternalURL: vi.fn(),
}));
vi.mock("./bridge", () => mocks);
import { ClaudeCLINotice, claudeCodeSetupURL } from "./ClaudeCLINotice";

const missing: ClaudeCLIStatus = {
  installed: false,
  supported: false,
  minimum_version: "2.1.248",
  install_available: false,
  installing: false,
};

describe("ClaudeCLINotice", () => {
  let container: HTMLDivElement, root: Root;

  beforeEach(() => {
    vi.clearAllMocks();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render() {
    await act(async () => root.render(<ClaudeCLINotice />));
  }
  function button(label: string) {
    return Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === label,
    );
  }

  it("names the CLI it found", async () => {
    mocks.getClaudeCLI.mockResolvedValue({
      ...missing,
      installed: true,
      supported: true,
      version: "2.1.300",
      path: "/usr/local/bin/claude",
    });
    await render();
    expect(container.textContent).toBe("使用 Claude Code 2.1.300。");
    expect(container.querySelector("button")).toBeNull();
  });

  it("links a desktop without the CLI to the official instructions", async () => {
    mocks.getClaudeCLI.mockResolvedValue(missing);
    await render();
    expect(container.textContent).toContain("这台电脑上没有找到 Claude Code");
    expect(button("安装 Claude Code")).toBeUndefined();
    await act(async () => button("查看安装说明")!.click());
    expect(mocks.openExternalURL).toHaveBeenCalledWith(claudeCodeSetupURL);
    expect(mocks.installClaudeCLI).not.toHaveBeenCalled();
  });

  it("asks a desktop with an old CLI to update it", async () => {
    mocks.getClaudeCLI.mockResolvedValue({
      ...missing,
      installed: true,
      version: "2.0.1",
    });
    await render();
    expect(container.textContent).toContain(
      "Claude Code 2.0.1 太旧，需要 2.1.248 或更新的版本",
    );
  });

  it("installs on request where Core offers it and follows the install", async () => {
    vi.useFakeTimers();
    try {
      mocks.getClaudeCLI
        .mockResolvedValueOnce({ ...missing, install_available: true })
        .mockResolvedValue({
          ...missing,
          install_available: true,
          installed: true,
          supported: true,
          version: "2.1.300",
        });
      mocks.installClaudeCLI.mockResolvedValue({
        ...missing,
        install_available: true,
        installing: true,
      });
      await render();
      expect(container.textContent).toContain("服务器上还没有 Claude Code");
      expect(button("查看安装说明")).toBeUndefined();
      await act(async () => button("安装 Claude Code")!.click());
      expect(mocks.installClaudeCLI).toHaveBeenCalledTimes(1);
      expect(container.textContent).toBe("正在安装 Claude Code…");
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });
      expect(container.textContent).toBe("使用 Claude Code 2.1.300。");
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows why the last install failed", async () => {
    mocks.getClaudeCLI.mockResolvedValue({
      ...missing,
      install_available: true,
      install_error: "install failed: curl: (6) Could not resolve host",
    });
    await render();
    expect(container.textContent).toContain(
      "上次安装失败：install failed: curl: (6) Could not resolve host",
    );
    expect(button("安装 Claude Code")).toBeDefined();
  });
});
