import { useT } from "../i18n";
import {
  usePerformanceDetails,
  type PerformanceTarget,
} from "../use-performance-details";
import { HelpDisclosure } from "./HelpDisclosure";
import { IconButton } from "./IconButton";
import { RefreshCw } from "./icons";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

export function UsagePerformanceDetails({
  target,
  open,
  scopeDescription,
  titleId,
}: {
  target: PerformanceTarget;
  open: boolean;
  scopeDescription: string;
  titleId: string;
}) {
  const t = useT();
  const { rows, refresh, loading } = usePerformanceDetails(target, open);
  return (
    <div className="grid gap-3">
      <div className="flex min-w-0 items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 id={titleId} className="text-sm font-semibold">
            {t("services.performanceDetailsTitle")}
          </h3>
          <p
            className="mt-1 truncate text-xs text-muted-foreground"
            title={target.name}
          >
            {target.name}
          </p>
        </div>
        <IconButton
          label={t(loading ? "common.refreshing" : "common.refresh")}
          disabled={loading}
          onClick={refresh}
          size="icon-sm"
          type="button"
        >
          <RefreshCw
            className={
              loading ? "animate-spin motion-reduce:animate-none" : undefined
            }
          />
        </IconButton>
      </div>
      <Table
        className="table-fixed text-xs tabular-nums"
        aria-label={t("services.performanceDetailsTitle")}
      >
        <TableHeader>
          <TableRow>
            <TableHead className="w-2/5 pl-0">
              {t("services.performancePeriod")}
            </TableHead>
            <TableHead className="text-right">
              {t("services.cacheUtilization")}
            </TableHead>
            <TableHead className="pr-0 text-right">TPS</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(({ period, status, usage }) => {
            const performance = usage?.performance;
            const placeholder = status === "loading" ? "…" : "—";
            return (
              <TableRow key={period} data-period={period}>
                <TableCell className="pl-0">
                  <div className="font-medium">
                    {t(
                      period === "today"
                        ? "services.performanceToday"
                        : `overview.range.${period}`,
                    )}
                  </div>
                  <div className="mt-1 whitespace-normal text-micro text-muted-foreground">
                    {status === "ready"
                      ? t("services.performanceRequests", {
                          count: usage?.requests ?? 0,
                        })
                      : t(
                          status === "loading"
                            ? "common.loading"
                            : "services.performanceError",
                        )}
                  </div>
                </TableCell>
                <TableCell className="text-right">
                  <div className="font-semibold">
                    {performance?.cache_hit_rate == null
                      ? placeholder
                      : `${(performance.cache_hit_rate * 100).toFixed(1)}%`}
                  </div>
                  <div className="mt-1 whitespace-normal text-micro text-muted-foreground">
                    {status === "ready"
                      ? t("services.performanceSampleCount", {
                          count: performance?.cache_samples ?? 0,
                        })
                      : placeholder}
                  </div>
                </TableCell>
                <TableCell className="pr-0 text-right">
                  <div className="font-semibold">
                    {performance?.output_tokens_per_second == null
                      ? placeholder
                      : performance.output_tokens_per_second.toFixed(1)}
                  </div>
                  <div className="mt-1 whitespace-normal text-micro text-muted-foreground">
                    {status === "ready"
                      ? t("services.performanceSampleCount", {
                          count: performance?.speed_samples ?? 0,
                        })
                      : placeholder}
                  </div>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      <HelpDisclosure title={t("services.performanceCalculation")}>
        <p>{t("services.performanceWindowsHint")}</p>
        <p>{scopeDescription}</p>
        <p>{t("services.cacheUtilizationHint")}</p>
        <p>{t("services.performanceSpeedHint")}</p>
      </HelpDisclosure>
    </div>
  );
}
