import { useMemo, useRef, useState, type KeyboardEvent } from "react";

import { BreakdownLine } from "@/components/BreakdownRow";
import { Repeat } from "@/components/icons";
import { UsageMeterInline } from "@/components/UsageMeter";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

import { useDisplayCurrency } from "./currency-display";
import { formatCompactNumber, formatShare } from "./format-compact-number";
import { useT } from "./i18n";
import { billingAmount } from "./pricing-model";
import {
  cacheMissCost,
  cacheMissReason,
  callPlaceLabel,
  callPlaces,
  carriedChangeText,
  carriedItems,
  carriedKind,
  carriedName,
  contextAxis,
  contextComposition,
  contextMeterValue,
  contextRuns,
  failedRepeatsText,
  formatExtraUSD,
  nearestCall,
  sumUSD,
  type CallPlace,
} from "./request-context-model";
import type {
  RequestRecord,
  RequestSession,
  SessionContext,
} from "./request-record-model";

/** How full a live conversation's context is, inside its lane. */
export function SessionContextMeter({
  session,
  className,
}: {
  session: RequestSession;
  className?: string;
}) {
  const t = useT();
  const meter = contextMeterValue(session);
  if (!meter) return null;
  const tokens = formatCompactNumber(meter.tokens);
  const limit = formatCompactNumber(meter.limit);
  return (
    <UsageMeterInline
      className={className}
      label={t("sessionContext.meter")}
      title={t(
        meter.basis === "compaction"
          ? "sessionContext.meterCompactionTitle"
          : "sessionContext.meterWindowTitle",
        { tokens, limit },
      )}
      tone={meter.warning ? "warning" : "default"}
      value={meter.percent}
      valueLabel={formatShare(meter.percent)}
    />
  );
}

/**
 * The line a live lane shows while the newest calls keep running the same
 * tool call into the same failure.
 */
export function SessionFailedRepeats({
  session,
  className,
}: {
  session: RequestSession;
  className?: string;
}) {
  useT();
  const repeats = session.failed_repeats;
  if (!repeats) return null;
  return (
    <span
      className={cn(
        "flex min-w-0 items-center gap-1.5 text-xs leading-5 text-danger-foreground",
        className,
      )}
      data-testid="lane-failed-repeats"
    >
      <Repeat
        animateOnHover={false}
        aria-hidden="true"
        className="size-3 shrink-0"
      />
      <span className="min-w-0 truncate">{failedRepeatsText(repeats)}</span>
    </span>
  );
}

/**
 * What the conversation sent the model call by call: how its context grew,
 * where the client compacted it, where the prompt cache missed, and what
 * that cost.
 */
export function RequestContext({
  session,
  context,
  turns,
  selectedRequestId,
  onSelectRequest,
  onOpenCall,
}: {
  session: RequestSession;
  /** Absent until the detail is read, and when no call reported usage. */
  context: SessionContext | undefined;
  turns: RequestRecord[];
  selectedRequestId: string | null;
  /** Points the detail at another call. */
  onSelectRequest: (requestId: string) => void;
  /** Shows a call where it happened, in the summary. */
  onOpenCall: (requestId: string) => void;
}) {
  const t = useT();
  const places = useMemo(() => callPlaces(turns), [turns]);
  if (!context || context.calls.length === 0) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto py-4">
        <p
          className="text-xs text-muted-foreground"
          data-testid="context-empty"
        >
          {t("sessionContext.empty")}
        </p>
      </div>
    );
  }
  const latest = context.calls[context.calls.length - 1]!;
  const windowTokens = session.context_window_tokens;
  const compactions = session.compaction_count ?? context.compactions.length;
  return (
    <div
      className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-4 [scrollbar-gutter:stable]"
      data-testid="session-context"
    >
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h3 className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm font-medium">
          {t("sessionContext.tab")}
          <span
            className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-xs font-normal text-muted-foreground [&>*+*]:before:mr-2 [&>*+*]:before:text-border [&>*+*]:before:content-['·']"
            data-testid="context-summary"
          >
            <span>
              {t("sessionContext.nowLabel")}{" "}
              <span className="font-medium tabular-nums text-foreground">
                {formatCompactNumber(latest.tokens)}
              </span>
            </span>
            {windowTokens ? (
              <span>
                {t("sessionContext.windowLabel")}{" "}
                <span className="tabular-nums">
                  {formatCompactNumber(windowTokens)}
                </span>
              </span>
            ) : null}
            {compactions > 0 ? (
              <span>
                {t("sessionContext.compactions", { count: compactions })}
              </span>
            ) : null}
          </span>
        </h3>
        {context.cache_misses.length > 0 ? (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <span
              aria-hidden="true"
              className="size-2 rounded-full bg-destructive"
            />
            {t("sessionContext.missLegend")}
          </span>
        ) : null}
      </div>
      <ContextCurve
        className="mt-3"
        context={context}
        onSelect={onSelectRequest}
        places={places}
        pointTokens={session.compaction_point_tokens ?? null}
        selectedId={selectedRequestId}
      />
      <ContextCostLine
        context={context}
        onOpenCall={onOpenCall}
        places={places}
        session={session}
      />
      <CallCarried
        context={context}
        places={places}
        selectedRequestId={selectedRequestId}
        turns={turns}
      />
    </div>
  );
}

/**
 * What one main call carried, beside the curve that picks it: its input by
 * source, and the rules and memory it sent with how each changed over the
 * conversation. The newest main call unless the detail points at another.
 */
function CallCarried({
  context,
  turns,
  places,
  selectedRequestId,
}: {
  context: SessionContext;
  turns: RequestRecord[];
  places: ReadonlyMap<string, CallPlace>;
  selectedRequestId: string | null;
}) {
  const t = useT();
  const calls = context.calls;
  const latestId = calls[calls.length - 1]!.request_id;
  const callId =
    selectedRequestId !== null &&
    calls.some((call) => call.request_id === selectedRequestId)
      ? selectedRequestId
      : latestId;
  const record = turns.find((turn) => turn.id === callId);
  const carried = record?.context;
  const composition = useMemo(
    () => (carried?.parts ? contextComposition(carried.parts) : []),
    [carried],
  );
  const items = useMemo(
    () => carriedItems(turns, context, callId, places),
    [turns, context, callId, places],
  );
  const title =
    callId === latestId
      ? t("sessionContext.carriedLatest")
      : t("sessionContext.carriedCall", {
          ordinal: places.get(callId)?.ordinal ?? 1,
        });
  return (
    <div className="@container/carried mt-6">
      <div className="grid gap-x-10 gap-y-6 @min-[44rem]/carried:grid-cols-2">
        <section data-testid="context-composition">
          <h4 className="mb-1 flex min-w-0 items-baseline gap-3 text-sm font-medium">
            <span className="truncate">{title}</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              {t("sessionContext.carriedHint")}
            </span>
          </h4>
          {composition.length > 0 ? (
            composition.map((line) => (
              <BreakdownLine
                key={line.key}
                label={t(`sessionContext.part.${line.key}`)}
                share={line.share}
                value={formatCompactNumber(line.tokens)}
              />
            ))
          ) : (
            <p className="py-1.5 text-xs text-muted-foreground">
              {carried
                ? t("sessionContext.carriedOnlyNew")
                : t("sessionContext.carriedUnknown")}
            </p>
          )}
        </section>
        <section data-testid="context-carried">
          <h4 className="mb-1 flex min-w-0 items-baseline gap-3 text-sm font-medium">
            <span className="truncate">{t("sessionContext.carriedTitle")}</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              {t("sessionContext.carriedEveryCall")}
            </span>
          </h4>
          {items.length > 0 ? (
            items.map(({ item, change }) => (
              <BreakdownLine
                key={`${item.kind}:${item.path ?? ""}:${item.name ?? ""}`}
                label={
                  <>
                    <span
                      className={cn(
                        "truncate",
                        (item.kind === "rules" || item.kind === "memory") &&
                          "font-mono text-micro",
                      )}
                    >
                      {carriedName(item)}
                    </span>
                    <span className="shrink-0 text-muted-foreground">
                      {carriedKind(item)}
                    </span>
                  </>
                }
                labelTitle={item.path}
                note={carriedChangeText(change)}
                share={null}
                value={formatCompactNumber(item.tokens)}
              />
            ))
          ) : (
            <p className="py-1.5 text-xs text-muted-foreground">
              {carried
                ? t("sessionContext.carriedNone")
                : t("sessionContext.carriedUnknown")}
            </p>
          )}
        </section>
      </div>
    </div>
  );
}

const PLOT_WIDTH = 1000;
const PLOT_HEIGHT = 200;

/**
 * The context of every main call, oldest first. Each compaction drops the
 * line; a dashed guide marks where the client compacts, red dots the calls
 * that missed the cache. Clicking a call points the detail at it; the arrow
 * keys walk the calls.
 */
function ContextCurve({
  context,
  pointTokens,
  places,
  selectedId,
  onSelect,
  className,
}: {
  context: SessionContext;
  pointTokens: number | null;
  places: ReadonlyMap<string, CallPlace>;
  selectedId: string | null;
  onSelect: (requestId: string) => void;
  className?: string;
}) {
  const t = useT();
  const plotRef = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const calls = context.calls;
  const count = calls.length;
  const geometry = useMemo(() => {
    const peak = Math.max(pointTokens ?? 0, ...calls.map((c) => c.tokens));
    const axis = contextAxis(peak);
    const x = (position: number) =>
      count <= 1 ? PLOT_WIDTH / 2 : (position / (count - 1)) * PLOT_WIDTH;
    const y = (tokens: number) =>
      PLOT_HEIGHT -
      (Math.min(tokens, axis.ceiling) / axis.ceiling) * PLOT_HEIGHT;
    const positions = new Map(
      calls.map((call, index) => [call.request_id, index]),
    );
    const runs = contextRuns(context).map((run) => {
      let line = "";
      for (let position = run.from; position <= run.to; position += 1) {
        const command = position === run.from ? "M" : "L";
        line += `${command}${x(position)},${y(calls[position]!.tokens)}`;
      }
      // A run of one call still shows as a short tick.
      if (run.from === run.to) line += `h0.01`;
      return {
        key: calls[run.from]!.request_id,
        line,
        area: `${line}V${PLOT_HEIGHT}H${x(run.from)}Z`,
      };
    });
    const drops = context.compactions.flatMap((compaction) => {
      const position = positions.get(compaction.request_id);
      if (position === undefined || position === 0) return [];
      const at = (x(position - 1) + x(position)) / 2;
      return [
        { key: compaction.request_id, at, top: y(compaction.before_tokens) },
      ];
    });
    const misses = context.cache_misses.flatMap((miss) => {
      const position = positions.get(miss.request_id);
      return position === undefined
        ? []
        : [
            {
              key: miss.request_id,
              at: x(position),
              top: y(calls[position]!.tokens),
            },
          ];
    });
    const ticks: number[] = [];
    for (let tick = 0; tick <= axis.ceiling; tick += axis.step)
      ticks.push(tick);
    return { axis, x, y, positions, runs, drops, misses, ticks };
  }, [calls, context, count, pointTokens]);
  const { x, y, positions, runs, drops, misses, ticks } = geometry;
  const latest = count - 1;
  const selected = selectedId !== null ? positions.get(selectedId) : undefined;
  const focus = hover ?? selected ?? latest;
  const left = (value: number) => `${(value / PLOT_WIDTH) * 100}%`;
  const top = (value: number) => `${(value / PLOT_HEIGHT) * 100}%`;
  const callText = (position: number) =>
    t("sessionContext.callTokens", {
      ordinal: places.get(calls[position]!.request_id)?.ordinal ?? position + 1,
      tokens: formatCompactNumber(calls[position]!.tokens),
    });
  const select = (position: number) => {
    const call = calls[Math.min(latest, Math.max(0, position))];
    if (call) onSelect(call.request_id);
  };
  const positionAt = (clientX: number) => {
    const box = plotRef.current?.getBoundingClientRect();
    if (!box || box.width <= 0) return null;
    return nearestCall(count, (clientX - box.left) / box.width);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const current = selected ?? latest;
    const next =
      event.key === "ArrowLeft"
        ? current - 1
        : event.key === "ArrowRight"
          ? current + 1
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? latest
              : null;
    if (next === null) return;
    event.preventDefault();
    select(next);
  };
  const first = places.get(calls[0]!.request_id)?.ordinal ?? 1;
  const last = places.get(calls[latest]!.request_id)?.ordinal ?? count;
  return (
    <div className={cn("min-w-0", className)} data-testid="context-curve">
      <div className="grid grid-cols-[2.75rem_minmax(0,1fr)_3.5rem] grid-rows-[12rem_auto] gap-x-2 pt-5">
        <div aria-hidden="true" className="relative">
          {ticks.map((tick) => (
            <span
              className="absolute right-0 -translate-y-1/2 text-micro leading-none tabular-nums text-muted-foreground"
              key={tick}
              style={{ top: top(y(tick)) }}
            >
              {formatCompactNumber(tick)}
            </span>
          ))}
        </div>
        <div
          aria-label={t("sessionContext.chartLabel", { count })}
          aria-valuemax={count}
          aria-valuemin={1}
          aria-valuenow={(selected ?? latest) + 1}
          aria-valuetext={callText(selected ?? latest)}
          className="relative cursor-pointer rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          data-testid="context-plot"
          onClick={(event) => {
            const position = positionAt(event.clientX);
            if (position !== null) select(position);
          }}
          onKeyDown={onKeyDown}
          onPointerLeave={() => setHover(null)}
          onPointerMove={(event) => {
            if (event.pointerType === "touch") return;
            setHover(positionAt(event.clientX));
          }}
          ref={plotRef}
          role="slider"
          tabIndex={0}
        >
          <svg
            aria-hidden="true"
            className="absolute inset-0 size-full overflow-visible"
            preserveAspectRatio="none"
            viewBox={`0 0 ${PLOT_WIDTH} ${PLOT_HEIGHT}`}
          >
            {ticks.map((tick) => (
              <line
                key={tick}
                stroke="var(--border)"
                strokeOpacity={tick === 0 ? 1 : 0.6}
                vectorEffect="non-scaling-stroke"
                x1={0}
                x2={PLOT_WIDTH}
                y1={y(tick)}
                y2={y(tick)}
              />
            ))}
            {pointTokens ? (
              <line
                data-testid="context-compaction-point"
                stroke="var(--warning)"
                strokeDasharray="4 3"
                vectorEffect="non-scaling-stroke"
                x1={0}
                x2={PLOT_WIDTH}
                y1={y(pointTokens)}
                y2={y(pointTokens)}
              />
            ) : null}
            {runs.map((run) => (
              <g key={run.key}>
                <path d={run.area} fill="var(--primary)" fillOpacity={0.08} />
                <path
                  d={run.line}
                  fill="none"
                  stroke="var(--primary)"
                  strokeLinejoin="round"
                  strokeWidth={1.5}
                  vectorEffect="non-scaling-stroke"
                />
              </g>
            ))}
            {drops.map((drop) => (
              <line
                key={drop.key}
                stroke="var(--muted-foreground)"
                strokeDasharray="2 3"
                vectorEffect="non-scaling-stroke"
                x1={drop.at}
                x2={drop.at}
                y1={drop.top}
                y2={PLOT_HEIGHT}
              />
            ))}
            {focus !== latest ? (
              <line
                stroke="var(--muted-foreground)"
                strokeOpacity={0.5}
                vectorEffect="non-scaling-stroke"
                x1={x(focus)}
                x2={x(focus)}
                y1={0}
                y2={PLOT_HEIGHT}
              />
            ) : null}
          </svg>
          {pointTokens ? (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute left-1 -translate-y-full pb-0.5 text-micro leading-none text-warning-foreground"
              style={{ top: top(y(pointTokens)) }}
            >
              {t("sessionContext.compactionPoint", {
                tokens: formatCompactNumber(pointTokens),
              })}
            </span>
          ) : null}
          {drops.map((drop) => (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute pl-1 text-micro leading-none text-muted-foreground"
              data-testid="context-drop"
              key={drop.key}
              style={{
                left: left(drop.at),
                top: `calc(${top(drop.top)} + 1.5rem)`,
              }}
            >
              {t("sessionContext.compacted")}
            </span>
          ))}
          {misses.map((miss) => (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-destructive ring-2 ring-background"
              data-testid="context-miss"
              key={miss.key}
              style={{ left: left(miss.at), top: top(miss.top) }}
            />
          ))}
          {focus !== latest ? (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary ring-2 ring-background"
              data-testid="context-focus"
              style={{
                left: left(x(focus)),
                top: top(y(calls[focus]!.tokens)),
              }}
            />
          ) : null}
          <span
            aria-hidden="true"
            className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary"
            style={{
              left: left(x(latest)),
              top: top(y(calls[latest]!.tokens)),
            }}
          />
          <span
            aria-hidden="true"
            className="pointer-events-none absolute left-full ml-2 -translate-y-1/2 text-xs font-medium whitespace-nowrap tabular-nums text-foreground"
            data-testid="context-latest"
            style={{ top: top(y(calls[latest]!.tokens)) }}
          >
            {formatCompactNumber(calls[latest]!.tokens)}
          </span>
        </div>
        <span aria-hidden="true" />
        <span aria-hidden="true" />
        <div className="mt-1.5 flex min-w-0 items-center justify-between gap-3 text-micro tabular-nums text-muted-foreground">
          <span>{t("sessionContext.firstCall", { ordinal: first })}</span>
          {hover !== null ? (
            <span aria-hidden="true" className="truncate text-text-secondary">
              {callText(hover)}
            </span>
          ) : null}
          <span>{t("sessionContext.lastCall", { ordinal: last })}</span>
        </div>
      </div>
    </div>
  );
}

/**
 * One line under the curve: what the conversation came to at official
 * prices, how often the cache hit, and what its misses cost, with the
 * reasons on demand.
 */
function ContextCostLine({
  session,
  context,
  places,
  onOpenCall,
}: {
  session: RequestSession;
  context: SessionContext;
  places: ReadonlyMap<string, CallPlace>;
  onOpenCall: (requestId: string) => void;
}) {
  const t = useT();
  useDisplayCurrency();
  const [open, setOpen] = useState(false);
  const misses = context.cache_misses;
  const extra = sumUSD(
    misses.flatMap((miss) => (miss.extra_usd != null ? [miss.extra_usd] : [])),
  );
  const amount =
    session.cost && session.cost.priced > 0
      ? billingAmount(session.cost)
      : null;
  const ratio = session.usage?.cache_hit_ratio;
  const parts = [
    amount ? (
      <span key="cost">
        {t("sessionContext.cost")}{" "}
        <span className="font-medium text-foreground">{amount}</span>
      </span>
    ) : null,
    ratio != null ? (
      <span key="hit">
        {t("sessionContext.cacheHit")}{" "}
        <span className="font-medium text-foreground">
          {formatShare(ratio * 100)}
        </span>
      </span>
    ) : null,
    misses.length > 0 ? (
      <span className="inline-flex items-baseline gap-2" key="misses">
        <span>
          {extra
            ? t("sessionContext.missesCost", {
                count: misses.length,
                amount: formatExtraUSD(extra),
              })
            : t("sessionContext.misses", { count: misses.length })}
        </span>
        <Popover onOpenChange={setOpen} open={open}>
          <PopoverTrigger asChild>
            <Button
              className="h-auto p-0 text-xs"
              data-testid="context-miss-reasons"
              type="button"
              variant="link"
            >
              {t("sessionContext.viewReasons")}
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="start"
            animated
            className="w-[28rem] max-w-[calc(100vw-2rem)] p-0 text-xs"
          >
            <p className="border-b px-3 py-2 font-medium">
              {t("sessionContext.reasonsTitle")}
            </p>
            <ul className="max-h-72 overflow-y-auto py-1">
              {misses.map((miss) => (
                <li key={miss.request_id}>
                  <Button
                    className="grid h-auto w-full grid-cols-[minmax(0,auto)_minmax(0,1fr)_auto] items-baseline gap-x-3 rounded-none px-3 py-1.5 text-left text-xs font-normal"
                    data-testid="context-miss-reason"
                    onClick={() => {
                      setOpen(false);
                      onOpenCall(miss.request_id);
                    }}
                    type="button"
                    variant="ghost"
                  >
                    <span className="whitespace-nowrap text-text-secondary">
                      {callPlaceLabel(places.get(miss.request_id))}
                    </span>
                    <span className="min-w-0 truncate first-letter:uppercase">
                      {cacheMissReason(miss) ??
                        t("sessionContext.reasonUnknown")}
                    </span>
                    <span className="whitespace-nowrap tabular-nums text-warning-foreground">
                      {cacheMissCost(miss)}
                    </span>
                  </Button>
                </li>
              ))}
            </ul>
            <p className="border-t px-3 py-2 text-muted-foreground">
              {t("sessionContext.reasonsNote")}
            </p>
          </PopoverContent>
        </Popover>
      </span>
    ) : null,
  ].filter(Boolean);
  if (parts.length === 0) return null;
  return (
    <p
      className="mt-3 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-xs text-muted-foreground [&>*+*]:before:mr-2 [&>*+*]:before:text-border [&>*+*]:before:content-['·']"
      data-testid="context-cost"
    >
      {parts}
    </p>
  );
}
