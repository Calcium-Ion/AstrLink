// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  finishExitAnimations,
  installDialogAnimations,
} from "../lib/test-dialog-animations";
import { ConfirmDialog } from "./ConfirmDialog";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

function Harness({
  onCancel,
  onConfirm,
}: {
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [open, setOpen] = useState(true);
  return (
    <ConfirmDialog
      confirmLabel="确认操作"
      description="确认描述"
      onCancel={() => {
        onCancel();
        setOpen(false);
      }}
      onConfirm={() => {
        onConfirm();
        setOpen(false);
      }}
      open={open}
      title="确认标题"
    />
  );
}

type PendingAction = "install" | "uninstall";

// Like the Agent tools page: the copy comes from the state cleared on close,
// and the cleared state falls back to a different prompt.
function PendingHarness({
  onCancel,
  onConfirm,
}: {
  onCancel: () => void;
  onConfirm: (action: PendingAction) => void;
}) {
  const [pending, setPending] = useState<PendingAction | null>("uninstall");
  return (
    <ConfirmDialog
      confirmLabel={pending === "uninstall" ? "卸载" : "安装"}
      description={pending === "uninstall" ? "卸载说明" : "安装说明"}
      destructive={pending === "uninstall"}
      onCancel={() => {
        onCancel();
        setPending(null);
      }}
      onConfirm={() => {
        onConfirm(pending === "uninstall" ? "uninstall" : "install");
        setPending(null);
      }}
      open={pending !== null}
      title={pending === "uninstall" ? "卸载工具？" : "安装工具？"}
    />
  );
}

function dialogButton(label: string): HTMLButtonElement {
  const match = [
    ...document.querySelectorAll<HTMLButtonElement>("button"),
  ].find((button) => button.textContent?.trim() === label);
  if (!match) throw new Error(`Missing dialog button: ${label}`);
  return match;
}

describe("ConfirmDialog", () => {
  it("does not report a confirmation as a cancellation", async () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onConfirm={onConfirm} />),
    );

    await act(async () => dialogButton("确认操作").click());

    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("reports an explicit cancellation once", async () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    await act(async () =>
      root.render(<Harness onCancel={onCancel} onConfirm={onConfirm} />),
    );

    await act(async () => dialogButton("取消").click());

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  describe("while the exit animation plays", () => {
    let removeDialogAnimations: () => void;

    beforeEach(() => {
      removeDialogAnimations = installDialogAnimations();
    });

    afterEach(() => removeDialogAnimations());

    it("keeps showing the prompt that was cancelled", async () => {
      await act(async () =>
        root.render(<PendingHarness onCancel={vi.fn()} onConfirm={vi.fn()} />),
      );

      await act(async () => dialogButton("取消").click());

      const closing = document.querySelector('[role="alertdialog"]');
      expect(closing?.getAttribute("data-state")).toBe("closed");
      expect(closing?.textContent).toContain("卸载工具？");
      expect(closing?.textContent).toContain("卸载说明");
      expect(closing?.textContent).not.toContain("安装");
      expect(dialogButton("卸载").dataset.variant).toBe("destructive");

      await finishExitAnimations();
      expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    });

    it("ignores the confirm button of the closing frame", async () => {
      const onCancel = vi.fn();
      const onConfirm = vi.fn();
      await act(async () =>
        root.render(
          <PendingHarness onCancel={onCancel} onConfirm={onConfirm} />,
        ),
      );

      await act(async () => dialogButton("取消").click());
      await act(async () => dialogButton("卸载").click());

      expect(onCancel).toHaveBeenCalledTimes(1);
      expect(onConfirm).not.toHaveBeenCalled();
    });
  });
});
