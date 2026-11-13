// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  emptyLocalClients,
  type LocalClientSnapshot,
} from "./local-client-model";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  refresh: vi.fn(),
  update: vi.fn(),
  repair: vi.fn(),
  listen: vi.fn(),
  open: vi.fn(),
  native: true,
}));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => mocks.native }));
vi.mock("./bridge", () => ({ openExternalURL: mocks.open }));
vi.mock("./local-client-bridge", () => ({
  getLocalClients: mocks.get,
  refreshLocalClients: mocks.refresh,
  updateLocalClients: mocks.update,
  repairLocalClients: mocks.repair,
  listenLocalClients: mocks.listen,
}));
import { LocalClientUpdates } from "./LocalClientUpdates";

function available(): LocalClientSnapshot {
  return {
    ...emptyLocalClients(),
    revision: 1,
    clients: emptyLocalClients().clients.map((c) => ({
      ...c,
      phase: "available",
      current_version: "1.0.0",
      latest_version: "1.1.0",
      install_method: "native",
      executable: `/bin/${c.id}`,
      can_update: true,
    })),
  };
}

describe("local client updates", () => {
  let root: Root;
  let container: HTMLDivElement;
  let listener: (snapshot: LocalClientSnapshot) => void;
  let unsubscribe: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.native = true;
    mocks.get.mockResolvedValue(available());
    mocks.refresh.mockResolvedValue(available());
    mocks.update.mockResolvedValue({ ...available(), revision: 2, busy: true });
    mocks.repair.mockResolvedValue({ ...available(), revision: 2, busy: true });
    unsubscribe = vi.fn();
    mocks.listen.mockImplementation(async (cb) => {
      listener = cb;
      return unsubscribe;
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });
  async function render(compact = false) {
    await act(async () =>
      root.render(<LocalClientUpdates compact={compact} />),
    );
  }
  function button(text: string) {
    return Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === text,
    )!;
  }

  it("shows versions and sends a single client or the supported update-all selection", async () => {
    await render();
    expect(container.textContent).toContain("Codex CLI");
    expect(container.textContent).toContain("Claude Code");
    expect(container.textContent).toContain("Cursor CLI");
    expect(container.querySelector('[data-client="pi"] h3')?.textContent).toBe(
      "Pi",
    );
    expect(container.textContent).toContain("1.1.0");
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="更新 Codex CLI"]')!
        .click(),
    );
    expect(mocks.update).toHaveBeenCalledWith([{ id: "codex", wsl: null }]);
    expect(button("刷新").disabled).toBe(true);
    await act(async () => listener({ ...available(), revision: 3 }));
    await act(async () => button("全部更新（4）").click());
    expect(mocks.update).toHaveBeenLastCalledWith(
      ["codex", "claude", "cursor", "pi"].map((id) => ({ id, wsl: null })),
    );
  });

  it("lists clients found in WSL by distribution and updates them there", async () => {
    const snapshot = available();
    snapshot.clients.push({
      ...snapshot.clients[1],
      wsl: "Ubuntu",
      executable: "/home/ada/.local/bin/claude",
    });
    snapshot.wsl_unchecked = ["Debian", "Arch"];
    mocks.get.mockResolvedValue(snapshot);
    await render();
    const wsl = container.querySelector('[data-client="claude"][data-wsl]');
    expect(wsl?.querySelector("h3")?.textContent).toBe("Claude Code");
    expect(wsl?.textContent).toContain("WSL · Ubuntu");
    expect(container.textContent).toContain(
      "Debian、Arch 未运行，没有检测其中的客户端。",
    );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[aria-label="更新 Claude Code（WSL · Ubuntu）"]',
        )!
        .click(),
    );
    expect(mocks.update).toHaveBeenCalledWith([
      { id: "claude", wsl: "Ubuntu" },
    ]);
    await act(async () => listener({ ...snapshot, revision: 3 }));
    await act(async () => button("全部更新（5）").click());
    expect(mocks.update).toHaveBeenLastCalledWith([
      ...["codex", "claude", "cursor", "pi"].map((id) => ({ id, wsl: null })),
      { id: "claude", wsl: "Ubuntu" },
    ]);
  });

  it("subscribes before the initial check and ignores stale results", async () => {
    mocks.get.mockResolvedValue(emptyLocalClients());
    let resolve!: (snapshot: LocalClientSnapshot) => void;
    mocks.refresh.mockImplementation(
      () =>
        new Promise<LocalClientSnapshot>((r) => {
          resolve = r;
        }),
    );
    await render();
    expect(mocks.refresh).toHaveBeenCalledOnce();
    await act(async () =>
      listener({ ...available(), revision: 5, busy: true }),
    );
    await act(async () => resolve(available()));
    expect(button("刷新").disabled).toBe(true);
  });

  it("reads the continuing host task after leaving and returning to the page", async () => {
    await render();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="更新 Codex CLI"]')!
        .click(),
    );
    await act(async () => root.render(<div>another page</div>));
    expect(unsubscribe).toHaveBeenCalledOnce();
    const progress = { ...available(), revision: 3, busy: true };
    progress.clients[0].phase = "updating";
    progress.clients[0].can_update = false;
    mocks.get.mockResolvedValue(progress);
    await render();
    expect(container.textContent).toContain("更新中…");
    expect(mocks.update).toHaveBeenCalledOnce();
    await act(async () =>
      listener({
        ...progress,
        revision: 4,
        busy: false,
        clients: progress.clients.map((c) => ({
          ...c,
          phase: "updated",
          current_version: "1.1.0",
          can_update: false,
        })),
      }),
    );
    expect(container.textContent).toContain("更新成功");
    expect(container.textContent).toContain("重新打开客户端");
  });

  it("offers retry for failures and manual guidance for unsupported or missing installations", async () => {
    const snapshot = available();
    snapshot.clients[0] = {
      ...snapshot.clients[0],
      phase: "error",
      error_code: "network",
      error_detail: "offline",
      can_update: false,
    };
    snapshot.clients[1] = {
      ...snapshot.clients[1],
      phase: "manual",
      can_update: false,
    };
    snapshot.clients[2] = {
      ...snapshot.clients[2],
      phase: "error",
      error_code: "damaged",
      error_detail: "C:\\npm\\cursor-agent.exe is not a Windows program",
      can_update: false,
    };
    mocks.get.mockResolvedValue(snapshot);
    await render();
    expect(container.textContent).toContain("请检查网络和代理设置后重试");
    expect(
      container.querySelector('[data-client="cursor"] [role="alert"]')
        ?.textContent,
    ).toBe("安装不完整或文件已损坏，无法启动。请重新安装后重试。");
    await act(async () => button("手动更新").click());
    expect(mocks.open).toHaveBeenCalledWith(
      "https://code.claude.com/docs/en/setup",
    );
    await act(async () => button("重试").click());
    expect(mocks.refresh).toHaveBeenCalledOnce();
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("collapses beside app release notes and disables native actions in browser preview", async () => {
    mocks.native = false;
    mocks.get.mockResolvedValue(emptyLocalClients());
    await render(true);
    expect(
      container.querySelector('button[aria-expanded="false"]'),
    ).not.toBeNull();
    await act(async () => button("本地客户端").click());
    expect(button("刷新").disabled).toBe(true);
    expect(container.textContent).toContain("请在桌面应用中");
    expect(mocks.refresh).not.toHaveBeenCalled();
  });

  it.each([null, "Ubuntu-24.04"])(
    "explains permission failures in the affected environment (%s)",
    async (wsl) => {
      const snapshot = available();
      const failed = {
        ...snapshot.clients[0],
        wsl,
        phase: "error" as const,
        install_method: "npm" as const,
        executable: "/usr/bin/codex",
        error_code: "permission",
        error_detail: "npm error code EACCES",
        can_update: false,
      };
      if (wsl === null) snapshot.clients[0] = failed;
      else snapshot.clients.push(failed);
      mocks.get.mockResolvedValue(snapshot);
      await render();
      const alert = container.querySelector('[role="alert"]');
      expect(alert?.textContent).toContain("没有读写客户端文件或缓存的权限");
      expect(alert?.textContent).toContain(
        wsl === null ? "本机" : "WSL · Ubuntu-24.04",
      );
      expect(container.textContent).toContain("npm error code EACCES");
      await act(async () => button("重试").click());
      expect(mocks.refresh).toHaveBeenCalledOnce();
      expect(mocks.update).not.toHaveBeenCalled();
    },
  );

  it("explains each damaged installation and repairs the selected client", async () => {
    const snapshot = available();
    const damaged = {
      phase: "damaged" as const,
      install_method: "npm" as const,
      can_update: false,
      error_detail: "codex.cmd is missing",
    };
    snapshot.clients[0] = {
      ...snapshot.clients[0],
      ...damaged,
      repair: "restore",
      previous_version: "0.40.0",
    };
    snapshot.clients[1] = {
      ...snapshot.clients[1],
      ...damaged,
      repair: "reinstall",
    };
    snapshot.clients[2] = {
      ...snapshot.clients[2],
      ...damaged,
      install_method: "native",
    };
    snapshot.clients.push({
      ...snapshot.clients[0],
      wsl: "Ubuntu",
      previous_version: null,
    });
    mocks.get.mockResolvedValue(snapshot);
    mocks.repair.mockResolvedValue({ ...snapshot, revision: 2, busy: true });
    await render();
    const card = (selector: string) => container.querySelector(selector)!;
    const codex = card('[data-client="codex"]:not([data-wsl])');
    const claude = card('[data-client="claude"]');
    const cursor = card('[data-client="cursor"]');
    const wsl = card('[data-client="codex"][data-wsl="Ubuntu"]');
    expect(codex.textContent).toContain("无法启动");
    expect(codex.querySelector('[role="alert"]')?.textContent).toBe(
      "上次更新没有完成，客户端无法启动。点「修复」恢复到 0.40.0 版本，无需下载。",
    );
    expect(wsl.querySelector('[role="alert"]')?.textContent).toBe(
      "上次更新没有完成，客户端无法启动。点「修复」恢复到原来的版本，无需下载。",
    );
    expect(claude.querySelector('[role="alert"]')?.textContent).toBe(
      "安装不完整，客户端无法启动。点「修复」重新下载并安装最新版本。",
    );
    expect(cursor.querySelector('[role="alert"]')?.textContent).toBe(
      "安装不完整，客户端无法启动。请点「如何安装」，按说明重新安装。",
    );
    // npm installations show the reinstall command; Cursor has no npm package.
    expect(codex.textContent).toContain("手动重新安装");
    expect(codex.textContent).toContain("npm install -g @openai/codex");
    expect(claude.textContent).toContain(
      "npm install -g @anthropic-ai/claude-code",
    );
    expect(cursor.textContent).not.toContain("手动重新安装");
    expect(cursor.querySelector('[aria-label^="修复"]')).toBeNull();
    await act(async () =>
      Array.from(cursor.querySelectorAll("button"))
        .find((b) => b.textContent === "如何安装")!
        .click(),
    );
    expect(mocks.open).toHaveBeenCalledWith(
      "https://cursor.com/docs/cli/installation",
    );
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[aria-label="修复 Codex CLI（WSL · Ubuntu）"]',
        )!
        .click(),
    );
    expect(mocks.repair).toHaveBeenCalledWith([{ id: "codex", wsl: "Ubuntu" }]);
    expect(mocks.update).not.toHaveBeenCalled();
    expect(
      container.querySelector<HTMLButtonElement>(
        '[aria-label="修复 Claude Code"]',
      )?.disabled,
    ).toBe(true);
    const repairing = { ...snapshot, revision: 3, busy: true };
    repairing.clients = snapshot.clients.map((c, i) =>
      i === 1 ? { ...c, phase: "repairing" } : c,
    );
    await act(async () => listener(repairing));
    expect(button("修复中…").disabled).toBe(true);
    await act(async () =>
      listener({
        ...repairing,
        revision: 4,
        busy: false,
        clients: repairing.clients.map((c, i) =>
          i === 1
            ? {
                ...c,
                phase: "repaired",
                current_version: "1.1.0",
                repair: null,
                error_detail: null,
              }
            : c,
        ),
      }),
    );
    expect(claude.textContent).toContain("修复成功");
    expect(claude.textContent).toContain("已修复，重新打开客户端即可使用。");
    expect(claude.querySelector('[role="alert"]')).toBeNull();
  });

  it("explains a running client, an unfinished download and a concurrent update", async () => {
    const snapshot = available();
    const failed = { phase: "error" as const, can_update: false };
    snapshot.clients[0] = {
      ...snapshot.clients[0],
      ...failed,
      error_code: "running",
      error_detail: "4242 C:\\npm\\codex.exe",
    };
    snapshot.clients[1] = {
      ...snapshot.clients[1],
      ...failed,
      error_code: "incomplete",
      kept_previous: true,
    };
    snapshot.clients[2] = {
      ...snapshot.clients[2],
      ...failed,
      error_code: "busy",
    };
    mocks.get.mockResolvedValue(snapshot);
    await render();
    const alert = (id: string) =>
      container.querySelector(`[data-client="${id}"] [role="alert"]`)
        ?.textContent;
    expect(alert("codex")).toBe(
      "客户端正在运行，没有更新。请先退出客户端，再刷新并更新。",
    );
    expect(alert("claude")).toBe(
      "新版本没有下载完整，常见原因是网络中断或代理。请检查网络后重试。",
    );
    expect(alert("cursor")).toBe(
      "另一个 AstrLink 正在更新这个客户端，请稍后刷新。",
    );
    // Only the client whose update failed after changing nothing visible
    // says which version it still runs.
    expect(
      container.querySelector('[data-client="claude"]')?.textContent,
    ).toContain("仍在使用原来的 1.0.0 版本。");
    expect(container.textContent?.split("仍在使用").length).toBe(2);
  });

  it("leaves scrolling to the About page", async () => {
    await render();
    expect(container.querySelector('[data-client="codex"]')).not.toBeNull();
    expect(container.querySelector('[class*="overscroll-"]')).toBeNull();
  });
});
