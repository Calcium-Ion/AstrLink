// @vitest-environment happy-dom

/**
 * §8.3: the required explanations must stay on screen where they are needed.
 * After the redesign most of them are visible text next to the control they
 * explain (the time window's rules sit in its editor, the label scope and the
 * reuse of an existing model in the classifier tab); the rest live behind a
 * `?` that must be a real, keyboard-focusable button. This suite pins both, so
 * a dropped explanation fails here instead of in review.
 */

import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  builtinToolAction: vi.fn().mockResolvedValue({ configured: false }),
  classifierCredentialAction: vi.fn().mockResolvedValue({ configured: false }),
  classifierDryRun: vi.fn(),
  getClassifierSettings: vi.fn(),
  getRoutingSettings: vi.fn(),
  updateClassifierSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn(), error: vi.fn() } }));

import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import { defaultClassifierSettings } from "./classifier-settings-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale, i18n } from "./i18n";

function tooltipTriggers(): HTMLButtonElement[] {
  return [
    ...document.querySelectorAll<HTMLButtonElement>(
      'button[data-slot="popover-trigger"][aria-label]',
    ),
  ].filter((trigger) => trigger.querySelector("svg") !== null);
}

describe("model routing explanations", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    // One real group with a time rule, so the editor and the time window's
    // editor actually render.
    bridge.getRoutingSettings.mockReset().mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [
            {
              id: "r",
              priority: 10,
              target_member_id: "a",
              when: { field: "system_time", within: ["22:00", "08:00"] },
            },
          ],
        },
      ],
    });
    bridge.getClassifierSettings.mockReset().mockResolvedValue({
      ...defaultClassifierSettings(),
      classifier_source: "existing_model",
    });
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    document.body.innerHTML = "";
  });

  async function mount(node: ReactNode): Promise<void> {
    await act(async () => {
      root.render(node);
    });
    await act(async () => {
      for (let index = 0; index < 5; index += 1) await Promise.resolve();
    });
  }

  async function click(element: Element | null) {
    if (!element) throw new Error("Missing element");
    await act(async () => {
      (element as HTMLElement).click();
      await Promise.resolve();
    });
  }

  it("keeps every `?` a focusable button with a name", async () => {
    await mount(
      <>
        <ModelRoutingGroupsPanel ready services={[]} onDirtyChange={() => {}} />
        <ClassifierSettingsPanel ready services={[]} onDirtyChange={() => {}} />
        <ClassifierDryRunPanel ready />
      </>,
    );
    await click(container.querySelector('[aria-label="编辑 astrlink/g"]'));
    const triggers = tooltipTriggers();
    expect(triggers.length).toBeGreaterThan(0);
    for (const trigger of triggers) {
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger.disabled).toBe(false);
      expect(trigger.getAttribute("aria-label")).toBeTruthy();
      trigger.focus();
      expect(document.activeElement).toBe(trigger);
    }
    const labels = triggers.map((item) => item.getAttribute("aria-label"));
    // 1 first match wins and 5 an unknown fact does not match: the rules help.
    expect(labels).toContain(i18n.t("routingGroups.editor.rulesHelpLabel"));
    // 6 the dry run calls the classifier for real.
    expect(labels).toContain(i18n.t("classifier.dryRun.runHintLabel"));
  });

  it("says first match wins and unknown does not match in the rules help", () => {
    const help = i18n.t("routingGroups.editor.rulesHelp");
    expect(help).toContain("第一条满足的规则决定用哪个模型");
    expect(help).toContain("取不到时");
  });

  it("explains the time window where it is edited", async () => {
    await mount(
      <ModelRoutingGroupsPanel ready services={[]} onDirtyChange={() => {}} />,
    );
    await click(container.querySelector('[aria-label="编辑 astrlink/g"]'));
    await click(
      container.querySelector(
        '[data-routing-rule="0"] [data-slot="condition-chip"]',
      ),
    );
    const editor = document.querySelector('[data-slot="popover-content"]')!;
    // 2 time zone, 3 half-open window, 4 cross-midnight.
    expect(editor.textContent).toContain(
      i18n.t("routingConditions.editor.timeNote"),
    );
    expect(editor.textContent).toContain("本地时间");
    expect(editor.textContent).toContain("不包含结束时刻");
    expect(editor.textContent).toContain(
      i18n.t("routingConditions.editor.crossesMidnight", {
        start: "22:00",
        end: "08:00",
      }),
    );
  });

  it("states the classifier's scope and credentials in its tab", async () => {
    await mount(
      <ClassifierSettingsPanel ready services={[]} onDirtyChange={() => {}} />,
    );
    // 7 the dimensions and their options apply to every routing group.
    expect(container.textContent).toContain(
      i18n.t("classifierTab.dimensionsDescription"),
    );
    // 8 reusing an existing model needs no separate key.
    expect(container.textContent).toContain(
      i18n.t("classifierTab.sourceExistingHint"),
    );
  });

  it("marks that a dry run calls the classifier once", async () => {
    await mount(<ClassifierDryRunPanel ready />);
    expect(container.textContent).toContain(
      i18n.t("dryRunTab.callsClassifier"),
    );
    expect(i18n.t("classifier.dryRun.runHint")).not.toBe(
      "classifier.dryRun.runHint",
    );
  });
});
