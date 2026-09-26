// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import type { Service } from "./service-model";
const mocks = vi.hoisted(() => ({ invoke: vi.fn(), billing: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: mocks.invoke }));
vi.mock("./pricing-bridge", () => ({ getServiceBilling: mocks.billing }));
import { ServiceStatisticsEntry } from "./ServiceStatisticsDialog";
let root: Root;
let host: HTMLDivElement;
const service = {
  id: "service_price",
  name: "Price test",
  models: ["gpt-6-astra"],
} as Service;
beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  mocks.billing.mockResolvedValue({
    config: {
      provider: "",
      bindings: {},
      billing_day: 1,
      time_zone: "UTC",
      monthly_budget_usd: "",
    },
  });
  mocks.invoke.mockImplementation(
    async (command: string, args: { operation?: string }) => {
      if (command === "service_statistics") return undefined;
      if (args.operation === "catalog")
        return {
          prices: [
            {
              provider: "openai",
              model: "gpt-6-astra",
              name: "GPT-6 Astra",
              expression:
                'len <= 272000 ? tier("small", p * 10 + cr * 1 + cc * 12.5 + c * 50) : tier("large", p * 20 + cr * 2 + cc * 25 + c * 75)',
            },
          ],
        };
      return { ok: true };
    },
  );
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.clearAllMocks();
});
const button = (text: string) =>
  [...document.querySelectorAll<HTMLButtonElement>("button")].find(
    (b) => b.textContent === text,
  )!;
async function openPrices(models: string[]) {
  await act(async () =>
    root.render(
      <ServiceStatisticsEntry
        service={{ ...service, models }}
        disabled={false}
      />,
    ),
  );
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>(
        '[aria-label="缓存与费用 · Price test"]',
      )!
      .click(),
  );
  await act(async () =>
    button("模型价格").dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    ),
  );
}
it("shows effective catalog tiers before offering explicit channel overrides", async () => {
  await openPrices(["gpt-6-astra"]);
  expect(document.body.textContent).toContain(
    "来源：价格目录 · OpenAI · GPT-6 Astra",
  );
  expect(document.body.textContent).toContain("$12.5");
  expect(document.body.textContent).toContain("$75");
  expect(document.querySelector('[aria-label="输入价格"]')).toBeNull();
  await act(async () => button("设置渠道自定义价格").click());
  for (const label of ["输入", "输出", "缓存读取", "缓存写入"]) {
    const input = document.querySelector<HTMLInputElement>(
      `[aria-label="${label}价格"]`,
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "2");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  await act(async () => button("保存模型价格").click());
  expect(mocks.invoke).toHaveBeenCalledWith(
    "pricing",
    expect.objectContaining({
      operation: "configure",
      input: expect.objectContaining({
        overrides: {
          "gpt-6-astra": {
            input: "2",
            output: "2",
            cache_read: "2",
            cache_write: "2",
          },
        },
      }),
    }),
  );
  expect(document.body.textContent).toContain("来源：渠道自定义价格");
});
it("marks an unmatched model unpriced instead of showing zero or unrelated catalog rates", async () => {
  await openPrices(["deepseek-v4.1-flash"]);
  expect(document.body.textContent).toContain("未配置价格");
  expect(document.body.textContent).not.toContain("$10");
});
