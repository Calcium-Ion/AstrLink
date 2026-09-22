// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
});
