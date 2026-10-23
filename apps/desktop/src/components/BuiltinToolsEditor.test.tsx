// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { defaultBuiltinTools, type BuiltinTools } from "@/builtin-tools-model";
import { applyLocale } from "@/i18n";
import type { RoutableService } from "@/service-model";
import { BuiltinToolsEditor } from "./BuiltinToolsEditor";
import { settleClosedLayer } from "../lib/test-layer-settle";

const action = vi.hoisted(() => vi.fn());
vi.mock("@/bridge", () => ({ builtinToolAction: action }));
let root: Root, container: HTMLDivElement;
beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  action
    .mockReset()
    .mockResolvedValue({ configured: false, ok: true, duration_ms: 12 });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});
it("keeps tests explicit and stores keys outside routing configuration", async () => {
  const changed = vi.fn();
  function Editor() {
    const [value, setValue] = useState(() => ({
      ...defaultBuiltinTools(),
      web_search: {
        enabled: false,
        backend: "external" as const,
        base_url: "https://search.example",
      },
    }));
    return (
      <BuiltinToolsEditor
        value={value}
        services={[]}
        disabled={false}
        onChange={(next) => {
          changed(next);
          setValue(next as typeof value);
        }}
      />
    );
  }
  await act(async () => root.render(<Editor />));
  expect(action.mock.calls.every((call) => call[1] === "status")).toBe(true);
  const toggle = container.querySelector<HTMLButtonElement>(
    '[aria-label="启用网页搜索接管"]',
  )!;
  await act(async () => toggle.click());
  expect(changed).toHaveBeenCalled();
  expect(action.mock.calls.some((call) => call[1] === "test")).toBe(false);
  const input = container.querySelector<HTMLInputElement>(
    'input[type="password"]',
  )!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, "secret-key");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const save = [
    ...container.querySelectorAll<HTMLButtonElement>("button"),
  ].find((button) => button.textContent === "保存密钥")!;
  await act(async () => save.click());
  expect(action).toHaveBeenCalledWith("web_search", "save_key", {
    secret: "secret-key",
  });
  expect(JSON.stringify(changed.mock.calls)).not.toContain("secret-key");
  const test = [
    ...container.querySelectorAll<HTMLButtonElement>("button"),
  ].find((button) => button.textContent === "测试工具")!;
  await act(async () => test.click());
  expect(action.mock.calls.filter((call) => call[1] === "test")).toHaveLength(
    1,
  );
  expect(container.textContent).toContain("工具可用");
});

async function choose(label: string, option: string) {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[role="combobox"][aria-label="${label}"]`,
  )!;
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const options = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].map((item) => item.textContent?.trim());
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (option)
    expect(item, `Available options: ${options.join(", ")}`).toBeDefined();
  if (!item) return options;
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  await settleClosedLayer();
  return options;
}

async function enter(label: string, value: string) {
  const input = container.querySelector<HTMLInputElement>(
    `input[aria-label="${label}"]`,
  )!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () =>
    input.dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
  );
}

it("separates the provider Images API from a chat model executing the tool", async () => {
  const responses = { protocol: "openai.responses", mode: "native" as const };
  const services: RoutableService[] = [
    {
      id: "newapi_main",
      name: "主站",
      kind: "newapi",
      enabled: true,
      models: ["gpt-5", "gpt-image-1"],
      capabilities: [{ ...responses, streaming: true }],
    },
    {
      id: "images_only",
      name: "绘图站",
      kind: "openai_compatible",
      enabled: true,
      models: ["gpt-image-1"],
      capabilities: [],
    },
    {
      id: "claude_main",
      name: "Claude",
      kind: "anthropic",
      enabled: true,
      models: ["claude-sonnet"],
      capabilities: [{ ...responses, streaming: true }],
    },
  ];
  let latest: BuiltinTools | undefined;
  function Editor() {
    const [value, setValue] = useState<BuiltinTools>(() => ({
      ...defaultBuiltinTools(),
      image_generation: {
        enabled: true,
        backend: "upstream",
        service_id: "newapi_main",
        model: "gpt-5",
      },
    }));
    return (
      <BuiltinToolsEditor
        value={value}
        services={services}
        disabled={false}
        onChange={(next) => {
          latest = next;
          setValue(next);
        }}
      />
    );
  }
  const field = (label: string) =>
    container.querySelector<HTMLInputElement>(`[aria-label="${label}"]`);
  await act(async () => root.render(<Editor />));
  expect(field("图片生成执行模型")?.value).toBe("gpt-5");
  expect(field("图片生成绘图模型")).toBeNull();
  await choose("图片生成执行方", "提供商的 Images API");
  expect(latest?.image_generation).toEqual({
    enabled: true,
    backend: "service_images",
    service_id: "",
    model: "",
  });
  await choose("图片生成提供商", "主站");
  expect(field("图片生成执行模型")).toBeNull();
  expect(container.textContent).not.toContain("API 密钥");
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(field("图片生成绘图模型"), "gpt-image-1");
    field("图片生成绘图模型")!.dispatchEvent(
      new Event("input", { bubbles: true }),
    );
  });
  expect(latest?.image_generation.model).toBe("");
  await act(async () =>
    field("图片生成绘图模型")!.dispatchEvent(
      new FocusEvent("focusout", { bubbles: true }),
    ),
  );
  expect(latest?.image_generation.model).toBe("gpt-image-1");
  const providers = (await choose("图片生成提供商", "")).join("\n");
  expect(providers).toContain("绘图站");
  expect(providers).not.toContain("Claude");
  await act(async () =>
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
  );
  await settleClosedLayer();
  const test = [
    ...container.querySelectorAll<HTMLButtonElement>("button"),
  ].filter((button) => button.textContent === "测试工具")[1]!;
  await act(async () => test.click());
  expect(action).toHaveBeenCalledWith("image_generation", "test", {
    enabled: true,
    backend: "service_images",
    service_id: "newapi_main",
    model: "gpt-image-1",
  });
  await choose("图片生成执行方", "自定义 Images API");
  expect(latest?.image_generation).toMatchObject({
    backend: "external",
    base_url: "",
    model: "",
  });
  await enter("图片生成 API 地址", "https://images.example/v1");
  await enter("图片生成绘图模型", "image-custom");
  await choose("图片生成执行方", "Responses · image_generation");
  expect(latest?.image_generation).toEqual({
    enabled: true,
    backend: "upstream",
    service_id: "newapi_main",
    model: "gpt-5",
  });
  expect(field("图片生成绘图模型")).toBeNull();
  await choose("图片生成执行方", "提供商的 Images API");
  expect(latest?.image_generation).toEqual({
    enabled: true,
    backend: "service_images",
    service_id: "newapi_main",
    model: "gpt-image-1",
  });
  await choose("图片生成执行方", "自定义 Images API");
  expect(latest?.image_generation).toEqual({
    enabled: true,
    backend: "external",
    base_url: "https://images.example/v1",
    model: "image-custom",
  });
  await act(async () => test.click());
  expect(action).toHaveBeenLastCalledWith("image_generation", "test", {
    enabled: true,
    backend: "external",
    base_url: "https://images.example/v1",
    model: "image-custom",
  });
});

it("offers Codex subscriptions and New API for the provider search API", async () => {
  const responses = {
    protocol: "openai.responses",
    mode: "native" as const,
    streaming: true,
  };
  const services: RoutableService[] = [
    {
      id: "newapi_main",
      name: "主站",
      kind: "newapi",
      enabled: true,
      models: ["gpt-5.5"],
      capabilities: [responses],
    },
    {
      id: "codex_team",
      name: "Codex 团队",
      kind: "codex_subscription",
      enabled: true,
      models: ["gpt-5.6-sol"],
      capabilities: [responses],
    },
    {
      id: "claude_main",
      name: "Claude",
      kind: "anthropic",
      enabled: true,
      models: ["claude-sonnet"],
      capabilities: [responses],
    },
  ];
  let latest: BuiltinTools | undefined;
  function Editor({ services }: { services: RoutableService[] }) {
    const [value, setValue] = useState<BuiltinTools>(() => ({
      ...defaultBuiltinTools(),
      web_search: {
        enabled: true,
        backend: "upstream",
        service_id: "newapi_main",
        model: "gpt-5.5",
      },
    }));
    return (
      <BuiltinToolsEditor
        value={value}
        services={services}
        disabled={false}
        onChange={(next) => {
          latest = next;
          setValue(next);
        }}
      />
    );
  }
  await act(async () => root.render(<Editor services={services} />));
  await choose("网页搜索执行方", "提供商的搜索 API");
  // A new backend starts independently, even if the old provider supports it.
  expect(latest?.web_search).toEqual({
    enabled: true,
    backend: "service_search",
    service_id: "",
    model: "",
  });
  expect(container.textContent).not.toContain("API 密钥");
  const providers = (await choose("网页搜索提供商", "")).join("\n");
  expect(providers).toContain("主站");
  expect(providers).toContain("Codex 团队");
  expect(providers).not.toContain("Claude");
  await act(async () =>
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
  );
  await settleClosedLayer();
  await choose("网页搜索提供商", "CodexCodex 团队");
  await enter("网页搜索执行模型", "gpt-5.6-sol");
  const test = [
    ...container.querySelectorAll<HTMLButtonElement>("button"),
  ].filter((button) => button.textContent === "测试工具")[0]!;
  await act(async () => test.click());
  expect(action).toHaveBeenCalledWith("web_search", "test", {
    enabled: true,
    backend: "service_search",
    service_id: "codex_team",
    model: "gpt-5.6-sol",
  });

  await choose("网页搜索执行方", "Tavily 兼容 API");
  expect(latest?.web_search).toEqual({
    enabled: true,
    backend: "external",
    base_url: "",
  });
  await enter("网页搜索 API 地址", "https://search.example");
  await enter("网页搜索 API 密钥", "unsaved-search-key");
  await choose("网页搜索执行方", "Responses · web_search");
  expect(latest?.web_search).toEqual({
    enabled: true,
    backend: "upstream",
    service_id: "newapi_main",
    model: "gpt-5.5",
  });
  expect(test.disabled).toBe(false);
  await act(async () => test.click());
  expect(action).toHaveBeenLastCalledWith("web_search", "test", {
    enabled: true,
    backend: "upstream",
    service_id: "newapi_main",
    model: "gpt-5.5",
  });
  await choose("网页搜索执行方", "提供商的搜索 API");
  expect(latest?.web_search).toEqual({
    enabled: true,
    backend: "service_search",
    service_id: "codex_team",
    model: "gpt-5.6-sol",
  });
  await choose("网页搜索执行方", "Tavily 兼容 API");
  expect(latest?.web_search).toEqual({
    enabled: true,
    backend: "external",
    base_url: "https://search.example",
  });
  expect(
    container.querySelector<HTMLInputElement>('input[type="password"]')?.value,
  ).toBe("unsaved-search-key");
  await enter("网页搜索 API 密钥", "");
  await act(async () => test.click());
  expect(action).toHaveBeenLastCalledWith("web_search", "test", {
    enabled: true,
    backend: "external",
    base_url: "https://search.example",
  });
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>('[aria-label="启用网页搜索接管"]')!
      .click(),
  );
  await choose("网页搜索执行方", "提供商的搜索 API");
  expect(latest?.web_search.enabled).toBe(false);
  await choose("网页搜索执行方", "Tavily 兼容 API");
  await act(async () => root.render(<Editor services={[services[2]!]} />));
  await choose("网页搜索执行方", "提供商的搜索 API");
  expect(latest?.web_search).toEqual({
    enabled: false,
    backend: "service_search",
    service_id: "",
    model: "",
  });
  expect(container.textContent).toContain(
    "暂无已启用的 Codex 订阅或 New API 提供商",
  );
});

it("passes on typed values only when committed and flags an incomplete tool", async () => {
  const services: RoutableService[] = [
    {
      id: "codex_main",
      name: "订阅",
      kind: "codex_subscription",
      enabled: true,
      models: ["gpt-5.6-sol"],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
      ],
    },
  ];
  const changed = vi.fn();
  function Editor() {
    const [value, setValue] = useState<BuiltinTools>(defaultBuiltinTools);
    return (
      <BuiltinToolsEditor
        value={value}
        services={services}
        disabled={false}
        onChange={(next) => {
          changed(next);
          setValue(next);
        }}
      />
    );
  }
  const field = (label: string) =>
    container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
  const type = async (input: HTMLInputElement, text: string) =>
    act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, text);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  const warning = () =>
    [...container.querySelectorAll('[role="status"]')].find((element) =>
      element.textContent?.startsWith("请填写"),
    )?.textContent;
  await act(async () => root.render(<Editor />));
  expect(warning()).toBeUndefined();
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>('[aria-label="启用网页搜索接管"]')!
      .click(),
  );
  expect(warning()).toContain("API 提供商和执行模型");
  await choose("网页搜索提供商", "Codex订阅");
  await settleClosedLayer();
  expect(warning()).toContain("请填写执行模型");

  await type(field("网页搜索执行模型"), "gpt-5");
  expect(field("网页搜索执行模型").value).toBe("gpt-5");
  expect(changed).toHaveBeenLastCalledWith(
    expect.objectContaining({
      web_search: {
        enabled: true,
        backend: "upstream",
        service_id: "codex_main",
        model: "",
      },
    }),
  );
  await type(field("网页搜索执行模型"), " gpt-5.6-sol ");
  await act(async () =>
    field("网页搜索执行模型").dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    ),
  );
  expect(changed.mock.lastCall?.[0].web_search.model).toBe("gpt-5.6-sol");
  expect(field("网页搜索执行模型").value).toBe("gpt-5.6-sol");
  expect(warning()).toBeUndefined();

  await choose("网页搜索执行方", "Tavily 兼容 API");
  await settleClosedLayer();
  const calls = changed.mock.calls.length;
  await type(field("网页搜索 API 地址"), "https://api.tav");
  expect(changed).toHaveBeenCalledTimes(calls);
  expect(warning()).toContain("请填写API 基础地址");
  await type(field("网页搜索 API 地址"), "https://api.tavily.com");
  await act(async () =>
    field("网页搜索 API 地址").dispatchEvent(
      new FocusEvent("focusout", { bubbles: true }),
    ),
  );
  expect(changed.mock.lastCall?.[0].web_search.base_url).toBe(
    "https://api.tavily.com",
  );
  expect(warning()).toBeUndefined();
});

it("offers MiniMax and its image models for the provider Images API", async () => {
  const services: RoutableService[] = [
    {
      id: "minimax_plan",
      name: "MiniMax 订阅",
      kind: "minimax_coding",
      enabled: true,
      models: ["MiniMax-M3"],
      capabilities: [],
    },
  ];
  function Editor() {
    const [value, setValue] = useState<BuiltinTools>(() => ({
      ...defaultBuiltinTools(),
      image_generation: {
        enabled: true,
        backend: "service_images",
        service_id: "minimax_plan",
        model: "",
      },
    }));
    return (
      <BuiltinToolsEditor
        value={value}
        services={services}
        disabled={false}
        onChange={setValue}
      />
    );
  }
  await act(async () => root.render(<Editor />));
  expect((await choose("图片生成提供商", "")).join("\n")).toContain(
    "MiniMax 订阅",
  );
  await act(async () =>
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
  );
  // Radix hands focus back to the provider trigger a tick after closing; a
  // model list opened before that is dismissed as focus moving outside it.
  await settleClosedLayer();
  await act(async () =>
    container
      .querySelector<HTMLInputElement>('[aria-label="图片生成绘图模型"]')!
      .click(),
  );
  expect(
    [...document.querySelectorAll('[role="option"]')].map((option) =>
      option.getAttribute("aria-label"),
    ),
  ).toEqual(["image-01", "image-01-live"]);
});
