import { useEffect, useMemo, useRef, useState } from "react";

import {
  cancelPrivacyModelInstallation,
  deletePrivacyModelInstallation,
  dryRunPrivacyPolicy,
  getPrivacyModelCatalog,
  getPrivacyModelInstallation,
  getPrivacyPolicy,
  installPrivacyModel,
  listPrivacyModelInstallations,
  probePrivacyModel,
  updatePrivacyPolicy,
} from "./bridge";
import {
  isResourceHeavyVariant,
  MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES,
  utf8ByteLength,
  type CanonicalPrivacyKind,
  type PrivacyAction,
  type PrivacyCatalogModel,
  type PrivacyDryRunProtocol,
  type PrivacyDryRunResult,
  type PrivacyLabelMapping,
  type PrivacyModelInstallation,
  type PrivacyModelInstallInput,
  type PrivacyModelProbe,
  type PrivacyModelVariant,
  type PrivacyPolicyPatch,
  type PrivacyPolicyRecord,
} from "./privacy-policy-model";
import { PageHeader } from "./PageHeader";

type SafetyPolicyStatus = "blocked" | "loading" | "ready" | "error";
type ModelView = "catalog" | "installed" | "custom";

interface CatalogPreparation {
  catalogID: string;
  probe: PrivacyModelProbe;
  variant: PrivacyModelVariant;
  labelMapping: PrivacyLabelMapping;
  touchedLabels: string[];
}

interface PendingInstallation {
  key: string;
  name: string;
  variant: PrivacyModelVariant;
  input: PrivacyModelInstallInput;
}

type PendingModelAction =
  | {
      kind: "activate";
      installationID: string;
      patch: PrivacyPolicyPatch;
    }
  | {
      kind: "remove";
      installationID: string;
    };

export interface SafetyPolicyProps {
  coreSessionKey: string | null;
  isReady: boolean;
}

const actionLabels: Record<PrivacyAction, string> = {
  allow: "允许",
  warn: "警告并继续",
  block: "阻止请求",
  redact: "脱敏后继续",
};

const dryRunProtocolOptions: ReadonlyArray<{
  value: PrivacyDryRunProtocol;
  label: string;
}> = [
  { value: "openai.chat", label: "OpenAI Chat Completions" },
  { value: "openai.completions", label: "OpenAI Completions" },
  { value: "openai.responses", label: "OpenAI Responses" },
  { value: "openai.responses.compact", label: "OpenAI Responses Compact" },
  { value: "anthropic.messages", label: "Anthropic Messages" },
  { value: "google.generate_content", label: "Google Generate Content" },
];

const dryRunSampleExample =
  "请联系 alice@example.com 或拨打 +1-415-555-2671，银行卡 4242-4242-4242-4242。";

const installationStatusLabels: Record<
  PrivacyModelInstallation["status"],
  string
> = {
  downloading: "下载中",
  ready: "已就绪",
  error: "不可用",
};

const installationErrorLabels: Record<
  NonNullable<PrivacyModelInstallation["error"]>,
  string
> = {
  download_failed: "下载失败",
  integrity_failed: "完整性校验失败",
  incompatible_model: "模型不兼容",
};

const canonicalKindOptions: ReadonlyArray<{
  value: CanonicalPrivacyKind;
  label: string;
}> = [
  { value: "email", label: "邮箱" },
  { value: "phone", label: "电话" },
  { value: "account", label: "账号" },
  { value: "payment_card", label: "银行卡" },
  { value: "ip_address", label: "IP 地址" },
  { value: "url", label: "URL" },
  { value: "common_secret", label: "常见密钥" },
  { value: "private_address", label: "私人地址" },
  { value: "private_date", label: "私人日期" },
  { value: "private_person", label: "人名" },
];

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const unit = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  const value = bytes / 1024 ** unit;
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

function prettyJSON(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function summarizeDryRunFindings(result: PrivacyDryRunResult): string {
  if (result.findings.length === 0) {
    return "未命中";
  }
  const counts = new Map<CanonicalPrivacyKind, number>();
  for (const finding of result.findings) {
    counts.set(finding.kind, (counts.get(finding.kind) ?? 0) + 1);
  }
  return canonicalKindOptions
    .filter((option) => counts.has(option.value))
    .map((option) => `${option.label} × ${counts.get(option.value)}`)
    .join(" · ");
}

function dryRunKindLabel(kind: CanonicalPrivacyKind): string {
  return (
    canonicalKindOptions.find((option) => option.value === kind)?.label ?? kind
  );
}

function recommendedVariant(
  variants: PrivacyModelVariant[],
): PrivacyModelVariant | null {
  return (
    variants.find((variant) => variant.supported && variant.recommended) ??
    variants.find((variant) => variant.supported) ??
    null
  );
}

function mergeInstallation(
  current: PrivacyModelInstallation[],
  next: PrivacyModelInstallation,
): PrivacyModelInstallation[] {
  const index = current.findIndex((item) => item.id === next.id);
  if (index < 0) return [...current, next];
  return current.map((item) => (item.id === next.id ? next : item));
}

function initialLabelMapping(probe: PrivacyModelProbe): PrivacyLabelMapping {
  return Object.fromEntries(
    probe.labels.map((label) => [label.label, label.suggested_kind]),
  );
}

function variantForCatalog(
  model: PrivacyCatalogModel,
  selectedVariants: Record<string, string>,
): PrivacyModelVariant | null {
  const selected = selectedVariants[model.id];
  return (
    model.variants.find(
      (variant) => variant.id === selected && variant.supported,
    ) ?? recommendedVariant(model.variants)
  );
}

interface LabelMappingDialogProps {
  title: string;
  labels: PrivacyModelProbe["labels"];
  mapping: PrivacyLabelMapping;
  touchedLabels: string[];
  summary: string;
  confirmLabel: string;
  confirmDisabled: boolean;
  onCancel: () => void;
  onChange: (label: string, kind: CanonicalPrivacyKind | null) => void;
  onConfirm: () => void;
}

function LabelMappingDialog({
  title,
  labels,
  mapping,
  touchedLabels,
  summary,
  confirmLabel,
  confirmDisabled,
  onCancel,
  onChange,
  onConfirm,
}: LabelMappingDialogProps) {
  return (
    <div className="token-dialog-backdrop" role="presentation">
      <section
        aria-labelledby="privacy-label-mapping-title"
        aria-modal="true"
        className="token-dialog model-mapping-dialog"
        role="dialog"
      >
        <h3 id="privacy-label-mapping-title">{title}</h3>
        <p>
          将模型基础标签映射到 AstrLink 的稳定隐私类别；不需要处理的标签可明确忽略。
        </p>
        <div className="label-mapping__list">
          {labels.map((label) => {
            const unresolved =
              label.suggested_kind === null &&
              !touchedLabels.includes(label.label);
            return (
              <label key={label.label}>
                <code>{label.label}</code>
                <select
                  aria-label={`${label.label} 标签映射`}
                  onChange={(event) => {
                    const value = event.currentTarget.value;
                    onChange(
                      label.label,
                      value === "" ? null : (value as CanonicalPrivacyKind),
                    );
                  }}
                  value={
                    unresolved
                      ? "__unresolved__"
                      : (mapping[label.label] ?? "")
                  }
                >
                  {unresolved ? (
                    <option disabled value="__unresolved__">
                      请选择
                    </option>
                  ) : null}
                  <option value="">忽略此标签</option>
                  {canonicalKindOptions.map((kind) => (
                    <option key={kind.value} value={kind.value}>
                      {kind.label}
                    </option>
                  ))}
                </select>
              </label>
            );
          })}
        </div>
        <div className="model-mapping-dialog__summary">{summary}</div>
        <div className="token-dialog__actions">
          <button className="btn-secondary" onClick={onCancel} type="button">
            取消
          </button>
          <button
            className="btn-primary"
            disabled={confirmDisabled}
            onClick={onConfirm}
            type="button"
          >
            {confirmLabel}
          </button>
        </div>
      </section>
    </div>
  );
}

interface ModelActionDialogProps {
  action: PendingModelAction;
  installation: PrivacyModelInstallation;
  onCancel: () => void;
  onConfirm: () => void;
}

function ModelActionDialog({
  action,
  installation,
  onCancel,
  onConfirm,
}: ModelActionDialogProps) {
  const activating = action.kind === "activate";
  const downloading = installation.status === "downloading";
  const heavy = isResourceHeavyVariant(installation);
  const title = activating
    ? "确认使用本地模型"
    : downloading
      ? "取消模型下载"
      : "删除本地模型";
  const confirmLabel = activating
    ? "确认用于策略"
    : downloading
      ? "确认取消下载"
      : "确认删除";

  return (
    <div className="token-dialog-backdrop" role="presentation">
      <section
        aria-labelledby="privacy-model-action-title"
        aria-modal="true"
        className="token-dialog model-action-dialog"
        role="dialog"
      >
        <h3 id="privacy-model-action-title">{title}</h3>
        <p>
          {activating
            ? `将使用 ${installation.name} · ${installation.variant_name} 进行本地检测。`
            : downloading
              ? `将停止 ${installation.name} 的下载并清理临时文件。`
              : `将从本机删除 ${installation.name} · ${installation.variant_name}，再次使用时需要重新下载。`}
        </p>
        {activating ? (
          <>
            <dl className="model-action-dialog__resources">
              <div>
                <dt>磁盘占用</dt>
                <dd>{formatBytes(installation.bytes_total)}</dd>
              </div>
              <div>
                <dt>预计内存</dt>
                <dd>{formatBytes(installation.estimated_ram_bytes)}</dd>
              </div>
            </dl>
            <p className="model-action-dialog__note">
              {heavy
                ? "该模型资源占用较高，性能较低的设备可能明显变慢。确认后会立即更新全局策略，并在首个受保护请求时加载模型。"
                : "确认后会立即更新全局策略，并在首个受保护请求时加载模型。"}
            </p>
          </>
        ) : null}
        <div className="token-dialog__actions">
          <button
            autoFocus
            className="btn-secondary"
            onClick={onCancel}
            type="button"
          >
            返回
          </button>
          <button
            className={activating ? "btn-primary" : "btn-danger"}
            onClick={onConfirm}
            type="button"
          >
            {confirmLabel}
          </button>
        </div>
      </section>
    </div>
  );
}

function InstallationResourceDialog({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: PendingInstallation;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="token-dialog-backdrop" role="presentation">
      <section
        aria-labelledby="privacy-install-resource-title"
        aria-modal="true"
        className="token-dialog model-action-dialog"
        role="dialog"
      >
        <h3 id="privacy-install-resource-title">确认安装本地模型</h3>
        <p>
          {pending.name} · {pending.variant.name} 资源占用较高。
        </p>
        <dl className="model-action-dialog__resources">
          <div>
            <dt>下载大小</dt>
            <dd>{formatBytes(pending.variant.bytes_total)}</dd>
          </div>
          <div>
            <dt>预计内存</dt>
            <dd>{formatBytes(pending.variant.estimated_ram_bytes)}</dd>
          </div>
        </dl>
        <p className="model-action-dialog__note">
          性能较低的设备可能明显变慢。
        </p>
        <div className="token-dialog__actions">
          <button
            autoFocus
            className="btn-secondary"
            onClick={onCancel}
            type="button"
          >
            返回
          </button>
          <button className="btn-primary" onClick={onConfirm} type="button">
            继续安装
          </button>
        </div>
      </section>
    </div>
  );
}

interface StreamingRestoreDemoDialogProps {
  onClose: () => void;
}

function StreamingRestoreDemoDialog({
  onClose,
}: StreamingRestoreDemoDialogProps) {
  const [replayKey, setReplayKey] = useState(0);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return (
    <div className="token-dialog-backdrop" role="presentation">
      <section
        aria-labelledby="streaming-restore-demo-title"
        aria-modal="true"
        className="token-dialog streaming-restore-demo-dialog"
        role="dialog"
      >
        <h3 id="streaming-restore-demo-title">流式响应还原演示</h3>
        <p>
          固定教学示例（OpenAI Responses · <code>stream: true</code>
          ）：请求侧脱敏与占位符还原。不对响应正文或 SSE 事件做审核扫描。
        </p>
        <p className="streaming-restore-demo__example-note">
          示例邮箱 <code>alice@example.com</code>
          为固定示例，不代表当前策略状态。
        </p>
        <div
          aria-label="请求脱敏与响应占位符还原数据流"
          className="streaming-restore-demo__canvas"
          key={replayKey}
        >
          <div className="streaming-restore-demo__nodes" aria-hidden="true">
            <div
              className="streaming-restore-demo__node streaming-restore-demo__node--client"
            >
              <strong>客户端</strong>
              <span>OpenAI Responses</span>
            </div>
            <div
              className="streaming-restore-demo__node streaming-restore-demo__node--gateway"
            >
              <span className="streaming-restore-demo__shield" />
              <strong>AstrLink</strong>
              <span>隐私网关</span>
            </div>
            <div
              className="streaming-restore-demo__node streaming-restore-demo__node--upstream"
            >
              <strong>上游</strong>
              <span>SSE 传输</span>
            </div>
          </div>

          <div
            aria-hidden="true"
            className="streaming-restore-demo__lane streaming-restore-demo__lane--request"
          >
            <div className="streaming-restore-demo__track">
              <span className="streaming-restore-demo__flow-dots" />
              <span className="streaming-restore-demo__lane-label">
                请求 →
              </span>
            </div>
            <span className="streaming-restore-demo__packet streaming-restore-demo__packet--plain">
              alice@example.com
            </span>
            <span className="streaming-restore-demo__packet streaming-restore-demo__packet--redacted">
              &lt;PRIVATE_EMAIL&gt;
            </span>
          </div>

          <div
            aria-hidden="true"
            className="streaming-restore-demo__lane streaming-restore-demo__lane--response"
          >
            <div className="streaming-restore-demo__track">
              <span className="streaming-restore-demo__flow-dots" />
              <span className="streaming-restore-demo__lane-label">
                ← 响应
              </span>
            </div>
            <span className="streaming-restore-demo__packet streaming-restore-demo__packet--chunk-a">
              data: &lt;PRIVATE_
            </span>
            <span className="streaming-restore-demo__packet streaming-restore-demo__packet--chunk-b">
              EMAIL&gt;
            </span>
            <span className="streaming-restore-demo__packet streaming-restore-demo__packet--restored">
              data: alice@example.com
            </span>
          </div>

          <ol className="streaming-restore-demo__legend">
            <li>
              <span className="streaming-restore-demo__swatch streaming-restore-demo__swatch--request" />
              请求侧脱敏：原文 → <code>&lt;PRIVATE_EMAIL&gt;</code>
            </li>
            <li>
              <span className="streaming-restore-demo__swatch streaming-restore-demo__swatch--sse" />
              上游分片跨 chunk 保留不完整占位符
            </li>
            <li>
              <span className="streaming-restore-demo__swatch streaming-restore-demo__swatch--restore" />
              网关拼完整后还原给客户端
            </li>
          </ol>
        </div>
        <div className="token-dialog__actions">
          <button
            className="btn-secondary"
            onClick={() => setReplayKey((current) => current + 1)}
            type="button"
          >
            重新播放
          </button>
          <button
            autoFocus
            className="btn-primary"
            onClick={onClose}
            type="button"
          >
            关闭
          </button>
        </div>
      </section>
    </div>
  );
}

export function SafetyPolicy({
  coreSessionKey,
  isReady,
}: SafetyPolicyProps) {
  const [status, setStatus] = useState<SafetyPolicyStatus>("blocked");
  const [record, setRecord] = useState<PrivacyPolicyRecord | null>(null);
  const [catalog, setCatalog] = useState<PrivacyCatalogModel[]>([]);
  const [installations, setInstallations] = useState<
    PrivacyModelInstallation[]
  >([]);
  const [view, setView] = useState<ModelView>("catalog");
  const [selectedVariants, setSelectedVariants] = useState<
    Record<string, string>
  >({});
  const [customRepoID, setCustomRepoID] = useState("");
  const [customRevision, setCustomRevision] = useState("main");
  const [probe, setProbe] = useState<PrivacyModelProbe | null>(null);
  const [customMappingOpen, setCustomMappingOpen] = useState(false);
  const [probeVariantID, setProbeVariantID] = useState("");
  const [labelMapping, setLabelMapping] = useState<PrivacyLabelMapping>({});
  const [labelMappingTouched, setLabelMappingTouched] = useState<string[]>([]);
  const [catalogPreparation, setCatalogPreparation] =
    useState<CatalogPreparation | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [operationBusy, setOperationBusy] = useState<string | null>(null);
  const [probing, setProbing] = useState(false);
  const [catalogProbeBusy, setCatalogProbeBusy] = useState<string | null>(null);
  const [dryRunProtocol, setDryRunProtocol] =
    useState<PrivacyDryRunProtocol>("openai.chat");
  const [dryRunSample, setDryRunSample] = useState(dryRunSampleExample);
  const [dryRunBusy, setDryRunBusy] = useState(false);
  const [dryRunError, setDryRunError] = useState<string | null>(null);
  const [dryRunResult, setDryRunResult] = useState<PrivacyDryRunResult | null>(
    null,
  );
  const [minConfidenceDraft, setMinConfidenceDraft] = useState("");
  const [pendingModelAction, setPendingModelAction] =
    useState<PendingModelAction | null>(null);
  const [pendingInstallation, setPendingInstallation] =
    useState<PendingInstallation | null>(null);
  const [streamingDemoOpen, setStreamingDemoOpen] = useState(false);
  const generationRef = useRef(0);
  const operationRequestRef = useRef(0);
  const probeRequestRef = useRef(0);
  const pollRequestRef = useRef(0);
  const dryRunRequestRef = useRef(0);
  const persistedMinConfidence = record?.policy.min_confidence;

  useEffect(() => {
    setMinConfidenceDraft(
      persistedMinConfidence === undefined
        ? ""
        : persistedMinConfidence.toFixed(2),
    );
  }, [persistedMinConfidence]);

  const load = async (generation: number) => {
    try {
      const [nextRecord, nextCatalog, nextInstallations] = await Promise.all([
        getPrivacyPolicy(),
        getPrivacyModelCatalog(),
        listPrivacyModelInstallations(),
      ]);
      if (generationRef.current !== generation) return;
      setRecord(nextRecord);
      setCatalog(nextCatalog.items);
      setInstallations(nextInstallations.items);
      setSelectedVariants(
        Object.fromEntries(
          nextCatalog.items.flatMap((model) => {
            const variant = recommendedVariant(model.variants);
            return variant === null ? [] : [[model.id, variant.id]];
          }),
        ),
      );
      setStatus("ready");
    } catch (loadError) {
      if (generationRef.current !== generation) return;
      setStatus("error");
      setError(messageOf(loadError, "无法读取安全策略与模型目录。"));
    }
  };

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    operationRequestRef.current += 1;
    probeRequestRef.current += 1;
    pollRequestRef.current += 1;
    dryRunRequestRef.current += 1;
    setError(null);
    setNotice(null);
    setSaving(false);
    setOperationBusy(null);
    setProbing(false);
    setCatalogProbeBusy(null);
    setDryRunBusy(false);
    setDryRunError(null);
    setDryRunResult(null);
    setPendingModelAction(null);
    setPendingInstallation(null);
    setStreamingDemoOpen(false);
    setProbe(null);
    setCustomMappingOpen(false);
    setLabelMapping({});
    setLabelMappingTouched([]);
    setCatalogPreparation(null);

    if (!isReady || coreSessionKey === null) {
      setStatus("blocked");
      setRecord(null);
      setCatalog([]);
      setInstallations([]);
      return () => {
        if (generationRef.current === generation) {
          generationRef.current += 1;
        }
      };
    }

    setStatus("loading");
    setRecord(null);
    setCatalog([]);
    setInstallations([]);
    void load(generation);

    return () => {
      if (generationRef.current === generation) {
        generationRef.current += 1;
      }
    };
  }, [coreSessionKey, isReady]);

  const downloadingIDs = useMemo(
    () =>
      installations
        .filter((installation) => installation.status === "downloading")
        .map((installation) => installation.id)
        .sort(),
    [installations],
  );
  const downloadingSignature = downloadingIDs.join(",");

  useEffect(() => {
    if (
      !isReady ||
      coreSessionKey === null ||
      downloadingIDs.length === 0
    ) {
      return;
    }
    const generation = generationRef.current;
    const pollRequest = pollRequestRef.current + 1;
    pollRequestRef.current = pollRequest;
    let cancelled = false;
    let timer: number | null = null;
    const stillCurrent = () =>
      !cancelled &&
      generationRef.current === generation &&
      pollRequestRef.current === pollRequest;
    const schedule = () => {
      if (!stillCurrent()) return;
      timer = window.setTimeout(() => {
        void poll();
      }, 900);
    };
    const poll = async () => {
      try {
        const updates = await Promise.all(
          downloadingIDs.map((id) => getPrivacyModelInstallation(id)),
        );
        if (!stillCurrent()) return;
        setInstallations((current) =>
          updates.reduce(
            (items, update) =>
              items.some((item) => item.id === update.id)
                ? mergeInstallation(items, update)
                : items,
            current,
          ),
        );
      } catch (pollError) {
        if (!stillCurrent()) return;
        setError(messageOf(pollError, "无法刷新模型下载进度。"));
      } finally {
        schedule();
      }
    };
    schedule();
    return () => {
      cancelled = true;
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [
    coreSessionKey,
    downloadingSignature,
    isReady,
  ]);

  const refresh = () => {
    if (
      !isReady ||
      coreSessionKey === null ||
      saving ||
      operationBusy !== null ||
      probing ||
      catalogProbeBusy !== null
    ) {
      return;
    }
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    operationRequestRef.current += 1;
    probeRequestRef.current += 1;
    pollRequestRef.current += 1;
    setStatus("loading");
    setRecord(null);
    setCatalog([]);
    setInstallations([]);
    setError(null);
    setNotice(null);
    setProbe(null);
    setCustomMappingOpen(false);
    setCatalogPreparation(null);
    setPendingModelAction(null);
    void load(generation);
  };

  const patchPolicy = async (patch: PrivacyPolicyPatch) => {
    if (record === null || saving || status !== "ready") return;
    const generation = generationRef.current;
    const previous = record;
    setRecord({
      ...record,
      policy: {
        ...record.policy,
        ...patch,
      },
    });
    setSaving(true);
    setError(null);
    setNotice(null);
    setDryRunResult(null);
    setDryRunError(null);

    try {
      const next = await updatePrivacyPolicy(record.etag, patch);
      if (generationRef.current !== generation) return;
      setRecord(next);
      setNotice("安全策略已保存。");
    } catch (patchError) {
      if (generationRef.current !== generation) return;
      let authoritative = previous;
      let reconciled = false;
      try {
        authoritative = await getPrivacyPolicy();
        reconciled = true;
      } catch {
        // Keep the last known-good record when Core cannot be queried.
      }
      if (generationRef.current !== generation) return;
      setRecord(authoritative);
      const failure = messageOf(patchError, "无法保存安全策略。");
      setError(
        reconciled
          ? `${failure}；已重新读取 Core 当前设置。`
          : `${failure}；已恢复上次设置。`,
      );
    } finally {
      if (generationRef.current === generation) setSaving(false);
    }
  };

  const selectedInstallation =
    record?.policy.local_model_id === null ||
    record?.policy.local_model_id === undefined
      ? null
      : (installations.find(
          (installation) =>
            installation.id === record.policy.local_model_id,
        ) ?? null);
  const selectedModelReady = selectedInstallation?.status === "ready";

  const requestModelActivation = (
    installation: PrivacyModelInstallation,
    patch: PrivacyPolicyPatch,
  ) => {
    setError(null);
    setNotice(null);
    setPendingModelAction({
      kind: "activate",
      installationID: installation.id,
      patch,
    });
  };

  const changeEnabled = (enabled: boolean) => {
    if (
      enabled &&
      record?.policy.detector === "local_model" &&
      !selectedModelReady
    ) {
      setError("请选择一个已就绪的本地模型后再启用策略。");
      setView("installed");
      return;
    }
    if (
      enabled &&
      record?.policy.detector === "local_model" &&
      selectedInstallation !== null
    ) {
      requestModelActivation(selectedInstallation, { enabled });
      return;
    }
    void patchPolicy({ enabled });
  };

  const useRegex = () => {
    void patchPolicy({ detector: "regex", local_model_id: null });
  };

  const chooseInstallation = (installation: PrivacyModelInstallation) => {
    if (installation.status !== "ready") {
      setError("模型下载并校验完成后才能用于检测。");
      return;
    }
    if (
      record?.policy.enabled === true &&
      record.policy.local_model_id !== installation.id
    ) {
      requestModelActivation(installation, {
        detector: "local_model",
        local_model_id: installation.id,
      });
      return;
    }
    void patchPolicy({
      detector: "local_model",
      local_model_id: installation.id,
    });
  };

  const changeAction = (action: string) => {
    if (action === "warn" || action === "block" || action === "redact") {
      void patchPolicy({ request_action: action });
    }
  };

  const commitMinConfidence = () => {
    if (record === null) return;
    const minConfidence = Number(minConfidenceDraft);
    if (
      minConfidenceDraft.trim() === "" ||
      !Number.isFinite(minConfidence) ||
      minConfidence < 0 ||
      minConfidence > 1
    ) {
      setMinConfidenceDraft(record.policy.min_confidence.toFixed(2));
      setError("模型最低置信度必须是 0 到 1 之间的数字。");
      return;
    }
    setMinConfidenceDraft(minConfidence.toFixed(2));
    if (minConfidence !== record.policy.min_confidence) {
      void patchPolicy({ min_confidence: minConfidence });
    }
  };

  const dryRunSampleBytes = utf8ByteLength(dryRunSample);
  const dryRunSampleOverLimit =
    dryRunSampleBytes > MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES;

  const runDryRun = async () => {
    if (
      !isReady ||
      coreSessionKey === null ||
      record === null ||
      dryRunBusy ||
      saving ||
      status !== "ready"
    ) {
      return;
    }
    const sample = dryRunSample.trim();
    if (sample === "") {
      setDryRunError("请输入用于试运行的样例文本。");
      setError("请输入用于试运行的样例文本。");
      return;
    }
    if (utf8ByteLength(sample) > MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES) {
      const message = `样例过长（上限 ${MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES / 1024} KiB），请缩短后再试。`;
      setDryRunError(message);
      setError(message);
      return;
    }
    if (
      record.policy.enabled &&
      record.policy.detector === "local_model" &&
      !selectedModelReady
    ) {
      setDryRunError("本地模型未就绪，无法试运行当前策略。");
      setError("本地模型未就绪，无法试运行当前策略。");
      return;
    }
    const generation = generationRef.current;
    const request = dryRunRequestRef.current + 1;
    dryRunRequestRef.current = request;
    setDryRunBusy(true);
    setDryRunError(null);
    setError(null);
    setNotice(null);
    try {
      const result = await dryRunPrivacyPolicy({
        protocol: dryRunProtocol,
        sample_text: sample,
        policy: {
          enabled: record.policy.enabled,
          detector: record.policy.detector,
          local_model_id: record.policy.local_model_id,
          min_confidence: record.policy.min_confidence,
          request_action: record.policy.request_action,
        },
      });
      if (
        generationRef.current !== generation ||
        dryRunRequestRef.current !== request
      ) {
        return;
      }
      setDryRunResult(result);
      setDryRunError(null);
      setNotice("试运行完成，结果仅用于本地预览。");
    } catch (caught) {
      if (
        generationRef.current !== generation ||
        dryRunRequestRef.current !== request
      ) {
        return;
      }
      setDryRunResult(null);
      const message = messageOf(caught, "无法完成安全策略试运行。");
      setDryRunError(message);
      setError(message);
    } finally {
      if (
        generationRef.current === generation &&
        dryRunRequestRef.current === request
      ) {
        setDryRunBusy(false);
      }
    }
  };

  const performInstallation = async ({
    key,
    variant,
    input,
  }: PendingInstallation) => {
    if (
      !isReady ||
      coreSessionKey === null ||
      operationBusy !== null ||
      !variant.supported
    ) {
      return;
    }
    const generation = generationRef.current;
    const request = operationRequestRef.current + 1;
    operationRequestRef.current = request;
    setOperationBusy(key);
    setError(null);
    setNotice(null);
    try {
      const installation = await installPrivacyModel(input);
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      setInstallations((current) =>
        mergeInstallation(current, installation),
      );
      setCatalogPreparation(null);
      setCustomMappingOpen(false);
      setView("installed");
      setNotice("模型安装已开始，可在“已安装”中查看进度。");
    } catch (installError) {
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(installError, "无法开始安装本地模型。"));
    } finally {
      if (
        generationRef.current === generation &&
        operationRequestRef.current === request
      ) {
        setOperationBusy(null);
      }
    }
  };

  const startInstallation = (
    key: string,
    name: string,
    variant: PrivacyModelVariant,
    input: PrivacyModelInstallInput,
  ) => {
    if (
      !isReady ||
      coreSessionKey === null ||
      operationBusy !== null ||
      pendingInstallation !== null ||
      !variant.supported
    ) {
      return;
    }
    const pending = { key, name, variant, input };
    if (isResourceHeavyVariant(variant)) {
      setPendingInstallation(pending);
      return;
    }
    void performInstallation(pending);
  };

  const prepareCatalogInstallation = async (
    model: PrivacyCatalogModel,
    variant: PrivacyModelVariant,
  ) => {
    if (
      probing ||
      catalogProbeBusy !== null ||
      operationBusy !== null ||
      !variant.supported
    ) {
      return;
    }
    const generation = generationRef.current;
    const request = probeRequestRef.current + 1;
    probeRequestRef.current = request;
    setCatalogProbeBusy(model.id);
    setCatalogPreparation(null);
    setError(null);
    setNotice(null);
    try {
      const result = await probePrivacyModel({
        repo_id: model.repo_id,
        revision: model.revision,
      });
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      if (
        result.repo_id !== model.repo_id ||
        result.requested_revision !== model.revision ||
        result.revision !== model.revision
      ) {
        throw new Error("Core 返回的探测结果与内置模型固定版本不一致。");
      }
      const probedVariant = result.variants.find(
        (candidate) =>
          candidate.id === variant.id && candidate.supported,
      );
      if (probedVariant === undefined) {
        throw new Error("所选内置模型版本未通过当前设备兼容性检查。");
      }
      setCatalogPreparation({
        catalogID: model.id,
        probe: result,
        variant: probedVariant,
        labelMapping: initialLabelMapping(result),
        touchedLabels: [],
      });
      setNotice("已校验固定模型版本，请确认标签映射与资源占用。");
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, "无法检查内置模型兼容性。"));
    } finally {
      if (
        generationRef.current === generation &&
        probeRequestRef.current === request
      ) {
        setCatalogProbeBusy(null);
      }
    }
  };

  const removeInstallation = (installation: PrivacyModelInstallation) => {
    if (operationBusy !== null) return;
    if (record?.policy.local_model_id === installation.id) {
      setError("该模型已被策略选中，请先切换到 Regex 或选择其他模型。");
      return;
    }
    setError(null);
    setNotice(null);
    setPendingModelAction({
      kind: "remove",
      installationID: installation.id,
    });
  };

  const confirmPendingModelAction = () => {
    if (pendingModelAction === null) return;
    const installation = installations.find(
      (item) => item.id === pendingModelAction.installationID,
    );
    if (installation === undefined) {
      setPendingModelAction(null);
      setError("模型状态已发生变化，请刷新后重试。");
      return;
    }
    if (pendingModelAction.kind === "activate") {
      if (installation.status !== "ready") {
        setPendingModelAction(null);
        setError("模型下载并校验完成后才能用于检测。");
        return;
      }
      const patch = pendingModelAction.patch;
      setPendingModelAction(null);
      void patchPolicy(patch);
      return;
    }
    if (record?.policy.local_model_id === installation.id) {
      setPendingModelAction(null);
      setError("该模型已被策略选中，请先切换到 Regex 或选择其他模型。");
      return;
    }
    setPendingModelAction(null);
    void performRemoveInstallation(installation);
  };

  const performRemoveInstallation = async (
    installation: PrivacyModelInstallation,
  ) => {
    const downloading = installation.status === "downloading";
    const generation = generationRef.current;
    const request = operationRequestRef.current + 1;
    operationRequestRef.current = request;
    setOperationBusy(installation.id);
    setError(null);
    setNotice(null);
    try {
      if (downloading) {
        await cancelPrivacyModelInstallation(installation.id);
      } else {
        await deletePrivacyModelInstallation(installation.id);
      }
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      pollRequestRef.current += 1;
      setInstallations((current) =>
        current.filter((item) => item.id !== installation.id),
      );
      setNotice(downloading ? "模型下载已取消。" : "本地模型已删除。");
    } catch (removeError) {
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(removeError, "无法取消或删除本地模型。"));
    } finally {
      if (
        generationRef.current === generation &&
        operationRequestRef.current === request
      ) {
        setOperationBusy(null);
      }
    }
  };

  const runProbe = async () => {
    if (
      probing ||
      catalogProbeBusy !== null ||
      operationBusy !== null
    ) {
      return;
    }
    const generation = generationRef.current;
    const request = probeRequestRef.current + 1;
    const requestedRepoID = customRepoID.trim();
    const requestedRevision = customRevision.trim();
    probeRequestRef.current = request;
    setProbing(true);
    setProbe(null);
    setCustomMappingOpen(false);
    setLabelMapping({});
    setLabelMappingTouched([]);
    setError(null);
    setNotice(null);
    try {
      const result = await probePrivacyModel({
        repo_id: requestedRepoID,
        revision: requestedRevision,
      });
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      if (
        result.repo_id !== requestedRepoID ||
        result.requested_revision !== requestedRevision
      ) {
        throw new Error("Core 返回的探测结果与当前自定义模型输入不一致。");
      }
      setProbe(result);
      setCustomMappingOpen(result.requires_label_mapping);
      setLabelMapping(initialLabelMapping(result));
      setLabelMappingTouched([]);
      setProbeVariantID(recommendedVariant(result.variants)?.id ?? "");
      setNotice("模型元数据与兼容性检查完成，尚未下载权重。");
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, "无法检查自定义模型。"));
    } finally {
      if (
        generationRef.current === generation &&
        probeRequestRef.current === request
      ) {
        setProbing(false);
      }
    }
  };

  if (status === "blocked") {
    return (
      <div className="safety-policy">
        <section className="safety-policy__unavailable">
          <strong>Core 就绪后可管理安全策略</strong>
          <span>策略和模型均由本地 Core 保存与执行。</span>
        </section>
      </div>
    );
  }

  const policy = record?.policy ?? null;
  const cannotEnableLocalModel =
    policy?.enabled === false &&
    policy.detector === "local_model" &&
    !selectedModelReady;
  const readyCount = installations.filter(
    (installation) => installation.status === "ready",
  ).length;
  const customVariant =
    probe?.variants.find(
      (variant) => variant.id === probeVariantID && variant.supported,
    ) ?? null;
  const unresolvedCustomLabels =
    probe?.labels.filter(
      (label) =>
        label.suggested_kind === null &&
        !labelMappingTouched.includes(label.label),
    ) ?? [];
  const catalogPreparationModel =
    catalogPreparation === null
      ? null
      : (catalog.find(
          (model) => model.id === catalogPreparation.catalogID,
        ) ?? null);
  const unresolvedCatalogLabels =
    catalogPreparation?.probe.labels.filter(
      (label) =>
        label.suggested_kind === null &&
        !catalogPreparation.touchedLabels.includes(label.label),
    ) ?? [];
  const pendingActionInstallation =
    pendingModelAction === null
      ? null
      : (installations.find(
          (installation) =>
            installation.id === pendingModelAction.installationID,
        ) ?? null);

  return (
    <div className="safety-policy">
      <PageHeader
        actions={
          <button
            className="btn-secondary"
            disabled={
              status === "loading" ||
              saving ||
              operationBusy !== null ||
              probing ||
              catalogProbeBusy !== null
            }
            onClick={refresh}
            type="button"
          >
            {status === "loading" ? "刷新中…" : "刷新"}
          </button>
        }
        description="在请求发送到上游前使用规则或所选本地模型检测敏感内容。"
        eyebrow="本地执行 · 全局策略"
        title="隐私保护"
        titleId="safety-policy-heading"
      />

      {error ? (
        <div className="inline-alert inline-alert--error" role="alert">
          {error}
        </div>
      ) : null}
      {notice ? (
        <div className="inline-alert inline-alert--success" role="status">
          {notice}
        </div>
      ) : null}

      {status === "loading" && record === null ? (
        <div className="safety-policy__loading" aria-label="正在读取安全策略">
          <span />
          <span />
        </div>
      ) : null}

      {status === "error" && record === null ? (
        <section className="safety-policy__unavailable">
          <strong>安全策略暂不可用</strong>
          <span>检查 Core 状态后重试。</span>
          <button className="btn-secondary" onClick={refresh} type="button">
            重试
          </button>
        </section>
      ) : null}

      {status === "ready" && policy !== null ? (
        <div className="safety-policy__grid">
          <section className="safety-card safety-card--policy">
            <div className="safety-card__title">
              <div>
                <span className="eyebrow">策略</span>
                <h3>全局隐私保护</h3>
              </div>
              <span
                className={`safety-card__state${
                  policy.enabled ? " safety-card__state--enabled" : ""
                }`}
              >
                {policy.enabled ? "已启用" : "已停用"}
              </span>
            </div>

            <label className="safety-master">
              <span>
                <strong>启用隐私保护</strong>
                <small>
                  {cannotEnableLocalModel
                    ? "需要先选择一个已就绪的本地模型"
                    : "变更会立即保存到本地 Core"}
                </small>
              </span>
              <input
                aria-label="启用隐私保护"
                checked={policy.enabled}
                disabled={saving || cannotEnableLocalModel}
                onChange={(event) => changeEnabled(event.currentTarget.checked)}
                type="checkbox"
              />
            </label>

            <fieldset className="safety-fieldset" disabled={saving}>
              <legend>检测方式</legend>
              <div className="safety-options">
                <label
                  className={
                    policy.detector === "regex"
                      ? "safety-option safety-option--selected"
                      : "safety-option"
                  }
                >
                  <input
                    checked={policy.detector === "regex"}
                    name="privacy-detector"
                    onChange={useRegex}
                    type="radio"
                  />
                  <span>
                    <strong>Regex</strong>
                    <small>快速且始终可用</small>
                  </span>
                </label>
                <label
                  className={
                    policy.detector === "local_model"
                      ? "safety-option safety-option--selected"
                      : "safety-option"
                  }
                >
                  <input
                    checked={policy.detector === "local_model"}
                    disabled={!selectedModelReady}
                    name="privacy-detector"
                    onChange={() => {
                      if (selectedInstallation !== null) {
                        chooseInstallation(selectedInstallation);
                      }
                    }}
                    type="radio"
                  />
                  <span>
                    <strong>本地模型</strong>
                    <small>
                      {selectedInstallation === null
                        ? "请从已安装模型中选择"
                        : `${selectedInstallation.name} · ${selectedInstallation.variant_name}`}
                    </small>
                  </span>
                </label>
              </div>
            </fieldset>

            <label className="safety-action" htmlFor="privacy-min-confidence">
              <span>
                <strong>模型最低置信度</strong>
                <small>
                  低于此分数的模型候选会被抑制；Regex 不受此门槛影响
                </small>
              </span>
              <input
                aria-label="模型最低置信度"
                disabled={saving}
                id="privacy-min-confidence"
                max="1"
                min="0"
                onBlur={commitMinConfidence}
                onChange={(event) =>
                  setMinConfidenceDraft(event.currentTarget.value)
                }
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.currentTarget.blur();
                  } else if (event.key === "Escape") {
                    event.preventDefault();
                    setMinConfidenceDraft(policy.min_confidence.toFixed(2));
                  }
                }}
                step="0.01"
                type="number"
                value={minConfidenceDraft}
              />
            </label>

            <label className="safety-action" htmlFor="privacy-request-action">
              <span>
                <strong>命中后的请求动作</strong>
                <small>响应审核将在后续版本提供</small>
              </span>
              <select
                disabled={saving}
                id="privacy-request-action"
                onChange={(event) => changeAction(event.currentTarget.value)}
                value={policy.request_action}
              >
                {policy.request_action === "allow" ? (
                  <option disabled value="allow">
                    允许（兼容值）
                  </option>
                ) : null}
                <option value="redact">{actionLabels.redact}</option>
                <option value="block">{actionLabels.block}</option>
                <option value="warn">{actionLabels.warn}</option>
              </select>
            </label>

            <label className="safety-master">
              <span>
                <strong>响应还原占位符</strong>
                <small>
                  {policy.request_action === "redact"
                    ? "默认开启：把本请求脱敏后的占位符在模型回复中还原给客户端"
                    : "仅在请求动作为「脱敏后继续」时生效"}
                </small>
              </span>
              <input
                aria-label="响应还原占位符"
                checked={policy.response_restore}
                disabled={saving || policy.request_action !== "redact"}
                onChange={(event) =>
                  void patchPolicy({
                    response_restore: event.currentTarget.checked,
                  })
                }
                type="checkbox"
              />
            </label>

            <button
              className="btn-secondary safety-streaming-demo-trigger"
              onClick={() => setStreamingDemoOpen(true)}
              type="button"
            >
              查看流式演示
            </button>

            <div className="safety-dry-run">
              <div className="safety-dry-run__title">
                <strong>试运行</strong>
                <small>用样例文本预览当前策略效果，不会转发上游</small>
              </div>
              <label className="safety-action" htmlFor="privacy-dry-run-protocol">
                <span>
                  <strong>协议</strong>
                  <small>决定样例如何包装成可检请求体</small>
                </span>
                <select
                  disabled={dryRunBusy}
                  id="privacy-dry-run-protocol"
                  onChange={(event) =>
                    setDryRunProtocol(
                      event.currentTarget.value as PrivacyDryRunProtocol,
                    )
                  }
                  value={dryRunProtocol}
                >
                  {dryRunProtocolOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label
                className="safety-dry-run__sample"
                htmlFor="privacy-dry-run-sample"
              >
                <span>
                  <strong>样例文本</strong>
                  <button
                    className="btn-secondary"
                    disabled={dryRunBusy}
                    onClick={() => setDryRunSample(dryRunSampleExample)}
                    type="button"
                  >
                    填入示例
                  </button>
                </span>
                <textarea
                  id="privacy-dry-run-sample"
                  disabled={dryRunBusy}
                  onChange={(event) => {
                    setDryRunSample(event.currentTarget.value);
                    setDryRunError(null);
                  }}
                  rows={4}
                  value={dryRunSample}
                />
                <small
                  className={
                    dryRunSampleOverLimit
                      ? "safety-dry-run__meter safety-dry-run__meter--over"
                      : "safety-dry-run__meter"
                  }
                >
                  {dryRunSampleBytes.toLocaleString()} /{" "}
                  {MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES.toLocaleString()} 字节
                  （256 KiB）
                </small>
              </label>
              <div className="safety-dry-run__actions">
                <button
                  className="btn-primary"
                  disabled={
                    dryRunBusy ||
                    saving ||
                    dryRunSample.trim() === "" ||
                    dryRunSampleOverLimit ||
                    (policy.enabled &&
                      policy.detector === "local_model" &&
                      !selectedModelReady)
                  }
                  onClick={() => {
                    void runDryRun();
                  }}
                  type="button"
                >
                  {dryRunBusy ? "试运行中…" : "试运行"}
                </button>
                {dryRunSampleOverLimit ? (
                  <small>样例过长（上限 256 KiB），请缩短后再试</small>
                ) : policy.enabled &&
                  policy.detector === "local_model" &&
                  !selectedModelReady ? (
                  <small>需要先选择已就绪的本地模型</small>
                ) : (
                  <small>使用上方当前策略配置预览</small>
                )}
              </div>
              {dryRunError !== null ? (
                <div
                  className="inline-alert inline-alert--error"
                  role="alert"
                >
                  {dryRunError}
                </div>
              ) : null}
              {dryRunResult !== null ? (
                <div
                  aria-live="polite"
                  className="safety-dry-run__result"
                >
                  <div className="safety-dry-run__decision">
                    <span>决策</span>
                    <strong>{actionLabels[dryRunResult.decision]}</strong>
                  </div>
                  <div className="safety-dry-run__summary">
                    <span>命中类别</span>
                    <strong>{summarizeDryRunFindings(dryRunResult)}</strong>
                  </div>
                  {dryRunResult.findings.length > 0 ? (
                    <div className="safety-dry-run__findings">
                      <span>通过判定（会执行策略）</span>
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">类别</th>
                            <th scope="col">位置</th>
                            <th scope="col">命中原因</th>
                          </tr>
                        </thead>
                        <tbody>
                          {dryRunResult.findings.map((finding, index) => (
                            <tr
                              key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                            >
                              <td>{dryRunKindLabel(finding.kind)}</td>
                              <td>
                                <code>{finding.path}</code>
                              </td>
                              <td>
                                {policy.detector === "regex"
                                  ? "Regex 命中（置信度门槛不适用）"
                                  : `${finding.confidence.toFixed(6)} ≥ ${policy.min_confidence.toFixed(2)}`}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  ) : null}
                  {dryRunResult.suppressed_findings.length > 0 ? (
                    <div className="safety-dry-run__findings safety-dry-run__findings--suppressed">
                      <span>低于门槛（已抑制，不执行策略）</span>
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">类别</th>
                            <th scope="col">位置</th>
                            <th scope="col">抑制原因</th>
                          </tr>
                        </thead>
                        <tbody>
                          {dryRunResult.suppressed_findings.map(
                            (finding, index) => (
                              <tr
                                key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                              >
                                <td>{dryRunKindLabel(finding.kind)}</td>
                                <td>
                                  <code>{finding.path}</code>
                                </td>
                                <td>
                                  {finding.confidence.toFixed(6)} &lt;{" "}
                                  {policy.min_confidence.toFixed(2)}
                                </td>
                              </tr>
                            ),
                          )}
                        </tbody>
                      </table>
                    </div>
                  ) : null}
                  {dryRunResult.redactions !== undefined &&
                  dryRunResult.redactions.length > 0 ? (
                    <div className="safety-dry-run__redactions">
                      <span>占位符对照（仅本地预览）</span>
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">占位符</th>
                            <th scope="col">类别</th>
                            <th scope="col">原文</th>
                          </tr>
                        </thead>
                        <tbody>
                          {dryRunResult.redactions.map((redaction) => (
                            <tr key={redaction.placeholder}>
                              <td>
                                <code>{redaction.placeholder}</code>
                              </td>
                              <td>{dryRunKindLabel(redaction.kind)}</td>
                              <td>
                                <code>{redaction.value}</code>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  ) : null}
                  {dryRunResult.redacted_body !== undefined ? (
                    <label className="safety-dry-run__body">
                      <span>脱敏后的请求体</span>
                      <pre>{prettyJSON(dryRunResult.redacted_body)}</pre>
                    </label>
                  ) : null}
                </div>
              ) : null}
            </div>

            <div className="safety-regex-note">
              <strong>Regex 覆盖限制</strong>
              <span>
                覆盖邮箱、电话、账号/银行卡、IP/URL
                与常见密钥；不识别人名、地址或上下文日期，也不支持自定义规则。
              </span>
            </div>

            <div className="safety-card__footer">
              <span>ETag 并发保护</span>
              <strong>{saving ? "保存中…" : "已与 Core 同步"}</strong>
            </div>
          </section>

          <section className="safety-card safety-card--models">
            <div className="safety-card__title">
              <div>
                <span className="eyebrow">模型库</span>
                <h3>本地隐私模型</h3>
              </div>
              <span className="model-state model-state--ready">
                {readyCount} 个就绪
              </span>
            </div>

            <div className="model-tabs" role="tablist" aria-label="模型视图">
              {(
                [
                  ["catalog", "内置"],
                  ["installed", `已安装 ${installations.length}`],
                  ["custom", "自定义"],
                ] as const
              ).map(([value, label]) => (
                <button
                  aria-selected={view === value}
                  className={view === value ? "model-tab model-tab--active" : "model-tab"}
                  key={value}
                  onClick={() => setView(value)}
                  role="tab"
                  type="button"
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="model-library">
              {view === "catalog" ? (
                <div className="catalog-list">
                  {catalog.map((model) => {
                    const variant = variantForCatalog(
                      model,
                      selectedVariants,
                    );
                    const existing =
                      variant === null
                        ? null
                        : (installations.find(
                            (installation) =>
                              installation.repo_id === model.repo_id &&
                              installation.revision === model.revision &&
                              installation.variant_id === variant.id,
                          ) ?? null);
                    return (
                      <article className="catalog-model" key={model.id}>
                        <div className="catalog-model__header">
                          <div>
                            <strong>{model.name}</strong>
                            <span>
                              {model.source === "official" ? "官方" : "社区"} ·{" "}
                              {model.license}
                            </span>
                          </div>
                          <span>{model.languages.join(" / ")}</span>
                        </div>
                        <p>{model.summary}</p>
                        <div className="catalog-model__controls">
                          <label>
                            <span>版本</span>
                            <select
                              aria-label={`${model.name} 模型版本`}
                              onChange={(event) => {
                                const variantID = event.currentTarget.value;
                                setSelectedVariants((current) => ({
                                  ...current,
                                  [model.id]: variantID,
                                }));
                                if (
                                  catalogPreparation?.catalogID === model.id
                                ) {
                                  setCatalogPreparation(null);
                                }
                              }}
                              value={variant?.id ?? ""}
                            >
                              {model.variants.map((candidate) => (
                                <option
                                  disabled={!candidate.supported}
                                  key={candidate.id}
                                  value={candidate.id}
                                >
                                  {candidate.name}
                                  {candidate.recommended ? " · 推荐" : ""}
                                  {!candidate.supported ? " · 当前不支持" : ""}
                                </option>
                              ))}
                            </select>
                          </label>
                          <div className="catalog-model__resources">
                            <span>
                              下载 {formatBytes(variant?.bytes_total ?? 0)}
                            </span>
                            <span>
                              内存{" "}
                              {formatBytes(variant?.estimated_ram_bytes ?? 0)}
                            </span>
                          </div>
                          {existing === null ? (
                            <button
                              className="btn-primary"
                              disabled={
                                variant === null ||
                                operationBusy !== null ||
                                catalogProbeBusy !== null ||
                                probing
                              }
                              onClick={() => {
                                if (variant === null) return;
                                void prepareCatalogInstallation(model, variant);
                              }}
                              type="button"
                            >
                              {catalogProbeBusy === model.id
                                ? "检查中…"
                                : "检查并安装"}
                            </button>
                          ) : (
                            <button
                              className="btn-secondary"
                              onClick={() => setView("installed")}
                              type="button"
                            >
                              查看{installationStatusLabels[existing.status]}
                            </button>
                          )}
                        </div>
                      </article>
                    );
                  })}
                  {catalog.length === 0 ? (
                    <p className="model-library__empty">目录中暂无可用模型。</p>
                  ) : null}
                </div>
              ) : null}

              {view === "installed" ? (
                <div className="installation-list">
                  {installations.map((installation) => {
                    const selected =
                      policy.local_model_id === installation.id;
                    const hasDownloadTotal = installation.bytes_total > 0;
                    const progress = hasDownloadTotal
                      ? Math.min(
                          100,
                          Math.round(
                            (installation.bytes_downloaded /
                              installation.bytes_total) *
                              100,
                          ),
                        )
                      : 0;
                    const sourceLabel =
                      installation.catalog_source === "official"
                        ? "官方目录"
                        : installation.catalog_source === "community"
                          ? "社区目录"
                          : "自定义公开仓库";
                    const licenseLabel =
                      installation.license ?? "未声明许可证";
                    const languageLabel =
                      installation.languages.length > 0
                        ? installation.languages.join(" / ")
                        : "语言未声明";
                    return (
                      <article
                        className={`installation${
                          selected ? " installation--selected" : ""
                        }`}
                        key={installation.id}
                      >
                        <div className="installation__header">
                          <div>
                            <strong>{installation.name}</strong>
                            <span>
                              {installation.variant_name} ·{" "}
                              {installation.quantization}
                            </span>
                          </div>
                          <span
                            className={`model-state model-state--${installation.status}`}
                          >
                            {selected
                              ? "策略已选择"
                              : installationStatusLabels[installation.status]}
                          </span>
                        </div>
                        <p className="installation__meta">
                          {sourceLabel} · {licenseLabel} · {languageLabel} ·{" "}
                          {installation.repo_id}
                        </p>
                        {installation.status === "downloading" ? (
                          <div className="model-progress">
                            <div className="model-progress__labels">
                              <span>
                                {hasDownloadTotal
                                  ? `${formatBytes(
                                      installation.bytes_downloaded,
                                    )} / ${formatBytes(
                                      installation.bytes_total,
                                    )}`
                                  : "正在准备下载"}
                              </span>
                              <strong>
                                {hasDownloadTotal ? `${progress}%` : "准备中"}
                              </strong>
                            </div>
                            <progress
                              aria-label={`${installation.name} 下载进度`}
                              max={installation.bytes_total || 1}
                              value={installation.bytes_downloaded}
                            />
                          </div>
                        ) : (
                          <p className="installation__meta">
                            {installation.error === null
                              ? `磁盘 ${formatBytes(
                                  installation.bytes_total,
                                )} · 预计内存 ${formatBytes(
                                  installation.estimated_ram_bytes,
                                )}`
                              : installationErrorLabels[installation.error]}
                          </p>
                        )}
                        {Object.keys(installation.label_mapping).length > 0 ? (
                          <details className="installation__mapping">
                            <summary>
                              标签映射 ·{" "}
                              {Object.keys(installation.label_mapping).length} 项
                            </summary>
                            <div>
                              {Object.entries(installation.label_mapping).map(
                                ([label, kind]) => (
                                  <span key={label}>
                                    <code>{label}</code>
                                    {" → "}
                                    {kind ?? "忽略"}
                                  </span>
                                ),
                              )}
                            </div>
                          </details>
                        ) : null}
                        <div className="installation__actions">
                          {installation.status === "ready" ? (
                            <button
                              className={
                                selected ? "btn-secondary" : "btn-primary"
                              }
                              disabled={saving || selected}
                              onClick={() =>
                                chooseInstallation(installation)
                              }
                              type="button"
                            >
                              {selected ? "当前模型" : "用于策略"}
                            </button>
                          ) : null}
                          {installation.status === "error" ? (
                            <button
                              className="btn-secondary"
                              disabled={operationBusy !== null}
                              onClick={() => {
                                const retryVariant: PrivacyModelVariant = {
                                  id: installation.variant_id,
                                  name: installation.variant_name,
                                  quantization: installation.quantization,
                                  bytes_total: installation.bytes_total,
                                  estimated_ram_bytes:
                                    installation.estimated_ram_bytes,
                                  recommended: false,
                                  supported: true,
                                  unsupported_reason: null,
                                };
                                void startInstallation(
                                  installation.id,
                                  installation.name,
                                  retryVariant,
                                  {
                                    repo_id: installation.repo_id,
                                    revision: installation.revision,
                                    variant_id: installation.variant_id,
                                    label_mapping:
                                      installation.label_mapping,
                                  },
                                );
                              }}
                              type="button"
                            >
                              重试
                            </button>
                          ) : null}
                          <button
                            className="btn-danger"
                            disabled={
                              operationBusy !== null || selected
                            }
                            onClick={() =>
                              void removeInstallation(installation)
                            }
                            type="button"
                          >
                            {operationBusy === installation.id
                              ? "处理中…"
                              : installation.status === "downloading"
                                ? "取消"
                                : "删除"}
                          </button>
                        </div>
                      </article>
                    );
                  })}
                  {installations.length === 0 ? (
                    <p className="model-library__empty">
                      尚未安装本地模型，可从“内置”或“自定义”开始。
                    </p>
                  ) : null}
                </div>
              ) : null}

              {view === "custom" ? (
                <div className="custom-model">
                  <div className="custom-model__form">
                    <label>
                      <span>Hugging Face 仓库</span>
                      <input
                        aria-label="Hugging Face 仓库"
                        disabled={probing}
                        onChange={(event) => {
                          setCustomRepoID(event.currentTarget.value);
                          setProbe(null);
                          setCustomMappingOpen(false);
                          setLabelMapping({});
                          setLabelMappingTouched([]);
                        }}
                        placeholder="组织/模型"
                        value={customRepoID}
                      />
                    </label>
                    <label>
                      <span>Revision</span>
                      <input
                        aria-label="模型 Revision"
                        disabled={probing}
                        onChange={(event) => {
                          setCustomRevision(event.currentTarget.value);
                          setProbe(null);
                          setCustomMappingOpen(false);
                          setLabelMapping({});
                          setLabelMappingTouched([]);
                        }}
                        placeholder="main、标签或 commit"
                        value={customRevision}
                      />
                    </label>
                    <button
                      className="btn-secondary"
                      disabled={
                        probing ||
                        operationBusy !== null ||
                        customRepoID.trim() === "" ||
                        customRevision.trim() === ""
                      }
                      onClick={() => void runProbe()}
                      type="button"
                    >
                      {probing ? "检查中…" : "检查兼容性"}
                    </button>
                  </div>

                  {probe !== null ? (
                    <div className="custom-model__result">
                      <div className="custom-model__summary">
                        <div>
                          <strong>{probe.name}</strong>
                          <span>
                            {probe.repo_id} ·{" "}
                            {probe.license === null
                              ? "未声明许可证"
                              : probe.license}
                          </span>
                        </div>
                        <span>{probe.languages.join(" / ")}</span>
                      </div>
                      <label className="custom-model__variant">
                        <span>本地运行版本</span>
                        <select
                          aria-label="自定义模型版本"
                          onChange={(event) =>
                            setProbeVariantID(event.currentTarget.value)
                          }
                          value={customVariant?.id ?? ""}
                        >
                          {probe.variants.map((variant) => (
                            <option
                              disabled={!variant.supported}
                              key={variant.id}
                              value={variant.id}
                            >
                              {variant.name}
                              {variant.recommended ? " · 推荐" : ""}
                              {!variant.supported ? " · 当前不支持" : ""}
                            </option>
                          ))}
                        </select>
                      </label>

                      <div className="custom-model__install">
                        <span>
                          {customVariant === null
                            ? "没有当前设备支持的版本"
                            : `下载 ${formatBytes(
                                customVariant.bytes_total,
                              )} · 预计内存 ${formatBytes(
                                customVariant.estimated_ram_bytes,
                              )}${
                                unresolvedCustomLabels.length > 0
                                  ? ` · 还有 ${unresolvedCustomLabels.length} 个标签待确认`
                                  : ""
                              }`}
                        </span>
                        <button
                          className="btn-secondary"
                          onClick={() => setCustomMappingOpen(true)}
                          type="button"
                        >
                          配置标签
                        </button>
                        <button
                          className="btn-primary"
                          disabled={
                            customVariant === null ||
                            unresolvedCustomLabels.length > 0 ||
                            operationBusy !== null
                          }
                          onClick={() => {
                            if (customVariant === null) return;
                            void startInstallation(
                              "custom",
                              probe.name,
                              customVariant,
                              {
                                repo_id: probe.repo_id,
                                revision: probe.revision,
                                variant_id: customVariant.id,
                                label_mapping: labelMapping,
                              },
                            );
                          }}
                          type="button"
                        >
                          {operationBusy === "custom"
                            ? "处理中…"
                            : "安装自定义模型"}
                        </button>
                      </div>
                    </div>
                  ) : (
                    <p className="custom-model__note">
                      仅探测元数据和兼容性，不加载仓库代码；确认版本、资源占用和标签映射后才会下载权重。
                    </p>
                  )}
                </div>
              ) : null}
            </div>

            <div className="model-trust-note">
              模型由 Core 固定 revision、校验文件并在本机运行；请求正文不会发送到模型仓库。
            </div>
          </section>
        </div>
      ) : null}
      {catalogPreparation !== null &&
      catalogPreparationModel !== null ? (
        <LabelMappingDialog
          confirmDisabled={
            unresolvedCatalogLabels.length > 0 ||
            operationBusy !== null
          }
          confirmLabel={
            operationBusy === catalogPreparation.catalogID
              ? "处理中…"
              : "确认安装"
          }
          labels={catalogPreparation.probe.labels}
          mapping={catalogPreparation.labelMapping}
          onCancel={() => setCatalogPreparation(null)}
          onChange={(label, kind) => {
            setCatalogPreparation((current) =>
              current === null
                ? null
                : {
                    ...current,
                    labelMapping: {
                      ...current.labelMapping,
                      [label]: kind,
                    },
                    touchedLabels: [
                      ...new Set([...current.touchedLabels, label]),
                    ],
                  },
            );
          }}
          onConfirm={() => {
            void startInstallation(
              catalogPreparation.catalogID,
              catalogPreparationModel.name,
              catalogPreparation.variant,
              {
                repo_id: catalogPreparation.probe.repo_id,
                revision: catalogPreparation.probe.revision,
                variant_id: catalogPreparation.variant.id,
                label_mapping: catalogPreparation.labelMapping,
              },
            );
          }}
          summary={`下载 ${formatBytes(
            catalogPreparation.variant.bytes_total,
          )} · 预计内存 ${formatBytes(
            catalogPreparation.variant.estimated_ram_bytes,
          )}${
            unresolvedCatalogLabels.length > 0
              ? ` · 还有 ${unresolvedCatalogLabels.length} 个标签待确认`
              : ""
          }`}
          title={`配置 ${catalogPreparationModel.name} 标签映射`}
          touchedLabels={catalogPreparation.touchedLabels}
        />
      ) : null}
      {customMappingOpen && probe !== null ? (
        <LabelMappingDialog
          confirmDisabled={unresolvedCustomLabels.length > 0}
          confirmLabel="应用映射"
          labels={probe.labels}
          mapping={labelMapping}
          onCancel={() => setCustomMappingOpen(false)}
          onChange={(label, kind) => {
            setLabelMapping((current) => ({
              ...current,
              [label]: kind,
            }));
            setLabelMappingTouched((current) => [
              ...new Set([...current, label]),
            ]);
          }}
          onConfirm={() => setCustomMappingOpen(false)}
          summary={`${probe.labels.length} 个基础标签${
            unresolvedCustomLabels.length > 0
              ? ` · 还有 ${unresolvedCustomLabels.length} 个标签待确认`
              : " · 映射已完整"
          }`}
          title={`配置 ${probe.name} 标签映射`}
          touchedLabels={labelMappingTouched}
        />
      ) : null}
      {pendingModelAction !== null &&
      pendingActionInstallation !== null ? (
        <ModelActionDialog
          action={pendingModelAction}
          installation={pendingActionInstallation}
          onCancel={() => setPendingModelAction(null)}
          onConfirm={confirmPendingModelAction}
        />
      ) : null}
      {pendingInstallation !== null ? (
        <InstallationResourceDialog
          onCancel={() => setPendingInstallation(null)}
          onConfirm={() => {
            const pending = pendingInstallation;
            setPendingInstallation(null);
            void performInstallation(pending);
          }}
          pending={pendingInstallation}
        />
      ) : null}
      {streamingDemoOpen ? (
        <StreamingRestoreDemoDialog
          onClose={() => setStreamingDemoOpen(false)}
        />
      ) : null}
    </div>
  );
}
