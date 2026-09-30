// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { WorkspaceSnapshotProvider } from "./workspace-snapshots";
import { useResetCredits } from "./use-reset-credits";
import type { ResetCreditsDetails } from "./subscription-usage-model";
import { SubscriptionResetButton } from "./SubscriptionUsageMeter";

const fetchDetails = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ getServiceResetCredits: fetchDetails }));
let root: Root;
let container: HTMLDivElement;
function Reader({ dialog = false }: { dialog?: boolean }) {
  const { state } = useResetCredits("service_codex", dialog ? undefined : 2);
  return (
    <output data-testid={dialog ? "dialog" : "list"}>
      {state.status === "ready" ? state.details.available_count : state.status}
    </output>
  );
}
async function render(dialog = false) {
  await act(async () =>
    root.render(
      <WorkspaceSnapshotProvider sessionKey="test">
        <Reader />
        {dialog ? <Reader dialog /> : null}
      </WorkspaceSnapshotProvider>,
    ),
  );
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
  vi.setSystemTime(new Date("2026-09-30T12:00:00Z"));
  fetchDetails
    .mockReset()
    .mockResolvedValue({ available_count: 2, credits: [] });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
});

it("checks every three hours and opening the dialog immediately refreshes the shared list", async () => {
  await render();
  expect(fetchDetails).toHaveBeenCalledTimes(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2 * 60 * 60_000);
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(fetchDetails).toHaveBeenCalledTimes(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60 * 60_000);
  });
  expect(fetchDetails).toHaveBeenCalledTimes(2);
  fetchDetails.mockResolvedValue({ available_count: 1, credits: [{}] });
  await render(true);
  expect(fetchDetails).toHaveBeenCalledTimes(3);
  expect(container.querySelector('[data-testid="list"]')?.textContent).toBe(
    "1",
  );
  expect(container.querySelector('[data-testid="dialog"]')?.textContent).toBe(
    "1",
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000);
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(fetchDetails).toHaveBeenCalledTimes(3);
});

it("does not let a slower automatic check overwrite a fresh manual result", async () => {
  let resolve!: (details: ResetCreditsDetails) => void;
  fetchDetails
    .mockReturnValueOnce(
      new Promise<ResetCreditsDetails>((done) => {
        resolve = done;
      }),
    )
    .mockResolvedValueOnce({ available_count: 1, credits: [{}] });
  await render();
  await render(true);
  await act(async () => resolve({ available_count: 2, credits: [] }));
  expect(container.querySelector('[data-testid="list"]')?.textContent).toBe(
    "1",
  );
});

it("shows a clickable warning badge and escalates its tone at 24 hours", async () => {
  fetchDetails.mockResolvedValue({
    available_count: 2,
    credits: [{ expires_at: "2026-10-02T12:00:00Z" }, {}],
  });
  const open = vi.fn();
  await act(async () =>
    root.render(
      <SubscriptionResetButton
        onReset={open}
        usage={{
          service_id: "service_codex",
          fetched_at: "2026-09-30T12:00:00Z",
          rate_limit_reset_credits: { available_count: 2 },
        }}
      />,
    ),
  );
  expect(
    container.querySelector('[data-tone="pending"]')?.textContent,
  ).toContain("1 次快到期");
  const warning = container.querySelector<HTMLButtonElement>(
    'button[title="查看重置次数的到期时间"]',
  )!;
  await act(async () => warning.click());
  expect(open).toHaveBeenCalledOnce();
  vi.setSystemTime(new Date("2026-10-01T12:00:00Z"));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000);
  });
  expect(container.querySelector('[data-tone="negative"]')).not.toBeNull();
});
