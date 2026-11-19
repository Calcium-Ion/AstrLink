// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getPluginHostSettings: vi.fn(),
  updatePluginHostSettings: vi.fn(),
  dryRunRequestPlugin: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import type { PluginDetail, PluginHostSettings } from "./plugin-model";
import { RequestPluginInstances } from "./RequestPluginInstances";

const detail = {
  key: "request-filter",
  name: "Request filter",
  enabled: true,
  kind: "request",
  request: {
    stages: ["ingress", "egress", "reply"],
    config_schema: {
      type: "object",
      properties: {
        keywords: {
          type: "string",
          format: "keywords",
          title: { en: "Keywords", zh: "关键词" },
          default: "",
        },
        replies: {
          type: "boolean",
          title: { en: "Check replies", zh: "检查回复" },
          default: false,
        },
        mode: {
          type: "string",
          title: { en: "Mode", zh: "方式" },
          enum: ["reject", "off"],
          enumTitles: [{ zh: "拒绝" }, { zh: "关闭" }],
          default: "reject",
        },
      },
    },
  },
} as unknown as PluginDetail;

const host: PluginHostSettings = {
  paused: false,
  injection_budget_bytes: 16384,
  language: "zh-CN",
  request_plugins: [
    {
      id: "rpi_other0001",
      name: "别的插件",
      plugin: "request-redact",
      enabled: true,
      models: [],
    },
  ],
};

let container: HTMLDivElement;
let root: Root;
let removeAnimations: () => void;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  removeAnimations = installDialogAnimations();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  bridgeMocks.getPluginHostSettings.mockResolvedValue(host);
  bridgeMocks.updatePluginHostSettings.mockImplementation(
    async (update: Partial<PluginHostSettings>) => ({
      ...host,
      request_plugins: (update.request_plugins ?? []).map(
        (instance, index) => ({
          id: instance.id ?? `rpi_new0000${index}`,
          ...instance,
        }),
      ),
    }),
  );
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  removeAnimations();
  vi.clearAllMocks();
});

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) =>
      candidate.textContent?.trim() === label ||
      candidate.getAttribute("aria-label") === label,
  );
  if (!(match instanceof HTMLButtonElement))
    throw new Error(`Missing button: ${label}`);
  return match;
}

async function type(
  element: HTMLInputElement | HTMLTextAreaElement,
  value: string,
) {
  const prototype =
    element instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  await act(async () => {
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("RequestPluginInstances", () => {
  it("adds an instance with the settings the schema describes and keeps the others", async () => {
    await act(async () => {
      root.render(<RequestPluginInstances detail={detail} />);
    });
    const panel = document.querySelector(
      '[data-testid="request-plugin-instances"]',
    ) as HTMLElement;
    expect(panel.textContent).toContain("入口检查");
    expect(panel.textContent).toContain("回复检查");
    // Another plugin's instance is not this plugin's.
    expect(
      panel.querySelectorAll('[data-testid="request-plugin-instance"]'),
    ).toHaveLength(0);
    await act(async () => button("添加实例").click());
    const editor = document.querySelector(
      '[data-testid="request-plugin-editor"]',
    ) as HTMLElement;
    expect(editor.textContent).toContain("关键词");
    expect(editor.textContent).toContain("拒绝");
    const [name, models, keywords] = [
      ...editor.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(
        "input, textarea",
      ),
    ];
    await type(name, "敏感词");
    await type(models, "gpt-4o, gpt-4o-mini\ngpt-4o");
    await type(keywords, "a\nb");
    await act(async () => {
      editor.querySelector<HTMLButtonElement>('[role="switch"]')!.click();
    });
    await act(async () => button("保存").click());
    await finishExitAnimations();
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenCalledWith({
      request_plugins: [
        host.request_plugins[0],
        {
          name: "敏感词",
          plugin: "request-filter",
          enabled: true,
          models: ["gpt-4o", "gpt-4o-mini"],
          config: { keywords: "a\nb", replies: true, mode: "reject" },
        },
      ],
    });
    expect(
      panel.querySelectorAll('[data-testid="request-plugin-instance"]'),
    ).toHaveLength(1);
    expect(panel.textContent).toContain("gpt-4o, gpt-4o-mini");
  });

  it("runs a sample through the instance being edited without sending it", async () => {
    bridgeMocks.dryRunRequestPlugin.mockResolvedValue({
      stopped: {
        status: 400,
        code: "request_rejected",
        message: "Not allowed (code: request_filtered)",
      },
      records: [
        {
          plugin: "request-filter",
          plugin_version: "1.0.0",
          hook: "ingress",
          title: "x",
          status: "ok",
          summary: "rejected: request_filtered",
          elapsed_ms: 1,
          network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
        },
      ],
      failures: [],
    });
    const withSamples = {
      ...detail,
      request: {
        ...detail.request,
        samples: [
          {
            name: { zh: "关键词" },
            format: "claude",
            body: {
              model: "claude",
              messages: [{ role: "user", content: "a b" }],
            },
            reply: "ok",
          },
        ],
      },
    } as unknown as PluginDetail;
    await act(async () => {
      root.render(<RequestPluginInstances detail={withSamples} />);
    });
    await act(async () => button("添加实例").click());
    await act(async () => button("试运行").click());
    expect(bridgeMocks.dryRunRequestPlugin).toHaveBeenCalledWith(
      "request-filter",
      {
        config: { keywords: "", replies: false, mode: "reject" },
        format: "claude",
        body: { model: "claude", messages: [{ role: "user", content: "a b" }] },
        reply: "ok",
      },
    );
    const result = document.querySelector(
      '[data-testid="request-plugin-dry-run-result"]',
    ) as HTMLElement;
    expect(result.textContent).toContain(
      "Not allowed (code: request_filtered)",
    );
    expect(result.textContent).toContain("入口检查");
  });
});
