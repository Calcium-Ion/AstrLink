// @vitest-environment happy-dom

import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const motionPreference = vi.hoisted(() => ({ reduced: false }));
vi.mock("motion/react", async (importOriginal) => ({
  ...await importOriginal<typeof import("motion/react")>(),
  useReducedMotion: () => motionPreference.reduced,
}));

import { SERVICE_ORDER_GUIDE_KEY, ServiceOrderHelp } from "./ServiceOrderHelp";
import { i18n } from "./i18n";

let root: Root;
let container: HTMLDivElement;
const dialog = () => document.querySelector('[data-slot="dialog-content"]');
const help = () => container.querySelector<HTMLButtonElement>(`button[aria-label="${i18n.t("services.orderLabel")}"]`)!;
const button = (text: string) => [...document.querySelectorAll("button")].find(item => item.textContent === text)!;
const order = () => [...document.querySelectorAll<HTMLElement>("[data-preview-item]")].map(item => item.dataset.previewItem);
const render = (ready = true) => act(async () => root.render(
  <StrictMode><ServiceOrderHelp ready={ready}><p>排序帮助</p></ServiceOrderHelp></StrictMode>,
));

beforeEach(() => {
  motionPreference.reduced = false;
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  localStorage.removeItem(SERVICE_ORDER_GUIDE_KEY);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  localStorage.removeItem(SERVICE_ORDER_GUIDE_KEY);
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it("waits for the catalog, remembers the first visit and survives StrictMode", async () => {
  await render(false);
  expect(dialog()).toBeNull();
  expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBeNull();
  await render();
  expect(dialog()?.textContent).toContain("拖拽调整提供商优先级");
  expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBe("seen");
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
  await act(async () => root.render(null));
  await render();
  expect(dialog()).toBeNull();
});

it("preserves help on clicks one to four and replays on every fifth click", async () => {
  localStorage.setItem(SERVICE_ORDER_GUIDE_KEY, "seen");
  await render();
  for (let cycle = 0; cycle < 2; cycle++) {
    for (let index = 0; index < 4; index++) {
      await act(async () => help().click());
      expect(dialog()).toBeNull();
      expect(document.querySelector('[data-slot="popover-content"]') !== null).toBe(index % 2 === 0);
    }
    await act(async () => help().click());
    expect(dialog()).not.toBeNull();
    expect(document.querySelector('[data-slot="popover-content"]')).toBeNull();
    expect(order()).toEqual(["codex", "openai", "newapi"]);
    await act(async () => button("知道了").click());
  }
});

it("demonstrates two provider moves, replays from the beginning and cancels on close", async () => {
  vi.useFakeTimers();
  await render();
  expect(order()).toEqual(["codex", "openai", "newapi"]);
  await act(async () => vi.advanceTimersByTime(1700));
  expect(order()).toEqual(["newapi", "codex", "openai"]);
  await act(async () => vi.advanceTimersByTime(4000));
  expect(order()).toEqual(["newapi", "openai", "codex"]);
  expect(dialog()?.textContent).toContain("开启故障切换后");
  await act(async () => document.querySelector<HTMLButtonElement>('button[aria-label="重播拖拽演示"]')!.click());
  expect(order()).toEqual(["codex", "openai", "newapi"]);
  await act(async () => button("知道了").click());
  await act(async () => vi.advanceTimersByTime(6000));
  expect(dialog()).toBeNull();
});

it("still opens and dismisses when browser storage is unavailable", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("unavailable"); });
  await render();
  expect(dialog()).not.toBeNull();
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
});

it("shows the result without animated dragging for reduced motion", async () => {
  motionPreference.reduced = true;
  await render();
  expect(order()).toEqual(["newapi", "openai", "codex"]);
  expect(document.querySelector("[data-lifted]")).toBeNull();
  expect(dialog()?.textContent).toContain("开启故障切换后");
});

it("closes with Escape and returns focus to the help trigger", async () => {
  await render();
  await act(async () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(dialog()).toBeNull();
  await act(async () => new Promise(resolve => setTimeout(resolve, 0)));
  expect(document.activeElement).toBe(help());
});
