// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DialogPicker, PickerDialog } from "./DialogPicker";
import { settleClosedLayer } from "../lib/test-layer-settle";

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
  onValueChange,
}: {
  onValueChange: (value: string) => void;
}) {
  const [value, setValue] = useState("beta");
  return (
    <DialogPicker
      aria-label="类型"
      description="保存后无法更改。"
      groups={[
        {
          label: "第一组",
          options: [
            { value: "alpha", label: "Alpha", description: "alpha.example" },
            { value: "beta", label: "Beta" },
          ],
        },
        { label: "空组", options: [] },
        { label: "第二组", options: [{ value: "gamma", label: "Gamma" }] },
      ]}
      onValueChange={(next) => {
        onValueChange(next);
        setValue(next);
      }}
      title="选择类型"
      value={value}
      valueLabel={value}
    />
  );
}

function card(label: string): HTMLButtonElement {
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find(
    (candidate) =>
      candidate.querySelector('[data-slot="dialog-picker-label"]')
        ?.textContent === label,
  );
  if (!found) throw new Error(`Missing card: ${label}`);
  return found;
}

async function openPicker(): Promise<void> {
  await act(async () => {
    container.querySelector<HTMLButtonElement>('[aria-label="类型"]')?.click();
    await Promise.resolve();
  });
}

describe("DialogPicker", () => {
  it("groups options and focuses the current choice when opened", async () => {
    await act(async () => root.render(<Harness onValueChange={() => {}} />));
    await openPicker();

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("选择类型");
    expect(
      [...(dialog?.querySelectorAll("section") ?? [])].map((section) =>
        section.getAttribute("aria-label"),
      ),
    ).toEqual(["第一组", "第二组"]);
    expect(card("Beta").getAttribute("aria-current")).toBe("true");
    expect(card("Alpha").hasAttribute("aria-current")).toBe(false);
    expect(document.activeElement).toBe(card("Beta"));
  });

  it("closes on selection and reports only a changed value", async () => {
    const onValueChange = vi.fn();
    await act(async () =>
      root.render(<Harness onValueChange={onValueChange} />),
    );
    await openPicker();
    await act(async () => card("Beta").click());
    expect(onValueChange).not.toHaveBeenCalled();

    await openPicker();
    await act(async () => card("Gamma").click());
    expect(onValueChange).toHaveBeenCalledWith("gamma");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(
      container.querySelector('[aria-label="类型"]')?.textContent,
    ).toContain("gamma");
  });

  it("starts a fresh pick without a trigger and returns focus on cancel", async () => {
    const onValueChange = vi.fn();
    function FreshPick() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">
            新建
          </button>
          <PickerDialog
            groups={[
              { label: "全部", options: [{ value: "alpha", label: "Alpha" }] },
            ]}
            onOpenChange={setOpen}
            onValueChange={onValueChange}
            open={open}
            title="选择类型"
          />
        </>
      );
    }
    await act(async () => root.render(<FreshPick />));
    const opener = container.querySelector("button")!;
    opener.focus();
    await act(async () => {
      opener.click();
      await Promise.resolve();
    });
    expect(card("Alpha").hasAttribute("aria-current")).toBe(false);

    await act(async () => {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    // Radix restores focus on a timer after the dialog unmounts.
    await settleClosedLayer();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(onValueChange).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(opener);

    await act(async () => {
      opener.click();
      await Promise.resolve();
    });
    await act(async () => card("Alpha").click());
    expect(onValueChange).toHaveBeenCalledWith("alpha");
  });
});
