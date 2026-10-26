// @vitest-environment happy-dom

import { act, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const exitSnapshots = vi.hoisted(() => [] as unknown[]);
vi.mock("@/lib/exit-snapshot", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/exit-snapshot")>();
  return {
    useExitSnapshot: <T,>(value: T, open: boolean): T => {
      exitSnapshots.push(value);
      return actual.useExitSnapshot(value, open);
    },
  };
});

import {
  ProofConfirmDialog,
  type ProofInput,
  type ProofMode,
  type ProofResult,
} from "./ProofConfirmDialog";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  vi.useRealTimers();
  exitSnapshots.length = 0;
  await act(async () => root.unmount());
  container.remove();
});

type Submit = (actionId: string, proof: ProofInput) => Promise<ProofResult>;

function Harness({
  biometricLabel,
  dismissable,
  onCancel = vi.fn(),
  onSubmit,
  proof = "password",
  submitDisabled,
  withField = false,
}: {
  biometricLabel?: string;
  dismissable?: boolean;
  onCancel?: () => void;
  onSubmit: Submit;
  proof?: ProofMode;
  submitDisabled?: boolean;
  withField?: boolean;
}) {
  const [open, setOpen] = useState(true);
  const fieldRef = useRef<HTMLInputElement>(null);
  return (
    <ProofConfirmDialog
      actions={[
        { id: "once", label: "仅本次" },
        { id: "window_15m", label: "15 分钟内" },
      ]}
      biometricLabel={biometricLabel}
      cancelLabel="拒绝"
      description={<p>申请说明</p>}
      dismissable={dismissable}
      fields={
        withField ? (
          <input aria-label="附加字段" data-slot="extra" ref={fieldRef} />
        ) : null
      }
      initialFocusRef={withField ? fieldRef : undefined}
      onCancel={() => {
        onCancel();
        setOpen(false);
      }}
      onSubmit={async (actionId, input) => {
        const result = await onSubmit(actionId, input);
        if (result.kind === "done") setOpen(false);
        return result;
      }}
      open={open}
      proof={proof}
      submitDisabled={submitDisabled}
      title="申请标题"
    >
      <p data-slot="facts">申请事实</p>
    </ProofConfirmDialog>
  );
}

function dialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[role="alertdialog"]');
}

function button(label: string): HTMLButtonElement {
  const match = [
    ...document.querySelectorAll<HTMLButtonElement>("button"),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

function passwordInput(): HTMLInputElement {
  const input = document.querySelector<HTMLInputElement>(
    'input[type="password"]',
  );
  if (!input) throw new Error("Missing password input");
  return input;
}

async function typePassword(value: string) {
  const input = passwordInput();
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function pressEnter() {
  const form = dialog()?.querySelector("form");
  if (!form) throw new Error("Missing form");
  await act(async () => {
    form.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
}

describe("ProofConfirmDialog", () => {
  it("focuses the password and keeps approvals disabled until one is typed", async () => {
    await act(async () => root.render(<Harness onSubmit={vi.fn<Submit>()} />));

    expect(dialog()?.textContent).toContain("申请标题");
    expect(dialog()?.textContent).toContain("申请事实");
    expect(document.activeElement).toBe(passwordInput());
    // A password manager can fill the current password into its fixed id.
    expect(passwordInput().autocomplete).toBe("current-password");
    expect(passwordInput().id).toBe("raw-current-password");
    expect(
      document.querySelector('label[for="raw-current-password"]'),
    ).not.toBeNull();
    expect(passwordInput().hasAttribute("maxlength")).toBe(false);
    expect(button("仅本次").disabled).toBe(true);
    expect(button("15 分钟内").disabled).toBe(true);
    expect(button("拒绝").disabled).toBe(false);

    await typePassword("correct horse");

    expect(button("仅本次").disabled).toBe(false);
    expect(button("15 分钟内").disabled).toBe(false);
  });

  it("counts the password limit in characters, not UTF-16 units", async () => {
    const onSubmit = vi.fn<Submit>();
    await act(async () => root.render(<Harness onSubmit={onSubmit} />));

    await typePassword("🔑".repeat(128));
    expect(passwordInput().value).toHaveLength(256);
    expect(passwordInput().getAttribute("aria-invalid")).toBeNull();
    expect(button("仅本次").disabled).toBe(false);

    await typePassword("🔑".repeat(129));
    expect(passwordInput().getAttribute("aria-invalid")).toBe("true");
    expect(dialog()?.textContent).toContain("口令不能超过 128 个字符。");
    expect(button("仅本次").disabled).toBe(true);
    await pressEnter();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("keeps no fields or callbacks in the closing snapshot", async () => {
    const onSubmit = vi.fn<Submit>().mockResolvedValue({ kind: "done" });
    await act(async () =>
      root.render(<Harness onSubmit={onSubmit} proof="confirm" withField />),
    );
    await act(async () => button("仅本次").click());

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(exitSnapshots.length).toBeGreaterThan(0);
    for (const snapshot of exitSnapshots) {
      expect(snapshot).not.toHaveProperty("fields");
      const kept = Object.values(snapshot as Record<string, unknown>);
      expect(kept.some((value) => typeof value === "function")).toBe(false);
    }
  });

  it("submits the default action on Enter and forgets the password", async () => {
    const onSubmit = vi
      .fn<Submit>()
      .mockResolvedValue({ kind: "password_invalid" });
    await act(async () => root.render(<Harness onSubmit={onSubmit} />));
    await typePassword("correct horse");

    await pressEnter();

    expect(onSubmit).toHaveBeenCalledExactlyOnceWith("once", {
      kind: "password",
      password: "correct horse",
    });
    expect(passwordInput().value).toBe("");
    expect(dialog()?.textContent).toContain("原文口令不正确。");
    expect(button("仅本次").disabled).toBe(true);
  });

  it("passes the chosen action and closes once it is done", async () => {
    const onSubmit = vi.fn<Submit>().mockResolvedValue({ kind: "done" });
    const onCancel = vi.fn();
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onSubmit={onSubmit} />),
    );
    await typePassword("correct horse");

    await act(async () => button("15 分钟内").click());

    expect(onSubmit).toHaveBeenCalledExactlyOnceWith("window_15m", {
      kind: "password",
      password: "correct horse",
    });
    expect(onCancel).not.toHaveBeenCalled();
    expect(dialog()?.getAttribute("data-state") ?? "closed").toBe("closed");
  });

  it("ignores Enter while no password is typed", async () => {
    const onSubmit = vi.fn<Submit>();
    await act(async () => root.render(<Harness onSubmit={onSubmit} />));

    await pressEnter();

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("counts down a backoff with every approval disabled", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
    const onSubmit = vi
      .fn<Submit>()
      .mockResolvedValueOnce({ kind: "backoff", retryAfterSeconds: 2 })
      .mockResolvedValueOnce({ kind: "done" });
    await act(async () => root.render(<Harness onSubmit={onSubmit} />));
    await typePassword("correct horse");
    await act(async () => button("仅本次").click());

    const backoff = () =>
      document.querySelector('[data-slot="proof-backoff"]')?.textContent;
    expect(backoff()).toContain("2 秒后重试");
    await typePassword("correct horse");
    expect(button("仅本次").disabled).toBe(true);
    expect(button("15 分钟内").disabled).toBe(true);
    expect(button("拒绝").disabled).toBe(false);

    await pressEnter();
    expect(onSubmit).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTime(1_250));
    expect(backoff()).toContain("1 秒后重试");

    await act(async () => vi.advanceTimersByTime(1_000));
    expect(backoff()).toBeUndefined();
    expect(button("仅本次").disabled).toBe(false);

    await act(async () => button("仅本次").click());
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it("reports a failed submission and keeps the dialog open", async () => {
    const onSubmit = vi
      .fn<Submit>()
      .mockRejectedValueOnce(new Error("核心未响应"));
    await act(async () => root.render(<Harness onSubmit={onSubmit} />));
    await typePassword("correct horse");

    await act(async () => button("仅本次").click());

    expect(dialog()?.getAttribute("data-state")).toBe("open");
    expect(dialog()?.textContent).toContain("核心未响应");
    expect(passwordInput().value).toBe("");
  });

  it("treats the cancel button and Escape as a cancellation", async () => {
    const onCancel = vi.fn();
    const onSubmit = vi.fn<Submit>();
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onSubmit={onSubmit} />),
    );

    await act(async () => button("拒绝").click());
    expect(onCancel).toHaveBeenCalledTimes(1);

    await act(async () => root.render(<></>));
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onSubmit={onSubmit} />),
    );
    await act(async () => {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });

    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("offers no way out but the action when it is not dismissable", async () => {
    const onCancel = vi.fn();
    const onSubmit = vi
      .fn<Submit>()
      .mockResolvedValueOnce({ kind: "error", message: "失败" })
      .mockResolvedValueOnce({ kind: "done" });
    await act(async () =>
      root.render(
        <Harness
          dismissable={false}
          onCancel={onCancel}
          onSubmit={onSubmit}
          proof="confirm"
        />,
      ),
    );

    expect(() => button("拒绝")).toThrow();
    await act(async () => {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await act(async () => {
      document
        .querySelector('[data-slot="alert-dialog-overlay"]')
        ?.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    });
    expect(onCancel).not.toHaveBeenCalled();
    expect(dialog()).not.toBeNull();

    // A failed attempt keeps it up too; only a done action closes it.
    await act(async () => button("仅本次").click());
    expect(dialog()?.textContent).toContain("失败");
    await act(async () => button("仅本次").click());
    expect(dialog()).toBeNull();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("cannot be cancelled while a submission is in flight", async () => {
    let finish: (result: ProofResult) => void = () => {};
    const onSubmit = vi.fn<Submit>(
      () =>
        new Promise<ProofResult>((resolve) => {
          finish = resolve;
        }),
    );
    const onCancel = vi.fn();
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onSubmit={onSubmit} />),
    );
    await typePassword("correct horse");
    await act(async () => button("仅本次").click());

    expect(button("处理中…").disabled).toBe(true);
    expect(button("拒绝").disabled).toBe(true);
    await act(async () => {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    expect(onCancel).not.toHaveBeenCalled();

    await act(async () => finish({ kind: "done" }));
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("offers Touch ID for the first action beside the password", async () => {
    const onSubmit = vi
      .fn<Submit>()
      .mockResolvedValueOnce({ kind: "error", message: "Touch ID 已取消。" })
      .mockResolvedValueOnce({ kind: "done" });
    await act(async () =>
      root.render(
        <Harness biometricLabel="使用 Touch ID" onSubmit={onSubmit} />,
      ),
    );

    // Touch ID needs no typed password; the password still fills in.
    expect(button("仅本次").disabled).toBe(true);
    expect(button("使用 Touch ID").disabled).toBe(false);
    await act(async () => button("使用 Touch ID").click());
    expect(onSubmit).toHaveBeenLastCalledWith("once", { kind: "biometric" });
    expect(dialog()?.textContent).toContain("Touch ID 已取消。");

    await typePassword("correct horse");
    await pressEnter();
    expect(onSubmit).toHaveBeenLastCalledWith("once", {
      kind: "password",
      password: "correct horse",
    });
  });

  it("offers no Touch ID where no password is asked for", async () => {
    await act(async () =>
      root.render(
        <Harness
          biometricLabel="使用 Touch ID"
          onSubmit={vi.fn<Submit>()}
          proof="confirm"
        />,
      ),
    );
    expect(document.querySelector('[data-slot="proof-biometric"]')).toBeNull();
  });

  it("asks for a plain confirmation without a password", async () => {
    const onSubmit = vi.fn<Submit>().mockResolvedValue({ kind: "done" });
    await act(async () =>
      root.render(<Harness onSubmit={onSubmit} proof="confirm" />),
    );

    expect(document.querySelector('input[type="password"]')).toBeNull();
    expect(document.activeElement).toBe(button("仅本次"));
    expect(button("仅本次").disabled).toBe(false);

    await pressEnter();

    expect(onSubmit).toHaveBeenCalledExactlyOnceWith("once", {
      kind: "confirm",
    });
  });

  it("keeps actions disabled while the caller's fields are invalid", async () => {
    const onSubmit = vi.fn<Submit>();
    await act(async () =>
      root.render(
        <Harness
          onSubmit={onSubmit}
          proof="confirm"
          submitDisabled
          withField
        />,
      ),
    );

    expect(document.activeElement?.getAttribute("data-slot")).toBe("extra");
    expect(button("仅本次").disabled).toBe(true);
    await pressEnter();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("disables the caller's fields while a submission is in flight", async () => {
    let finish: (result: ProofResult) => void = () => {};
    const onSubmit = vi.fn<Submit>(
      () =>
        new Promise<ProofResult>((resolve) => {
          finish = resolve;
        }),
    );
    await act(async () =>
      root.render(<Harness onSubmit={onSubmit} withField />),
    );
    // Browsers disable every control inside a disabled fieldset; happy-dom
    // does not, so this reads the fieldset that holds the field.
    const fieldset = () =>
      document
        .querySelector('[data-slot="extra"]')
        ?.closest<HTMLFieldSetElement>("fieldset");
    expect(fieldset()?.disabled).toBe(false);

    await typePassword("correct horse");
    await act(async () => button("仅本次").click());
    expect(passwordInput().disabled).toBe(true);
    expect(fieldset()?.disabled).toBe(true);

    await act(async () => finish({ kind: "error", message: "网络中断" }));
    expect(fieldset()?.disabled).toBe(false);
    expect(passwordInput().disabled).toBe(false);
  });
});
