// @vitest-environment happy-dom

import { act, StrictMode, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import {
  SERVICE_EDITOR_TOUR_KEY,
  ServiceEditorTour,
} from "./ServiceEditorTour";
import { i18n } from "./i18n";

let root: Root;
let container: HTMLDivElement;
const tour = () => document.querySelector('[data-slot="spotlight-tour"]');
const spotlight = () =>
  document.querySelector<HTMLElement>("[data-tour-spotlight]")?.dataset
    .tourSpotlight;
const title = () => tour()?.querySelector("h3")?.textContent;
const button = (text: string) =>
  [...document.querySelectorAll("button")].find(
    (item) => item.textContent === text,
  )!;
const help = () =>
  document.querySelector<HTMLButtonElement>(
    `button[aria-label="${i18n.t("services.editorTour.help")}"]`,
  )!;

function Harness({ modelCount }: { modelCount: number }) {
  const tabs = useRef<HTMLDivElement>(null);
  return (
    <div>
      <div ref={tabs}>
        {["connection", "models", "protocols", "failure"].map((id) => (
          <button data-tour-target={id} key={id} type="button">
            {id}
          </button>
        ))}
      </div>
      <ServiceEditorTour
        modelCount={modelCount}
        protocols={["openai.responses", "openai.chat"]}
        root={tabs}
      />
    </div>
  );
}

const render = (modelCount = 0) =>
  act(async () =>
    root.render(
      <StrictMode>
        <Harness modelCount={modelCount} />
      </StrictMode>,
    ),
  );
// Exit animations finish on their own clock; wait for the layer to unmount.
const closed = () =>
  vi.waitFor(() => {
    expect(tour()).toBeNull();
    expect(spotlight()).toBeUndefined();
  });

beforeEach(() => {
  // Most tests represent returning users who already took the tour.
  localStorage.setItem(SERVICE_EDITOR_TOUR_KEY, "seen");
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
  localStorage.removeItem(SERVICE_EDITOR_TOUR_KEY);
  vi.useRealTimers();
});

it("starts once on the first visit after the editor settles", async () => {
  vi.useFakeTimers();
  localStorage.removeItem(SERVICE_EDITOR_TOUR_KEY);
  await render();
  expect(tour()).toBeNull();
  await act(async () => vi.advanceTimersByTime(400));
  expect(title()).toBe("连接");
  expect(spotlight()).toBe("connection");
  expect(localStorage.getItem(SERVICE_EDITOR_TOUR_KEY)).toBe("seen");
  expect(document.activeElement).toBe(button("下一步"));
  vi.useRealTimers();
  await act(async () => button("跳过").click());
  await closed();
  await act(async () => root.render(null));
  await render();
  await act(async () => new Promise((resolve) => setTimeout(resolve, 450)));
  expect(tour()).toBeNull();
});

it("steps through every tab and returns focus to the trigger", async () => {
  await render();
  expect(tour()).toBeNull();
  await act(async () => help().click());
  expect(tour()?.textContent).toContain("1 / 4");
  await act(async () => button("下一步").click());
  expect(title()).toBe("支持模型");
  expect(spotlight()).toBe("models");
  expect(tour()?.textContent).toContain("2 / 4");
  expect(tour()?.textContent).toContain("现在还没有模型，记得来这里添加。");
  await act(async () => button("上一步").click());
  expect(title()).toBe("连接");
  await act(async () => button("下一步").click());
  await act(async () => button("下一步").click());
  expect(title()).toBe("入口协议");
  expect(spotlight()).toBe("protocols");
  // Clients come first; the protocol stays visible beside each one.
  expect(
    [...document.querySelectorAll<HTMLElement>("[data-client-protocol]")].map(
      (row) => {
        // Brand marks carry SVG titles but are hidden from assistive tech.
        const visible = row.cloneNode(true) as HTMLElement;
        visible.querySelectorAll("svg").forEach((mark) => mark.remove());
        return [visible.textContent, row.dataset.supported === "true"];
      },
    ),
  ).toEqual([
    ["CodexOpenAI Responses支持", true],
    ["Claude CodeAnthropic Messages未开启", false],
    ["OpenCode 等兼容工具OpenAI Chat Completions支持", true],
    ["Gemini CLIGemini Generate Content未开启", false],
  ]);
  await act(async () => button("下一步").click());
  expect(title()).toBe("失败处理");
  expect(spotlight()).toBe("failure");
  await act(async () => button("知道了").click());
  await closed();
  await vi.waitFor(() => expect(document.activeElement).toBe(help()));
});

it("omits the empty-list reminder when models exist", async () => {
  await render(3);
  await act(async () => help().click());
  await act(async () => button("下一步").click());
  expect(title()).toBe("支持模型");
  expect(tour()?.textContent).not.toContain("现在还没有模型");
});

it("restarts from the trigger while open", async () => {
  await render();
  await act(async () => help().click());
  await act(async () => button("下一步").click());
  expect(title()).toBe("支持模型");
  await act(async () => {
    help().dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    help().focus();
    help().click();
  });
  expect(title()).toBe("连接");
  expect(spotlight()).toBe("connection");
});

it("replays from the first step and closes with Escape", async () => {
  await render();
  await act(async () => help().click());
  await act(async () => button("下一步").click());
  await act(async () => button("下一步").click());
  expect(title()).toBe("入口协议");
  await act(async () =>
    document.activeElement!.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    ),
  );
  await closed();
  await act(async () => help().click());
  expect(title()).toBe("连接");
});
