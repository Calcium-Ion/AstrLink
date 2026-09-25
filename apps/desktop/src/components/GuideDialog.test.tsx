// @vitest-environment happy-dom

import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import {
  finishExitAnimations,
  installDialogAnimations,
} from "../lib/test-dialog-animations";
import { GuideDialog } from "./GuideDialog";

let container: HTMLDivElement;
let root: Root;
let removeDialogAnimations: () => void;

beforeEach(() => {
  removeDialogAnimations = installDialogAnimations();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  removeDialogAnimations();
});

function Demo({ onMount }: { onMount: () => void }) {
  useEffect(() => onMount(), [onMount]);
  return <p>演示内容</p>;
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) =>
      candidate.getAttribute("aria-label") === label ||
      candidate.textContent?.trim() === label,
  );
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

it("keeps the demo through the closing animation and restarts it on reopen", async () => {
  const onMount = vi.fn();
  await act(async () =>
    root.render(
      <GuideDialog
        description="演示说明"
        dismissLabel="知道了"
        replayLabel="重新播放"
        title="演示标题"
        triggerLabel="查看演示"
      >
        <Demo onMount={onMount} />
      </GuideDialog>,
    ),
  );

  await act(async () => button("查看演示").click());
  expect(onMount).toHaveBeenCalledTimes(1);

  await act(async () => button("知道了").click());
  const closing = document.querySelector('[role="dialog"]');
  expect(closing?.getAttribute("data-state")).toBe("closed");
  expect(closing?.textContent).toContain("演示内容");

  await finishExitAnimations();
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await act(async () => button("查看演示").click());
  expect(onMount).toHaveBeenCalledTimes(2);
});
