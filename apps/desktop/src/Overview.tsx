import type { ReactNode } from "react";
import { Check, ChevronRight, Copy, Plus, RefreshCw } from "lucide-react";

import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import type { AccessTokenCatalog } from "./AccessTokenManager";
import { phaseLabel, phaseTone, type AppSnapshot } from "./core-model";
import { i18n } from "./i18n";
import { PageHeader } from "./PageHeader";
import type { Service } from "./service-model";
import type { ServiceCatalogStatus } from "./ServiceManager";
import {
  formatCacheHitPercent,
  mergeCatalogServiceUsage,
  modelUsageLabel,
  usageBarPercent,
  type MergedServiceUsage,
  type TodayUsageGroup,
  type TodayUsageState,
  type TodayUsageStatus,
} from "./today-usage";

export interface ServiceCatalog {
  status: ServiceCatalogStatus;
  items: Service[];
  error: string | null;
  stale: boolean;
}

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
  onOpenRecords,
  onOpenService,
  onRefreshServices,
  onRefreshTodayUsage,
  onRestart,
  snapshot,
  todayUsage,
  tokenCatalog,
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
  onOpenRecords: () => void;
  onOpenService: (serviceId: string) => void;
  onRefreshServices: () => void;
  onRefreshTodayUsage: () => void;
  onRestart: () => void;
  snapshot: AppSnapshot | null;
  todayUsage: TodayUsageState;
  tokenCatalog: AccessTokenCatalog;
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
  const summary = todayUsage.summary;
  const serviceRows = mergeCatalogServiceUsage(
    catalog.items,
    summary?.by_service ?? [],
  );
  const modelRows = summary?.by_model ?? [];
  const systemDetails: Array<[string, string]> = [
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
      conversionEngine?.available
        ? `${conversionEngine.name} ${conversionEngine.version ?? ""}`.trim()
        : t("overview.relaykitOff"),
    ],
  ];

  return (
    <div className="grid gap-4 [&_[data-slot=page-header]]:mb-4">
      <PageHeader
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <OverviewStatusChip
              isReady={isReady}
              statusLabel={statusLabel}
              statusTone={statusTone}
            />
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
        description={t("overview.description")}
        title={t("overview.title")}
      />

      <Panel
        aria-labelledby="access-heading"
        className={cn(
          !isReady && statusTone === "negative" && "border-destructive/25",
        )}
      >
        <div className="flex min-w-0 flex-wrap items-center gap-3 px-4 py-3">
          <div className="flex min-w-0 flex-1 items-center gap-3">
            <div
              className={cn(
                "flex min-w-0 flex-1 flex-col",
                apiCopied && "text-success-foreground",
              )}
            >
              <span
                className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase"
                id="access-heading"
              >
                {apiAddressLabel}
              </span>
              <code className="mt-1 overflow-hidden font-mono text-sm font-medium tracking-tight text-ellipsis whitespace-nowrap">
                {inferenceURL || t("overview.waitingReady")}
              </code>
            </div>
            <Button
              aria-label={t("overview.copyApiAddress")}
              disabled={!inferenceURL}
              onClick={() => onCopy(inferenceURL, apiAddressLabel)}
              size="icon"
              type="button"
              variant="outline"
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
              <strong className="text-base tracking-tight tabular-nums">
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
                  : "outline"
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

      <Panel aria-labelledby="today-usage-heading">
        <PanelHeader
          actions={
            <>
              {summary && summary.failed_requests > 0 ? (
                <Button
                  onClick={onOpenRecords}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("overview.failedCount", { count: summary.failed_requests })}
                </Button>
              ) : null}
              <Button
                disabled={!isReady || todayUsage.status === "loading"}
                onClick={onRefreshTodayUsage}
                size="icon-sm"
                type="button"
                variant="ghost"
                aria-label={t("common.refresh")}
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn(
                    "motion-reduce:animate-none",
                    todayUsage.status === "loading" && "animate-spin",
                  )}
                />
                <span className="sr-only">{t("common.refresh")}</span>
              </Button>
            </>
          }
        >
          <SectionKicker>{t("overview.localSummary")}</SectionKicker>
          <h2
            className="mt-1 text-base font-semibold tracking-tight"
            id="today-usage-heading"
          >
            {t("overview.todayUsage")}
          </h2>
        </PanelHeader>

        {todayUsage.status === "loading" || summary?.capped ? (
          <div className="flex flex-wrap items-center gap-1.5 border-b px-4 py-2">
            <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
              {todayUsage.status === "loading" ? t("overview.aggregating") : t("overview.recent1000")}
            </Badge>
          </div>
        ) : null}

        <div className="grid grid-cols-4 gap-4 px-4 py-4 max-[860px]:grid-cols-2">
          <OverviewHeroMetric
            label={t("overview.requests")}
            value={formatOverviewMetric(summary?.requests, todayUsage.status)}
          />
          <OverviewHeroMetric
            label={t("overview.totalTokens")}
            value={formatOverviewMetric(summary?.total_tokens, todayUsage.status)}
          />
          <OverviewHeroMetric
            label={t("overview.inputOutput")}
            value={formatSplitMetric(
              summary?.input_tokens,
              summary?.output_tokens,
              todayUsage.status,
            )}
          />
          <OverviewHeroMetric
            label={t("overview.cacheHits")}
            value={formatOverviewCacheHit(summary, todayUsage.status)}
          />
        </div>

        <div className="flex items-center justify-between border-t px-4 py-2.5">
          <span className="text-xs text-muted-foreground">{t("overview.estimatedCost")}</span>
          <strong className="text-xs font-medium tracking-tight text-muted-foreground tabular-nums">
            —
          </strong>
        </div>

        {todayUsage.status === "error" && todayUsage.error ? (
          <p className="border-t px-4 py-2.5 text-xs text-danger-foreground" role="alert">
            {todayUsage.error}
          </p>
        ) : (
          <p className="border-t px-4 py-2.5 text-micro text-muted-foreground">
            {t("overview.usageNote")}
          </p>
        )}
      </Panel>

      <div className="grid grid-cols-2 items-start gap-4 max-[860px]:grid-cols-1">
        <Panel aria-labelledby="usage-by-service-heading">
          <PanelHeader
            actions={
              <>
                <div className="flex items-baseline gap-3 pr-1 max-[560px]:hidden">
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
            <SectionKicker>{t("overview.mix")}</SectionKicker>
            <h2
              className="mt-1 text-base font-semibold tracking-tight"
              id="usage-by-service-heading"
            >
              {t("overview.byService")}
            </h2>
          </PanelHeader>

          <ServiceUsageBody
            catalog={catalog}
            catalogUnknown={catalogUnknown}
            isReady={isReady}
            onAddService={onAddService}
            onOpenService={onOpenService}
            onRefreshServices={onRefreshServices}
            rows={serviceRows}
            showBar={serviceRows.some((row) => row.total_tokens > 0)}
            status={todayUsage.status}
          />

          <Button
            className="h-auto w-full justify-between rounded-none px-4 py-2.5 text-xs font-medium text-text-secondary no-underline hover:bg-muted hover:text-foreground hover:no-underline"
            onClick={onManageServices}
            type="button"
            variant="link"
          >
            {t("overview.manageAllServices")}
            <span aria-hidden="true">→</span>
          </Button>
        </Panel>

        <Panel aria-labelledby="usage-by-model-heading">
          <PanelHeader>
            <SectionKicker>{t("overview.mix")}</SectionKicker>
            <h2
              className="mt-1 text-base font-semibold tracking-tight"
              id="usage-by-model-heading"
            >
              {t("overview.byModel")}
            </h2>
          </PanelHeader>
          <ModelUsageBody
            rows={modelRows}
            showBar={modelRows.some((row) => row.total_tokens > 0)}
            status={todayUsage.status}
          />
        </Panel>
      </div>

      <Panel asChild>
        <details className="group">
          <summary className="flex cursor-pointer list-none items-center justify-between px-4 py-3 text-text-secondary [&::-webkit-details-marker]:hidden">
            <span className="flex min-w-0 flex-col">
              <strong className="text-sm font-medium text-foreground">{t("overview.systemDetails")}</strong>
              <small className="mt-0.5 text-xs text-muted-foreground">
                {t("overview.systemDetailsHint")}
              </small>
            </span>
            <ChevronRight
              aria-hidden="true"
              className="size-4 shrink-0 transition-transform group-open:rotate-90"
            />
          </summary>
          <div className="border-t px-4 py-4">
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
        </details>
      </Panel>
    </div>
  );
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
  status: TodayUsageStatus;
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
      <div className="flex items-center justify-center border-b px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t("overview.loadingServices")}</p>
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
  rows: TodayUsageGroup[];
  showBar: boolean;
  status: TodayUsageStatus;
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
      <div className="flex items-center justify-center px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t("overview.aggregatingModels")}</p>
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
  status: TodayUsageStatus;
  tokens: number;
}) {
  const t = i18n.t.bind(i18n);
  const content = (
    <>
      {leading}
      <span className="flex min-w-0 flex-1 flex-col">
        <strong className="overflow-hidden text-sm font-medium text-ellipsis whitespace-nowrap">
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
        <strong className="text-sm tracking-tight tabular-nums">
          {formatOverviewMetric(tokens, status)}
        </strong>
        <span className="text-micro text-muted-foreground tabular-nums">
          {t("overview.requestTimes", {
            value: formatOverviewMetric(requests, status),
          })}
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

function OverviewHeroMetric({
  label,
  value,
}: {
  label: string;
  value: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-micro font-medium tracking-[0.06em] text-muted-foreground">
        {label}
      </span>
      <strong className="text-lg tracking-tight tabular-nums">{value}</strong>
    </div>
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
      <strong className="text-base tracking-tight tabular-nums">
        {unknown ? "—" : value}
      </strong>
      <span className="text-micro text-muted-foreground">{label}</span>
    </div>
  );
}

function OverviewStatusChip({
  isReady,
  statusLabel,
  statusTone,
}: {
  isReady: boolean;
  statusLabel: string;
  statusTone: StatusTone;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-micro font-medium",
        statusTone === "negative" &&
          "border-destructive/25 bg-danger-wash text-danger-foreground",
        statusTone === "positive" &&
          "border-success/20 bg-success-wash text-success-foreground",
        statusTone === "pending" &&
          "border-warning/25 bg-warning-wash text-warning-foreground",
        statusTone === "neutral" && "border-border bg-card text-text-secondary",
      )}
    >
      <StatusDot tone={statusTone} />
      {isReady ? t("overview.gatewayHealthy") : statusLabel}
    </span>
  );
}

function formatOverviewMetric(
  value: number | undefined,
  status: TodayUsageStatus,
): string {
  if (value === undefined || status === "blocked" || status === "error") {
    return "—";
  }
  return value.toLocaleString();
}

function formatSplitMetric(
  left: number | undefined,
  right: number | undefined,
  status: TodayUsageStatus,
): string {
  if (
    left === undefined ||
    right === undefined ||
    status === "blocked" ||
    status === "error"
  ) {
    return "—";
  }
  return `${left.toLocaleString()} / ${right.toLocaleString()}`;
}

function formatOverviewCacheHit(
  summary: TodayUsageState["summary"],
  status: TodayUsageStatus,
): string {
  if (!summary || status === "blocked" || status === "error") return "—";
  return formatCacheHitPercent(summary);
}
