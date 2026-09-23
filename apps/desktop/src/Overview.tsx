import { BillingOverview, useBillingSummary } from "./BillingOverview";
import {
  memo,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import {
  Activity,
  ArrowRight,
  ArrowUpRight,
  Boxes,
  Bot,
  Check,
  CircleDollarSign,
  Copy,
  Key,
  Plus,
  RefreshCw,
  RotateCcw,
  SlidersHorizontal,
  Server,
} from "@/components/icons";

import { CompactCount } from "@/components/CompactCount";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ExternalLink } from "@/components/ExternalLink";
import {
  ActivityHeatmap,
  type ActivityCell,
} from "@/components/ActivityHeatmap";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { SegmentedControl } from "@/components/SegmentedControl";
import { InferencePortNotice } from "@/components/InferencePortNotice";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup, MetricValuePair } from "@/components/Metric";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel, PanelHeader } from "@/components/Panel";
import { OrderedList } from "@/components/OrderedList";
import { useOverviewLayout, type OverviewModuleId } from "./overview-layout";
import { PaginatedList } from "@/components/PaginatedList";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { BarShapeProps, TooltipContentProps } from "recharts";

import type { AccessTokenCatalog } from "./AccessTokenManager";
import { phaseLabel, phaseTone, type AppSnapshot } from "./core-model";
import { billingAmount, type BillingSummary } from "./pricing-model";
import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import astrlinkLogo from "./assets/astrlink-logo.svg";
import { i18n, useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { ActionGroup } from "@/components/ActionGroup";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
import { placeFloatingCard } from "./place-floating-card";
import type { Service } from "./service-model";
import type { ServiceCatalogStatus } from "./ServiceManager";
import {
  dayTokenStack,
  emptyUsageTotals,
  formatCacheHitPercent,
  isUsageRangePreset,
  mergeCatalogServiceUsage,
  modelUsageLabel,
  usageBarPercent,
  USAGE_RANGE_PRESETS,
  type DayTokenStackSeries,
  type MergedServiceUsage,
  type UsageDayBucket,
  type UsageGroup,
  type UsageRangePreset,
  type UsageState,
  type UsageStatus,
  type UsageSummary,
  type UsageTotals,
} from "./usage-range";

export interface ServiceCatalog {
  status: ServiceCatalogStatus;
  items: Service[];
  error: string | null;
  stale: boolean;
}

const USAGE_METRIC_GRID =
  "gap-3 bg-transparent [&>div]:rounded-md [&>div]:border @min-[640px]/workspace-surface:grid-cols-[1fr_1fr_1.4fr_1fr]";

const USAGE_BREAKDOWN_HEADER = "min-h-14 flex-wrap items-center py-2";

export function Overview({
  catalog,
  copyError,
  copyFeedback,
  isNativeApp,
  isReady,
  isRestarting,
  onAddService,
  onCopy,
  onManageServices,
  onManageTokens,
  onOpenService,
  onOpenTokenRecords,
  onRefreshServices,
  onRefreshUsage,
  onRestart,
  onUsagePresetChange,
  snapshot,
  tokenCatalog,
  usage,
  usagePreset,
}: {
  catalog: ServiceCatalog;
  copyError: string | null;
  copyFeedback: string | null;
  isNativeApp: boolean;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onCopy: (value: string, label: string) => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onOpenService: (serviceId: string) => void;
  onOpenTokenRecords: (tokenId: string) => void;
  onRefreshServices: () => void;
  onRefreshUsage: () => void;
  onRestart: () => void;
  onUsagePresetChange: (preset: UsageRangePreset) => void;
  snapshot: AppSnapshot | null;
  tokenCatalog: AccessTokenCatalog;
  usage: UsageState;
  usagePreset: UsageRangePreset;
}) {
  const t = i18n.t.bind(i18n);
  const [view, setView] = useState<"chart" | "heatmap">(() =>
    usagePreset === "1d" || usagePreset === "7d" ? "chart" : "heatmap",
  );
  const [editingLayout, setEditingLayout] = useState(false);
  const layout = useOverviewLayout();
  const visibleModules = layout.order.filter(
    (id) => !layout.hidden.includes(id),
  );
  const capabilities = snapshot?.capabilities ?? null;
  const conversionEngine = capabilities?.conversion_engine;
  const statusTone = snapshot ? phaseTone(snapshot.phase) : "neutral";
  const statusLabel = snapshot
    ? phaseLabel(snapshot.phase)
    : t("core.phase.connecting");
  const enabledCount = catalog.items.filter(
    (service) => service.enabled,
  ).length;
  const catalogUnknown =
    catalog.status === "blocked" && catalog.items.length === 0;
  const tokensUnknown =
    tokenCatalog.status === "blocked" && tokenCatalog.items.length === 0;
  const summary = usage.summary;
  const { status: billingStatus, summary: billingSummary } = useBillingSummary({
    from: summary?.window.from,
    to: summary?.window.to,
    ready: isReady && usage.status !== "blocked" && usage.status !== "error",
    revision: summary,
  });

  // A disconnected catalog is unknown, not empty. Use the same compact surface
  // with connection-specific content, and keep any retained activity visible.
  const emptyWorkspace =
    catalog.items.length === 0 &&
    tokenCatalog.items.length === 0 &&
    catalog.status !== "error" &&
    tokenCatalog.status !== "error" &&
    usage.status !== "error" &&
    !summary?.scanned_records &&
    !summary?.totals.requests &&
    !summary?.totals.total_tokens &&
    !summary?.by_service.length &&
    !summary?.by_model.length;
  const inferenceURL = snapshot?.ready?.inference_url ?? "";
  const apiAddressLabel = t("overview.apiAddress");
  const apiCopied =
    copyFeedback === t("copy.copiedNamed", { label: apiAddressLabel });
  // A retained summary survives a failed refresh so the panel does not flash
  // empty, but its numbers must not be presented as current.
  const totals =
    usage.status === "blocked" || usage.status === "error"
      ? undefined
      : summary?.totals;
  const serviceRows = mergeCatalogServiceUsage(
    catalog.items,
    summary?.by_service ?? [],
  );
  const modelRows = summary?.by_model ?? [];
  const systemDetails: Array<[string, ReactNode]> = [
    [
      t("overview.desktopVersion"),
      snapshot?.app_version ?? t("common.unknown"),
    ],
    [
      t("overview.gatewayVersion"),
      snapshot?.version?.core_version ??
        snapshot?.ready?.core_version ??
        t("overview.pending"),
    ],
    [
      t("overview.process"),
      snapshot?.pid ? `PID ${snapshot.pid}` : t("overview.notRunning"),
    ],
    [
      t("overview.controlListen"),
      snapshot?.ready?.control_url ?? t("overview.unassigned"),
    ],
    [
      t("overview.controlApiVersion"),
      snapshot?.version?.control_api_version ?? t("overview.pendingHandshake"),
    ],
    [
      t("overview.conversionEngine"),
      <ExternalLink
        href="https://github.com/QuantumNous/new-api/tree/main/relaykit"
        title={t("overview.relaykitSource")}
      >
        {conversionEngine?.available
          ? `${conversionEngine.name} ${conversionEngine.version ?? ""}`.trim()
          : t("overview.relaykitOff")}
      </ExternalLink>,
    ],
  ];

  const moduleLabels: Record<OverviewModuleId, string> = {
    usage: t("overview.usageSummary"),
    "providers-models": t("overview.providersAndModels"),
    tokens: t("overview.byAccessToken"),
    access: t("overview.apiAddress"),
    system: t("overview.systemDetails"),
  };
  const moduleContent: Record<OverviewModuleId, ReactNode> = {
    usage: (
      <section
        aria-labelledby="usage-heading"
        aria-busy={usage.status === "loading"}
        className="grid min-w-0 grid-cols-1 gap-3"
      >
        <div
          className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2"
          data-slot="overview-toolbar"
        >
          <div className="flex items-center gap-1.5">
            <h2 className="text-sm font-medium" id="usage-heading">
              {t("overview.usageSummary")}
            </h2>
            <HelpPopover label={t("overview.usageDetails")}>
              <p>
                {t(
                  usagePreset === "1d"
                    ? "overview.hourlyUsageNote"
                    : "overview.usageNote",
                )}
              </p>
              <p className="mt-2">{t("overview.heatmapNote")}</p>
            </HelpPopover>
          </div>
          <ActionGroup className="gap-4">
            <SegmentedControl
              label={t("overview.viewLabel")}
              variant="line"
              value={view}
              onValueChange={(next) => {
                setView(next);
                if (
                  next === "heatmap" &&
                  (usagePreset === "1d" || usagePreset === "7d")
                )
                  onUsagePresetChange("1y");
              }}
              options={[
                { value: "heatmap", label: t("overview.heatmap") },
                { value: "chart", label: t("overview.chart") },
              ]}
            />
            <span aria-hidden="true" className="h-4 w-px bg-border" />
            <UsageRangeSelect
              onChange={onUsagePresetChange}
              preset={usagePreset}
              calendar={view === "heatmap"}
            />
          </ActionGroup>
        </div>
        {usage.status === "loading" && !summary ? (
          <UsagePanelSkeleton />
        ) : (
          <div className="relative grid min-w-0 grid-cols-1 gap-3">
            <MetricGroup className={USAGE_METRIC_GRID}>
              <UsageMetric
                icon={<Activity />}
                label={t("overview.requests")}
                metric={compactMetric(totals?.requests, usage.status)}
              />
              <UsageMetric icon={<Boxes />} label={t("overview.totalTokens")}>
                <CompactCount
                  value={usageCountValue(totals?.total_tokens, usage.status)}
                />
              </UsageMetric>
              <UsageMetric
                icon={<ArrowUpRight />}
                label={t("overview.inputOutput")}
              >
                <MetricValuePair
                  first={
                    <CompactCount
                      value={usageCountValue(
                        totals?.input_tokens,
                        usage.status,
                      )}
                    />
                  }
                  second={
                    <CompactCount
                      value={usageCountValue(
                        totals?.output_tokens,
                        usage.status,
                      )}
                    />
                  }
                />
              </UsageMetric>
              <UsageMetric
                icon={<RefreshCw />}
                label={t("overview.cacheHits")}
                metric={{
                  text: cacheHitMetric(totals, usage.status),
                  title: null,
                }}
              />
            </MetricGroup>
            <Panel aria-labelledby="activity-heading">
              <h3 className="sr-only" id="activity-heading">
                {t("overview.activity")}
              </h3>
              {view === "heatmap" ? (
                <UsageHeatmap
                  points={summary?.by_day ?? []}
                  status={usage.status}
                />
              ) : (
                <UsageDayChart
                  grain={usagePreset === "1d" ? "hour" : "day"}
                  points={
                    usagePreset === "1d"
                      ? (summary?.by_hour ?? [])
                      : (summary?.by_day ?? [])
                  }
                  status={usage.status}
                />
              )}
              <BillingOverview
                loading={billingStatus === "loading"}
                summary={billingSummary}
              />
            </Panel>
            {usage.status === "loading" ? (
              <div
                className="absolute inset-0 z-10 flex items-center justify-center rounded-md bg-card/70"
                data-slot="usage-loading"
              >
                <LoadingState
                  className="rounded-md border bg-card px-3 py-1.5 text-xs"
                  label={t("overview.aggregating")}
                />
              </div>
            ) : null}
          </div>
        )}
        {summary?.capped && usage.status !== "loading" ? (
          <Badge
            className="w-fit bg-warning-wash text-warning-foreground"
            variant="secondary"
          >
            {t("overview.recentCapped", {
              count: formatExactNumber(summary.scanned_records),
            })}
          </Badge>
        ) : null}
        {usage.status === "error" && usage.error ? (
          <p className="text-xs text-danger-foreground" role="alert">
            {usage.error}
          </p>
        ) : null}
      </section>
    ),
    "providers-models": (
      <div className="grid min-w-0 grid-cols-1 items-start gap-3 @min-[640px]/workspace-surface:grid-cols-2">
        <Panel aria-labelledby="usage-by-service-heading">
          <PanelHeader
            className={USAGE_BREAKDOWN_HEADER}
            actions={
              <>
                <div className="flex items-baseline gap-2 pr-1">
                  <OverviewCount
                    label={t("overview.configured")}
                    unknown={catalogUnknown}
                    value={catalog.items.length}
                  />
                  <OverviewCount
                    label={t("overview.enabled")}
                    unknown={catalogUnknown}
                    value={enabledCount}
                  />
                </div>
                {catalogUnknown ? (
                  <Badge
                    className="bg-warning-wash text-warning-foreground"
                    variant="secondary"
                  >
                    {t("overview.waitingGateway")}
                  </Badge>
                ) : catalog.stale ? (
                  <Badge
                    className="bg-warning-wash text-warning-foreground"
                    variant="secondary"
                  >
                    {t("overview.waitingRefresh")}
                  </Badge>
                ) : catalog.items.length > 0 ? (
                  <Button
                    disabled={!isReady}
                    onClick={onAddService}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    <Plus aria-hidden="true" />
                    {t("overview.add")}
                  </Button>
                ) : null}
              </>
            }
          >
            <h2 className="text-sm font-semibold" id="usage-by-service-heading">
              {t("overview.byService")}
            </h2>
          </PanelHeader>

          <PaginatedList
            key={usagePreset}
            items={serviceRows}
            itemsClassName="min-h-75"
            label={t("overview.byService")}
            footer={
              <Button
                className="justify-start px-0 text-xs text-text-secondary has-[>svg]:px-0"
                onClick={onManageServices}
                size="sm"
                type="button"
                variant="link"
              >
                {t("overview.manageAllServices")}
                <ArrowRight className="size-4" />
              </Button>
            }
          >
            {(visibleRows) => (
              <ServiceUsageBody
                catalog={catalog}
                catalogUnknown={catalogUnknown}
                isReady={isReady}
                onAddService={onAddService}
                onOpenService={onOpenService}
                onRefreshServices={onRefreshServices}
                rows={serviceRows}
                visibleRows={visibleRows}
                showBar={
                  usage.status === "ready" &&
                  serviceRows.some((row) => row.total_tokens > 0)
                }
                status={usage.status}
              />
            )}
          </PaginatedList>
        </Panel>

        <Panel aria-labelledby="usage-by-model-heading">
          <PanelHeader
            className={USAGE_BREAKDOWN_HEADER}
            actions={
              <span className="text-micro text-muted-foreground">
                {t("overview.rankedByTokens")}
              </span>
            }
          >
            <h2 className="text-sm font-semibold" id="usage-by-model-heading">
              {t("overview.byModel")}
            </h2>
          </PanelHeader>
          <PaginatedList
            key={usagePreset}
            items={modelRows}
            itemsClassName="min-h-75"
            label={t("overview.byModel")}
            footer={
              <span className="text-xs text-muted-foreground">
                {usage.status === "ready"
                  ? t("overview.modelCount", { count: modelRows.length })
                  : usage.status === "loading"
                    ? t("overview.aggregatingModels")
                    : t(
                        usage.status === "blocked"
                          ? "overview.waitingGateway"
                          : "overview.waitingRefresh",
                      )}
              </span>
            }
          >
            {(visibleRows) => (
              <ModelUsageBody
                rows={modelRows}
                visibleRows={visibleRows}
                showBar={
                  usage.status === "ready" &&
                  modelRows.some((row) => row.total_tokens > 0)
                }
                status={usage.status}
              />
            )}
          </PaginatedList>
        </Panel>
      </div>
    ),
    tokens: (
      <TokenUsagePanel
        billingStatus={billingStatus}
        billingSummary={billingSummary}
        isReady={isReady}
        onOpenTokenRecords={onOpenTokenRecords}
        onRefreshUsage={onRefreshUsage}
        status={usage.status}
        tokenCatalog={tokenCatalog}
        tokensUnknown={tokensUnknown}
        usage={summary}
        usagePreset={usagePreset}
      />
    ),
    access: (
      <Panel
        aria-labelledby="access-heading"
        className={cn(
          !isReady && statusTone === "negative" && "border-destructive/25",
        )}
      >
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <div
              className={cn(
                "flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1",
                apiCopied && "text-success-foreground",
              )}
            >
              <span
                className="shrink-0 text-xs text-muted-foreground"
                id="access-heading"
              >
                {apiAddressLabel}
              </span>
              <code
                className="min-w-0 truncate font-mono text-xs font-medium tracking-tight"
                title={inferenceURL}
              >
                {inferenceURL || t("overview.waitingReady")}
              </code>
            </div>
            <Button
              aria-label={t("overview.copyApiAddress")}
              disabled={!inferenceURL}
              onClick={() => onCopy(inferenceURL, apiAddressLabel)}
              size="icon-sm"
              type="button"
              variant="ghost"
            >
              {apiCopied ? (
                <Check aria-hidden="true" />
              ) : (
                <Copy aria-hidden="true" />
              )}
            </Button>
          </div>

          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex min-w-0 items-baseline gap-1.5">
              <strong className="text-xs font-medium tabular-nums">
                {tokensUnknown ? "—" : tokenCatalog.items.length}
              </strong>
              <span className="text-xs text-text-secondary">
                {tokensUnknown
                  ? t("overview.tokenPendingHint")
                  : tokenCatalog.items.length
                    ? t("overview.tokenCountHint")
                    : t("overview.noTokens")}
              </span>
            </div>
            {tokenCatalog.stale ? (
              <Badge
                className="bg-warning-wash text-warning-foreground"
                variant="secondary"
              >
                {t("overview.waitingRefresh")}
              </Badge>
            ) : null}
            <Button
              disabled={!isReady && tokenCatalog.items.length === 0}
              onClick={onManageTokens}
              size="sm"
              type="button"
              variant={
                !tokensUnknown && tokenCatalog.items.length === 0
                  ? "default"
                  : "ghost"
              }
            >
              {t("overview.manageTokens")}
            </Button>
          </div>
        </div>
        {copyError ? (
          <p
            className="border-t px-4 py-2 text-xs text-danger-foreground"
            role="alert"
          >
            {copyError}
          </p>
        ) : copyFeedback ? (
          <p
            className="border-t px-4 py-2 text-xs text-success-foreground"
            role="status"
          >
            {copyFeedback}
          </p>
        ) : null}
      </Panel>
    ),
    system: (
      <section
        aria-labelledby="system-details-heading"
        className="min-w-0 px-1 pb-1"
      >
        <h2
          className="text-xs font-medium text-muted-foreground"
          id="system-details-heading"
        >
          {t("overview.systemDetails")}
        </h2>
        <div className="mt-3 grid min-w-0 gap-3 text-xs leading-relaxed text-muted-foreground">
          <dl className="grid grid-cols-3 gap-x-6 gap-y-4 max-[720px]:grid-cols-2">
            {systemDetails.map(([term, detail]) => (
              <div className="min-w-0" key={term}>
                <dt className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                  {term}
                </dt>
                <dd className="mt-1 overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">
                  {detail}
                </dd>
              </div>
            ))}
          </dl>
          <div className="mt-4 grid gap-2 border-t pt-4">
            <span className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
              {t("overview.protocolCapabilities")}
            </span>
            <div className="flex flex-wrap gap-1.5">
              {capabilities?.protocols.length ? (
                capabilities.protocols.map((protocol) => (
                  <code
                    className="rounded-sm border px-1.5 py-0.5 font-mono text-micro text-text-secondary"
                    key={protocol.id}
                  >
                    {protocol.id}
                  </code>
                ))
              ) : (
                <small className="text-xs text-muted-foreground">
                  {t("overview.protocolsAfterReady")}
                </small>
              )}
            </div>
          </div>
        </div>
      </section>
    ),
  };

  return (
    <ScrollWorkspace
      contentClassName="[overflow-anchor:none]"
      contentSlot="overview-content"
      data-slot="overview-workspace"
      header={
        <>
          <PageHeader
            actions={
              <>
                {editingLayout ? (
                  <IconButton
                    disabled={layout.isDefault && !layout.saveFailed}
                    label={t("overview.resetLayout")}
                    onClick={layout.reset}
                    type="button"
                  >
                    <RotateCcw aria-hidden="true" />
                  </IconButton>
                ) : null}
                <IconButton
                  aria-pressed={editingLayout}
                  label={t(
                    editingLayout
                      ? "overview.finishLayout"
                      : "overview.customizeLayout",
                  )}
                  onClick={() => setEditingLayout((value) => !value)}
                  type="button"
                  variant={editingLayout ? "secondary" : "ghost"}
                >
                  {editingLayout ? (
                    <Check aria-hidden="true" />
                  ) : (
                    <SlidersHorizontal aria-hidden="true" />
                  )}
                </IconButton>
                {!emptyWorkspace ? (
                  <Button
                    disabled={!isReady || usage.status === "loading"}
                    onClick={onRefreshUsage}
                    size="icon-sm"
                    type="button"
                    variant="ghost"
                    aria-label={t("common.refresh")}
                  >
                    <RefreshCw
                      aria-hidden="true"
                      className={cn(
                        usage.status === "loading" &&
                          "animate-spin motion-reduce:animate-none",
                      )}
                    />
                  </Button>
                ) : null}
                <StatusBadge tone={statusTone}>
                  {isReady ? t("overview.gatewayHealthy") : statusLabel}
                </StatusBadge>
                {isNativeApp && !isReady && !emptyWorkspace ? (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={isRestarting || snapshot?.phase === "stopping"}
                    onClick={onRestart}
                    type="button"
                  >
                    {isRestarting
                      ? t("overview.restarting")
                      : t("overview.restartGateway")}
                  </Button>
                ) : null}
              </>
            }
            className="mb-0 border-0 py-0"
            title={t("overview.title")}
            variant="compact"
          />
          <InferencePortNotice snapshot={snapshot} />
          {layout.saveFailed ? (
            <p className="text-xs text-danger-foreground" role="alert">
              {t("overview.layoutSaveFailed")}
            </p>
          ) : null}
        </>
      }
    >
      <div className="grid min-w-0 gap-3 pb-1">
        {emptyWorkspace && !editingLayout ? (
          <>
            <OverviewWelcome
              catalog={catalog}
              isNativeApp={isNativeApp}
              isReady={isReady}
              isRestarting={isRestarting}
              onAddService={onAddService}
              onManageServices={onManageServices}
              onManageTokens={onManageTokens}
              onRestart={onRestart}
              snapshot={snapshot}
              tokenCatalog={tokenCatalog}
            />
            {!layout.hidden.includes("system") && moduleContent.system}
          </>
        ) : !editingLayout && visibleModules.length === 0 ? (
          <EmptyState
            title={t("overview.allModulesHidden")}
            description={t("overview.allModulesHiddenHint")}
            action={
              <Button variant="outline" onClick={() => setEditingLayout(true)}>
                {t("overview.customizeLayout")}
              </Button>
            }
          />
        ) : (
          <>
            <OrderedList
              className="gap-3"
              disabled={!editingLayout}
              handleHint={t("overview.layoutDragHint")}
              itemLabel={({ id }) => moduleLabels[id]}
              itemActions={({ id }) => (
                <Label className="flex shrink-0 items-center gap-2 text-xs font-normal text-muted-foreground">
                  {t("overview.moduleVisible")}
                  <Switch
                    aria-label={t("overview.showModule", {
                      module: moduleLabels[id],
                    })}
                    checked={!layout.hidden.includes(id)}
                    onCheckedChange={(visible) =>
                      layout.setVisible(id, visible)
                    }
                    size="sm"
                  />
                </Label>
              )}
              items={(editingLayout ? layout.order : visibleModules).map(
                (id) => ({ id }),
              )}
              label={t("overview.customizeLayout")}
              onChange={(items) => layout.save(items.map(({ id }) => id))}
              variant="modules"
            >
              {({ id }) => !layout.hidden.includes(id) && moduleContent[id]}
            </OrderedList>
          </>
        )}
      </div>
    </ScrollWorkspace>
  );
}

interface TokenUsageRow {
  id: string;
  name: string;
  usage: UsageGroup;
  billing: BillingSummary["by_token"][number];
}

type TokenSortKey = "tokens" | "fee" | "requests";

function emptyTokenBilling(tokenId: string): TokenUsageRow["billing"] {
  return {
    token_id: tokenId,
    amount_usd: "0",
    priced: 0,
    unpriced: 0,
    pending: 0,
    revalued: 0,
    requests: 0,
  };
}

function mergeTokenUsageRows(
  catalog: AccessTokenCatalog,
  usage: UsageSummary | null,
  billing: BillingSummary | null,
): TokenUsageRow[] {
  const usageById = new Map(
    (usage?.by_token ?? [])
      .filter(
        (group): group is UsageGroup & { id: string } => group.id !== null,
      )
      .map((group) => [group.id, group]),
  );
  const billingById = new Map(
    (billing?.by_token ?? []).map((group) => [group.token_id, group]),
  );
  return catalog.items.map((token) => ({
    id: token.id,
    name: token.name,
    usage: usageById.get(token.id) ?? { id: token.id, ...emptyUsageTotals() },
    billing: billingById.get(token.id) ?? emptyTokenBilling(token.id),
  }));
}

function tokenRequestCount(row: TokenUsageRow): number {
  return row.usage.requests + row.usage.failed_requests;
}

function tokenFee(row: TokenUsageRow): number {
  const value = Number(row.billing.amount_usd);
  return Number.isFinite(value) ? value : 0;
}

function compareTokenUsageRows(
  left: TokenUsageRow,
  right: TokenUsageRow,
  sort: TokenSortKey,
): number {
  const primary =
    sort === "tokens"
      ? right.usage.total_tokens - left.usage.total_tokens
      : sort === "fee"
        ? tokenFee(right) - tokenFee(left)
        : tokenRequestCount(right) - tokenRequestCount(left);
  if (primary !== 0) return primary;

  // Keep ties deterministic without exposing a second active sort condition.
  if (right.usage.total_tokens !== left.usage.total_tokens) {
    return right.usage.total_tokens - left.usage.total_tokens;
  }
  if (tokenRequestCount(right) !== tokenRequestCount(left)) {
    return tokenRequestCount(right) - tokenRequestCount(left);
  }
  if (tokenFee(right) !== tokenFee(left))
    return tokenFee(right) - tokenFee(left);

  return left.name.localeCompare(
    right.name,
    i18n.language === "zh-CN" ? "zh" : "en",
  );
}

function TokenUsagePanel({
  billingStatus,
  billingSummary,
  isReady,
  onOpenTokenRecords,
  onRefreshUsage,
  status,
  tokenCatalog,
  tokensUnknown,
  usage,
  usagePreset,
}: {
  billingStatus: "idle" | "loading" | "ready" | "error";
  billingSummary: BillingSummary | null;
  isReady: boolean;
  onOpenTokenRecords: (tokenId: string) => void;
  onRefreshUsage: () => void;
  status: UsageStatus;
  tokenCatalog: AccessTokenCatalog;
  tokensUnknown: boolean;
  usage: UsageSummary | null;
  usagePreset: UsageRangePreset;
}) {
  const t = i18n.t.bind(i18n);
  const [sort, setSort] = useState<TokenSortKey>("tokens");
  const rows = useMemo(
    () => mergeTokenUsageRows(tokenCatalog, usage, billingSummary),
    [billingSummary, tokenCatalog, usage],
  );
  const sortedRows = useMemo(
    () =>
      [...rows].sort((left, right) => compareTokenUsageRows(left, right, sort)),
    [rows, sort],
  );
  const sortOptions = [
    { key: "tokens" as const, label: t("overview.sortTokens"), Icon: Boxes },
    {
      key: "fee" as const,
      label: t("overview.sortFee"),
      Icon: CircleDollarSign,
    },
    {
      key: "requests" as const,
      label: t("overview.sortRequests"),
      Icon: Activity,
    },
  ];

  return (
    <Panel
      aria-labelledby="usage-by-token-heading"
      data-testid="token-usage-panel"
    >
      <PanelHeader
        className={USAGE_BREAKDOWN_HEADER}
        actions={
          <div className="flex min-w-0 items-center gap-2">
            <HelpPopover label={t("overview.tokenCostCoverageLabel")}>
              <p>{t("overview.tokenFailureExplanation")}</p>
              <p>{t("overview.tokenCostCoverage")}</p>
            </HelpPopover>
            <div
              aria-label={t("overview.tokenSortLabel")}
              className="flex items-center gap-0.5"
              data-testid="token-sort-controls"
            >
              {sortOptions.map(({ key, label, Icon }) => {
                const active = sort === key;
                const buttonLabel = active
                  ? t("overview.tokenSortActive", { label })
                  : t("overview.tokenSortBy", { label });
                return (
                  <IconButton
                    aria-pressed={active}
                    className={cn(
                      "relative",
                      active &&
                        "bg-accent text-accent-foreground ring-1 ring-inset ring-ring/35",
                    )}
                    data-active={active ? "true" : "false"}
                    data-testid={`token-sort-${key}`}
                    key={key}
                    label={buttonLabel}
                    onClick={() => setSort(key)}
                    type="button"
                  >
                    <Icon aria-hidden="true" />
                  </IconButton>
                );
              })}
            </div>
          </div>
        }
      >
        <h2 className="text-sm font-semibold" id="usage-by-token-heading">
          {t("overview.byAccessToken")}
        </h2>
      </PanelHeader>
      <PaginatedList
        key={usagePreset}
        items={sortedRows}
        itemsClassName="min-h-24"
        label={t("overview.byAccessToken")}
        footer={
          <span className="text-xs text-muted-foreground">
            {status === "ready"
              ? t("overview.tokenUsageCount", { count: sortedRows.length })
              : status === "loading"
                ? t("overview.aggregatingTokens")
                : t(
                    status === "blocked"
                      ? "overview.waitingGateway"
                      : "overview.waitingRefresh",
                  )}
          </span>
        }
      >
        {(visibleRows) => {
          // A blocked catalog is unknown, not empty: the token list itself was
          // never read, so "no usage" would misreport missing data as zero.
          if (tokensUnknown) {
            return (
              <div className="flex flex-col items-center justify-center gap-1 border-b px-4 py-8 text-center">
                <p className="text-sm text-text-secondary">
                  {t("overview.tokensAfterReady")}
                </p>
                <span className="text-xs text-muted-foreground">
                  {t("overview.noTokenCatalog")}
                </span>
              </div>
            );
          }
          if (status === "blocked" || (status === "error" && !usage)) {
            return (
              <EmptyState
                className="border-0 py-8"
                title={t(
                  status === "blocked"
                    ? "overview.usageBlocked"
                    : "overview.usageFailed",
                )}
              />
            );
          }
          if (status === "loading" && !usage) {
            return (
              <div className="flex items-center justify-center px-4 py-8">
                <LoadingState label={t("overview.aggregatingTokens")} />
              </div>
            );
          }
          if (visibleRows.length === 0) {
            return (
              <EmptyState
                action={
                  tokenCatalog.status === "error" ? (
                    <Button
                      disabled={!isReady}
                      onClick={onRefreshUsage}
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      {t("common.retry")}
                    </Button>
                  ) : undefined
                }
                className="border-0 py-8"
                title={t("overview.noAccessTokenUsage")}
              />
            );
          }
          // A retained summary survives a failed refresh so the panel does not
          // flash empty, but its numbers must not read as current. A blocked
          // gateway already returned above, so only a failed refresh reaches here.
          const stale = status === "error";
          const placeholder = "—";
          return (
            <div>
              {visibleRows.map((row) => {
                const requests = tokenRequestCount(row);
                const failureRate =
                  requests > 0 ? row.usage.failed_requests / requests : null;
                const amount =
                  billingStatus === "ready"
                    ? billingAmount(row.billing)
                    : billingStatus === "loading"
                      ? t("common.loading")
                      : t("pricing.unavailable");
                return (
                  <Button
                    className="grid h-auto min-h-14 w-full grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 rounded-none border-b bg-transparent px-4 py-2.5 text-left font-normal text-foreground hover:bg-muted"
                    key={row.id}
                    onClick={() => onOpenTokenRecords(row.id)}
                    type="button"
                    variant="ghost"
                  >
                    <span
                      className="min-w-0 truncate text-sm font-medium"
                      title={row.name}
                    >
                      {row.name}
                    </span>
                    <span className="text-right text-sm font-semibold tabular-nums">
                      {stale
                        ? placeholder
                        : formatCompactNumber(row.usage.total_tokens)}
                    </span>
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-micro text-muted-foreground tabular-nums">
                      <span>
                        {t("overview.tokenRequests", {
                          count: stale
                            ? placeholder
                            : formatExactNumber(requests),
                        })}
                      </span>
                      {!stale &&
                      failureRate !== null &&
                      row.usage.failed_requests > 0 ? (
                        <span
                          title={t("overview.tokenFailureDetail", {
                            count: formatExactNumber(row.usage.failed_requests),
                            total: formatExactNumber(requests),
                          })}
                        >
                          <span aria-hidden="true" className="mr-2">
                            ·
                          </span>
                          {t("overview.tokenFailureRate", {
                            rate: `${(failureRate * 100).toFixed(1)}%`,
                          })}
                        </span>
                      ) : null}
                    </span>
                    <span className="self-start text-right text-micro text-muted-foreground tabular-nums">
                      {amount}
                    </span>
                  </Button>
                );
              })}
            </div>
          );
        }}
      </PaginatedList>
    </Panel>
  );
}

function OverviewWelcome({
  catalog,
  isNativeApp,
  isReady,
  isRestarting,
  onAddService,
  onManageServices,
  onManageTokens,
  onRestart,
  snapshot,
  tokenCatalog,
}: {
  catalog: ServiceCatalog;
  isNativeApp: boolean;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onRestart: () => void;
  snapshot: AppSnapshot | null;
  tokenCatalog: AccessTokenCatalog;
}) {
  const t = i18n.t.bind(i18n);
  const loading =
    !snapshot ||
    (isReady &&
      (catalog.status !== "ready" || tokenCatalog.status !== "ready"));
  const gatewayTone = snapshot ? phaseTone(snapshot.phase) : "pending";
  const gatewayLabel = snapshot
    ? phaseLabel(snapshot.phase)
    : t("core.phase.connecting");
  const description = loading
    ? t("overview.welcomeLoading")
    : !isNativeApp
      ? t("overview.welcomePreview")
      : !isReady
        ? t("overview.welcomeDisconnected")
        : t("overview.welcomeEmpty");

  return (
    <section
      aria-labelledby="welcome-heading"
      className="flex min-w-0 flex-1 flex-col"
      data-slot="overview-welcome"
    >
      <EmptyState
        className="min-h-72 flex-1"
        description={description}
        illustration={
          <div
            aria-hidden="true"
            className="flex w-64 max-w-full items-center justify-center gap-3"
          >
            <span className="flex size-11 shrink-0 items-center justify-center rounded-md border bg-muted/50 text-muted-foreground">
              <Bot className="size-5" />
            </span>
            <span className="min-w-2 flex-1 border-t border-dashed border-input" />
            <img
              alt=""
              className="size-16 shrink-0"
              height={64}
              src={astrlinkLogo}
              width={64}
            />
            <span className="min-w-2 flex-1 border-t border-dashed border-input" />
            <span className="flex size-11 shrink-0 items-center justify-center rounded-md border bg-muted/50 text-muted-foreground">
              <Server className="size-5" />
            </span>
          </div>
        }
        title={t("overview.welcomeTitle")}
        titleId="welcome-heading"
        variant="page"
        action={
          <div className="flex flex-wrap items-center justify-center gap-2">
            {loading ? (
              <LoadingState label={t("overview.loadingServices")} />
            ) : !isNativeApp ? (
              <Button
                onClick={onManageServices}
                type="button"
                variant="outline"
              >
                <Server aria-hidden="true" />
                {t("overview.welcomeBrowseServices")}
                <ArrowRight aria-hidden="true" />
              </Button>
            ) : !isReady ? (
              <Button
                disabled={isRestarting || snapshot?.phase === "stopping"}
                onClick={onRestart}
                type="button"
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn(
                    isRestarting && "animate-spin motion-reduce:animate-none",
                  )}
                />
                {t(
                  isRestarting
                    ? "overview.restarting"
                    : "overview.restartGateway",
                )}
              </Button>
            ) : (
              <>
                <Button onClick={onAddService} type="button">
                  <Plus aria-hidden="true" />
                  {t("overview.addService")}
                </Button>
                <Button onClick={onManageTokens} type="button" variant="ghost">
                  <Key aria-hidden="true" />
                  {t("overview.welcomeCreateToken")}
                </Button>
              </>
            )}
          </div>
        }
      />
      {isNativeApp && !isReady && snapshot?.last_error ? (
        <p
          className="mb-4 max-h-24 overflow-y-auto break-words text-xs text-danger-foreground"
          role="alert"
        >
          {snapshot.last_error}
        </p>
      ) : null}
      <dl className="grid min-w-0 border-y @min-[520px]/workspace-surface:grid-cols-3">
        {[
          {
            icon: <StatusDot tone={isNativeApp ? gatewayTone : "neutral"} />,
            label: t("nav.gateway"),
            value: isNativeApp
              ? gatewayLabel
              : t("overview.welcomeNotConnected"),
          },
          {
            icon: <Server aria-hidden="true" className="size-4" />,
            label: t("nav.services"),
            value: t(
              catalog.status === "ready"
                ? "overview.welcomeNoServices"
                : "overview.welcomeNotRead",
            ),
          },
          {
            icon: <Key aria-hidden="true" className="size-4" />,
            label: t("nav.tokens"),
            value: t(
              tokenCatalog.status === "ready"
                ? "overview.welcomeNoTokens"
                : "overview.welcomeNotRead",
            ),
          },
        ].map(({ icon, label, value }) => (
          <div
            className="flex min-w-0 items-center gap-3 px-3 py-4 max-[520px]:py-3"
            key={label}
          >
            <span className="flex size-8 shrink-0 items-center justify-center text-muted-foreground">
              {icon}
            </span>
            <div className="min-w-0">
              <dt className="text-xs font-medium">{label}</dt>
              <dd className="mt-0.5 text-xs text-muted-foreground">{value}</dd>
            </div>
          </div>
        ))}
      </dl>
    </section>
  );
}

function UsageRangeSelect({
  onChange,
  preset,
  calendar = false,
}: {
  onChange: (preset: UsageRangePreset) => void;
  preset: UsageRangePreset;
  calendar?: boolean;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <Select
      value={preset}
      onValueChange={(value) => {
        if (isUsageRangePreset(value)) onChange(value);
      }}
    >
      <SelectTrigger
        aria-label={t("overview.rangeLabel")}
        size="sm"
        className="min-w-24 border-border text-xs"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {USAGE_RANGE_PRESETS.filter(
          (candidate) =>
            !calendar || (candidate !== "1d" && candidate !== "7d"),
        ).map((candidate) => (
          <SelectItem key={candidate} value={candidate}>
            {t(`overview.range.${candidate}`)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

const AXIS_TICK = { fill: "var(--muted-foreground)", fontSize: 11 } as const;
const AXIS_LINE = { stroke: "var(--border)" } as const;

const DAY_STACK_BARS = [
  {
    active: "var(--primary-hover)",
    dataKey: "stack_input",
    fill: "var(--primary)",
    series: "input",
    swatch: "bg-primary",
    labelKey: "overview.input",
  },
  {
    active: "var(--violet-foreground)",
    dataKey: "stack_output",
    fill: "var(--violet)",
    series: "output",
    swatch: "bg-violet",
    labelKey: "overview.output",
  },
  {
    active: "var(--warning-foreground)",
    dataKey: "stack_cache_write",
    fill: "var(--warning)",
    series: "cache_write",
    swatch: "bg-warning",
    labelKey: "overview.cacheWrite",
  },
  {
    active: "var(--success-foreground)",
    dataKey: "stack_cache_read",
    fill: "var(--success)",
    series: "cache_read",
    swatch: "bg-success",
    labelKey: "overview.cacheRead",
  },
] as const;

function UsagePanelSkeleton() {
  const t = i18n.t.bind(i18n);
  return (
    <div
      aria-label={t("overview.aggregating")}
      className="grid"
      data-slot="usage-skeleton"
      role="status"
    >
      <MetricGroup className={USAGE_METRIC_GRID}>
        {Array.from({ length: 4 }, (_, index) => (
          <Metric
            key={index}
            label={
              <span className="block h-4 w-12 animate-pulse rounded bg-muted motion-reduce:animate-none" />
            }
            value={
              <span className="block h-7 w-20 animate-pulse rounded bg-muted motion-reduce:animate-none" />
            }
          />
        ))}
      </MetricGroup>
      <div className="px-4 py-3.5">
        <span className="block h-4 w-20 animate-pulse rounded bg-muted motion-reduce:animate-none" />
        <div className="mt-3 h-40 animate-pulse rounded-md bg-muted motion-reduce:animate-none" />
      </div>
    </div>
  );
}

type HeatmapPoint = UsageTotals & { date: string; hour?: number };
const heatmapCells = new WeakMap<HeatmapPoint[], Map<string, ActivityCell[]>>();

function prepareHeatmapCells(
  points: HeatmapPoint[],
  metric: "tokens" | "requests",
  locale: string,
): ActivityCell[] {
  let variants = heatmapCells.get(points);
  if (!variants) {
    variants = new Map();
    heatmapCells.set(points, variants);
  }
  const key = `${locale}:${metric}`;
  const cached = variants.get(key);
  if (cached) return cached;
  const t = i18n.getFixedT(locale);
  const cells = points.map((point) => {
    const dateLabel = chartPointLabel(point);
    return {
      key: point.date,
      date: point.date,
      label: `${dateLabel}, ${formatExactNumber(point.total_tokens)} Token, ${t("overview.dayRequests", { count: formatExactNumber(point.requests) })}, ${t("overview.dayFailed", { count: formatExactNumber(point.failed_requests) })}`,
      value:
        metric === "tokens"
          ? point.total_tokens
          : point.requests + point.failed_requests,
      detail: () => (
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums">
          <strong className="font-medium">{dateLabel}</strong>
          <span>{formatCompactNumber(point.total_tokens)} Token</span>
          <span>
            {t("overview.dayRequests", {
              count: formatExactNumber(point.requests),
            })}
          </span>
          {point.failed_requests > 0 ? (
            <span>
              {t("overview.dayFailed", {
                count: formatExactNumber(point.failed_requests),
              })}
            </span>
          ) : null}
        </span>
      ),
    };
  });
  variants.set(key, cells);
  return cells;
}

const UsageHeatmap = memo(function UsageHeatmap({
  points,
  status,
}: {
  points: Array<UsageTotals & { date: string; hour?: number }>;
  status: UsageStatus;
}) {
  const t = useT();
  const locale = i18n.language;
  const [metric, setMetric] = useState<"tokens" | "requests">("tokens");
  const unavailable = status === "blocked" || status === "error";
  const active = points.filter(
    (point) =>
      point.requests + point.failed_requests > 0 || point.total_tokens > 0,
  ).length;
  // The summary is retained while navigating. Its immutable day buckets can
  // reuse their labels on return; replacing the summary releases this cache.
  const cells = useMemo(
    () => prepareHeatmapCells(points, metric, locale),
    [points, metric, locale],
  );
  return (
    <div className="px-4 py-3.5">
      <div className="flex min-h-6 flex-wrap items-center justify-between gap-x-3 gap-y-2 text-micro text-muted-foreground">
        <span>
          {unavailable
            ? t("overview.heatmap")
            : t("overview.activeDays", { count: active, total: points.length })}
        </span>
        <div
          className="flex items-center gap-1"
          role="group"
          aria-label={t("overview.heatmapMetric")}
        >
          <Button
            size="xs"
            variant={metric === "tokens" ? "secondary" : "ghost"}
            aria-pressed={metric === "tokens"}
            onClick={() => setMetric("tokens")}
          >
            Token
          </Button>
          <Button
            size="xs"
            variant={metric === "requests" ? "secondary" : "ghost"}
            aria-pressed={metric === "requests"}
            onClick={() => setMetric("requests")}
          >
            {t("overview.requests")}
          </Button>
        </div>
      </div>
      <div className="mt-3 min-h-40">
        {unavailable ? (
          <EmptyState
            className="h-full border-0 py-4"
            title={t(
              status === "blocked"
                ? "overview.chartBlocked"
                : "overview.chartFailed",
            )}
          />
        ) : (
          <ActivityHeatmap
            cells={cells}
            emptyLabel={
              active === 0
                ? t("overview.noUsage")
                : t("overview.noTokensInRange")
            }
            label={t("overview.heatmapLabel")}
            caption={
              points.length
                ? `${points[0].date} ~ ${points[points.length - 1].date}`
                : ""
            }
            lessLabel={t("overview.less")}
            moreLabel={t("overview.more")}
            locale={i18n.language}
          />
        )}
      </div>
    </div>
  );
});

const UsageDayChart = memo(function UsageDayChart({
  grain,
  points,
  status,
}: {
  grain: "day" | "hour";
  points: Array<UsageTotals & { date: string; hour?: number }>;
  status: UsageStatus;
}) {
  const t = useT();
  const unavailable = status === "blocked" || status === "error";
  const chartRef = useRef<HTMLDivElement>(null);
  const stackedPoints = points.map((point) => {
    const stack = dayTokenStack(point);
    return {
      ...point,
      slot: chartSlot(point),
      stack_input: stack.input,
      stack_output: stack.output,
      stack_cache_write: stack.cache_write,
      stack_cache_read: stack.cache_read,
    };
  });
  const hourly = grain === "hour";
  const empty =
    status === "ready" &&
    !points.some((point) => point.requests > 0 || point.total_tokens > 0);

  return (
    <div className="px-4 py-3.5" data-slot="usage-chart">
      <div className="flex min-h-6 flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <span className="text-micro font-medium text-muted-foreground">
          {t(hourly ? "overview.hourlyTokens" : "overview.dailyTokens")}
        </span>
        <div className="flex flex-wrap items-center justify-end gap-2">
          {DAY_STACK_BARS.map((bar) => (
            <span
              className="inline-flex items-center gap-1 text-micro text-muted-foreground"
              key={bar.series}
            >
              <span
                aria-hidden="true"
                className={cn("size-1.5 rounded-sm", bar.swatch)}
              />
              {t(bar.labelKey)}
            </span>
          ))}
        </div>
      </div>
      <div
        aria-label={t(
          hourly ? "overview.hourlyTokensChart" : "overview.dailyTokensChart",
        )}
        className="relative mt-3 h-52 @max-[440px]/workspace-surface:h-64"
        ref={chartRef}
        role="img"
      >
        {unavailable ? (
          <EmptyState
            className="h-full border-0 py-4"
            title={t(
              status === "blocked"
                ? "overview.chartBlocked"
                : "overview.chartFailed",
            )}
          />
        ) : (
          <ResponsiveContainer height="100%" width="100%">
            <BarChart
              barCategoryGap="22%"
              data={stackedPoints}
              margin={{ top: 4, right: 4, left: 0, bottom: 0 }}
            >
              <CartesianGrid
                stroke="var(--border)"
                strokeDasharray="3 3"
                vertical={false}
              />
              <XAxis
                axisLine={AXIS_LINE}
                dataKey={hourly ? "slot" : "date"}
                interval={hourly ? 2 : xAxisInterval(points.length)}
                tick={AXIS_TICK}
                tickFormatter={(value: string) =>
                  hourly ? hourTickLabel(value) : dayLabel(value, "short")
                }
                tickLine={false}
              />
              <YAxis
                axisLine={false}
                tick={AXIS_TICK}
                tickFormatter={(value: number) => formatCompactNumber(value)}
                tickLine={false}
                width={56}
              />
              <Tooltip
                content={(props) => (
                  <DayTooltip {...props} originRef={chartRef} />
                )}
                cursor={{ fill: "var(--accent)" }}
                isAnimationActive={false}
                wrapperStyle={{ display: "none" }}
              />
              {DAY_STACK_BARS.map((bar) => (
                <Bar
                  activeBar={{ fill: bar.active }}
                  dataKey={bar.dataKey}
                  fill={bar.fill}
                  isAnimationActive={false}
                  key={bar.series}
                  maxBarSize={28}
                  shape={DAY_BAR_SHAPES[bar.series]}
                  stackId="tokens"
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
        {empty ? (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center pb-6">
            <span className="rounded-md bg-card px-3 py-2 text-xs text-muted-foreground">
              {t("overview.noUsage")}
            </span>
          </div>
        ) : null}
      </div>
    </div>
  );
});

function dayBarShape(series: DayTokenStackSeries) {
  return function DayBarShape(props: BarShapeProps) {
    const { fill, height, payload, width, x, y } = props;
    if (x == null || y == null || width == null || height == null) return null;
    const date =
      payload && typeof payload === "object" && "date" in payload
        ? String(payload.date)
        : "";
    const hour =
      payload && typeof payload === "object" && "hour" in payload
        ? String((payload as { hour?: number }).hour)
        : undefined;
    const barHeight = Math.max(0, height);
    return (
      <rect
        data-date={date}
        data-hour={hour}
        data-series={series}
        data-testid="usage-day"
        fill={fill}
        height={barHeight}
        rx={isTopStackSegment(payload, series) ? 2 : 0}
        width={width}
        x={x}
        y={y}
      />
    );
  };
}

function isTopStackSegment(
  payload: unknown,
  series: DayTokenStackSeries,
): boolean {
  if (!payload || typeof payload !== "object") return series === "input";
  const row = payload as {
    stack_cache_read?: number;
    stack_cache_write?: number;
    stack_output?: number;
  };
  const read = Number(row.stack_cache_read) || 0;
  const write = Number(row.stack_cache_write) || 0;
  const output = Number(row.stack_output) || 0;
  if (series === "cache_read") return read > 0;
  if (series === "cache_write") return write > 0 && read === 0;
  if (series === "output") return output > 0 && write === 0 && read === 0;
  return output === 0 && write === 0 && read === 0;
}

const DAY_BAR_SHAPES = {
  input: dayBarShape("input"),
  output: dayBarShape("output"),
  cache_write: dayBarShape("cache_write"),
  cache_read: dayBarShape("cache_read"),
} as const;

function DayTooltip({
  active,
  coordinate,
  originRef,
  payload,
}: TooltipContentProps & { originRef: RefObject<HTMLDivElement | null> }) {
  const cardRef = useRef<HTMLDivElement>(null);
  const [placed, setPlaced] = useState<{ left: number; top: number } | null>(
    null,
  );
  const bucket = payload[0]?.payload as
    | (UsageDayBucket & { hour?: number })
    | undefined;
  const origin = originRef.current?.getBoundingClientRect();
  const anchorX = (origin?.left ?? 0) + (coordinate?.x ?? 0);
  const anchorY = (origin?.top ?? 0) + (coordinate?.y ?? 0);

  useLayoutEffect(() => {
    if (!active || !bucket || coordinate == null) {
      setPlaced(null);
      return;
    }
    const card = cardRef.current;
    if (!card) return;
    const rect = card.getBoundingClientRect();
    const next = placeFloatingCard({
      anchorX,
      anchorY,
      height: rect.height,
      viewportHeight: window.innerHeight,
      viewportWidth: window.innerWidth,
      width: rect.width,
    });
    setPlaced((prev) =>
      prev && prev.left === next.left && prev.top === next.top ? prev : next,
    );
  }, [active, anchorX, anchorY, bucket?.date, bucket?.hour, coordinate]);

  if (!active || payload.length === 0 || coordinate == null || !bucket) {
    return null;
  }
  const t = i18n.t.bind(i18n);
  return createPortal(
    <div
      className="grid w-max max-w-xs gap-1 rounded-sm border bg-card px-2.5 py-2 text-left text-micro text-foreground"
      data-slot="usage-day-tooltip"
      ref={cardRef}
      style={{
        left: placed?.left ?? anchorX,
        pointerEvents: "none",
        position: "fixed",
        top: placed?.top ?? anchorY,
        visibility: placed ? "visible" : "hidden",
        zIndex: 50,
      }}
    >
      <strong className="text-xs font-medium">{chartPointLabel(bucket)}</strong>
      <span className="tabular-nums">
        {t("overview.dayTokens", {
          compact: formatCompactNumber(bucket.total_tokens),
          exact: formatExactNumber(bucket.total_tokens),
        })}
      </span>
      <span className="tabular-nums">
        {t("overview.dayRequests", {
          count: formatExactNumber(bucket.requests),
        })}
      </span>
      {bucket.failed_requests > 0 ? (
        <span className="tabular-nums">
          {t("overview.dayFailed", {
            count: formatExactNumber(bucket.failed_requests),
          })}
        </span>
      ) : null}
      <span className="flex items-center gap-1.5 tabular-nums">
        <span aria-hidden="true" className="size-1.5 rounded-sm bg-primary" />
        {t("overview.input")}: {formatCompactNumber(bucket.input_tokens)}
      </span>
      <span className="flex items-center gap-1.5 tabular-nums">
        <span aria-hidden="true" className="size-1.5 rounded-sm bg-violet" />
        {t("overview.output")}: {formatCompactNumber(bucket.output_tokens)}
      </span>
      <span className="flex items-center gap-1.5 tabular-nums">
        <span aria-hidden="true" className="size-1.5 rounded-sm bg-success" />
        {t("overview.cacheRead")}:{" "}
        {formatCompactNumber(bucket.cache_read_tokens)}
      </span>
      <span className="flex items-center gap-1.5 tabular-nums">
        <span aria-hidden="true" className="size-1.5 rounded-sm bg-warning" />
        {t("overview.cacheWrite")}:{" "}
        {formatCompactNumber(bucket.cache_write_tokens)}
      </span>
      <span className="tabular-nums">
        {t("overview.cacheHitRate")}: {formatCacheHitPercent(bucket)}
      </span>
    </div>,
    document.body,
  );
}

function xAxisInterval(count: number): number | "preserveStartEnd" {
  if (count <= 7) return 0;
  return Math.max(1, Math.ceil(count / 5) - 1);
}

function ServiceUsageBody({
  catalog,
  catalogUnknown,
  isReady,
  onAddService,
  onOpenService,
  onRefreshServices,
  rows,
  visibleRows,
  showBar,
  status,
}: {
  catalog: ServiceCatalog;
  catalogUnknown: boolean;
  isReady: boolean;
  onAddService: () => void;
  onOpenService: (serviceId: string) => void;
  onRefreshServices: () => void;
  rows: MergedServiceUsage[];
  visibleRows: MergedServiceUsage[];
  showBar: boolean;
  status: UsageStatus;
}) {
  const t = i18n.t.bind(i18n);
  if (catalogUnknown) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 border-b px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">
          {t("overview.servicesAfterReady")}
        </p>
        <span className="text-xs text-muted-foreground">
          {t("overview.noCatalog")}
        </span>
      </div>
    );
  }

  if (catalog.status === "error" && catalog.items.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 border-b px-4 py-8 text-center">
        <p className="text-sm text-danger-foreground">
          {catalog.error ?? t("overview.readServicesFailed")}
        </p>
        <Button
          size="sm"
          variant="outline"
          onClick={onRefreshServices}
          type="button"
        >
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  if (catalog.status === "loading" && catalog.items.length === 0) {
    return (
      <div className="flex items-center justify-center border-b px-4 py-8">
        <LoadingState label={t("overview.loadingServices")} />
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <div className="border-b px-4 py-6">
        <EmptyState
          action={
            <Button
              disabled={!isReady}
              onClick={onAddService}
              size="sm"
              type="button"
            >
              {t("overview.addService")}
            </Button>
          }
          className="border-0 py-2"
          description={t("overview.emptyServicesHint")}
          title={t("overview.emptyServices")}
        />
      </div>
    );
  }

  return (
    <div>
      {visibleRows.map((row) => (
        <UsageBreakdownRow
          barPercent={usageBarPercent(row.total_tokens, rows)}
          key={row.id ?? "unattributed"}
          label={row.name}
          leading={
            <StatusDot
              tone={
                row.enabled === true
                  ? "positive"
                  : row.enabled === false
                    ? "neutral"
                    : "pending"
              }
            />
          }
          onClick={
            row.in_catalog && row.id
              ? () => onOpenService(row.id as string)
              : undefined
          }
          requests={row.requests}
          showBar={showBar}
          status={status}
          tokens={row.total_tokens}
        />
      ))}
    </div>
  );
}

function ModelUsageBody({
  rows,
  visibleRows,
  showBar,
  status,
}: {
  rows: UsageGroup[];
  visibleRows: UsageGroup[];
  showBar: boolean;
  status: UsageStatus;
}) {
  const t = i18n.t.bind(i18n);
  if (status === "blocked" || (status === "error" && rows.length === 0)) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">
          {status === "blocked"
            ? t("overview.usageBlocked")
            : t("overview.usageFailed")}
        </p>
      </div>
    );
  }

  if (status === "loading" && rows.length === 0) {
    return (
      <div className="flex items-center justify-center px-4 py-8">
        <LoadingState label={t("overview.aggregatingModels")} />
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">
          {t("overview.noSuccessfulRequests")}
        </p>
        <span className="text-xs text-muted-foreground">
          {t("overview.modelUsageHint")}
        </span>
      </div>
    );
  }

  return (
    <div>
      {visibleRows.map((row) => {
        const label = modelUsageLabel(row.id);
        return (
          <UsageBreakdownRow
            barPercent={usageBarPercent(row.total_tokens, rows)}
            key={row.id ?? "unknown-model"}
            label={label}
            leading={<ModelBrandIcon model={row.id} />}
            requests={row.requests}
            showBar={showBar}
            status={status}
            tokens={row.total_tokens}
          />
        );
      })}
    </div>
  );
}

function UsageBreakdownRow({
  barPercent,
  label,
  leading,
  onClick,
  requests,
  showBar,
  status,
  tokens,
}: {
  barPercent: number;
  label: string;
  leading?: ReactNode;
  onClick?: () => void;
  requests: number;
  showBar: boolean;
  status: UsageStatus;
  tokens: number;
}) {
  const t = i18n.t.bind(i18n);
  const requestMetric = compactMetric(requests, status);
  const content = (
    <>
      {leading}
      <span className="flex min-w-0 flex-1 flex-col">
        <strong
          className="overflow-hidden text-sm font-medium text-ellipsis whitespace-nowrap"
          title={label}
        >
          {label}
        </strong>
        {showBar ? (
          <span
            aria-hidden="true"
            className="mt-1.5 block h-1 overflow-hidden rounded-full bg-muted"
          >
            <span
              className="block h-full rounded-full bg-primary/40"
              style={{ width: `${barPercent}%` }}
            />
          </span>
        ) : null}
      </span>
      <span className="flex shrink-0 flex-col items-end">
        <CompactCount
          className="text-sm font-semibold tracking-tight"
          value={usageCountValue(tokens, status)}
        />
        <span
          className="text-micro text-muted-foreground tabular-nums"
          title={
            requestMetric.title
              ? t("overview.requestTimes", { value: requestMetric.title })
              : undefined
          }
        >
          {t("overview.requestTimes", { value: requestMetric.text })}
        </span>
      </span>
    </>
  );

  if (onClick) {
    return (
      <Button
        className="grid h-15 min-w-0 w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-none border-b bg-transparent px-4 py-2.5 text-left font-normal text-foreground hover:bg-muted"
        onClick={onClick}
        type="button"
        variant="ghost"
      >
        {content}
      </Button>
    );
  }

  return (
    <DataRow className="grid h-15 grid-cols-[auto_minmax(0,1fr)_auto] items-center py-2.5">
      {content}
    </DataRow>
  );
}

function UsageMetric({
  children,
  label,
  metric,
  icon,
}: {
  children?: ReactNode;
  label: string;
  icon?: ReactNode;
  metric?: CompactMetric;
}) {
  return (
    <Metric
      icon={icon}
      label={label}
      title={metric?.title ?? undefined}
      value={children ?? metric?.text}
    />
  );
}

function OverviewCount({
  label,
  unknown,
  value,
}: {
  label: string;
  unknown: boolean;
  value: number;
}) {
  return (
    <div className="flex items-baseline gap-1.5">
      <strong className="text-xs font-medium tabular-nums">
        {unknown ? "—" : value}
      </strong>
      <span className="text-micro text-muted-foreground">{label}</span>
    </div>
  );
}

/**
 * A compact rendering plus the exact digits it hides. `title` is null when the
 * compact form is already exact, so small numbers get no redundant tooltip.
 */
interface CompactMetric {
  text: string;
  title: string | null;
}

function usageCountValue(
  value: number | undefined,
  status: UsageStatus,
): number | null {
  if (value === undefined || status === "blocked" || status === "error") {
    return null;
  }
  return value;
}

function compactMetric(
  value: number | undefined,
  status: UsageStatus,
): CompactMetric {
  if (value === undefined || status === "blocked" || status === "error") {
    return { text: "—", title: null };
  }
  const text = formatCompactNumber(value);
  const exact = formatExactNumber(value);
  return { text, title: exact === text ? null : exact };
}

function cacheHitMetric(
  totals: UsageTotals | undefined,
  status: UsageStatus,
): string {
  if (!totals || status === "blocked" || status === "error") return "—";
  return formatCacheHitPercent(totals);
}

const dayFormats = new Map<string, Intl.DateTimeFormat>();

/** Renders a `YYYY-MM-DD` bucket key in the active locale. */
function dayLabel(date: string, width: "long" | "short"): string {
  const [year, month, day] = date.split("-").map(Number);
  if (!year || !month || !day) return date;
  const locale = i18n.language === "zh-CN" ? "zh-CN" : "en";
  const key = `${locale}:${width}`;
  let format = dayFormats.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat(locale, {
      ...(width === "long" ? { year: "numeric" } : {}),
      month: "short",
      day: "numeric",
    });
    dayFormats.set(key, format);
  }
  return format.format(new Date(year, month - 1, day));
}

function chartPointLabel(point: { date: string; hour?: number }): string {
  const day = dayLabel(point.date, "long");
  if (point.hour == null) return day;
  return `${day} ${String(point.hour).padStart(2, "0")}:00`;
}

function chartSlot(point: { date: string; hour?: number }): string {
  if (point.hour == null) return point.date;
  return `${point.date}T${String(point.hour).padStart(2, "0")}`;
}

function hourTickLabel(slot: string): string {
  const hour = slot.split("T")[1];
  if (hour == null) return slot;
  return `${String(Number(hour)).padStart(2, "0")}:00`;
}
