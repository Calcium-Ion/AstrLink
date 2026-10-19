// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  getSessionChannelBindings: vi.fn(),
  releaseSessionChannelBindings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));
import { SessionChannelBindings } from "./SessionChannelBindings";
import { parseChannelBindingAudit } from "./channel-binding-model";
import { applyLocale } from "./i18n";

const scope = {
  session_id: "session_one",
  local_access_token_id: "token_one",
  protocol: "openai.chat",
  model: "public",
};
const binding = {
  ...scope,
  service_id: "service_b",
  source: "fingerprint",
  request_id: "request_one",
  updated_at: "2026-09-22T01:00:00Z",
  expires_at: "2099-09-22T02:00:00Z",
};
const event = {
  ...scope,
  id: 1,
  action: "switched",
  reason: "request_succeeded",
  source: "fingerprint",
  service_id: "service_b",
  previous_service_id: "service_a",
  request_id: "request_one",
  at: "2026-09-22T01:00:00Z",
};
let root: Root, container: HTMLDivElement;
beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.resetAllMocks();
  bridge.getSessionChannelBindings.mockResolvedValue({
    enabled: true,
    bindings: [binding],
    events: [event],
    has_more: false,
  });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
});

it("explains switching, opens the request and releases without removing history", async () => {
  const select = vi.fn();
  await act(async () =>
    root.render(
      <SessionChannelBindings
        sessionId="session_one"
        serviceNames={{
          service_a: "主 API 提供商",
          service_b: "备用 API 提供商",
        }}
        onSelectRequest={select}
      />,
    ),
  );
  expect(container.textContent).toContain("主 API 提供商 → 备用 API 提供商");
  expect(container.textContent).toContain("助手文本指纹匹配");
  // What re-selecting does is behind the ? icon, not under the title.
  expect(container.textContent).not.toContain("仍可能选中同一个");
  expect(
    container.querySelector('[aria-label="重新选择会怎样"]'),
  ).not.toBeNull();
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "查看这次请求")!
      .click(),
  );
  expect(select).toHaveBeenCalledWith("request_one");
  bridge.releaseSessionChannelBindings.mockResolvedValue({
    enabled: true,
    bindings: [],
    events: [
      {
        ...event,
        id: 2,
        action: "released",
        reason: "user_requested",
        source: "",
        service_id: "",
        previous_service_id: "",
        request_id: "",
      },
      event,
    ],
    has_more: false,
  });
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "重新选择")!
      .click(),
  );
  expect(bridge.releaseSessionChannelBindings).toHaveBeenCalledWith(
    "session_one",
  );
  expect(container.textContent).toContain("暂无沿用记录");
  expect(container.textContent).toContain("手动解除");
  expect(container.textContent).toContain("主 API 提供商 → 备用 API 提供商");
});

it("keeps the current binding visible when release fails and permits retry", async () => {
  bridge.releaseSessionChannelBindings.mockRejectedValue(
    Error("Core unavailable"),
  );
  await act(async () =>
    root.render(
      <SessionChannelBindings
        sessionId="session_one"
        serviceNames={{ service_b: "备用 API 提供商" }}
        onSelectRequest={vi.fn()}
      />,
    ),
  );
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "重新选择")!
      .click(),
  );
  expect(container.textContent).toContain("Core unavailable");
  expect(container.textContent).toContain("备用 API 提供商");
  expect(
    [...container.querySelectorAll("button")].find(
      (b) => b.textContent === "重新选择",
    )!.disabled,
  ).toBe(false);
});

it("validates audit input and rejects unsupported actions and invalid dates", () => {
  const data = {
    enabled: true,
    bindings: [binding],
    events: [event],
    has_more: false,
  };
  expect(parseChannelBindingAudit(data).events[0].action).toBe("switched");
  // An older Core sends no routing bindings; a newer one lists them.
  expect(parseChannelBindingAudit(data).model_routing_bindings).toEqual([]);
  expect(
    parseChannelBindingAudit({
      ...data,
      model_routing_bindings: [
        {
          local_access_token_id: "token_one",
          protocol: "openai.chat",
          virtual_model_id: "astrlink/program",
          service_id: "service_a",
          model: "gpt-6.1-sol",
          expires_at: "2099-09-22T02:00:00Z",
        },
      ],
    }).model_routing_bindings[0].virtual_model_id,
  ).toBe("astrlink/program");
  expect(() =>
    parseChannelBindingAudit({
      ...data,
      events: [{ ...event, action: "unknown" }],
    }),
  ).toThrow();
  expect(() =>
    parseChannelBindingAudit({
      ...data,
      bindings: [{ ...binding, expires_at: "invalid" }],
    }),
  ).toThrow();
});

it("keeps paged history stable when an earlier background poll finishes", async () => {
  vi.useFakeTimers();
  const latest = {
    enabled: true,
    bindings: [binding],
    events: [
      { ...event, id: 3 },
      { ...event, id: 2 },
    ],
    has_more: true,
  };
  bridge.getSessionChannelBindings.mockResolvedValueOnce(latest);
  await act(async () =>
    root.render(
      <SessionChannelBindings
        sessionId="session_one"
        serviceNames={{}}
        onSelectRequest={vi.fn()}
      />,
    ),
  );
  let finishPoll!: (value: typeof latest) => void;
  bridge.getSessionChannelBindings.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishPoll = resolve;
      }),
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000);
  });
  bridge.getSessionChannelBindings.mockResolvedValueOnce({
    ...latest,
    events: [event],
    has_more: false,
  });
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "加载更早记录")!
      .click(),
  );
  expect(bridge.getSessionChannelBindings).toHaveBeenLastCalledWith(
    "session_one",
    2,
  );
  expect(container.querySelectorAll("ol > li")).toHaveLength(3);
  await act(async () => {
    finishPoll({
      ...latest,
      events: [
        { ...event, id: 4 },
        { ...event, id: 3 },
      ],
    });
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(6000);
  });
  expect(container.querySelectorAll("ol > li")).toHaveLength(3);
  expect(bridge.getSessionChannelBindings).toHaveBeenCalledTimes(3);
  bridge.getSessionChannelBindings.mockResolvedValueOnce({
    ...latest,
    events: [{ ...event, id: 4 }],
    has_more: false,
  });
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "刷新")!
      .click(),
  );
  expect(container.querySelectorAll("ol > li")).toHaveLength(1);
});

it("lists the routing group's kept model next to the API provider and clears both", async () => {
  bridge.getSessionChannelBindings.mockResolvedValue({
    enabled: true,
    bindings: [binding],
    model_routing_bindings: [
      {
        local_access_token_id: "token_one",
        protocol: "openai.chat",
        virtual_model_id: "astrlink/program",
        service_id: "service_a",
        model: "gpt-6.1-sol",
        request_id: "request_origin",
        expires_at: "2099-09-22T02:00:00Z",
      },
    ],
    events: [],
    has_more: false,
  });
  await act(async () =>
    root.render(
      <SessionChannelBindings
        sessionId="session_one"
        serviceNames={{
          service_a: "subapi-claude",
          service_b: "备用 API 提供商",
        }}
        onSelectRequest={vi.fn()}
      />,
    ),
  );
  const routed = container.querySelector(
    '[data-routing-binding="astrlink/program"]',
  );
  expect(routed?.textContent).toContain("astrlink/program");
  expect(routed?.textContent).toContain("gpt-6.1-sol");
  expect(routed?.textContent).toContain("subapi-claude");
  expect(container.textContent).toContain("备用 API 提供商");

  bridge.releaseSessionChannelBindings.mockResolvedValue({
    enabled: true,
    bindings: [],
    model_routing_bindings: [],
    events: [],
    has_more: false,
  });
  await act(async () =>
    [...container.querySelectorAll("button")]
      .find((b) => b.textContent === "重新选择")!
      .click(),
  );
  expect(container.querySelector("[data-routing-binding]")).toBeNull();
  expect(container.textContent).toContain("暂无沿用记录");
});

it("marks the list off when session stickiness is disabled", async () => {
  bridge.getSessionChannelBindings.mockResolvedValue({
    enabled: false,
    bindings: [binding],
    model_routing_bindings: [],
    events: [],
    has_more: false,
  });
  await act(async () =>
    root.render(
      <SessionChannelBindings
        sessionId="session_one"
        serviceNames={{ service_b: "备用 API 提供商" }}
        onSelectRequest={vi.fn()}
      />,
    ),
  );
  expect(container.querySelector("[data-binding-disabled]")?.textContent).toBe(
    "已关闭",
  );
});
