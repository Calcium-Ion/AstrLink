// @vitest-environment happy-dom
import { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
  listRoutes: vi.fn(),
  listRecoveryPaths: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
import { RouteManager } from "./RouteManager";
import { defaultFailurePolicy } from "./failure-policy-model";

it("shows default-setting tabs and clears dirty state on unmount under StrictMode", async () => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  bridge.getRoutingSettings.mockResolvedValue({
    default_failure_policy: defaultFailurePolicy(),
    strategy: "failover_only",
    max_attempts: 6,
    allow_unmatched_failover: false,
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container),
    dirty = vi.fn();
  try {
    await act(async () =>
      root.render(
        <StrictMode>
          <RouteManager
            coreSessionKey="test"
            services={[
              {
                id: "service_a",
                name: "A",
                enabled: true,
                models: ["gpt-5"],
                capabilities: [],
              },
            ]}
            protocols={[]}
            isReady
            onDirtyChange={dirty}
            onManageServices={() => {}}
          />
        </StrictMode>,
      ),
    );
    expect(
      container.querySelector('[data-testid="routing-defaults-panel"]'),
    ).not.toBeNull();
    const tabs = [...container.querySelectorAll('[role="tab"]')];
    expect(tabs).toHaveLength(5);
    expect(tabs[0].getAttribute("aria-selected")).toBe("true");
    expect(container.textContent).toContain("Codex 自动审查");
    expect(container.textContent).not.toContain("astrlink/auto");
    // The redirect editor suggests models from the forwarded services.
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "添加重定向")!
        .click(),
    );
    await act(async () =>
      container
        .querySelector<HTMLInputElement>(
          'input[role="combobox"][aria-label="第 1 条规则的目标模型"]',
        )!
        .click(),
    );
    expect(
      [...document.querySelectorAll('[role="option"]')].map((option) =>
        option.getAttribute("aria-label"),
      ),
    ).toEqual(["gpt-5"]);
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          'button[aria-label="删除 第 1 条规则 的重定向"]',
        )!
        .click(),
    );
    expect(dirty).toHaveBeenLastCalledWith(false);
    await act(async () =>
      tabs
        .find((tab) => tab.textContent === "恢复与重试")!
        .dispatchEvent(new MouseEvent("click", { bubbles: true })),
    );
    expect(container.textContent).toContain("ABC：失败后换下一家");
    expect(bridge.listRoutes).not.toHaveBeenCalled();
    expect(bridge.listRecoveryPaths).not.toHaveBeenCalled();
    const input = container.querySelector<HTMLInputElement>(
      'input[type="number"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "4");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dirty).toHaveBeenLastCalledWith(true);
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
  expect(dirty).toHaveBeenLastCalledWith(false);
});
