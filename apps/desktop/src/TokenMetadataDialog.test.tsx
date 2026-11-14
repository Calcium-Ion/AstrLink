// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  listMetadataAttributes: vi.fn(),
  getAccessTokenMetadata: vi.fn(),
  saveAccessTokenMetadata: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import type { AccessTokenSummary } from "./access-token-model";
import type { MetadataAttribute } from "./plugin-model";
import { metadataValueFits, TokenMetadataDialog } from "./TokenMetadataDialog";
import { installDialogAnimations } from "./lib/test-dialog-animations";

const token: AccessTokenSummary = {
  id: "token_01",
  name: "VS Code",
  hint: "astr_…K8Q2",
  source: "user",
  created_at: "2026-07-24T10:30:00Z",
  service_scope: "all",
  service_ids: [],
};

function attribute(
  key: string,
  label: string,
  pattern: string,
): MetadataAttribute {
  return {
    key,
    label,
    description: "",
    pattern,
    owner: "user",
    created_at: "2026-10-18T00:00:00Z",
    updated_at: "2026-10-18T00:00:00Z",
    header: `X-Astrlink-Meta-${key}`,
  };
}

const env = attribute("env", "环境", "^(dev|prod)$");
const project = attribute("project", "项目", "^[a-z]+$");

function button(label: string): HTMLButtonElement {
  const match = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
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

describe("metadataValueFits", () => {
  it("accepts the reserved none and what the pattern accepts", () => {
    expect(metadataValueFits(env, "prod")).toBe(true);
    expect(metadataValueFits(env, "none")).toBe(true);
    expect(metadataValueFits(env, "production")).toBe(false);
    expect(metadataValueFits(attribute("bad", "坏", "^("), "x")).toBe(false);
  });
});

describe("TokenMetadataDialog", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  let removeAnimations: () => void;
  const onClose = vi.fn();

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    removeAnimations = installDialogAnimations();
    bridgeMocks.listMetadataAttributes.mockResolvedValue([env, project]);
    bridgeMocks.getAccessTokenMetadata.mockResolvedValue({ env: "dev" });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    removeAnimations();
    container.remove();
  });

  async function render(target: AccessTokenSummary | null) {
    await act(async () => {
      reactRoot.render(
        <TokenMetadataDialog onClose={onClose} token={target} />,
      );
    });
  }

  it("loads nothing while closed", async () => {
    await render(null);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(bridgeMocks.listMetadataAttributes).not.toHaveBeenCalled();
    expect(bridgeMocks.getAccessTokenMetadata).not.toHaveBeenCalled();
  });

  it("saves the values that are set and refuses ones the pattern rejects", async () => {
    bridgeMocks.saveAccessTokenMetadata.mockResolvedValue({
      project: "astrlink",
    });
    await render(token);
    expect(bridgeMocks.getAccessTokenMetadata).toHaveBeenCalledWith("token_01");
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "默认元信息 · VS Code",
    );
    expect(
      document.querySelector<HTMLInputElement>("#token-metadata-env")?.value,
    ).toBe("dev");
    await setInput("#token-metadata-project", "AstrLink");
    await act(async () => button("保存").click());
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "「项目」的值不符合校验规则。",
    );
    expect(bridgeMocks.saveAccessTokenMetadata).not.toHaveBeenCalled();
    await setInput("#token-metadata-project", " astrlink ");
    await setInput("#token-metadata-env", "");
    await act(async () => button("保存").click());
    expect(bridgeMocks.saveAccessTokenMetadata).toHaveBeenCalledWith(
      "token_01",
      { project: "astrlink" },
    );
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("points to the settings when there are no attributes", async () => {
    bridgeMocks.listMetadataAttributes.mockResolvedValue([]);
    bridgeMocks.getAccessTokenMetadata.mockResolvedValue({});
    await render(token);
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("还没有元信息属性");
    expect(dialog?.textContent).toContain(
      "请先在「设置 → 元信息」里新增属性。",
    );
    expect(button("保存").disabled).toBe(true);
  });
});
