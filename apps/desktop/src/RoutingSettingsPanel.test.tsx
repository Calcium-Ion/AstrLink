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

  it("loads and saves a single policy for all services", async () => {
    const dirty = vi.fn();
    await act(async () => {
      root.render(<RoutingSettingsPanel ready onDirtyChange={dirty} />);
    });
    expect(container.textContent).toContain("在这里配置一次");
    const input = container.querySelector<HTMLInputElement>(
      'input[type="number"]',
    )!;
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

  it("retries loading and preserves an unsaved draft across reconnection", async () => {
    bridge.getRoutingSettings.mockRejectedValueOnce(Error("Core unavailable"));
    const onDirtyChange = vi.fn();
    const render = async (ready: boolean) =>
      act(async () =>
        root.render(
          <RoutingSettingsPanel ready={ready} onDirtyChange={onDirtyChange} />,
        ),
      );
    await render(true);
    expect(container.textContent).toContain("Core unavailable");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重试")!
        .click(),
    );
    const input = container.querySelector<HTMLInputElement>(
      'input[type="number"]',
    )!;
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
      root.render(<RoutingSettingsPanel ready onDirtyChange={() => {}} />),
    );
    expect(
      [...container.querySelectorAll("h2")].map(
        (element) => element.textContent,
      ),
    ).toEqual(["默认失败处理", "默认恢复顺序与总次数", "供应商切换"]);
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="失败后允许自动换供应商"]',
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
      root.render(<RoutingSettingsPanel ready onDirtyChange={vi.fn()} />);
    });
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
      root.render(<RoutingSettingsPanel ready onDirtyChange={vi.fn()} />);
    });
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
      root.render(<RoutingSettingsPanel ready onDirtyChange={vi.fn()} />);
    });
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="允许修复函数输出密文"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(
      container
        .querySelector('[role="switch"][aria-label="Codex / OpenAI 推理修复重试"]')
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
    expect(container.textContent).not.toContain("openai_function_output_repair");
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
    expect(container.querySelectorAll('svg[data-animated-icon="arrow-right"]')).toHaveLength(2);
    expect(container.querySelectorAll("li")).toHaveLength(3);
  });
});
