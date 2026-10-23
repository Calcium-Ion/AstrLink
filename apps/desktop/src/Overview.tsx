import { isWebEdition } from "./edition";
import { useBillingSummary, type BillingStatus } from "./BillingOverview";
import {
  memo,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import {
  Activity,
  ArrowRight,
  Boxes,
  Check,
  CircleDollarSign,
  Copy,
  Plus,
  RefreshCw,
  RotateCcw,
  Settings,
  SlidersHorizontal,
  SquareStack,
} from "@/components/icons";

import { ActionHint } from "@/components/ActionHint";
import { ChartTooltipCard } from "@/components/ChartTooltip";
import { BreakdownRow, breakdownShare } from "@/components/BreakdownRow";
import { CompactCount } from "@/components/CompactCount";
import { EmptyState } from "@/components/EmptyState";
import { ExternalLink } from "@/components/ExternalLink";
import {
  ActivityHeatmap,
  type ActivityCell,
} from "@/components/ActivityHeatmap";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { SegmentedControl } from "@/components/SegmentedControl";
import { InferencePortNotice } from "@/components/InferencePortNotice";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup } from "@/components/Metric";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel, PanelHeader } from "@/components/Panel";
import { OrderedList } from "@/components/OrderedList";
import { useOverviewLayout, type OverviewModuleId } from "./overview-layout";
import { PaginatedList } from "@/components/PaginatedList";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { TokenClientMarks } from "@/components/TokenClientMarks";
import { UsageRangePicker } from "@/components/UsageRangePicker";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip as ChartTooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { BarShapeProps, TooltipContentProps } from "recharts";

import type { AccessTokenCatalog } from "./AccessTokenManager";
import { phaseLabel, phaseTone, type AppSnapshot } from "./core-model";
import {
  billingAmount,
  type BillingAmounts,
  type BillingSummary,
} from "./pricing-model";
import { billingNoteText } from "./PricingWorkspace";
import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import type { CopyState } from "./copy-feedback";
import { clientsUsing, useClientConfigs } from "./use-client-configs";
import type { RecordsTarget } from "./request-live-model";
import { statusLabel, type RequestStatus } from "./request-record-model";
import { currentLocale, i18n, useT } from "./i18n";
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
  hourlyUsageWindow,
  mergeCatalogServiceUsage,
  modelUsageLabel,
  usageRangeKey,
  USAGE_RANGE_PRESETS,
  type DayTokenStackSeries,
  type MergedServiceUsage,
  type UsageDayBucket,
  type UsageGroup,
  type UsageRange,
  type UsageState,
  type UsageStatus,
  type UsageSummary,
  type UsageStatusCounts,
  type UsageTotals,
} from "./usage-range";

export interface ServiceCatalog {
  status: ServiceCatalogStatus;
  items: Service[];
  error: string | null;
  stale: boolean;
}

// Four equal tiles, as in the privacy statistics; secondary numbers go in a
// tile's detail line so a long value never wraps the row.
const USAGE_METRIC_GRID =
  "gap-3 bg-transparent [&>div]:rounded-md [&>div]:border";

const USAGE_BREAKDOWN_HEADER = "min-h-14 flex-wrap items-center py-2";

/** Rows a breakdown page shows; see `PaginatedList`. */
const BREAKDOWN_PAGE_SIZE = 5;
/**
 * Room for one to five rows. Side-by-side lists share the height of the
 * longer page, so their footers stay level without holding empty rows.
 */
const BREAKDOWN_LIST_HEIGHT = [
  "min-h-15",
  "min-h-30",
  "min-h-45",
  "min-h-60",
  "min-h-75",
] as const;

export function Overview({
  catalog,
  copyState,
  isNativeApp,
  isReady,
  isRestarting,
  onAddService,
  onCopy,
  onManageServices,
  onManageTokens,
  onOpenOnboarding,
  onDismissOnboardingHint,
  showOnboardingHint = false,
  onOpenRecords,
  onOpenService,
  onRefreshServices,
  onRefresh,
  onRestart,
  onUsageRangeChange,
  snapshot,
  tokenCatalog,
  usage,
  usageRange,
}: {
  catalog: ServiceCatalog;
  copyState: CopyState;
  isNativeApp: boolean;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onCopy: (value: string) => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onOpenOnboarding?: () => void;
  onDismissOnboardingHint?: () => void;
  showOnboardingHint?: boolean;
  /** Request records narrowed to a row; the caller adds the usage range. */
  onOpenRecords: (target: RecordsTarget) => void;
  /** The provider's settings. */
  onOpenService: (serviceId: string) => void;
  onRefreshServices: () => void;
  onRefresh: () => void;
  onRestart: () => void;
  onUsageRangeChange: (range: UsageRange) => void;
  snapshot: AppSnapshot | null;
  tokenCatalog: AccessTokenCatalog;
  usage: UsageState;
  usageRange: UsageRange;
}) {
  const t = i18n.t.bind(i18n);
  const [view, setView] = useState<"chart" | "heatmap">(() =>
    usageRange === "1d" || usageRange === "7d" ? "chart" : "heatmap",
  );
  const rangeKey = usageRangeKey(usageRange);
  const [editingLayout, setEditingLayout] = useState(false);
  const layout = useOverviewLayout();
  const footerRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!showOnboardingHint) return;
    const scroller = footerRef.current?.closest<HTMLElement>(
      '[data-slot="overview-content"]',
    );
    scroller?.scrollTo?.({
      top: scroller.scrollHeight,
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
        ? "instant"
        : "smooth",
    });
  }, [showOnboardingHint]);
  const visibleModules = layout.order.filter(
    (id) => id !== "system" && !layout.hidden.includes(id),
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

  const inferenceURL = snapshot?.ready?.client_inference_url ?? "";
  const apiAddressLabel = t("overview.apiAddress");
  const apiCopied = copyState === "copied";
  // A retained summary survives a failed refresh so the panel does not flash
  // empty, but its numbers must not be presented as current.
  const totals =
    usage.status === "blocked" || usage.status === "error"
      ? undefined
      : summary?.totals;
  const serviceRows = mergeCatalogServiceUsage(
    catalog.items,
    summary?.by_service ?? [],
  ).filter((row) => row.total_tokens > 0 || row.requests > 0);
  const modelRows = (summary?.by_model ?? []).filter(
    (row) => row.total_tokens > 0 || row.requests > 0,
  );
  const costs = breakdownCosts(billingSummary);
  const breakdownHeight =
    BREAKDOWN_LIST_HEIGHT[
      Math.min(
        BREAKDOWN_PAGE_SIZE,
        Math.max(1, serviceRows.length, modelRows.length),
      ) - 1
    ];
  // The grain follows the summary on screen, which a wider range replaces
  // only once it has loaded.
  const hourly = summary ? hourlyUsageWindow(summary.window) : false;
  const systemDetailRows: Array<[string, ReactNode] | null> = [
    isWebEdition
      ? null
      : [
          t("overview.desktopVersion"),
          snapshot?.app_version ?? t("common.unknown"),
        ],
    [
      t("overview.gatewayVersion"),
      snapshot?.version?.core_version ??
        snapshot?.ready?.core_version ??
        t("overview.pending"),
    ],
    isWebEdition
      ? null
      : [
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
  const systemDetails = systemDetailRows.filter((row) => row !== null);

  const moduleLabels: Record<OverviewModuleId, string> = {
    usage: t("overview.usageSummary"),
    "providers-models": t("overview.providersAndModels"),
    tokens: t("overview.statusAndTokens"),
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
                  usageRange === "1d"
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
                  (usageRange === "1d" || usageRange === "7d")
                )
                  onUsageRangeChange("1y");
              }}
              options={[
                { value: "heatmap", label: t("overview.heatmap") },
                { value: "chart", label: t("overview.chart") },
              ]}
            />
            <span aria-hidden="true" className="h-4 w-px bg-border" />
            <UsageRangePicker
              ariaLabel={t("overview.rangeLabel")}
              className="min-w-24"
              onChange={(next) => {
                if (next !== null) onUsageRangeChange(next);
              }}
              // A calendar of one or seven days says less than a bar chart.
              presets={
                view === "heatmap"
                  ? USAGE_RANGE_PRESETS.filter(
                      (preset) => preset !== "1d" && preset !== "7d",
                    )
                  : USAGE_RANGE_PRESETS
              }
              value={usageRange}
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
              <UsageMetric
                detail={
                  totals && totals.input_tokens + totals.output_tokens > 0 ? (
                    <TokenSplit
                      input={totals.input_tokens}
                      output={totals.output_tokens}
                    />
                  ) : undefined
                }
                icon={<Boxes />}
                label={t("overview.totalTokens")}
              >
                <CompactCount
                  value={usageCountValue(totals?.total_tokens, usage.status)}
                />
              </UsageMetric>
              <UsageMetric
                icon={<SquareStack />}
                label={t("overview.cacheHits")}
                metric={{
                  text: cacheHitMetric(totals, usage.status),
                  title: null,
                }}
              />
              <UsageMetric
                detail={
                  billingSummary
                    ? billingNoteText(t, billingSummary) || undefined
                    : undefined
                }
                icon={<CircleDollarSign />}
                label={t("pricing.officialAmount")}
                metric={{
                  text: billingSummary ? billingAmount(billingSummary) : "—",
                  title: t("overview.costNote"),
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
                  grain={hourly ? "hour" : "day"}
                  points={
                    hourly ? (summary?.by_hour ?? []) : (summary?.by_day ?? [])
                  }
                  status={usage.status}
                />
              )}
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
            key={rangeKey}
            items={serviceRows}
            itemsClassName={breakdownHeight}
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
                costs={costs.byService}
                catalogUnknown={catalogUnknown}
                isReady={isReady}
                onAddService={onAddService}
                onOpenRecords={onOpenRecords}
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
            key={rangeKey}
            items={modelRows}
            itemsClassName={breakdownHeight}
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
                costs={costs.byModel}
                onOpenRecords={onOpenRecords}
                rows={modelRows}
                services={catalog.items}
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
      // How the calls ended over the range, beside the tokens that made them.
      <div className="grid min-w-0 grid-cols-1 gap-3 @min-[640px]/workspace-surface:grid-cols-2">
        <RequestStatusChart
          counts={
            usage.status === "blocked" || usage.status === "error"
              ? null
              : (summary?.status_counts ?? null)
          }
          grain={hourly ? "hour" : "day"}
          onOpenRecords={onOpenRecords}
          points={hourly ? (summary?.by_hour ?? []) : (summary?.by_day ?? [])}
          status={usage.status}
        />
        <TokenUsagePanel
          billingStatus={billingStatus}
          billingSummary={billingSummary}
          inferenceURL={inferenceURL}
          isReady={isReady}
          onOpenRecords={onOpenRecords}
          onRefresh={onRefresh}
          rangeKey={rangeKey}
          status={usage.status}
          tokenCatalog={tokenCatalog}
          tokensUnknown={tokensUnknown}
          usage={summary}
        />
      </div>
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
              onClick={() => onCopy(inferenceURL)}
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
        {copyState === "failed" ? (
          <p
            className="border-t px-4 py-2 text-xs text-danger-foreground"
            role="alert"
          >
            {t("copy.manualSelect")}
          </p>
        ) : apiCopied ? (
          <p
            className="border-t px-4 py-2 text-xs text-success-foreground"
            role="status"
          >
            {t("copy.copiedNamed", { label: apiAddressLabel })}
          </p>
        ) : null}
      </Panel>
    ),
    system: (
      <section
        aria-labelledby="system-details-heading"
        className="min-w-0 flex-1 py-1.5"
      >
        <HelpDisclosure
          title={
            <h2 className="font-medium" id="system-details-heading">
              {t("overview.systemDetails")}
            </h2>
          }
        >
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
      </section>
    ),
  };

  return (
    <ScrollWorkspace
      contentClassName="flex flex-col [overflow-anchor:none]"
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
                <Button
                  disabled={!isReady || usage.status === "loading"}
                  onClick={onRefresh}
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
                <StatusBadge tone={statusTone}>
                  {isReady ? t("overview.gatewayHealthy") : statusLabel}
                </StatusBadge>
                {isNativeApp && (!isReady || snapshot?.restart_pending) ? (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={isRestarting || snapshot?.phase === "stopping"}
                    onClick={onRestart}
                    type="button"
                  >
                    {isRestarting
                      ? t("overview.restarting")
                      : isReady
                        ? t("overview.restartToApply")
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
        {isNativeApp && !isReady && snapshot?.last_error ? (
          <p
            className="max-h-24 overflow-y-auto break-words text-xs text-danger-foreground"
            role="alert"
          >
            {snapshot.last_error}
          </p>
        ) : null}
        {!editingLayout && visibleModules.length === 0 ? (
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
              items={(editingLayout
                ? layout.order.filter((id) => id !== "system")
                : visibleModules
              ).map((id) => ({ id }))}
              label={t("overview.customizeLayout")}
              onChange={(items) =>
                layout.save([...items.map(({ id }) => id), "system"])
              }
              variant="modules"
            >
              {({ id }) => !layout.hidden.includes(id) && moduleContent[id]}
            </OrderedList>
          </>
        )}
      </div>
      <div
        ref={footerRef}
        className="mt-auto flex min-w-0 shrink-0 items-start justify-between gap-3 px-1 pt-3"
        data-slot="overview-footer"
      >
        {editingLayout ? (
          <Label className="flex min-h-7 items-center gap-2 text-xs font-normal text-muted-foreground">
            {t("overview.systemDetails")}
            <Switch
              aria-label={t("overview.showModule", {
                module: moduleLabels.system,
              })}
              checked={!layout.hidden.includes("system")}
              onCheckedChange={(visible) =>
                layout.setVisible("system", visible)
              }
              size="sm"
            />
          </Label>
        ) : !layout.hidden.includes("system") ? (
          moduleContent.system
        ) : null}
        {onOpenOnboarding ? (
          <div className="ml-auto shrink-0">
            <ActionHint
              open={showOnboardingHint}
              onDismiss={() => onDismissOnboardingHint?.()}
              message={t("onboarding.deferredHint")}
              dismissLabel={t("common.close")}
            >
              <Button
                variant={showOnboardingHint ? "secondary" : "ghost"}
                className={cn(showOnboardingHint && "text-primary")}
                size="sm"
                onClick={onOpenOnboarding}
              >
                {t("onboarding.open")}
              </Button>
            </ActionHint>
          </div>
        ) : null}
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
): number {
  if (right.usage.total_tokens !== left.usage.total_tokens) {
    return right.usage.total_tokens - left.usage.total_tokens;
  }
  // Keep ties deterministic.
  if (tokenRequestCount(right) !== tokenRequestCount(left)) {
    return tokenRequestCount(right) - tokenRequestCount(left);
  }
  if (tokenFee(right) !== tokenFee(left))
    return tokenFee(right) - tokenFee(left);

  return left.name.localeCompare(right.name, currentLocale());
}

function TokenUsagePanel({
  billingStatus,
  billingSummary,
  inferenceURL,
  isReady,
  onOpenRecords,
  onRefresh,
  rangeKey,
  status,
  tokenCatalog,
  tokensUnknown,
  usage,
}: {
  billingStatus: BillingStatus;
  billingSummary: BillingSummary | null;
  inferenceURL: string;
  isReady: boolean;
  onOpenRecords: (target: RecordsTarget) => void;
  onRefresh: () => void;
  rangeKey: string;
  status: UsageStatus;
  tokenCatalog: AccessTokenCatalog;
  tokensUnknown: boolean;
  usage: UsageSummary | null;
}) {
  const t = i18n.t.bind(i18n);
  const { configs: clientConfigs } = useClientConfigs(inferenceURL);
  const rows = useMemo(
    () => mergeTokenUsageRows(tokenCatalog, usage, billingSummary),
    [billingSummary, tokenCatalog, usage],
  );
  const sortedRows = useMemo(
    () => [...rows].sort(compareTokenUsageRows),
    [rows],
  );

  return (
    <Panel
      aria-labelledby="usage-by-token-heading"
      data-testid="token-usage-panel"
    >
      <PanelHeader
        className={USAGE_BREAKDOWN_HEADER}
        actions={
          <div className="flex min-w-0 items-center gap-2">
            <span className="text-micro text-muted-foreground">
              {t("overview.rankedByTokens")}
            </span>
            <HelpPopover label={t("overview.tokenCostCoverageLabel")}>
              <p>{t("overview.tokenFailureExplanation")}</p>
              <p>{t("overview.tokenCostCoverage")}</p>
            </HelpPopover>
          </div>
        }
      >
        <h2 className="text-sm font-semibold" id="usage-by-token-heading">
          {t("overview.byAccessToken")}
        </h2>
      </PanelHeader>
      <PaginatedList
        key={rangeKey}
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
                      onClick={onRefresh}
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
            <TooltipProvider delayDuration={ROW_HINT_DELAY_MS}>
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
                const openRecords = () => onOpenRecords({ tokenId: row.id });
                const recordsHint = t("overview.openRecords", {
                  name: row.name,
                });
                return (
                  // The whole row opens the token's records; the counts sit
                  // above that button as their own, and the text around them
                  // lets clicks through to it.
                  <div
                    className="relative grid min-h-14 grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 border-b px-4 py-2.5 text-left last:border-b-0 hover:bg-muted"
                    data-testid="token-usage-row"
                    key={row.id}
                  >
                    <RowHint hint={recordsHint}>
                      <Button
                        aria-label={recordsHint}
                        className="absolute inset-0 h-auto rounded-none p-0 hover:bg-transparent focus-visible:ring-inset"
                        onClick={openRecords}
                        type="button"
                        variant="ghost"
                      />
                    </RowHint>
                    <span className="pointer-events-none relative flex min-w-0 items-center gap-1.5">
                      <span
                        className="min-w-0 truncate text-sm font-medium"
                        data-slot="token-usage-name"
                      >
                        {row.name}
                      </span>
                      <TokenClientMarks
                        className="pointer-events-auto"
                        clients={clientsUsing(clientConfigs, row.id)}
                      />
                    </span>
                    <span className="pointer-events-none relative text-right text-sm font-semibold tabular-nums">
                      {stale
                        ? placeholder
                        : formatCompactNumber(row.usage.total_tokens)}
                    </span>
                    <span className="pointer-events-none relative flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-micro text-muted-foreground tabular-nums">
                      {stale ? (
                        <span>
                          {t("overview.tokenRequests", { count: placeholder })}
                        </span>
                      ) : (
                        <RowLink hint={recordsHint} onClick={openRecords}>
                          {t("overview.tokenRequests", {
                            count: formatExactNumber(requests),
                          })}
                        </RowLink>
                      )}
                      {!stale &&
                      failureRate !== null &&
                      row.usage.failed_requests > 0 ? (
                        <>
                          <span aria-hidden="true">·</span>
                          <RowLink
                            hint={t("overview.tokenFailureDetail", {
                              count: formatExactNumber(
                                row.usage.failed_requests,
                              ),
                              total: formatExactNumber(requests),
                            })}
                            onClick={() =>
                              onOpenRecords({
                                tokenId: row.id,
                                status: "failed",
                              })
                            }
                          >
                            {t("overview.tokenFailureRate", {
                              rate: `${(failureRate * 100).toFixed(1)}%`,
                            })}
                          </RowLink>
                        </>
                      ) : null}
                    </span>
                    <span className="pointer-events-none relative self-start text-right text-micro text-muted-foreground tabular-nums">
                      {amount}
                    </span>
                  </div>
                );
              })}
            </TooltipProvider>
          );
        }}
      </PaginatedList>
    </Panel>
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
  locale: string,
): ActivityCell[] {
  let variants = heatmapCells.get(points);
  if (!variants) {
    variants = new Map();
    heatmapCells.set(points, variants);
  }
  const cached = variants.get(locale);
  if (cached) return cached;
  const t = i18n.getFixedT(locale);
  const cells = points.map((point) => {
    const dateLabel = chartPointLabel(point);
    return {
      key: point.date,
      date: point.date,
      label: `${dateLabel}, ${formatExactNumber(point.total_tokens)} Token, ${t("overview.dayRequests", { count: formatExactNumber(point.requests) })}, ${t("overview.dayFailed", { count: formatExactNumber(point.failed_requests) })}`,
      value: point.total_tokens,
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
  variants.set(locale, cells);
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
  const unavailable = status === "blocked" || status === "error";
  const active = points.filter(
    (point) =>
      point.requests + point.failed_requests > 0 || point.total_tokens > 0,
  ).length;
  // The summary is retained while navigating. Its immutable day buckets can
  // reuse their labels on return; replacing the summary releases this cache.
  const cells = useMemo(
    () => prepareHeatmapCells(points, locale),
    [points, locale],
  );
  return (
    <div className="px-4 py-3.5">
      <div className="min-h-40">
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
                ? `${points[0].date} ~ ${points[points.length - 1].date} · ${t("overview.activeDays", { count: active, total: points.length })}`
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
              <ChartTooltip
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

const STATUS_SERIES = [
  {
    status: "succeeded",
    dataKey: "requests",
    fill: "var(--success)",
    active: "var(--success-foreground)",
    swatch: "bg-success",
  },
  {
    status: "failed",
    dataKey: "failed_requests",
    fill: "var(--destructive)",
    active: "var(--danger-foreground)",
    swatch: "bg-destructive",
  },
] as const;

/** Outcomes the buckets do not split by day, named under the chart. */
const STATUS_OTHERS = ["blocked", "cancelled", "pending"] as const;

type StatusPoint = UsageTotals & { date: string; hour?: number };

/**
 * Completed and failed calls per day, or per hour over the last day, in the
 * look of the token chart. A bar opens the request records for its day and
 * outcome; the legend opens them for the whole range.
 */
const RequestStatusChart = memo(function RequestStatusChart({
  counts,
  grain,
  onOpenRecords,
  points,
  status,
}: {
  counts: UsageStatusCounts | null;
  grain: "day" | "hour";
  onOpenRecords: (target: RecordsTarget) => void;
  points: StatusPoint[];
  status: UsageStatus;
}) {
  const t = useT();
  const chartRef = useRef<HTMLDivElement>(null);
  const unavailable = status === "blocked" || status === "error";
  const hourly = grain === "hour";
  const total = counts
    ? Object.values(counts).reduce((sum, count) => sum + count, 0)
    : 0;
  const empty =
    status === "ready" &&
    !points.some((point) => point.requests + point.failed_requests > 0);
  const shapes = useMemo(() => {
    const open = (requestStatus: RequestStatus, point: StatusPoint) =>
      // An hour is no day of its own, so it opens the range on screen.
      onOpenRecords(
        point.hour == null
          ? {
              status: requestStatus,
              range: { from: point.date, to: point.date },
            }
          : { status: requestStatus },
      );
    return {
      succeeded: statusBarShape("succeeded", open),
      failed: statusBarShape("failed", open),
    };
  }, [onOpenRecords]);
  const data = points.map((point) => ({ ...point, slot: chartSlot(point) }));
  const others = counts ? STATUS_OTHERS.filter((key) => counts[key] > 0) : [];

  return (
    <Panel
      aria-busy={status === "loading"}
      aria-labelledby="request-status-heading"
      className="flex flex-col"
      data-testid="request-status-panel"
    >
      <PanelHeader
        actions={
          counts && !unavailable ? (
            <span className="text-xs text-muted-foreground tabular-nums">
              {t("overview.statusTotal", { count: formatExactNumber(total) })}
            </span>
          ) : undefined
        }
        className={USAGE_BREAKDOWN_HEADER}
      >
        <h2 className="text-sm font-semibold" id="request-status-heading">
          {t("overview.requestStatus")}
        </h2>
      </PanelHeader>
      <div
        className="flex flex-1 flex-col px-4 py-3.5"
        data-slot="status-chart"
      >
        <div className="flex min-h-6 flex-wrap items-center justify-between gap-x-3 gap-y-2">
          <span className="text-micro font-medium text-muted-foreground">
            {t(hourly ? "overview.hourlyRequests" : "overview.dailyRequests")}
          </span>
          {counts ? (
            <div className="-mr-1.5 flex flex-wrap items-center justify-end gap-1">
              {STATUS_SERIES.map((series) => (
                <Button
                  className="h-6 gap-1.5 px-1.5 text-micro font-normal text-muted-foreground"
                  data-status={series.status}
                  disabled={counts[series.status] === 0}
                  key={series.status}
                  onClick={() => onOpenRecords({ status: series.status })}
                  title={t("overview.statusOpenHint")}
                  type="button"
                  variant="ghost"
                >
                  <span
                    aria-hidden="true"
                    className={cn("size-1.5 rounded-sm", series.swatch)}
                  />
                  {statusLabel(series.status)}
                  <span className="font-medium text-foreground tabular-nums">
                    {formatExactNumber(counts[series.status])}
                  </span>
                </Button>
              ))}
            </div>
          ) : null}
        </div>
        <div
          aria-label={t(
            hourly
              ? "overview.hourlyRequestsChart"
              : "overview.dailyRequestsChart",
          )}
          // At least the token chart's height; a taller panel beside it
          // lends the rest.
          className="relative mt-3 min-h-44 flex-1"
          ref={chartRef}
          role="img"
        >
          {unavailable ? (
            <EmptyState
              className="h-full border-0 py-4"
              title={t(
                status === "blocked"
                  ? "overview.statusBlocked"
                  : "overview.statusFailed",
              )}
            />
          ) : (
            <ResponsiveContainer height="100%" width="100%">
              <BarChart
                barCategoryGap="22%"
                data={data}
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
                  allowDecimals={false}
                  axisLine={false}
                  tick={AXIS_TICK}
                  tickFormatter={(value: number) => formatCompactNumber(value)}
                  tickLine={false}
                  width={40}
                />
                <ChartTooltip
                  content={(props) => (
                    <StatusTooltip {...props} originRef={chartRef} />
                  )}
                  cursor={{ fill: "var(--accent)" }}
                  isAnimationActive={false}
                  wrapperStyle={{ display: "none" }}
                />
                {STATUS_SERIES.map((series) => (
                  <Bar
                    activeBar={{ fill: series.active }}
                    dataKey={series.dataKey}
                    fill={series.fill}
                    isAnimationActive={false}
                    key={series.status}
                    maxBarSize={28}
                    shape={shapes[series.status]}
                    stackId="requests"
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
        {others.length ? (
          <p className="mt-2 flex flex-wrap gap-x-3 text-micro text-muted-foreground tabular-nums">
            {others.map((key) => (
              <span key={key}>
                {statusLabel(key)}{" "}
                {t("overview.requestTimes", {
                  value: formatExactNumber(counts?.[key] ?? 0),
                })}
              </span>
            ))}
          </p>
        ) : null}
      </div>
    </Panel>
  );
});

function statusBarShape(
  requestStatus: (typeof STATUS_SERIES)[number]["status"],
  open: (requestStatus: RequestStatus, point: StatusPoint) => void,
) {
  return function StatusBarShape(props: BarShapeProps) {
    const { fill, height, payload, width, x, y } = props;
    if (x == null || y == null || width == null || height == null) return null;
    const point = payload as StatusPoint;
    // Failures sit on top, so completed calls round only without them.
    const top = requestStatus === "failed" || !(point.failed_requests > 0);
    return (
      <rect
        className="cursor-pointer"
        data-date={point.date}
        data-hour={point.hour}
        data-status={requestStatus}
        data-testid="status-day"
        fill={fill}
        height={Math.max(0, height)}
        onClick={() => open(requestStatus, point)}
        rx={top ? 2 : 0}
        width={width}
        x={x}
        y={y}
      />
    );
  };
}

function StatusTooltip({
  active,
  coordinate,
  originRef,
  payload,
}: TooltipContentProps & { originRef: RefObject<HTMLDivElement | null> }) {
  const t = i18n.t.bind(i18n);
  const point = payload[0]?.payload as StatusPoint | undefined;
  if (!point) return null;
  const calls = point.requests + point.failed_requests;
  return (
    <ChartTooltipCard
      active={active}
      coordinate={coordinate}
      data-slot="status-day-tooltip"
      originRef={originRef}
      placementKey={chartSlot(point)}
    >
      <strong className="text-xs font-medium">{chartPointLabel(point)}</strong>
      {STATUS_SERIES.map((series) => (
        <span
          className="flex items-center gap-1.5 tabular-nums"
          key={series.status}
        >
          <span
            aria-hidden="true"
            className={cn("size-1.5 rounded-sm", series.swatch)}
          />
          {statusLabel(series.status)}:{" "}
          {formatExactNumber(point[series.dataKey])}
        </span>
      ))}
      {calls > 0 ? (
        <span className="tabular-nums">
          {t("overview.tokenFailureRate", {
            rate: `${((point.failed_requests * 100) / calls).toFixed(1)}%`,
          })}
        </span>
      ) : null}
      {calls > 0 ? (
        <span className="text-muted-foreground">
          {t("overview.statusChartHint")}
        </span>
      ) : null}
    </ChartTooltipCard>
  );
}

function xAxisInterval(count: number): number | "preserveStartEnd" {
  if (count <= 7) return 0;
  return Math.max(1, Math.ceil(count / 5) - 1);
}

function ServiceUsageBody({
  catalog,
  catalogUnknown,
  costs,
  isReady,
  onAddService,
  onOpenRecords,
  onOpenService,
  onRefreshServices,
  rows,
  visibleRows,
  showBar,
  status,
}: {
  catalog: ServiceCatalog;
  catalogUnknown: boolean;
  costs: ReadonlyMap<string, BillingAmounts>;
  isReady: boolean;
  onAddService: () => void;
  onOpenRecords: (target: RecordsTarget) => void;
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
    const emptyCatalog = catalog.items.length === 0;
    return (
      <div className="border-b px-4 py-6">
        <EmptyState
          action={
            emptyCatalog ? (
              <Button
                disabled={!isReady}
                onClick={onAddService}
                size="sm"
                type="button"
              >
                {t("overview.addService")}
              </Button>
            ) : undefined
          }
          className="border-0 py-2"
          description={
            emptyCatalog ? t("overview.emptyServicesHint") : undefined
          }
          title={t(
            emptyCatalog
              ? "overview.emptyServices"
              : status === "ready"
                ? "overview.noSuccessfulRequests"
                : status === "loading"
                  ? "overview.aggregating"
                  : status === "blocked"
                    ? "overview.waitingGateway"
                    : "overview.waitingRefresh",
          )}
        />
      </div>
    );
  }

  const servicesById = new Map(
    catalog.items.map((service) => [service.id, service]),
  );
  return (
    <TooltipProvider delayDuration={ROW_HINT_DELAY_MS}>
      {visibleRows.map((row) => {
        // Rows of deleted providers and unattributed calls have nothing to open.
        const service =
          row.in_catalog && row.id ? servicesById.get(row.id) : undefined;
        return (
          <UsageBreakdownRow
            action={
              service ? (
                <IconButton
                  className="text-muted-foreground"
                  label={t("overview.serviceSettings", {
                    name: service.name,
                  })}
                  onClick={() => onOpenService(service.id)}
                  type="button"
                >
                  <Settings aria-hidden="true" />
                </IconButton>
              ) : (
                // Holds the settings column so the figures stay in line.
                <span aria-hidden="true" className="block size-7" />
              )
            }
            barPercent={breakdownShare(row.total_tokens, tokenTotals(rows))}
            cost={row.id ? costs.get(row.id) : undefined}
            hint={
              service
                ? t("overview.openRecords", { name: service.name })
                : undefined
            }
            key={row.id ?? "unattributed"}
            label={row.name}
            labelDetail={
              service ? (
                <NameDetail
                  empty={t("overview.serviceNoModels")}
                  items={service.models.map((model) => ({
                    key: model,
                    label: model,
                  }))}
                  summary={t("overview.serviceModels", {
                    count: service.models.length,
                  })}
                  title={service.name}
                />
              ) : undefined
            }
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
              service
                ? () => onOpenRecords({ serviceId: service.id })
                : undefined
            }
            requests={row.requests}
            showBar={showBar}
            status={status}
            tokens={row.total_tokens}
          />
        );
      })}
    </TooltipProvider>
  );
}

function ModelUsageBody({
  costs,
  onOpenRecords,
  rows,
  services,
  visibleRows,
  showBar,
  status,
}: {
  costs: ReadonlyMap<string, BillingAmounts>;
  onOpenRecords: (target: RecordsTarget) => void;
  rows: UsageGroup[];
  services: Service[];
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
    <TooltipProvider delayDuration={ROW_HINT_DELAY_MS}>
      {visibleRows.map((row) => {
        const label = modelUsageLabel(row.id);
        const model = row.id;
        // Usage groups by the model sent upstream, which is what a provider's
        // model list names.
        const providers = model
          ? services.filter((service) => service.models.includes(model))
          : [];
        return (
          <UsageBreakdownRow
            barPercent={breakdownShare(row.total_tokens, tokenTotals(rows))}
            cost={model ? costs.get(model) : undefined}
            hint={
              model ? t("overview.openRecords", { name: label }) : undefined
            }
            key={row.id ?? "unknown-model"}
            label={label}
            labelDetail={
              model ? (
                <NameDetail
                  empty={t("overview.modelNoProviders")}
                  items={providers.map((service) => ({
                    key: service.id,
                    label: service.enabled
                      ? service.name
                      : t("overview.disabledService", { name: service.name }),
                  }))}
                  summary={t("overview.modelProviders", {
                    count: providers.length,
                  })}
                  title={label}
                />
              ) : undefined
            }
            leading={
              model ? (
                <ModelBrandIcon model={model} />
              ) : (
                // A blank mark keeps the name in line with the others.
                <span aria-hidden="true" className="block size-3.5" />
              )
            }
            onClick={
              model ? () => onOpenRecords({ models: [model] }) : undefined
            }
            requests={row.requests}
            showBar={showBar}
            status={status}
            tokens={row.total_tokens}
          />
        );
      })}
    </TooltipProvider>
  );
}

const ROW_HINT_DELAY_MS = 400;
// A tooltip holds a few lines; the rest is a count.
const DETAIL_ITEMS = 8;

/**
 * Who stands behind a row's name: a provider's models, or the providers that
 * list a model.
 */
function NameDetail({
  empty,
  items,
  summary,
  title,
}: {
  empty: string;
  items: Array<{ key: string; label: string }>;
  summary: string;
  title: string;
}) {
  const t = i18n.t.bind(i18n);
  const shown = items.slice(0, DETAIL_ITEMS);
  return (
    <span className="grid gap-1 text-left" data-slot="usage-name-detail">
      <strong className="font-medium break-all">{title}</strong>
      <span className="opacity-75">{items.length ? summary : empty}</span>
      {shown.length ? (
        <ul className="grid gap-0.5">
          {shown.map((item) => (
            <li className="truncate" key={item.key}>
              {item.label}
            </li>
          ))}
          {items.length > shown.length ? (
            <li className="opacity-75">
              {t("overview.moreItems", { count: items.length - shown.length })}
            </li>
          ) : null}
        </ul>
      ) : null}
    </span>
  );
}

/** What clicking does, shown while the pointer rests on it or it has focus. */
function RowHint({ children, hint }: { children: ReactElement; hint: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent sideOffset={4}>{hint}</TooltipContent>
    </Tooltip>
  );
}

/** A count in a clickable row that opens a narrower list of its own. */
function RowLink({
  children,
  hint,
  onClick,
}: {
  children: ReactNode;
  hint: string;
  onClick: () => void;
}) {
  return (
    <RowHint hint={hint}>
      <Button
        className="pointer-events-auto h-auto rounded-xs p-0 font-normal text-inherit underline-offset-2 hover:text-foreground"
        onClick={onClick}
        size="xs"
        type="button"
        variant="link"
      >
        {children}
      </Button>
    </RowHint>
  );
}

function tokenTotals(rows: Array<Pick<UsageGroup, "total_tokens">>) {
  return rows.map((row) => row.total_tokens);
}

/** What the range cost per API provider and per model, for the rows. */
function breakdownCosts(billing: BillingSummary | null): {
  byService: ReadonlyMap<string, BillingAmounts>;
  byModel: ReadonlyMap<string, BillingAmounts>;
} {
  const byService = new Map(
    (billing?.by_service ?? []).map((row) => [row.service_id, row]),
  );
  // Usage lists a model once; billing lists it once per official provider
  // that priced it, so those listings are added up for the row.
  const byModel = new Map<string, BillingAmounts>();
  for (const row of billing?.by_model ?? []) {
    const listed = byModel.get(row.model);
    byModel.set(
      row.model,
      listed
        ? {
            amount_usd: String(
              Number(listed.amount_usd) + Number(row.amount_usd),
            ),
            priced: listed.priced + row.priced,
            unpriced: listed.unpriced + row.unpriced,
            pending: listed.pending + row.pending,
            revalued: listed.revalued + row.revalued,
            requests: listed.requests + row.requests,
          }
        : row,
    );
  }
  return { byService, byModel };
}

function UsageBreakdownRow({
  action,
  barPercent,
  cost,
  hint,
  label,
  labelDetail,
  leading,
  onClick,
  requests,
  showBar,
  status,
  tokens,
}: {
  action?: ReactNode;
  barPercent: number;
  /** The row's API-equivalent cost, beside its call count. */
  cost?: BillingAmounts;
  hint?: string;
  label: string;
  labelDetail?: ReactNode;
  leading?: ReactNode;
  onClick?: () => void;
  requests: number;
  showBar: boolean;
  status: UsageStatus;
  tokens: number;
}) {
  const t = i18n.t.bind(i18n);
  const requestMetric = compactMetric(requests, status);
  // An unpriced row has no amount to show; its title says why.
  const amount = cost && cost.priced > 0 ? billingAmount(cost) : null;
  const requestText = t("overview.requestTimes", { value: requestMetric.text });
  return (
    <BreakdownRow
      action={action}
      detail={amount ? `${requestText} · ${amount}` : requestText}
      detailTitle={
        [
          requestMetric.title
            ? t("overview.requestTimes", { value: requestMetric.title })
            : "",
          cost ? billingNoteText(t, cost) : "",
        ]
          .filter(Boolean)
          .join(" · ") || undefined
      }
      hint={hint}
      label={label}
      labelDetail={labelDetail}
      leading={leading}
      onClick={onClick}
      share={showBar ? barPercent : null}
      value={<CompactCount value={usageCountValue(tokens, status)} />}
    />
  );
}

function UsageMetric({
  children,
  detail,
  label,
  metric,
  icon,
}: {
  children?: ReactNode;
  detail?: ReactNode;
  label: string;
  icon?: ReactNode;
  metric?: CompactMetric;
}) {
  return (
    <Metric
      detail={detail}
      icon={icon}
      label={label}
      title={metric?.title ?? undefined}
      value={children ?? metric?.text}
    />
  );
}

/**
 * Input and output under the total. A narrow tile stacks them, and the
 * clipped box hides the separator that would start the second line.
 */
function TokenSplit({ input, output }: { input: number; output: number }) {
  const t = useT();
  return (
    <span className="flex flex-wrap gap-x-3 overflow-hidden">
      <span className="whitespace-nowrap">
        {t("overview.input")} <CompactCount value={input} />
      </span>
      <span className="relative whitespace-nowrap before:absolute before:-left-2 before:content-['·']">
        {t("overview.output")} <CompactCount value={output} />
      </span>
    </span>
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
  const locale = currentLocale();
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
