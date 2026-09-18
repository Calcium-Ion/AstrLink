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

it("shows only defaults and clears dirty state on unmount under StrictMode", async () => {
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
            services={[]}
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
    expect(container.querySelector('[role="tab"]')).toBeNull();
    expect(container.textContent).toContain("ABC：每家最多一次");
    expect(container.textContent).not.toContain("astrlink/auto");
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
