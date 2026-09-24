// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  getRoutingSettings: vi.fn(),
  listRecoveryPaths: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));
import {
  defaultFailurePolicy,
  identitySettingKeys,
  type FailurePolicy,
  type FailoverPolicy,
} from "./failure-policy-model";
import { RoutingSettingsPanel } from "./RoutingSettingsPanel";
import { FailoverEditor } from "./components/FailoverEditor";
import { RecoveryChain, RecoveryDetails } from "./components/RecoveryDetails";
import type { RequestRecord } from "./request-record-model";
import { applyLocale, i18n } from "./i18n";

describe("shared global recovery settings", () => {
  let container: HTMLDivElement, root: Root;
  const settings = () => ({
    default_failure_policy: { ...defaultFailurePolicy(), max_retries: 3 },
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
  });
  beforeEach(async () => {
    await applyLocale("zh-CN");
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.listRecoveryPaths.mockResolvedValue([]);
    bridge.getRoutingSettings.mockReset().mockResolvedValue(settings());
    bridge.updateRoutingSettings
      .mockReset()
      .mockImplementation(async (value) => ({ ...settings(), ...value }));
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function selectTab(label: string) {
    const tab = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
    ].find((element) => element.textContent === label)!;
    await act(async () => tab.click());
  }

  function retryInput() {
    return [...container.querySelectorAll("label")]
      .find(
        (label) =>
          label.querySelector(":scope > span")?.textContent === "最多重试几次",
      )!
      .querySelector<HTMLInputElement>("input")!;
  }

  it.each(["Codex", "Claude", "Grok"])(
    "saves %s identity independently through Routing and retains a failed draft",
    async (provider) => {
      const dirty = vi.fn();
      await act(async () =>
        root.render(
          <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
        ),
      );
      await selectTab("转发身份");
      const toggles = [
        ...container.querySelectorAll<HTMLButtonElement>('[role="switch"]'),
      ];
      expect(toggles).toHaveLength(3);
      for (const toggle of toggles)
        expect(toggle.getAttribute("aria-checked")).toBe("true");
      const toggle = container.querySelector<HTMLButtonElement>(
        `[aria-label="统一 ${provider} 客户端身份"]`,
      )!;
      await act(async () => toggle.click());
      expect(dirty).toHaveBeenLastCalledWith(true);
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
      await selectTab("会话粘性");
      await selectTab("转发身份");
      expect(
        container
          .querySelector(`[aria-label="统一 ${provider} 客户端身份"]`)
          ?.getAttribute("aria-checked"),
      ).toBe("false");
      const save = [...container.querySelectorAll("button")].find(
        (button) => button.textContent === "保存默认策略",
      )!;
      bridge.updateRoutingSettings.mockRejectedValueOnce(new Error("保存失败"));
      await act(async () => save.click());
      expect(container.textContent).toContain("保存失败");
      expect(dirty).toHaveBeenLastCalledWith(true);
      await act(async () => save.click());
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        [`${provider.toLowerCase()}_identity_enforcement`]: false,
      });
      expect(dirty).toHaveBeenLastCalledWith(false);
    },
  );

  it("loads saved identity opt-outs and keeps them disabled while offline", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      ...Object.fromEntries(identitySettingKeys.map((key) => [key, false])),
    });
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    await selectTab("转发身份");
    for (const toggle of container.querySelectorAll('[role="switch"]'))
      expect(toggle.getAttribute("aria-checked")).toBe("false");
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={[]}
          ready={false}
          onDirtyChange={dirty}
        />,
      ),
    );
    expect(
      container.querySelector<HTMLFieldSetElement>("fieldset")?.disabled,
    ).toBe(true);
    expect(
      [...container.querySelectorAll("button")].find(
        (button) => button.textContent === "保存默认策略",
      )?.disabled,
    ).toBe(true);
  });

  it("loads and saves a single policy for all services", async () => {
    const dirty = vi.fn();
    await act(async () => {
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      );
    });
    await selectTab("恢复与重试");
    expect(container.textContent).toContain("在这里配置一次");
    const input = retryInput();
    expect(input.value).toBe("3");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "4");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dirty).toHaveBeenLastCalledWith(true);
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存默认策略",
    )!;
    await act(async () => save.click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...settings().default_failure_policy,
        max_retries: 4,
      },
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it.each([undefined, true, false])(
    "defaults session stickiness on and preserves an explicit %s setting",
    async (enabled) => {
      bridge.getRoutingSettings.mockResolvedValue({
        ...settings(),
        ...(enabled === undefined
          ? {}
          : { channel_stickiness: { enabled, ttl_seconds: 3600 } }),
      });
      const dirty = vi.fn();
      await act(async () =>
        root.render(
          <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
        ),
      );
      await selectTab("会话粘性");
      const heading = [...container.querySelectorAll("h2")].find(
        (h) => h.textContent === "同一会话优先复用 API 提供商",
      )!;
      const toggle = container.querySelector<HTMLButtonElement>(
        `[role="switch"][aria-labelledby="${heading.id}"]`,
      )!;
      expect(toggle.getAttribute("aria-checked")).toBe(String(enabled ?? true));
      expect(dirty).toHaveBeenLastCalledWith(false);
      await act(async () => toggle.click());
      expect(container.textContent?.includes("闲置过期时间（分钟）")).toBe(
        enabled === false,
      );
      await act(async () =>
        [...container.querySelectorAll("button")]
          .find((b) => b.textContent === "保存默认策略")!
          .click(),
      );
      expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
        channel_stickiness: { enabled: enabled === false, ttl_seconds: 3600 },
      });
    },
  );

  it("retries loading and preserves an unsaved draft across reconnection", async () => {
    bridge.getRoutingSettings.mockRejectedValueOnce(Error("Core unavailable"));
    const onDirtyChange = vi.fn();
    const render = async (ready: boolean) =>
      act(async () =>
        root.render(
          <RoutingSettingsPanel
            services={[]}
            ready={ready}
            onDirtyChange={onDirtyChange}
          />,
        ),
      );
    await render(true);
    expect(container.textContent).toContain("Core unavailable");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重试")!
        .click(),
    );
    await selectTab("恢复与重试");
    const input = retryInput();
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "5");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await render(false);
    await render(true);
    expect(input.value).toBe("5");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        default_failure_policy: expect.objectContaining({ max_retries: 5 }),
      }),
    );
  });

  it("changes unmatched failover independently of the global attempt budget", async () => {
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={() => {}} />,
      ),
    );
    await selectTab("恢复与重试");
    expect(
      [...container.querySelectorAll("h2")].map(
        (element) => element.textContent,
      ),
    ).toEqual(["失败恢复与切换", "重试次数与等待时间", "推理内容修复"]);
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="失败后允许自动换 API 提供商"]',
        )!
        .click(),
    );
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      allow_unmatched_failover: true,
    });
  });

  it("saves the thinking signature switch and labels repair attempts", async () => {
    await act(async () => {
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      );
    });
    await selectTab("恢复与重试");
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="思考签名修复重试"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存默认策略",
    )!;
    await act(async () => save.click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...settings().default_failure_policy,
        thinking_signature_recovery: false,
      },
    });
    await act(async () => {
      root.render(
        <RecoveryDetails
          value={{
            action: "retry",
            reason: "thinking_signature_repair",
            delay_ms: 0,
          }}
        />,
      );
    });
    expect(container.textContent).toContain("思考签名修复");
    expect(container.textContent).not.toContain("thinking_signature_repair");
  });

  it("saves OpenAI repair independently and labels its attempts", async () => {
    await act(async () => {
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      );
    });
    await selectTab("恢复与重试");
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="Codex / OpenAI 推理修复重试"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    expect(
      container
        .querySelector('[role="switch"][aria-label="思考签名修复重试"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存默认策略",
    )!;
    await act(async () => save.click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...settings().default_failure_policy,
        openai_reasoning_recovery: false,
      },
    });
    await act(async () => {
      root.render(
        <RecoveryDetails
          value={{
            action: "retry",
            reason: "openai_reasoning_repair",
            delay_ms: 0,
          }}
        />,
      );
    });
    expect(container.textContent).toContain("OpenAI 推理修复");
    expect(container.textContent).not.toContain("openai_reasoning_repair");
  });

  it("saves the opt-in function-output switch and labels its attempts", async () => {
    await act(async () => {
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      );
    });
    await selectTab("恢复与重试");
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="允许修复函数输出密文"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(
      container
        .querySelector(
          '[role="switch"][aria-label="Codex / OpenAI 推理修复重试"]',
        )
        ?.getAttribute("aria-checked"),
    ).toBe("true");
    await act(async () => toggle.click());
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存默认策略",
    )!;
    await act(async () => save.click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...settings().default_failure_policy,
        openai_function_output_recovery: true,
      },
    });
    await act(async () => {
      root.render(
        <RecoveryDetails
          value={{
            action: "retry",
            reason: "openai_function_output_repair",
            delay_ms: 0,
          }}
        />,
      );
    });
    expect(container.textContent).toContain("OpenAI 函数输出修复");
    expect(container.textContent).not.toContain(
      "openai_function_output_repair",
    );
  });

  it("keeps drafts across tabs, scopes reset to the current group, and saves all edits together", async () => {
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    expect(
      [...container.querySelectorAll('[role="tab"]')].map(
        (tab) => tab.textContent,
      ),
    ).toEqual(["模型重定向", "恢复与重试", "错误规则", "会话粘性", "转发身份"]);
    await selectTab("恢复与重试");
    await act(async () =>
      container.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
    );
    await act(async () => {
      const input = retryInput();
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "4");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await selectTab("恢复与重试");
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="思考签名修复重试"]',
        )!
        .click(),
    );
    await selectTab("错误规则");
    await selectTab("恢复与重试");
    expect(retryInput().value).toBe("4");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重置此组设置")!
        .click(),
    );
    expect(retryInput().value).toBe("1");
    await selectTab("恢复与重试");
    expect(
      container
        .querySelector('[role="switch"][aria-label="思考签名修复重试"]')!
        .getAttribute("aria-checked"),
    ).toBe("false");
    await selectTab("错误规则");
    expect(container.textContent).toContain("HTTP 429");
    expect(container.textContent).not.toContain("最多重试几次");
    await selectTab("恢复与重试");
    expect(
      container.querySelector('[role="switch"]')!.getAttribute("aria-checked"),
    ).toBe("true");
    expect(dirty).toHaveBeenLastCalledWith(true);
    expect(bridge.getRoutingSettings).toHaveBeenCalledTimes(1);
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...defaultFailurePolicy(),
        thinking_signature_recovery: false,
      },
      allow_unmatched_failover: true,
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  const services = [
    {
      id: "service_a",
      name: "A",
      enabled: true,
      models: ["gpt-5", "claude-sonnet-4-5"],
      capabilities: [],
    },
    {
      id: "service_b",
      name: "B",
      enabled: true,
      models: ["gpt-5"],
      capabilities: [],
    },
    {
      id: "service_off",
      name: "Off",
      enabled: false,
      models: ["gpt-6"],
      capabilities: [],
    },
  ];

  function redirectField(label: string) {
    return container.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${label}"]`,
    )!;
  }

  async function typeRedirect(label: string, value: string) {
    await act(async () => {
      const input = redirectField(label);
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  function button(label: string) {
    return [...container.querySelectorAll("button")].find(
      (element) => element.textContent === label,
    )!;
  }

  it("opens on model redirects and saves only the replaced redirect list", async () => {
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={dirty}
        />,
      ),
    );
    const tabs = [...container.querySelectorAll('[role="tab"]')];
    expect(tabs[0].textContent).toBe("模型重定向");
    expect(tabs[0].getAttribute("aria-selected")).toBe("true");
    expect(container.textContent).toContain("Codex 自动审查");
    // A document without model_redirects is not a pending change.
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(button("保存默认策略").disabled).toBe(true);

    await act(async () => button("添加重定向").click());
    await act(async () => redirectField("第 1 条规则的请求模型").click());
    expect(
      [...document.querySelectorAll('[role="option"]')].map((option) =>
        option.getAttribute("aria-label"),
      ),
    ).toEqual(["claude-sonnet-4-5", "gpt-5", "astrlink/auto"]);
    await typeRedirect("第 1 条规则的请求模型", "gpt-4o");
    await typeRedirect("第 1 条规则的目标模型", "gpt-6");
    expect(container.textContent).toContain(
      "已启用的 API 提供商都未列出 gpt-6",
    );
    await typeRedirect("第 1 条规则的目标模型", "gpt-5");
    expect(container.textContent).not.toContain("已启用的 API 提供商都未列出");
    expect(dirty).toHaveBeenLastCalledWith(true);

    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      model_redirects: [{ from: "gpt-4o", to: "gpt-5", enabled: true }],
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("persists the built-in rule only after it is configured and saved", async () => {
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={dirty}
        />,
      ),
    );
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    const toggle = () =>
      container.querySelector<HTMLButtonElement>(
        '[aria-label="启用 codex-auto-review 的重定向"]',
      )!;
    expect(toggle().getAttribute("aria-checked")).toBe("false");
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await act(async () => toggle().click());
    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: true },
      ],
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
    await act(async () => toggle().click());
    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: false },
      ],
    });
    expect(redirectField("Codex 自动审查的目标模型").value).toBe("gpt-5");
  });

  it("blocks saving until every redirect is valid", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      model_redirects: [{ from: "gpt-4o", to: "gpt-5", enabled: true }],
    });
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={dirty}
        />,
      ),
    );
    expect(dirty).toHaveBeenLastCalledWith(false);
    await act(async () => button("添加重定向").click());
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await selectTab("恢复与重试");
    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(
      container.querySelector('[role="tab"][aria-selected="true"]')
        ?.textContent,
    ).toBe("模型重定向");
    expect(
      [...container.querySelectorAll('[role="alert"]')].map(
        (element) => element.textContent,
      ),
    ).toEqual(["请先修正模型重定向中标出的问题，再保存。", "请填写请求模型。"]);

    await typeRedirect("第 2 条规则的请求模型", "claude-3-opus");
    await typeRedirect("第 2 条规则的目标模型", "gpt-4o");
    expect(
      [...container.querySelectorAll('[role="alert"]')].map(
        (element) => element.textContent,
      ),
    ).toEqual([
      "请先修正模型重定向中标出的问题，再保存。",
      "目标模型是另一条规则的请求模型；重定向只生效一次，不能串联。",
    ]);
    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();

    await typeRedirect("第 2 条规则的目标模型", "claude-sonnet-4-5");
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await act(async () => button("保存默认策略").click());
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      model_redirects: [
        { from: "gpt-4o", to: "gpt-5", enabled: true },
        { from: "claude-3-opus", to: "claude-sonnet-4-5", enabled: true },
      ],
    });
  });

  it("does not mark an unchanged group reset as dirty", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      default_failure_policy: defaultFailurePolicy(),
    });
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    await selectTab("恢复与重试");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重置此组设置")!
        .click(),
    );
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(
      [...container.querySelectorAll("button")].find(
        (button) => button.textContent === "保存默认策略",
      )!.disabled,
    ).toBe(true);
  });

  it("edits every error through one retry switch and preserves untouched legacy rules", async () => {
    const policy = {
      ...settings().default_failure_policy,
      http_status: {
        "401": "failover",
        "418": "retry",
        "429": "retry_and_failover",
        "500": "stop",
      } as FailurePolicy["http_status"],
    };
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      default_failure_policy: policy,
    });
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    await selectTab("错误规则");
    const retrySwitch = (error: string) =>
      [
        ...container.querySelectorAll<HTMLButtonElement>('[role="switch"]'),
      ].find((button) => button.getAttribute("aria-label")?.startsWith(error))!;
    expect(container.querySelectorAll('[role="combobox"]')).toHaveLength(0);
    for (const code of ["401", "418", "429"]) {
      expect(retrySwitch(`HTTP ${code}`).getAttribute("aria-checked")).toBe(
        "true",
      );
    }
    expect(retrySwitch("HTTP 500").getAttribute("aria-checked")).toBe("false");
    expect(dirty).toHaveBeenLastCalledWith(false);
    await act(async () => retrySwitch("HTTP 401").click());
    await act(async () => retrySwitch("HTTP 500").click());
    await act(async () => retrySwitch("无法连接").click());
    await selectTab("恢复与重试");
    await selectTab("错误规则");
    expect(retrySwitch("HTTP 401").getAttribute("aria-checked")).toBe("false");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    expect(bridge.updateRoutingSettings).toHaveBeenCalledWith({
      default_failure_policy: {
        ...policy,
        network_error: "stop",
        http_status: {
          ...policy.http_status,
          "401": "stop",
          "500": "retry_and_failover",
        },
      },
    });
  });

  it("validates custom statuses and resets only error rules", async () => {
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={() => {}} />,
      ),
    );
    await selectTab("错误规则");
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="添加错误状态码（400–599）"]',
    )!;
    const add = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "添加规则",
    )!;
    const setStatus = async (status: string) =>
      act(async () => {
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(input, status);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
    for (const status of ["", "200", "600", "abc", "429"]) {
      await setStatus(status);
      expect(add.disabled).toBe(true);
    }
    await setStatus("418");
    await act(async () =>
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );
    expect(input.value).toBe("");
    expect(
      container
        .querySelector('[role="switch"][aria-label="HTTP 418 · 允许重试"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="移除 HTTP 401 规则"]')!
        .click(),
    );
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    const saved =
      bridge.updateRoutingSettings.mock.calls[0][0].default_failure_policy;
    expect(saved.http_status["418"]).toBe("retry_and_failover");
    expect(saved.http_status).not.toHaveProperty("401");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重置此组设置")!
        .click(),
    );
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "保存默认策略")!
        .click(),
    );
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      default_failure_policy: settings().default_failure_policy,
    });
  });

  it("keeps route exceptions optional and restores global order and counts", async () => {
    function Editor() {
      const [override, setOverride] = useState<FailurePolicy>();
      const [custom, setCustom] = useState<FailoverPolicy | undefined>({
        enabled: true,
        strategy: "failover_first",
        max_attempts: 12,
      });
      return (
        <FailoverEditor
          title={i18n.t("routes.failureTitle", { name: "Code alias" })}
          scopeHint={i18n.t("routes.failureScope", {
            name: "Code alias",
            protocol: "OpenAI Responses",
          })}
          overrideLabel={i18n.t("routes.failureOverride", {
            name: "Code alias",
          })}
          overrideHint={i18n.t("routes.failureOverrideHint", {
            name: "Code alias",
          })}
          value={
            custom ?? {
              enabled: true,
              strategy: "retry_first",
              max_attempts: 6,
            }
          }
          onChange={setCustom}
          override={override}
          onOverrideChange={setOverride}
          inheritedFailurePolicy={settings().default_failure_policy}
          onResetOrder={custom ? () => setCustom(undefined) : undefined}
          targets={[{ name: "A" }, { name: "B" }]}
        />
      );
    }
    await act(async () => root.render(<Editor />));
    expect(container.textContent).toContain("A：允许的错误最多重试 3 次");
    expect(container.textContent).toContain("最多尝试 12 次");
    const toggle = [
      ...container.querySelectorAll<HTMLButtonElement>('button[role="switch"]'),
    ].find((button) =>
      button
        .closest("label")
        ?.textContent?.includes("为「Code alias」单独设置失败处理"),
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await act(async () => toggle.click());
    expect(container.textContent).toContain("遇到这些错误时");
    expect(container.textContent).toContain(
      "统一用于「Code alias」的全部首选和备用目标",
    );
    await act(async () => toggle.click());
    expect(container.textContent).not.toContain("遇到这些错误时");
    const reset = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "恢复全局顺序和次数",
    )!;
    await act(async () => reset.click());
    expect(container.textContent).toContain("最多尝试 6 次");
    expect(container.textContent).toContain(
      "顺序和总次数跟随「路由 → 默认策略」",
    );
  });

  it("renders readable attempt details", async () => {
    await act(async () =>
      root.render(
        <RecoveryDetails
          value={{
            upstream_model: "actual-model",
            action: "failover",
            reason: "http_429",
            delay_ms: 500,
            stop_reason: "attempt_limit",
          }}
        />,
      ),
    );
    expect(container.textContent).toContain("切换备用目标");
    expect(container.textContent).toContain("HTTP 429");
    expect(container.textContent).toContain("等待 500 毫秒");
    expect(container.textContent).toContain("已达到总尝试上限");
  });
  it("shows the actual failure, retry and backup chain", async () => {
    const attempts = [
      {
        id: "request_a",
        service_id: "service_a",
        attempt_index: 1,
        status: "failed",
        recovery: { action: "retry", delay_ms: 0 },
      },
      {
        id: "request_a_retry",
        service_id: "service_a",
        attempt_index: 2,
        status: "failed",
        recovery: { action: "failover", delay_ms: 0 },
      },
      {
        id: "request_b",
        service_id: "service_b",
        attempt_index: 3,
        status: "succeeded",
      },
    ] as RequestRecord[];
    await act(async () =>
      root.render(
        <RecoveryChain
          records={attempts}
          serviceNames={{ service_a: "A", service_b: "B" }}
        />,
      ),
    );
    expect(container.textContent).toMatch(/A.*重试 A.*切换到 B/);
    expect(
      container.querySelectorAll('svg[data-animated-icon="arrow-right"]'),
    ).toHaveLength(2);
    expect(container.querySelectorAll("li")).toHaveLength(3);
  });
});
