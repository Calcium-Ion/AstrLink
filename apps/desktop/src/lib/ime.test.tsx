// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

type Control = HTMLInputElement | HTMLTextAreaElement;

function Harness({
  multiline = false,
  onBlur,
}: {
  multiline?: boolean;
  onBlur?: (value: string) => void;
}) {
  const [value, setValue] = useState("model-");
  const props = {
    "aria-label": "model",
    value,
    onChange: (event: { currentTarget: Control }) =>
      setValue(event.currentTarget.value),
    onBlur: (event: { currentTarget: Control }) =>
      onBlur?.(event.currentTarget.value),
  };
  return multiline ? <Textarea {...props} /> : <Input {...props} />;
}

const control = () => document.querySelector<Control>("[aria-label=model]")!;

function composition(type: string, data: string) {
  control().dispatchEvent(
    Object.assign(new Event(type, { bubbles: true }), { data }),
  );
}

/** Replaces the field text the way WebKit edits marked text, bypassing React's value tracker. */
function editText(value: string, inputType: string) {
  const element = control();
  Object.getOwnPropertyDescriptor(
    Object.getPrototypeOf(element),
    "value",
  )!.set!.call(element, value);
  element.setSelectionRange(value.length, value.length);
  element.dispatchEvent(new InputEvent("input", { bubbles: true, inputType }));
}

function composePinyin() {
  control().focus();
  control().setSelectionRange(6, 6);
  composition("compositionstart", "");
  for (const marked of ["g", "g p", "g p t"]) {
    composition("compositionupdate", marked);
    editText(`model-${marked}`, "insertCompositionText");
  }
}

describe("IME text controls", () => {
  it.each([false, true])(
    "restores raw pinyin before blur when a composition is abandoned (multiline: %s)",
    async (multiline) => {
      const onBlur = vi.fn();
      await act(async () =>
        root.render(<Harness multiline={multiline} onBlur={onBlur} />),
      );
      await act(async () => {
        composePinyin();
        // WebKit ends the composition as focus moves but keeps "g p t".
        composition("compositionend", "");
        control().blur();
      });
      expect(control().value).toBe("model-gpt");
      expect(control().selectionStart).toBe(9);
      expect(onBlur).toHaveBeenCalledExactlyOnceWith("model-gpt");
    },
  );

  it.each([
    ["a cancelled", "", "model-"],
    ["a confirmed candidate", "个平台", "model-个平台"],
    ["confirmed raw letters", "gpt", "model-gpt"],
  ])("keeps %s composition", async (_, committed, expected) => {
    await act(async () => root.render(<Harness />));
    await act(async () => {
      composePinyin();
      editText(expected, committed ? "insertFromComposition" : "");
      composition("compositionend", committed);
    });
    expect(control().value).toBe(expected);
  });

  it("keeps IME keys away from field and window shortcuts", async () => {
    const onKeyDown = vi.fn();
    const onWindowKeyDown = vi.fn();
    window.addEventListener("keydown", onWindowKeyDown);
    await act(async () =>
      root.render(<Input aria-label="model" onKeyDown={onKeyDown} />),
    );
    for (const init of [{ isComposing: true }, { keyCode: 229 }])
      control().dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true, ...init }),
      );
    expect(onKeyDown).not.toHaveBeenCalled();
    expect(onWindowKeyDown).not.toHaveBeenCalled();

    control().dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    );
    expect(onKeyDown).toHaveBeenCalledOnce();
    expect(onWindowKeyDown).toHaveBeenCalledOnce();
    window.removeEventListener("keydown", onWindowKeyDown);
  });

  it("keeps a dialog open when Escape dismisses the IME candidates", async () => {
    const onOpenChange = vi.fn();
    await act(async () =>
      root.render(
        <Dialog open onOpenChange={onOpenChange}>
          <DialogContent aria-describedby={undefined}>
            <DialogTitle>title</DialogTitle>
            <Input aria-label="model" />
          </DialogContent>
        </Dialog>,
      ),
    );
    const escape = (keyCode: number) =>
      act(async () => {
        control().dispatchEvent(
          new KeyboardEvent("keydown", {
            key: "Escape",
            keyCode,
            bubbles: true,
            cancelable: true,
          }),
        );
      });
    await escape(229);
    expect(onOpenChange).not.toHaveBeenCalled();
    await escape(27);
    expect(onOpenChange).toHaveBeenCalledExactlyOnceWith(false);
  });
});
