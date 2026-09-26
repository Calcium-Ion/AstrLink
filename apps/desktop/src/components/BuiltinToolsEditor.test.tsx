// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { defaultBuiltinTools, type BuiltinTools } from "@/builtin-tools-model";
import { applyLocale } from "@/i18n";
import type { RoutableService } from "@/service-model";
import { BuiltinToolsEditor } from "./BuiltinToolsEditor";

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
  if (!item) return options;
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  return options;
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
  await choose("图片生成执行方", "直接调用提供商的绘图接口");
  expect(latest?.image_generation).toEqual({
    enabled: true,
    backend: "service_images",
    service_id: "newapi_main",
    model: "",
  });
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
  const providers = (await choose("图片生成提供商", "")).join("\n");
  expect(providers).toContain("绘图站");
  expect(providers).not.toContain("Claude");
  await act(async () =>
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
  );
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
  await choose("图片生成执行方", "自定义绘图 API（OpenAI Images 兼容）");
  expect(latest?.image_generation).toMatchObject({
    backend: "external",
    model: "gpt-image-1",
  });
  await choose("图片生成执行方", "由上游模型调用内置工具");
  expect(latest?.image_generation).toMatchObject({
    backend: "upstream",
    service_id: "newapi_main",
    model: "",
  });
  expect(field("图片生成绘图模型")).toBeNull();
});
