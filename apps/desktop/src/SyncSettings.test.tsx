// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getSyncConfig: vi.fn(),
  getSyncStatus: vi.fn(),
  configureSync: vi.fn(),
  setSyncPassphrase: vi.fn(),
  forgetSyncPassphrase: vi.fn(),
  runSync: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { applyLocale } from "./i18n";
import { settleClosedLayer } from "./lib/test-layer-settle";
import { SyncSettings } from "./SyncSettings";
import type { SyncConfig } from "./sync-model";

const storedConfig: SyncConfig = {
  enabled: false,
  device_name: "",
  force_push: false,
  trigger: {
    mode: "interval",
    interval_minutes: 60,
    cron: "",
    debounce_seconds: 30,
  },
  scope: {
    services: true,
    credentials: true,
    policies: true,
    pricing: true,
    tokens: true,
  },
  remote: {
    kind: "webdav",
    endpoint: "https://dav.example.com/astrlink",
    path: "astrlink",
    region: "",
    username: "howie",
    password: "",
    access_key: "",
    secret_key: "",
    addressing: "path",
  },
  remote_password_set: true,
  remote_secret_key_set: false,
};

const status = {
  enabled: false,
  passphrase_set: true,
  remote_configured: true,
  configured: false,
  running: false,
  device_id: "device_0123456789abcdef01234567",
  device_name: "howie-desktop",
  host_name: "howie-desktop",
  pending_changes: 3,
  first_sync_pending: false,
  force_push_armed: false,
  conflicts: [],
};

function button(
  label: string,
  within: ParentNode = document,
): HTMLButtonElement {
  // Exact text: "保存" is a prefix of "保存口令", so an inclusive match would
  // click the wrong button.
  const found = [...within.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!found) throw new Error(`missing button: ${label}`);
  return found as HTMLButtonElement;
}

/** Lets the initial reads settle inside React's act. */
async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

/** React reads the tracked value, so a raw assignment is not seen as input. */
async function type(input: HTMLInputElement, value: string): Promise<void> {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function numberField(within: ParentNode): HTMLInputElement {
  const field = within.querySelector<HTMLInputElement>("input[type='number']");
  if (!field) throw new Error("missing number field");
  return field;
}

describe("SyncSettings", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getSyncConfig.mockReset().mockResolvedValue(storedConfig);
    bridge.getSyncStatus.mockReset().mockResolvedValue(status);
    bridge.configureSync.mockReset();
    bridge.setSyncPassphrase.mockReset().mockResolvedValue(true);
    bridge.forgetSyncPassphrase.mockReset().mockResolvedValue(undefined);
    bridge.runSync.mockReset().mockResolvedValue(status);
    notifyMocks.success.mockReset();
    notifyMocks.error.mockReset();
  });

  afterEach(async () => {
    await applyLocale("zh-CN");
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(): Promise<ReturnType<typeof vi.fn>> {
    const onDirtyChange = vi.fn();
    await act(async () =>
      root.render(<SyncSettings onDirtyChange={onDirtyChange} />),
    );
    await settle();
    return onDirtyChange;
  }

  it("shows that a stored remote password is kept when the field is blank", async () => {
    await render();
    expect(container.textContent).toContain("已保存。留空表示不修改。");
    const password = container.querySelector<HTMLInputElement>(
      "input[type='password']",
    );
    expect(password?.value).toBe("");
  });

  it("reports the draft as dirty and discards it back to the stored one", async () => {
    const onDirtyChange = await render();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);

    await type(numberField(container), "15");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(button("放弃修改").disabled).toBe(false);
    expect(container.textContent).toContain("同步设置尚未保存。");

    await act(async () => button("放弃修改").click());
    expect(button("保存").disabled).toBe(true);
  });

  it("refuses an interval below the engine's floor before the save", async () => {
    await render();
    await type(numberField(container), "1");
    expect(container.textContent).toContain("间隔至少需要 5 分钟。");
    expect(button("保存").disabled).toBe(true);
    expect(bridge.configureSync).not.toHaveBeenCalled();
  });

  it("saves the draft and takes the returned configuration as the baseline", async () => {
    bridge.configureSync.mockResolvedValue({ ...storedConfig, enabled: true });
    await render();
    const toggle = container.querySelector<HTMLButtonElement>("#sync-enabled");
    if (!toggle) throw new Error("missing enable switch");
    await act(async () => toggle.click());
    await act(async () => button("保存").click());
    await settle();

    // The write-only flags travel to the bridge, which drops them before the
    // control request; sync-model.test.ts covers that stripping.
    const sent = bridge.configureSync.mock.calls[0]?.[0] as SyncConfig;
    expect(sent.enabled).toBe(true);
    expect(sent.remote_password_set).toBe(true);
    expect(notifyMocks.success).toHaveBeenCalled();
    expect(button("保存").disabled).toBe(true);
  });

  it("locks a dependent group when providers are turned off", async () => {
    await render();
    const services = container.querySelector<HTMLButtonElement>(
      "#sync-scope-services",
    );
    if (!services) throw new Error("missing services switch");
    await act(async () => services.click());
    expect(
      container.querySelector<HTMLButtonElement>("#sync-scope-credentials")
        ?.disabled,
    ).toBe(true);
    expect(
      container.querySelector<HTMLButtonElement>("#sync-scope-pricing")
        ?.disabled,
    ).toBe(true);
    expect(
      container.querySelector<HTMLButtonElement>("#sync-scope-policies")
        ?.disabled,
    ).toBe(false);
    expect(container.textContent).toContain("随提供商一起同步");
  });

  it("keeps the force push switch off while sync is off", async () => {
    await render();
    const toggle =
      container.querySelector<HTMLButtonElement>("#sync-force-push");
    if (!toggle) throw new Error("missing force push switch");
    expect(toggle.disabled).toBe(true);
  });

  it("asks before arming a force push and keeps it in the draft", async () => {
    bridge.getSyncConfig.mockResolvedValue({ ...storedConfig, enabled: true });
    await render();
    const toggle =
      container.querySelector<HTMLButtonElement>("#sync-force-push");
    if (!toggle) throw new Error("missing force push switch");
    expect(toggle.disabled).toBe(false);

    await act(async () => toggle.click());
    const dialog = document.querySelector("[role='alertdialog']");
    expect(dialog?.textContent).toContain("会用本机的配置包替换远端的配置包");
    expect(container.textContent).not.toContain("保存后依然生效");

    await act(async () => {
      const confirm = dialog
        ? [...dialog.querySelectorAll("button")].find((candidate) =>
            candidate.textContent?.includes("开启强制推送"),
          )
        : undefined;
      if (!confirm) throw new Error("missing confirm button");
      confirm.click();
    });
    await settleClosedLayer();
    expect(container.textContent).toContain("保存后依然生效");
    expect(button("保存").disabled).toBe(false);

    bridge.configureSync.mockResolvedValue({
      ...storedConfig,
      enabled: true,
      force_push: true,
    });
    await act(async () => button("保存").click());
    await settle();
    const sent = bridge.configureSync.mock.calls[0]?.[0] as SyncConfig;
    expect(sent.force_push).toBe(true);
  });

  it("warns that an armed run overwrites the remote", async () => {
    bridge.getSyncStatus.mockResolvedValue({
      ...status,
      force_push_armed: true,
    });
    await render();
    expect(container.textContent).toContain(
      "已开启强制推送：下次同步会用本机配置覆盖远端。",
    );
  });

  it("says that a first sync adopts the remote", async () => {
    bridge.getSyncStatus.mockResolvedValue({
      ...status,
      first_sync_pending: true,
    });
    await render();
    expect(container.textContent).toContain("首次同步会采用远端已有的配置");
  });

  it("offers the access tokens as a group of their own", async () => {
    await render();
    expect(container.textContent).toContain("访问令牌");
    const tokens =
      container.querySelector<HTMLButtonElement>("#sync-scope-tokens");
    if (!tokens) throw new Error("missing tokens switch");
    expect(tokens.disabled).toBe(false);
    expect(tokens.getAttribute("aria-checked")).toBe("true");

    await act(async () => tokens.click());
    bridge.configureSync.mockResolvedValue({
      ...storedConfig,
      scope: { ...storedConfig.scope, tokens: false },
    });
    await act(async () => button("保存").click());
    await settle();
    const sent = bridge.configureSync.mock.calls[0]?.[0] as SyncConfig;
    expect(sent.scope.tokens).toBe(false);
  });

  it("asks for confirmation before forgetting the passphrase", async () => {
    await render();
    await act(async () => button("忘记口令").click());
    const dialog = document.querySelector("[role='alertdialog']");
    expect(dialog?.textContent).toContain("在重新输入口令前同步会暂停");
    expect(bridge.forgetSyncPassphrase).not.toHaveBeenCalled();

    await act(async () => {
      const confirm = dialog
        ? [...dialog.querySelectorAll("button")].find((candidate) =>
            candidate.textContent?.includes("忘记口令"),
          )
        : undefined;
      if (!confirm) throw new Error("missing confirm button");
      confirm.click();
    });
    await settleClosedLayer();
    expect(bridge.forgetSyncPassphrase).toHaveBeenCalledTimes(1);
    expect(document.querySelector("[role='alertdialog']")).toBeNull();
  });

  it("reports a wrong passphrase instead of the raw error", async () => {
    bridge.setSyncPassphrase.mockRejectedValue(
      new Error(
        "the remote holds a package sealed with a different sync passphrase",
      ),
    );
    await render();
    const field = container.querySelector<HTMLInputElement>(
      "input[placeholder='至少 12 个字符']",
    );
    if (!field) throw new Error("missing passphrase field");
    await type(field, "a-long-enough-passphrase");
    await act(async () => button("保存口令").click());
    await settle();
    expect(container.textContent).toContain(
      "该口令无法打开远程上已有的配置包。",
    );
  });

  it("says the build has no sync instead of showing a form", async () => {
    bridge.getSyncConfig.mockRejectedValue(new Error("sync_unavailable"));
    await render();
    expect(container.textContent).toContain("当前版本不提供配置同步。");
    expect(container.querySelector("button")).toBeNull();
  });

  it("lists a merged concurrent edit", async () => {
    bridge.getSyncStatus.mockResolvedValue({
      ...status,
      conflicts: [
        {
          kind: "service",
          name: "Anthropic",
          resolution: "remote_kept",
          at: "2026-10-11T03:00:00Z",
        },
      ],
    });
    await render();
    expect(container.textContent).toContain("Anthropic");
    expect(container.textContent).toContain("远程保留");
  });

  it("runs a sync on request and keeps the refreshed status", async () => {
    bridge.getSyncStatus.mockResolvedValue({ ...status, configured: true });
    bridge.runSync.mockResolvedValue({
      ...status,
      configured: true,
      enabled: true,
    });
    await render();
    await act(async () => button("立即同步").click());
    await settle();
    expect(bridge.runSync).toHaveBeenCalledTimes(1);
    expect(notifyMocks.success).toHaveBeenCalled();
  });

  it("explains a remote lock rather than showing the transport error", async () => {
    bridge.getSyncStatus.mockResolvedValue({ ...status, configured: true });
    bridge.runSync.mockRejectedValue(
      new Error("another device holds the remote lock"),
    );
    await render();
    await act(async () => button("立即同步").click());
    await settle();
    expect(container.textContent).toContain(
      "其他设备正持有远程锁，请稍后再试。",
    );
    expect(notifyMocks.success).not.toHaveBeenCalled();
  });
});
