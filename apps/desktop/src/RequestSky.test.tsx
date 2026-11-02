// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getUsageSummary: vi.fn(),
  getRequestActivity: vi.fn(),
}));
vi.mock("./bridge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./bridge")>()),
  ...bridge,
}));
// Client icons render only inside the sky's static layer, so their renders
// count that layer's.
const iconRenders = vi.hoisted(() => ({ count: 0 }));
vi.mock("@/components/ClientTypeIcon", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/components/ClientTypeIcon")>();
  const { createElement } = await import("react");
  return {
    ...actual,
    ClientTypeIcon: (props: Parameters<typeof actual.ClientTypeIcon>[0]) => {
      iconRenders.count += 1;
      return createElement(actual.ClientTypeIcon, props);
    },
  };
});

import { applyLocale } from "./i18n";
import type {
  RequestActivity,
  RequestActivityCall,
} from "./request-record-model";
import { RequestSky } from "./RequestSky";
import type { Service, ServiceKind } from "./service-model";
import {
  emptyUsageSummary,
  resolveUsageWindow,
  type UsageRoute,
  type UsageSummary,
  type UsageWindow,
} from "./usage-range";
import { settleClosedLayer } from "@/lib/test-layer-settle";

function service(index: number, kind: ServiceKind = "newapi"): Service {
  return {
    id: `service_${index}`,
    name: `Provider ${index}`,
    kind,
    enabled: true,
    models: [],
    capabilities: [],
    ...(kind === "codex_subscription"
      ? { subscription: { provider: "openai", status: "connected" } }
      : {}),
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  } as Service;
}

function route(
  client: UsageRoute["client_type"],
  serviceId: string | null,
  requests: number,
  extra: Partial<UsageRoute> = {},
): UsageRoute {
  return {
    client_type: client,
    service_id: serviceId,
    failed_service_id: null,
    failed: false,
    rewrites: [],
    requests,
    ...extra,
  };
}

function call(
  startedAt: string,
  extra: Partial<RequestActivityCall> = {},
): RequestActivityCall {
  return {
    session_id: "session_1",
    client_type: "codex",
    started_at: startedAt,
    completed_at: startedAt,
    status: "succeeded",
    service_id: "service_1",
    ...extra,
  };
}

const activity = (calls: RequestActivityCall[]): RequestActivity => ({
  now: "2026-11-02T08:00:00Z",
  calls,
});

describe("RequestSky", () => {
  let container: HTMLDivElement;
  let root: Root;
  let reducedMotion = false;

  beforeEach(async () => {
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    reducedMotion = false;
    iconRenders.count = 0;
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("reduced-motion") && reducedMotion,
      media: query,
      addEventListener() {},
      removeEventListener() {},
    }));
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 1006, 316),
    );
    bridge.getUsageSummary.mockReset();
    bridge.getRequestActivity.mockReset();
    bridge.getRequestActivity.mockResolvedValue(activity([]));
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  /** Answers today's read with `today` and the 30-day read with `month`. */
  function serve(today: UsageRoute[], month: UsageRoute[] = today) {
    bridge.getUsageSummary.mockImplementation(async (window: UsageWindow) => {
      const days =
        (new Date(window.to).getTime() - new Date(window.from).getTime()) /
        86_400_000;
      return {
        ...emptyUsageSummary(window),
        by_route: days < 2 ? today : month,
      };
    });
  }

  async function render(
    services: Service[],
    overview: {
      overviewMonth?: UsageSummary | null;
      revision?: UsageSummary | null;
    } = {},
  ) {
    await act(async () =>
      root.render(
        <RequestSky
          coreSessionKey="session"
          isReady
          overviewMonth={overview.overviewMonth}
          revision={overview.revision ?? null}
          services={services}
        />,
      ),
    );
    await act(async () => {});
  }

  /** The overview's own read of a preset range. */
  function overviewRead(
    preset: "7d" | "30d",
    routes: UsageRoute[],
  ): UsageSummary {
    return {
      ...emptyUsageSummary(resolveUsageWindow(preset, new Date())),
      by_route: routes,
    };
  }
  const spans = () =>
    bridge.getUsageSummary.mock.calls.map(([window]) =>
      Math.round(
        (new Date(window.to).getTime() - new Date(window.from).getTime()) /
          86_400_000,
      ),
    );

  const all = (slot: string) =>
    container.querySelectorAll(`[data-slot="${slot}"]`);
  const discs = (slot: string) =>
    [...all(slot)].map((node) => {
      const disc = node.querySelector('circle[r="11"]')!;
      return [disc.getAttribute("cx"), disc.getAttribute("cy")];
    });

  it("draws one node per endpoint and flares only for used rewrites", async () => {
    serve([
      route("codex", "service_2", 3, {
        failed_service_id: "service_1",
        rewrites: ["privacy", "conversion"],
      }),
      route("claude_code", "service_1", 5),
      route("claude_code", "service_2", 1, { failed: true }),
    ]);
    await render([service(1, "codex_subscription"), service(2), service(3)]);
    expect(container.querySelector("h2")?.textContent).toBe("今天的请求");
    expect(all("request-sky-planet")).toHaveLength(3);
    expect(all("request-sky-client")).toHaveLength(2);
    expect(all("request-sky-trails")).toHaveLength(1);
    expect(all("request-sky-failover")).toHaveLength(1);
    expect(all("request-sky-fail")).toHaveLength(1);
    expect(
      [...all("request-sky-flare")].map((flare) => flare.textContent),
    ).toEqual(["隐私脱敏", "格式转换"]);
    // Provider 3 had nothing today: still there, dimmed, without a wash.
    const idle = all("request-sky-planet")[2];
    expect(idle.getAttribute("data-idle")).toBe("true");
    expect(idle.getAttribute("opacity")).toBe("0.45");
    expect(idle.querySelectorAll("circle")).toHaveLength(1);
    expect(container.textContent).toContain("Provider 1");
    expect(container.textContent).toContain("Claude Code");
  });

  it("switches to 30 days without a read and without moving a node", async () => {
    serve(
      [route("codex", "service_1", 2)],
      [
        route("codex", "service_1", 40),
        route("cursor", "service_2", 12),
        route("claude_code", "service_3", 30, { rewrites: ["privacy"] }),
      ],
    );
    await render([service(1), service(2), service(3)]);
    const reads = bridge.getUsageSummary.mock.calls.length;
    expect(reads).toBe(2);
    const today = {
      clients: discs("request-sky-client"),
      planets: discs("request-sky-planet"),
    };
    expect(today.clients).toHaveLength(3);
    expect(all("request-sky-flare")).toHaveLength(0);
    expect(
      [...all("request-sky-client")].map((node) =>
        node.getAttribute("data-idle"),
      ),
    ).toEqual([null, "true", "true"]);

    const month = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "近 30 天",
    )!;
    await act(async () => month.click());
    expect(container.querySelector("h2")?.textContent).toBe("近 30 天的请求");
    expect(bridge.getUsageSummary.mock.calls.length).toBe(reads);
    expect(discs("request-sky-client")).toEqual(today.clients);
    expect(discs("request-sky-planet")).toEqual(today.planets);
    expect(container.querySelector("[data-idle]")).toBeNull();
    expect(all("request-sky-flare")).toHaveLength(1);
  });

  it("shows the overview's 30 days instead of reading them again", async () => {
    serve([route("codex", "service_1", 2)], [route("cursor", "service_9", 99)]);
    const services = [service(1), service(2)];
    // The overview is still reading its 30 days: nothing draws yet.
    await render(services, { overviewMonth: null });
    expect(spans()).toEqual([1]);
    expect(all("request-sky-client")).toHaveLength(0);
    expect(
      container
        .querySelector('[data-slot="request-sky"]')
        ?.getAttribute("aria-busy"),
    ).toBe("true");

    const first = overviewRead("30d", [
      route("codex", "service_1", 40),
      route("claude_code", "service_2", 12),
    ]);
    await render(services, { overviewMonth: first, revision: first });
    expect(spans()).toEqual([1]);
    expect(all("request-sky-client")).toHaveLength(2);
    const month = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "近 30 天",
    )!;
    await act(async () => month.click());
    expect(container.querySelector("[data-idle]")).toBeNull();

    // A new read of the same 30 days is new traffic: the sky reads today.
    const next = overviewRead("30d", [
      route("codex", "service_1", 41),
      route("claude_code", "service_2", 12),
    ]);
    await render(services, { overviewMonth: next, revision: next });
    expect(spans()).toEqual([1, 1]);

    // Another overview range is no new traffic; the sky reads its own month.
    const week = overviewRead("7d", [route("codex", "service_1", 9)]);
    await render(services, { revision: week });
    expect(spans()).toEqual([1, 1, 30]);
  });

  it("folds providers past the busiest five and lists them on demand", async () => {
    const counts = [3, 0, 50, 7, 0, 1, 30, 0, 9, 2, 0, 12, 0, 0, 4];
    serve(
      counts.flatMap((count, index) =>
        count > 0 ? [route("codex", `service_${index + 1}`, count)] : [],
      ),
    );
    await render(counts.map((_, index) => service(index + 1)));
    expect(all("request-sky-planet")).toHaveLength(5);
    expect(all("request-sky-cluster")).toHaveLength(1);
    const more = container.querySelector<HTMLButtonElement>(
      '[data-slot="request-sky-more"]',
    )!;
    expect(more.textContent).toBe("另外 4 家");
    await act(async () => {
      more.dispatchEvent(
        new PointerEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      more.click();
    });
    const rows = [...document.querySelectorAll("[role='dialog'] li")].map(
      (row) => row.textContent,
    );
    expect(rows).toEqual([
      "Provider 154 次",
      "Provider 13 次",
      "Provider 102 次",
      "Provider 61 次",
    ]);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    await settleClosedLayer();
  });

  it("keeps a heavy month to a few hundred sky elements", async () => {
    const clients = [
      "codex",
      "claude_code",
      "cursor",
      "opencode",
      "pi",
      "cline",
      "unknown",
    ] as const;
    const rewrites = [
      "model_redirect",
      "privacy",
      "image_compression",
      "global_prompt",
      "reasoning_effort",
      "conversion",
      "client_identity",
    ] as const;
    const heavy = clients.flatMap((client, at) =>
      Array.from({ length: 12 }, (_, index) =>
        route(client, `service_${index + 1}`, 4000 + at * 300 + index, {
          rewrites: [rewrites[(at + index) % rewrites.length]],
          ...(index % 4 === 0
            ? { failed_service_id: `service_${((index + 3) % 12) + 1}` }
            : {}),
          failed: index % 5 === 0,
        }),
      ),
    );
    serve(heavy.slice(0, 10), heavy);
    await render(Array.from({ length: 12 }, (_, index) => service(index + 1)));
    const month = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "近 30 天",
    )!;
    await act(async () => month.click());
    const sky = container.querySelector('[data-slot="request-sky"]')!;
    expect(all("request-sky-flare")).toHaveLength(7);
    expect(sky.querySelectorAll("svg *").length).toBeLessThan(200);
    expect(sky.querySelectorAll("*").length).toBeLessThan(400);
  });

  it("re-renders nothing static on clock ticks and activity polls", async () => {
    vi.useFakeTimers({
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "requestAnimationFrame",
        "cancelAnimationFrame",
        "performance",
      ],
    });
    serve([route("codex", "service_1", 3), route("cursor", "service_1", 1)]);
    let polls = 0;
    bridge.getRequestActivity.mockImplementation(async () => {
      polls += 1;
      return activity([
        call(`2026-11-02T07:5${polls % 10}:00Z`, {
          status: "pending",
          completed_at: null,
        }),
      ]);
    });
    await render([service(1)]);
    const icons = iconRenders.count;
    const time = container.querySelector("time")!;
    expect(icons).toBeGreaterThan(0);
    await act(async () => vi.advanceTimersByTimeAsync(31_000));
    expect(polls).toBeGreaterThan(20);
    expect(container.querySelector("time")).toBe(time);
    expect(iconRenders.count).toBe(icons);
  });

  it("flies comets only for calls that finish after the sky opened", async () => {
    vi.useFakeTimers({
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "requestAnimationFrame",
        "cancelAnimationFrame",
        "performance",
      ],
    });
    serve([route("codex", "service_1", 1)]);
    bridge.getRequestActivity
      .mockResolvedValueOnce(activity([call("2026-11-02T07:59:00Z")]))
      .mockResolvedValue(
        activity([
          call("2026-11-02T07:59:30Z", {
            rewrites: ["model_redirect", "client_identity"],
          }),
          call("2026-11-02T07:59:00Z"),
        ]),
      );
    await render([service(1)]);
    await act(async () => vi.advanceTimersByTimeAsync(100));
    expect(all("request-sky-comet")).toHaveLength(0);
    const icons = iconRenders.count;
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    await act(async () => vi.advanceTimersByTimeAsync(900));
    expect(all("request-sky-comet")).toHaveLength(1);
    // Neither flare was lit today, so each shows whole while the comet passes.
    expect(
      [...all("request-sky-flash")].map((flash) =>
        flash.getAttribute("data-kind"),
      ),
    ).toEqual(["model_redirect", "client_identity"]);
    expect(iconRenders.count).toBe(icons);
    const reads = bridge.getUsageSummary.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(3_000));
    expect(all("request-sky-comet")).toHaveLength(0);
    expect(all("request-sky-flash")).toHaveLength(0);
    // Once it lands, the sky reads the day again to keep the trail.
    expect(bridge.getUsageSummary.mock.calls.length).toBe(reads + 1);
  });

  it("keeps the trails still with reduced motion", async () => {
    reducedMotion = true;
    vi.useFakeTimers({
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "requestAnimationFrame",
        "cancelAnimationFrame",
        "performance",
      ],
    });
    serve([route("codex", "service_1", 1)]);
    bridge.getRequestActivity
      .mockResolvedValueOnce(activity([]))
      .mockResolvedValue(activity([call("2026-11-02T07:59:30Z")]));
    await render([service(1)]);
    await act(async () => vi.advanceTimersByTimeAsync(1_100));
    await act(async () => vi.advanceTimersByTimeAsync(500));
    expect(all("request-sky-comet")).toHaveLength(0);
    expect(all("request-sky-trails")).toHaveLength(1);
    // Without comets there is nothing to poll for.
    expect(bridge.getRequestActivity).not.toHaveBeenCalled();
  });
});
