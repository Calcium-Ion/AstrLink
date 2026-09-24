// @vitest-environment happy-dom

import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const motionPreference = vi.hoisted(() => ({ reduced: false }));
vi.mock("motion/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("motion/react")>()),
  useReducedMotion: () => motionPreference.reduced,
}));

import { PROTOCOL_MODE_GUIDE_KEY, ProtocolModeHelp } from "./ProtocolModeHelp";
import { i18n } from "./i18n";

let root: Root;
let container: HTMLDivElement;
const dialog = () => document.querySelector('[data-slot="dialog-content"]');
const button = (text: string) =>
  [...document.querySelectorAll("button")].find(
    (item) => item.textContent === text,
  )!;
const help = () => button(i18n.t("services.protocolModes.help"));
const packet = (lane: "passthrough" | "convert") => {
  const element = document.querySelector<HTMLElement>(
    `[data-protocol-lane="${lane}"] [data-flow-packet]`,
  )!;
  return {
    position: element.dataset.flowPacket,
    protocol: element.querySelector("span")?.textContent,
    lost: [...element.querySelectorAll<HTMLElement>("[data-lost]")].map(
      (item) => item.textContent,
    ),
  };
};
const start = { position: "0", protocol: "Responses", lost: [] };
const render = () =>
  act(async () =>
    root.render(
      <StrictMode>
        <ProtocolModeHelp />
      </StrictMode>,
    ),
  );

beforeEach(() => {
  motionPreference.reduced = false;
  // Most tests represent returning users who already saw the first-visit demo.
  localStorage.setItem(PROTOCOL_MODE_GUIDE_KEY, "seen");
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
  vi.unstubAllGlobals();
  localStorage.removeItem(PROTOCOL_MODE_GUIDE_KEY);
  vi.useRealTimers();
});

it("plays once on the first visit and stays closed afterwards", async () => {
  localStorage.removeItem(PROTOCOL_MODE_GUIDE_KEY);
  await render();
  expect(dialog()?.textContent).toContain("原样转发还是转换格式？");
  expect(localStorage.getItem(PROTOCOL_MODE_GUIDE_KEY)).toBe("seen");
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
  await act(async () => root.render(null));
  await render();
  expect(dialog()).toBeNull();
});

it("still opens the first time when storage is unavailable", async () => {
  const blocked = () => {
    throw new Error("blocked");
  };
  vi.stubGlobal("localStorage", { getItem: blocked, setItem: blocked });
  await render();
  expect(dialog()).not.toBeNull();
});

it("opens only from the help trigger", async () => {
  await render();
  expect(dialog()).toBeNull();
  await act(async () => help().click());
  expect(dialog()?.textContent).toContain("原样转发还是转换格式？");
  expect(document.querySelector('[data-slot="popover-content"]')).toBeNull();
  await act(async () => button("知道了").click());
  expect(dialog()).toBeNull();
});

it("plays both modes side by side and drops only Codex-only tools", async () => {
  vi.useFakeTimers();
  await render();
  await act(async () => help().click());
  expect(packet("passthrough")).toEqual(start);
  expect(packet("convert")).toEqual(start);
  expect(dialog()?.textContent).toContain("apply_patch");
  await act(async () => vi.advanceTimersByTime(900));
  expect(packet("passthrough").position).toBe("1");
  expect(packet("convert")).toEqual({ ...start, position: "1" });
  await act(async () => vi.advanceTimersByTime(900));
  expect(packet("passthrough")).toEqual({ ...start, position: "1" });
  expect(packet("convert")).toEqual({
    position: "1",
    protocol: "Chat",
    lost: ["专用工具"],
  });
  await act(async () => vi.advanceTimersByTime(1000));
  expect(packet("passthrough")).toEqual({ ...start, position: "2" });
  expect(packet("convert")).toEqual({
    position: "2",
    protocol: "Chat",
    lost: ["专用工具"],
  });
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('button[aria-label="重播演示"]')!
      .click(),
  );
  expect(packet("passthrough")).toEqual(start);
  expect(packet("convert")).toEqual(start);
  await act(async () => button("知道了").click());
  await act(async () => vi.advanceTimersByTime(4000));
  expect(dialog()).toBeNull();
});

it("shows the final comparison without movement for reduced motion", async () => {
  motionPreference.reduced = true;
  await render();
  await act(async () => help().click());
  expect(packet("passthrough")).toEqual({ ...start, position: "2" });
  expect(packet("convert")).toEqual({
    position: "2",
    protocol: "Chat",
    lost: ["专用工具"],
  });
});

it("closes with Escape and returns focus to the help trigger", async () => {
  await render();
  await act(async () => help().click());
  expect(document.activeElement).toBe(button("知道了"));
  await act(async () =>
    document.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    ),
  );
  expect(dialog()).toBeNull();
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
  expect(document.activeElement).toBe(help());
});
