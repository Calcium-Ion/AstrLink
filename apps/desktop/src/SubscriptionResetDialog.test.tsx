// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ResetCreditsDetails } from "./subscription-usage-model";

const fetchDetails = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ getServiceResetCredits: fetchDetails }));
import { SubscriptionResetDialog } from "./SubscriptionResetDialog";

let root: Root;
let container: HTMLDivElement;
const onConfirm = vi.fn();
const onCancel = vi.fn();
function button(label: string) {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>("button"),
  ].find((button) => button.textContent?.trim() === label);
  if (!button) throw new Error(`Missing button: ${label}`);
  return button;
}
async function render(serviceId = "service_codex") {
  await act(async () =>
    root.render(
      <SubscriptionResetDialog
        key={serviceId}
        serviceId={serviceId}
        serviceName="Codex"
        onCancel={onCancel}
        onConfirm={onConfirm}
      />,
    ),
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
  vi.setSystemTime(new Date("2026-09-30T12:00:00Z"));
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
});

it("loads on open, orders available credits, and only confirms after loading", async () => {
  let resolve!: (details: ResetCreditsDetails) => void;
  fetchDetails.mockReturnValueOnce(
    new Promise<ResetCreditsDetails>((done) => {
      resolve = done;
    }),
  );
  await render();
  expect(fetchDetails).toHaveBeenCalledWith("service_codex");
  expect(button("重置").disabled).toBe(true);
  expect(onConfirm).not.toHaveBeenCalled();
  await act(async () =>
    resolve({
      available_count: 4,
      credits: [
        { expires_at: "2026-10-15T12:00:00Z" },
        {},
        { expires_at: "2026-10-01T12:00:00Z" },
        { expires_at: "2026-09-29T12:00:00Z" },
      ],
    }),
  );
  expect(document.body.textContent).toContain("当前还可重置 3 次");
  expect(
    [...document.querySelectorAll("time")].map((time) => time.dateTime),
  ).toEqual(["2026-10-01T12:00:00Z", "2026-10-15T12:00:00Z"]);
  expect(document.body.textContent).toContain("最早到期");
  expect(document.body.textContent).toContain("未提供到期时间");
  await act(async () => button("重置").click());
  await act(async () => button("重置").click());
  expect(onConfirm).toHaveBeenCalledOnce();
});

it("offers retry on failure and disables reset when no credits remain", async () => {
  fetchDetails
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce({ available_count: 0, credits: [] });
  await render();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain(
    "请重试",
  );
  expect(button("重置").disabled).toBe(true);
  await act(async () => button("重试").click());
  expect(document.body.textContent).toContain("当前没有可用的重置次数");
  expect(button("重置").disabled).toBe(true);
  await act(async () => button("取消").click());
  expect(onCancel).toHaveBeenCalledOnce();
  expect(onConfirm).not.toHaveBeenCalled();
});

it("ignores old service responses and a credit expiring while the dialog is open", async () => {
  let resolve!: (details: ResetCreditsDetails) => void;
  fetchDetails
    .mockReturnValueOnce(
      new Promise<ResetCreditsDetails>((done) => {
        resolve = done;
      }),
    )
    .mockResolvedValueOnce({
      available_count: 1,
      credits: [{ expires_at: "2026-09-30T12:00:10Z" }],
    });
  await render("service_old");
  await render("service_new");
  await act(async () => resolve({ available_count: 50, credits: [] }));
  expect(document.body.textContent).toContain("当前还可重置 1 次");
  vi.setSystemTime(new Date("2026-09-30T12:00:11Z"));
  fetchDetails.mockResolvedValueOnce({ available_count: 0, credits: [] });
  await act(async () => button("重置").click());
  expect(onConfirm).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain("当前没有可用的重置次数");
});
