import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import {
  ArrowDown,
  ArrowUp,
  ChevronRight,
  MessageSquare,
} from "@/components/icons";
import { IconButton } from "@/components/IconButton";
import { RequestServiceLabel } from "@/components/RequestServiceLabel";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import type { CopyFeedback } from "./copy-feedback";
import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
import { formatDuration } from "./request-live-model";
import type { AuditContent, RequestRecord } from "./request-record-model";
import {
  requestServiceIdentity,
  type RequestServiceIdentity,
  type RequestServiceMap,
} from "./request-service-model";
import {
  extendPendingTimeline,
  listScrollForTimeline,
  timelineScrollForList,
  timelineWeight,
  trajectoryRows,
  trajectoryTimeline,
  type TrajectoryCallColumn,
  type TrajectoryLane,
  type TrajectoryRow,
  type TrajectoryTimeline,
  type TrajectoryTimelineCall,
  type TrajectoryTimelineItem,
  type TrajectoryTimelinePhase,
} from "./request-trajectory-model";
import { chipToneClass } from "./trajectory-chip";
import { TrajectoryInspector } from "./TrajectoryInspector";
import { useDetachedInspector } from "./trajectory-inspector-window";

const TIMELINE_MIN_CALL_PX = 6;
const TIMELINE_GAP_PX = 8;
const TIMELINE_GAP_MIN_PX = 2;
const TIMELINE_TICK_PX = 2;
const TIMELINE_BAR_MIN_PX = 3;

/**
 * A bar 500 ms wider is invisible, and every widening reflows the whole strip,
 * so the timeline advances on a coarser grid than the duration labels do.
 */
const TIMELINE_CLOCK_INTERVAL_MS = 500;

/**
 * Below this the whole list is cheaper to mount than to window, and keeping it
 * whole means a trajectory small enough to read in one sitting stays fully in
 * the DOM for find-in-page. Above it the row count is the problem: a 95-turn
 * conversation is ~1400 rows, and mounting them all is what froze the page.
 */
const LIST_VIRTUALIZE_MIN_ROWS = 80;
const LIST_ROW_ESTIMATE_PX = 32;
const LIST_OVERSCAN_ROWS = 12;
const ROW_COLUMNS =
  "grid grid-cols-[4.5rem_minmax(0,1fr)_5rem_1rem] gap-2 @min-[560px]/trajectory:grid-cols-[4.5rem_minmax(0,1fr)_6rem_4rem_1rem] @min-[760px]/trajectory:grid-cols-[4.5rem_4.5rem_minmax(0,1fr)_6rem_4rem_1rem]";

/**
 * How long a programmatic scroll owns one direction. Writing scrollTop/Left
 * fires the other pane's listener, and a window rather than a one-shot flag
 * lets a windowed scroll settle over several frames without swallowing the
 * next real one.
 */
const REVEAL_SUPPRESS_MS = 250;

/**
 * Width a knee-length call keeps once the strip stops fitting. Because every
 * minimum is derived from the same scale, columns pinned at their minimum stay
 * proportional to each other, so overflowing into a horizontal scroll costs
 * nothing in readability. A constant minimum would instead make every column
 * identical the moment the sum outgrows the viewport. 96px puts about 8–10
 * similar calls on a typical ~944px strip; the rest scroll.
 */
const TIMELINE_KNEE_PX = 96;

const timelineLanes: TrajectoryLane[] = ["client", "gateway", "upstream"];
const NO_SERVICES: RequestServiceMap = {};

export function RequestTrajectory({
  turns,
  childrenByRoot,
  selectedRequestId,
  onSelectRequest,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
  services = NO_SERVICES,
}: {
  turns: RequestRecord[];
  childrenByRoot: Record<string, RequestRecord[]>;
  selectedRequestId: string | null;
  onSelectRequest: (requestId: string) => void;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
  services?: RequestServiceMap;
}) {
  const t = useT();
  const serviceByRequest = useMemo(
    () =>
      Object.fromEntries(
        [...turns, ...Object.values(childrenByRoot).flat()].map((record) => [
          record.id,
          requestServiceIdentity(record, services),
        ]),
      ),
    [turns, childrenByRoot, services],
  );
  const rows = useMemo(
    () =>
      trajectoryRows(turns, childrenByRoot).map((row) => {
        const service = serviceByRequest[row.requestId];
        // Keep the original event metadata; translate only its service ID for display.
        return row.chip === "ROUTE" && service?.id
          ? {
              ...row,
              summary: row.summary.replace(service.id, () => service.name),
            }
          : row;
      }),
    [childrenByRoot, turns, serviceByRequest],
  );
  // Resolving a row id by scanning `rows` costs nothing once, and used to cost
  // a full scan inside every phase mark of every lane: 1300 marks against 1400
  // rows is close to two million comparisons per paint.
  const rowIndex = useMemo(() => {
    const byId = new Map<string, TrajectoryRow>();
    const positionById = new Map<string, number>();
    const firstPhaseByRequestId = new Map<string, TrajectoryRow>();
    rows.forEach((row, position) => {
      byId.set(row.id, row);
      positionById.set(row.id, position);
      if (row.chip !== "TURN" && !firstPhaseByRequestId.has(row.requestId)) {
        firstPhaseByRequestId.set(row.requestId, row);
      }
    });
    return { byId, positionById, firstPhaseByRequestId };
  }, [rows]);
  // The strip advances its own open calls, so the settled layout is built once
  // per turn set instead of once per clock tick. See `extendPendingTimeline`.
  const timeline = useMemo(() => trajectoryTimeline(rows, Date.now()), [rows]);
  const turnKey = useMemo(
    () => turns.map((turn) => turn.id).join(","),
    [turns],
  );
  const sessionKey = turns[0]?.session_id ?? turns[0]?.id ?? "";
  const [selectedRowId, setSelectedRowId] = useState<string | null>(null);
  const [overlayOpen, setOverlayOpen] = useState(true);
  const [highlightedRequestId, setHighlightedRequestId] = useState<
    string | null
  >(null);
  const [revealNonce, setRevealNonce] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const suppressStripFromListUntilRef = useRef(0);
  const suppressListFromStripUntilRef = useRef(0);
  const followSelectionOnStripRef = useRef(false);
  const syncFromRef = useRef<"list" | "strip" | null>(null);
  const syncFrameRef = useRef<number | null>(null);
  const rowsRef = useRef(rows);
  rowsRef.current = rows;

  useEffect(() => {
    setSelectedRowId(rows[rows.length - 1]?.id ?? null);
    setOverlayOpen(true);
    setHighlightedRequestId(null);
  }, [turnKey]);

  const selectedRow =
    (selectedRowId === null ? undefined : rowIndex.byId.get(selectedRowId)) ??
    rows[rows.length - 1] ??
    null;
  const selectedRecord = selectedRow
    ? findRecord(turns, childrenByRoot, selectedRow.requestId)
    : null;

  // Stable while a poll changes nothing, so the detached window is only pushed
  // a payload when the selection or the record behind it actually moved.
  const selection = useMemo(
    () =>
      selectedRow && selectedRecord
        ? {
            row: selectedRow,
            record: selectedRecord,
            service: serviceByRequest[selectedRecord.id],
          }
        : null,
    [selectedRecord, selectedRow, serviceByRequest],
  );
  const inspectorWindow = useDetachedInspector(selection);

  const selectRow = useCallback(
    (row: TrajectoryRow, options?: { reveal?: boolean; inspect?: boolean }) => {
      setSelectedRowId(row.id);
      onSelectRequest(row.requestId);
      // The timeline lands on a call. Highlight every phase of that request
      // instead of opening the inspector on the one mark that was clicked.
      if (options?.inspect === false) {
        setHighlightedRequestId(row.requestId);
        if (options.reveal) setRevealNonce((nonce) => nonce + 1);
        return;
      }
      setHighlightedRequestId(null);
      setOverlayOpen(true);
      // The clicked row is known right here, so the window is handed the phase
      // directly. Reading it back from state would send the row that was
      // selected before the click, because this update is still pending.
      const record = findRecord(turns, childrenByRoot, row.requestId);
      if (record) {
        inspectorWindow.show({
          row,
          record,
          service: serviceByRequest[record.id],
        });
      }
    },
    [childrenByRoot, inspectorWindow, onSelectRequest, turns, serviceByRequest],
  );
  const selectListRow = useCallback(
    (row: TrajectoryRow) => {
      followSelectionOnStripRef.current = true;
      selectRow(row);
    },
    [selectRow],
  );
  const revealRow = useCallback(
    (row: TrajectoryRow) => selectRow(row, { reveal: true, inspect: false }),
    [selectRow],
  );

  const virtualizer = useVirtualizer({
    count: rows.length,
    estimateSize: () => LIST_ROW_ESTIMATE_PX,
    getItemKey: (position) => rows[position]?.id ?? position,
    getScrollElement: () => listRef.current,
    overscan: LIST_OVERSCAN_ROWS,
  });
  const virtualized = rows.length >= LIST_VIRTUALIZE_MIN_ROWS;
  const virtualRows = virtualized ? virtualizer.getVirtualItems() : null;
  const virtualizerRef = useRef(virtualizer);
  virtualizerRef.current = virtualizer;

  const syncStripFromList = () => {
    const list = listRef.current;
    const scroller = scrollerRef.current;
    if (!list || !scroller) return;
    const columns = measureTimelineColumns(scroller);
    const maxScroll = scroller.scrollWidth - scroller.clientWidth;
    const next = Math.round(
      timelineScrollForList(
        rowsRef.current,
        columns,
        list.scrollTop,
        list.scrollHeight - list.clientHeight,
        maxScroll,
      ),
    );
    if (next === Math.round(scroller.scrollLeft)) return;
    suppressListFromStripUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    scroller.scrollLeft = next;
  };

  const syncListFromStrip = () => {
    const list = listRef.current;
    const scroller = scrollerRef.current;
    if (!list || !scroller) return;
    const columns = measureTimelineColumns(scroller);
    // A resize can remove horizontal overflow. It must not reset the list.
    const timelineMaxScroll = scroller.scrollWidth - scroller.clientWidth;
    if (timelineMaxScroll <= 0) return;
    const maxScroll = Math.max(0, list.scrollHeight - list.clientHeight);
    const next = listScrollForTimeline(
      rowsRef.current,
      columns,
      // Match the integer scrollWidth/clientWidth, including a fractional
      // last pixel from CSS layout, so the final call reaches the list end.
      Math.round(scroller.scrollLeft),
      timelineMaxScroll,
      maxScroll,
    );
    const clamped = Math.round(Math.min(maxScroll, Math.max(0, next)));
    if (clamped === Math.round(list.scrollTop)) return;
    suppressStripFromListUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    if (rowsRef.current.length >= LIST_VIRTUALIZE_MIN_ROWS) {
      virtualizerRef.current.scrollToOffset(clamped, { align: "start" });
      return;
    }
    list.scrollTop = clamped;
  };

  const scheduleSync = (from: "list" | "strip") => {
    if (from === "list" && Date.now() < suppressStripFromListUntilRef.current) {
      return;
    }
    if (
      from === "strip" &&
      Date.now() < suppressListFromStripUntilRef.current
    ) {
      return;
    }
    syncFromRef.current = from;
    if (syncFrameRef.current !== null) return;
    syncFrameRef.current = requestAnimationFrame(() => {
      syncFrameRef.current = null;
      const origin = syncFromRef.current;
      syncFromRef.current = null;
      if (origin === "list") syncStripFromList();
      if (origin === "strip") syncListFromStrip();
    });
  };

  useEffect(() => {
    const list = listRef.current;
    if (!list || !highlightedRequestId || revealNonce === 0) return;
    if (list.clientHeight === 0) return;
    const first =
      rowIndex.firstPhaseByRequestId.get(highlightedRequestId) ??
      rowIndex.byId.get(selectedRowId ?? "");
    if (!first) return;
    if (virtualized) {
      const position = rowIndex.positionById.get(first.id);
      if (position === undefined) return;
      suppressStripFromListUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
      virtualizer.scrollToIndex(position, { align: "start" });
      return;
    }
    const target = list.querySelector<HTMLElement>(
      `[data-testid="trajectory-row"][data-row-id="${first.id}"]`,
    );
    if (!target) return;
    const top = target.offsetTop;
    if (top === list.scrollTop) return;
    suppressStripFromListUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    list.scrollTop = top;
  }, [highlightedRequestId, revealNonce]);

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !sessionKey) return;
    let cancelled = false;
    let observer: ResizeObserver | undefined;
    const scrollToLatest = () => {
      const current = rowsRef.current;
      const last = current[current.length - 1];
      if (!last || list.clientHeight === 0) return false;
      suppressListFromStripUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
      const latestOffset = Math.max(
        0,
        current.length * LIST_ROW_ESTIMATE_PX - list.clientHeight,
      );
      list.scrollTop = latestOffset;
      if (current.length >= LIST_VIRTUALIZE_MIN_ROWS) {
        virtualizerRef.current.scrollToOffset(latestOffset, { align: "start" });
      } else {
        const target = list.querySelector<HTMLElement>(
          `[data-testid="trajectory-row"][data-row-id="${last.id}"]`,
        );
        if (!target) return false;
        list.scrollTop = Math.max(
          0,
          target.offsetTop + target.offsetHeight - list.clientHeight,
        );
      }
      list.dispatchEvent(new Event("scroll"));
      syncStripFromList();
      return true;
    };
    const run = () => {
      if (cancelled) return;
      if (scrollToLatest()) return;
      if (typeof ResizeObserver === "undefined") return;
      observer = new ResizeObserver(() => {
        if (scrollToLatest()) observer?.disconnect();
      });
      observer.observe(list);
    };
    // The virtualizer flushSynces on scrollToOffset. Doing that inside this
    // layout effect makes React refuse the update, so wait until the commit
    // has finished.
    queueMicrotask(run);
    return () => {
      cancelled = true;
      observer?.disconnect();
    };
  }, [sessionKey]);

  useEffect(
    () => () => {
      if (syncFrameRef.current !== null) {
        cancelAnimationFrame(syncFrameRef.current);
      }
    },
    [],
  );

  useEffect(() => {
    const list = listRef.current;
    const scroller = scrollerRef.current;
    if (!list || !scroller || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => syncStripFromList());
    observer.observe(list);
    observer.observe(scroller);
    return () => observer.disconnect();
  }, [sessionKey]);

  return (
    <div className="@container/trajectory flex h-full min-h-0 min-w-0 flex-1 flex-col">
      <TrajectoryTimelineView
        followSelectionRef={followSelectionOnStripRef}
        onScroll={() => scheduleSync("strip")}
        onSelectRow={revealRow}
        rowById={rowIndex.byId}
        scrollerRef={scrollerRef}
        selectedRow={selectedRow}
        suppressListFromStripUntilRef={suppressListFromStripUntilRef}
        timeline={timeline}
      />
      <div
        className={cn(
          ROW_COLUMNS,
          "h-7 shrink-0 items-center border-b bg-muted/40 px-2 text-micro text-muted-foreground",
        )}
      >
        <span>{t("trajectory.phase")}</span>
        <span className="hidden @min-[760px]/trajectory:block">
          {t("trajectory.time")}
        </span>
        <span className="flex min-w-0 items-center gap-2">
          <span>{t("trajectory.eventList")}</span>
          <span className="hidden tabular-nums @min-[560px]/trajectory:inline">
            {t("trajectory.eventCount", {
              count: rows.filter((row) => row.chip !== "TURN").length,
            })}
          </span>
          <span className="ml-auto flex items-center gap-0.5">
            <IconButton
              size="icon-xs"
              disabled={rows.length === 0}
              label={t("trajectory.firstCall")}
              onClick={() => {
                const first = rows.find((row) => row.chip !== "TURN");
                if (first) {
                  followSelectionOnStripRef.current = true;
                  revealRow(first);
                }
              }}
              type="button"
            >
              <ArrowUp aria-hidden="true" />
            </IconButton>
            <IconButton
              size="icon-xs"
              disabled={rows.length === 0}
              label={t("trajectory.latestCall")}
              onClick={() => {
                const last = rows[rows.length - 1];
                if (last) {
                  followSelectionOnStripRef.current = true;
                  revealRow(last);
                }
              }}
              type="button"
            >
              <ArrowDown aria-hidden="true" />
            </IconButton>
          </span>
        </span>
        <span className="text-right">{t("trajectory.result")}</span>
        <span className="hidden text-right @min-[560px]/trajectory:block">
          {t("records.duration")}
        </span>
        <span />
      </div>
      <div className="relative flex min-h-0 min-w-0 flex-1">
        <div
          aria-label={t("trajectory.eventList")}
          className="relative min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-gutter:stable]"
          data-testid="trajectory-list"
          onScroll={() => scheduleSync("list")}
          ref={listRef}
        >
          <ol
            className="relative"
            style={
              virtualRows ? { height: virtualizer.getTotalSize() } : undefined
            }
          >
            {virtualRows
              ? virtualRows.map((item) => (
                  <TrajectoryRowView
                    key={item.key}
                    highlighted={chainHighlighted(
                      rows[item.index]!,
                      highlightedRequestId,
                      selectedRow?.chip === "TURN",
                    )}
                    measureRef={virtualizer.measureElement}
                    offsetPx={item.start}
                    onSelect={selectListRow}
                    position={item.index}
                    row={rows[item.index]!}
                    service={serviceByRequest[rows[item.index]!.requestId]}
                    selected={rows[item.index]!.id === selectedRow?.id}
                  />
                ))
              : rows.map((row, position) => (
                  <TrajectoryRowView
                    key={row.id}
                    highlighted={chainHighlighted(
                      row,
                      highlightedRequestId,
                      selectedRow?.chip === "TURN",
                    )}
                    onSelect={selectListRow}
                    position={position}
                    row={row}
                    service={serviceByRequest[row.requestId]}
                    selected={row.id === selectedRow?.id}
                  />
                ))}
          </ol>
        </div>
        {/* Without a window host the pane floats above the list instead of
            splitting the row, so the list keeps its full width either way. */}
        {selection && !inspectorWindow.enabled ? (
          <aside
            className={cn(
              "absolute inset-y-0 right-0 z-10 flex min-h-0 w-[min(100%,26rem)] border-l shadow-lg",
              !overlayOpen && "hidden",
            )}
          >
            <TrajectoryInspector
              auditContent={
                auditContent?.request_id === selection.row.requestId
                  ? auditContent
                  : null
              }
              auditError={
                selectedRequestId === selection.row.requestId
                  ? auditError
                  : null
              }
              auditLoading={
                selectedRequestId === selection.row.requestId &&
                (auditLoading ||
                  auditContent?.request_id !== selection.row.requestId)
              }
              copyFeedback={copyFeedback}
              onClose={() => setOverlayOpen(false)}
              record={selection.record}
              row={selection.row}
              service={selection.service}
            />
          </aside>
        ) : null}
      </div>
    </div>
  );
}

function measureTimelineColumns(scroller: HTMLElement): TrajectoryCallColumn[] {
  const columns: TrajectoryCallColumn[] = [];
  const seen = new Set<string>();
  for (const node of scroller.querySelectorAll<HTMLElement>(
    '[data-testid="trajectory-call"]',
  )) {
    const requestId = node.getAttribute("data-request-id");
    if (!requestId || seen.has(requestId)) continue;
    seen.add(requestId);
    columns.push({
      requestId,
      offset: node.offsetLeft,
      width: node.offsetWidth,
    });
  }
  return columns;
}

function TrajectoryTimelineView({
  timeline,
  rowById,
  scrollerRef,
  selectedRow,
  followSelectionRef,
  suppressListFromStripUntilRef,
  onScroll,
  onSelectRow,
}: {
  timeline: TrajectoryTimeline;
  rowById: Map<string, TrajectoryRow>;
  scrollerRef: RefObject<HTMLDivElement | null>;
  selectedRow: TrajectoryRow | null;
  followSelectionRef: RefObject<boolean>;
  suppressListFromStripUntilRef: RefObject<number>;
  onScroll: () => void;
  onSelectRow: (row: TrajectoryRow) => void;
}) {
  const t = useT();
  // The clock lives here rather than on the page, so a call still waiting on
  // its upstream repaints the strip and nothing else.
  const clockMs = useLiveClock(timeline.open, TIMELINE_CLOCK_INTERVAL_MS);
  const { items, kneeMs } = useMemo(
    () => extendPendingTimeline(timeline, clockMs),
    [clockMs, timeline],
  );
  const showRuler = items.some(
    (item) => item.kind === "call" && item.call.turnRowId,
  );

  useEffect(() => {
    if (!followSelectionRef.current) return;
    followSelectionRef.current = false;
    const scroller = scrollerRef.current;
    const selectedId = selectedRow?.id;
    if (!scroller || !selectedId) return;
    const target = scroller.querySelector<HTMLElement>(
      `[data-row-id="${selectedId}"]`,
    );
    if (!target) return;
    const scrollerRect = scroller.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    let shifted = false;
    if (targetRect.left < scrollerRect.left) {
      scroller.scrollLeft -= scrollerRect.left - targetRect.left;
      shifted = true;
    } else if (targetRect.right > scrollerRect.right) {
      scroller.scrollLeft += targetRect.right - scrollerRect.right;
      shifted = true;
    }
    if (shifted) {
      suppressListFromStripUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    }
  }, [
    followSelectionRef,
    scrollerRef,
    selectedRow?.id,
    suppressListFromStripUntilRef,
  ]);

  return (
    <div
      aria-label={t("trajectory.timeline")}
      className="flex shrink-0 gap-2 border-b px-2 py-2"
    >
      <div className="flex w-16 shrink-0 flex-col space-y-1">
        {showRuler ? <div className="h-3.5" /> : null}
        {timelineLanes.map((lane) => (
          <span
            className="flex h-3 items-center text-micro font-medium text-muted-foreground"
            key={lane}
          >
            {t(`trajectory.lanes.${lane}`)}
          </span>
        ))}
      </div>
      <div
        className="min-w-0 flex-1 overflow-x-auto pb-2"
        data-testid="trajectory-timeline"
        onScroll={onScroll}
        ref={scrollerRef}
      >
        <div className="relative space-y-1">
          {showRuler ? (
            <div className="flex h-3.5">
              {items.map((item, index) => (
                <TimelineRulerItem
                  item={item}
                  key={`ruler-${index}`}
                  kneeMs={kneeMs}
                  onSelectRow={onSelectRow}
                  rowById={rowById}
                />
              ))}
            </div>
          ) : null}
          {timelineLanes.map((lane) => (
            <div className="flex h-3" key={lane}>
              {items.map((item, index) =>
                item.kind === "gap" ? (
                  <TimelineGap
                    item={item}
                    key={`gap-${lane}-${index}`}
                    kneeMs={kneeMs}
                    tagged={lane === "client"}
                  />
                ) : (
                  <TimelineCall
                    call={item.call}
                    key={`call-${lane}-${item.call.requestId}`}
                    kneeMs={kneeMs}
                    lane={lane}
                    onSelectRow={onSelectRow}
                    rowById={rowById}
                    selected={selectedRow?.requestId === item.call.requestId}
                    // Scoped to this call, so moving the selection only
                    // re-renders the two columns that actually changed rather
                    // than every column in every lane.
                    selectedRowId={
                      selectedRow?.requestId === item.call.requestId
                        ? selectedRow.id
                        : null
                    }
                  />
                ),
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function itemFlexStyle(
  item: TrajectoryTimelineItem,
  kneeMs: number,
): {
  flex: string;
  minWidth: number;
  width?: number;
} {
  if (item.kind === "gap" && item.collapsed) {
    return {
      flex: `0 0 ${TIMELINE_GAP_PX}px`,
      minWidth: TIMELINE_GAP_PX,
      width: TIMELINE_GAP_PX,
    };
  }
  const minScale = TIMELINE_KNEE_PX / kneeMs;
  if (item.kind === "gap") {
    const weight = timelineWeight(item.durationMs, kneeMs);
    // Back-to-back calls in a tool loop are milliseconds apart, which earns a
    // fraction of a pixel and fuses their bars into one. The seam is what says
    // "this is where the next call starts".
    return {
      flex: `${weight} 1 0px`,
      minWidth: Math.max(TIMELINE_GAP_MIN_PX, weight * minScale),
    };
  }
  const weight = timelineWeight(item.call.durationMs, kneeMs);
  return {
    flex: `${weight} 1 0px`,
    minWidth: Math.max(TIMELINE_MIN_CALL_PX, weight * minScale),
  };
}

const TimelineRulerItem = memo(function TimelineRulerItem({
  item,
  kneeMs,
  rowById,
  onSelectRow,
}: {
  item: TrajectoryTimelineItem;
  kneeMs: number;
  rowById: Map<string, TrajectoryRow>;
  onSelectRow: (row: TrajectoryRow) => void;
}) {
  const turnRowId = item.kind === "call" ? item.call.turnRowId : item.turnRowId;
  const turnFirst = item.kind === "call" && item.call.turnFirst;
  const turnRow =
    turnFirst && item.call.turnRowId
      ? (rowById.get(item.call.turnRowId) ?? null)
      : null;
  return (
    <div
      className={cn(
        "flex h-3.5 items-center",
        turnRowId && "border-b border-border",
      )}
      style={itemFlexStyle(item, kneeMs)}
    >
      {turnRow ? (
        <Button
          aria-label={turnRow.summary}
          className="h-3.5 min-w-0 truncate rounded-none bg-transparent p-0 text-micro leading-none font-medium text-muted-foreground hover:bg-transparent hover:text-foreground"
          data-testid="trajectory-turn-label"
          onClick={() => onSelectRow(turnRow)}
          title={turnRow.summary}
          type="button"
          variant="ghost"
        >
          {item.kind === "call" && item.call.turnIndex !== null
            ? item.call.turnIndex
            : "—"}
        </Button>
      ) : null}
    </div>
  );
});

const TimelineGap = memo(function TimelineGap({
  item,
  kneeMs,
  tagged = false,
}: {
  item: Extract<TrajectoryTimelineItem, { kind: "gap" }>;
  kneeMs: number;
  tagged?: boolean;
}) {
  const style = itemFlexStyle(item, kneeMs);
  if (!item.collapsed) {
    return <span aria-hidden style={style} />;
  }
  return (
    <span
      aria-hidden={!tagged}
      className="flex h-3 shrink-0 items-center"
      data-testid={tagged ? "trajectory-gap" : undefined}
      style={style}
      title={
        tagged
          ? i18n.t("trajectory.gap", {
              duration: formatDuration(item.durationMs),
            })
          : undefined
      }
    >
      <span className="w-full border-t border-dashed border-border" />
    </span>
  );
});

const TimelineCall = memo(function TimelineCall({
  call,
  kneeMs,
  lane,
  rowById,
  selected,
  selectedRowId,
  onSelectRow,
}: {
  call: TrajectoryTimelineCall;
  kneeMs: number;
  lane: TrajectoryLane;
  rowById: Map<string, TrajectoryRow>;
  selected: boolean;
  selectedRowId: string | null;
  onSelectRow: (row: TrajectoryRow) => void;
}) {
  const firstLanePhase = call.phases.find((phase) => phase.lane === lane);
  const laneRow = firstLanePhase
    ? (rowById.get(firstLanePhase.rowId) ?? null)
    : null;
  return (
    <div
      className={cn(
        "relative h-3 overflow-hidden rounded-sm bg-muted/70",
        laneRow && "cursor-pointer",
        selected && "ring-1 ring-primary ring-inset",
      )}
      data-lane={lane}
      data-request-id={call.requestId}
      data-testid="trajectory-call"
      onClick={(event) => {
        if (
          (event.target as HTMLElement).closest(
            '[data-testid="trajectory-phase"]',
          )
        ) {
          return;
        }
        if (laneRow) onSelectRow(laneRow);
      }}
      style={itemFlexStyle({ kind: "call", call }, kneeMs)}
      title={`${call.summary} · ${call.result} · ${formatDuration(call.durationMs)}`}
    >
      {call.phases
        .filter((phase) => phase.lane === lane)
        .map((phase) => (
          <TimelinePhaseSlot
            callDurationMs={call.durationMs}
            key={phase.rowId}
            onSelectRow={onSelectRow}
            phase={phase}
            rowById={rowById}
            selected={phase.rowId === selectedRowId}
          />
        ))}
    </div>
  );
});

/**
 * Gateway and client events last microseconds to milliseconds against an
 * upstream wait measured in seconds, so they cannot carry an honest width.
 * They draw as fixed ticks placed at their real offset; only the upstream bar
 * is sized by its share of the call. Positioning them absolutely also keeps
 * them out of the column's width budget, so a column is only as wide as its
 * duration earns.
 */
const TimelinePhaseSlot = memo(function TimelinePhaseSlot({
  phase,
  callDurationMs,
  rowById,
  selected,
  onSelectRow,
}: {
  phase: TrajectoryTimelinePhase;
  callDurationMs: number;
  rowById: Map<string, TrajectoryRow>;
  selected: boolean;
  onSelectRow: (row: TrajectoryRow) => void;
}) {
  const t = useT();
  const span = Math.max(1, callDurationMs);
  const leftPercent = Math.min(100, (phase.startMs / span) * 100);
  const tick = phase.lane !== "upstream";
  const style = tick
    ? {
        left: `min(${leftPercent}%, calc(100% - ${TIMELINE_TICK_PX}px))`,
        width: TIMELINE_TICK_PX,
      }
    : {
        left: `${leftPercent}%`,
        width: `max(${TIMELINE_BAR_MIN_PX}px, ${(phase.durationMs / span) * 100}%)`,
      };
  const row = rowById.get(phase.rowId);
  return (
    <Button
      className={cn(
        // The phase tone is the whole point of the mark, so no hover tint.
        "absolute inset-y-0 h-auto min-w-0 rounded-none p-0 hover:bg-inherit",
        chipToneClass(phase.chip, phase.tone),
        selected && "z-10 ring-1 ring-primary ring-inset",
      )}
      data-chip={phase.chip}
      data-row-id={phase.rowId}
      data-testid="trajectory-phase"
      onClick={() => {
        if (row) onSelectRow(row);
      }}
      style={style}
      title={`${t(`trajectory.chips.${phase.chip}`)} · ${phase.summary}${row?.result ? ` · ${row.result}` : ""} · ${formatDuration(phase.durationMs)}`}
      type="button"
      variant="ghost"
    />
  );
});

function chainHighlighted(
  row: TrajectoryRow,
  requestId: string | null,
  includeTurn: boolean,
): boolean {
  if (requestId === null || row.requestId !== requestId) return false;
  return row.chip !== "TURN" || includeTurn;
}

function findRecord(
  turns: RequestRecord[],
  childrenByRoot: Record<string, RequestRecord[]>,
  requestId: string,
): RequestRecord | null {
  for (const turn of turns) {
    if (turn.id === requestId) return turn;
    const child = (childrenByRoot[turn.id] ?? []).find(
      (item) => item.id === requestId,
    );
    if (child) return child;
  }
  return null;
}

/**
 * Memoized and handed a stable `onSelect`, so moving the selection repaints the
 * two rows that changed instead of all 1400 of them.
 */
const TrajectoryRowView = memo(function TrajectoryRowView({
  row,
  service,
  selected,
  highlighted,
  position,
  offsetPx,
  measureRef,
  onSelect,
}: {
  row: TrajectoryRow;
  service?: RequestServiceIdentity;
  selected: boolean;
  highlighted: boolean;
  position: number;
  /** Set only while the list is windowed, where rows are placed by transform. */
  offsetPx?: number;
  measureRef?: (node: Element | null) => void;
  onSelect: (row: TrajectoryRow) => void;
}) {
  const t = useT();
  const started = new Date(row.startedAt);
  const ended = row.endedAt === null ? NaN : Date.parse(row.endedAt);
  const duration =
    Number.isFinite(ended) && Number.isFinite(started.getTime())
      ? formatDuration(Math.max(0, ended - started.getTime()))
      : row.tone === "pending"
        ? t("status.pending")
        : "\u2014";
  const time = Number.isFinite(started.getTime())
    ? started.toLocaleTimeString(i18n.language, { hour12: false })
    : "\u2014";
  return (
    <li
      data-index={position}
      ref={measureRef}
      style={
        offsetPx === undefined
          ? undefined
          : {
              position: "absolute",
              top: 0,
              left: 0,
              width: "100%",
              transform: `translateY(${offsetPx}px)`,
            }
      }
    >
      <Button
        aria-current={selected ? "true" : undefined}
        className={cn(
          ROW_COLUMNS,
          "group relative h-8 w-full shrink-0 items-center rounded-none border-b border-border/60 bg-transparent px-2 py-0 text-left text-xs font-normal text-foreground shadow-none hover:bg-muted focus-visible:-outline-offset-2 focus-visible:outline-2 focus-visible:outline-ring focus-visible:ring-0",
          row.chip === "TURN" && "border-border bg-muted/70 font-medium",
          row.tone === "failed" && "bg-danger-wash/60 hover:bg-danger-wash",
          row.tone === "blocked" && "bg-blocked-wash/60 hover:bg-blocked-wash",
          row.tone === "cancelled" &&
            "bg-warning-wash/50 hover:bg-warning-wash",
          (selected || highlighted) && "bg-accent hover:bg-accent",
          selected &&
            "before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary",
        )}
        data-chip={row.chip}
        data-highlighted={highlighted ? "true" : undefined}
        data-request-id={row.requestId}
        data-row-id={row.id}
        data-selected={selected ? "true" : undefined}
        data-testid="trajectory-row"
        data-tone={row.tone}
        onClick={() => onSelect(row)}
        type="button"
        variant="ghost"
      >
        {row.chip === "TURN" ? (
          <span className="flex items-center gap-1.5 text-micro text-muted-foreground">
            <MessageSquare aria-hidden="true" className="size-3" />
            {t("trajectory.chips.TURN")}
          </span>
        ) : (
          <Badge
            className={cn(
              "h-5 max-w-full",
              chipToneClass(row.chip, row.tone, "subtle"),
            )}
            variant="secondary"
          >
            {t(`trajectory.chips.${row.chip}`)}
          </Badge>
        )}
        <time
          className="hidden font-mono text-micro text-muted-foreground @min-[760px]/trajectory:block"
          dateTime={row.startedAt}
          title={row.startedAt}
        >
          {time}
        </time>
        <span className="flex min-w-0 items-center gap-2" title={row.summary}>
          {service && (row.chip === "UPSTREAM" || row.chip === "RETRY") ? (
            <RequestServiceLabel
              className="max-w-[65%] shrink-0 font-medium"
              service={service}
            />
          ) : null}
          <span className="truncate">{row.summary}</span>
        </span>
        <span
          className={cn(
            "flex min-w-0 items-center justify-end gap-1.5 text-micro text-muted-foreground",
            row.tone === "failed" && "text-danger-foreground",
            row.tone === "blocked" && "text-blocked-foreground",
            row.tone === "cancelled" && "text-warning-foreground",
          )}
          title={row.result}
        >
          {row.chip !== "TURN" ? (
            <StatusDot
              tone={
                row.tone === "ok"
                  ? "positive"
                  : row.tone === "failed"
                    ? "negative"
                    : row.tone === "cancelled"
                      ? "neutral"
                      : row.tone
              }
            />
          ) : null}
          <span className="truncate">{row.result}</span>
        </span>
        <span
          className="hidden truncate text-right font-mono text-micro tabular-nums text-muted-foreground @min-[560px]/trajectory:block"
          title={duration}
        >
          {duration}
        </span>
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "size-3 text-muted-foreground/40 group-hover:text-foreground",
            selected && "text-primary",
          )}
        />
      </Button>
    </li>
  );
});
