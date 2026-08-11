// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createRoute: vi.fn(),
  deleteRoute: vi.fn(),
  getRoute: vi.fn(),
  listRoutes: vi.fn(),
  updateRoute: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { RouteManager } from "./RouteManager";
import type { Route } from "./route-model";
import type { RoutableService } from "./service-model";

const service: RoutableService = {
  id: "service_01",
  name: "Primary gateway",
  enabled: true,
  models: ["gpt-5.2", "team/code"],
  capabilities: [
    {
      protocol: "openai.responses",
      mode: "native",
      streaming: true,
    },
  ],
};

const route: Route = {
  id: "route_01",
  name: "Code alias",
  enabled: true,
  priority: 10,
  match: { protocol: "openai.responses", model: "team/code" },
  selection: { mode: "priority" },
  targets: [
    {
      service_id: service.id,
      plan_type: "native",
      upstream_protocol: "openai.responses",
      priority: 0,
      upstream_model: "gpt-5.2",
    },
  ],
};

const etag = `"sha256:${"a".repeat(64)}"`;

function findButton(label: string): HTMLButtonElement {
  const result = [...document.querySelectorAll("button")].find(
    (button) => button.textContent?.trim() === label,
  );
  if (!(result instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return result;
}

function labeledInput(label: string): HTMLInputElement {
  const wrapper = [...document.querySelectorAll("label")].find((candidate) =>
    candidate.querySelector(":scope > span")?.textContent?.includes(label),
  );
  const input = wrapper?.querySelector("input");
  if (!(input instanceof HTMLInputElement)) {
    throw new Error(`Missing input: ${label}`);
  }
  return input;
}

async function setInput(input: HTMLInputElement, value: string): Promise<void> {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!setter) throw new Error("Missing input value setter");
  await act(async () => {
    setter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("RouteManager", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.listRoutes.mockResolvedValue({
      items: [route],
      next_cursor: null,
    });
    bridgeMocks.getRoute.mockResolvedValue({ route, etag });
    bridgeMocks.deleteRoute.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
  });

  async function render(): Promise<void> {
    await act(async () => {
      root.render(
        <RouteManager
          coreSessionKey="42|http://127.0.0.1:43117"
          services={[service]}
          isReady
          onDirtyChange={() => undefined}
          onManageServices={() => undefined}
          protocols={[
            {
              id: "openai.responses",
              phase: "alpha",
              primary: true,
              streaming: true,
            },
          ]}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
  }

  it("renders auto showcase above persisted priority routes", async () => {
    await render();

    expect(
      container.querySelector('[data-testid="auto-routing-showcase"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("astrlink/auto");
    expect(container.textContent).toContain("理解任务");
    expect(container.textContent).toContain("固定路由与别名");
    expect(container.textContent).toContain("Code alias");
    expect(container.textContent).toContain("team/code");
    expect(container.textContent).toContain("gpt-5.2");
    expect(container.textContent).toContain("训练中 · 不可启用");
    expect(container.textContent).not.toContain("mmBERT");
    expect(container.querySelector('[data-testid="route-auto-gate"]')).toBeNull();
  });

  it("creates a model-alias route from the priority editor", async () => {
    bridgeMocks.listRoutes.mockResolvedValueOnce({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.createRoute.mockResolvedValue({ route, etag });
    await render();

    await act(async () => {
      findButton("新建固定路由").click();
    });
    expect(
      container.querySelector('[data-testid="auto-routing-showcase"]'),
    ).toBeNull();
    await setInput(labeledInput("路由名称"), "Code alias");
    await setInput(labeledInput("公开模型名"), "team/code");
    await setInput(labeledInput("上游模型"), "gpt-5.2");

    await act(async () => {
      findButton("创建固定路由").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.createRoute).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Code alias",
        match: {
          protocol: "openai.responses",
          model: "team/code",
        },
        selection: { mode: "priority" },
        targets: [
          expect.objectContaining({
            service_id: "service_01",
            plan_type: "native",
            upstream_model: "gpt-5.2",
          }),
        ],
      }),
    );
  });

  it("uses an in-app dialog before deleting a route", async () => {
    await render();

    await act(async () => {
      findButton("删除").click();
      await Promise.resolve();
    });
    expect(container.querySelector('[role="dialog"]')).not.toBeNull();
    expect(container.textContent).toContain("删除路由？");

    await act(async () => {
      findButton("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteRoute).toHaveBeenCalledWith(route.id, etag);
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });
});
