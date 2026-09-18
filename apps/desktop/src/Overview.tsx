import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { ArrowRight, Check, Copy, Plus, RefreshCw } from "@/components/icons";

import { CompactCount } from "@/components/CompactCount";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ExternalLink } from "@/components/ExternalLink";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup, MetricValuePair } from "@/components/Metric";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { formatCompactNumber, formatExactNumber } from "./format-compact-number";
import { i18n } from "./i18n";
import { PageHeader } from "./PageHeader";
import { placeFloatingCard } from "./place-floating-card";
import type { Service } from "./service-model";
import type { ServiceCatalogStatus } from "./ServiceManager";
import {
  dayTokenStack,
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
  type UsageTotals,
} from "./usage-range";

export interface ServiceCatalog {
  status: ServiceCatalogStatus;
  items: Service[];
  error: string | null;
  stale: boolean;
}

const USAGE_METRIC_GRID =
  "border-b @min-[640px]/workspace-surface:grid-cols-[1fr_1fr_1.6fr_0.8fr]";

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
  const capabilities = snapshot?.capabilities ?? null;
  const conversionEngine = capabilities?.conversion_engine;
  const statusTone = snapshot ? phaseTone(snapshot.phase) : "neutral";
  const statusLabel = snapshot ? phaseLabel(snapshot.phase) : t("core.phase.connecting");
  const enabledCount = catalog.items.filter((service) => service.enabled).length;
  const catalogUnknown =
    catalog.status === "blocked" && catalog.items.length === 0;
  const tokensUnknown =
    tokenCatalog.status === "blocked" && tokenCatalog.items.length === 0;
  const inferenceURL = snapshot?.ready?.inference_url ?? "";
  const apiAddressLabel = t("overview.apiAddress");
  const apiCopied = copyFeedback === t("copy.copiedNamed", { label: apiAddressLabel });
  const summary = usage.summary;
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
    [t("overview.desktopVersion"), snapshot?.app_version ?? t("common.unknown")],
    [
      t("overview.gatewayVersion"),
      snapshot?.version?.core_version ?? snapshot?.ready?.core_version ?? t("overview.pending"),
    ],
    [t("overview.process"), snapshot?.pid ? `PID ${snapshot.pid}` : t("overview.notRunning")],
    [t("overview.controlListen"), snapshot?.ready?.control_url ?? t("overview.unassigned")],
    [t("overview.controlContract"), snapshot?.version?.control_api_version ?? t("overview.pendingHandshake")],
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

  return (
    <div className="grid min-w-0 gap-4">
      <PageHeader
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <StatusBadge tone={statusTone}>
              {isReady ? t("overview.gatewayHealthy") : statusLabel}
            </StatusBadge>
            {isNativeApp && !isReady ? (
              <Button
                variant="outline"
                disabled={isRestarting || snapshot?.phase === "stopping"}
                onClick={onRestart}
                type="button"
              >
                {isRestarting ? t("overview.restarting") : t("overview.restartGateway")}
              </Button>
            ) : null}
          </div>
        }
        className="mb-0 items-center border-0 pb-1"
        description={t("overview.description")}
        title={t("overview.title")}
      />

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
              <code className="min-w-0 truncate font-mono text-xs font-medium tracking-tight" title={inferenceURL}>
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
              <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
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
          <p className="border-t px-4 py-2 text-xs text-danger-foreground" role="alert">
            {copyError}
          </p>
        ) : copyFeedback ? (
          <p className="border-t px-4 py-2 text-xs text-success-foreground" role="status">
            {copyFeedback}
          </p>
        ) : null}
      </Panel>

      <Panel
        aria-busy={usage.status === "loading"}
        aria-labelledby="usage-heading"
      >
        <PanelHeader
          className="items-center max-[560px]:flex-wrap"
          actions={
            <>
              <UsageRangeTabs
                onChange={onUsagePresetChange}
                preset={usagePreset}
              />
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
                    "motion-reduce:animate-none",
                    usage.status === "loading" && "animate-spin motion-reduce:animate-none",
                  )}
                />
                <span className="sr-only">{t("common.refresh")}</span>
              </Button>
            </>
          }
        >
          <h2
            className="text-sm font-semibold"
            id="usage-heading"
          >
            {t("overview.usageSummary")}
          </h2>
        </PanelHeader>

        {summary?.capped && usage.status !== "loading" ? (
          <div className="flex flex-wrap items-center gap-1.5 border-b px-4 py-2">
            <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
              {t("overview.recentCapped", {
                count: formatExactNumber(summary.scanned_records),
              })}
            </Badge>
          </div>
        ) : null}

        {usage.status === "loading" && !summary ? (
          <UsagePanelSkeleton />
        ) : (
          <div className="relative">
            <MetricGroup className={USAGE_METRIC_GRID}>
              <UsageMetric
                label={t("overview.requests")}
                metric={compactMetric(totals?.requests, usage.status)}
              />
              <UsageMetric label={t("overview.totalTokens")}>
                <CompactCount
                  value={usageCountValue(totals?.total_tokens, usage.status)}
                />
              </UsageMetric>
              <UsageMetric label={t("overview.inputOutput")}>
                <MetricValuePair
                  first={<CompactCount value={usageCountValue(totals?.input_tokens, usage.status)} />}
                  second={<CompactCount value={usageCountValue(totals?.output_tokens, usage.status)} />}
                />
              </UsageMetric>
              <UsageMetric
                label={t("overview.cacheHits")}
                metric={{ text: cacheHitMetric(totals, usage.status), title: null }}
              />
            </MetricGroup>

            <UsageDayChart
              grain={usagePreset === "1d" ? "hour" : "day"}
              points={
                usagePreset === "1d"
                  ? (summary?.by_hour ?? [])
                  : (summary?.by_day ?? [])
              }
              status={usage.status}
            />

            {usage.status === "loading" ? (
              <div
                className="absolute inset-0 z-10 flex items-center justify-center bg-card/70"
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

        {usage.status === "error" && usage.error ? (
          <p className="border-t px-4 py-2.5 text-xs text-danger-foreground" role="alert">
            {usage.error}
          </p>
        ) : null}
        <div className="border-t px-4 py-2.5">
          <HelpDisclosure title={t("overview.usageDetails")}>
            <p>{t(usagePreset === "1d" ? "overview.hourlyUsageNote" : "overview.usageNote")}</p>
            <p>{t("overview.estimatedCost")} — · {t("overview.costComingSoon")}</p>
          </HelpDisclosure>
        </div>
      </Panel>

      <div className="grid min-w-0 grid-cols-1 items-start gap-4 @min-[640px]/workspace-surface:grid-cols-2">
        <Panel aria-labelledby="usage-by-service-heading">
          <PanelHeader
            className="min-h-14 flex-wrap items-center"
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
                  <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
                    {t("overview.waitingGateway")}
                  </Badge>
                ) : catalog.stale ? (
                  <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
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
            <h2
              className="text-sm font-semibold"
              id="usage-by-service-heading"
            >
              {t("overview.byService")}
            </h2>
          </PanelHeader>

          {/* Keep the list viewport stable when a range changes its row count. */}
          <div className="h-56 overflow-y-auto overscroll-contain">
            <ServiceUsageBody
              catalog={catalog}
              catalogUnknown={catalogUnknown}
              isReady={isReady}
              onAddService={onAddService}
              onOpenService={onOpenService}
              onRefreshServices={onRefreshServices}
              rows={serviceRows}
              showBar={usage.status === "ready" && serviceRows.some((row) => row.total_tokens > 0)}
              status={usage.status}
            />
          </div>

          <Button
            className="h-auto w-full justify-between rounded-none border-t px-4 py-2.5 text-xs font-medium text-text-secondary no-underline hover:bg-muted hover:text-foreground hover:no-underline"
            onClick={onManageServices}
            type="button"
            variant="link"
          >
            {t("overview.manageAllServices")}
            <ArrowRight className="size-4" />
          </Button>
        </Panel>

        <Panel aria-labelledby="usage-by-model-heading">
          <PanelHeader
            className="min-h-14 items-center"
            actions={
              <span className="text-micro text-muted-foreground">{t("overview.rankedByTokens")}</span>
            }
          >
            <h2
              className="text-sm font-semibold"
              id="usage-by-model-heading"
            >
              {t("overview.byModel")}
            </h2>
          </PanelHeader>
          <div className="h-56 overflow-y-auto overscroll-contain">
            <ModelUsageBody
              rows={modelRows}
              showBar={usage.status === "ready" && modelRows.some((row) => row.total_tokens > 0)}
              status={usage.status}
            />
          </div>
          <p className="border-t px-4 py-2.5 text-xs text-muted-foreground">
            {usage.status === "ready"
              ? t("overview.modelCount", { count: modelRows.length })
              : usage.status === "loading"
                ? t("overview.aggregatingModels")
                : t(usage.status === "blocked" ? "overview.waitingGateway" : "overview.waitingRefresh")}
          </p>
        </Panel>
      </div>

      <div className="px-1 pb-1">
        <HelpDisclosure title={t("overview.systemDetails")}>
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
        </HelpDisclosure>
      </div>
    </div>
  );
}

function UsageRangeTabs({
  onChange,
  preset,
}: {
  onChange: (preset: UsageRangePreset) => void;
  preset: UsageRangePreset;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <Tabs
      onValueChange={(value) => {
        if (isUsageRangePreset(value)) onChange(value);
      }}
      value={preset}
    >
      <TabsList aria-label={t("overview.rangeLabel")}>
        {USAGE_RANGE_PRESETS.map((candidate) => (
          <TabsTrigger key={candidate} type="button" value={candidate}>
            {t(`overview.range.${candidate}`)}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
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
            label={<span className="block h-4 w-12 animate-pulse rounded bg-muted motion-reduce:animate-none" />}
            value={<span className="block h-7 w-20 animate-pulse rounded bg-muted motion-reduce:animate-none" />}
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

function UsageDayChart({
  grain,
  points,
  status,
}: {
  grain: "day" | "hour";
  points: Array<UsageTotals & { date: string; hour?: number }>;
  status: UsageStatus;
}) {
  const t = i18n.t.bind(i18n);
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
  const empty = status === "ready" && !points.some((point) => point.requests > 0 || point.total_tokens > 0);

  return (
    <div className="px-4 py-3.5">
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
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
        className="relative mt-3 h-40"
        ref={chartRef}
        role="img"
      >
        {unavailable ? (
          <EmptyState
            className="h-full border-0 py-4"
            title={t(status === "blocked" ? "overview.chartBlocked" : "overview.chartFailed")}
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
}

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
      <strong className="text-xs font-medium">
        {chartPointLabel(bucket)}
      </strong>
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
        {t("overview.cacheRead")}: {formatCompactNumber(bucket.cache_read_tokens)}
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
  showBar: boolean;
  status: UsageStatus;
}) {
  const t = i18n.t.bind(i18n);
  if (catalogUnknown) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 border-b px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t("overview.servicesAfterReady")}</p>
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
        <Button size="sm" variant="outline" onClick={onRefreshServices} type="button">
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
            <Button disabled={!isReady} onClick={onAddService} size="sm" type="button">
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
      {rows.map((row) => (
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
  showBar,
  status,
}: {
  rows: UsageGroup[];
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
        <p className="text-sm text-text-secondary">{t("overview.noSuccessfulRequests")}</p>
        <span className="text-xs text-muted-foreground">
          {t("overview.modelUsageHint")}
        </span>
      </div>
    );
  }

  return (
    <div>
      {rows.map((row) => {
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
        <strong className="overflow-hidden text-sm font-medium text-ellipsis whitespace-nowrap" title={label}>
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
        className="grid h-auto min-w-0 w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-none border-b bg-transparent px-4 py-2.5 text-left font-normal text-foreground hover:bg-muted"
        onClick={onClick}
        type="button"
        variant="ghost"
      >
        {content}
      </Button>
    );
  }

  return (
    <DataRow className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center py-2.5">
      {content}
    </DataRow>
  );
}

function UsageMetric({
  children,
  label,
  metric,
}: {
  children?: ReactNode;
  label: string;
  metric?: CompactMetric;
}) {
  return (
    <Metric
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

/** Renders a `YYYY-MM-DD` bucket key in the active locale. */
function dayLabel(date: string, width: "long" | "short"): string {
  const [year, month, day] = date.split("-").map(Number);
  if (!year || !month || !day) return date;
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
    ...(width === "long" ? { year: "numeric" } : {}),
    month: "short",
    day: "numeric",
  }).format(new Date(year, month - 1, day));
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
