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
    expect(mocks.update).toHaveBeenCalledWith(["codex"]);
    expect(button("刷新").disabled).toBe(true);
    await act(async () => listener({ ...available(), revision: 3 }));
    await act(async () => button("全部更新（4）").click());
    expect(mocks.update).toHaveBeenLastCalledWith([
      "codex",
      "claude",
      "cursor",
      "pi",
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
});
