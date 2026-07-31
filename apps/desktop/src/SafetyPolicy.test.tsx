// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  cancelPrivacyModelInstallation: vi.fn(),
  deletePrivacyModelInstallation: vi.fn(),
  dryRunPrivacyPolicy: vi.fn(),
  getPrivacyModelCatalog: vi.fn(),
  getPrivacyModelInstallation: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  installPrivacyModel: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  probePrivacyModel: vi.fn(),
  updatePrivacyPolicy: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { SafetyPolicy } from "./SafetyPolicy";
import type {
  PrivacyCatalogModel,
  PrivacyModelInstallation,
  PrivacyModelProbe,
  PrivacyPolicyRecord,
} from "./privacy-policy-model";

const etag = `"sha256:${"a".repeat(64)}"`;
const revision = "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088";
const catalogInstallationID = "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const customInstallationID = "model_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
const catalogModel: PrivacyCatalogModel = {
  id: "catalog_sheltron_ettin_32m",
  name: "Ettin Privacy 32M",
  summary: "轻量英文隐私实体检测模型。",
  source: "community",
  repo_id: "sheltron-ai/privacy-filter-ettin-32m",
  revision,
  license: "apache-2.0",
  languages: ["en"],
  adapter: "hf_token_classification",
  variants: [
    {
      id: "cpu_int8",
      name: "CPU INT8",
      quantization: "int8",
      bytes_total: 180_000_000,
      estimated_ram_bytes: 420_000_000,
      recommended: true,
      supported: true,
      unsupported_reason: null,
    },
  ],
};

function policyRecord(
  overrides: Partial<PrivacyPolicyRecord["policy"]> = {},
): PrivacyPolicyRecord {
  return {
    policy: {
      id: "policy_privacy_default",
      name: "隐私保护",
      enabled: false,
      priority: 0,
      detector: "regex",
      local_model_id: null,
      min_confidence: 0.6,
      request_action: "redact",
      response_action: "allow",
      response_restore: true,
      match: {},
      ...overrides,
    },
    etag,
  };
}

function installation(
  overrides: Partial<PrivacyModelInstallation> = {},
): PrivacyModelInstallation {
  const variant = catalogModel.variants[0];
  return {
    id: catalogInstallationID,
    source: "catalog",
    catalog_id: catalogModel.id,
    catalog_source: catalogModel.source,
    name: catalogModel.name,
    license: catalogModel.license,
    languages: catalogModel.languages,
    repo_id: catalogModel.repo_id,
    revision,
    variant_id: variant.id,
    variant_name: variant.name,
    quantization: variant.quantization,
    adapter: catalogModel.adapter,
    status: "downloading",
    bytes_downloaded: 45_000_000,
    bytes_total: variant.bytes_total,
    estimated_ram_bytes: variant.estimated_ram_bytes,
    error: null,
    label_mapping: {},
    installed_at: null,
    ...overrides,
  };
}

function readyInstallation(): PrivacyModelInstallation {
  const variant = catalogModel.variants[0];
  return installation({
    status: "ready",
    bytes_downloaded: variant.bytes_total,
    error: null,
    installed_at: "2026-07-24T10:30:00Z",
  });
}

function probe(
  overrides: Partial<PrivacyModelProbe> = {},
): PrivacyModelProbe {
  return {
    repo_id: "example/privacy-filter",
    requested_revision: "main",
    revision,
    name: "Custom Privacy Filter",
    license: "apache-2.0",
    languages: ["en", "zh"],
    adapter: "hf_token_classification",
    variants: catalogModel.variants,
    labels: [
      { label: "EMAIL", suggested_kind: "email" },
      { label: "PERSON", suggested_kind: "private_person" },
      { label: "MISC", suggested_kind: null },
    ],
    requires_label_mapping: true,
    ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, reject, resolve };
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>(selector);
  if (input === null) throw new Error(`Missing input: ${selector}`);
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (setter === undefined) throw new Error("Missing input value setter");
  await act(async () => {
    setter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function setSelect(selector: string, value: string): Promise<void> {
  const select = document.querySelector<HTMLSelectElement>(selector);
  if (select === null) throw new Error(`Missing select: ${selector}`);
  await act(async () => {
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("SafetyPolicy", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    window.confirm = vi.fn(() => true);
    bridgeMocks.getPrivacyPolicy.mockResolvedValue(policyRecord());
    bridgeMocks.getPrivacyModelCatalog.mockResolvedValue({
      items: [catalogModel],
    });
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValue({ items: [] });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    vi.useRealTimers();
    container.remove();
  });

  async function renderPolicy(session = "session-1"): Promise<void> {
    await act(async () => {
      root.render(<SafetyPolicy coreSessionKey={session} isReady />);
      await Promise.resolve();
    });
    await flush();
  }

  it("uses backend values and rolls an optimistic ETag patch back on failure", async () => {
    const pending = deferred<PrivacyPolicyRecord>();
    bridgeMocks.updatePrivacyPolicy.mockReturnValueOnce(pending.promise);
    await renderPolicy();

    const enabled = container.querySelector<HTMLInputElement>(
      'input[aria-label="启用隐私保护"]',
    );
    expect(enabled?.checked).toBe(false);

    await act(async () => {
      enabled?.click();
      await Promise.resolve();
    });
    expect(enabled?.checked).toBe(true);
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      enabled: true,
    });

    await act(async () => {
      pending.reject(new Error("ETag mismatch"));
      await Promise.resolve();
    });
    expect(enabled?.checked).toBe(false);
    expect(container.textContent).toContain("ETag mismatch");
  });

  it("keeps rendering when switching a catalog quantization variant", async () => {
    const openAIModel: PrivacyCatalogModel = {
      ...catalogModel,
      id: "catalog_openai_privacy_filter",
      name: "OpenAI Privacy Filter",
      source: "official",
      repo_id: "openai/privacy-filter",
      variants: [
        {
          ...catalogModel.variants[0],
          id: "cpu_q4",
          name: "CPU Q4",
          quantization: "q4",
          bytes_total: 512 * 1024 ** 2,
          estimated_ram_bytes: 2 * 1024 ** 3,
          recommended: true,
        },
        {
          ...catalogModel.variants[0],
          id: "cpu_int8",
          name: "CPU INT8",
          quantization: "int8",
          bytes_total: 1024 ** 3,
          estimated_ram_bytes: 3 * 1024 ** 3,
          recommended: false,
        },
      ],
    };
    bridgeMocks.getPrivacyModelCatalog.mockResolvedValueOnce({
      items: [openAIModel],
    });
    await renderPolicy();

    const selector = container.querySelector<HTMLSelectElement>(
      '[aria-label="OpenAI Privacy Filter 模型版本"]',
    );
    expect(selector?.value).toBe("cpu_q4");

    await setSelect(
      '[aria-label="OpenAI Privacy Filter 模型版本"]',
      "cpu_int8",
    );

    expect(selector?.value).toBe("cpu_int8");
    expect(container.textContent).toContain("OpenAI Privacy Filter");
    expect(container.textContent).toContain("下载 1.0 GB");
  });

  it("installs a catalog variant and polls its per-installation progress", async () => {
    vi.useFakeTimers();
    bridgeMocks.probePrivacyModel.mockResolvedValueOnce(
      probe({
        repo_id: catalogModel.repo_id,
        requested_revision: revision,
        name: catalogModel.name,
        license: catalogModel.license,
        languages: catalogModel.languages,
        labels: [
          { label: "EMAIL", suggested_kind: "email" },
          { label: "MISC", suggested_kind: null },
        ],
        requires_label_mapping: true,
      }),
    );
    bridgeMocks.installPrivacyModel.mockResolvedValueOnce(installation());
    bridgeMocks.getPrivacyModelInstallation.mockResolvedValueOnce(
      readyInstallation(),
    );
    await renderPolicy();

    await act(async () => {
      button("检查并安装").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probePrivacyModel).toHaveBeenCalledWith({
      repo_id: catalogModel.repo_id,
      revision,
    });
    expect(
      container.querySelector('[role="dialog"]')?.textContent,
    ).toContain("标签映射");
    expect(button("确认安装").disabled).toBe(true);
    await setSelect('[aria-label="MISC 标签映射"]', "");
    expect(button("确认安装").disabled).toBe(false);

    await act(async () => {
      button("确认安装").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.installPrivacyModel).toHaveBeenCalledWith({
      repo_id: catalogModel.repo_id,
      revision,
      variant_id: "cpu_int8",
      label_mapping: { EMAIL: "email", MISC: null },
    });
    expect(container.textContent).toContain("25%");
    expect(button("取消")).toBeTruthy();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
      await Promise.resolve();
    });
    expect(bridgeMocks.getPrivacyModelInstallation).toHaveBeenCalledWith(
      catalogInstallationID,
    );
    expect(container.textContent).toContain("已就绪");
    expect(container.textContent).toContain("社区目录");
    expect(container.textContent).toContain(catalogModel.license);
    expect(button("用于策略")).toBeTruthy();
  });

  it("does not restore a cancelled installation from an older poll", async () => {
    vi.useFakeTimers();
    const stalePoll = deferred<PrivacyModelInstallation>();
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [installation()],
    });
    bridgeMocks.getPrivacyModelInstallation.mockReturnValueOnce(
      stalePoll.promise,
    );
    bridgeMocks.cancelPrivacyModelInstallation.mockResolvedValueOnce(
      undefined,
    );
    await renderPolicy();

    await act(async () => button("已安装 1").click());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
      await Promise.resolve();
    });
    expect(bridgeMocks.getPrivacyModelInstallation).toHaveBeenCalledWith(
      catalogInstallationID,
    );

    await act(async () => {
      button("取消").click();
      await Promise.resolve();
    });
    expect(
      container.querySelector('[role="dialog"]')?.textContent,
    ).toContain("取消模型下载");
    expect(
      bridgeMocks.cancelPrivacyModelInstallation,
    ).not.toHaveBeenCalled();
    await act(async () => {
      button("确认取消下载").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.cancelPrivacyModelInstallation).toHaveBeenCalledWith(
      catalogInstallationID,
    );
    expect(container.textContent).toContain("尚未安装本地模型");

    await act(async () => {
      stalePoll.resolve(
        installation({
          bytes_downloaded: 90_000_000,
        }),
      );
      await Promise.resolve();
    });
    expect(container.textContent).toContain("尚未安装本地模型");
    expect(
      container.querySelector(
        `[aria-label="${catalogModel.name} 下载进度"]`,
      ),
    ).toBeNull();
  });

  it("continues polling after a transient progress request failure", async () => {
    vi.useFakeTimers();
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [installation()],
    });
    bridgeMocks.getPrivacyModelInstallation
      .mockRejectedValueOnce(new Error("temporary unavailable"))
      .mockResolvedValueOnce(readyInstallation());
    await renderPolicy();
    await act(async () => button("已安装 1").click());

    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
      await Promise.resolve();
    });
    expect(container.textContent).toContain("temporary unavailable");
    expect(bridgeMocks.getPrivacyModelInstallation).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
      await Promise.resolve();
    });
    expect(bridgeMocks.getPrivacyModelInstallation).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain("已就绪");
  });

  it("probes a custom model, exposes base-label mapping, and confirms heavy resources", async () => {
    const heavyProbe = probe({
      variants: [
        {
          ...catalogModel.variants[0],
          id: "cpu_fp32",
          name: "CPU FP32",
          bytes_total: 2 * 1024 ** 3,
          estimated_ram_bytes: 4 * 1024 ** 3,
        },
      ],
    });
    bridgeMocks.probePrivacyModel.mockResolvedValueOnce(heavyProbe);
    bridgeMocks.installPrivacyModel.mockResolvedValueOnce(
      installation({
        id: customInstallationID,
        source: "custom",
        catalog_id: null,
        catalog_source: null,
        name: heavyProbe.name,
        license: heavyProbe.license,
        languages: heavyProbe.languages,
        repo_id: heavyProbe.repo_id,
        variant_id: "cpu_fp32",
        variant_name: "CPU FP32",
        quantization: "int8",
        bytes_downloaded: 0,
        bytes_total: 2 * 1024 ** 3,
        estimated_ram_bytes: 4 * 1024 ** 3,
        label_mapping: {
          EMAIL: "email",
          PERSON: "private_person",
          MISC: null,
        },
      }),
    );
    await renderPolicy();

    await act(async () => button("自定义").click());
    await setInput('[aria-label="Hugging Face 仓库"]', heavyProbe.repo_id);
    await act(async () => {
      button("检查兼容性").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probePrivacyModel).toHaveBeenCalledWith({
      repo_id: heavyProbe.repo_id,
      revision: "main",
    });
    expect(container.textContent).toContain("标签映射");
    expect(
      container.querySelector<HTMLSelectElement>(
        '[aria-label="PERSON 标签映射"]',
      )?.value,
    ).toBe("private_person");
    expect(
      container.querySelector<HTMLSelectElement>(
        '[aria-label="MISC 标签映射"]',
      )?.value,
    ).toBe("__unresolved__");
    expect(button("安装自定义模型").disabled).toBe(true);

    await setSelect('[aria-label="MISC 标签映射"]', "");
    await act(async () => button("应用映射").click());
    expect(container.querySelector('[role="dialog"]')).toBeNull();
    expect(button("安装自定义模型").disabled).toBe(false);

    await act(async () => {
      button("安装自定义模型").click();
      await Promise.resolve();
    });
    expect(container.textContent).toContain("性能较低的设备");
    expect(bridgeMocks.installPrivacyModel).not.toHaveBeenCalled();
    expect(window.confirm).not.toHaveBeenCalled();
    await act(async () => {
      button("继续安装").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.installPrivacyModel).toHaveBeenCalledWith({
      repo_id: heavyProbe.repo_id,
      revision,
      variant_id: "cpu_fp32",
      label_mapping: {
        EMAIL: "email",
        PERSON: "private_person",
        MISC: null,
      },
    });
    expect(container.textContent).toContain("自定义公开仓库");
    expect(container.textContent).toContain(heavyProbe.license);
  });

  it("locks the custom repository identity while a probe is in flight", async () => {
    const pending = deferred<PrivacyModelProbe>();
    bridgeMocks.probePrivacyModel.mockReturnValueOnce(pending.promise);
    await renderPolicy();

    await act(async () => button("自定义").click());
    await setInput(
      '[aria-label="Hugging Face 仓库"]',
      "example/privacy-filter",
    );
    await act(async () => {
      button("检查兼容性").click();
      await Promise.resolve();
    });

    expect(
      container.querySelector<HTMLInputElement>(
        '[aria-label="Hugging Face 仓库"]',
      )?.disabled,
    ).toBe(true);
    expect(
      container.querySelector<HTMLInputElement>(
        '[aria-label="模型 Revision"]',
      )?.disabled,
    ).toBe(true);

    await act(async () => {
      pending.resolve(probe());
      await Promise.resolve();
    });
    expect(
      container.querySelector<HTMLInputElement>(
        '[aria-label="Hugging Face 仓库"]',
      )?.disabled,
    ).toBe(false);
  });

  it("keeps a selected model undeletable and ignores old Core-session results", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({
        detector: "local_model",
        local_model_id: catalogInstallationID,
      }),
    );
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [readyInstallation()],
    });
    await renderPolicy();
    await act(async () => button("已安装 1").click());
    expect(button("当前模型").disabled).toBe(true);
    expect(button("删除").disabled).toBe(true);

    const oldPolicy = deferred<PrivacyPolicyRecord>();
    bridgeMocks.getPrivacyPolicy
      .mockReturnValueOnce(oldPolicy.promise)
      .mockResolvedValueOnce(policyRecord({ request_action: "block" }));
    bridgeMocks.getPrivacyModelCatalog
      .mockResolvedValueOnce({ items: [catalogModel] })
      .mockResolvedValueOnce({ items: [catalogModel] });
    bridgeMocks.listPrivacyModelInstallations
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [] });

    await act(async () => {
      root.render(<SafetyPolicy coreSessionKey="session-old" isReady />);
      await Promise.resolve();
    });
    await renderPolicy("session-new");
    expect(
      container.querySelector<HTMLSelectElement>("#privacy-request-action")
        ?.value,
    ).toBe("block");

    await act(async () => {
      oldPolicy.resolve(policyRecord({ request_action: "warn" }));
      await Promise.resolve();
    });
    expect(
      container.querySelector<HTMLSelectElement>("#privacy-request-action")
        ?.value,
    ).toBe("block");
  });

  it("confirms resource use before activating a local model", async () => {
    const ready = readyInstallation();
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: true }),
    );
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [ready],
    });
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(
      policyRecord({
        enabled: true,
        detector: "local_model",
        local_model_id: ready.id,
      }),
    );
    await renderPolicy();

    await act(async () => button("已安装 1").click());
    await act(async () => {
      button("用于策略").click();
      await Promise.resolve();
    });

    const dialog = container.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("确认使用本地模型");
    expect(dialog?.textContent).toContain("预计内存");
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();

    await act(async () => {
      button("确认用于策略").click();
      await Promise.resolve();
    });
    expect(container.querySelector('[role="dialog"]')).toBeNull();
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      detector: "local_model",
      local_model_id: ready.id,
    });
    expect(container.textContent).toContain("安全策略已保存");
  });

  it("shows an in-app confirmation and feedback when deleting a model", async () => {
    const ready = readyInstallation();
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [ready],
    });
    bridgeMocks.deletePrivacyModelInstallation.mockResolvedValueOnce(
      undefined,
    );
    await renderPolicy();

    await act(async () => button("已安装 1").click());
    await act(async () => {
      button("删除").click();
      await Promise.resolve();
    });

    expect(
      container.querySelector('[role="dialog"]')?.textContent,
    ).toContain("删除本地模型");
    expect(
      bridgeMocks.deletePrivacyModelInstallation,
    ).not.toHaveBeenCalled();

    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deletePrivacyModelInstallation).toHaveBeenCalledWith(
      ready.id,
    );
    expect(container.textContent).toContain("本地模型已删除");
    expect(container.textContent).toContain("尚未安装本地模型");
  });

  it("keeps an enabled policy unchanged when model activation is cancelled", async () => {
    const ready = readyInstallation();
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: true }),
    );
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [ready],
    });
    await renderPolicy();

    await act(async () => button("已安装 1").click());
    await act(async () => button("用于策略").click());
    await act(async () => button("返回").click());

    expect(container.querySelector('[role="dialog"]')).toBeNull();
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
    expect(
      container.querySelector<HTMLInputElement>(
        'input[name="privacy-detector"]:checked',
      )?.closest("label")?.textContent,
    ).toContain("Regex");
  });

  it("patches response restore independently of request action", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ response_restore: false }),
    );
    await renderPolicy();

    const toggle = container.querySelector<HTMLInputElement>(
      'input[aria-label="响应还原占位符"]',
    );
    expect(toggle?.checked).toBe(true);

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      response_restore: false,
    });
    expect(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="响应还原占位符"]',
      )?.checked,
    ).toBe(false);
  });

  it("opens a local streaming restore demo without mutating policy", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({
        request_action: "block",
        response_restore: false,
      }),
    );
    await renderPolicy();
    await flush();

    const bridgeCallsBefore = Object.fromEntries(
      Object.entries(bridgeMocks).map(([name, mock]) => [
        name,
        mock.mock.calls.length,
      ]),
    );

    await act(async () => {
      button("查看流式演示").click();
      await Promise.resolve();
    });

    const dialog = container.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog?.getAttribute("aria-modal")).toBe("true");
    expect(
      dialog?.querySelector("#streaming-restore-demo-title")?.textContent,
    ).toBe("流式响应还原演示");
    expect(dialog?.textContent).toContain("请求侧脱敏");
    expect(dialog?.textContent).toContain("占位符还原");
    expect(dialog?.textContent).toContain("不对响应正文或 SSE");
    expect(dialog?.textContent).toContain("固定示例");
    expect(dialog?.textContent).toContain("alice@example.com");
    expect(dialog?.textContent).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(dialog?.textContent).toContain("data: <PRIVATE_EMAIL_7f3a");
    expect(dialog?.textContent).toContain("91c04d28be56>");
    expect(dialog?.textContent).toContain("data: alice@example.com");
    expect(dialog?.textContent).toContain("客户端");
    expect(dialog?.textContent).toContain("AstrLink");
    expect(dialog?.textContent).toContain("上游");
    expect(dialog?.textContent).not.toContain("响应审核扫描");
    expect(dialog?.textContent).not.toContain("SSE 审核");

    const canvasBefore = dialog?.querySelector(
      ".streaming-restore-demo__canvas",
    );
    const packetsBefore = [
      ...container.querySelectorAll(".streaming-restore-demo__packet"),
    ];
    expect(canvasBefore).not.toBeNull();
    expect(packetsBefore).toHaveLength(5);
    expect(
      container.querySelector(".streaming-restore-demo__lane--request"),
    ).not.toBeNull();
    expect(
      container.querySelector(".streaming-restore-demo__lane--response"),
    ).not.toBeNull();
    expect(
      container.querySelector(".streaming-restore-demo__packet--plain")
        ?.textContent,
    ).toContain("alice@example.com");
    expect(
      container.querySelector(".streaming-restore-demo__packet--redacted")
        ?.textContent,
    ).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(
      container.querySelector(".streaming-restore-demo__packet--chunk-a")
        ?.textContent,
    ).toContain("data: <PRIVATE_EMAIL_7f3a");
    expect(
      container.querySelector(".streaming-restore-demo__packet--chunk-b")
        ?.textContent,
    ).toContain("91c04d28be56>");
    expect(
      container.querySelector(".streaming-restore-demo__packet--restored")
        ?.textContent,
    ).toContain("data: alice@example.com");

    await act(async () => {
      button("重新播放").click();
      await Promise.resolve();
    });
    const canvasAfter = container.querySelector(
      ".streaming-restore-demo__canvas",
    );
    const packetsAfter = [
      ...container.querySelectorAll(".streaming-restore-demo__packet"),
    ];
    expect(canvasAfter).not.toBeNull();
    expect(canvasAfter).not.toBe(canvasBefore);
    expect(packetsAfter).toHaveLength(5);
    expect(packetsAfter[0]).not.toBe(packetsBefore[0]);

    for (const [name, mock] of Object.entries(bridgeMocks)) {
      expect(mock.mock.calls.length, name).toBe(bridgeCallsBefore[name]);
    }
    expect(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="响应还原占位符"]',
      )?.checked,
    ).toBe(false);
    expect(
      container.querySelector<HTMLSelectElement>("#privacy-request-action")
        ?.value,
    ).toBe("block");

    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
      await Promise.resolve();
    });
    expect(container.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => {
      button("查看流式演示").click();
      await Promise.resolve();
    });
    expect(container.querySelector('[role="dialog"]')).not.toBeNull();
    await act(async () => {
      button("关闭").click();
      await Promise.resolve();
    });
    expect(container.querySelector('[role="dialog"]')).toBeNull();

    for (const [name, mock] of Object.entries(bridgeMocks)) {
      expect(mock.mock.calls.length, name).toBe(bridgeCallsBefore[name]);
    }
  });

  it("patches the model confidence threshold", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ min_confidence: 0.87 }),
    );
    await renderPolicy();

    const threshold = container.querySelector<HTMLInputElement>(
      'input[aria-label="模型最低置信度"]',
    );
    expect(threshold?.min).toBe("0");
    expect(threshold?.max).toBe("1");
    expect(threshold?.step).toBe("0.01");

    threshold?.focus();
    await setInput('input[aria-label="模型最低置信度"]', "0.87");
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
    await act(async () => threshold?.blur());
    await flush();

    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      min_confidence: 0.87,
    });
    expect(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="模型最低置信度"]',
      )?.value,
    ).toBe("0.87");
    expect(container.textContent).toContain("Regex 不受此门槛影响");
  });

  it("runs a policy dry-run and shows the decision summary", async () => {
    const ready = readyInstallation();
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({
        enabled: true,
        detector: "local_model",
        local_model_id: ready.id,
      }),
    );
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [ready],
    });
    bridgeMocks.dryRunPrivacyPolicy.mockResolvedValueOnce({
      decision: "redact",
      findings_summary: "email=1",
      findings: [
        {
          kind: "email",
          path: "/messages/0/content",
          start: 6,
          end: 23,
          confidence: 0.91,
        },
      ],
      suppressed_findings: [
        {
          kind: "private_person",
          path: "/messages/0/content",
          start: 0,
          end: 6,
          confidence: 0.596717,
        },
      ],
      redactions: [
        {
          placeholder: "<PRIVATE_EMAIL_7f3a91c04d28be56>",
          kind: "email",
          value: "alice@example.com",
        },
      ],
      redacted_body:
        '{"messages":[{"content":"email <PRIVATE_EMAIL_7f3a91c04d28be56>","role":"user"}]}',
      inspected_body:
        '{"messages":[{"content":"email alice@example.com","role":"user"}]}',
    });
    await renderPolicy();

    await act(async () => {
      button("试运行").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenCalledWith({
      protocol: "openai.chat",
      sample_text: expect.stringContaining("alice@example.com"),
      policy: {
        enabled: true,
        detector: "local_model",
        local_model_id: ready.id,
        min_confidence: 0.6,
        request_action: "redact",
      },
    });
    expect(container.textContent).toContain("脱敏后继续");
    expect(container.textContent).toContain("邮箱 × 1");
    expect(container.textContent).toContain("0.910000 ≥ 0.60");
    expect(container.textContent).toContain("低于门槛（已抑制，不执行策略）");
    expect(container.textContent).toContain("0.596717 < 0.60");
    expect(container.textContent).toContain("占位符对照（仅本地预览）");
    expect(container.textContent).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(container.textContent).toContain("alice@example.com");
    expect(container.textContent).toContain("脱敏后的请求体");
  });

  it("prompts that privacy protection is disabled instead of showing no findings", async () => {
    await renderPolicy();

    expect(container.textContent).toContain(
      "隐私保护未开启，请先开启后再试运行",
    );

    await act(async () => {
      button("试运行").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.dryRunPrivacyPolicy).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      "隐私保护未开启，请先开启后再试运行。",
    );
    expect(container.querySelector(".safety-dry-run__result")).toBeNull();
  });
});
