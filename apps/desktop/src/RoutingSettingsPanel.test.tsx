// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  builtinToolAction: vi.fn().mockResolvedValue({ configured: false }),
  getClientIdentities: vi.fn(),
  getPreferences: vi.fn(),
  getRoutingSettings: vi.fn(),
  getClassifierSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));
const motionPreference = vi.hoisted(() => ({ reduced: false }));
vi.mock("motion/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("motion/react")>()),
  useReducedMotion: () => motionPreference.reduced,
}));
import {
  defaultFailurePolicy,
  identityLearningKeys,
  identitySettingKeys,
  subscriptionProtectionKeys,
  type FailurePolicy,
} from "./failure-policy-model";
import {
  RoutingSettingsPanel,
  routingAutosaveDelay,
} from "./RoutingSettingsPanel";
import { RecoveryChain, RecoveryDetails } from "./components/RecoveryDetails";
import type { RequestRecord } from "./request-record-model";
import { applyLocale } from "./i18n";
import { settleClosedLayer } from "./lib/test-layer-settle";

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
    vi.useFakeTimers();
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getRoutingSettings.mockReset().mockResolvedValue(settings());
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue({ dimensions: [] });
    bridge.getClientIdentities
      .mockReset()
      .mockRejectedValue(new Error("unavailable"));
    bridge.getPreferences
      .mockReset()
      .mockRejectedValue(new Error("desktop only"));
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
    vi.useRealTimers();
  });

  async function flushAutosave() {
    await act(async () => vi.advanceTimersByTimeAsync(routingAutosaveDelay));
  }

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
        ...container.querySelectorAll<HTMLButtonElement>(
          '[data-testid="upstream-identity-settings"] [role="switch"]',
        ),
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
      bridge.updateRoutingSettings.mockRejectedValueOnce(new Error("保存失败"));
      await flushAutosave();
      expect(container.textContent).toContain("保存失败");
      expect(dirty).toHaveBeenLastCalledWith(true);
      await act(async () => button("重试").click());
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        [`${provider.toLowerCase()}_identity_enforcement`]: false,
      });
      expect(dirty).toHaveBeenLastCalledWith(false);
    },
  );

  it.each([
    ["official_client_passthrough", "官方客户端请求原样转发"],
    ["subscription_risk_protection", "识别上游封控并暂停调度"],
    ["codex_request_normalization", "修正 Codex 订阅请求"],
    ["claude_request_normalization", "修正 Claude 订阅请求结构"],
    ["subscription_session_isolation", "按账号隔离会话标识"],
  ])(
    "defaults %s on under Forwarding identity and saves it independently",
    async (key, label) => {
      const dirty = vi.fn();
      await act(async () =>
        root.render(
          <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
        ),
      );
      await selectTab("转发身份");
      const toggles = [
        ...container.querySelectorAll<HTMLButtonElement>(
          '[data-testid="subscription-protection-settings"] [role="switch"]',
        ),
      ];
      expect(toggles).toHaveLength(subscriptionProtectionKeys.length);
      for (const toggle of toggles)
        expect(toggle.getAttribute("aria-checked")).toBe("true");
      const toggle = container.querySelector<HTMLButtonElement>(
        `[aria-label="${label}"]`,
      )!;
      await act(async () => toggle.click());
      expect(toggle.getAttribute("aria-checked")).toBe("false");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        [key]: false,
      });
      expect(dirty).toHaveBeenLastCalledWith(false);
    },
  );

  it.each([
    ["codex_identity_auto_learn", "从 Codex CLI 请求学习身份"],
    ["claude_identity_auto_learn", "从 Claude Code 请求学习身份"],
    ["grok_identity_auto_learn", "从 Grok CLI 请求学习身份"],
  ])("defaults %s on and saves it independently", async (key, label) => {
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    await selectTab("转发身份");
    const toggles = [
      ...container.querySelectorAll<HTMLButtonElement>(
        '[data-testid="identity-learning-settings"] [role="switch"]',
      ),
    ];
    expect(toggles).toHaveLength(identityLearningKeys.length);
    for (const toggle of toggles)
      expect(toggle.getAttribute("aria-checked")).toBe("true");
    const toggle = container.querySelector<HTMLButtonElement>(
      `[aria-label="${label}"]`,
    )!;
    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      [key]: false,
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("shows the learned or built-in version under each learning switch", async () => {
    bridge.getClientIdentities.mockResolvedValue({
      codex: { learned_version: "0.160.0", builtin_version: "0.155.1" },
      claude: { builtin_version: "2.1.258" },
      grok: { builtin_version: "1.0.45", learned_version: "1.0.50" },
    });
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      ),
    );
    await selectTab("转发身份");
    const status = (label: string) => {
      const toggle = container.querySelector(`[aria-label="${label}"]`)!;
      const [, id] = toggle.getAttribute("aria-describedby")!.split(" ");
      return document.getElementById(id)?.textContent;
    };
    expect(status("从 Grok CLI 请求学习身份")).toBe("已学到版本 1.0.50");
    expect(status("从 Codex CLI 请求学习身份")).toBe("已学到版本 0.160.0");
    expect(status("从 Claude Code 请求学习身份")).toBe(
      "尚未学到，内置版本 2.1.258",
    );
  });

  it.each([
    ["codex_identity_version", "Codex 最低版本", "0.160.0", "0.143.9"],
    ["claude_identity_version", "Claude Code 最低版本", "2.1.300", "2.1"],
    ["grok_identity_version", "Grok 最低版本", "1.0.50", "1.0"],
  ])(
    "commits the %s override on blur or Enter and clears it with an empty value",
    async (key, label, version, invalid) => {
      bridge.updateRoutingSettings.mockImplementation(async (patch) => {
        const saved: Record<string, unknown> = { ...settings(), ...patch };
        // The core omits a cleared override.
        if (saved[key] === "") delete saved[key];
        return saved;
      });
      const dirty = vi.fn();
      await act(async () =>
        root.render(
          <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
        ),
      );
      await selectTab("转发身份");
      const input = [...container.querySelectorAll("label")]
        .find(
          (element) =>
            element.querySelector(":scope > span")?.textContent === label,
        )!
        .querySelector<HTMLInputElement>("input")!;
      expect(input.value).toBe("");
      expect(input.placeholder).toBe("留空则使用已学习或内置版本");
      const type = async (value: string) =>
        act(async () => {
          Object.getOwnPropertyDescriptor(
            HTMLInputElement.prototype,
            "value",
          )!.set!.call(input, value);
          input.dispatchEvent(new Event("input", { bubbles: true }));
        });
      const blur = async () =>
        act(async () =>
          input.dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
        );

      // A partly typed version is neither autosaved nor committed.
      await type(invalid);
      await flushAutosave();
      expect(dirty).not.toHaveBeenCalledWith(true);
      await blur();
      expect(input.getAttribute("aria-invalid")).toBe("true");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();

      await type(` ${version} `);
      expect(input.hasAttribute("aria-invalid")).toBe(false);
      await act(async () =>
        input.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
        ),
      );
      expect(input.value).toBe(version);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        [key]: version,
      });
      expect(dirty).toHaveBeenLastCalledWith(false);

      await type("");
      await blur();
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        [key]: "",
      });
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(2);
      expect(input.value).toBe("");
      expect(dirty).toHaveBeenLastCalledWith(false);
    },
  );

  it("loads saved identity opt-outs and keeps them disabled while offline", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      ...Object.fromEntries(
        [
          ...identitySettingKeys,
          ...subscriptionProtectionKeys,
          ...identityLearningKeys,
        ].map((key) => [key, false]),
      ),
    });
    const dirty = vi.fn();
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
      ),
    );
    await selectTab("转发身份");
    const toggles = container.querySelectorAll('[role="switch"]');
    expect(toggles).toHaveLength(
      identitySettingKeys.length +
        subscriptionProtectionKeys.length +
        identityLearningKeys.length,
    );
    for (const toggle of toggles)
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
    expect(button("保存默认策略")).toBeUndefined();
  });

  describe("thinking and prompts", () => {
    let saved: Record<string, unknown>;
    beforeEach(() => {
      motionPreference.reduced = false;
      saved = settings();
      bridge.updateRoutingSettings.mockImplementation(
        async (patch) => (saved = { ...saved, ...patch }),
      );
    });

    async function openTab(dirty = vi.fn()) {
      await act(async () =>
        root.render(
          <RoutingSettingsPanel services={[]} ready onDirtyChange={dirty} />,
        ),
      );
      await selectTab("请求改写");
      return dirty;
    }

    async function openWith(values: Record<string, unknown>) {
      saved = { ...settings(), ...values };
      bridge.getRoutingSettings.mockResolvedValue(saved);
      return openTab();
    }

    function promptField(label: string) {
      return [
        ...container.querySelectorAll<HTMLTextAreaElement>("textarea"),
      ].find(
        (input) =>
          document.getElementById(input.getAttribute("aria-labelledby")!)
            ?.textContent === label,
      )!;
    }

    async function typePrompt(input: HTMLTextAreaElement, value: string) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(
          HTMLTextAreaElement.prototype,
          "value",
        )!.set!.call(input, value);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }

    async function blur(input: HTMLTextAreaElement) {
      await act(async () =>
        input.dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
      );
    }

    function thinking() {
      return container.querySelector<HTMLElement>(
        '[data-testid="thinking-settings"]',
      )!;
    }

    /** The labels of the option rows the thinking panel shows. */
    function thinkingRows() {
      return [
        ...thinking().querySelectorAll<HTMLElement>('[role="radiogroup"]'),
      ].map(
        (group) =>
          document.getElementById(group.getAttribute("aria-labelledby")!)
            ?.textContent,
      );
    }

    function thinkingOption(label: string) {
      return [
        ...thinking().querySelectorAll<HTMLButtonElement>('[role="radio"]'),
      ].find((item) => item.getAttribute("aria-label") === label)!;
    }

    function checkedOptions() {
      return [
        ...thinking().querySelectorAll('[role="radio"][data-state="checked"]'),
      ].map((option) => option.getAttribute("aria-label"));
    }

    const rangeEnds = { from: "最少想多少", to: "最多想多少" } as const;

    function rangeSelect(end: keyof typeof rangeEnds) {
      return thinking().querySelector<HTMLButtonElement>(
        `button[role="combobox"][aria-label="${rangeEnds[end]}"]`,
      );
    }

    /** Picks a level for one end of the custom range and waits for it to save. */
    async function chooseEnd(end: keyof typeof rangeEnds, option: string) {
      await act(async () => {
        rangeSelect(end)!.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
        await Promise.resolve();
      });
      const item = [
        ...document.querySelectorAll<HTMLElement>('[role="option"]'),
      ].find((candidate) => candidate.textContent?.trim() === option)!;
      await act(async () => {
        item.click();
        await Promise.resolve();
      });
      await flushAutosave();
      // Radix returns focus on a timer, so the select settles on real time.
      vi.useRealTimers();
      await settleClosedLayer();
      vi.useFakeTimers();
    }

    const help = {
      thinking: "思考强度说明",
      prompt: "全局提示词说明",
    } as const;

    function helpButton(which: keyof typeof help) {
      return container.querySelector<HTMLButtonElement>(
        `button[aria-label="${help[which]}"]`,
      )!;
    }

    function popover() {
      return document.querySelector<HTMLElement>(
        '[data-slot="popover-content"]',
      );
    }

    async function openHelp(which: keyof typeof help) {
      await act(async () => helpButton(which).click());
      expect(popover()).not.toBeNull();
      return popover()!;
    }

    async function closeHelp(which: keyof typeof help) {
      // Radix returns focus on a timer, so the layer settles on real time.
      vi.useRealTimers();
      await act(async () =>
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        ),
      );
      await settleClosedLayer();
      vi.useFakeTimers();
      expect(popover()).toBeNull();
      expect(document.activeElement).toBe(helpButton(which));
    }

    function demoText() {
      return popover()!.querySelector("p")?.textContent;
    }

    function demoSwitch(label: string) {
      return [
        ...popover()!.querySelectorAll<HTMLButtonElement>(
          '[data-slot="segmented-control-item"]',
        ),
      ].find((item) => item.textContent === label);
    }

    async function showExample(label: string) {
      await act(async () => demoSwitch(label)!.click());
    }

    function visual() {
      return popover()!.querySelector<HTMLElement>("[data-visual]")!;
    }

    /** Where each request card of the thermometer sits; * marks a stopped card. */
    function thermometer() {
      const scale = visual();
      expect(scale.dataset.visual).toBe("thermometer");
      return {
        range: scale.dataset.range,
        cards: [...scale.querySelectorAll<HTMLElement>("[data-request]")].map(
          (card) =>
            `${card.textContent}: ${card.dataset.level}${card.dataset.capped ? "*" : ""}`,
        ),
        bump: scale.querySelector<HTMLElement>("[data-bump]") !== null,
      };
    }

    /**
     * The request cards passing through AstrLink, as the model side shows
     * them once they arrive; * marks a card whose level changed.
     */
    function flow() {
      const scene = visual();
      expect(scene.dataset.visual).toBe("flow");
      const gateway = scene.querySelector<HTMLElement>("[data-gateway]")!;
      expect(gateway.textContent).toBe("AstrLink");
      expect(gateway.querySelector("img")?.getAttribute("alt")).toBe("");
      return {
        mode: scene.dataset.flow,
        arrived: scene.dataset.arrived,
        cards: [...scene.querySelectorAll<HTMLElement>("[data-card]")].map(
          (card) => `${card.textContent}${card.dataset.changed ? "*" : ""}`,
        ),
      };
    }

    function promptBlocks() {
      return [
        ...popover()!.querySelectorAll<HTMLElement>(
          '[data-prompt-card="main"] [data-prompt-block]',
        ),
      ].map((block) => block.dataset.promptBlock);
    }

    it("scrolls inside the tab panel and shows the second row the mode uses", async () => {
      const dirty = await openTab();
      const panel = container.querySelector(
        '[role="tabpanel"][data-state="active"]',
      )!;
      expect(panel.hasAttribute("data-tab-scroller")).toBe(true);
      expect(panel.className).toContain("overflow-y-auto");
      expect(
        panel.querySelector('[data-testid="global-prompt-settings"]'),
      ).not.toBeNull();
      // Following the client needs only the mode.
      expect(thinkingRows()).toEqual(["方式"]);
      expect(checkedOptions()).toEqual(["跟随客户端"]);

      await act(async () => thinkingOption("固定").click());
      expect(thinkingRows()).toEqual(["方式", "档位"]);
      expect(checkedOptions()).toEqual(["固定", "中"]);
      expect(dirty).toHaveBeenLastCalledWith(true);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
        reasoning_mode: "fixed",
      });
      expect(dirty).toHaveBeenLastCalledWith(false);

      await act(async () => thinkingOption("关").click());
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        reasoning_level: "off",
      });
      // Pressing the selected level again keeps it rather than clearing it.
      await act(async () => thinkingOption("关").click());
      expect(checkedOptions()).toEqual(["固定", "关"]);

      await act(async () => thinkingOption("自适应").click());
      expect(thinkingRows()).toEqual(["方式", "范围"]);
      expect(checkedOptions()).toEqual(["自适应", "日常"]);
      expect(rangeSelect("from")).toBeNull();
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        reasoning_mode: "adaptive",
      });

      await act(async () => thinkingOption("研究").click());
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        adaptive_preset: "research",
      });
      expect(rangeSelect("from")).toBeNull();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(4);

      // Following the client again keeps every choice for later.
      await act(async () => thinkingOption("跟随客户端").click());
      expect(thinkingRows()).toEqual(["方式"]);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        reasoning_mode: "passthrough",
      });
      expect(saved).toMatchObject({
        reasoning_mode: "passthrough",
        reasoning_level: "off",
        adaptive_preset: "research",
      });
      await act(async () => thinkingOption("固定").click());
      expect(checkedOptions()).toEqual(["固定", "关"]);
      await act(async () => thinkingOption("自适应").click());
      expect(checkedOptions()).toEqual(["自适应", "研究"]);
    });

    it("keeps the custom range in order when one end passes the other", async () => {
      await openWith({ reasoning_mode: "adaptive" });
      await act(async () => thinkingOption("自定义").click());
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        adaptive_preset: "custom",
      });
      // The two ends sit on the range row and read as a sentence.
      const from = rangeSelect("from")!;
      const to = rangeSelect("to")!;
      expect(from.textContent).toBe("从低");
      expect(to.textContent).toBe("到最高");
      expect(from.closest('[role="radiogroup"]')).toBeNull();
      expect(
        thinkingOption("自定义").closest('[role="radiogroup"]')?.parentElement,
      ).toBe(from.parentElement?.parentElement);

      await chooseEnd("to", "中");
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        adaptive_max: "medium",
      });
      // A lower end above the upper end carries the upper end along.
      await chooseEnd("from", "高");
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        adaptive_min: "high",
        adaptive_max: "high",
      });
      expect(rangeSelect("to")!.textContent).toBe("到高");
      // An upper end below the lower end carries the lower end along.
      await chooseEnd("to", "关");
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        adaptive_min: "off",
        adaptive_max: "off",
      });
      await chooseEnd("to", "最高");
      expect(saved).toMatchObject({
        adaptive_preset: "custom",
        adaptive_min: "off",
        adaptive_max: "max",
      });
      expect(
        thinkingOption("自定义")
          .closest("label")
          ?.querySelector("[data-strength]")
          ?.getAttribute("data-strength"),
      ).toBe("1-4");
      // The range survives a switch to fixed and back.
      await act(async () => thinkingOption("固定").click());
      expect(rangeSelect("from")).toBeNull();
      await act(async () => thinkingOption("自适应").click());
      expect(checkedOptions()).toEqual(["自适应", "自定义"]);
      expect(rangeSelect("from")!.textContent).toBe("从关");
      expect(rangeSelect("to")!.textContent).toBe("到最高");
    });

    it("shows only controls in both panels and keeps explanations behind one help button", async () => {
      await openWith({ reasoning_mode: "adaptive", adaptive_preset: "custom" });
      const header = thinking().querySelector('[data-slot="panel-header"]')!;
      expect(header.querySelector("h2")?.textContent).toBe("思考强度");
      expect(header.querySelector("h2")?.nextElementSibling).toBe(
        helpButton("thinking"),
      );
      expect(header.querySelector("p")?.textContent).toBe(
        "设置模型回答前想多久。",
      );
      expect(
        thinking().querySelectorAll('button[aria-label$="说明"]'),
      ).toHaveLength(1);
      expect(thinking().querySelector('[aria-live="polite"]')).toBeNull();
      expect(thinking().querySelectorAll("p")).toHaveLength(1);
      expect(thinking().textContent).not.toContain("每个请求");
      await act(async () => thinkingOption("固定").click());
      expect(
        thinking().querySelectorAll('button[aria-label$="说明"]'),
      ).toHaveLength(1);

      const prompt = container.querySelector<HTMLElement>(
        '[data-testid="global-prompt-settings"]',
      )!;
      const promptHeader = prompt.querySelector('[data-slot="panel-header"]')!;
      expect(promptHeader.querySelector("h2")?.nextElementSibling).toBe(
        helpButton("prompt"),
      );
      expect(promptHeader.querySelector('[role="switch"]')).not.toBeNull();
      expect(prompt.querySelector("p")).toBeNull();
      expect(prompt.querySelector("ol")).toBeNull();
      expect(prompt.querySelectorAll("textarea")).toHaveLength(2);
      expect(popover()).toBeNull();
    });

    it("explains each mode with its own picture, and the switchers change only the example", async () => {
      await openWith({ reasoning_mode: "fixed", reasoning_level: "high" });
      await openHelp("thinking");
      // It opens on the panel's mode: the client's cards pass through
      // AstrLink one after another and come out at the fixed level, with
      // the changed ones highlighted.
      expect(demoSwitch("固定")?.getAttribute("data-state")).toBe("on");
      expect(demoSwitch("日常")).toBeUndefined();
      expect(visual().getAttribute("aria-hidden")).toBe("true");
      expect(flow()).toEqual({
        mode: "fixed",
        arrived: "0",
        cards: ["低", "高", "中"],
      });
      expect(demoText()).toBe(
        "每个请求都按「高」思考。客户端自己选的档位被替换。",
      );
      await act(async () => vi.advanceTimersByTime(1200));
      expect(flow().cards).toEqual(["高*", "高", "中"]);
      await act(async () => vi.advanceTimersByTime(1400));
      expect(flow()).toEqual({
        mode: "fixed",
        arrived: "3",
        cards: ["高*", "高", "高*"],
      });
      // It holds, then starts over.
      await act(async () => vi.advanceTimersByTime(1600));
      expect(flow().cards).toEqual(["低", "高", "中"]);

      // Adaptive: a thermometer whose brackets mark the range, opening on
      // the panel's preset.
      await showExample("自适应");
      expect(demoSwitch("日常")?.getAttribute("data-state")).toBe("on");
      expect(thermometer()).toEqual({
        range: "low..max",
        cards: [
          "问个小问题: low",
          "调试一个 bug: medium",
          "改了一下午代码: max",
        ],
        bump: false,
      });
      expect(demoText()).toBe(
        "简单的问题想得少，复杂的问题想得多。从「低」起步，对话变长、用的工具多或问题很长时逐级提高，最多到「最高」。适合平时写代码和聊天。",
      );
      await showExample("研究");
      expect(thermometer()).toEqual({
        range: "medium..max",
        cards: [
          "问个小问题: medium",
          "调试一个 bug: high",
          "改了一下午代码: max*",
        ],
        bump: true,
      });
      expect(demoText()).toBe(
        "每个问题至少按「中」思考。对话变长、用的工具多或问题很长时逐级提高，直到这个模型能想的最多。适合难题和长时间钻研，回答更慢，花费更多。",
      );
      // Custom shows the panel's own range.
      await showExample("自定义");
      expect(thermometer().range).toBe("low..max");
      expect(demoText()).toBe(
        "你来定最少想多少、最多想多少。简单的问题用下限，越复杂越靠近上限。两头选一样时，每个请求都按那一档。",
      );

      // Passthrough: the same picture, and the cards come out with their
      // levels as they were.
      await showExample("跟随客户端");
      expect(demoSwitch("日常")).toBeUndefined();
      expect(flow()).toEqual({
        mode: "passthrough",
        arrived: "0",
        cards: ["低", "高", "中"],
      });
      expect(demoText()).toBe(
        "客户端怎么设，就怎么发。想换档位，去客户端里改。",
      );
      await act(async () => vi.advanceTimersByTime(2600));
      expect(flow()).toEqual({
        mode: "passthrough",
        arrived: "3",
        cards: ["低", "高", "中"],
      });

      expect(checkedOptions()).toEqual(["固定", "高"]);
      await closeHelp("thinking");
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    });

    it.each([
      [
        { reasoning_mode: "fixed", reasoning_level: "off" },
        "flow fixed",
        "模型直接回答。关不掉的新模型想得最少。",
      ],
      [
        { reasoning_mode: "passthrough", reasoning_level: "max" },
        "flow passthrough",
        "客户端怎么设，就怎么发。想换档位，去客户端里改。",
      ],
      [
        {
          reasoning_mode: "adaptive",
          adaptive_preset: "custom",
          adaptive_min: "high",
          adaptive_max: "max",
        },
        "high..max",
        "你来定最少想多少、最多想多少。简单的问题用下限，越复杂越靠近上限。两头选一样时，每个请求都按那一档。",
      ],
    ])(
      "opens the explanation on the panel's settings %o",
      async (values, shown, text) => {
        await openWith(values);
        await openHelp("thinking");
        expect(
          visual().dataset.visual === "thermometer"
            ? visual().dataset.range
            : `flow ${visual().dataset.flow}`,
        ).toBe(shown);
        expect(demoText()).toBe(text);
        await closeHelp("thinking");
      },
    );

    it("shows no thinking on the model side when fixed is off", async () => {
      motionPreference.reduced = true;
      await openWith({ reasoning_mode: "fixed", reasoning_level: "off" });
      await openHelp("thinking");
      expect(flow()).toEqual({
        mode: "fixed",
        arrived: "3",
        cards: ["不思考*", "不思考*", "不思考*"],
      });
      await closeHelp("thinking");
    });

    it("stops a card at the upper end of a narrow range", async () => {
      await openWith({
        reasoning_mode: "adaptive",
        adaptive_preset: "custom",
        adaptive_min: "medium",
        adaptive_max: "medium",
      });
      await openHelp("thinking");
      expect(thermometer()).toEqual({
        range: "medium..medium",
        cards: [
          "问个小问题: medium",
          "调试一个 bug: medium*",
          "改了一下午代码: medium*",
        ],
        bump: true,
      });
      await closeHelp("thinking");
    });

    it("keeps the explanations free of jargon and each preset to three sentences", async () => {
      await openWith({ reasoning_mode: "adaptive" });
      const banned = /预算|力度|token|effort|budget|缓存|haiku/i;
      await openHelp("thinking");
      for (const mode of ["跟随客户端", "固定", "自适应"]) {
        await showExample(mode);
        const presets = mode === "自适应" ? ["日常", "研究", "自定义"] : [""];
        for (const preset of presets) {
          if (preset) await showExample(preset);
          expect(popover()!.textContent).not.toMatch(banned);
          const sentences = demoText()!.split("。").filter(Boolean);
          expect(sentences.length).toBeLessThanOrEqual(3);
        }
      }
      await closeHelp("thinking");
      await openHelp("prompt");
      expect(popover()!.textContent).not.toMatch(banned);
      await closeHelp("prompt");
    });

    it("assembles the system prompt in the help popover", async () => {
      await openWith({
        system_prompt_enabled: true,
        system_prompt_prefix: "所有回答都用简体中文。\n第二行",
        system_prompt_suffix: "",
      });
      const content = await openHelp("prompt");
      const demo = content.querySelector<HTMLElement>("[data-prompt-demo]")!;
      expect(
        demo
          .querySelector('[aria-hidden="true"]')
          ?.contains(content.querySelector('[data-prompt-card="main"]')),
      ).toBe(true);
      expect(demo.dataset.promptStep).toBe("0");
      expect(promptBlocks()).toEqual(["identity", "client"]);
      expect(
        content.querySelector('[data-prompt-card="main"] h4')?.textContent,
      ).toBe("发给模型的系统提示词");
      const identity = content.querySelector('[data-prompt-block="identity"]')!;
      expect(identity.textContent).toBe("客户端的身份声明");
      expect(identity.querySelector("svg")).not.toBeNull();
      const smallTasks = content.querySelector(
        '[data-prompt-card="small-tasks"]',
      )!;
      expect(smallTasks.querySelector("h4")?.textContent).toBe(
        "起标题这类小任务",
      );
      expect(smallTasks.textContent).toContain("给这段对话起个标题");
      expect(smallTasks.textContent).toContain("保持原样");
      expect(demoText()).toBe("已开启。每个请求多出这两段文字的输入用量。");
      await act(async () => vi.advanceTimersByTime(900));
      expect(demo.dataset.promptStep).toBe("1");
      expect(promptBlocks()).toEqual(["identity", "prefix", "client"]);
      // The saved text's first line, or the placeholder when empty.
      expect(
        content.querySelector('[data-prompt-block="prefix"]')?.textContent,
      ).toBe("你的开头所有回答都用简体中文。");
      await act(async () => vi.advanceTimersByTime(900));
      expect(demo.dataset.promptStep).toBe("2");
      expect(promptBlocks()).toEqual([
        "identity",
        "prefix",
        "client",
        "suffix",
      ]);
      expect(
        content.querySelector('[data-prompt-block="suffix"]')?.textContent,
      ).toBe("你的末尾例如：改代码之前，先说明打算怎么改。");
      // It holds the result, then starts over.
      await act(async () => vi.advanceTimersByTime(2200));
      expect(demo.dataset.promptStep).toBe("0");
      await act(async () => vi.advanceTimersByTime(900));
      expect(demo.dataset.promptStep).toBe("1");
      await closeHelp("prompt");
    });

    it("says nothing is added yet while the global prompt is off", async () => {
      await openTab();
      const content = await openHelp("prompt");
      expect(demoText()).toBe(
        "未开启。打开右上角的开关后，这两段文字会加进每个请求。",
      );
      await act(async () => vi.advanceTimersByTime(5000));
      expect(
        content.querySelector<HTMLElement>("[data-prompt-demo]")?.dataset
          .promptStep,
      ).toBe("0");
      expect(promptBlocks()).toEqual(["identity", "client"]);
      await closeHelp("prompt");
    });

    it("shows every explanation at its final state when motion is reduced", async () => {
      motionPreference.reduced = true;
      await openWith({
        reasoning_mode: "adaptive",
        adaptive_preset: "research",
        reasoning_level: "low",
        system_prompt_enabled: true,
        system_prompt_prefix: "先给结论。",
        system_prompt_suffix: "最后列出风险。",
      });
      await openHelp("thinking");
      expect(thermometer().cards).toEqual([
        "问个小问题: medium",
        "调试一个 bug: high",
        "改了一下午代码: max*",
      ]);
      await showExample("固定");
      expect(flow()).toEqual({
        mode: "fixed",
        arrived: "3",
        cards: ["低", "低*", "低*"],
      });
      await showExample("跟随客户端");
      expect(flow()).toEqual({
        mode: "passthrough",
        arrived: "3",
        cards: ["低", "高", "中"],
      });
      await closeHelp("thinking");

      const content = await openHelp("prompt");
      expect(
        content.querySelector<HTMLElement>("[data-prompt-demo]")?.dataset
          .promptStep,
      ).toBe("2");
      expect(promptBlocks()).toEqual([
        "identity",
        "prefix",
        "client",
        "suffix",
      ]);
      expect(
        content.querySelector('[data-prompt-block="suffix"]')?.textContent,
      ).toBe("你的末尾最后列出风险。");
      await closeHelp("prompt");
    });

    it("saves the switch and commits prompt text verbatim on blur", async () => {
      const dirty = await openTab();
      const toggle = container.querySelector<HTMLButtonElement>(
        '[role="switch"][aria-label="使用全局提示词"]',
      )!;
      expect(toggle.getAttribute("aria-checked")).toBe("false");
      await act(async () => toggle.click());
      expect(toggle.getAttribute("aria-checked")).toBe("true");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        system_prompt_enabled: true,
      });

      const prefix = promptField("加在开头");
      expect(prefix.value).toBe("");
      expect(prefix.placeholder).toBe("例如：所有回答都用简体中文。");
      expect(prefix.maxLength).toBe(20000);
      expect(promptField("加在末尾").placeholder).toBe(
        "例如：改代码之前，先说明打算怎么改。",
      );
      const text = "  所有回答都用简体中文。\n\n先给结论。  ";
      await typePrompt(prefix, "  所有回答都用简体中文。");
      // Enter inserts a newline: nothing prevents it and nothing is saved.
      const enter = new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      });
      await act(async () => prefix.dispatchEvent(enter));
      expect(enter.defaultPrevented).toBe(false);
      await typePrompt(prefix, text);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(1);
      expect(dirty).toHaveBeenLastCalledWith(false);

      await blur(prefix);
      expect(dirty).toHaveBeenLastCalledWith(true);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        system_prompt_prefix: text,
      });
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(2);
      expect(prefix.value).toBe(text);
      expect(dirty).toHaveBeenLastCalledWith(false);

      // Whitespace-only text clears the prompt.
      await typePrompt(prefix, " \n\t ");
      await blur(prefix);
      expect(prefix.value).toBe("");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
        system_prompt_prefix: "",
      });
      // An unchanged blur saves nothing.
      await blur(prefix);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(3);
    });

    it("keeps the prompt texts editable while off and announces character limits without saving drafts", async () => {
      await openTab();
      const input = promptField("加在开头");
      expect(input.disabled).toBe(false);
      const count = document.getElementById(
        input.getAttribute("aria-describedby")!,
      )!;
      expect(count.getAttribute("aria-live")).toBe("polite");
      expect(count.textContent).toBe("0 / 20000");
      await typePrompt(input, "字".repeat(18000));
      expect(count.textContent).toBe("18000 / 20000");
      expect(count.className).toContain("text-warning-foreground");
      await typePrompt(input, "字".repeat(20000));
      expect(count.className).toContain("text-destructive");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
      await blur(input);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
        system_prompt_prefix: "字".repeat(20000),
      });
    });

    it("names each option row and gives every option its own value", async () => {
      await openWith({
        reasoning_mode: "fixed",
        adaptive_preset: "custom",
        adaptive_min: "off",
        adaptive_max: "high",
      });
      const options = () =>
        [
          ...thinking().querySelectorAll<HTMLButtonElement>('[role="radio"]'),
        ].map(
          (option) => `${option.getAttribute("aria-label")}=${option.value}`,
        );
      expect(thinkingRows()).toEqual(["方式", "档位"]);
      expect(thinking().querySelector("small")).toBeNull();
      expect(options()).toEqual([
        "跟随客户端=passthrough",
        "固定=fixed",
        "自适应=adaptive",
        "关=off",
        "低=low",
        "中=medium",
        "高=high",
        "最高=max",
      ]);
      await act(async () => thinkingOption("自适应").click());
      expect(thinkingRows()).toEqual(["方式", "范围"]);
      expect(options().slice(3)).toEqual([
        "日常=everyday",
        "研究=research",
        "自定义=custom",
      ]);
      // Each preset's bars light its range.
      expect(
        [...thinking().querySelectorAll("[data-strength]")]
          .slice(-3)
          .map((meter) => meter.getAttribute("data-strength")),
      ).toEqual(["1-4", "2-4", "1-3"]);
      expect(rangeSelect("from")!.textContent).toBe("从关");
      expect(rangeSelect("to")!.textContent).toBe("到高");
    });

    it("keeps prompt text typed right before switching tabs", async () => {
      await openTab();
      const suffix = "改代码之前，\n先说明打算怎么改。";
      // Switching tabs removes the field; WebKit sends no blur for it.
      await typePrompt(promptField("加在末尾"), suffix);
      await selectTab("恢复与重试");
      await flushAutosave();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
        system_prompt_suffix: suffix,
      });
      await selectTab("请求改写");
      expect(promptField("加在末尾").value).toBe(suffix);
    });

    it("loads saved values and keeps the fields disabled while offline", async () => {
      const dirty = await openWith({
        system_prompt_enabled: true,
        system_prompt_prefix: "前缀\n第二行",
        system_prompt_suffix: "后缀",
        reasoning_mode: "adaptive",
        reasoning_level: "high",
        adaptive_preset: "custom",
        adaptive_min: "medium",
        adaptive_max: "max",
      });
      expect(dirty).toHaveBeenLastCalledWith(false);
      expect(
        container
          .querySelector('[role="switch"][aria-label="使用全局提示词"]')
          ?.getAttribute("aria-checked"),
      ).toBe("true");
      expect(promptField("加在开头").value).toBe("前缀\n第二行");
      expect(promptField("加在末尾").value).toBe("后缀");
      expect(checkedOptions()).toEqual(["自适应", "自定义"]);
      expect(rangeSelect("from")!.textContent).toBe("从中");
      await act(async () =>
        root.render(
          <RoutingSettingsPanel
            services={[]}
            ready={false}
            onDirtyChange={dirty}
          />,
        ),
      );
      const fieldsets = [
        ...container.querySelectorAll<HTMLFieldSetElement>("fieldset"),
      ];
      expect(fieldsets).not.toHaveLength(0);
      for (const fieldset of fieldsets) expect(fieldset.disabled).toBe(true);
      for (const element of [thinkingOption("自适应"), rangeSelect("from")!])
        expect(element.closest("fieldset")?.disabled).toBe(true);
      await flushAutosave();
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    });
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
    await flushAutosave();
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
        (h) => h.textContent === "同一会话沿用之前的模型和 API 提供商",
      )!;
      const toggle = container.querySelector<HTMLButtonElement>(
        `[role="switch"][aria-labelledby="${heading.id}"]`,
      )!;
      expect(toggle.getAttribute("aria-checked")).toBe(String(enabled ?? true));
      expect(dirty).toHaveBeenLastCalledWith(false);
      await act(async () => toggle.click());
      expect(container.textContent?.includes("闲置过期")).toBe(
        enabled === false,
      );
      await flushAutosave();
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
    await flushAutosave();
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
    await flushAutosave();
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
    await flushAutosave();
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
    await flushAutosave();
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

  it("labels an error inside the stream by the status rule it followed", async () => {
    await act(async () => {
      root.render(
        <RecoveryDetails
          value={{ action: "failover", reason: "stream_503", delay_ms: 0 }}
        />,
      );
    });
    expect(container.textContent).toContain("流内报错（按 HTTP 503 处理）");
    expect(container.textContent).not.toContain("stream_503");
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
    await flushAutosave();
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

  function inheritTimeoutSwitch() {
    return [...container.querySelectorAll("label")]
      .find((label) => label.textContent === "沿用网关基础响应超时（设置）")!
      .querySelector<HTMLButtonElement>('[role="switch"]')!;
  }

  const unlimitedHint = "设置中当前为 0（不限时）";
  const inactiveRule = "未设等待时限，此规则不会触发";

  it("flags the response timeout rule an unlimited gateway timeout disables", async () => {
    bridge.getPreferences.mockResolvedValue({
      values: { response_start_timeout_seconds: 0 },
    });
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      ),
    );
    await selectTab("恢复与重试");
    expect(container.textContent).toContain(unlimitedHint);
    await selectTab("错误规则");
    expect(container.textContent).toContain(inactiveRule);
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="等待响应超时 · 允许重试"]',
        )!
        .click(),
    );
    expect(container.textContent).not.toContain(inactiveRule);
  });

  it("states a finite gateway timeout and lets an own timeout replace it", async () => {
    bridge.getPreferences.mockResolvedValue({
      values: { response_start_timeout_seconds: 90 },
    });
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      ),
    );
    await selectTab("恢复与重试");
    expect(container.textContent).toContain("设置中当前为 90 秒。");
    await act(async () => inheritTimeoutSwitch().click());
    expect(container.textContent).not.toContain("设置中当前为");
    const input = [...container.querySelectorAll("label")]
      .find(
        (label) =>
          label.querySelector(":scope > span")?.textContent ===
          "等待响应超时（秒）",
      )!
      .querySelector<HTMLInputElement>("input")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "0");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await selectTab("错误规则");
    expect(container.textContent).toContain(inactiveRule);
  });

  it("omits the inherited timeout when preferences are unavailable", async () => {
    await act(async () =>
      root.render(
        <RoutingSettingsPanel services={[]} ready onDirtyChange={vi.fn()} />,
      ),
    );
    await selectTab("恢复与重试");
    expect(inheritTimeoutSwitch().getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).not.toContain("设置中当前为");
    await selectTab("错误规则");
    expect(container.textContent).not.toContain(inactiveRule);
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
    ).toEqual([
      "请求改写",
      "重定向",
      "恢复与重试",
      "错误规则",
      "会话粘性",
      "转发身份",
    ]);
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
    await flushAutosave();
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
      kind: "openai" as const,
      enabled: true,
      models: ["gpt-5", "claude-sonnet-4-5"],
      capabilities: [],
    },
    {
      id: "service_b",
      name: "B",
      kind: "openai" as const,
      enabled: true,
      models: ["gpt-5"],
      capabilities: [],
    },
    {
      id: "service_off",
      name: "Off",
      kind: "openai" as const,
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
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
  }

  function button(label: string) {
    return [...container.querySelectorAll("button")].find(
      (element) => element.textContent === label,
    )!;
  }

  it("opens on request rewriting and saves only the replaced redirect list", async () => {
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
    expect(tabs[0].textContent).toBe("请求改写");
    expect(tabs[0].getAttribute("aria-selected")).toBe("true");
    expect(
      [...container.querySelectorAll('[role="tabpanel"] h2')].map(
        (element) => element.textContent,
      ),
    ).toEqual(["请求图片压缩", "思考强度", "全局提示词"]);
    expect(container.textContent).not.toContain("Codex 自动审查");
    await selectTab("重定向");
    expect(container.textContent).toContain("Codex 自动审查");
    expect(container.textContent).not.toContain("请求图片压缩");
    // A document without model_redirects is not a pending change.
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(button("保存默认策略")).toBeUndefined();

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

    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      model_redirects: [{ from: "gpt-4o", to: "gpt-5", enabled: true }],
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("automatically persists built-in configuration and toggles", async () => {
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
    await selectTab("重定向");
    const toggle = () =>
      container.querySelector<HTMLButtonElement>(
        '[aria-label="启用 codex-auto-review 的重定向"]',
      )!;
    expect(toggle().getAttribute("aria-checked")).toBe("false");
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await act(async () => toggle().click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: true },
      ],
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
    await act(async () => toggle().click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: false },
      ],
    });
    expect(redirectField("Codex 自动审查的目标模型").value).toBe("gpt-5");
  });

  it("puts image compression first and shows the quality only for lossy mode", async () => {
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
    const panel = () =>
      container.querySelector<HTMLElement>(
        '[data-testid="image-compression-settings"]',
      )!;
    expect(
      container.querySelector('[role="tabpanel"] > fieldset')!
        .firstElementChild,
    ).toBe(panel());
    const toggle = () =>
      panel().querySelector<HTMLButtonElement>(
        '[role="switch"][aria-label="启用请求图片压缩"]',
      )!;
    const option = (label: string) =>
      panel().querySelector<HTMLButtonElement>(
        `[role="radio"][aria-label="${label}"]`,
      )!;
    const checked = () =>
      [...panel().querySelectorAll('[role="radio"][data-state="checked"]')].map(
        (item) => item.getAttribute("aria-label"),
      );
    const quality = () =>
      panel().querySelector<HTMLInputElement>('input[aria-label="WebP 质量"]');
    expect(toggle().getAttribute("aria-checked")).toBe("false");
    expect(checked()).toEqual(["无损"]);
    expect(quality()).toBeNull();
    expect(panel().textContent).toContain("转发前把请求里的图片转成 WebP");
    expect(panel().textContent).toContain("像素完全不变");
    // The details stay behind the help button.
    expect(panel().textContent).not.toContain("base64");

    await act(async () => toggle().click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      image_compression: { enabled: true, mode: "lossless", quality: 85 },
    });

    await act(async () => option("有损").click());
    expect(checked()).toEqual(["有损", "清晰"]);
    expect(quality()!.value).toBe("85");
    expect(panel().textContent).toContain("JPEG 也会压缩");
    await act(async () => option("省流").click());
    expect(quality()!.value).toBe("60");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      image_compression: { enabled: true, mode: "lossy", quality: 60 },
    });

    // A typed quality that matches no preset leaves every preset unselected.
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(quality(), "70");
      quality()!.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(checked()).toEqual(["有损"]);
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      image_compression: { enabled: true, mode: "lossy", quality: 70 },
    });

    // Lossless keeps the chosen quality for the next switch back.
    await act(async () => option("无损").click());
    expect(quality()).toBeNull();
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      image_compression: { enabled: true, mode: "lossless", quality: 70 },
    });
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(4);
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("loads saved image compression and keeps it disabled while offline", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      image_compression: { enabled: true, mode: "lossy", quality: 70 },
    });
    const render = (ready: boolean) =>
      act(async () =>
        root.render(
          <RoutingSettingsPanel
            services={services}
            ready={ready}
            onDirtyChange={vi.fn()}
          />,
        ),
      );
    await render(true);
    await render(false);
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="启用请求图片压缩"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    expect(toggle.disabled).toBe(true);
    const quality = container.querySelector<HTMLInputElement>(
      'input[aria-label="WebP 质量"]',
    )!;
    expect(quality.value).toBe("70");
    expect(quality.disabled).toBe(true);
    for (const option of container.querySelectorAll<HTMLButtonElement>(
      '[data-testid="image-compression-settings"] [role="radio"]',
    ))
      expect(option.disabled).toBe(true);
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
  });

  it("does not save a partial model name until the edit is committed", async () => {
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    await selectTab("重定向");
    const input = redirectField("Codex 自动审查的目标模型");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "gpt-");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain("输入完成后自动保存");
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: false },
      ],
    });
  });

  it("serializes writes and keeps a newer edit when an older response arrives", async () => {
    let finish!: (result: unknown) => void;
    bridge.updateRoutingSettings.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
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
    await selectTab("重定向");
    const toggle = () =>
      container.querySelector<HTMLButtonElement>(
        '[aria-label="启用 codex-auto-review 的重定向"]',
      )!;
    await act(async () => toggle().click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(1);
    expect(toggle().disabled).toBe(false);
    // Revert while the enable request is still in flight.
    await act(async () => toggle().click());
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(1);
    await act(async () =>
      finish({
        ...settings(),
        model_redirects: [
          { from: "codex-auto-review", to: "gpt-5.6-luna", enabled: true },
        ],
      }),
    );
    expect(toggle().getAttribute("aria-checked")).toBe("false");
    expect(redirectField("Codex 自动审查的目标模型").value).toBe("gpt-5");
    expect(dirty).toHaveBeenLastCalledWith(true);
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(2);
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      model_redirects: [
        { from: "codex-auto-review", to: "gpt-5", enabled: false },
      ],
    });
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(container.textContent).toContain("已自动保存");
  });

  it("keeps failed edits without retrying in a loop and retries after another edit", async () => {
    bridge.updateRoutingSettings.mockRejectedValueOnce(
      new Error("Core unavailable"),
    );
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    await selectTab("重定向");
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await flushAutosave();
    expect(container.textContent).toContain("自动保存失败，修改已保留。");
    expect(redirectField("Codex 自动审查的目标模型").value).toBe("gpt-5");
    await act(async () => vi.advanceTimersByTimeAsync(10000));
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(1);
    await typeRedirect("Codex 自动审查的目标模型", "claude-sonnet-4-5");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(2);
    expect(container.textContent).not.toContain("自动保存失败");
  });

  it("words a refused save in the operator's language", async () => {
    bridge.updateRoutingSettings.mockRejectedValueOnce(
      new Error(
        'PATCH /control/v1/routing-settings returned 422 Unprocessable Entity: {"error":{"code":"image_provider_unsupported","message":"image_generation: the selected provider has no Images API.","retryable":false,"details":[]},"request_id":"req_0"}',
      ),
    );
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    await selectTab("重定向");
    await typeRedirect("Codex 自动审查的目标模型", "gpt-5");
    await flushAutosave();
    expect(container.textContent).toContain(
      "自动保存失败，修改已保留。 图片生成选用的 API 提供商没有 Images API。",
    );
    expect(container.textContent).not.toContain("returned 422");
  });

  it("never autosaves a half-typed built-in tool model", async () => {
    const webSearch = {
      enabled: true,
      backend: "upstream" as const,
      service_id: "service_r",
      model: "gpt-5.6-sol",
    };
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      builtin_tools: {
        web_search: webSearch,
        image_generation: { enabled: false, backend: "upstream" },
      },
    });
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={[
            {
              id: "service_r",
              name: "R",
              kind: "openai",
              enabled: true,
              models: ["gpt-5.6-sol", "gpt-5.6-luna"],
              capabilities: [
                {
                  protocol: "openai.responses",
                  mode: "native",
                  streaming: true,
                },
              ],
            },
          ]}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    await selectTab("重定向");
    const input = redirectField("网页搜索执行模型");
    expect(input.value).toBe("gpt-5.6-sol");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "gpt-5.6-l");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    await typeRedirect("网页搜索执行模型", "gpt-5.6-luna");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      builtin_tools: {
        web_search: { ...webSearch, model: "gpt-5.6-luna" },
        image_generation: { enabled: false, backend: "upstream" },
      },
    });
  });

  it("saves other settings while a built-in tool is incomplete", async () => {
    const search = {
      enabled: false,
      backend: "upstream" as const,
      service_id: "service_r",
    };
    const images = {
      enabled: true,
      backend: "service_images" as const,
      service_id: "service_r",
      model: "gpt-image-1",
    };
    let stored: Record<string, unknown> = {
      ...settings(),
      builtin_tools: { web_search: search, image_generation: images },
    };
    bridge.getRoutingSettings.mockResolvedValue(stored);
    bridge.updateRoutingSettings.mockImplementation(
      async (patch) => (stored = { ...stored, ...patch }),
    );
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={[
            {
              id: "service_r",
              name: "R",
              kind: "openai",
              enabled: true,
              models: ["gpt-5.6-sol", "gpt-image-1"],
              capabilities: [
                {
                  protocol: "openai.responses",
                  mode: "native",
                  streaming: true,
                },
              ],
            },
          ]}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    const status = () =>
      container.querySelector('[role="status"][aria-live="polite"]')
        ?.textContent;
    const toggle = (label: string) =>
      container.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!;
    const notice =
      "「重定向」中有未填完的设置，填完后会自动保存。其他修改照常保存。";

    await selectTab("重定向");
    await act(async () => toggle("启用网页搜索接管").click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(status()).toBe("未填完的设置未保存");

    // The other tool in the same document still saves on its own.
    await act(async () => toggle("启用图片生成接管").click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: {
        web_search: search,
        image_generation: { ...images, enabled: false },
      },
    });
    expect(toggle("启用网页搜索接管").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(container.textContent).not.toContain(notice);

    await selectTab("转发身份");
    expect(container.textContent).toContain(notice);
    await act(async () => toggle("统一 Codex 客户端身份").click());
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      codex_identity_enforcement: false,
    });
    expect(status()).toBe("未填完的设置未保存");

    await selectTab("重定向");
    expect(toggle("启用网页搜索接管").getAttribute("aria-checked")).toBe(
      "true",
    );
    await typeRedirect("网页搜索执行模型", "gpt-5.6-sol");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: {
        web_search: { ...search, enabled: true, model: "gpt-5.6-sol" },
        image_generation: { ...images, enabled: false },
      },
    });
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(3);
    expect(status()).toBe("已自动保存，新请求立即生效");
  });

  it("retains backend drafts across tabs and saves only the complete active backend", async () => {
    const search = {
      enabled: true,
      backend: "upstream" as const,
      service_id: "newapi_main",
      model: "gpt-5.5",
    };
    const images = { enabled: false, backend: "upstream" as const };
    let stored = {
      ...settings(),
      builtin_tools: { web_search: search, image_generation: images },
    };
    bridge.getRoutingSettings.mockResolvedValue(stored);
    bridge.updateRoutingSettings.mockImplementation(
      async (patch) => (stored = { ...stored, ...patch }),
    );
    const services = [
      {
        id: "newapi_main",
        name: "主站",
        kind: "newapi" as const,
        enabled: true,
        models: ["gpt-5.5", "search-model"],
        capabilities: [
          {
            protocol: "openai.responses",
            mode: "native" as const,
            streaming: true,
          },
        ],
      },
    ];
    async function choose(label: string, option: string) {
      await act(async () => {
        container
          .querySelector<HTMLButtonElement>(
            `button[role="combobox"][aria-label="${label}"]`,
          )!
          .dispatchEvent(
            new PointerEvent("pointerdown", {
              bubbles: true,
              button: 0,
              pointerType: "mouse",
            }),
          );
      });
      await act(async () => {
        const item = [
          ...document.querySelectorAll<HTMLElement>('[role="option"]'),
        ].find((candidate) => candidate.textContent?.trim() === option);
        expect(item).toBeDefined();
        item!.click();
      });
      const settled = settleClosedLayer();
      await vi.advanceTimersByTimeAsync(0);
      await settled;
    }
    await act(async () =>
      root.render(
        <RoutingSettingsPanel
          services={services}
          ready
          onDirtyChange={vi.fn()}
        />,
      ),
    );
    await selectTab("重定向");
    await choose("网页搜索执行方", "提供商的搜索 API");
    expect(redirectField("网页搜索执行模型").value).toBe("");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain("自动保存失败");

    await choose("网页搜索提供商", "主站");
    await typeRedirect("网页搜索执行模型", "search-model");
    await flushAutosave();
    const providerSearch = {
      ...search,
      backend: "service_search",
      model: "search-model",
    };
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: { web_search: providerSearch, image_generation: images },
    });
    // An invalid URL stays in its own draft and cannot block another backend.
    await choose("网页搜索执行方", "Tavily 兼容 API");
    const invalidUrl = container.querySelector<HTMLInputElement>(
      'input[aria-label="网页搜索 API 地址"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(invalidUrl, "https://");
      invalidUrl.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () =>
      invalidUrl.dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
    );
    const savedCount = bridge.updateRoutingSettings.mock.calls.length;
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(savedCount);
    await choose("网页搜索执行方", "Responses · web_search");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: { web_search: search, image_generation: images },
    });
    await choose("网页搜索执行方", "Tavily 兼容 API");
    expect(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="网页搜索 API 地址"]',
      )!.value,
    ).toBe("https://");

    await choose("网页搜索执行方", "Tavily 兼容 API");
    const url = container.querySelector<HTMLInputElement>(
      'input[aria-label="网页搜索 API 地址"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(url, "https://search.example");
      url.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () =>
      url.dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
    );
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: {
        web_search: {
          enabled: true,
          backend: "external",
          base_url: "https://search.example",
        },
        image_generation: images,
      },
    });
    await selectTab("会话粘性");
    await selectTab("重定向");
    await choose("网页搜索执行方", "Responses · web_search");
    expect(redirectField("网页搜索执行模型").value).toBe("gpt-5.5");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: { web_search: search, image_generation: images },
    });
    await choose("网页搜索执行方", "提供商的搜索 API");
    expect(redirectField("网页搜索执行模型").value).toBe("search-model");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      builtin_tools: { web_search: providerSearch, image_generation: images },
    });
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
    await selectTab("重定向");
    await act(async () => button("添加重定向").click());
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await selectTab("恢复与重试");
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(
      container.querySelector('[role="tab"][aria-selected="true"]')
        ?.textContent,
    ).toBe("恢复与重试");
    await selectTab("重定向");
    expect(
      [...container.querySelectorAll('[role="alert"]')].map(
        (element) => element.textContent,
      ),
    ).toEqual(["请填写请求模型。"]);

    await typeRedirect("第 2 条规则的请求模型", "claude-3-opus");
    await typeRedirect("第 2 条规则的目标模型", "gpt-4o");
    await flushAutosave();
    expect(
      [...container.querySelectorAll('[role="alert"]')].map(
        (element) => element.textContent,
      ),
    ).toEqual(["目标模型是另一条规则的请求模型；重定向只生效一次，不能串联。"]);
    await flushAutosave();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();

    await typeRedirect("第 2 条规则的目标模型", "claude-sonnet-4-5");
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await flushAutosave();
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
    expect(button("保存默认策略")).toBeUndefined();
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
    await flushAutosave();
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
    await flushAutosave();
    const saved =
      bridge.updateRoutingSettings.mock.calls[0][0].default_failure_policy;
    expect(saved.http_status["418"]).toBe("retry_and_failover");
    expect(saved.http_status).not.toHaveProperty("401");
    await act(async () =>
      [...container.querySelectorAll("button")]
        .find((button) => button.textContent === "重置此组设置")!
        .click(),
    );
    await flushAutosave();
    expect(bridge.updateRoutingSettings).toHaveBeenLastCalledWith({
      default_failure_policy: settings().default_failure_policy,
    });
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
