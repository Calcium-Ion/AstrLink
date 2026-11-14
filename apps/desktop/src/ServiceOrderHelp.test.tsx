// @vitest-environment happy-dom

import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const motionPreference = vi.hoisted(() => ({ reduced: false }));
vi.mock("motion/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("motion/react")>()),
  useReducedMotion: () => motionPreference.reduced,
}));

import { SERVICE_ORDER_GUIDE_KEY, ServiceOrderHelp } from "./ServiceOrderHelp";
import { i18n } from "./i18n";
import { settleClosedLayer } from "./lib/test-layer-settle";

let root: Root;
let container: HTMLDivElement;
const dialog = () => document.querySelector('[data-slot="dialog-content"]');
const help = () =>
  container.querySelector<HTMLButtonElement>(
    `button[aria-label="${i18n.t("services.orderLabel")}"]`,
  )!;
const button = (text: string) =>
  [...document.querySelectorAll("button")].find(
    (item) => item.textContent === text,
  )!;
const order = () =>
  [...document.querySelectorAll<HTMLElement>("[data-preview-item]")].map(
    (item) => item.dataset.previewItem,
  );
const render = (ready = true, providerCount = 2) =>
  act(async () =>
    root.render(
      <StrictMode>
        <ServiceOrderHelp providerCount={providerCount} ready={ready} />
      </StrictMode>,
    ),
  );

beforeEach(() => {
  motionPreference.reduced = false;
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
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
  expect(dialog()?.textContent).toContain("拖动卡片调整提供商先后");
  expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBe("seen");
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
  await act(async () => root.render(null));
  await render();
  expect(dialog()).toBeNull();
});

it("waits for two providers before the first visit opens or remembers it", async () => {
  for (const providerCount of [0, 1]) {
    await render(true, providerCount);
    expect(dialog()).toBeNull();
    expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBeNull();
  }
  // The help button still opens the demo with one provider.
  await act(async () => help().click());
  expect(dialog()?.textContent).toContain("拖动卡片调整提供商先后");
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
  await settleClosedLayer();
  expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBeNull();

  await render(true, 2);
  expect(dialog()?.textContent).toContain("拖动卡片调整提供商先后");
  expect(localStorage.getItem(SERVICE_ORDER_GUIDE_KEY)).toBe("seen");
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
  await settleClosedLayer();
  await act(async () => root.render(null));
  await render(true, 3);
  expect(dialog()).toBeNull();
});

it("opens the demo from the first click on every visit without a help popover", async () => {
  localStorage.setItem(SERVICE_ORDER_GUIDE_KEY, "seen");
  await render();
  expect(dialog()).toBeNull();
  for (let visit = 0; visit < 2; visit++) {
    await act(async () => help().click());
    expect(dialog()).not.toBeNull();
    expect(document.querySelector('[data-slot="popover-content"]')).toBeNull();
    expect(order()).toEqual(["codex", "openai", "newapi"]);
    await act(async () => button("知道了").click());
    expect(dialog()).toBeNull();
  }
});

const firstAccount = () =>
  document.querySelector<HTMLElement>("[data-demo-account][data-first]")
    ?.dataset.demoAccount;

it("moves a card to the front, then shows which account each strategy uses, loops, replays and cancels on close", async () => {
  vi.useFakeTimers();
  await render();
  expect(order()).toEqual(["codex", "openai", "newapi"]);
  expect(firstAccount()).toBeUndefined();
  await act(async () => vi.advanceTimersByTime(1000));
  expect(
    document.querySelector("[data-lifted]")?.getAttribute("data-preview-item"),
  ).toBe("newapi");
  await act(async () => vi.advanceTimersByTime(900));
  expect(order()).toEqual(["newapi", "codex", "openai"]);
  await act(async () => vi.advanceTimersByTime(2100));
  expect(firstAccount()).toBe("b");
  expect(dialog()?.textContent).toContain("最近快重置优先：先用 B");
  await act(async () => vi.advanceTimersByTime(2800));
  expect(firstAccount()).toBe("a");
  expect(dialog()?.textContent).toContain("额度剩余最多优先：先用 A");
  // After the hold it starts over.
  await act(async () => vi.advanceTimersByTime(3200));
  expect(order()).toEqual(["codex", "openai", "newapi"]);
  await act(async () => vi.advanceTimersByTime(2000));
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('button[aria-label="重播拖拽演示"]')!
      .click(),
  );
  expect(order()).toEqual(["codex", "openai", "newapi"]);
  await act(async () => button("知道了").click());
  await act(async () => vi.advanceTimersByTime(12000));
  expect(dialog()).toBeNull();
});

it("still opens and dismisses when browser storage is unavailable", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("unavailable");
  });
  await render();
  expect(dialog()).not.toBeNull();
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
});

it("shows the result without animated dragging for reduced motion", async () => {
  motionPreference.reduced = true;
  await render();
  expect(order()).toEqual(["newapi", "codex", "openai"]);
  expect(document.querySelector("[data-lifted]")).toBeNull();
  expect(firstAccount()).toBe("a");
  expect(dialog()?.textContent).toContain("额度剩余最多优先");
});

it("closes with Escape and returns focus to the help trigger", async () => {
  await render();
  await act(async () =>
    document.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    ),
  );
  expect(dialog()).toBeNull();
  await settleClosedLayer();
  expect(document.activeElement).toBe(help());
});
