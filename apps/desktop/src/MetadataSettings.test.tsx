// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  listMetadataAttributes: vi.fn(),
  createMetadataAttribute: vi.fn(),
  updateMetadataAttribute: vi.fn(),
  deleteMetadataAttribute: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { MetadataSettings } from "./MetadataSettings";
import type { MetadataAttribute } from "./plugin-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";

const env: MetadataAttribute = {
  key: "env",
  label: "环境",
  description: "请求来自哪个环境",
  pattern: "^(dev|prod)$",
  owner: "user",
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  header: "X-Astrlink-Meta-Env",
};

const project: MetadataAttribute = {
  key: "project",
  label: "项目",
  description: "",
  pattern: "^[a-z][a-z0-9-]*$",
  marker: "memory-project",
  owner: "powercontext",
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  header: "X-Astrlink-Meta-Project",
};

const orphan: MetadataAttribute = {
  ...project,
  key: "team",
  label: "团队",
  marker: undefined,
  owner: "team-rules",
  header: "X-Astrlink-Meta-Team",
  owner_missing: true,
};

function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
    (candidate) =>
      candidate.textContent?.trim() === label ||
      candidate.getAttribute("aria-label") === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

function row(label: string): HTMLElement {
  const match = [
    ...document.querySelectorAll<HTMLElement>(
      '[data-testid="metadata-attribute"]',
    ),
  ].find((candidate) => candidate.textContent?.includes(label));
  if (!match) throw new Error(`Missing attribute row: ${label}`);
  return match;
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
  });
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>(selector);
  if (!input) throw new Error(`Missing input: ${selector}`);
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!valueSetter) throw new Error("Missing input value setter");
  await act(async () => {
    valueSetter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("MetadataSettings", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  let removeAnimations: () => void;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    removeAnimations = installDialogAnimations();
    bridgeMocks.listMetadataAttributes.mockResolvedValue([
      env,
      project,
      orphan,
    ]);
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    removeAnimations();
    container.remove();
  });

  async function render() {
    await act(async () => {
      reactRoot.render(<MetadataSettings />);
    });
  }

  it("lets the user change only their own attributes", async () => {
    await render();
    expect(
      document.querySelectorAll('[data-testid="metadata-attribute"]'),
    ).toHaveLength(3);
    expect(row("环境").querySelectorAll("button")).toHaveLength(2);
    expect(row("项目").textContent).toContain("插件 powercontext");
    expect(row("项目").textContent).toContain("memory-project");
    expect(row("项目").querySelector("button")).toBeNull();
    // A plugin that is gone leaves its attribute to the user to delete.
    expect(row("团队").textContent).toContain("插件 team-rules（已移除）");
    expect(row("团队").querySelectorAll("button")).toHaveLength(1);
    expect(button("删除团队", row("团队"))).toBeTruthy();
  });

  it("checks a new attribute before creating it", async () => {
    bridgeMocks.createMetadataAttribute.mockResolvedValue(env);
    await render();
    await click(button("新增属性"));
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
    if (!dialog) throw new Error("Missing the attribute dialog");
    await setInput("#metadata-key", "Env");
    await setInput("#metadata-label", "环境");
    await setInput("#metadata-pattern", "dev|prod");
    await click(button("保存", dialog));
    expect(dialog.textContent).toContain("键须以小写字母开头");
    await setInput("#metadata-key", "env");
    await click(button("保存", dialog));
    expect(dialog.textContent).toContain("校验规则须以 ^ 开头、$ 结尾");
    await setInput("#metadata-pattern", "^(dev|prod)$");
    await setInput("#metadata-marker", "astrlink-env");
    await click(button("保存", dialog));
    expect(dialog.textContent).toContain("且不能包含 astrlink");
    expect(bridgeMocks.createMetadataAttribute).not.toHaveBeenCalled();
    await setInput("#metadata-marker", "");
    await click(button("保存", dialog));
    await finishExitAnimations();
    expect(bridgeMocks.createMetadataAttribute).toHaveBeenCalledWith({
      key: "env",
      label: "环境",
      description: "",
      pattern: "^(dev|prod)$",
      marker: "",
    });
    expect(bridgeMocks.listMetadataAttributes).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("keeps an attribute's key when editing it", async () => {
    bridgeMocks.updateMetadataAttribute.mockResolvedValue(env);
    await render();
    await click(button("编辑环境", row("环境")));
    const key = document.querySelector<HTMLInputElement>("#metadata-key");
    expect(key?.value).toBe("env");
    expect(key?.disabled).toBe(true);
    await setInput("#metadata-label", " 部署环境 ");
    await click(
      button("保存", document.querySelector('[role="dialog"]') as HTMLElement),
    );
    expect(bridgeMocks.updateMetadataAttribute).toHaveBeenCalledWith("env", {
      key: "env",
      label: "部署环境",
      description: "请求来自哪个环境",
      pattern: "^(dev|prod)$",
      marker: "",
    });
  });

  it("deletes an attribute after confirmation", async () => {
    bridgeMocks.deleteMetadataAttribute.mockResolvedValue(undefined);
    await render();
    await click(button("删除环境", row("环境")));
    const confirm = document.querySelector<HTMLElement>('[role="alertdialog"]');
    expect(confirm?.textContent).toContain("删除这个属性？");
    expect(confirm?.textContent).toContain("「环境」和它的所有取值");
    await click(button("删除", confirm as HTMLElement));
    await finishExitAnimations();
    expect(bridgeMocks.deleteMetadataAttribute).toHaveBeenCalledWith("env");
    expect(bridgeMocks.listMetadataAttributes).toHaveBeenCalledTimes(2);
  });
});
