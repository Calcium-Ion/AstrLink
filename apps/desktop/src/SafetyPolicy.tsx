import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowUpRight,
  Boxes,
  Flask as FlaskConical,
  SlidersHorizontal as ListFilter,
  LockKeyhole,
  RotateCcw,
  ScanText as ScanLine,
  SlidersHorizontal,
  type AnimatedIcon,
} from "@/components/icons";
import { Panel, PanelFooter, PanelHeader } from "@/components/Panel";
import { ChoiceCard } from "@/components/ChoiceCard";
import { Field } from "@/components/Field";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { StatusBadge } from "@/components/StatusBadge";
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
import { RadioGroup } from "@/components/ui/radio-group";
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
  getPrivacyRegexBuiltinRules,
  installPrivacyModel,
  listPrivacyModelInstallations,
  probeLocalPrivacyModel,
  probePrivacyModel,
  updatePrivacyPolicy,
} from "./bridge";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import {
  isResourceHeavyVariant,
  MAX_PRIVACY_ALLOWLIST_RULES,
  MAX_PRIVACY_ALLOWLIST_VALUE_CHARS,
  MAX_PRIVACY_CUSTOM_REGEX_RULES,
  MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES,
  MAX_PRIVACY_REGEX_PATTERN_CHARS,
  PLACEHOLDER_STYLE_LOCKED_KINDS,
  PRIVACY_KINDS,
  PRIVACY_REGEX_DETECTOR_KINDS,
  utf8ByteLength,
  validateLocalProbeInput,
  type CanonicalPrivacyKind,
  type PlaceholderStyle,
  type PrivacyAction,
  type PrivacyAllowlistRule,
  type PrivacyAllowlistType,
  type PrivacyCatalogModel,
  type PrivacyDetector,
  type PrivacyDryRunFinding,
  type PrivacyDryRunProtocol,
  type PrivacyDryRunResult,
  type PrivacyKindRule,
  type PrivacyLabelMapping,
  type PrivacyModelInstallation,
  type PrivacyModelInstallInput,
  type PrivacyModelProbe,
  type PrivacyModelVariant,
  type PrivacyPolicyPatch,
  type PrivacyPolicyRecord,
  type PrivacyRegexDetectorKind,
  type PrivacyRegexRule,
  type PrivacyRegexSource,
  type PrivacySuppressionReason,
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

function actionLabel(action: PrivacyAction): string {
  return i18n.t(`privacy.${action}`);
}

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
  text: string;
}

function dryRunSampleLabel(id: string): string {
  return i18n.t(`safety.sample.${id}.label`);
}

function dryRunSampleDescription(id: string): string {
  return i18n.t(`safety.sample.${id}.description`);
}

const dryRunSamplePresets: ReadonlyArray<DryRunSamplePreset> = [
  {
    id: "mixed-contact",
    text: "请联系虚构用户 Alice：alice@example.com，电话 +65 6123 4567；测试卡号 4242 4242 4242 4242。",
  },
  {
    id: "account",
    text: "请将退款打到测试账户，account number: 12345678901；IBAN 为 GB82WEST12345698765432。",
  },
  {
    id: "network",
    text: "故障信息：客户端 IP 192.0.2.10，回调地址 https://private.example/callback?ticket=demo。",
  },
  {
    id: "secret",
    text: '以下均为虚构测试值：api_key=example_test_key_1234567890，password="demo_password_123456"。',
  },
  {
    id: "zh-profile",
    text: "以下为虚构资料：李明住在上海市测试区示例路 88 号，出生日期为 1990-01-02，邮箱 liming@example.cn。",
  },
  {
    id: "en-profile",
    text: "Fictional profile: Alice Doe lives at 123 Example Street, Testville, and was born on January 2, 1990.",
  },
  {
    id: "mixed-language",
    text: "虚构客户王小明于 2025-08-01 提交 ticket，电话 +86 138 0013 8000，邮箱 wang@example.com，访问 https://support.example/ticket/42。",
  },
  {
    id: "clean",
    text: "请把这段公开产品说明总结成三点，并给出一个简短标题。",
  },
  {
    id: "numeric-boundary",
    text: "订单号 1234567890，测试卡号 4242 4242 4242 4241，地址 999.999.1.1；这些都不应按高置信度 PII 处理。",
  },
];

const defaultDryRunSample = dryRunSamplePresets[0].text;

function installationStatusLabel(
  status: PrivacyModelInstallation["status"],
): string {
  if (status === "error") return i18n.t("safety.unavailableStatus");
  return i18n.t(`safety.${status}`);
}

function installationErrorLabel(
  error: NonNullable<PrivacyModelInstallation["error"]>,
): string {
  switch (error) {
    case "download_failed":
      return i18n.t("safety.downloadFailed");
    case "integrity_failed":
      return i18n.t("safety.integrityFailed");
    case "incompatible_model":
      return i18n.t("safety.incompatible");
  }
}

const CANONICAL_KIND_VALUES: readonly CanonicalPrivacyKind[] = [
  "email",
  "phone",
  "account",
  "payment_card",
  "ip_address",
  "url",
  "common_secret",
  "private_address",
  "private_date",
  "private_person",
];

function canonicalKindOptions(): ReadonlyArray<{
  value: CanonicalPrivacyKind;
  label: string;
}> {
  return CANONICAL_KIND_VALUES.map((value) => ({
    value,
    label: i18n.t(`privacy.${value}`),
  }));
}

function regexKindOptions(): ReadonlyArray<{
  value: CanonicalPrivacyKind;
  label: string;
}> {
  return canonicalKindOptions().filter((option) =>
    (PRIVACY_REGEX_DETECTOR_KINDS as readonly string[]).includes(option.value),
  );
}

function canonicalKindLabel(kind: CanonicalPrivacyKind): string {
  return i18n.t(`privacy.${kind}`);
}

/** Only produced by the local model, so a Regex policy cannot hit these. */
const localModelOnlyKinds: ReadonlySet<CanonicalPrivacyKind> = new Set([
  "private_person",
  "private_address",
  "private_date",
]);

function placeholderStyleLabel(style: PlaceholderStyle): string {
  return i18n.t(`privacy.${style}`);
}

/**
 * Shown as a tooltip on rows whose style cannot be changed. The shape of these
 * placeholders is a safety property, not a preference.
 */
function placeholderStyleLockReason(
  kind: CanonicalPrivacyKind,
): string | undefined {
  switch (kind) {
    case "common_secret":
      return i18n.t("safety.secretLock");
    case "private_person":
      return i18n.t("safety.personLock");
    case "private_address":
      return i18n.t("safety.addressLock");
    case "private_date":
      return i18n.t("safety.dateLock");
    default:
      return undefined;
  }
}

const ALLOWLIST_TYPES: readonly PrivacyAllowlistType[] = [
  "literal",
  "domain_suffix",
  "cidr",
];

function allowlistTypeLabel(type: PrivacyAllowlistType): string {
  return i18n.t(`privacy.${type}`);
}

const allowlistTypePlaceholders: Record<PrivacyAllowlistType, string> = {
  literal: "ops@your-company.example",
  domain_suffix: "github.com",
  cidr: "10.0.0.0/8",
};

function defaultAllowlistRule(): PrivacyAllowlistRule {
  return { type: "domain_suffix", value: "" };
}

function defaultCustomRegexRule(): PrivacyRegexRule {
  return { kind: "email", pattern: `(?i)\\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\\.[A-Z]{2,}\\b` };
}
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
    return i18n.t("privacy.noHit");
  }
  const counts = new Map<CanonicalPrivacyKind, number>();
  for (const finding of result.findings) {
    counts.set(finding.kind, (counts.get(finding.kind) ?? 0) + 1);
  }
  return canonicalKindOptions()
    .filter((option) => counts.has(option.value))
    .map((option) => `${option.label} × ${counts.get(option.value)}`)
    .join(" · ");
}

function dryRunKindLabel(kind: CanonicalPrivacyKind): string {
  return canonicalKindLabel(kind);
}

function dryRunLiveSummary(result: PrivacyDryRunResult): string {
  return i18n.t("safety.dryRunDone", {
    action: actionLabel(result.decision),
    summary: summarizeDryRunFindings(result),
  });
}

function dryRunConfidenceReason(
  confidence: number,
  minConfidence: number,
  detector: PrivacyDetector,
  mode: "hit" | "suppressed",
): string {
  if (detector === "regex" && mode === "hit") {
    return i18n.t("safety.regexConfidenceN/A");
  }
  const comparison = mode === "hit" ? "≥" : "<";
  return `${confidence.toFixed(2)} ${comparison} ${minConfidence.toFixed(2)}`;
}

function suppressionReasonLabel(reason: PrivacySuppressionReason): string {
  switch (reason) {
    case "low_confidence":
      return i18n.t("safety.belowConfidence");
    case "kind_disabled":
      return i18n.t("safety.kindDisabled");
    case "allowlisted":
      return i18n.t("safety.allowlisted");
    case "placeholder":
      return i18n.t("safety.alreadyFake");
    case "unrepresentable":
      return i18n.t("safety.poolExhausted");
  }
}

/**
 * A suppressed finding used to mean exactly one thing—too low a confidence—so
 * the reason was derivable from the score. It now has several causes, and only
 * the low-confidence one is about the score.
 */
function dryRunSuppressionReason(
  finding: PrivacyDryRunFinding,
  minConfidence: number,
  detector: PrivacyDetector,
): string {
  const reason = finding.reason;
  if (reason === undefined || reason === "low_confidence") {
    return dryRunConfidenceReason(
      finding.confidence,
      minConfidence,
      detector,
      "suppressed",
    );
  }
  return suppressionReasonLabel(reason);
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
  const t = useT();
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-xl sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
          {t("safety.mappingHint")}
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
                    aria-label={t("safety.labelMapping", { label: label.label })}
                    className="w-full"
                    id={selectID}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                  {unresolved ? (
                    <SelectItem disabled value="__unresolved__">
                      {t("safety.pleaseSelect")}
                    </SelectItem>
                  ) : null}
                  <SelectItem value="__ignore__">{t("safety.ignoreLabel")}</SelectItem>
                  {canonicalKindOptions().map((kind) => (
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
            {t("common.cancel")}
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
  const t = useT();
  const activating = action.kind === "activate";
  const downloading = installation.status === "downloading";
  const local = installation.source === "local";
  const heavy = isResourceHeavyVariant(installation);
  const title = activating
    ? t("safety.confirmUse")
    : downloading
      ? local
        ? t("safety.cancelImport")
        : t("safety.cancelDownload")
      : t("safety.deleteModel");
  const confirmLabel = activating
    ? t("safety.confirmForPolicy")
    : downloading
      ? local
        ? t("safety.confirmCancelImport")
        : t("safety.confirmCancelDownload")
      : t("safety.confirmDelete");
  const transferAction = local ? t("safety.importAction") : t("safety.downloadAction");

  const description = (
    <>
      <p>
        {activating
            ? t("safety.useBody", {
                name: installation.name,
                variant: installation.variant_name,
              })
            : downloading
              ? t("safety.stopBody", {
                  name: installation.name,
                  action: transferAction,
                })
              : t("safety.deleteBody", {
                  name: installation.name,
                  variant: installation.variant_name,
                  action: transferAction,
                })}
      </p>
      {activating ? (
        <>
          <dl className="grid grid-cols-2 gap-2.5">
            <div className="rounded-lg bg-muted p-3">
              <dt className="text-sm text-muted-foreground">{t("safety.diskUsage")}</dt>
              <dd className="mt-1 text-sm font-medium">{formatBytes(installation.bytes_total)}</dd>
            </div>
            <div className="rounded-lg bg-muted p-3">
              <dt className="text-sm text-muted-foreground">{t("safety.estimatedRam")}</dt>
              <dd className="mt-1 text-sm font-medium">{formatBytes(installation.estimated_ram_bytes)}</dd>
            </div>
          </dl>
          <FormMessage tone={heavy ? "warning" : "notice"}>
            {heavy ? t("safety.heavyConfirm") : t("safety.normalConfirm")}
          </FormMessage>
        </>
      ) : null}
    </>
  );
  return (
    <ConfirmDialog
      cancelLabel={t("safety.back")}
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
  const t = useT();
  const local = pending.key === "local";
  return (
    <ConfirmDialog
      cancelLabel={t("safety.back")}
      confirmLabel={t("safety.continueInstall")}
      description={
        <>
          <p>{t("safety.heavyResource", {
            name: pending.name,
            variant: pending.variant.name,
          })}</p>
        <dl className="grid grid-cols-2 gap-2.5">
          <div className="rounded-lg bg-muted p-3">
            <dt className="text-sm text-muted-foreground">{local ? t("safety.importSize") : t("safety.downloadSize")}</dt>
            <dd className="mt-1 text-sm font-medium">{formatBytes(pending.variant.bytes_total)}</dd>
          </div>
          <div className="rounded-lg bg-muted p-3">
            <dt className="text-sm text-muted-foreground">{t("safety.estimatedRam")}</dt>
            <dd className="mt-1 text-sm font-medium">{formatBytes(pending.variant.estimated_ram_bytes)}</dd>
          </div>
        </dl>
        <FormMessage tone="warning">
          {t("safety.slowDevice")}
        </FormMessage>
        </>
      }
      onCancel={onCancel}
      onConfirm={onConfirm}
      open
      title={t("safety.confirmInstallTitle")}
    />
  );
}

interface StreamingRestoreDemoDialogProps {
  onClose: () => void;
}

function StreamingRestoreDemoDialog({
  onClose,
}: StreamingRestoreDemoDialogProps) {
  const t = useT();
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
          <DialogTitle id="streaming-restore-demo-title">{t("safety.demoTitle")}</DialogTitle>
          <DialogDescription>
          {t("safety.demoDescriptionLead")}<code>stream: true</code>
          {t("safety.demoDescriptionTail")}
          </DialogDescription>
        </DialogHeader>
        <p className="rounded-lg bg-muted px-3 py-2.5 text-sm leading-relaxed text-text-secondary">
          {t("safety.demoEmailLead")}<code>alice@example.com</code>
          {t("safety.demoEmailTail")}
        </p>
        <p className="rounded-lg bg-muted px-3 py-2.5 text-sm leading-relaxed text-text-secondary">
          {t("safety.demoTokenNote")}
          <code>&lt;PRIVATE_EMAIL_7f3a91c04d28be56&gt;</code>{t("safety.demoTokenNoteEnd")}
        </p>
        <div
          aria-label={t("safety.flowTitle")}
          className="grid gap-2.5 rounded-xl border bg-card p-3"
          data-streaming-demo
          data-testid="streaming-restore-demo"
          key={replayKey}
        >
          <div className="grid grid-cols-3 gap-2.5" aria-hidden="true">
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-input bg-card px-2.5 py-2 text-center [&>strong]:text-sm [&>span]:text-xs [&>span]:text-muted-foreground"
            >
              <strong>{t("safety.demoClient")}</strong>
              <span>OpenAI Responses</span>
            </div>
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-primary/40 bg-accent/70 px-2.5 py-2 text-center [&>strong]:text-sm [&>span:last-child]:text-xs [&>span:last-child]:text-muted-foreground"
            >
              <span className="pointer-events-none absolute -inset-1 animate-[streaming-restore-demo-shield_12s_linear_infinite] rounded-md opacity-0" />
              <strong>AstrLink</strong>
              <span>{t("safety.demoGateway")}</span>
            </div>
            <div
              className="relative z-2 flex min-h-[58px] flex-col items-center justify-center gap-0.5 rounded-md border border-input bg-card px-2.5 py-2 text-center [&>strong]:text-sm [&>span]:text-xs [&>span]:text-muted-foreground"
            >
              <strong>{t("safety.demoUpstream")}</strong>
              <span>{t("safety.demoSse")}</span>
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
                {t("safety.demoRequest")}
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
                {t("safety.demoResponse")}
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
              {t("safety.demoBody", { email: "alice@example.com" })}
            </span>
          </div>

          <ol className="mt-1 grid list-none gap-1 p-0 [&>li]:flex [&>li]:items-center [&>li]:gap-[7px] [&>li]:text-xs [&>li]:leading-snug [&>li]:text-text-secondary">
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-primary" />
              {t("safety.demoStepRedact")}
              <code>&lt;PRIVATE_EMAIL_7f3a91c04d28be56&gt;</code>
            </li>
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-warning" />
              {t("safety.demoStepSplit")}
            </li>
            <li>
              <span className="size-[9px] shrink-0 rounded-full bg-success" />
              {t("safety.demoStepRestore")}
            </li>
          </ol>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => setReplayKey((current) => current + 1)}
            type="button"
          >
            {t("safety.replay")}
          </Button>
          <Button
            autoFocus
            onClick={onClose}
            type="button"
          >
            {t("common.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PolicySection({
  title,
  icon: Icon,
  actions,
  description,
  children,
}: {
  title: string;
  icon: AnimatedIcon;
  actions?: ReactNode;
  description?: string;
  children: ReactNode;
}) {
  return (
    <Panel className="@container">
      <PanelHeader actions={actions} className="items-center">
        <h2 className="flex items-center gap-2 text-sm font-semibold">
          <Icon aria-hidden="true" className="size-4 shrink-0 text-primary" />
          {title}
        </h2>
        {description ? (
          <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
            {description}
          </p>
        ) : null}
      </PanelHeader>
      <div className="grid min-w-0 gap-4 p-4">{children}</div>
    </Panel>
  );
}

export function SafetyPolicy({
  coreSessionKey,
  isReady,
}: SafetyPolicyProps) {
  const t = useT();
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
  const [confirmFillBuiltinRules, setConfirmFillBuiltinRules] = useState(false);
  const [regexPatternDrafts, setRegexPatternDrafts] = useState<string[]>([]);
  const [allowlistDrafts, setAllowlistDrafts] = useState<string[]>([]);
  // A new allowlist row is held locally until it has a value, because an empty
  // value would be rejected by the contract.
  const [allowlistPending, setAllowlistPending] = useState(false);
  const [allowlistPendingType, setAllowlistPendingType] =
    useState<PrivacyAllowlistType>(defaultAllowlistRule().type);
  const [fillingBuiltinRules, setFillingBuiltinRules] = useState(false);
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
  const persistedCustomRegexRules = record?.policy.custom_regex_rules;
  const persistedAllowlistRules = record?.policy.allowlist_rules;

  useEffect(() => {
    setMinConfidenceDraft(
      persistedMinConfidence === undefined
        ? ""
        : persistedMinConfidence.toFixed(2),
    );
  }, [persistedMinConfidence]);

  useEffect(() => {
    setRegexPatternDrafts(
      persistedCustomRegexRules === undefined
        ? []
        : persistedCustomRegexRules.map((rule) => rule.pattern),
    );
  }, [persistedCustomRegexRules]);

  useEffect(() => {
    setAllowlistDrafts(
      persistedAllowlistRules === undefined
        ? []
        : persistedAllowlistRules.map((rule) => rule.value),
    );
    setAllowlistPending(false);
  }, [persistedAllowlistRules]);

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
      setError(messageOf(loadError, t("safety.readFailed")));
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
        setError(messageOf(pollError, t("safety.progressFailed")));
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
    setProbe(null);
    setProbeView(null);
    setCustomMappingOpen(false);
    setCatalogPreparation(null);
    setPendingModelAction(null);
    void load(generation);
  };

  const patchPolicy = async (
    patch: PrivacyPolicyPatch,
    successNotice = i18n.t("safety.saved"),
  ): Promise<boolean> => {
    if (record === null || saving || status !== "ready") return false;
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
    setDryRunResult(null);
    setDryRunError(null);

    try {
      const next = await updatePrivacyPolicy(record.etag, patch);
      if (generationRef.current !== generation) return false;
      setRecord(next);
      notify.success(successNotice);
      return true;
    } catch (patchError) {
      if (generationRef.current !== generation) return false;
      let authoritative = previous;
      let reconciled = false;
      try {
        authoritative = await getPrivacyPolicy();
        reconciled = true;
      } catch {
        // Keep the last known-good record when Core cannot be queried.
      }
      if (generationRef.current !== generation) return false;
      setRecord(authoritative);
      const failure = messageOf(patchError, t("safety.saveFailed"));
      setError(
        reconciled
          ? t("safety.saveFailedReread", { failure })
          : t("safety.saveFailedRestore", { failure }),
      );
      return false;
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
      setError(t("safety.needModel"));
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

  const changeRegexSource = (source: PrivacyRegexSource) => {
    if (record === null || saving) return;
    if (source === "custom" && record.policy.custom_regex_rules.length === 0) {
      void (async () => {
        setFillingBuiltinRules(true);
        setError(null);
        try {
          const catalog = await getPrivacyRegexBuiltinRules();
          await patchPolicy({
            regex_source: "custom",
            custom_regex_rules: catalog.rules,
          });
        } catch (fillError) {
          setError(messageOf(fillError, t("safety.switchCustomFailed")));
        } finally {
          setFillingBuiltinRules(false);
        }
      })();
      return;
    }
    void patchPolicy({ regex_source: source });
  };

  const saveCustomRegexRules = (rules: PrivacyRegexRule[]) => {
    void patchPolicy({ custom_regex_rules: rules });
  };

  const addCustomRegexRule = () => {
    if (record === null) return;
    if (record.policy.custom_regex_rules.length >= MAX_PRIVACY_CUSTOM_REGEX_RULES) {
      setError(t("safety.tooManyCustom", { max: MAX_PRIVACY_CUSTOM_REGEX_RULES }));
      return;
    }
    saveCustomRegexRules([
      ...record.policy.custom_regex_rules,
      defaultCustomRegexRule(),
    ]);
  };

  const removeCustomRegexRule = (index: number) => {
    if (record === null) return;
    const next = record.policy.custom_regex_rules.filter((_, i) => i !== index);
    if (record.policy.regex_source === "custom" && next.length === 0) {
      setError(t("safety.needOneCustom"));
      return;
    }
    saveCustomRegexRules(next);
  };

  const changeCustomRegexKind = (
    index: number,
    kind: PrivacyRegexDetectorKind,
  ) => {
    if (record === null) return;
    const next = record.policy.custom_regex_rules.map((rule, i) =>
      i === index ? { ...rule, kind } : rule,
    );
    saveCustomRegexRules(next);
  };

  const commitCustomRegexPattern = (index: number) => {
    if (record === null) return;
    const draft = regexPatternDrafts[index] ?? "";
    const length = [...draft].length;
    if (length < 1 || length > MAX_PRIVACY_REGEX_PATTERN_CHARS) {
      setError(
        t("safety.regexLength", { max: MAX_PRIVACY_REGEX_PATTERN_CHARS }),
      );
      setRegexPatternDrafts(
        record.policy.custom_regex_rules.map((rule) => rule.pattern),
      );
      return;
    }
    if (draft === record.policy.custom_regex_rules[index]?.pattern) return;
    const next = record.policy.custom_regex_rules.map((rule, i) =>
      i === index ? { ...rule, pattern: draft } : rule,
    );
    saveCustomRegexRules(next);
  };

  const kindRuleFor = (kind: CanonicalPrivacyKind): PrivacyKindRule =>
    record?.policy.kind_rules.find((rule) => rule.kind === kind) ?? {
      kind,
      enabled: false,
      style: "token",
    };

  const saveKindRule = (kind: CanonicalPrivacyKind, patch: Partial<PrivacyKindRule>) => {
    if (record === null) return;
    // The whole list is sent because kind_rules is replaced, not merged.
    const next = PRIVACY_KINDS.map((candidate) => {
      const rule = kindRuleFor(candidate);
      return candidate === kind ? { ...rule, ...patch } : rule;
    });
    void patchPolicy({ kind_rules: next });
  };

  const saveAllowlistRules = (rules: PrivacyAllowlistRule[]) => {
    void patchPolicy({ allowlist_rules: rules });
  };

  const addAllowlistRule = () => {
    if (record === null) return;
    if (record.policy.allowlist_rules.length >= MAX_PRIVACY_ALLOWLIST_RULES) {
      setError(t("safety.tooManyAllowlist", { max: MAX_PRIVACY_ALLOWLIST_RULES }));
      return;
    }
    setAllowlistDrafts((current) => [...current, ""]);
    setAllowlistPending(true);
  };

  const removeAllowlistRule = (index: number) => {
    if (record === null) return;
    if (allowlistPending && index === record.policy.allowlist_rules.length) {
      setAllowlistPending(false);
      setAllowlistDrafts(record.policy.allowlist_rules.map((rule) => rule.value));
      return;
    }
    saveAllowlistRules(
      record.policy.allowlist_rules.filter((_, position) => position !== index),
    );
  };

  const changeAllowlistType = (index: number, type: PrivacyAllowlistType) => {
    if (record === null) return;
    if (allowlistPending && index === record.policy.allowlist_rules.length) {
      setAllowlistPendingType(type);
      return;
    }
    saveAllowlistRules(
      record.policy.allowlist_rules.map((rule, position) =>
        position === index ? { ...rule, type } : rule,
      ),
    );
  };

  const commitAllowlistValue = (index: number) => {
    if (record === null) return;
    const rules = record.policy.allowlist_rules;
    const draft = (allowlistDrafts[index] ?? "").trim();
    const isNew = allowlistPending && index === rules.length;
    if (draft.length === 0) {
      if (isNew) return;
      setAllowlistDrafts(rules.map((rule) => rule.value));
      return;
    }
    if ([...draft].length > MAX_PRIVACY_ALLOWLIST_VALUE_CHARS) {
      setError(t("safety.allowlistLength", { max: MAX_PRIVACY_ALLOWLIST_VALUE_CHARS }));
      return;
    }
    if (isNew) {
      setAllowlistPending(false);
      saveAllowlistRules([...rules, { type: allowlistPendingType, value: draft }]);
      return;
    }
    if (draft === rules[index]?.value) return;
    saveAllowlistRules(
      rules.map((rule, position) =>
        position === index ? { ...rule, value: draft } : rule,
      ),
    );
  };

  const fillBuiltinRules = async () => {
    if (record === null || fillingBuiltinRules) return;
    setConfirmFillBuiltinRules(false);
    setFillingBuiltinRules(true);
    setError(null);
    try {
      const catalog = await getPrivacyRegexBuiltinRules();
      await patchPolicy(
        {
          regex_source: "custom",
          custom_regex_rules: catalog.rules,
        },
        t("safety.filledBuiltin"),
      );
    } catch (fillError) {
      setError(messageOf(fillError, t("safety.fillBuiltinFailed")));
    } finally {
      setFillingBuiltinRules(false);
    }
  };

  const chooseInstallation = (installation: PrivacyModelInstallation) => {
    if (installation.status !== "ready") {
      setError(t("safety.modelNotReady"));
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
      setError(t("safety.confidenceRange"));
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
      const message = t("safety.dryRunOff");
      setDryRunResult(null);
      setDryRunError(message);
      setError(message);
      return;
    }
    const sample = dryRunSample.trim();
    if (sample === "") {
      setDryRunError(t("privacy.sampleRequired"));
      setError(t("privacy.sampleRequired"));
      return;
    }
    if (utf8ByteLength(sample) > MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES) {
      const message = t("safety.sampleTooLongKib", {
        kib: MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES / 1024,
      });
      setDryRunError(message);
      setError(message);
      return;
    }
    if (
      record.policy.enabled &&
      record.policy.detector === "local_model" &&
      !selectedModelReady
    ) {
      setDryRunError(t("safety.dryRunModelNotReady"));
      setError(t("safety.dryRunModelNotReady"));
      return;
    }
    const generation = generationRef.current;
    const request = dryRunRequestRef.current + 1;
    dryRunRequestRef.current = request;
    setDryRunBusy(true);
    setDryRunError(null);
    setError(null);
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
      const message = messageOf(caught, t("safety.dryRunFailed"));
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
      notify.success(
        key === "local"
          ? t("safety.importStarted")
          : t("safety.installStarted"),
      );
    } catch (installError) {
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(installError, t("safety.installFailed")));
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
        throw new Error(t("safety.probeMismatch"));
      }
      const probedVariant = result.variants.find(
        (candidate) =>
          candidate.id === variant.id && candidate.supported,
      );
      if (probedVariant === undefined) {
        throw new Error(t("safety.variantIncompatible"));
      }
      setCatalogPreparation({
        catalogID: model.id,
        probe: result,
        variant: probedVariant,
        labelMapping: initialLabelMapping(result),
        touchedLabels: [],
      });
      notify.success(t("safety.probeReady"));
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, t("safety.probeFailed")));
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
      setError(t("safety.inUseSwitch"));
      return;
    }
    setError(null);
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
      setError(t("safety.statusChanged"));
      return;
    }
    if (pendingModelAction.kind === "activate") {
      if (installation.status !== "ready") {
        setPendingModelAction(null);
        setError(t("safety.modelNotReady"));
        return;
      }
      const patch = pendingModelAction.patch;
      setPendingModelAction(null);
      void patchPolicy(patch);
      return;
    }
    if (record?.policy.local_model_id === installation.id) {
      setPendingModelAction(null);
      setError(t("safety.inUseSwitch"));
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
      notify.success(downloading ? t("safety.downloadCancelled") : t("safety.modelDeleted"));
    } catch (removeError) {
      if (
        generationRef.current !== generation ||
        operationRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(removeError, t("safety.cancelDeleteFailed")));
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
        throw new Error(t("safety.customProbeMismatch"));
      }
      setProbe(result);
      setProbeView("custom");
      setCustomMappingOpen(result.requires_label_mapping);
      setLabelMapping(initialLabelMapping(result));
      setLabelMappingTouched([]);
      setProbeVariantID(recommendedVariant(result.variants)?.id ?? "");
      notify.success(t("safety.customProbed"));
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, t("safety.customProbeFailed")));
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
      setError(
        validationError instanceof Error &&
          validationError.message.includes("not a URI")
          ? t("safety.localNoUri")
          : messageOf(
              validationError,
              t("safety.localPathRequired"),
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
      notify.success(t("safety.localProbed"));
    } catch (probeError) {
      if (
        generationRef.current !== generation ||
        probeRequestRef.current !== request
      ) {
        return;
      }
      setError(messageOf(probeError, t("safety.localProbeFailed")));
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
          description={t("safety.description")}
          title={t("safety.title")}
          titleId="safety-policy-heading"
        />
        <EmptyState
          description={t("safety.readyHint")}
          title={t("safety.waiting")}
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
        className="@max-[520px]:flex-col @max-[520px]:items-start @max-[520px]:gap-3"
        actions={
          <>
            {record !== null && saving ? (
              <span className="text-xs text-muted-foreground">
                {t("common.saving")}
              </span>
            ) : null}
            {policy !== null ? (
              <>
                <Label className="inline-flex cursor-pointer items-center gap-2.5 border-r pr-3 text-xs font-medium">
                  <span>{t("safety.enable")}</span>
                  <Switch
                    aria-label={t("safety.enable")}
                    checked={policy.enabled}
                    disabled={saving || cannotEnableLocalModel}
                    id="privacy-enabled"
                    onCheckedChange={changeEnabled}
                    size="sm"
                    title={
                      cannotEnableLocalModel
                        ? t("safety.needReadyModel")
                        : undefined
                    }
                  />
                </Label>
              </>
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
              {status === "loading"
                ? t("common.refreshing")
                : t("common.refresh")}
            </Button>
          </>
        }
        description={t("safety.description")}
        title={t("safety.title")}
        titleId="safety-policy-heading"
      />

      {error ? (
        <FormMessage className="mb-2.5 shrink-0" tone="error">
          {error}
        </FormMessage>
      ) : null}

      {status === "loading" && record === null ? (
        <div
          className="grid min-w-0 gap-3 rounded-lg border bg-card p-5"
          aria-label={t("safety.loading")}
        >
          <span className="h-4 w-36 animate-pulse rounded bg-muted" />
          <span className="h-20 animate-pulse rounded-lg bg-muted" />
        </div>
      ) : null}

      {status === "error" && record === null ? (
        <EmptyState
          action={
            <Button variant="outline" onClick={refresh} type="button">
              {t("safety.retry")}
            </Button>
          }
          description={t("safety.unavailableHint")}
          title={t("safety.unavailable")}
        />
      ) : null}

      {status === "ready" && policy !== null ? (
        <Tabs
          className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
          onValueChange={(value) => setWorkspace(value as WorkspaceView)}
          value={workspace}
        >
          <TabsList
            aria-label={t("safety.workspace")}
            className="h-9 w-fit shrink-0"
          >
            <TabsTrigger onClick={() => setWorkspace("policy")} value="policy">
              <SlidersHorizontal aria-hidden="true" />
              {t("safety.tabPolicy")}
            </TabsTrigger>
            <TabsTrigger
              aria-label={t("safety.dryRunResult")}
              onClick={() => setWorkspace("dryRun")}
              value="dryRun"
            >
              <FlaskConical aria-hidden="true" />
              {t("safety.run")}
            </TabsTrigger>
            <TabsTrigger onClick={() => setWorkspace("models")} value="models">
              <Boxes aria-hidden="true" />
              {t("safety.tabModels")}
            </TabsTrigger>
          </TabsList>

          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto"
            forceMount
            hidden={workspace !== "policy"}
            value="policy"
          >
            <div className="mx-auto grid w-full min-w-0 max-w-6xl items-start gap-4 pb-4 pr-1 @[760px]:grid-cols-[minmax(280px,0.85fr)_minmax(0,1.3fr)]">
              <div className="grid min-w-0 gap-4">
                <PolicySection
                  title={t("safety.detector")}
                  icon={ScanLine}
                  actions={
                    !selectedModelReady ? (
                      <Button
                        className="h-auto gap-1 px-0 py-0 text-xs font-medium"
                        onClick={() => {
                          setWorkspace("models");
                          setView(
                            installations.length > 0 ? "installed" : "catalog",
                          );
                        }}
                        size="sm"
                        type="button"
                        variant="link"
                      >
                        {t("safety.goToModels")}
                        <ArrowUpRight aria-hidden="true" className="size-3.5" />
                      </Button>
                    ) : null
                  }
                >
                  <fieldset className="min-w-0 border-0 p-0" disabled={saving}>
                    <legend className="sr-only">{t("safety.detector")}</legend>
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
                      <ChoiceCard
                        className="items-center"
                        id="privacy-detector-regex"
                        label="Regex"
                        description={t("safety.regexAlways")}
                        selected={policy.detector === "regex"}
                        disabled={saving}
                        value="regex"
                      />
                      <ChoiceCard
                        className="items-center"
                        id="privacy-detector-local-model"
                        label={t("safety.localModels")}
                        description={
                          selectedInstallation === null
                            ? t("safety.chooseInstalled")
                            : `${selectedInstallation.name} · ${selectedInstallation.variant_name}`
                        }
                        selected={policy.detector === "local_model"}
                        disabled={saving || !selectedModelReady}
                        value="local_model"
                      />
                    </RadioGroup>
                  </fieldset>

                  {policy.detector === "regex" ? (
                    <fieldset
                      className="min-w-0 border-0 p-0"
                      disabled={saving || fillingBuiltinRules}
                    >
                      <legend className="mb-2 px-0 text-xs font-medium text-text-secondary">
                        {t("safety.regexSource")}
                      </legend>
                      <RadioGroup
                        className="grid min-w-0 grid-cols-2 gap-2"
                        disabled={saving || fillingBuiltinRules}
                        onValueChange={(value) => {
                          if (value === "builtin" || value === "custom") {
                            changeRegexSource(value);
                          }
                        }}
                        value={policy.regex_source}
                      >
                        <ChoiceCard
                          className="items-center"
                          id="privacy-regex-source-builtin"
                          label={t("safety.builtinRules")}
                          description={t("safety.builtinFixed")}
                          selected={policy.regex_source === "builtin"}
                          disabled={saving || fillingBuiltinRules}
                          value="builtin"
                        />
                        <ChoiceCard
                          className="items-center"
                          id="privacy-regex-source-custom"
                          label={t("safety.customRules")}
                          description={t("safety.customListOnly")}
                          selected={policy.regex_source === "custom"}
                          disabled={saving || fillingBuiltinRules}
                          value="custom"
                        />
                      </RadioGroup>

                      {policy.regex_source === "builtin" ? (
                        <div className="mt-3">
                          <HelpDisclosure title={t("safety.ruleCoverage")}>
                            <p className="text-xs leading-relaxed">
                              {t("safety.builtinCoverage", {
                                kinds: regexKindOptions()
                                  .map((option) => option.label)
                                  .join(t("safety.listJoin")),
                              })}
                            </p>
                            <p>{t("safety.builtinHint")}</p>
                          </HelpDisclosure>
                        </div>
                      ) : (
                        <div className="mt-3 grid min-w-0 gap-2">
                          <div className="flex flex-wrap items-center gap-2">
                            <Button
                              disabled={saving || fillingBuiltinRules}
                              onClick={addCustomRegexRule}
                              size="sm"
                              type="button"
                              variant="outline"
                            >
                              {t("safety.addRule")}
                            </Button>
                            <Button
                              disabled={saving || fillingBuiltinRules}
                              onClick={() => {
                                if (policy.custom_regex_rules.length > 0) {
                                  setConfirmFillBuiltinRules(true);
                                } else {
                                  void fillBuiltinRules();
                                }
                              }}
                              size="sm"
                              type="button"
                              variant="outline"
                            >
                              {t("safety.fillBuiltin")}
                            </Button>
                            <span className="text-sm text-muted-foreground">
                              {policy.custom_regex_rules.length}/
                              {MAX_PRIVACY_CUSTOM_REGEX_RULES}
                            </span>
                          </div>
                          {policy.custom_regex_rules.length === 0 ? (
                            <p className="text-sm text-muted-foreground">
                              {t("safety.noCustomRules")}
                            </p>
                          ) : (
                            <ul className="grid min-w-0 gap-2">
                              {policy.custom_regex_rules.map((rule, index) => (
                                <li
                                  className="grid min-w-0 gap-2 rounded-md border bg-card p-2.5 @[640px]:grid-cols-[8.5rem_minmax(0,1fr)_auto] @[640px]:items-start"
                                  key={`regex-rule-${index}`}
                                >
                                  <Select
                                    disabled={saving || fillingBuiltinRules}
                                    onValueChange={(value) =>
                                      changeCustomRegexKind(
                                        index,
                                        value as PrivacyRegexDetectorKind,
                                      )
                                    }
                                    value={rule.kind}
                                  >
                                    <SelectTrigger
                                      aria-label={t("safety.ruleKind", {
                                        index: index + 1,
                                      })}
                                      className="h-9 w-full px-3 text-sm"
                                      size="sm"
                                    >
                                      <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                      {regexKindOptions().map((option) => (
                                        <SelectItem
                                          key={option.value}
                                          value={option.value}
                                        >
                                          {option.label}
                                        </SelectItem>
                                      ))}
                                    </SelectContent>
                                  </Select>
                                  <Input
                                    aria-label={t("safety.rulePattern", {
                                      index: index + 1,
                                    })}
                                    className="h-9 min-w-0 font-mono text-sm md:text-sm"
                                    disabled={saving || fillingBuiltinRules}
                                    onBlur={() =>
                                      commitCustomRegexPattern(index)
                                    }
                                    onChange={(event) => {
                                      const value = event.currentTarget.value;
                                      setRegexPatternDrafts((current) => {
                                        const next = [...current];
                                        next[index] = value;
                                        return next;
                                      });
                                    }}
                                    onKeyDown={(event) => {
                                      if (event.key === "Enter") {
                                        event.currentTarget.blur();
                                      } else if (event.key === "Escape") {
                                        event.preventDefault();
                                        setRegexPatternDrafts(
                                          policy.custom_regex_rules.map(
                                            (item) => item.pattern,
                                          ),
                                        );
                                      }
                                    }}
                                    placeholder={t("safety.re2Hint")}
                                    value={
                                      regexPatternDrafts[index] ?? rule.pattern
                                    }
                                  />
                                  <Button
                                    disabled={saving || fillingBuiltinRules}
                                    onClick={() => removeCustomRegexRule(index)}
                                    size="sm"
                                    type="button"
                                    variant="ghost"
                                  >
                                    {t("common.delete")}
                                  </Button>
                                </li>
                              ))}
                            </ul>
                          )}
                        </div>
                      )}
                    </fieldset>
                  ) : null}

                  <div className="border-t pt-4">
                    <Field
                      htmlFor="privacy-request-action"
                      label={t("safety.requestAction")}
                    >
                      <Select
                        disabled={saving}
                        onValueChange={changeAction}
                        value={policy.request_action}
                      >
                        <SelectTrigger
                          aria-label={t("safety.requestAction")}
                          className="h-9 w-full px-3 text-sm"
                          id="privacy-request-action"
                          size="sm"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {policy.request_action === "allow" ? (
                            <SelectItem disabled value="allow">
                              {t("safety.allowCompat")}
                            </SelectItem>
                          ) : null}
                          <SelectItem value="redact">
                            {actionLabel("redact")}
                          </SelectItem>
                          <SelectItem value="block">
                            {actionLabel("block")}
                          </SelectItem>
                          <SelectItem value="warn">
                            {actionLabel("warn")}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                    </Field>
                  </div>
                  <div className="border-t pt-3">
                    <HelpDisclosure
                      title={t("safety.advancedDetection")}
                      open={policy.detector === "local_model"}
                    >
                      <Field
                        htmlFor="privacy-min-confidence"
                        label={t("safety.minConfidence")}
                        hint={t("safety.minConfidenceHint")}
                      >
                        <Input
                          aria-label={t("safety.minConfidence")}
                          className="h-9 w-28 px-3 text-sm md:text-sm"
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
                              setMinConfidenceDraft(
                                policy.min_confidence.toFixed(2),
                              );
                            }
                          }}
                          step="0.01"
                          type="number"
                          value={minConfidenceDraft}
                        />
                      </Field>
                    </HelpDisclosure>
                  </div>
                </PolicySection>

                <PolicySection
                  title={t("safety.responseHandling")}
                  icon={RotateCcw}
                >
                  <fieldset className="min-w-0 border-0 p-0" disabled={saving}>
                    <legend className="sr-only">
                      {t("safety.restoreScope")}
                    </legend>
                    <div className="grid min-w-0 divide-y">
                      <Label className="flex min-w-0 cursor-pointer items-center justify-between gap-3 pb-3 font-normal">
                        <span className="flex min-w-0 flex-col gap-1">
                          <strong className="text-sm font-medium">
                            {t("safety.restore")}
                          </strong>
                          <small className="text-xs leading-relaxed text-muted-foreground">
                            {t("safety.responseRestoreDetail")}
                          </small>
                        </span>
                        <Switch
                          aria-label={t("safety.restore")}
                          checked={policy.response_restore}
                          disabled={
                            saving || policy.request_action !== "redact"
                          }
                          id="privacy-response-restore"
                          onCheckedChange={(checked) =>
                            void patchPolicy({
                              response_restore: checked,
                            })
                          }
                          size="sm"
                          title={
                            policy.request_action !== "redact"
                              ? t("safety.restoreHint")
                              : undefined
                          }
                        />
                      </Label>
                      <Label className="flex min-w-0 cursor-pointer items-center justify-between gap-3 py-3 font-normal last:pb-0">
                        <span className="flex min-w-0 flex-col gap-0.5">
                          <strong className="text-sm font-medium leading-snug">
                            {t("safety.restoreTools")}
                          </strong>
                          <small
                            className="text-xs leading-relaxed text-muted-foreground"
                            title={t("safety.restoreToolsDetail")}
                          >
                            {t("safety.restoreToolsShort")}
                            <span className="sr-only">
                              {t("safety.restoreToolsDetail")}
                            </span>
                          </small>
                        </span>
                        <Switch
                          aria-label={t("safety.restoreTools")}
                          checked={policy.restore_tool_arguments}
                          disabled={saving || !policy.response_restore}
                          onCheckedChange={(checked) =>
                            void patchPolicy({
                              restore_tool_arguments: checked,
                            })
                          }
                          size="sm"
                          title={
                            policy.response_restore
                              ? undefined
                              : t("safety.restoreToolsHint")
                          }
                        />
                      </Label>
                      <Label className="flex min-w-0 cursor-pointer items-center justify-between gap-3 py-3 font-normal last:pb-0">
                        <span className="flex min-w-0 flex-col gap-0.5">
                          <strong className="text-sm font-medium leading-snug">
                            {t("safety.injectNotice")}
                          </strong>
                          <small className="text-xs leading-relaxed text-muted-foreground">
                            {t("safety.injectNoticeHint", {
                              style: placeholderStyleLabel("token"),
                            })}
                          </small>
                        </span>
                        <Switch
                          aria-label={t("safety.injectNotice")}
                          checked={policy.placeholder_notice}
                          disabled={saving}
                          onCheckedChange={(checked) =>
                            void patchPolicy({ placeholder_notice: checked })
                          }
                          size="sm"
                        />
                      </Label>
                    </div>
                  </fieldset>
                  <Button
                    className="h-auto w-fit gap-1 px-0 py-0 text-xs font-medium"
                    onClick={() => setStreamingDemoOpen(true)}
                    size="sm"
                    type="button"
                    variant="link"
                  >
                    {t("safety.viewStreamingDemo")}
                    <ArrowUpRight aria-hidden="true" className="size-3.5" />
                  </Button>
                </PolicySection>
              </div>
              <div className="grid min-w-0 gap-4">
                <PolicySection
                  title={t("safety.perKindRedact")}
                  icon={SlidersHorizontal}
                  description={t("safety.redactTypesHint")}
                  actions={
                    <Badge variant="secondary">
                      {t("safety.enabledTypes", {
                        count: PRIVACY_KINDS.filter(
                          (kind) => kindRuleFor(kind).enabled,
                        ).length,
                        total: PRIVACY_KINDS.length,
                      })}
                    </Badge>
                  }
                >
                  <fieldset className="min-w-0 border-0 p-0" disabled={saving}>
                    <legend className="sr-only">
                      {t("safety.perKindRedact")}
                    </legend>
                    <div className="mb-3">
                      <HelpDisclosure title={t("safety.placeholderGuide")}>
                        <p className="text-xs leading-relaxed">
                          {t("safety.styleHintLead", {
                            natural: placeholderStyleLabel("natural"),
                            token: placeholderStyleLabel("token"),
                          })}
                          <code className="font-mono">&lt;PRIVATE_…&gt;</code>
                          {t("safety.styleHintTail")}
                        </p>
                        <ul className="grid gap-2">
                          {PRIVACY_KINDS.filter((kind) =>
                            PLACEHOLDER_STYLE_LOCKED_KINDS.has(kind),
                          ).map((kind) => (
                            <li key={kind}>
                              <strong className="font-medium text-foreground">
                                {canonicalKindLabel(kind)}：
                              </strong>
                              {placeholderStyleLockReason(kind)}
                            </li>
                          ))}
                        </ul>
                      </HelpDisclosure>
                    </div>
                    <ul className="min-w-0 divide-y">
                      {PRIVACY_KINDS.map((kind) => {
                        const rule = kindRuleFor(kind);
                        const lockReason = placeholderStyleLockReason(kind);
                        const styleLocked =
                          PLACEHOLDER_STYLE_LOCKED_KINDS.has(kind);
                        const unreachable =
                          policy.detector === "regex" &&
                          localModelOnlyKinds.has(kind);
                        return (
                          <li
                            className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-2.5 first:pt-0 last:pb-0"
                            data-testid={`privacy-kind-rule-${kind}`}
                            key={kind}
                          >
                            <span className="flex min-w-0 flex-col gap-1">
                              <strong className="text-sm font-medium leading-snug">
                                {canonicalKindLabel(kind)}
                              </strong>
                              {unreachable ? (
                                <small className="text-xs text-muted-foreground">
                                  {t("safety.localOnlyShort")}
                                </small>
                              ) : styleLocked ? (
                                <small
                                  className="flex items-center gap-1 text-xs text-muted-foreground"
                                  title={lockReason}
                                >
                                  <LockKeyhole
                                    aria-hidden="true"
                                    className="size-3"
                                  />
                                  {t("safety.fixedStyle")}
                                </small>
                              ) : null}
                              {lockReason || unreachable ? (
                                <span
                                  className="sr-only"
                                  id={`privacy-kind-hint-${kind}`}
                                >
                                  {unreachable
                                    ? t("safety.localOnlyKind")
                                    : null}{" "}
                                  {lockReason}
                                </span>
                              ) : null}
                            </span>
                            <span className="flex shrink-0 items-center gap-2">
                              <Select
                                disabled={
                                  saving || styleLocked || !rule.enabled
                                }
                                onValueChange={(value) =>
                                  saveKindRule(kind, {
                                    style: value as PlaceholderStyle,
                                  })
                                }
                                value={rule.style}
                              >
                                <SelectTrigger
                                  aria-label={t("safety.styleFor", {
                                    kind: canonicalKindLabel(kind),
                                  })}
                                  aria-describedby={
                                    lockReason || unreachable
                                      ? `privacy-kind-hint-${kind}`
                                      : undefined
                                  }
                                  className="h-8 w-36 px-2.5 text-xs @[440px]:w-56"
                                  size="sm"
                                  title={
                                    styleLocked
                                      ? lockReason
                                      : placeholderStyleLabel(rule.style)
                                  }
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="natural">
                                    {placeholderStyleLabel("natural")}
                                  </SelectItem>
                                  <SelectItem value="token">
                                    {placeholderStyleLabel("token")}
                                  </SelectItem>
                                </SelectContent>
                              </Select>
                              <Switch
                                aria-label={t("safety.redactKind", {
                                  kind: canonicalKindLabel(kind),
                                })}
                                checked={rule.enabled}
                                disabled={saving}
                                onCheckedChange={(enabled) =>
                                  saveKindRule(kind, { enabled })
                                }
                                size="sm"
                              />
                            </span>
                          </li>
                        );
                      })}
                    </ul>
                  </fieldset>
                </PolicySection>
                <PolicySection title={t("safety.allowlist")} icon={ListFilter}>
                  <fieldset className="min-w-0 border-0 p-0" disabled={saving}>
                    <legend className="sr-only">{t("safety.allowlist")}</legend>
                    <p className="mb-2 text-xs leading-relaxed text-muted-foreground">
                      {t("safety.allowlistHint")}
                    </p>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        disabled={saving || allowlistPending}
                        onClick={addAllowlistRule}
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {t("safety.addAllowlist")}
                      </Button>
                      <span className="text-sm text-muted-foreground">
                        {policy.allowlist_rules.length}/
                        {MAX_PRIVACY_ALLOWLIST_RULES}
                      </span>
                    </div>
                    {policy.allowlist_rules.length === 0 &&
                    !allowlistPending ? (
                      <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                        {t("safety.allowlistEmpty")}
                      </p>
                    ) : (
                      <ul className="mt-2 grid min-w-0 gap-1.5">
                        {[
                          ...policy.allowlist_rules,
                          ...(allowlistPending
                            ? [{ type: allowlistPendingType, value: "" }]
                            : []),
                        ].map((rule, index) => (
                          <li
                            className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 @[400px]:grid-cols-[120px_minmax(0,1fr)_auto]"
                            key={`${rule.type}-${index}`}
                          >
                            <Select
                              disabled={saving}
                              onValueChange={(value) =>
                                changeAllowlistType(
                                  index,
                                  value as PrivacyAllowlistType,
                                )
                              }
                              value={rule.type}
                            >
                              <SelectTrigger
                                aria-label={t("safety.allowlistKind", {
                                  index: index + 1,
                                })}
                                className="h-9 w-full px-2.5 text-xs"
                                size="sm"
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {ALLOWLIST_TYPES.map((type) => (
                                  <SelectItem key={type} value={type}>
                                    {allowlistTypeLabel(type)}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                            <Input
                              aria-label={t("safety.allowlistValue", {
                                index: index + 1,
                              })}
                              autoFocus={
                                allowlistPending &&
                                index === policy.allowlist_rules.length
                              }
                              className="col-span-2 row-start-2 h-9 min-w-0 font-mono text-sm md:text-sm @[400px]:col-span-1 @[400px]:row-start-auto"
                              disabled={saving}
                              onBlur={() => commitAllowlistValue(index)}
                              onChange={(event) => {
                                const value = event.currentTarget.value;
                                setAllowlistDrafts((current) => {
                                  const next = [...current];
                                  next[index] = value;
                                  return next;
                                });
                              }}
                              onKeyDown={(event) => {
                                if (event.key === "Enter") {
                                  event.currentTarget.blur();
                                } else if (event.key === "Escape") {
                                  event.preventDefault();
                                  setAllowlistDrafts(
                                    policy.allowlist_rules.map(
                                      (item) => item.value,
                                    ),
                                  );
                                  setAllowlistPending(false);
                                }
                              }}
                              placeholder={allowlistTypePlaceholders[rule.type]}
                              value={allowlistDrafts[index] ?? rule.value}
                            />
                            <Button
                              disabled={saving}
                              onClick={() => removeAllowlistRule(index)}
                              size="sm"
                              type="button"
                              variant="ghost"
                            >
                              {t("safety.remove")}
                            </Button>
                          </li>
                        ))}
                      </ul>
                    )}
                  </fieldset>
                </PolicySection>
              </div>
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
                  {t("safety.run")}
                </h3>
                <p className="mt-1 text-sm leading-relaxed text-text-secondary">
                  {t("safety.dryRunHint")}
                </p>
              </div>
              <Button
                className="h-auto shrink-0 px-0 py-0 text-sm font-medium"
                onClick={() => setWorkspace("policy")}
                size="sm"
                type="button"
                variant="link"
              >
                {t("safety.backToPolicy")}
              </Button>
            </div>

              <Label
                className="grid min-w-0 gap-2 font-normal @[560px]:grid-cols-[minmax(0,1fr)_240px] @[560px]:items-center"
                htmlFor="privacy-dry-run-protocol"
              >
                <span className="flex min-w-0 flex-col gap-1">
                  <strong className="text-sm font-medium">{t("safety.protocol")}</strong>
                  <small className="text-sm leading-snug text-muted-foreground">
                    {t("safety.protocolHint")}
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
                    aria-label={t("safety.dryRunProtocol")}
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
                    <strong className="text-sm font-medium">{t("safety.sampleTextShort")}</strong>
                  </Label>
                  <small className="text-sm text-muted-foreground">
                    {t("safety.sampleCount", { count: dryRunSamplePresets.length })}
                  </small>
                </div>
                <div
                  aria-label={t("safety.dryRunSample")}
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
                        title={dryRunSampleDescription(preset.id)}
                        type="button"
                        variant="outline"
                      >
                        {dryRunSampleLabel(preset.id)}
                      </Button>
                    );
                  })}
                </div>
                <small className="text-sm leading-relaxed text-muted-foreground">
                  {selectedDryRunPreset
                    ? dryRunSampleDescription(selectedDryRunPreset.id)
                    : t("safety.customSample")}
                </small>
                <Textarea
                  aria-label={t("safety.sampleText")}
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
                  {t("safety.sampleBytes", {
                    used: dryRunSampleBytes.toLocaleString(),
                    max: MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES.toLocaleString(),
                  })}
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
                  {dryRunBusy ? t("safety.running") : t("safety.run")}
                </Button>
                {dryRunSampleOverLimit ? (
                  <small className="text-sm text-destructive">{t("safety.sampleTooLongHint")}</small>
                ) : !policy.enabled ? (
                  <small className="text-sm text-muted-foreground">{t("safety.dryRunOffHint")}</small>
                ) : policy.enabled &&
                  policy.detector === "local_model" &&
                  !selectedModelReady ? (
                  <small className="text-sm text-muted-foreground">{t("safety.needReadyModelHint")}</small>
                ) : (
                  <small className="text-sm text-muted-foreground">{t("safety.previewCurrent")}</small>
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
                {t("safety.dryRunResult")}
              </h3>
            <p aria-live="polite" className="sr-only">
              {dryRunBusy
                ? t("safety.running")
                : dryRunResult !== null
                  ? dryRunLiveSummary(dryRunResult)
                  : ""}
            </p>
              {dryRunBusy ? (
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {t("safety.running")}
                </p>
              ) : dryRunResult !== null ? (
                <div
                  className="grid gap-3"
                  data-testid="safety-dry-run-result"
                >
                  <p className="text-sm text-muted-foreground">{t("safety.localPreviewOnly")}</p>
                  <div className="flex items-center justify-between gap-3 rounded-md bg-accent px-3 py-2 text-sm text-accent-foreground">
                    <span>{t("safety.decision")}</span>
                    <strong className="font-medium">{actionLabel(dryRunResult.decision)}</strong>
                  </div>
                  <div className="flex items-center justify-between gap-3 text-sm">
                    <span>{t("safety.hitKinds")}</span>
                    <strong>{summarizeDryRunFindings(dryRunResult)}</strong>
                  </div>
                  {dryRunResult.findings.length > 0 ? (
                    <div className="grid gap-2">
                      <span className="text-sm font-medium text-success-foreground">{t("safety.passedJudgment")}</span>
                      <ul className="grid gap-2">
                        {dryRunResult.findings.map((finding, index) => (
                          <li
                            className="rounded-lg border bg-card px-3 py-2 text-sm"
                            key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                          >
                            <strong className="font-medium">{dryRunKindLabel(finding.kind)}</strong>
                            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                              {t("safety.position")}{" "}
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
                      <span className="text-sm font-medium text-warning-foreground">{t("safety.suppressed")}</span>
                      <ul className="grid gap-2">
                        {dryRunResult.suppressed_findings.map(
                          (finding, index) => (
                            <li
                              className="rounded-lg border bg-card px-3 py-2 text-sm"
                              key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}
                            >
                              <strong className="font-medium">{dryRunKindLabel(finding.kind)}</strong>
                              <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                                {t("safety.position")}{" "}
                                <code className="break-all">{finding.path}</code>
                              </p>
                              <p className="mt-0.5 text-sm leading-relaxed">
                                {dryRunSuppressionReason(
                                  finding,
                                  policy.min_confidence,
                                  policy.detector,
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
                      <span className="text-sm font-medium">{t("safety.placeholderCompare")}</span>
                      <ul className="grid gap-2">
                        {dryRunResult.redactions.map((redaction) => (
                          <li
                            className="rounded-lg border bg-card px-3 py-2 text-sm"
                            key={redaction.placeholder}
                          >
                            <code className="break-all">{redaction.placeholder}</code>
                            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                              {dryRunKindLabel(redaction.kind)} ·{" "}
                              {placeholderStyleLabel(redaction.style)}
                            </p>
                            <p className="mt-0.5 text-sm leading-relaxed">
                              {t("safety.original")}{" "}
                              <code className="break-all">{redaction.value}</code>
                            </p>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {dryRunResult.redacted_body !== undefined ? (
                    <div className="grid gap-1.5">
                      <span className="text-sm font-medium">{t("safety.redactedBody")}</span>
                      <pre className="overflow-x-auto rounded-lg bg-foreground p-3 font-mono text-xs font-normal whitespace-pre-wrap text-background">{prettyJSON(dryRunResult.redacted_body)}</pre>
                    </div>
                  ) : null}
                </div>
              ) : (
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {t("safety.notYetRun")}
                </p>
              )}
            </div>
            </div>
          </TabsContent>

          <TabsContent
            className="@container/models flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
            forceMount
            hidden={workspace !== "models"}
            value="models"
          >
            <div className="flex min-w-0 shrink-0 items-start justify-between gap-3">
              <div className="min-w-0">
                <h3 className="text-sm font-semibold tracking-tight">
                  {t("safety.localPrivacyModels")}
                </h3>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {t("safety.modelsStayLocal")}
                </p>
              </div>
              <StatusBadge tone={readyCount > 0 ? "positive" : "neutral"}>
                {t("safety.readyCount", { count: readyCount })}
              </StatusBadge>
            </div>

            <Tabs
              className="mt-3 flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden"
              onValueChange={(value) => setView(value as ModelView)}
              value={view}
            >
              <TabsList
                className="w-full shrink-0 justify-start border-b"
                aria-label={t("safety.modelView")}
                scrollable
                variant="line"
              >
                <TabsTrigger
                  onClick={() => setView("catalog")}
                  value="catalog"
                >
                  {t("safety.builtin")}
                </TabsTrigger>
                <TabsTrigger
                  onClick={() => setView("installed")}
                  value="installed"
                >
                  {t("safety.installedCount", { count: installations.length })}
                </TabsTrigger>
                <TabsTrigger
                  onClick={() => setView("local")}
                  value="local"
                >
                  {t("safety.localImport")}
                </TabsTrigger>
                <TabsTrigger
                  onClick={() => setView("custom")}
                  value="custom"
                >
                  {t("safety.custom")}
                </TabsTrigger>
              </TabsList>
              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="catalog">
                <div className="grid items-stretch gap-3 pb-3 pr-1 @[760px]/models:grid-cols-2">
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
                      <Panel asChild className="flex flex-col" key={model.id}>
                        <article>
                          <div className="flex flex-1 flex-col gap-3 p-4">
                            <div className="min-w-0">
                              <h4 className="text-sm font-semibold leading-snug break-words">{model.name}</h4>
                              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                                <span>{model.source === "official" ? t("safety.official") : t("safety.community")} · {model.license}</span>
                                {model.languages.map((language) => (
                                  <Badge key={language} variant="secondary">{language}</Badge>
                                ))}
                              </div>
                            </div>
                            <p className="text-xs leading-relaxed text-text-secondary">{model.summary}</p>
                            <Field
                              className="mt-auto"
                              htmlFor={variantSelectID}
                              label={t("safety.version")}
                            >
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
                                  aria-label={t("safety.versionsFor", { name: model.name })}
                                  className="w-full"
                                  id={variantSelectID}
                                  size="sm"
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {model.variants.map((candidate) => (
                                    <SelectItem disabled={!candidate.supported} key={candidate.id} value={candidate.id}>
                                      {candidate.name}
                                      {candidate.recommended ? t("safety.recommended") : ""}
                                      {!candidate.supported ? t("safety.unsupported") : ""}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </Field>
                          </div>
                          <PanelFooter actions={
                              existing === null ? (
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
                                    ? t("common.checking")
                                    : t("safety.checkAndInstall")}
                                </Button>
                              ) : (
                                <Button
                                  onClick={() => setView("installed")}
                                  size="sm"
                                  type="button"
                                  variant="outline"
                                >
                                  {t("safety.viewStatus", {
                                    status: installationStatusLabel(existing.status),
                                  })}
                                </Button>
                              )
                          }>
                            <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground tabular-nums">
                              <span>{t("safety.downloadSizeInline", { size: formatBytes(variant?.bytes_total ?? 0) })}</span>
                              <span>{t("safety.memoryInline", { size: formatBytes(variant?.estimated_ram_bytes ?? 0) })}</span>
                            </div>
                          </PanelFooter>
                        </article>
                      </Panel>
                    );
                  })}
                  {catalog.length === 0 ? (
                    <EmptyState className="col-span-full" title={t("safety.catalogEmpty")} />
                  ) : null}
                </div>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="installed">
                <div className="grid items-start gap-3 pb-3 pr-1 @[760px]/models:grid-cols-2">
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
                        ? t("safety.localImport")
                        : installation.catalog_source === "official"
                        ? t("safety.officialCatalog")
                        : installation.catalog_source === "community"
                          ? t("safety.communityCatalog")
                          : t("safety.customRepo");
                    const licenseLabel =
                      installation.license ?? t("safety.licenseUnknown");
                    const languageLabel =
                      installation.languages.length > 0
                        ? installation.languages.join(" / ")
                        : t("safety.languageUnknown");
                    return (
                      <Panel
                        asChild
                        className={cn(
                          "flex h-full flex-col",
                          selected && "border-primary/40 bg-accent/50 ring-1 ring-primary/10",
                        )}
                        key={installation.id}
                      >
                        <article>
                          <div className="grid gap-3 p-4">
                            <div className="flex min-w-0 flex-wrap items-start justify-between gap-2.5">
                              <div className="min-w-0">
                                <strong className="block text-sm font-semibold break-words">
                                  {installation.name}
                                </strong>
                                <span className="mt-1 block text-xs leading-snug text-muted-foreground">
                                  {installation.variant_name} ·{" "}
                                  {installation.quantization}
                                </span>
                              </div>
                              <StatusBadge
                                tone={installation.status === "ready" ? "positive" : installation.status === "error" ? "negative" : "pending"}
                              >
                                {selected
                                  ? t("safety.policySelected")
                                  : installation.source === "local" &&
                                      installation.status === "downloading"
                                    ? t("safety.importing")
                                    : installationStatusLabel(installation.status)}
                              </StatusBadge>
                            </div>
                            <div className="space-y-1 text-xs leading-relaxed text-muted-foreground">
                              <p>{sourceLabel} · {licenseLabel} · {languageLabel}</p>
                              <p className="break-all">{installation.repo_id}</p>
                            </div>
                            {installation.status === "downloading" ? (
                              <div className="grid gap-1.5">
                                <div className="flex items-center justify-between text-xs text-muted-foreground tabular-nums">
                                  <span>
                                    {hasDownloadTotal
                                      ? `${formatBytes(
                                          installation.bytes_downloaded,
                                        )} / ${formatBytes(
                                          installation.bytes_total,
                                        )}`
                                      : installation.source === "local"
                                        ? t("safety.preparingImport")
                                        : t("safety.preparingDownload")}
                                  </span>
                                  <strong>
                                    {hasDownloadTotal ? `${progress}%` : t("safety.preparing")}
                                  </strong>
                                </div>
                                <Progress
                                  aria-label={t("safety.progressAria", {
                                    name: installation.name,
                                    action:
                                      installation.source === "local"
                                        ? t("safety.import")
                                        : t("safety.download"),
                                  })}
                                  value={progress}
                                />
                              </div>
                            ) : (
                              <p className="text-xs leading-relaxed text-muted-foreground">
                                {installation.error === null
                                  ? t("safety.diskAndRam", {
                                      disk: formatBytes(installation.bytes_total),
                                      ram: formatBytes(installation.estimated_ram_bytes),
                                    })
                                  : installationErrorLabel(installation.error)}
                              </p>
                            )}
                            {Object.keys(installation.label_mapping).length > 0 ? (
                              <details className="rounded-md border bg-muted/40 px-3 py-2 text-xs">
                                <summary className="cursor-pointer font-semibold">
                                  {t("safety.labelMappingCount", {
                                    count: Object.keys(installation.label_mapping).length,
                                  })}
                                </summary>
                                <div className="mt-2 grid gap-1 text-xs text-muted-foreground">
                                  {Object.entries(installation.label_mapping).map(
                                    ([label, kind]) => (
                                      <span key={label}>
                                        <code>{label}</code>
                                        {" → "}
                                        {kind ?? t("safety.ignore")}
                                      </span>
                                    ),
                                  )}
                                </div>
                              </details>
                            ) : null}
                          </div>
                          <PanelFooter actions={<>
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
                                {selected ? t("safety.currentModel") : t("safety.usedByPolicy")}
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
                                {t("safety.retry")}
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
                                ? t("common.processing")
                                : installation.status === "downloading"
                                  ? t("common.cancel")
                                  : t("common.delete")}
                            </Button>
                          </>} />
                        </article>
                      </Panel>
                    );
                  })}
                  {installations.length === 0 ? (
                    <EmptyState className="col-span-full" title={t("safety.noneInstalled")} />
                  ) : null}
                </div>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="local">
                <Panel className="grid max-w-3xl gap-4 p-4">
                  <div className="grid gap-3 @[560px]:grid-cols-[minmax(0,1fr)_auto] @[560px]:items-end">
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-local-model-path"
                    >
                      <span>{t("safety.localPathField")}</span>
                      <Input
                        aria-describedby="local-model-mount-note"
                        aria-label={t("safety.localPath")}
                        autoComplete="off"
                        disabled={probing}
                        id="privacy-local-model-path"
                        maxLength={4096}
                        onChange={(event) => {
                          setLocalPath(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder={t("safety.localPathPlaceholder")}
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
                      {probing ? t("common.checking") : t("safety.checkLocal")}
                    </Button>
                  </div>
                  <p className="rounded-md bg-muted px-3 py-2.5 text-xs leading-relaxed text-muted-foreground" id="local-model-mount-note">
                    {t("safety.localMountNoteLead")}
                    <code>smb://</code>、<code>file://</code>
                    {t("safety.localMountNoteTail")}
                  </p>

                  {probe !== null && probeView === "local" ? (
                    <div className="grid gap-3 rounded-md border border-primary/20 bg-accent/40 p-3.5">
                      <div className="flex items-start justify-between gap-3">
                        <div>
                          <strong className="block text-sm">{probe.name}</strong>
                          <span className="mt-0.5 block text-xs text-muted-foreground">
                            {t("safety.pathChecked", {
                              license:
                                probe.license === null
                                  ? t("safety.licenseUnknown")
                                  : probe.license,
                            })}
                          </span>
                        </div>
                        <Badge variant="secondary">{probe.languages.join(" / ")}</Badge>
                      </div>
                      <Label
                        className="grid gap-1.5 text-xs font-medium"
                        htmlFor="privacy-local-model-variant"
                      >
                        <span>{t("safety.localRunVersion")}</span>
                        <Select
                          onValueChange={setProbeVariantID}
                          value={probeVariant?.id ?? ""}
                        >
                          <SelectTrigger
                            aria-label={t("safety.localVersion")}
                            className="w-full"
                            id="privacy-local-model-variant"
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {probe.variants.map((variant) => (
                              <SelectItem disabled={!variant.supported} key={variant.id} value={variant.id}>
                                {variant.name}
                                {variant.recommended ? t("safety.recommended") : ""}
                                {!variant.supported ? t("safety.unsupported") : ""}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Label>

                      <div className="flex flex-wrap items-center justify-end gap-2">
                        <span className="mr-auto text-sm leading-relaxed text-muted-foreground">
                          {probeVariant === null
                            ? t("safety.noSupportedVariant")
                            : `${t("safety.importAndRam", {
                                size: formatBytes(probeVariant.bytes_total),
                                ram: formatBytes(probeVariant.estimated_ram_bytes),
                              })}${
                                unresolvedCustomLabels.length > 0
                                  ? t("safety.labelsPending", {
                                      count: unresolvedCustomLabels.length,
                                    })
                                  : ""
                              }`}
                        </span>
                        <Button
                          onClick={() => setCustomMappingOpen(true)}
                          type="button"
                          variant="outline"
                        >
                          {t("safety.configureLabelsShort")}
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
                            ? t("common.processing")
                            : t("safety.importLocal")}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs leading-5 text-muted-foreground">
                      {t("safety.localOnnxHint")}
                    </p>
                  )}
                </Panel>
              </TabsContent>

              <TabsContent className="min-h-0 min-w-0 flex-1 overflow-y-auto" value="custom">
                <Panel className="grid max-w-3xl gap-4 p-4">
                  <div className="grid gap-3 @[560px]:grid-cols-2">
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-custom-repository"
                    >
                      <span>{t("safety.hfRepo")}</span>
                      <Input
                        aria-label={t("safety.hfRepo")}
                        disabled={probing}
                        id="privacy-custom-repository"
                        onChange={(event) => {
                          setCustomRepoID(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder={t("safety.orgModel")}
                        value={customRepoID}
                      />
                    </Label>
                    <Label
                      className="grid gap-1.5 text-xs font-medium"
                      htmlFor="privacy-custom-revision"
                    >
                      <span>Revision</span>
                      <Input
                        aria-label={t("safety.revision")}
                        disabled={probing}
                        id="privacy-custom-revision"
                        onChange={(event) => {
                          setCustomRevision(event.currentTarget.value);
                          resetProbedModel();
                        }}
                        placeholder={t("safety.revisionPlaceholder")}
                        value={customRevision}
                      />
                    </Label>
                    <Button
                      className="justify-self-end @[560px]:col-span-2"
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
                      {probing ? t("common.checking") : t("safety.checkCompat")}
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
                              ? t("safety.licenseUnknown")
                              : probe.license}
                          </span>
                        </div>
                        <Badge variant="secondary">{probe.languages.join(" / ")}</Badge>
                      </div>
                      <Label
                        className="grid gap-1.5 text-xs font-medium"
                        htmlFor="privacy-custom-model-variant"
                      >
                        <span>{t("safety.localRunVersion")}</span>
                        <Select
                          onValueChange={setProbeVariantID}
                          value={probeVariant?.id ?? ""}
                        >
                          <SelectTrigger
                            aria-label={t("safety.customVersion")}
                            className="w-full"
                            id="privacy-custom-model-variant"
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {probe.variants.map((variant) => (
                              <SelectItem disabled={!variant.supported} key={variant.id} value={variant.id}>
                                {variant.name}
                                {variant.recommended ? t("safety.recommended") : ""}
                                {!variant.supported ? t("safety.unsupported") : ""}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Label>

                      <div className="flex flex-wrap items-center justify-end gap-2">
                        <span className="mr-auto text-sm leading-relaxed text-muted-foreground">
                          {probeVariant === null
                            ? t("safety.noSupportedVariant")
                            : `${t("safety.downloadAndRam", {
                                size: formatBytes(probeVariant.bytes_total),
                                ram: formatBytes(probeVariant.estimated_ram_bytes),
                              })}${
                                unresolvedCustomLabels.length > 0
                                  ? t("safety.labelsPending", {
                                      count: unresolvedCustomLabels.length,
                                    })
                                  : ""
                              }`}
                        </span>
                        <Button
                          onClick={() => setCustomMappingOpen(true)}
                          type="button"
                          variant="outline"
                        >
                          {t("safety.configureLabelsShort")}
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
                            ? t("common.processing")
                            : t("safety.installCustom")}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs leading-5 text-muted-foreground">
                      {t("safety.customProbeHint")}
                    </p>
                  )}
                </Panel>
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
              ? t("common.processing")
              : t("safety.confirmInstall")
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
          summary={`${t("safety.downloadAndRam", {
            size: formatBytes(catalogPreparation.variant.bytes_total),
            ram: formatBytes(catalogPreparation.variant.estimated_ram_bytes),
          })}${
            unresolvedCatalogLabels.length > 0
              ? t("safety.labelsPending", {
                  count: unresolvedCatalogLabels.length,
                })
              : ""
          }`}
          title={t("safety.configureLabels", {
            name: catalogPreparationModel.name,
          })}
          touchedLabels={catalogPreparation.touchedLabels}
        />
      ) : null}
      {customMappingOpen && probe !== null ? (
        <LabelMappingDialog
          confirmDisabled={unresolvedCustomLabels.length > 0}
          confirmLabel={t("safety.applyMapping")}
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
          summary={`${t("safety.labelCount", { count: probe.labels.length })}${
            unresolvedCustomLabels.length > 0
              ? t("safety.labelsPending", {
                  count: unresolvedCustomLabels.length,
                })
              : t("safety.mappingComplete")
          }`}
          title={t("safety.configureLabels", { name: probe.name })}
          touchedLabels={labelMappingTouched}
        />
      ) : null}
      {confirmFillBuiltinRules ? (
        <ConfirmDialog
          confirmLabel={t("safety.overwriteFill")}
          description={t("safety.overwriteHint")}
          onCancel={() => setConfirmFillBuiltinRules(false)}
          onConfirm={() => {
            void fillBuiltinRules();
          }}
          open={confirmFillBuiltinRules}
          title={t("safety.fillBuiltin")}
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
