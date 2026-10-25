// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { applyLocale, i18n } from "./i18n";
import type { RawSealingState } from "./raw-sealing-model";

const mocks = vi.hoisted(() => ({
  acknowledgeRawKey: vi.fn(),
  setRawPassword: vi.fn(),
  unlockRaw: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
  toastWarning: vi.fn(),
}));

// Also stands in for `@/bridge`, which the proof dialog imports.
vi.mock("./bridge", () => ({
  acknowledgeRawKey: mocks.acknowledgeRawKey,
  setRawPassword: mocks.setRawPassword,
  unlockRaw: mocks.unlockRaw,
}));
vi.mock("sonner", () => ({
  toast: {
    error: mocks.toastError,
    success: mocks.toastSuccess,
    warning: mocks.toastWarning,
  },
}));

import {
  RawKeyReplacedGate,
  RawPasswordPanel,
  RawSealingDialogs,
  rawPasswordMissing,
  unlockIdleMinutes,
  type RawDialog,
} from "./RawSealingControls";

let container: HTMLDivElement;
let root: Root;

function sealing(overrides: Partial<RawSealingState> = {}): RawSealingState {
  return {
    raw_available: true,
    configured: true,
    password_set: true,
    // No raw password protects the key.
    password_required: overrides.password_set === false,
    envelopes: ["password"],
    key_verified: true,
    unlocked: false,
    unlock_expires_at: null,
    unlock_idle_seconds: 900,
    retry_after_seconds: 0,
    password_min_length: 8,
    password_max_length: 128,
    key_replaced: false,
    ...overrides,
  };
}

const unconfigured = sealing({
  raw_available: false,
  configured: false,
  password_set: false,
  envelopes: [],
  key_verified: false,
});

function sealed(
  status: Partial<RawSealingState> = {},
  reset: { deleted_parts: number; affected_records: number } | null = null,
) {
  return { outcome: "sealing", status: sealing(status), reset };
}

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  for (const mock of Object.values(mocks)) mock.mockReset();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  await applyLocale("zh-CN");
});

function button(
  label: string,
  scope: ParentNode = document,
): HTMLButtonElement {
  const match = [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

function queryButton(label: string, scope: ParentNode = document) {
  return (
    [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
      (candidate) => candidate.textContent?.trim() === label,
    ) ?? null
  );
}

function dialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>(
    '[data-slot="proof-confirm-dialog"]',
  );
}

/** The plain confirmation, such as the destructive one before a reset. */
function confirmDialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>(
    '[data-slot="alert-dialog-content"]',
  );
}

function newPasswordInputs(): HTMLInputElement[] {
  return [
    ...document.querySelectorAll<HTMLInputElement>(
      'input[autocomplete="new-password"]',
    ),
  ];
}

function proofPasswordInput(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>(
    'input[type="password"][autocomplete="current-password"]',
  );
}

async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function typeNewPassword(password: string, confirmation = password) {
  const [first, second] = newPasswordInputs();
  await type(first, password);
  await type(second, confirmation);
}

async function click(target: HTMLElement) {
  await act(async () => {
    target.click();
    await Promise.resolve();
  });
  await act(async () => {});
}

describe("raw sealing helpers", () => {
  it("rounds the idle lock to whole minutes", () => {
    expect(unlockIdleMinutes(sealing())).toBe(15);
    expect(unlockIdleMinutes(sealing({ unlock_idle_seconds: 20 }))).toBe(1);
  });

  it("misses protection until a raw password protects raw content", () => {
    expect(rawPasswordMissing(unconfigured)).toBe(true);
    expect(rawPasswordMissing(sealing())).toBe(false);
    expect(rawPasswordMissing(null)).toBe(false);
  });
});

describe("RawPasswordPanel", () => {
  async function renderPanel(
    props: Partial<Parameters<typeof RawPasswordPanel>[0]> = {},
  ) {
    const onAction = vi.fn();
    const onAgentAccessChange = vi.fn();
    await act(async () =>
      root.render(
        <RawPasswordPanel
          agentAccess
          busy={false}
          error={null}
          onAction={onAction}
          onAgentAccessChange={onAgentAccessChange}
          status={sealing()}
          {...props}
        />,
      ),
    );
    return { onAction, onAgentAccessChange };
  }

  function panel(): HTMLElement {
    const match = container.querySelector<HTMLElement>(
      '[data-slot="raw-password-panel"]',
    );
    if (!match) throw new Error("Missing raw password panel");
    return match;
  }

  function agentSwitch(): HTMLButtonElement {
    const match = panel().querySelector<HTMLButtonElement>('[role="switch"]');
    if (!match) throw new Error("Missing agent access switch");
    return match;
  }

  it("offers change and reset once a password is set", async () => {
    const { onAction, onAgentAccessChange } = await renderPanel();

    expect(
      panel().querySelector('[data-slot="raw-password-state"]')?.textContent,
    ).toBe("口令");
    expect(panel().textContent).toContain("输入口令");
    await click(button("修改", panel()));
    expect(onAction).toHaveBeenLastCalledWith("change");
    await click(button("重置", panel()));
    expect(onAction).toHaveBeenLastCalledWith("reset");

    expect(agentSwitch().disabled).toBe(false);
    expect(agentSwitch().getAttribute("aria-checked")).toBe("true");
    await click(agentSwitch());
    expect(onAgentAccessChange).toHaveBeenCalledWith(false);
  });

  it("asks for protection whenever none is set, capture or not", async () => {
    const { onAction } = await renderPanel({ status: unconfigured });

    expect(
      panel().querySelector('[data-slot="raw-password-state"]')?.textContent,
    ).toBe("未设置");
    const missing = panel().querySelector<HTMLElement>(
      '[data-slot="raw-password-missing"]',
    );
    expect(missing?.textContent).toContain("新请求的原文不会保存");
    expect(queryButton("设置", panel())).toBeNull();
    await click(button("开始设置", missing!));
    expect(onAction).toHaveBeenCalledWith("set");

    expect(agentSwitch().disabled).toBe(true);
    expect(agentSwitch().getAttribute("aria-checked")).toBe("false");
    expect(panel().textContent).toContain("请先设置原文保护");
  });

  it("leaves the agent toggle to pages that pass it", async () => {
    await renderPanel({ onAgentAccessChange: undefined });
    expect(panel().querySelector('[role="switch"]')).toBeNull();
  });

  it("shows a status failure instead of the hint", async () => {
    await renderPanel({ error: "offline", status: null });
    expect(panel().textContent).toContain("无法读取原文保护状态：offline");
    expect(
      panel().querySelector('[data-slot="raw-password-state"]')?.textContent,
    ).toBe("未知");
    expect(agentSwitch().disabled).toBe(false);
  });
});

describe("RawSealingDialogs", () => {
  const onClose = vi.fn();
  const onStatus = vi.fn();

  function Harness({
    initialDialog,
    initialStatus,
  }: {
    initialDialog: RawDialog;
    initialStatus: RawSealingState;
  }) {
    const [current, setCurrent] = useState<RawDialog | null>(initialDialog);
    const [status, setStatus] = useState(initialStatus);
    return (
      <RawSealingDialogs
        dialog={current}
        onClose={(done) => {
          onClose(done);
          setCurrent(null);
        }}
        onStatus={(next) => {
          onStatus(next);
          setStatus(next);
        }}
        status={status}
      />
    );
  }

  async function renderDialogs(dialogKind: RawDialog, status: RawSealingState) {
    await act(async () =>
      root.render(
        <Harness initialDialog={dialogKind} initialStatus={status} />,
      ),
    );
    await act(async () => {});
  }

  beforeEach(() => {
    onClose.mockReset();
    onStatus.mockReset();
  });

  it("sets a first password without a proof and clears it after submit", async () => {
    mocks.setRawPassword.mockResolvedValue(sealed());
    await renderDialogs({ kind: "set" }, unconfigured);

    expect(dialog()?.textContent).toContain("保护请求原文");
    // Only the password form: no method choice, fields a password manager
    // can fill, and the hint that it may.
    expect(dialog()?.querySelector('[role="radio"]')).toBeNull();
    expect(dialog()?.querySelector("form")).not.toBeNull();
    expect(proofPasswordInput()).toBeNull();
    for (const id of ["raw-new-password", "raw-confirm-password"]) {
      const input = document.getElementById(id) as HTMLInputElement | null;
      expect(input?.getAttribute("autocomplete")).toBe("new-password");
      expect(input?.hasAttribute("data-1p-ignore")).toBe(false);
      expect(dialog()?.querySelector(`label[for="${id}"]`)).not.toBeNull();
    }
    expect(
      dialog()?.querySelector('[data-slot="raw-password-manager-hint"]')
        ?.textContent,
    ).toBe("可以用 1Password 等密码管理器生成并保存口令。");
    expect(dialog()?.textContent).toContain("口令不会保存在任何地方");
    const submit = button("设置口令并继续", dialog()!);
    expect(submit.disabled).toBe(true);

    await typeNewPassword("short");
    expect(submit.disabled).toBe(true);
    await typeNewPassword("correct horse", "correct horsf");
    expect(dialog()?.textContent).toContain("两次输入的口令不一致");
    expect(submit.disabled).toBe(true);

    await typeNewPassword("correct horse");
    expect(submit.disabled).toBe(false);
    await click(submit);

    expect(mocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(onStatus).toHaveBeenCalledWith(
      expect.objectContaining({ configured: true, password_set: true }),
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith("已设置口令");
    expect(onClose).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("keeps the dialog open and empties the fields when setting fails", async () => {
    mocks.setRawPassword.mockRejectedValue(
      new Error(
        'POST /v1/raw-sealing/password returned 409 Conflict: {"error":{"code":"raw_password_already_set"}}',
      ),
    );
    await renderDialogs({ kind: "set" }, unconfigured);

    await typeNewPassword("correct horse battery");
    await click(button("设置口令并继续", dialog()!));

    expect(dialog()?.textContent).toContain("原文口令已经设置");
    expect(newPasswordInputs().map((input) => input.value)).toEqual(["", ""]);
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.toastSuccess).not.toHaveBeenCalled();
  });

  it("changes the password with the current one as proof", async () => {
    mocks.setRawPassword
      .mockResolvedValueOnce({ outcome: "password_invalid" })
      .mockResolvedValueOnce(sealed());
    await renderDialogs({ kind: "change" }, sealing());

    expect(dialog()?.textContent).toContain("当前口令");
    await type(proofPasswordInput()!, "wrong password");
    await typeNewPassword("new passphrase");
    await click(button("修改口令", dialog()!));

    expect(mocks.setRawPassword).toHaveBeenLastCalledWith(
      "change",
      "new passphrase",
      { kind: "password", password: "wrong password" },
    );
    expect(dialog()?.textContent).toContain("原文口令不正确");
    expect(proofPasswordInput()?.value).toBe("");
    expect(newPasswordInputs().map((input) => input.value)).toEqual(["", ""]);
    expect(onClose).not.toHaveBeenCalled();

    await type(proofPasswordInput()!, "old password");
    await typeNewPassword("new passphrase");
    await click(button("修改口令", dialog()!));
    expect(mocks.setRawPassword).toHaveBeenLastCalledWith(
      "change",
      "new passphrase",
      { kind: "password", password: "old password" },
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith("已修改原文口令");
    expect(onClose).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("resets behind a destructive confirmation, then sets a new password", async () => {
    mocks.setRawPassword.mockResolvedValue(
      sealed({}, { deleted_parts: 6, affected_records: 3 }),
    );
    await renderDialogs({ kind: "reset" }, sealing());

    expect(dialog()).toBeNull();
    expect(document.body.textContent).toContain("重置原文口令？");
    await click(button("重置并删除原文"));
    expect(mocks.setRawPassword).not.toHaveBeenCalled();

    expect(dialog()?.textContent).toContain("设置新的原文口令");
    expect(proofPasswordInput()).toBeNull();
    await typeNewPassword("fresh passphrase");
    await click(button("重置口令", dialog()!));

    expect(mocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "reset",
      "fresh passphrase",
      undefined,
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "已重置原文口令，删除了 3 条记录中的 6 份原文。",
    );
    expect(onClose).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("counts the deleted parts and records in English", async () => {
    await applyLocale("en");
    mocks.setRawPassword
      .mockResolvedValueOnce(
        sealed({}, { deleted_parts: 1, affected_records: 1 }),
      )
      .mockResolvedValueOnce(
        sealed({}, { deleted_parts: 6, affected_records: 3 }),
      );

    const reset = async () => {
      await renderDialogs({ kind: "reset" }, sealing());
      await click(button(i18n.t("rawSealing.resetConfirm")));
      await typeNewPassword("fresh passphrase");
      await click(button(i18n.t("rawSealing.resetAction"), dialog()!));
    };

    await reset();
    expect(mocks.toastSuccess).toHaveBeenLastCalledWith(
      "Raw password reset. Deleted 1 raw part from 1 record.",
    );

    await act(async () => root.unmount());
    root = createRoot(container);
    await reset();
    expect(mocks.toastSuccess).toHaveBeenLastCalledWith(
      "Raw password reset. Deleted 6 raw parts from 3 records.",
    );
  });

  it("closes without a change when the reset is cancelled", async () => {
    await renderDialogs({ kind: "reset" }, sealing());
    await click(button("取消"));
    expect(mocks.setRawPassword).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledExactlyOnceWith(false);
  });

  it("unlocks with the raw password and names the idle lock", async () => {
    mocks.unlockRaw.mockResolvedValue(sealed({ unlocked: true }));
    await renderDialogs({ kind: "unlock" }, sealing());

    expect(dialog()?.textContent).toContain("15 分钟无操作会自动锁定");
    expect(newPasswordInputs()).toHaveLength(0);
    await type(proofPasswordInput()!, "correct horse");
    await click(button("解锁", dialog()!));

    expect(mocks.unlockRaw).toHaveBeenCalledExactlyOnceWith({
      kind: "password",
      password: "correct horse",
    });
    expect(onStatus).toHaveBeenCalledWith(
      expect.objectContaining({ unlocked: true }),
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith("原文已解锁");
    expect(onClose).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("refuses to unlock with a bare confirmation", async () => {
    await renderDialogs({ kind: "unlock" }, unconfigured);

    await click(button("解锁", dialog()!));

    expect(mocks.unlockRaw).not.toHaveBeenCalled();
    expect(dialog()?.textContent).toContain("还没设置原文保护，无法解锁原文");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("sets the password that turning on capture needs without its own toast", async () => {
    mocks.setRawPassword.mockResolvedValue(sealed());
    await renderDialogs({ kind: "capture" }, unconfigured);

    expect(dialog()?.textContent).toContain("确认开启正文捕获");
    expect(dialog()?.textContent).toContain("开启前先设置原文保护");
    await typeNewPassword("correct horse");
    await click(button("确认开启", dialog()!));

    expect(mocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(mocks.toastSuccess).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("reports a cancelled capture confirmation", async () => {
    await renderDialogs({ kind: "capture" }, unconfigured);
    await typeNewPassword("correct horse");
    await click(button("取消", dialog()!));
    expect(mocks.setRawPassword).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledExactlyOnceWith(false);
  });
});

describe("RawKeyReplacedGate", () => {
  function Gate({ initialStatus }: { initialStatus: RawSealingState | null }) {
    const [status, setStatus] = useState(initialStatus);
    return <RawKeyReplacedGate onStatus={setStatus} status={status} />;
  }

  async function renderGate(initialStatus: RawSealingState | null) {
    await act(async () => root.render(<Gate initialStatus={initialStatus} />));
    await act(async () => {});
  }

  it("asks nothing of a device without a raw password", async () => {
    // Nothing raw is kept without one, so it waits for capture or Settings.
    await renderGate(unconfigured);
    expect(dialog()).toBeNull();
    expect(mocks.setRawPassword).not.toHaveBeenCalled();
  });

  /** A key an offline `astrlink-core raw-password` reset while the app was closed. */
  const replacedKey = sealing({ key_replaced: true });

  it("warns about a key replaced outside the desktop until its password confirms it", async () => {
    mocks.acknowledgeRawKey
      .mockResolvedValueOnce({ outcome: "password_invalid" })
      .mockResolvedValueOnce(sealed());
    await renderGate(replacedKey);

    expect(dialog()?.textContent).toContain("原文密钥在 AstrLink 之外被更换");
    expect(
      dialog()?.querySelector('[data-slot="raw-key-replaced"]')?.textContent,
    ).toContain("设置它的人可能读得到");
    expect(dialog()?.textContent).toContain("astrlink-core raw-password");
    expect(dialog()?.textContent).toContain("确认不会解锁原文");
    expect(queryButton("取消", dialog()!)).toBeNull();
    await act(async () => {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    expect(dialog()).not.toBeNull();

    await type(proofPasswordInput()!, "wrong passphrase");
    await click(button("确认是我设置的", dialog()!));
    expect(dialog()?.textContent).toContain("原文密钥在 AstrLink 之外被更换");

    await type(proofPasswordInput()!, "terminal passphrase");
    await click(button("确认是我设置的", dialog()!));
    expect(mocks.acknowledgeRawKey).toHaveBeenLastCalledWith({
      kind: "password",
      password: "terminal passphrase",
    });
    expect(mocks.acknowledgeRawKey).toHaveBeenCalledTimes(2);
    expect(mocks.setRawPassword).not.toHaveBeenCalled();
    expect(mocks.unlockRaw).not.toHaveBeenCalled();
    expect(mocks.toastSuccess).toHaveBeenCalledWith("已确认新的原文密钥");
    expect(dialog()).toBeNull();
  });

  it("resets a replaced key behind the destructive confirmation", async () => {
    mocks.setRawPassword.mockResolvedValue(
      sealed({}, { deleted_parts: 2, affected_records: 1 }),
    );
    await renderGate(replacedKey);

    await click(button("不是你设置的？重置原文密钥", dialog()!));
    expect(confirmDialog()?.textContent).toContain("重置原文口令？");
    await click(button("取消", confirmDialog()!));
    expect(dialog()?.textContent).toContain("原文密钥在 AstrLink 之外被更换");

    await click(button("不是你设置的？重置原文密钥", dialog()!));
    await click(button("重置并删除原文", confirmDialog()!));
    expect(dialog()?.textContent).toContain("设置新的原文口令");
    await click(button("返回", dialog()!));
    expect(dialog()?.textContent).toContain("原文密钥在 AstrLink 之外被更换");
    expect(mocks.setRawPassword).not.toHaveBeenCalled();

    await click(button("不是你设置的？重置原文密钥", dialog()!));
    await click(button("重置并删除原文", confirmDialog()!));
    // Nothing here opens the replaced key, so a confirmation is the proof.
    expect(proofPasswordInput()).toBeNull();
    await typeNewPassword("fresh passphrase");
    await click(button("重置口令", dialog()!));
    expect(mocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "reset",
      "fresh passphrase",
      undefined,
    );
    expect(mocks.acknowledgeRawKey).not.toHaveBeenCalled();
    expect(dialog()).toBeNull();
    expect(confirmDialog()).toBeNull();
  });

  it("stays away once a password is set, or while unknown", async () => {
    await renderGate(sealing());
    expect(dialog()).toBeNull();

    await renderGate(null);
    expect(dialog()).toBeNull();
  });
});
