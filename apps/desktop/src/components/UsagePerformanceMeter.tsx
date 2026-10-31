import { type ReactNode, useEffect, useId, useState } from "react";
import { useT } from "../i18n";
import type { ServicePerformance, UsageStatus } from "../usage-range";
import type { PerformanceTarget } from "../use-performance-details";
import {
  CacheRate,
  formatCacheRate,
  PerformanceUnit,
  TokensPerSecond,
  UsagePerformanceDetails,
} from "./UsagePerformanceDetails";
import { Activity } from "./icons";
import { DataField } from "./DataRow";
import { IconButton } from "./IconButton";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { cn } from "@/lib/utils";

export function UsagePerformanceMeter({
  performance,
  status,
  periodLabel,
  scopeDescription,
  target,
  ready,
  testId = "usage-performance",
  layout = "stacked",
}: {
  performance?: ServicePerformance;
  status: UsageStatus;
  periodLabel: string;
  scopeDescription: string;
  target: PerformanceTarget;
  ready: boolean;
  testId?: string;
  layout?: "stacked" | "fields" | "icon";
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!ready) setOpen(false);
  }, [ready]);
  const titleId = useId();
  const valuesId = useId();
  const cache = performance?.cache_hit_rate;
  const speed = performance?.output_tokens_per_second;
  const placeholder = status === "loading" ? "…" : "—";
  const details = (trigger: ReactNode) => (
    <Popover open={open && ready} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="end" className="w-96" aria-labelledby={titleId}>
        <UsagePerformanceDetails
          target={target}
          open={open && ready}
          scopeDescription={scopeDescription}
          titleId={titleId}
        />
      </PopoverContent>
    </Popover>
  );
  const detailsLabel = t("services.performanceDetailsFor", {
    name: target.name,
  });
  if (layout === "fields") {
    return (
      <div
        className={cn(
          "col-span-2 row-span-2 grid grid-cols-2 grid-rows-subgrid gap-x-4 gap-y-1 tabular-nums",
          status === "error" && "row-span-3",
        )}
        data-testid={testId}
      >
        <DataField
          className="row-span-2 grid grid-rows-subgrid"
          label={t("services.cacheUtilization")}
          value={
            <span className="font-semibold">
              <CacheRate value={cache} placeholder={placeholder} />
            </span>
          }
        />
        {/* The details button sits beside the label so the value keeps the
            whole column; the negative margin keeps the label row height. */}
        <DataField
          className="row-span-2 grid grid-rows-subgrid"
          label={
            <span className="inline-flex items-center gap-1">
              TPS
              {details(
                <Button
                  aria-label={detailsLabel}
                  className="-my-0.75"
                  disabled={!ready}
                  size="icon-xs"
                  variant="ghost"
                  type="button"
                >
                  <Activity
                    aria-hidden="true"
                    className="text-muted-foreground"
                  />
                </Button>,
              )}
            </span>
          }
          value={
            <span className="font-semibold">
              <TokensPerSecond value={speed} placeholder={placeholder} />
            </span>
          }
        />
        {status === "error" ? (
          <p className="col-span-2 text-micro text-muted-foreground">
            {t("services.performanceError")}
          </p>
        ) : null}
      </div>
    );
  }
  if (layout === "icon") {
    // A card has no room for the values; the button names them and opens the
    // same details.
    const summary =
      status === "error"
        ? t("services.performanceErrorNamed", { name: target.name })
        : t("services.performanceSummary", {
            name: target.name,
            cache: cache == null ? placeholder : `${formatCacheRate(cache)}%`,
            speed: speed == null ? placeholder : `${speed.toFixed(1)} tok/s`,
          });
    return details(
      <IconButton
        className="text-muted-foreground"
        data-testid={testId}
        disabled={!ready}
        label={summary}
        size="icon-xs"
        type="button"
      >
        <Activity aria-hidden="true" />
      </IconButton>,
    );
  }
  // Units share the last column, so "%" and "tok/s" start at the same edge and
  // the numbers end at the same edge. Each label stays with its own number,
  // so a long TPS can use the space under the longer cache label. A missing
  // value keeps its unit invisible, so every provider reserves the same width.
  const metricRow = (
    label: string,
    amount: string | null,
    unit: string,
    spaced = false,
  ) => (
    <span className="col-span-full grid grid-cols-subgrid items-baseline gap-x-0.5">
      <span className="flex items-baseline justify-between gap-1.5">
        <span className="text-muted-foreground">{label}</span>
        <span>{amount ?? placeholder}</span>
      </span>
      {/* Grid and flex drop this space from layout; it only keeps the unit a
          separate word in the text alternative. */}
      {spaced && amount != null ? " " : null}
      <PerformanceUnit
        aria-hidden={amount == null || undefined}
        className={cn(amount == null && "invisible")}
      >
        {unit}
      </PerformanceUnit>
    </span>
  );
  // The two values open the details themselves, like the billing amount next
  // to them; the range selector above the list already names the period.
  return details(
    <Button
      aria-describedby={valuesId}
      aria-label={detailsLabel}
      className="h-auto w-full justify-start px-0 text-left text-xs font-normal tabular-nums"
      data-testid={testId}
      disabled={!ready}
      size="xs"
      variant="ghost"
      type="button"
    >
      <span
        className="grid w-full grid-cols-[1fr_auto] gap-x-0.5 gap-y-1"
        id={valuesId}
      >
        <span className="sr-only">{periodLabel}</span>
        {status === "error" ? (
          <span className="col-span-full text-muted-foreground">
            {t("services.performanceError")}
          </span>
        ) : (
          <>
            {metricRow(
              t("services.cacheUtilization"),
              cache == null ? null : formatCacheRate(cache),
              "%",
            )}
            {metricRow(
              "TPS",
              speed == null ? null : speed.toFixed(1),
              "tok/s",
              true,
            )}
          </>
        )}
      </span>
    </Button>,
  );
}
