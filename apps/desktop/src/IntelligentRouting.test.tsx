// @vitest-environment happy-dom

import { act } from "react";
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

import { IntelligentRouting } from "./IntelligentRouting";
import { defaultClassifierSettings } from "./classifier-settings-model";
import { applyLocale } from "./i18n";
import { defaultFailurePolicy } from "./failure-policy-model";
import type { RoutableService } from "./service-model";

const services: RoutableService[] = [
  {
    id: "svc",
    name: "DeepSeek",
    kind: "deepseek",
    enabled: true,
    models: ["deepseek-chat", "deepseek-reasoner"],
    capabilities: [],
  },
];

function routingSettings() {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
    model_routing_groups: [
      {
        id: "g",
        enabled: true,
        members: [
          { id: "a", service_id: "svc", model: "deepseek-chat" },
          { id: "b", service_id: "svc", model: "deepseek-reasoner" },
        ],
        default_member_id: "a",
        rules: [
          {
            id: "r",
            priority: 10,
            when: { field: "task_type", in: ["coding"] },
            target_member_id: "b",
          },
        ],
      },
    ],
  };
}

function tabs(): HTMLElement[] {
  return [
    ...document.querySelectorAll<HTMLElement>(
      '[data-slot="tabs-list"] [role="tab"]',
    ),
  ];
}

function tab(label: string): HTMLButtonElement {
  const match = tabs().find((item) =>
    item.textContent?.trim().startsWith(label),
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing tab: ${label}`);
  }
  return match;
}

// Radix activates a trigger on mousedown, not click.
async function selectTab(label: string): Promise<void> {
  await act(async () => {
    tab(label).dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
    await Promise.resolve();
  });
}

describe("intelligent routing workspaces", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getRoutingSettings
      .mockReset()
      .mockImplementation(async () => routingSettings());
    bridge.updateRoutingSettings.mockReset();
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue({ ...defaultClassifierSettings(), enabled: true });
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    bridge.updateClassifierSettings.mockReset();
    bridge.classifierDryRun.mockReset();
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

  async function flush() {
    await act(async () => {
      for (let index = 0; index < 5; index += 1) await Promise.resolve();
    });
  }

  async function render(
    view?: "groups" | "classifier" | "dryRun",
    onDirtyChange: (dirty: boolean) => void = vi.fn(),
  ) {
    await act(async () => {
      root.render(
        <IntelligentRouting
          initialView={view}
          services={services}
          ready
          onDirtyChange={onDirtyChange}
        />,
      );
    });
    await flush();
  }

  async function click(element: Element | null | undefined) {
    if (!element) throw new Error("Missing element to click");
    await act(async () => {
      (element as HTMLElement).click();
      await Promise.resolve();
    });
    await flush();
  }

  function button(text: string): HTMLButtonElement | undefined {
    return [...container.querySelectorAll<HTMLButtonElement>("button")].find(
      (item) => item.textContent?.trim() === text,
    );
  }

  const actions = () =>
    container.querySelector("[data-intelligent-routing-actions]")!;

  async function openEditor() {
    await click(container.querySelector('[aria-label="编辑 astrlink/g"]'));
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).not.toBeNull();
  }

  it("shows three tabs: routing groups, classifier and dry run", async () => {
    await render();
    expect(
      tabs().map((item) => item.textContent?.replace(/\d+$/, "").trim()),
    ).toEqual(["路由组", "分类器", "试运行"]);
    expect(tab("路由组").getAttribute("data-state")).toBe("active");
  });

  it("honours an explicit view so a tray jump can land on a tab", async () => {
    await render("dryRun");
    expect(tab("试运行").getAttribute("data-state")).toBe("active");
    expect(
      container.querySelector('[data-testid="classifier-dry-run-panel"]'),
    ).not.toBeNull();
    // Nothing runs until the user asks.
    expect(bridge.classifierDryRun).not.toHaveBeenCalled();
  });

  it("gives the active tab's actions the row beside the tabs", async () => {
    await render();
    expect(actions().textContent).toContain("新建路由组");
    await selectTab("分类器");
    expect(actions().textContent).not.toContain("新建路由组");
    expect(actions().textContent).toContain("保存");
    await selectTab("试运行");
    expect(actions().textContent).toBe("");
  });

  it("opens a group as its own page and comes back to the tabs", async () => {
    await render();
    await openEditor();
    // The editor brings its own header with a way back; the tab row steps aside.
    expect(
      container.querySelector('[data-slot="tabs-list"]')?.closest("[hidden]"),
    ).not.toBeNull();
    expect(container.querySelector("h1")?.textContent).toBe("astrlink/g");
    await click(button("智能路由"));
    expect(
      container.querySelector('[data-slot="tabs-list"]')?.closest("[hidden]"),
    ).toBeNull();
    expect(
      container.querySelector('[data-testid="routing-group-list"]'),
    ).not.toBeNull();
  });

  it("jumps from the editor to the dry run with the group chosen", async () => {
    await render();
    await openEditor();
    // An unsaved edit makes the dry run say it uses the saved version.
    await click(container.querySelector('[data-routing-remove-rule="0"]'));
    await click(container.querySelector("[data-routing-dry-run]"));
    expect(tab("试运行").getAttribute("data-state")).toBe("active");
    expect(
      container.querySelector("[data-dry-run-group]")?.textContent,
    ).toContain("astrlink/g");
    expect(
      container.querySelector("[data-classifier-dry-run-unsaved]")?.textContent,
    ).toContain("astrlink/g");
    // Back on the groups tab the draft is still there.
    await selectTab("路由组");
    expect(container.querySelectorAll("[data-routing-rule]")).toHaveLength(0);
    expect(button("保存")?.disabled).toBe(false);
  });

  it("keeps the dry run on a group renamed or deleted in the editor", async () => {
    bridge.updateRoutingSettings.mockImplementation(
      async (patch: Record<string, unknown>) => ({
        ...routingSettings(),
        ...patch,
      }),
    );
    await render();
    await openEditor();
    await click(container.querySelector("[data-routing-dry-run]"));
    await selectTab("路由组");
    await click(container.querySelector("[data-routing-rename]"));
    const input = document.querySelector<HTMLInputElement>(
      '[data-slot="popover-content"] input',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "g2");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click(button("保存"));
    await selectTab("试运行");
    expect(
      container.querySelector("[data-dry-run-group]")?.textContent,
    ).toContain("astrlink/g2");

    await selectTab("路由组");
    await click(button("删除路由组"));
    const confirm = document.querySelector('[role="alertdialog"]')!;
    await click(
      [...confirm.querySelectorAll("button")].find(
        (item) => item.textContent?.trim() === "删除",
      ),
    );
    await selectTab("试运行");
    expect(
      container.querySelector("[data-dry-run-group]")?.textContent,
    ).not.toContain("astrlink/g2");
  });

  it("opens the editor from a dry run's rule verdicts", async () => {
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.9,
      fallback_reason: null,
      duration_ms: 12,
      sanitized_preview: "写一个排序函数",
      decision: {
        member_id: "b",
        service_id: "svc",
        model: "deepseek-reasoner",
        rule_id: "r",
      },
      fields: [],
      dimensions: [],
      rules: [
        {
          rule_id: "r",
          priority: 10,
          target_member_id: "b",
          target_model: "deepseek-reasoner",
          state: "true",
          selected: true,
          target_missing: false,
          fields: [],
        },
      ],
    });
    await render("dryRun");
    await click(container.querySelector("[data-dry-run-start]"));
    expect(bridge.classifierDryRun).toHaveBeenCalledWith(
      expect.objectContaining({ group_id: "g" }),
    );
    await click(button("编辑规则"));
    expect(tab("路由组").getAttribute("data-state")).toBe("active");
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).not.toBeNull();
  });

  it("keeps the groups dirty flag while another tab changes", async () => {
    const dirty = vi.fn();
    await render("groups", dirty);
    await openEditor();
    await click(container.querySelector('[data-routing-remove-rule="0"]'));
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
    await selectTab("分类器");
    await selectTab("试运行");
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
  });

  it("keeps an unsaved classifier draft alive across a tab switch", async () => {
    const dirty = vi.fn();
    await render("classifier", dirty);
    const panel = container.querySelector(
      '[data-testid="classifier-settings-panel"]',
    );
    const toggle = panel?.querySelector<HTMLButtonElement>('[role="switch"]');
    await click(toggle);
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
    await selectTab("试运行");
    await selectTab("分类器");
    expect(
      container.querySelector('[data-testid="classifier-settings-panel"]'),
    ).toBe(panel);
    expect(toggle?.getAttribute("data-state")).toBe("unchecked");
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
  });
});
