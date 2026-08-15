import { useEffect, useMemo, useRef, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

import {
  cancelPrivacyModelInstallation,
  deletePrivacyModelInstallation,
  dryRunPrivacyPolicy,
  getPrivacyModelCatalog,
  getPrivacyModelInstallation,
  getPrivacyPolicy,
  installPrivacyModel,
  listPrivacyModelInstallations,
  probeLocalPrivacyModel,
  probePrivacyModel,
  updatePrivacyPolicy,
} from "./bridge";
import {
  isResourceHeavyVariant,
  MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES,
  utf8ByteLength,
  validateLocalProbeInput,
  type CanonicalPrivacyKind,
  type PrivacyAction,
  type PrivacyCatalogModel,
  type PrivacyDetector,
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
type WorkspaceView = "policy" | "dryRun" | "models";
type ModelView = "catalog" | "installed" | "custom" | "local";
type ProbeView = Extract<ModelView, "custom" | "local">;

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

interface DryRunSamplePreset {
  id: string;
  label: string;
  description: string;
  text: string;
}

const dryRunSamplePresets: ReadonlyArray<DryRunSamplePreset> = [
  {
    id: "mixed-contact",
    label: "综合联系方式",
    description: "邮箱、国际电话和测试银行卡，适合快速检查多类别命中。",
    text: "请联系虚构用户 Alice：alice@example.com，电话 +65 6123 4567；测试卡号 4242 4242 4242 4242。",
  },
  {
    id: "account",
    label: "账号与 IBAN",
    description: "上下文账号和标准测试 IBAN，用于检查账号类识别。",
    text: "请将退款打到测试账户，account number: 12345678901；IBAN 为 GB82WEST12345698765432。",
  },
  {
    id: "network",
    label: "IP 与链接",
    description: "保留用途 IPv4 和 .example 链接，不包含真实网络目标。",
    text: "故障信息：客户端 IP 192.0.2.10，回调地址 https://private.example/callback?ticket=demo。",
  },
  {
    id: "secret",
    label: "测试密钥",
    description: "明确标记为虚构的 API Key 和密码赋值格式。",
    text: '以下均为虚构测试值：api_key=example_test_key_1234567890，password="demo_password_123456"。',
  },
  {
    id: "zh-profile",
    label: "中文个人资料",
    description: "中文姓名、地址、日期和邮箱，更适合验证本地模型。",
    text: "以下为虚构资料：李明住在上海市测试区示例路 88 号，出生日期为 1990-01-02，邮箱 liming@example.cn。",
  },
  {
    id: "en-profile",
    label: "英文个人资料",
    description: "英文姓名、地址和出生日期，更适合验证本地模型。",
    text: "Fictional profile: Alice Doe lives at 123 Example Street, Testville, and was born on January 2, 1990.",
  },
  {
    id: "mixed-language",
    label: "中英混合多实体",
    description: "姓名、日期、电话、邮箱和链接混合在同一段文本中。",
    text: "虚构客户王小明于 2025-08-01 提交 ticket，电话 +86 138 0013 8000，邮箱 wang@example.com，访问 https://support.example/ticket/42。",
  },
  {
    id: "clean",
    label: "无敏感信息",
    description: "不包含 PII 的正常文本，用于检查误报。",
    text: "请把这段公开产品说明总结成三点，并给出一个简短标题。",
  },
  {
    id: "numeric-boundary",
    label: "数字边界反例",
    description: "无效卡号、无效 IP 和普通订单号，用于检查数字误报。",
    text: "订单号 1234567890，测试卡号 4242 4242 4242 4241，地址 999.999.1.1；这些都不应按高置信度 PII 处理。",
  },
];

const defaultDryRunSample = dryRunSamplePresets[0].text;

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

function dryRunLiveSummary(result: PrivacyDryRunResult): string {
  return `试运行完成：${actionLabels[result.decision]}，${summarizeDryRunFindings(result)}。仅本地预览。`;
}

function dryRunConfidenceReason(
  confidence: number,
  minConfidence: number,
  detector: PrivacyDetector,
  mode: "hit" | "suppressed",
): string {
  if (detector === "regex" && mode === "hit") {
    return "Regex 命中（置信度门槛不适用）";
  }
  const comparison = mode === "hit" ? "≥" : "<";
  return `${confidence.toFixed(2)} ${comparison} ${minConfidence.toFixed(2)}`;
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
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-xl sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
          将模型基础标签映射到 AstrLink 的稳定隐私类别；不需要处理的标签可明确忽略。
          </DialogDescription>
        </DialogHeader>
        <div className="grid max-h-[52vh] gap-2 overflow-auto pr-1">
          {labels.map((label, index) => {
            const unresolved =
              label.suggested_kind === null &&
              !touchedLabels.includes(label.label);
            const selectID = `privacy-label-mapping-${index}`;
            return (
              <Label
                className="grid grid-cols-[minmax(0,1fr)_180px] items-center gap-3 rounded-lg border bg-muted px-3 py-2 max-[520px]:grid-cols-1"
                htmlFor={selectID}
                key={label.label}
              >
                <code className="overflow-hidden text-sm text-ellipsis whitespace-nowrap">{label.label}</code>
                <Select
                  onValueChange={(value) => {
                    onChange(
                      label.label,
                      value === "__ignore__" ? null : (value as CanonicalPrivacyKind),
                    );
                  }}
                  value={
                    unresolved
                      ? "__unresolved__"
                      : (mapping[label.label] ?? "__ignore__")
                  }
                >
                  <SelectTrigger
                    aria-label={`${label.label} 标签映射`}
                    className="w-full"
                    id={selectID}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                  {unresolved ? (
                    <SelectItem disabled value="__unresolved__">
                      请选择
                    </SelectItem>
                  ) : null}
                  <SelectItem value="__ignore__">忽略此标签</SelectItem>
                  {canonicalKindOptions.map((kind) => (
                    <SelectItem key={kind.value} value={kind.value}>
                      {kind.label}
                    </SelectItem>
                  ))}
                  </SelectContent>
                </Select>
              </Label>
            );
          })}
        </div>
        <div className="rounded-lg bg-muted px-3 py-2 text-sm text-text-secondary">{summary}</div>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel} type="button">
            取消
          </Button>
          <Button
            disabled={confirmDisabled}
            onClick={onConfirm}
            type="button"
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
  const local = installation.source === "local";
  const heavy = isResourceHeavyVariant(installation);
  const title = activating
    ? "确认使用本地模型"
    : downloading
      ? local
        ? "取消模型导入"
        : "取消模型下载"
      : "删除本地模型";
  const confirmLabel = activating
    ? "确认用于策略"
    : downloading
      ? local
        ? "确认取消导入"
        : "确认取消下载"
      : "确认删除";

  const description = (
    <>
      <p>
        {activating
            ? `将使用 ${installation.name} · ${installation.variant_name} 进行本地检测。`
            : downloading
              ? `将停止 ${installation.name} 的${local ? "导入" : "下载"}并清理临时文件。`
              : `将从本机删除 ${installation.name} · ${installation.variant_name}，再次使用时需要重新${local ? "导入" : "下载"}。`}
      </p>
      {activating ? (
        <>
          <dl className="grid grid-cols-2 gap-2.5">
            <div className="rounded-lg bg-muted p-3">
              <dt className="text-sm text-muted-foreground">磁盘占用</dt>
              <dd className="mt-1 text-sm font-medium">{formatBytes(installation.bytes_total)}</dd>
            </div>
            <div className="rounded-lg bg-muted p-3">
              <dt className="text-sm text-muted-foreground">预计内存</dt>
              <dd className="mt-1 text-sm font-medium">{formatBytes(installation.estimated_ram_bytes)}</dd>
            </div>
          </dl>
          <FormMessage tone={heavy ? "warning" : "notice"}>
            {heavy
              ? "该模型资源占用较高，性能较低的设备可能明显变慢。确认后会立即更新全局策略，并在首个受保护请求时加载模型。"
              : "确认后会立即更新全局策略，并在首个受保护请求时加载模型。"}
          </FormMessage>
        </>
      ) : null}
    </>
  );
  return (
    <ConfirmDialog
      cancelLabel="返回"
      confirmLabel={confirmLabel}
      description={description}
      destructive={!activating}
      onCancel={onCancel}
      onConfirm={onConfirm}
      open
      title={title}
    />
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
  const local = pending.key === "local";
  return (
    <ConfirmDialog
      cancelLabel="返回"
      confirmLabel="继续安装"
      description={
        <>
          <p>{pending.name} · {pending.variant.name} 资源占用较高。</p>
        <dl className="grid grid-cols-2 gap-2.5">
          <div className="rounded-lg bg-muted p-3">
            <dt className="text-sm text-muted-foreground">{local ? "导入大小" : "下载大小"}</dt>
            <dd className="mt-1 text-sm font-medium">{formatBytes(pending.variant.bytes_total)}</dd>
          </div>
          <div className="rounded-lg bg-muted p-3">
            <dt className="text-sm text-muted-foreground">预计内存</dt>
            <dd className="mt-1 text-sm font-medium">{formatBytes(pending.variant.estimated_ram_bytes)}</dd>
          </div>
        </dl>
        <FormMessage tone="warning">
          性能较低的设备可能明显变慢。
        </FormMessage>
        </>
      }
      onCancel={onCancel}
      onConfirm={onConfirm}
      open
      title="确认安装本地模型"
    />
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
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-36px)] max-w-[720px] overflow-auto sm:max-w-[720px]">
        <DialogHeader>
          <DialogTitle id="streaming-restore-demo-title">流式响应还原演示</DialogTitle>
          <DialogDescription>
          固定教学示例（OpenAI Responses · <code>stream: true</code>
          ）：请求侧脱敏与占位符还原。不对响应正文或 SSE 事件做审核扫描。
          </DialogDescription>
        </DialogHeader>
        <p className="rounded-lg bg-muted px-3 py-2.5 text-sm leading-relaxed text-text-secondary">
          示例邮箱 <code>alice@example.com</code>
          为固定示例，不代表当前策略状态。
        </p>
        <p className="rounded-lg bg-muted px-3 py-2.5 text-sm leading-relaxed text-text-secondary">
          实际请求会使用随机后缀；这里固定展示
          <code>&lt;PRIVATE_EMAIL_7f3a91c04d28be56&gt;</code>。
        </p>
        <div
          aria-label="请求脱敏与响应占位符还原数据流"
          className="grid gap-2.5 rounded-xl border bg-card p-3"
          data-streaming-demo
          data-testid="streaming-restore-demo"
          key={replayKey}
        >
          <div className="grid grid-cols-3 gap-2.5" aria-hidden="true">
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-input bg-card px-2.5 py-2 text-center [&>strong]:text-sm [&>span]:text-xs [&>span]:text-muted-foreground"
            >
              <strong>客户端</strong>
              <span>OpenAI Responses</span>
            </div>
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-primary/40 bg-accent/70 px-2.5 py-2 text-center [&>strong]:text-sm [&>span:last-child]:text-xs [&>span:last-child]:text-muted-foreground"
            >
              <span className="pointer-events-none absolute -inset-1 animate-[streaming-restore-demo-shield_12s_linear_infinite] rounded-md opacity-0" />
              <strong>AstrLink</strong>
              <span>隐私网关</span>
            </div>
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-input bg-card px-2.5 py-2 text-center [&>strong]:text-sm [&>span]:text-xs [&>span]:text-muted-foreground"
            >
              <strong>上游</strong>
              <span>SSE 传输</span>
            </div>
          </div>

          <div
            aria-hidden="true"
            className="relative z-1 -order-1 mx-1 h-10"
            data-lane="request"
          >
            <div className="absolute inset-x-[17%] top-1/2 h-0.5 -translate-y-1/2 overflow-hidden rounded-full bg-primary/20">
              <span className="absolute inset-0 animate-[streaming-restore-demo-dots-ltr_4.2s_linear_infinite] border-t-2 border-dashed border-current text-primary opacity-70" />
              <span className="absolute -top-3.5 left-1/2 -translate-x-1/2 text-xs font-medium tracking-[0.02em] whitespace-nowrap text-accent-foreground">
                请求 →
              </span>
            </div>
            <span className="pointer-events-none absolute top-1/2 left-[17%] z-3 max-w-[min(168px,42%)] -translate-1/2 animate-[streaming-restore-demo-plain_12s_linear_infinite] overflow-hidden rounded-full border border-primary/35 bg-accent px-[7px] py-1 font-mono text-xs leading-tight font-semibold text-accent-foreground text-ellipsis whitespace-nowrap shadow-sm" data-packet="plain">
              alice@example.com
            </span>
            <span className="pointer-events-none absolute top-1/2 left-[17%] z-3 max-w-[min(168px,42%)] -translate-1/2 animate-[streaming-restore-demo-redacted_12s_linear_infinite] overflow-hidden rounded-full border border-primary/35 bg-accent px-[7px] py-1 font-mono text-xs leading-tight font-semibold text-accent-foreground text-ellipsis whitespace-nowrap shadow-sm" data-packet="redacted">
              &lt;PRIVATE_EMAIL_7f3a91c04d28be56&gt;
            </span>
          </div>

          <div
            aria-hidden="true"
            className="relative z-1 order-0 mx-1 h-10"
            data-lane="response"
          >
            <div className="absolute inset-x-[17%] top-1/2 h-0.5 -translate-y-1/2 overflow-hidden rounded-full bg-success/20">
              <span className="absolute inset-0 animate-[streaming-restore-demo-dots-rtl_4.2s_linear_infinite] border-t-2 border-dashed border-current text-success opacity-70" />
              <span className="absolute -top-3.5 left-1/2 -translate-x-1/2 text-xs font-medium tracking-[0.02em] whitespace-nowrap text-success-foreground">
                ← 响应
              </span>
            </div>
            <span className="pointer-events-none absolute top-1/2 left-[17%] z-3 max-w-[min(168px,42%)] -translate-1/2 animate-[streaming-restore-demo-chunk-a_12s_linear_infinite] overflow-hidden rounded-full border border-warning/40 bg-warning-wash px-[7px] py-1 font-mono text-xs leading-tight font-semibold text-warning-foreground text-ellipsis whitespace-nowrap shadow-sm" data-packet="chunk-a">
              {
                'data: {"type":"response.output_text.delta","item_id":"item_1","content_index":0,"delta":"<PRIVATE_EMAIL_7f3a"}'
              }
            </span>
            <span className="pointer-events-none absolute top-1/2 left-[17%] z-3 max-w-[min(168px,42%)] -translate-1/2 animate-[streaming-restore-demo-chunk-b_12s_linear_infinite] overflow-hidden rounded-full border border-warning/40 bg-warning-wash px-[7px] py-1 font-mono text-xs leading-tight font-semibold text-warning-foreground text-ellipsis whitespace-nowrap shadow-sm" data-packet="chunk-b">
              {
                'data: {"type":"response.output_text.delta","item_id":"item_1","content_index":0,"delta":"91c04d28be56>"}'
              }
            </span>
            <span className="pointer-events-none absolute top-1/2 left-[17%] z-3 max-w-[min(168px,42%)] -translate-1/2 animate-[streaming-restore-demo-restored_12s_linear_infinite] overflow-hidden rounded-full border border-success/40 bg-success-wash px-[7px] py-1 font-mono text-xs leading-tight font-semibold text-success-foreground text-ellipsis whitespace-nowrap shadow-sm" data-packet="restored">
              正文: alice@example.com
            </span>
          </div>

          <ol className="mt-1 grid list-none gap-1 p-0 [&>li]:flex [&>li]:items-center [&>li]:gap-[7px] [&>li]:text-xs [&>li]:leading-snug [&>li]:text-text-secondary">
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-primary" />
              请求侧脱敏：原文 →
              <code>&lt;PRIVATE_EMAIL_7f3a91c04d28be56&gt;</code>
            </li>
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-warning" />
              上游把占位符拆到两个 SSE delta event
            </li>
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-success" />
              网关拼完整后还原给客户端
            </li>
          </ol>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => setReplayKey((current) => current + 1)}
            type="button"
          >
            重新播放
          </Button>
          <Button
            autoFocus
            onClick={onClose}
            type="button"
          >
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
  const [workspace, setWorkspace] = useState<WorkspaceView>("policy");
  const [view, setView] = useState<ModelView>("catalog");
  const [selectedVariants, setSelectedVariants] = useState<
    Record<string, string>
  >({});
  const [customRepoID, setCustomRepoID] = useState("");
  const [customRevision, setCustomRevision] = useState("main");
  const [localPath, setLocalPath] = useState("");
  const [probe, setProbe] = useState<PrivacyModelProbe | null>(null);
  const [probeView, setProbeView] = useState<ProbeView | null>(null);
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
  const [dryRunSample, setDryRunSample] = useState(defaultDryRunSample);
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
  const dryRunResultHeadingRef = useRef<HTMLHeadingElement>(null);
  const persistedMinConfidence = record?.policy.min_confidence;

  useEffect(() => {
    setMinConfidenceDraft(
      persistedMinConfidence === undefined
        ? ""
        : persistedMinConfidence.toFixed(2),
    );
  }, [persistedMinConfidence]);

  useEffect(() => {
    if (dryRunResult === null) return;
    dryRunResultHeadingRef.current?.focus();
  }, [dryRunResult]);

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
    setWorkspace("policy");
    setProbe(null);
    setProbeView(null);
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
    setProbeView(null);
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
  const selectedDryRunPreset =
    dryRunSamplePresets.find((preset) => preset.text === dryRunSample) ?? null;

  const changeDryRunSample = (sample: string) => {
    setDryRunSample(sample);
    setDryRunError(null);
    setDryRunResult(null);
  };

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
    if (!record.policy.enabled) {
      const message = "隐私保护未开启，请先开启后再试运行。";
      setDryRunResult(null);
      setDryRunError(message);
      setError(message);
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
      setWorkspace("dryRun");
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
      setNotice(
        key === "local"
          ? "本地模型导入已开始，可在“已安装”中查看进度。"
          : "模型安装已开始，可在“已安装”中查看进度。",
      );
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

  const resetProbedModel = () => {
    setProbe(null);
    setProbeView(null);
    setCustomMappingOpen(false);
    setProbeVariantID("");
    setLabelMapping({});
    setLabelMappingTouched([]);
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
    resetProbedModel();
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
      setProbeView("custom");
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

  const runLocalProbe = async () => {
    if (
      probing ||
      catalogProbeBusy !== null ||
      operationBusy !== null
    ) {
      return;
    }
    let input: ReturnType<typeof validateLocalProbeInput>;
    try {
      input = validateLocalProbeInput({ path: localPath });
    } catch (validationError) {
      setNotice(null);
      setError(
        validationError instanceof Error &&
          validationError.message.includes("not a URI")
          ? "本地导入不接收 URI；请先在系统中挂载共享目录，再填写本机路径。"
          : messageOf(
              validationError,
              "请输入已挂载到本机的模型目录或 ONNX 文件路径。",
            ),
      );
      return;
    }
    const generation = generationRef.current;
    const request = probeRequestRef.current + 1;
    probeRequestRef.current = request;
    setProbing(true);
    resetProbedModel();
    setError(null);
    setNotice(null);
    try {
      const result = await probeLocalPrivacyModel(input);
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setProbe(result);
      setProbeView("local");
      setCustomMappingOpen(result.requires_label_mapping);
      setLabelMapping(initialLabelMapping(result));
      setLabelMappingTouched([]);
      setProbeVariantID(recommendedVariant(result.variants)?.id ?? "");
      setNotice("本地模型检查完成，请确认版本、标签映射与资源占用。");
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, "无法检查本地模型路径。"));
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
      <div className="flex h-full min-h-0 w-full min-w-0 flex-col">
        <PageHeader
          description="在请求发送到上游前使用规则或所选本地模型检测敏感内容。"
          eyebrow="本地执行 · 全局策略"
          title="隐私保护"
          titleId="safety-policy-heading"
        />
        <EmptyState
          description="策略和模型均由本地 Core 保存与执行。"
          title="Core 就绪后可管理安全策略"
        />
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
  const probeVariant =
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
    <div
      className="@container flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden"
      data-testid="safety-policy"
    >
      <PageHeader
        actions={
          <div className="flex items-center gap-2.5">
            {record !== null ? (
              <span className="text-sm text-muted-foreground">
                {saving ? "保存中…" : "已与 Core 同步"}
              </span>
            ) : null}
            <Button
              disabled={
                status === "loading" ||
                saving ||
                operationBusy !== null ||
                probing ||
                catalogProbeBusy !== null
              }
              onClick={refresh}
              size="sm"
              variant="outline"
              type="button"
            >
              {status === "loading" ? "刷新中…" : "刷新"}
            </Button>
          </div>
        }
        description="在请求发送到上游前使用规则或所选本地模型检测敏感内容。"
        eyebrow="本地执行 · 全局策略"
        title="隐私保护"
        titleId="safety-policy-heading"
      />

      {error ? (
        <FormMessage className="mb-2.5 shrink-0" tone="error">{error}</FormMessage>
      ) : null}
      {notice ? (
        <FormMessage className="mb-2.5 shrink-0" tone="success">{notice}</FormMessage>
      ) : null}

      {status === "loading" && record === null ? (
        <div
          className="grid min-w-0 gap-3 rounded-lg border bg-card p-5"
          aria-label="正在读取安全策略"
        >
          <span className="h-4 w-36 animate-pulse rounded bg-muted" />
          <span className="h-20 animate-pulse rounded-lg bg-muted" />
        </div>
      ) : null}

      {status === "error" && record === null ? (
        <EmptyState
          action={
            <Button variant="outline" onClick={refresh} type="button">
              重试
            </Button>
          }
          description="检查 Core 状态后重试。"
          title="安全策略暂不可用"
        />
      ) : null}

      {status === "ready" && policy !== null ? (
        <Tabs
          className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
          onValueChange={(value) => setWorkspace(value as WorkspaceView)}
          value={workspace}
        >
          <TabsList
            aria-label="隐私保护工作区"
            className="h-9 w-full max-w-[420px] shrink-0"
          >
            <TabsTrigger
              onClick={() => setWorkspace("policy")}
              value="policy"
            >
              策略
            </TabsTrigger>
            <TabsTrigger
              aria-label="试运行结果"
              onClick={() => setWorkspace("dryRun")}
              value="dryRun"
            >
              试运行
            </TabsTrigger>
            <TabsTrigger
              onClick={() => setWorkspace("models")}
              value="models"
            >
              模型
            </TabsTrigger>
          </TabsList>

          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto"
            forceMount
            hidden={workspace !== "policy"}
            value="policy"
          >
            <div className="grid min-w-0 gap-3 pb-2">
            <Label
              className="flex items-center justify-between gap-3 font-normal"
              htmlFor="privacy-enabled"
            >
              <span className="flex min-w-0 flex-col gap-0.5">
                <strong className="text-sm font-medium">启用隐私保护</strong>
                <small className="text-sm leading-snug text-muted-foreground">
                  {cannotEnableLocalModel
                    ? "需要先选择一个已就绪的本地模型"
                    : "变更会立即保存到本地 Core"}
                </small>
              </span>
              <Switch
                aria-label="启用隐私保护"
                checked={policy.enabled}
                className="shrink-0"
                disabled={saving || cannotEnableLocalModel}
                id="privacy-enabled"
                onCheckedChange={changeEnabled}
                size="sm"
              />
            </Label>

            <fieldset className="min-w-0 border-0 p-0" disabled={saving}>
              <legend className="mb-1.5 flex items-center justify-between gap-2 px-0 text-sm font-medium text-text-secondary">
                <span>检测方式</span>
                {!selectedModelReady ? (
                  <Button
                    className="h-auto px-0 py-0 text-sm font-medium"
                    onClick={() => {
                      setWorkspace("models");
                      setView(installations.length > 0 ? "installed" : "catalog");
                    }}
                    size="sm"
                    type="button"
                    variant="link"
                  >
                    去模型库
                  </Button>
                ) : null}
              </legend>
              <RadioGroup
                className="grid min-w-0 grid-cols-2 gap-2"
                disabled={saving}
                onValueChange={(value) => {
                  if (value === "regex") {
                    useRegex();
                  } else if (selectedInstallation !== null) {
                    chooseInstallation(selectedInstallation);
                  }
                }}
                value={policy.detector}
              >
                <Label
                  className={cn(
                    "flex h-10 min-w-0 cursor-pointer items-center gap-2 rounded-md border bg-card px-2.5 font-normal transition-colors",
                    policy.detector === "regex" && "border-primary/35 bg-accent",
                  )}
                  htmlFor="privacy-detector-regex"
                >
                  <RadioGroupItem
                    aria-label="Regex"
                    className="shrink-0"
                    id="privacy-detector-regex"
                    value="regex"
                  />
                  <span className="flex min-w-0 flex-col">
                    <strong className="text-sm font-medium leading-none">Regex</strong>
                    <small className="mt-0.5 overflow-hidden text-sm leading-none text-muted-foreground text-ellipsis whitespace-nowrap">
                      快速且始终可用
                    </small>
                  </span>
                </Label>
                <Label
                  className={cn(
                    "flex h-10 min-w-0 cursor-pointer items-center gap-2 rounded-md border bg-card px-2.5 font-normal transition-colors has-[[data-disabled]]:cursor-not-allowed has-[[data-disabled]]:opacity-50",
                    policy.detector === "local_model" && "border-primary/35 bg-accent",
                  )}
                  htmlFor="privacy-detector-local-model"
                >
                  <RadioGroupItem
                    aria-label="本地模型"
                    className="shrink-0"
                    disabled={!selectedModelReady}
                    id="privacy-detector-local-model"
                    value="local_model"
                  />
                  <span className="flex min-w-0 flex-col">
                    <strong className="text-sm font-medium leading-none">本地模型</strong>
                    <small className="mt-0.5 overflow-hidden text-sm leading-none text-muted-foreground text-ellipsis whitespace-nowrap">
                      {selectedInstallation === null
                        ? "请从已安装模型中选择"
                        : `${selectedInstallation.name} · ${selectedInstallation.variant_name}`}
                    </small>
                  </span>
                </Label>
              </RadioGroup>
            </fieldset>

            <div className="grid min-w-0 gap-3 @[560px]:grid-cols-2">
            <Label
              className="grid min-w-0 gap-1.5 font-normal"
              htmlFor="privacy-min-confidence"
            >
              <span className="flex min-w-0 flex-col gap-0.5">
                <strong className="text-sm font-medium">模型最低置信度</strong>
                <small className="text-sm leading-snug text-muted-foreground">
                  低于此分数的模型候选会被抑制；Regex 不受此门槛影响
                </small>
              </span>
              <Input
                aria-label="模型最低置信度"
                className="h-9 w-full px-3 text-sm md:text-sm"
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
            </Label>

            <Label
              className="grid min-w-0 gap-1.5 font-normal"
              htmlFor="privacy-request-action"
            >
              <span className="flex min-w-0 flex-col gap-0.5">
                <strong className="text-sm font-medium">命中后的请求动作</strong>
                <small className="text-sm leading-snug text-muted-foreground">响应审核将在后续版本提供</small>
              </span>
              <Select
                disabled={saving}
                onValueChange={changeAction}
                value={policy.request_action}
              >
                <SelectTrigger
                  aria-label="命中后的请求动作"
                  className="h-9 w-full px-3 text-sm"
                  id="privacy-request-action"
                  size="sm"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {policy.request_action === "allow" ? (
                    <SelectItem disabled value="allow">允许（兼容值）</SelectItem>
                  ) : null}
                  <SelectItem value="redact">{actionLabels.redact}</SelectItem>
                  <SelectItem value="block">{actionLabels.block}</SelectItem>
                  <SelectItem value="warn">{actionLabels.warn}</SelectItem>
                </SelectContent>
              </Select>
            </Label>
            </div>

            <Label
              className="flex items-center justify-between gap-3 font-normal"
              htmlFor="privacy-response-restore"
            >
              <span className="flex min-w-0 flex-col gap-0.5">
                <strong className="text-sm font-medium">响应还原占位符</strong>
                <small className="text-sm leading-snug text-muted-foreground">
                  {policy.request_action === "redact"
                    ? "默认开启：把本请求脱敏后的占位符在模型回复中还原给客户端"
                    : "仅在请求动作为「脱敏后继续」时生效"}
                </small>
              </span>
              <Switch
                aria-label="响应还原占位符"
                checked={policy.response_restore}
                className="shrink-0"
                disabled={saving || policy.request_action !== "redact"}
                id="privacy-response-restore"
                onCheckedChange={(checked) =>
                  void patchPolicy({
                    response_restore: checked,
                  })
                }
                size="sm"
              />
            </Label>

            <p className="text-sm leading-relaxed text-muted-foreground">
              Regex 覆盖邮箱、电话、账号/银行卡、IP/URL 与常见密钥；不识别人名、地址或上下文日期。
              {" "}
              <Button
                className="h-auto px-0 py-0 text-sm font-medium"
                onClick={() => setStreamingDemoOpen(true)}
                size="sm"
                type="button"
                variant="link"
              >
                查看流式演示
              </Button>
            </p>
            </div>
          </TabsContent>

          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto"
            forceMount
            hidden={workspace !== "dryRun"}
            value="dryRun"
          >
            <div className="grid min-w-0 gap-3 pb-2">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <h3 className="text-base font-semibold tracking-tight">
                  试运行
                </h3>
                <p className="mt-1 text-sm leading-relaxed text-text-secondary">
                  用样例文本预览当前策略效果，不会转发上游
                </p>
              </div>
              <Button
                className="h-auto shrink-0 px-0 py-0 text-sm font-medium"
                onClick={() => setWorkspace("policy")}
                size="sm"
                type="button"
                variant="link"
              >
                返回策略
              </Button>
            </div>

              <Label
                className="grid min-w-0 gap-2 font-normal @[560px]:grid-cols-[minmax(0,1fr)_240px] @[560px]:items-center"
                htmlFor="privacy-dry-run-protocol"
              >
                <span className="flex min-w-0 flex-col gap-1">
                  <strong className="text-sm font-medium">协议</strong>
                  <small className="text-sm leading-snug text-muted-foreground">
                    决定样例如何包装成可检请求体
                  </small>
                </span>
                <Select
                  disabled={dryRunBusy}
                  onValueChange={(value) => {
                    setDryRunProtocol(value as PrivacyDryRunProtocol);
                    setDryRunError(null);
                    setDryRunResult(null);
                  }}
                  value={dryRunProtocol}
                >
                  <SelectTrigger
                    aria-label="试运行协议"
                    className="h-9 w-full px-3 text-sm"
                    id="privacy-dry-run-protocol"
                    size="sm"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {dryRunProtocolOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Label>
              <div className="grid gap-2">
                <div className="flex items-center justify-between gap-3">
                  <Label
                    className="items-start leading-normal font-normal"
                    htmlFor="privacy-dry-run-sample"
                  >
                    <strong className="text-sm font-medium">样例文本</strong>
                  </Label>
                  <small className="text-sm text-muted-foreground">
                    {dryRunSamplePresets.length} 组虚构测试用例
                  </small>
                </div>
                <div
                  aria-label="试运行样例"
                  className="flex flex-wrap gap-1.5"
                  role="group"
                >
                  {dryRunSamplePresets.map((preset) => {
                    const selected = selectedDryRunPreset?.id === preset.id;
                    return (
                      <Button
                        aria-pressed={selected}
                        className={cn(
                          "h-7 px-2.5 text-sm",
                          selected && "border-primary/45 bg-accent text-accent-foreground",
                        )}
                        disabled={dryRunBusy}
                        key={preset.id}
                        onClick={() => changeDryRunSample(preset.text)}
                        size="xs"
                        title={preset.description}
                        type="button"
                        variant="outline"
                      >
                        {preset.label}
                      </Button>
                    );
                  })}
                </div>
                <small className="text-sm leading-relaxed text-muted-foreground">
                  {selectedDryRunPreset?.description ??
                    "自定义样例：可以继续编辑下方文本。"}
                </small>
                <Textarea
                  aria-label="试运行样例文本"
                  className="min-h-24 text-sm leading-relaxed"
                  id="privacy-dry-run-sample"
                  disabled={dryRunBusy}
                  onChange={(event) => {
                    changeDryRunSample(event.currentTarget.value);
                  }}
                  rows={4}
                  value={dryRunSample}
                />
                <small className={cn(
                  "justify-self-end text-sm text-muted-foreground",
                  dryRunSampleOverLimit && "font-medium text-destructive",
                )}>
                  {dryRunSampleBytes.toLocaleString()} /{" "}
                  {MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES.toLocaleString()} 字节
                  （256 KiB）
                </small>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  aria-busy={dryRunBusy}
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
                  size="sm"
                  type="button"
                >
                  {dryRunBusy ? "试运行中…" : "试运行"}
                </Button>
                {dryRunSampleOverLimit ? (
                  <small className="text-sm text-destructive">样例过长（上限 256 KiB），请缩短后再试</small>
                ) : !policy.enabled ? (
                  <small className="text-sm text-muted-foreground">隐私保护未开启，请先开启后再试运行</small>
                ) : policy.enabled &&
                  policy.detector === "local_model" &&
                  !selectedModelReady ? (
                  <small className="text-sm text-muted-foreground">需要先选择已就绪的本地模型</small>
                ) : (
                  <small className="text-sm text-muted-foreground">使用「策略」中的当前配置预览</small>
                )}
              </div>
              {dryRunError !== null ? (
                <FormMessage tone="error">{dryRunError}</FormMessage>
              ) : null}

            <div className="grid gap-3 border-t pt-4">
              <h3
                className="text-base font-semibold tracking-tight outline-none"
                id="dry-run-result-heading"
                ref={dryRunResultHeadingRef}
                tabIndex={-1}
              >
                试运行结果
              </h3>
            <p aria-live="polite" className="sr-only">
              {dryRunBusy
                ? "试运行中…"
                : dryRunResult !== null
                  ? dryRunLiveSummary(dryRunResult)
                  : ""}
            </p>
              {dryRunBusy ? (
                <p className="text-sm leading-relaxed text-muted-foreground">
                  试运行中…
                </p>
              ) : dryRunResult !== null ? (
                <div
                  className="grid gap-3"
                  data-testid="safety-dry-run-result"
                >
                  <p className="text-sm text-muted-foreground">仅本地预览</p>
                  <div className="flex items-center justify-between gap-3 rounded-md bg-accent px-3 py-2 text-sm text-accent-foreground">
                    <span>决策</span>
                    <strong className="font-medium">{actionLabels[dryRunResult.decision]}</strong>
                  </div>
                  <div className="flex items-center justify-between gap-3 text-sm">
                    <span>命中类别</span>
                    <strong>{summarizeDryRunFindings(dryRunResult)}</strong>
                  </div>
                  {dryRunResult.findings.length > 0 ? (
                    <div className="grid gap-2">
                      <span className="text-sm font-medium text-success-foreground">通过判定（会执行策略）</span>
                      <ul className="grid gap-2">
                        {dryRunResult.findings.map((finding, index) => (
                          <li
                            className="rounded-lg border bg-card px-3 py-2 text-sm"
                            key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                          >
                            <strong className="font-medium">{dryRunKindLabel(finding.kind)}</strong>
                            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                              位置{" "}
                              <code className="break-all">{finding.path}</code>
                            </p>
                            <p className="mt-0.5 text-sm leading-relaxed">
                              {dryRunConfidenceReason(
                                finding.confidence,
                                policy.min_confidence,
                                policy.detector,
                                "hit",
                              )}
                            </p>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {dryRunResult.suppressed_findings.length > 0 ? (
                    <div className="grid gap-2 rounded-lg bg-warning-wash/60 p-2">
                      <span className="text-sm font-medium text-warning-foreground">低于门槛（已抑制，不执行策略）</span>
                      <ul className="grid gap-2">
                        {dryRunResult.suppressed_findings.map(
                          (finding, index) => (
                            <li
                              className="rounded-lg border bg-card px-3 py-2 text-sm"
                              key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                            >
                              <strong className="font-medium">{dryRunKindLabel(finding.kind)}</strong>
                              <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                                位置{" "}
                                <code className="break-all">{finding.path}</code>
                              </p>
                              <p className="mt-0.5 text-sm leading-relaxed">
                                {dryRunConfidenceReason(
                                  finding.confidence,
                                  policy.min_confidence,
                                  policy.detector,
                                  "suppressed",
                                )}
                              </p>
                            </li>
                          ),
                        )}
                      </ul>
                    </div>
                  ) : null}
                  {dryRunResult.redactions !== undefined &&
                  dryRunResult.redactions.length > 0 ? (
                    <div className="grid gap-2">
                      <span className="text-sm font-medium">占位符对照（仅本地预览）</span>
                      <ul className="grid gap-2">
                        {dryRunResult.redactions.map((redaction) => (
                          <li
                            className="rounded-lg border bg-card px-3 py-2 text-sm"
                            key={redaction.placeholder}
                          >
                            <code className="break-all">{redaction.placeholder}</code>
                            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                              {dryRunKindLabel(redaction.kind)}
                            </p>
                            <p className="mt-0.5 text-sm leading-relaxed">
                              原文{" "}
                              <code className="break-all">{redaction.value}</code>
                            </p>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {dryRunResult.redacted_body !== undefined ? (
                    <div className="grid gap-1.5">
                      <span className="text-sm font-medium">脱敏后的请求体</span>
                      <pre className="overflow-x-auto rounded-lg bg-foreground p-3 font-mono text-xs font-normal whitespace-pre-wrap text-background">{prettyJSON(dryRunResult.redacted_body)}</pre>
                    </div>
                  ) : null}
                </div>
              ) : (
                <p className="text-sm leading-relaxed text-muted-foreground">
                  尚未试运行。结果仅用于本地预览，不会转发上游。
                </p>
              )}
            </div>
            </div>
          </TabsContent>

          <TabsContent
            className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
            forceMount
            hidden={workspace !== "models"}
            value="models"
          >
            <div className="flex min-w-0 shrink-0 items-start justify-between gap-3">
              <div className="min-w-0">
                <h3 className="text-base font-semibold tracking-tight">
                  本地隐私模型
                </h3>
                <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                  模型由 Core 校验并在本机运行；请求正文不会发送到模型来源。
                </p>
              </div>
              <Badge
                className="shrink-0 rounded-full border-transparent bg-success-wash px-2 py-0.5 text-xs font-medium text-success-foreground"
                variant="outline"
              >
                {readyCount} 个就绪
              </Badge>
            </div>

            <Tabs
              className="mt-3 flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden"
              onValueChange={(value) => setView(value as ModelView)}
              value={view}
            >
              <TabsList
                className="grid h-auto w-full shrink-0 grid-cols-2 gap-0 rounded-md bg-muted p-[3px] @[560px]:grid-cols-4"
                aria-label="模型视图"
              >
                <TabsTrigger
                  className="h-auto rounded-sm px-2 py-2 text-sm font-medium"
                  onClick={() => setView("catalog")}
                  value="catalog"
                >
                  内置
                </TabsTrigger>
                <TabsTrigger
                  className="h-auto rounded-sm px-2 py-2 text-sm font-medium"
                  onClick={() => setView("installed")}
                  value="installed"
                >
                  已安装 {installations.length}
                </TabsTrigger>
                <TabsTrigger
                  className="h-auto rounded-sm px-2 py-2 text-sm font-medium"
                  onClick={() => setView("local")}
                  value="local"
                >
                  本地导入
                </TabsTrigger>
                <TabsTrigger
                  className="h-auto rounded-sm px-2 py-2 text-sm font-medium"
                  onClick={() => setView("custom")}
                  value="custom"
                >
                  自定义
                </TabsTrigger>
              </TabsList>
              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="catalog">
                <div className="grid gap-3">
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
                    const variantSelectID = `privacy-catalog-variant-${model.id.replace(/[^A-Za-z0-9_-]/g, "-")}`;
                    return (
                      <article className="grid min-w-0 gap-3 rounded-md border bg-muted p-3.5" key={model.id}>
                        <div className="flex min-w-0 items-start justify-between gap-2.5">
                          <div className="flex min-w-0 flex-col gap-1">
                            <strong className="min-w-0 overflow-hidden text-sm font-medium text-ellipsis">
                              {model.name}
                            </strong>
                            <span className="text-sm leading-snug text-muted-foreground">
                              {model.source === "official" ? "官方" : "社区"} ·{" "}
                              {model.license}
                            </span>
                          </div>
                          <Badge
                            className="shrink-0 px-2 py-0.5 text-xs"
                            variant="secondary"
                          >
                            {model.languages.join(" / ")}
                          </Badge>
                        </div>
                        <p className="text-sm leading-relaxed text-text-secondary">{model.summary}</p>
                        <div className="grid gap-2.5">
                          <div className="flex min-w-0 flex-wrap items-end justify-between gap-2.5">
                            <Label
                              className="grid min-w-[180px] flex-1 gap-1.5 text-sm leading-normal font-medium"
                              htmlFor={variantSelectID}
                            >
                              <span>版本</span>
                              <Select
                                onValueChange={(variantID) => {
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
                                <SelectTrigger
                                  aria-label={`${model.name} 模型版本`}
                                  className="h-9 w-full px-3 text-sm"
                                  id={variantSelectID}
                                  size="sm"
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {model.variants.map((candidate) => (
                                    <SelectItem disabled={!candidate.supported} key={candidate.id} value={candidate.id}>
                                      {candidate.name}
                                      {candidate.recommended ? " · 推荐" : ""}
                                      {!candidate.supported ? " · 当前不支持" : ""}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </Label>
                            <div className="flex flex-wrap gap-2.5 pb-1.5 text-sm text-muted-foreground">
                              <span>
                                下载 {formatBytes(variant?.bytes_total ?? 0)}
                              </span>
                              <span>
                                内存{" "}
                                {formatBytes(variant?.estimated_ram_bytes ?? 0)}
                              </span>
                            </div>
                          </div>
                          <div className="flex justify-end">
                            {existing === null ? (
                              <Button
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
                                size="sm"
                                type="button"
                              >
                                {catalogProbeBusy === model.id
                                  ? "检查中…"
                                  : "检查并安装"}
                              </Button>
                            ) : (
                              <Button
                                onClick={() => setView("installed")}
                                size="sm"
                                type="button"
                                variant="outline"
                              >
                                查看{installationStatusLabels[existing.status]}
                              </Button>
                            )}
                          </div>
                        </div>
                      </article>
                    );
                  })}
                  {catalog.length === 0 ? (
                    <p className="rounded-xl border border-dashed p-5 text-center text-xs text-muted-foreground">目录中暂无可用模型。</p>
                  ) : null}
                </div>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="installed">
                <div className="grid gap-3">
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
                      installation.source === "local"
                        ? "本地导入"
                        : installation.catalog_source === "official"
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
                        className={cn(
                          "grid gap-3 rounded-md border bg-muted p-3.5",
                          selected && "border-primary/40 bg-accent/50 ring-1 ring-primary/10",
                        )}
                        key={installation.id}
                      >
                        <div className="flex min-w-0 items-start justify-between gap-2.5">
                          <div className="min-w-0">
                            <strong className="block min-w-0 overflow-hidden text-sm font-medium text-ellipsis">
                              {installation.name}
                            </strong>
                            <span className="mt-1 block text-sm leading-snug text-muted-foreground">
                              {installation.variant_name} ·{" "}
                              {installation.quantization}
                            </span>
                          </div>
                          <Badge
                            variant="outline"
                            className={cn(
                              "bg-muted text-muted-foreground",
                              installation.status === "ready" && "border-success/25 bg-success-wash text-success-foreground",
                              installation.status === "downloading" && "border-primary/25 bg-accent text-accent-foreground",
                              installation.status === "error" && "border-destructive/25 bg-danger-wash text-danger-foreground",
                            )}
                          >
                            {selected
                              ? "策略已选择"
                              : installation.source === "local" &&
                                  installation.status === "downloading"
                                ? "导入中"
                                : installationStatusLabels[
                                    installation.status
                                  ]}
                          </Badge>
                        </div>
                        <p className="text-sm leading-relaxed text-muted-foreground">
                          {sourceLabel} · {licenseLabel} · {languageLabel} ·{" "}
                          {installation.repo_id}
                        </p>
                        {installation.status === "downloading" ? (
                          <div className="grid gap-1.5">
                            <div className="flex items-center justify-between text-sm text-muted-foreground">
                              <span>
                                {hasDownloadTotal
                                  ? `${formatBytes(
                                      installation.bytes_downloaded,
                                    )} / ${formatBytes(
                                      installation.bytes_total,
                                    )}`
                                  : installation.source === "local"
                                    ? "正在准备导入"
                                    : "正在准备下载"}
                              </span>
                              <strong>
                                {hasDownloadTotal ? `${progress}%` : "准备中"}
                              </strong>
                            </div>
                            <Progress
                              aria-label={`${installation.name} ${
                                installation.source === "local"
                                  ? "导入"
                                  : "下载"
                              }进度`}
                              value={progress}
                            />
                          </div>
                        ) : (
                          <p className="text-sm leading-relaxed text-muted-foreground">
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
                          <details className="rounded-lg border bg-card px-3 py-2.5 text-sm">
                            <summary className="cursor-pointer font-semibold">
                              标签映射 ·{" "}
                              {Object.keys(installation.label_mapping).length} 项
                            </summary>
                            <div className="mt-2 grid gap-1 text-sm text-muted-foreground">
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
                        <div className="flex flex-wrap items-center justify-end gap-2">
                          {installation.status === "ready" ? (
                            <Button
                              disabled={saving || selected}
                              onClick={() =>
                                chooseInstallation(installation)
                              }
                              size="sm"
                              type="button"
                              variant={selected ? "secondary" : "default"}
                            >
                              {selected ? "当前模型" : "用于策略"}
                            </Button>
                          ) : null}
                          {installation.status === "error" &&
                          installation.source !== "local" ? (
                            <Button
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
                              size="sm"
                              type="button"
                              variant="outline"
                            >
                              重试
                            </Button>
                          ) : null}
                          <Button
                            disabled={
                              operationBusy !== null || selected
                            }
                            onClick={() =>
                              void removeInstallation(installation)
                            }
                            size="sm"
                            type="button"
                            variant="destructive"
                          >
                            {operationBusy === installation.id
                              ? "处理中…"
                              : installation.status === "downloading"
                                ? "取消"
                                : "删除"}
                          </Button>
                        </div>
                      </article>
                    );
                  })}
                  {installations.length === 0 ? (
                    <p className="rounded-xl border border-dashed p-5 text-center text-xs text-muted-foreground">
                      尚未安装本地模型，可从“内置”“本地导入”或“自定义”开始。
                    </p>
                  ) : null}
                </div>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="local">
                <div className="grid gap-3.5 rounded-md border bg-muted p-3.5">
                  <div className="grid gap-3 @[560px]:grid-cols-[minmax(0,1fr)_auto] @[560px]:items-end">
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-local-model-path"
                    >
                      <span>已挂载的模型目录或 ONNX 文件</span>
                      <Input
                        aria-describedby="local-model-mount-note"
                        aria-label="本地模型路径"
                        autoComplete="off"
                        disabled={probing}
                        id="privacy-local-model-path"
                        maxLength={4096}
                        onChange={(event) => {
                          setLocalPath(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder="例如 /Volumes/models/privacy/model_int8.onnx"
                        spellCheck={false}
                        value={localPath}
                      />
                    </Label>
                    <Button
                      disabled={
                        probing ||
                        operationBusy !== null ||
                        localPath.trim() === ""
                      }
                      onClick={() => void runLocalProbe()}
                      type="button"
                      variant="outline"
                    >
                      {probing ? "检查中…" : "检查本地模型"}
                    </Button>
                  </div>
                  <p className="rounded-lg bg-card px-3 py-2.5 text-sm leading-relaxed text-muted-foreground" id="local-model-mount-note">
                    请先在系统中挂载网络共享，再填写本机绝对目录或 ONNX 文件路径；不接收{" "}
                    <code>smb://</code>、<code>file://</code> 或其他 URI。
                  </p>

                  {probe !== null && probeView === "local" ? (
                    <div className="grid gap-3 rounded-md border border-primary/20 bg-accent/40 p-3.5">
                      <div className="flex items-start justify-between gap-3">
                        <div>
                          <strong className="block text-sm">{probe.name}</strong>
                          <span className="mt-0.5 block text-xs text-muted-foreground">
                            已检查此路径 ·{" "}
                            {probe.license === null
                              ? "未声明许可证"
                              : probe.license}
                          </span>
                        </div>
                        <Badge variant="secondary">{probe.languages.join(" / ")}</Badge>
                      </div>
                      <Label
                        className="grid gap-1.5 text-xs font-medium"
                        htmlFor="privacy-local-model-variant"
                      >
                        <span>本地运行版本</span>
                        <Select
                          onValueChange={setProbeVariantID}
                          value={probeVariant?.id ?? ""}
                        >
                          <SelectTrigger
                            aria-label="本地模型版本"
                            className="w-full"
                            id="privacy-local-model-variant"
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {probe.variants.map((variant) => (
                              <SelectItem disabled={!variant.supported} key={variant.id} value={variant.id}>
                                {variant.name}
                                {variant.recommended ? " · 推荐" : ""}
                                {!variant.supported ? " · 当前不支持" : ""}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Label>

                      <div className="flex flex-wrap items-center justify-end gap-2">
                        <span className="mr-auto text-sm leading-relaxed text-muted-foreground">
                          {probeVariant === null
                            ? "没有当前设备支持的版本"
                            : `导入 ${formatBytes(
                                probeVariant.bytes_total,
                              )} · 预计内存 ${formatBytes(
                                probeVariant.estimated_ram_bytes,
                              )}${
                                unresolvedCustomLabels.length > 0
                                  ? ` · 还有 ${unresolvedCustomLabels.length} 个标签待确认`
                                  : ""
                              }`}
                        </span>
                        <Button
                          onClick={() => setCustomMappingOpen(true)}
                          type="button"
                          variant="outline"
                        >
                          配置标签
                        </Button>
                        <Button
                          disabled={
                            probeVariant === null ||
                            unresolvedCustomLabels.length > 0 ||
                            operationBusy !== null
                          }
                          onClick={() => {
                            if (probeVariant === null) return;
                            void startInstallation(
                              "local",
                              probe.name,
                              probeVariant,
                              {
                                repo_id: probe.repo_id,
                                revision: probe.revision,
                                variant_id: probeVariant.id,
                                label_mapping: labelMapping,
                              },
                            );
                          }}
                          type="button"
                        >
                          {operationBusy === "local"
                            ? "处理中…"
                            : "导入本地模型"}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs leading-5 text-muted-foreground">
                      指定 ONNX 文件时只检查该版本及其配置、Tokenizer 和外部数据；确认后才会导入到 AstrLink 的受管模型目录。
                    </p>
                  )}
                </div>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="custom">
                <div className="grid gap-3.5 rounded-md border bg-muted p-3.5">
                  <div className="grid gap-3 @[560px]:grid-cols-2">
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-custom-repository"
                    >
                      <span>Hugging Face 仓库</span>
                      <Input
                        aria-label="Hugging Face 仓库"
                        disabled={probing}
                        id="privacy-custom-repository"
                        onChange={(event) => {
                          setCustomRepoID(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder="组织/模型"
                        value={customRepoID}
                      />
                    </Label>
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-custom-revision"
                    >
                      <span>Revision</span>
                      <Input
                        aria-label="模型 Revision"
                        disabled={probing}
                        id="privacy-custom-revision"
                        onChange={(event) => {
                          setCustomRevision(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder="main、标签或 commit"
                        value={customRevision}
                      />
                    </Label>
                    <Button
                      className="justify-self-end"
                      disabled={
                        probing ||
                        operationBusy !== null ||
                        customRepoID.trim() === "" ||
                        customRevision.trim() === ""
                      }
                      onClick={() => void runProbe()}
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      {probing ? "检查中…" : "检查兼容性"}
                    </Button>
                  </div>

                  {probe !== null && probeView === "custom" ? (
                    <div className="grid gap-3 rounded-md border border-primary/20 bg-accent/40 p-3.5">
                      <div className="flex items-start justify-between gap-3">
                        <div>
                          <strong className="block text-sm">{probe.name}</strong>
                          <span className="mt-0.5 block text-xs text-muted-foreground">
                            {probe.repo_id} ·{" "}
                            {probe.license === null
                              ? "未声明许可证"
                              : probe.license}
                          </span>
                        </div>
                        <Badge variant="secondary">{probe.languages.join(" / ")}</Badge>
                      </div>
                      <Label
                        className="grid gap-1.5 text-xs font-medium"
                        htmlFor="privacy-custom-model-variant"
                      >
                        <span>本地运行版本</span>
                        <Select
                          onValueChange={setProbeVariantID}
                          value={probeVariant?.id ?? ""}
                        >
                          <SelectTrigger
                            aria-label="自定义模型版本"
                            className="w-full"
                            id="privacy-custom-model-variant"
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {probe.variants.map((variant) => (
                              <SelectItem disabled={!variant.supported} key={variant.id} value={variant.id}>
                                {variant.name}
                                {variant.recommended ? " · 推荐" : ""}
                                {!variant.supported ? " · 当前不支持" : ""}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Label>

                      <div className="flex flex-wrap items-center justify-end gap-2">
                        <span className="mr-auto text-sm leading-relaxed text-muted-foreground">
                          {probeVariant === null
                            ? "没有当前设备支持的版本"
                            : `下载 ${formatBytes(
                                probeVariant.bytes_total,
                              )} · 预计内存 ${formatBytes(
                                probeVariant.estimated_ram_bytes,
                              )}${
                                unresolvedCustomLabels.length > 0
                                  ? ` · 还有 ${unresolvedCustomLabels.length} 个标签待确认`
                                  : ""
                              }`}
                        </span>
                        <Button
                          onClick={() => setCustomMappingOpen(true)}
                          type="button"
                          variant="outline"
                        >
                          配置标签
                        </Button>
                        <Button
                          disabled={
                            probeVariant === null ||
                            unresolvedCustomLabels.length > 0 ||
                            operationBusy !== null
                          }
                          onClick={() => {
                            if (probeVariant === null) return;
                            void startInstallation(
                              "custom",
                              probe.name,
                              probeVariant,
                              {
                                repo_id: probe.repo_id,
                                revision: probe.revision,
                                variant_id: probeVariant.id,
                                label_mapping: labelMapping,
                              },
                            );
                          }}
                          type="button"
                        >
                          {operationBusy === "custom"
                            ? "处理中…"
                            : "安装自定义模型"}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs leading-5 text-muted-foreground">
                      仅探测元数据和兼容性，不加载仓库代码；确认版本、资源占用和标签映射后才会下载权重。
                    </p>
                  )}
                </div>
              </TabsContent>
            </Tabs>
          </TabsContent>
        </Tabs>
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
