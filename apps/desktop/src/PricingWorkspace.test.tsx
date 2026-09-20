// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ getServiceBilling: vi.fn() }));
vi.mock("./pricing-bridge", () => mocks);
import { PricingWorkspace } from "./PricingWorkspace";
import type { Service } from "./service-model";
let root: Root, container: HTMLDivElement;
const config = {
  provider: "moonshotai",
  bindings: {},
  monthly_budget_usd: "",
  billing_day: 1,
  time_zone: "UTC",
};
const amount = {
  amount_usd: "18.420000000",
  priced: 10,
  unpriced: 2,
  pending: 1,
  revalued: 0,
  requests: 13,
};
const service = {
  id: "service_kimi",
  name: "Kimi account",
  kind: "kimi_coding",
  models: ["kimi-for-coding"],
  enabled: true,
  capabilities: [],
  created_at: "2026-09-19T00:00:00Z",
  updated_at: "2026-09-19T00:00:00Z",
} as Service;
beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  mocks.getServiceBilling.mockResolvedValue({
    config,
    periods: [
      {
        id: "current",
        kind: "month",
        start: "2026-09-01T00:00:00Z",
        end: "2026-10-01T00:00:00Z",
        observed_at: null,
        used_percent: null,
        budget_usd: "",
        remaining_usd: "",
        coverage: "partial",
        summary: {
          ...amount,
          from: "2026-09-01T00:00:00Z",
          to: "2026-10-01T00:00:00Z",
          by_model: [
            { ...amount, provider: "moonshotai", model: "kimi-for-coding" },
          ],
        },
      },
    ],
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});
it("shows read-only cycle costs with no pricing setup workflow", async () => {
  await act(async () =>
    root.render(<PricingWorkspace services={[service]} onClose={() => {}} />),
  );
  expect(document.body.textContent).toContain("$18.42");
  expect(document.body.textContent).toContain("2 次待计价");
  expect(document.body.textContent).toContain("仅含已记录调用");
  expect(document.body.textContent).toContain("Moonshot AI");
  expect(document.querySelectorAll('[role="combobox"]')).toHaveLength(1);
  expect(document.querySelectorAll('input, [role="tab"]')).toHaveLength(0);
  for (const label of ["同步价格", "价格映射", "预算", "保存", "Coding Plan"])
    expect(document.body.textContent).not.toContain(label);
});
it("does not present failed data loading as a zero bill", async () => {
  mocks.getServiceBilling.mockRejectedValue(new Error("offline"));
  await act(async () =>
    root.render(<PricingWorkspace services={[service]} onClose={() => {}} />),
  );
  expect(document.body.textContent).toContain("金额暂不可用");
  expect(document.body.textContent).not.toContain("$0.00");
});
