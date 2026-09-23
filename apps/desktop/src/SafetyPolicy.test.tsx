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
  getPrivacyRegexBuiltinRules: vi.fn(),
  installPrivacyModel: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  pausePrivacyModelInstallation: vi.fn(),
  resumePrivacyModelInstallation: vi.fn(),
  probeLocalPrivacyModel: vi.fn(),
  probePrivacyModel: vi.fn(),
  updatePrivacyPolicy: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { SafetyPolicy } from "./SafetyPolicy";
import { defaultPrivacyKindRules } from "./privacy-policy-model";
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
      regex_source: "builtin",
      custom_regex_rules: [],
      request_action: "redact",
      response_action: "allow",
      response_restore: true,
      kind_rules: defaultPrivacyKindRules(),
      allowlist_rules: [{ type: "domain_suffix", value: "github.com" }],
      restore_tool_arguments: true,
      placeholder_notice: true,
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

function probe(overrides: Partial<PrivacyModelProbe> = {}): PrivacyModelProbe {
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

function actionButton(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) =>
      candidate.textContent?.trim() === label &&
      candidate.getAttribute("role") !== "tab",
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing action button: ${label}`);
  }
  return match;
}

async function openModels(): Promise<void> {
  await act(async () => {
    button("模型").click();
    await Promise.resolve();
  });
}

async function openPolicySection(label: string): Promise<void> {
  const tab = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
  ].find((candidate) => candidate.textContent?.trim().startsWith(label));
  if (!tab) throw new Error(`Missing policy section: ${label}`);
  await act(async () => {
    tab.dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
    await Promise.resolve();
  });
}

async function openDryRun(): Promise<void> {
  await act(async () => {
    document
      .querySelector<HTMLButtonElement>('[role="tab"][aria-label="试运行"]')
      ?.click();
    await Promise.resolve();
  });
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

async function chooseOption(selector: string, option: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(selector);
  if (trigger === null) throw new Error(`Missing select trigger: ${selector}`);
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
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
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

    const enabled = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="启用隐私保护"]',
    );
    expect(enabled?.getAttribute("aria-checked")).toBe("false");

    await act(async () => {
      enabled?.click();
      await Promise.resolve();
    });
    expect(enabled?.getAttribute("aria-checked")).toBe("true");
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      enabled: true,
    });

    await act(async () => {
      pending.reject(new Error("ETag mismatch"));
      await Promise.resolve();
    });
    expect(enabled?.getAttribute("aria-checked")).toBe("false");
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
    await openModels();

    const selector = container.querySelector<HTMLButtonElement>(
      '[aria-label="OpenAI Privacy Filter 模型版本"]',
    );
    expect(selector?.textContent).toContain("CPU Q4");

    await chooseOption(
      '[aria-label="OpenAI Privacy Filter 模型版本"]',
      "CPU INT8",
    );

    expect(selector?.textContent).toContain("CPU INT8");
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
    await openModels();

    await act(async () => {
      button("检查并安装").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probePrivacyModel).toHaveBeenCalledWith({
      repo_id: catalogModel.repo_id,
      revision,
    });
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "标签映射",
    );
    expect(button("确认安装").disabled).toBe(true);
    await chooseOption('[aria-label="MISC 标签映射"]', "忽略此标签");
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

  it("pauses without losing progress, ignores a stale poll, and resumes polling", async () => {
    vi.useFakeTimers();
    const stalePoll = deferred<PrivacyModelInstallation>();
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [installation()],
    });
    bridgeMocks.getPrivacyModelInstallation.mockReturnValueOnce(
      stalePoll.promise,
    );
    bridgeMocks.pausePrivacyModelInstallation.mockResolvedValueOnce(
      installation({ status: "paused" }),
    );
    bridgeMocks.resumePrivacyModelInstallation.mockResolvedValueOnce(
      installation(),
    );
    await renderPolicy();
    await openModels();
    await act(async () => button("已安装 1").click());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
    });
    await act(async () => button("暂停下载").click());
    expect(bridgeMocks.pausePrivacyModelInstallation).toHaveBeenCalledWith(
      catalogInstallationID,
    );
    expect(container.textContent).toContain("已暂停");
    expect(
      container
        .querySelector('[role="progressbar"]')
        ?.getAttribute("aria-valuenow"),
    ).toBe("25");
    await act(async () =>
      stalePoll.resolve(installation({ bytes_downloaded: 90_000_000 })),
    );
    expect(button("继续下载")).toBeTruthy();
    expect(
      container
        .querySelector('[role="progressbar"]')
        ?.getAttribute("aria-valuenow"),
    ).toBe("25");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1800);
    });
    expect(bridgeMocks.getPrivacyModelInstallation).toHaveBeenCalledTimes(1);
    bridgeMocks.getPrivacyModelInstallation.mockResolvedValueOnce(
      readyInstallation(),
    );
    await act(async () => button("继续下载").click());
    expect(bridgeMocks.resumePrivacyModelInstallation).toHaveBeenCalledWith(
      catalogInstallationID,
    );
    expect(button("暂停下载")).toBeTruthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(900);
    });
    expect(container.textContent).toContain("已就绪");
    expect(bridgeMocks.cancelPrivacyModelInstallation).not.toHaveBeenCalled();
  });

  it("keeps a paused installation and reports a failed resume", async () => {
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [installation({ status: "paused" })],
    });
    bridgeMocks.resumePrivacyModelInstallation.mockRejectedValueOnce(
      new Error("网络暂不可用"),
    );
    await renderPolicy();
    await openModels();
    await act(async () => button("已安装 1").click());
    await act(async () => button("继续下载").click());
    expect(container.textContent).toContain("网络暂不可用");
    expect(container.textContent).toContain("已暂停");
    expect(button("继续下载").disabled).toBe(false);
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
    bridgeMocks.cancelPrivacyModelInstallation.mockResolvedValueOnce(undefined);
    await renderPolicy();
    await openModels();

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
      document.querySelector('[role="alertdialog"]')?.textContent,
    ).toContain("取消模型下载");
    expect(bridgeMocks.cancelPrivacyModelInstallation).not.toHaveBeenCalled();
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
      container.querySelector(`[aria-label="${catalogModel.name} 下载进度"]`),
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
    await openModels();
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
    await openModels();

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
    expect(document.body.textContent).toContain("标签映射");
    expect(
      document.querySelector<HTMLButtonElement>(
        '[aria-label="PERSON 标签映射"]',
      )?.textContent,
    ).toContain("人名");
    expect(
      document.querySelector<HTMLButtonElement>('[aria-label="MISC 标签映射"]')
        ?.textContent,
    ).toContain("请选择");
    expect(button("安装自定义模型").disabled).toBe(true);

    await chooseOption('[aria-label="MISC 标签映射"]', "忽略此标签");
    await act(async () => button("应用映射").click());
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(button("安装自定义模型").disabled).toBe(false);

    await act(async () => {
      button("安装自定义模型").click();
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("性能较低的设备");
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

  it("probes an already-mounted ONNX file and reuses the install flow", async () => {
    const localProbe = probe({
      repo_id: "local/model-aaaaaaaaaaaa",
      requested_revision: revision,
      name: "Astr PII Ettin 32M",
    });
    const localInstallation = installation({
      id: customInstallationID,
      source: "local",
      catalog_id: null,
      catalog_source: null,
      name: localProbe.name,
      license: localProbe.license,
      languages: localProbe.languages,
      repo_id: localProbe.repo_id,
      revision: localProbe.revision,
      label_mapping: {
        EMAIL: "email",
        PERSON: "private_person",
        MISC: null,
      },
    });
    bridgeMocks.probeLocalPrivacyModel.mockResolvedValueOnce(localProbe);
    bridgeMocks.installPrivacyModel.mockResolvedValueOnce(localInstallation);
    await renderPolicy();
    await openModels();

    await act(async () => button("本地导入").click());
    expect(container.textContent).toContain("请先在系统中挂载网络共享");
    expect(container.textContent).toContain("smb://");

    await setInput(
      '[aria-label="本地模型路径"]',
      "smb://host/share/model.onnx",
    );
    await act(async () => {
      button("检查本地模型").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probeLocalPrivacyModel).not.toHaveBeenCalled();
    expect(container.textContent).toContain("本地导入不接收 URI");

    await setInput(
      '[aria-label="本地模型路径"]',
      "  /Volumes/models/astr-pii-ettin/model_int8.onnx  ",
    );
    await act(async () => {
      button("检查本地模型").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probeLocalPrivacyModel).toHaveBeenCalledWith({
      path: "/Volumes/models/astr-pii-ettin/model_int8.onnx",
    });
    expect(container.textContent).toContain(localProbe.name);
    expect(container.textContent).toContain("已检查此路径");
    expect(container.textContent).not.toContain(
      "/Volumes/models/astr-pii-ettin/model_int8.onnx ·",
    );
    expect(
      document.querySelector<HTMLButtonElement>('[aria-label="MISC 标签映射"]')
        ?.textContent,
    ).toContain("请选择");

    await chooseOption('[aria-label="MISC 标签映射"]', "忽略此标签");
    await act(async () => button("应用映射").click());
    await act(async () => {
      button("导入本地模型").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.installPrivacyModel).toHaveBeenCalledWith({
      repo_id: localProbe.repo_id,
      revision: localProbe.revision,
      variant_id: catalogModel.variants[0].id,
      label_mapping: {
        EMAIL: "email",
        PERSON: "private_person",
        MISC: null,
      },
    });
    expect(container.textContent).toContain("本地导入");
    expect(container.textContent).toContain("导入中");
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("does not offer a Hugging Face retry for failed local imports", async () => {
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [
        installation({
          source: "local",
          catalog_id: null,
          catalog_source: null,
          status: "error",
          error: "integrity_failed",
          bytes_downloaded: 0,
          installed_at: null,
        }),
      ],
    });
    await renderPolicy();
    await openModels();

    await act(async () => button("已安装 1").click());
    expect(container.textContent).toContain("本地导入");
    expect(container.textContent).toContain("完整性校验失败");
    expect(
      [...container.querySelectorAll("button")].some(
        (candidate) => candidate.textContent?.trim() === "重试",
      ),
    ).toBe(false);
  });

  it("locks the custom repository identity while a probe is in flight", async () => {
    const pending = deferred<PrivacyModelProbe>();
    bridgeMocks.probePrivacyModel.mockReturnValueOnce(pending.promise);
    await renderPolicy();
    await openModels();

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
      container.querySelector<HTMLInputElement>('[aria-label="模型 Revision"]')
        ?.disabled,
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
    await openModels();
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
      container.querySelector<HTMLButtonElement>("#privacy-request-action")
        ?.textContent,
    ).toContain("阻止请求");

    await act(async () => {
      oldPolicy.resolve(policyRecord({ request_action: "warn" }));
      await Promise.resolve();
    });
    expect(
      container.querySelector<HTMLButtonElement>("#privacy-request-action")
        ?.textContent,
    ).toContain("阻止请求");
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
    await openModels();

    await act(async () => button("已安装 1").click());
    await act(async () => {
      button("用于策略").click();
      await Promise.resolve();
    });

    const dialog = document.querySelector('[role="alertdialog"]');
    expect(dialog?.textContent).toContain("确认使用本地模型");
    expect(dialog?.textContent).toContain("预计内存");
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();

    await act(async () => {
      button("确认用于策略").click();
      await Promise.resolve();
    });
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      detector: "local_model",
      local_model_id: ready.id,
    });
    expect(notifyMocks.success).toHaveBeenCalledWith("安全策略已保存。");
  });

  it("shows an in-app confirmation and feedback when deleting a model", async () => {
    const ready = readyInstallation();
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValueOnce({
      items: [ready],
    });
    bridgeMocks.deletePrivacyModelInstallation.mockResolvedValueOnce(undefined);
    await renderPolicy();
    await openModels();

    await act(async () => button("已安装 1").click());
    await act(async () => {
      button("删除").click();
      await Promise.resolve();
    });

    expect(
      document.querySelector('[role="alertdialog"]')?.textContent,
    ).toContain("删除本地模型");
    expect(bridgeMocks.deletePrivacyModelInstallation).not.toHaveBeenCalled();

    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deletePrivacyModelInstallation).toHaveBeenCalledWith(
      ready.id,
    );
    expect(notifyMocks.success).toHaveBeenCalledWith("本地模型已删除。");
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
    await openModels();

    await act(async () => button("已安装 1").click());
    await act(async () => button("用于策略").click());
    await act(async () => button("返回").click());

    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
    expect(
      container
        .querySelector<HTMLButtonElement>('[role="radio"][aria-label="Regex"]')
        ?.getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("patches response restore independently of request action", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ response_restore: false }),
    );
    await renderPolicy();

    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="响应还原占位符"]',
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("true");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      response_restore: false,
    });
    expect(
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="响应还原占位符"]',
        )
        ?.getAttribute("aria-checked"),
    ).toBe("false");
  });

  // The whole kind_rules list is sent because a patch replaces it rather than
  // merging, so a partial list would silently reset the omitted kinds.
  it("patches the whole kind rule list when one kind is toggled", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(policyRecord());
    await renderPolicy();
    await openPolicySection("脱敏规则");

    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="脱敏URL"]',
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("false");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });
    await flush();

    const expected = defaultPrivacyKindRules().map((rule) =>
      rule.kind === "url" ? { ...rule, enabled: true } : rule,
    );
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      kind_rules: expected,
    });
  });

  it("locks the placeholder style of kinds where the shape is a safety property", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    await renderPolicy();
    await openPolicySection("脱敏规则");

    const locked = container.querySelector<HTMLButtonElement>(
      '[aria-label="常见密钥 占位符形态"]',
    );
    expect(locked?.getAttribute("data-disabled")).not.toBeNull();
    expect(locked?.textContent).toContain("标记占位符");
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="了解两种占位符"]')
        ?.click(),
    );
    expect(document.body.textContent).toContain("诱导模型真的拿去调用 API");

    const configurable = container.querySelector<HTMLButtonElement>(
      '[aria-label="邮箱 占位符形态"]',
    );
    expect(configurable?.getAttribute("data-disabled")).toBeNull();
    expect(configurable?.textContent).toContain("保留域假值");
  });

  it("appends an allowlist rule only once it has a value", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(policyRecord());
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(policyRecord());
    await renderPolicy();
    await openPolicySection("脱敏规则");

    await act(async () => {
      button("添加名单").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();

    await setInput('input[aria-label="名单 2 值"]', "internal.example");
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
    await act(async () => {
      container
        .querySelector<HTMLInputElement>('input[aria-label="名单 2 值"]')
        ?.blur();
    });
    await flush();

    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      allowlist_rules: [
        { type: "domain_suffix", value: "github.com" },
        { type: "domain_suffix", value: "internal.example" },
      ],
    });
  });

  it("edits and removes the original allowlist entry while search is active", async () => {
    let current = policyRecord({
      allowlist_rules: [
        { type: "domain_suffix", value: "github.com" },
        { type: "cidr", value: "10.0.0.0/8" },
        { type: "domain_suffix", value: "internal.example" },
      ],
    });
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(current);
    bridgeMocks.updatePrivacyPolicy.mockImplementation(async (_etag, patch) => {
      current = { ...current, policy: { ...current.policy, ...patch } };
      return current;
    });
    await renderPolicy();
    await openPolicySection("脱敏规则");
    await setInput('input[type="search"]', "  INTERNAL  ");
    expect(container.querySelector('input[aria-label="名单 1 值"]')).toBeNull();

    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="名单 3 值"]',
    )!;
    await act(async () => input.focus());
    await setInput('input[aria-label="名单 3 值"]', "internal.updated.example");
    await act(async () => input.blur());
    await flush();
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenLastCalledWith(etag, {
      allowlist_rules: [
        { type: "domain_suffix", value: "github.com" },
        { type: "cidr", value: "10.0.0.0/8" },
        { type: "domain_suffix", value: "internal.updated.example" },
      ],
    });

    await act(async () => button("移除").click());
    await flush();
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenLastCalledWith(etag, {
      allowlist_rules: [
        { type: "domain_suffix", value: "github.com" },
        { type: "cidr", value: "10.0.0.0/8" },
      ],
    });
    expect(container.textContent).toContain("没有匹配的名单条目");
  });

  it("clears a search when adding a rule and keeps the pending row across section switches", async () => {
    await renderPolicy();
    await openPolicySection("脱敏规则");
    await setInput('input[type="search"]', "no-match");
    expect(container.textContent).toContain("没有匹配的名单条目");
    await act(async () => button("添加名单").click());
    expect(
      container.querySelector<HTMLInputElement>('input[type="search"]')?.value,
    ).toBe("");
    await chooseOption('[aria-label="名单 2 类型"]', "IP 段");
    await openPolicySection("检测与还原");
    await openPolicySection("脱敏规则");
    expect(
      container.querySelector<HTMLInputElement>('input[aria-label="名单 2 值"]')
        ?.value,
    ).toBe("");
    expect(
      container.querySelector('[aria-label="名单 2 类型"]')?.textContent,
    ).toContain("IP 段");
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
  });

  it("ties tool argument restore to response restore", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ response_restore: false }),
    );
    await renderPolicy();

    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="还原工具调用参数"]',
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("true");
    expect(toggle?.getAttribute("disabled")).not.toBeNull();
    expect(container.textContent).toContain(
      "工具在本机执行；关闭后 Agent 会拿着假值去请求或写入文件",
    );
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

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog?.getAttribute("data-state")).toBe("open");
    expect(
      dialog?.querySelector("#streaming-restore-demo-title")?.textContent,
    ).toBe("流式响应还原演示");
    expect(dialog?.textContent).toContain("请求侧脱敏");
    expect(dialog?.textContent).toContain("占位符还原");
    expect(dialog?.textContent).toContain("不对响应正文或 SSE");
    expect(dialog?.textContent).toContain("固定示例");
    expect(dialog?.textContent).toContain("alice@example.com");
    expect(dialog?.textContent).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(dialog?.textContent).toContain(
      '"type":"response.output_text.delta"',
    );
    expect(dialog?.textContent).toContain('"delta":"<PRIVATE_EMAIL_7f3a"');
    expect(dialog?.textContent).toContain('"delta":"91c04d28be56>"');
    expect(dialog?.textContent).toContain("正文: alice@example.com");
    expect(dialog?.textContent).toContain("客户端");
    expect(dialog?.textContent).toContain("AstrLink");
    expect(dialog?.textContent).toContain("上游");
    expect(dialog?.textContent).not.toContain("响应审核扫描");
    expect(dialog?.textContent).not.toContain("SSE 审核");

    const canvasBefore = dialog?.querySelector(
      '[data-testid="streaming-restore-demo"]',
    );
    const packetsBefore = [...document.querySelectorAll("[data-packet]")];
    expect(canvasBefore).not.toBeNull();
    expect(packetsBefore).toHaveLength(5);
    expect(document.querySelector('[data-lane="request"]')).not.toBeNull();
    expect(document.querySelector('[data-lane="response"]')).not.toBeNull();
    expect(
      document.querySelector('[data-packet="plain"]')?.textContent,
    ).toContain("alice@example.com");
    expect(
      document.querySelector('[data-packet="redacted"]')?.textContent,
    ).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(
      document.querySelector('[data-packet="chunk-a"]')?.textContent,
    ).toContain('"delta":"<PRIVATE_EMAIL_7f3a"');
    expect(
      document.querySelector('[data-packet="chunk-b"]')?.textContent,
    ).toContain('"delta":"91c04d28be56>"');
    expect(
      document.querySelector('[data-packet="restored"]')?.textContent,
    ).toContain("正文: alice@example.com");

    await act(async () => {
      button("重新播放").click();
      await Promise.resolve();
    });
    const canvasAfter = document.querySelector(
      '[data-testid="streaming-restore-demo"]',
    );
    const packetsAfter = [...document.querySelectorAll("[data-packet]")];
    expect(canvasAfter).not.toBeNull();
    expect(canvasAfter).not.toBe(canvasBefore);
    expect(packetsAfter).toHaveLength(5);
    expect(packetsAfter[0]).not.toBe(packetsBefore[0]);

    for (const [name, mock] of Object.entries(bridgeMocks)) {
      expect(mock.mock.calls.length, name).toBe(bridgeCallsBefore[name]);
    }
    expect(
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="响应还原占位符"]',
        )
        ?.getAttribute("aria-checked"),
    ).toBe("false");
    expect(
      container.querySelector<HTMLButtonElement>("#privacy-request-action")
        ?.textContent,
    ).toContain("阻止请求");

    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
      await Promise.resolve();
    });
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => {
      button("查看流式演示").click();
      await Promise.resolve();
    });
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    await act(async () => {
      button("关闭").click();
      await Promise.resolve();
    });
    expect(document.querySelector('[role="dialog"]')).toBeNull();

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
          confidence: 0.42,
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
    await openDryRun();

    await act(async () => {
      actionButton("开始检测").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenCalledWith({
      protocol: "openai.chat",
      sample_text: expect.stringContaining("chen.yu@example.com"),
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
    expect(container.textContent).toContain("置信度 91%，达到 60% 门槛");
    expect(container.textContent).toContain("已忽略 1 处");
    expect(container.textContent).toContain("置信度 42%，低于 60% 门槛");
    expect(container.textContent).toContain("替换为");
    expect(container.textContent).toContain("<PRIVATE_EMAIL_7f3a91c04d28be56>");
    expect(container.textContent).toContain("alice@example.com");
    expect(container.textContent).toContain("脱敏后的请求");
    expect(
      document
        .querySelector('[role="tab"][aria-label="试运行"]')
        ?.getAttribute("data-state"),
    ).toBe("active");
    await openPolicySection("检测与还原");
    expect(button("检测与还原").getAttribute("data-state")).toBe("active");
  });

  it("locates UTF-8 matches in the exact input and supports repeated keyboard tests", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: true }),
    );
    const text = "  中文😀：alice@example.com，再次 alice@example.com  ";
    const value = "alice@example.com";
    const start = text.lastIndexOf(value);
    bridgeMocks.dryRunPrivacyPolicy.mockResolvedValue({
      decision: "warn",
      findings_summary: "email=1",
      findings: [
        {
          kind: "email",
          path: "/messages/0/content",
          confidence: 1,
          start: new TextEncoder().encode(text.slice(0, start)).length,
          end: new TextEncoder().encode(text.slice(0, start + value.length))
            .length,
        },
      ],
      suppressed_findings: [],
      inspected_body: JSON.stringify({ messages: [{ content: text }] }),
    });
    await renderPolicy();
    await openDryRun();
    const input = container.querySelector<HTMLTextAreaElement>(
      "#privacy-dry-run-sample",
    )!;
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )!.set!;
    await act(async () => {
      setter.call(input, text);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    for (const modifier of ["metaKey", "ctrlKey"]) {
      const target = modifier === "metaKey" ? input : document.activeElement!;
      await act(async () =>
        target.dispatchEvent(
          new KeyboardEvent("keydown", {
            key: "Enter",
            [modifier]: true,
            bubbles: true,
          }),
        ),
      );
      await flush();
      expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenLastCalledWith(
        expect.objectContaining({ sample_text: text }),
      );
    }
    const row = container.querySelector('[data-testid="dry-run-finding"]')!;
    expect(row.querySelector("mark")?.textContent).toBe(value);
    expect(row.textContent).toContain("匹配正则规则");
    expect(row.textContent).toContain("发出警告");
    await act(async () => button("定位原文").click());
    expect(document.activeElement).toBe(input);
    expect(input.selectionStart).toBe(start);
    expect(input.selectionEnd).toBe(start + value.length);
    const details = container.querySelector(
      '[data-testid="safety-dry-run-result"] details',
    )!;
    expect(details.hasAttribute("open")).toBe(false);
    await act(async () => button("清空").click());
    expect(input.value).toBe("");
    expect(actionButton("开始检测").disabled).toBe(true);
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).toBeNull();
    await chooseOption('[aria-label="选择示例"]', "售后工单");
    expect(input.value).toContain("chen.yu@example.com");
    await chooseOption('[aria-label="选择示例"]', "报销付款邮件");
    expect(input.value).toContain("IBAN");
    await chooseOption('[aria-label="选择示例"]', "售后工单");
    expect(input.value).toContain("chen.yu@example.com");
  });

  it("offers diverse dry-run presets and clears stale results when switching", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: true, detector: "regex" }),
    );
    bridgeMocks.dryRunPrivacyPolicy.mockResolvedValueOnce({
      decision: "allow",
      findings_summary: "",
      findings: [],
      suppressed_findings: [],
      redactions: [],
      inspected_body: '{"messages":[{"content":"故障信息","role":"user"}]}',
    });
    await renderPolicy();
    await openDryRun();

    await chooseOption('[aria-label="选择示例"]', "服务故障日志");
    const sample = container.querySelector<HTMLTextAreaElement>(
      'textarea[aria-label="待检测文本"]',
    );
    expect(sample?.value).toContain("10.24.8.16");
    expect(sample?.value).toContain("https://hooks.example.com/");
    expect(
      container.querySelector('[aria-label="选择示例"]')?.textContent,
    ).toContain("服务故障日志");

    await act(async () => {
      actionButton("开始检测").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenCalledWith({
      protocol: "openai.chat",
      sample_text: expect.stringContaining("10.24.8.16"),
      policy: {
        enabled: true,
        detector: "regex",
        local_model_id: null,
        min_confidence: 0.6,
        request_action: "redact",
      },
    });
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).not.toBeNull();

    await chooseOption('[aria-label="选择示例"]', "产品发布说明");
    expect(sample?.value).toContain("本次更新支持将多份文档合并导出");
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).toBeNull();
    expect(
      container.querySelector('[aria-label="选择示例"]')?.textContent,
    ).toContain("产品发布说明");
  });

  it("applies a request format chosen from the compact settings and clears stale results", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: true }),
    );
    bridgeMocks.dryRunPrivacyPolicy.mockResolvedValue({
      decision: "allow",
      findings_summary: "",
      findings: [],
      suppressed_findings: [],
      inspected_body: '{"messages":[]}',
    });
    await renderPolicy();
    await openDryRun();
    await act(async () => actionButton("开始检测").click());
    await flush();
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).not.toBeNull();

    await act(async () => button("请求格式").click());
    await chooseOption('[aria-label="试运行协议"]', "Anthropic Messages");
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).toBeNull();
    await act(async () => button("请求格式").click());
    await act(async () => actionButton("开始检测").click());
    await flush();
    expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenLastCalledWith(
      expect.objectContaining({
        protocol: "anthropic.messages",
        sample_text: expect.stringContaining("chen.yu@example.com"),
      }),
    );
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).not.toBeNull();
  });

  it("previews a disabled policy as enabled without saving the override", async () => {
    bridgeMocks.getPrivacyPolicy.mockResolvedValueOnce(
      policyRecord({ enabled: false, detector: "regex" }),
    );
    bridgeMocks.dryRunPrivacyPolicy.mockResolvedValueOnce({
      decision: "redact",
      findings_summary: "email=1",
      findings: [],
      suppressed_findings: [],
      redactions: [],
      inspected_body: '{"messages":[{"content":"alice@example.com","role":"user"}]}',
    });
    await renderPolicy();
    await openDryRun();

    await act(async () => {
      actionButton("开始检测").click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.dryRunPrivacyPolicy).toHaveBeenCalledWith({
      protocol: "openai.chat",
      sample_text: expect.any(String),
      policy: {
        enabled: true,
        detector: "regex",
        local_model_id: null,
        min_confidence: 0.6,
        request_action: "redact",
      },
    });
    expect(bridgeMocks.updatePrivacyPolicy).not.toHaveBeenCalled();
    expect(
      container.querySelector('[data-testid="safety-dry-run-result"]'),
    ).not.toBeNull();
    expect(
      container
        .querySelector('[role="switch"][aria-label="启用隐私保护"]')
        ?.getAttribute("aria-checked"),
    ).toBe("false");
  });

  it("switches regex source and can seed custom rules from the builtin catalog", async () => {
    const builtinRules = [
      { kind: "email" as const, pattern: `(?i)alice@[a-z.]+` },
      { kind: "common_secret" as const, pattern: `sk-[A-Za-z0-9]+` },
    ];
    bridgeMocks.getPrivacyRegexBuiltinRules.mockResolvedValue({
      rules: builtinRules,
    });
    bridgeMocks.updatePrivacyPolicy.mockResolvedValueOnce(
      policyRecord({
        regex_source: "custom",
        custom_regex_rules: builtinRules,
      }),
    );
    await renderPolicy();

    expect(container.textContent).toContain("Regex 规则来源");
    expect(container.textContent).toContain("固定且不可编辑");

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[role="radio"][aria-label="自定义规则"]',
        )
        ?.click();
      await Promise.resolve();
    });
    await flush();

    expect(bridgeMocks.getPrivacyRegexBuiltinRules).toHaveBeenCalled();
    expect(bridgeMocks.updatePrivacyPolicy).toHaveBeenCalledWith(etag, {
      regex_source: "custom",
      custom_regex_rules: builtinRules,
    });
    expect(container.textContent).toContain("一键填入内置规则");
    expect(container.textContent).toContain("添加规则");
  });
});
