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

import { ArrowDown, ArrowUp, ChevronRight } from "@/components/icons";
import { FindBar } from "@/components/FindBar";
import { IconButton } from "@/components/IconButton";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

import type { CopyFeedback } from "./copy-feedback";
import {
  findMatcher,
  findShortcutLabel,
  findStepShortcut,
  isFindShortcut,
  stepFind,
} from "./find-model";
import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
import { formatDuration } from "./request-live-model";
import type { AuditContent, RequestRecord } from "./request-record-model";
import { readableRouteRows } from "./request-routing-model";
import {
  requestServiceIdentity,
  routeServices,
  type RequestServiceIdentity,
  type RequestServiceMap,
} from "./request-service-model";
import {
  extendPendingTimeline,
  findInTrajectory,
  foldTimelineColumns,
  timelineWeight,
  trajectoryListLayout,
  trajectoryListSummaries,
  trajectoryRows,
  trajectoryTimeline,
  type TrajectoryCallColumn,
  type TrajectoryLane,
  type TrajectoryRow,
  type TrajectoryTimeline,
  type TrajectoryTimelineCall,
  type TrajectoryTimelineItem,
  type TrajectoryTimelinePhase,
  trajectoryChipLabel,
} from "./request-trajectory-model";
import { chipToneClass } from "./trajectory-chip";
import { TrajectoryInspector } from "./TrajectoryInspector";
import { useDetachedInspector } from "./trajectory-inspector-window";
import { trajectoryCardLayout } from "./trajectory-card-model";
import {
  TrajectoryFlowList,
  type TrajectoryFlowListHandle,
} from "./TrajectoryFlowList";
import {
  flowDurationScale,
  flowListScrollForTimeline,
  flowTimelineScrollForList,
  trajectoryFlowEntries,
  trajectoryTurnDigests,
} from "./trajectory-flow-model";

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
  onUnlockRaw,
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
  /** Opens the raw unlock from the overlay inspector. */
  onUnlockRaw?: () => void;
}) {
  const t = useT();
  // The strip is the overview; on a short or narrow window the call list
  // needs that height more, so the strip starts folded there.
  const compactViewport = useMediaQuery(
    "(max-height: 780px), (max-width: 760px)",
  );
  const [timelineOverride, setTimelineOverride] = useState<boolean | null>(
    null,
  );
  const timelineOpen = timelineOverride ?? !compactViewport;
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
  const rows = useMemo(() => {
    const records = new Map(
      [...turns, ...Object.values(childrenByRoot).flat()].map((record) => [
        record.id,
        record,
      ]),
    );
    // Keep the original event metadata; only the route text is for display.
    return readableRouteRows(
      trajectoryRows(turns, childrenByRoot),
      records,
      services,
    );
  }, [childrenByRoot, turns, services]);
  const listSummaries = useMemo(() => trajectoryListSummaries(rows), [rows]);
  const cardRows = useMemo(
    () => trajectoryCardLayout(rows, Date.now()),
    [rows],
  );
  // Resolving a row id by scanning `rows` costs nothing once, and used to cost
  // a full scan inside every phase mark of every lane: 1300 marks against 1400
  // rows is close to two million comparisons per paint.
  const rowIndex = useMemo(() => {
    const byId = new Map<string, TrajectoryRow>();
    const firstPhaseByRequestId = new Map<string, TrajectoryRow>();
    for (const row of rows) {
      byId.set(row.id, row);
      if (row.chip !== "TURN" && !firstPhaseByRequestId.has(row.requestId)) {
        firstPhaseByRequestId.set(row.requestId, row);
      }
    }
    return { byId, firstPhaseByRequestId };
  }, [rows]);
  // The strip advances its own open calls, so the settled layout is built once
  // per turn set instead of once per clock tick. See `extendPendingTimeline`.
  const timeline = useMemo(() => trajectoryTimeline(rows, Date.now()), [rows]);
  // Folded turns still report their calls, and every call is scaled against
  // the longest one in the session, not only against those listed.
  const flowEntries = useMemo(
    () => trajectoryFlowEntries(rows, cardRows),
    [rows, cardRows],
  );
  const turnDigests = useMemo(
    () => trajectoryTurnDigests(flowEntries),
    [flowEntries],
  );
  const durationScale = useMemo(
    () => flowDurationScale(flowEntries, timeline.kneeMs, Date.now()),
    [flowEntries, timeline.kneeMs],
  );
  const sessionKey = turns[0]?.session_id ?? turns[0]?.id ?? "";
  const [selectedRowId, setSelectedRowId] = useState<string | null>(null);
  // Only the turns the operator opened or closed by hand. Every other turn
  // follows `trajectoryListLayout`: the latest one open, the rest folded.
  const [turnOverrides, setTurnOverrides] = useState<
    ReadonlyMap<string, boolean>
  >(() => new Map());
  const find = useTrajectoryFind(rows, serviceByRequest, rowIndex.byId);
  const listLayout = useMemo(
    () =>
      trajectoryListLayout(rows, turnOverrides, selectedRowId, find.foundTurn),
    [rows, turnOverrides, selectedRowId, find.foundTurn],
  );
  const listRows = listLayout.rows;
  const [overlayOpen, setOverlayOpen] = useState(true);
  const [highlightedRequestId, setHighlightedRequestId] = useState<
    string | null
  >(null);
  const [revealNonce, setRevealNonce] = useState(0);
  // The call a jump landed on glows once; a click inside the list does not.
  const [flash, setFlash] = useState<{
    requestId: string;
    key: number;
  } | null>(null);
  // A pick made after find landed hands the open call back to the selection
  // until find steps again.
  const [findDismissedSeq, setFindDismissedSeq] = useState<number | null>(null);
  const findSeqRef = useRef(find.seq);
  findSeqRef.current = find.seq;
  const listRef = useRef<HTMLDivElement>(null);
  const flowRef = useRef<TrajectoryFlowListHandle>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const suppressStripFromListUntilRef = useRef(0);
  const suppressListFromStripUntilRef = useRef(0);
  const followSelectionOnStripRef = useRef(false);
  const syncFromRef = useRef<"list" | "strip" | null>(null);
  const syncFrameRef = useRef<number | null>(null);
  // The scroll sync maps the rows the list draws, not every row.
  const listLayoutRef = useRef(listLayout);
  listLayoutRef.current = listLayout;
  const selectedRowIdRef = useRef(selectedRowId);
  selectedRowIdRef.current = selectedRowId;

  // No row id means "the latest row", so an untouched list follows new turns.
  // A row the operator picked stays picked when a turn lands: resetting it on
  // every new turn moved the list and the open inspector window off the call
  // being read.
  useEffect(() => {
    setSelectedRowId(null);
    setTurnOverrides(new Map());
    setOverlayOpen(true);
    setHighlightedRequestId(null);
    setFlash(null);
    setFindDismissedSeq(null);
  }, [sessionKey]);

  // Find opens the turn it lands on even where the operator folded it; one
  // folded again after the landing stays folded until find steps back to it.
  const { foundTurn, seq: findSeq, stop: findStop } = find;
  useEffect(() => {
    if (!foundTurn) return;
    setTurnOverrides((current) => {
      if (current.get(foundTurn) !== false) return current;
      const next = new Map(current);
      next.delete(foundTurn);
      return next;
    });
  }, [foundTurn, findSeq]);

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
            services: routeServices(selectedRecord, services),
          }
        : null,
    [selectedRecord, selectedRow, serviceByRequest, services],
  );
  const inspectorWindow = useDetachedInspector(selection);

  const selectRow = useCallback(
    (row: TrajectoryRow, options?: { reveal?: boolean; inspect?: boolean }) => {
      const { openTurns, turnByRowId } = listLayoutRef.current;
      const previous = turnByRowId.get(selectedRowIdRef.current ?? "");
      setTurnOverrides((current) =>
        withTurnsOpen(current, [
          turnByRowId.get(row.id),
          // The turn the last pick held open stays open: folding it would pull
          // the rows below it up under the cursor. One folded by hand stays so.
          previous !== undefined &&
          !current.has(previous) &&
          openTurns.has(previous)
            ? previous
            : undefined,
        ]),
      );
      setSelectedRowId(row.id);
      setFindDismissedSeq(findSeqRef.current);
      onSelectRequest(row.requestId);
      // The timeline lands on a call. Highlight every phase of that request
      // instead of opening the inspector on the one mark that was clicked.
      if (options?.inspect === false) {
        setHighlightedRequestId(row.requestId);
        if (options.reveal) {
          setRevealNonce((nonce) => nonce + 1);
          setFlash((current) => ({
            requestId: row.requestId,
            key: (current?.key ?? 0) + 1,
          }));
        }
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
          services: routeServices(record, services),
        });
      }
    },
    [
      childrenByRoot,
      inspectorWindow,
      onSelectRequest,
      turns,
      serviceByRequest,
      services,
    ],
  );
  // A header folds and unfolds its turn; it no longer opens the inspector,
  // since its first call's client row already does.
  const toggleTurn = useCallback((row: TrajectoryRow) => {
    const open = listLayoutRef.current.openTurns.has(row.id);
    setTurnOverrides((current) => new Map(current).set(row.id, !open));
  }, []);
  const selectListRow = useCallback(
    (row: TrajectoryRow) => {
      if (row.chip === "TURN") {
        toggleTurn(row);
        return;
      }
      followSelectionOnStripRef.current = true;
      selectRow(row, { inspect: false });
    },
    [selectRow, toggleTurn],
  );
  const revealRow = useCallback(
    (row: TrajectoryRow) => selectRow(row, { reveal: true, inspect: false }),
    [selectRow],
  );

  // Retries list under their root call, so measured entries already carry
  // the request id the strip draws its column for.
  const flowMeasurements = () => flowRef.current?.measurements() ?? [];
  const syncStripFromList = () => {
    const list = listRef.current;
    const scroller = scrollerRef.current;
    if (!list || !scroller) return;
    const columns = foldTimelineColumns(
      measureTimelineColumns(scroller),
      listLayoutRef.current.anchorByRequestId,
    );
    const next = Math.round(
      flowTimelineScrollForList(
        flowMeasurements(),
        columns,
        list.scrollTop,
        list.scrollHeight - list.clientHeight,
        scroller.scrollWidth - scroller.clientWidth,
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
    // A resize can remove horizontal overflow. It must not reset the list.
    const maxScroll = scroller.scrollWidth - scroller.clientWidth;
    if (maxScroll <= 0) return;
    const columns = foldTimelineColumns(
      measureTimelineColumns(scroller),
      listLayoutRef.current.anchorByRequestId,
    );
    const next = Math.round(
      flowListScrollForTimeline(
        flowMeasurements(),
        columns,
        // Match the integer scrollWidth/clientWidth, including a fractional
        // last pixel from CSS layout, so the final call reaches the list end.
        Math.round(scroller.scrollLeft),
        maxScroll,
        list.scrollHeight - list.clientHeight,
      ),
    );
    if (next === Math.round(list.scrollTop)) return;
    suppressStripFromListUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    flowRef.current?.scrollToOffset(next);
  };

  const scheduleSync = (from: "list" | "strip") => {
    if (from === "list" && Date.now() < suppressStripFromListUntilRef.current) {
      return;
    }
    // A reveal from the strip settles the list over several frames; the
    // strip the reader just clicked must not follow those scrolls.
    if (from === "list" && flowRef.current?.revealing()) return;
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
      rowIndex.byId.get(selectedRowId ?? "") ??
      rowIndex.firstPhaseByRequestId.get(highlightedRequestId);
    if (!first) return;
    suppressStripFromListUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
    flowRef.current?.revealRow(first.id, { quiet: true });
  }, [highlightedRequestId, revealNonce]);

  const foundPhaseId = listRows.find(
    (row) =>
      row.chip !== "TURN" &&
      listLayout.turnByRowId.get(row.id) === foundTurn &&
      find.queryFor(row),
  )?.id;
  // Find opens the call holding its hit on that phase, without moving the
  // selection the inspector follows. A pick made afterwards wins.
  const findFocusRowId =
    findStop !== null && findDismissedSeq !== findSeq
      ? (foundPhaseId ??
        (rowIndex.byId.get(findStop)?.chip === "TURN" ? null : findStop))
      : null;
  // Reveal once the matching phase is visible, including a manually folded turn.
  useEffect(() => {
    const list = listRef.current;
    if (!list || !findStop || list.clientHeight === 0) return;
    flowRef.current?.revealRow(foundPhaseId ?? findStop);
  }, [foundPhaseId, findStop, findSeq]);

  // ⌘F goes to the search box while the list is on screen; ⌘G steps.
  const findKeys = useRef(find);
  findKeys.current = find;
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const list = listRef.current;
      if (!list || list.getClientRects().length === 0) return;
      if (isFindShortcut(event)) {
        event.preventDefault();
        findKeys.current.focus();
        return;
      }
      const direction = findStepShortcut(event);
      if (direction === null) return;
      event.preventDefault();
      findKeys.current.step(direction);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !sessionKey) return;
    let cancelled = false;
    let observer: ResizeObserver | undefined;
    const scrollToLatest = () => {
      if (!flowRef.current || list.clientHeight === 0) return false;
      suppressListFromStripUntilRef.current = Date.now() + REVEAL_SUPPRESS_MS;
      flowRef.current.scrollToLatest();
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
    <Collapsible
      className="@container/trajectory flex h-full min-h-0 min-w-0 flex-1 flex-col"
      open={timelineOpen}
      onOpenChange={setTimelineOverride}
    >
      {/* Keep the strip mounted so its scroll position and observers survive. */}
      <CollapsibleContent
        forceMount
        className="shrink-0 data-[state=closed]:hidden"
      >
        <TrajectoryTimelineView
          visible={timelineOpen}
          followSelectionRef={followSelectionOnStripRef}
          onScroll={() => scheduleSync("strip")}
          onSelectRow={revealRow}
          rowById={rowIndex.byId}
          scrollerRef={scrollerRef}
          selectedRow={selectedRow}
          suppressListFromStripUntilRef={suppressListFromStripUntilRef}
          timeline={timeline}
        />
      </CollapsibleContent>
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-2 text-micro text-muted-foreground">
        <CollapsibleTrigger asChild>
          <Button size="sm" variant="ghost" className="gap-1 px-1.5">
            <ChevronRight
              aria-hidden="true"
              className={cn(
                "size-3 transition-transform",
                timelineOpen && "rotate-90",
              )}
            />
            {t("trajectory.timeline")}
          </Button>
        </CollapsibleTrigger>
        <span className="hidden shrink-0 tabular-nums @min-[560px]/trajectory:inline">
          {t("trajectory.eventCount", {
            count: rows.filter((row) => row.chip !== "TURN").length,
          })}
        </span>
        {/* Always on screen, so find is seen rather than remembered. */}
        <FindBar
          active={find.active}
          className="min-w-0 flex-1"
          disabled={rows.length === 0}
          inputRef={find.inputRef}
          label={t("find.turnsLabel")}
          onQueryChange={find.setQuery}
          onStep={find.step}
          placeholder={t("find.turnsPlaceholder")}
          query={find.query}
          result={find.result}
          shortcut={findShortcutLabel()}
        />
        <span
          className="hidden shrink-0 @min-[760px]/trajectory:inline"
          title={t("trajectory.timingHint")}
        >
          {t("trajectory.timingLabel")}
        </span>
        <span className="flex shrink-0 items-center gap-0.5">
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
      </div>
      <div className="relative flex min-h-0 min-w-0 flex-1">
        <TrajectoryFlowList
          key={sessionKey}
          ref={flowRef}
          scrollRef={listRef}
          rows={listRows}
          cards={cardRows}
          summaries={listSummaries}
          services={serviceByRequest}
          selectedRow={selectedRow}
          focusRowId={findFocusRowId ?? selectedRow?.id ?? null}
          highlightedRequestId={highlightedRequestId}
          flash={flash}
          expandedTurns={listLayout.openTurns}
          turnDigests={turnDigests}
          durationScale={durationScale}
          queryFor={find.queryFor}
          stopState={find.stopState}
          onSelect={selectListRow}
          onInspect={selectRow}
          onScroll={() => scheduleSync("list")}
        />
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
              onUnlockRaw={onUnlockRaw}
              record={selection.record}
              row={selection.row}
              service={selection.service}
              services={selection.services}
            />
          </aside>
        ) : null}
      </div>
    </Collapsible>
  );
}

type FindStopState = "stop" | "active";

/**
 * Find over the whole conversation, landing on turns. The query and the
 * position live here; the list reads which turn to open and scroll to, and
 * which rows to mark.
 */
function useTrajectoryFind(
  rows: TrajectoryRow[],
  serviceByRequest: Record<string, RequestServiceIdentity>,
  rowById: Map<string, TrajectoryRow>,
) {
  const [query, setQueryState] = useState("");
  const [active, setActive] = useState(0);
  const [seq, setSeq] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const matcher = useMemo(() => findMatcher(query), [query]);
  const found = useMemo(
    () =>
      matcher
        ? findInTrajectory(rows, matcher, (row) =>
            rowFindTexts(row, serviceByRequest[row.requestId]),
          )
        : null,
    [matcher, rows, serviceByRequest],
  );
  const count = found?.stops.length ?? 0;
  const position = Math.min(active, Math.max(0, count - 1));
  const stop = count > 0 ? found!.stops[position]! : null;
  const foundTurn = stop && rowById.get(stop)?.chip === "TURN" ? stop : null;
  const stops = useMemo(() => new Set(found?.stops), [found]);
  const trimmed = query.trim();

  const setQuery = useCallback((next: string) => {
    setQueryState(next);
    setActive(0);
    setSeq((current) => current + 1);
  }, []);
  const step = useCallback(
    (direction: 1 | -1) => {
      if (count === 0) return;
      setActive((current) => stepFind(current, count, direction));
      setSeq((current) => current + 1);
    },
    [count],
  );
  const focus = useCallback(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  return {
    query,
    active: position,
    seq,
    stop,
    foundTurn,
    inputRef,
    result: found ? { count, capped: false } : null,
    setQuery,
    step,
    focus,
    /** The query to mark in a row, only where the row holds a hit. */
    queryFor: (row: TrajectoryRow) =>
      found?.rows.has(row.id) ? trimmed : undefined,
    stopState: (row: TrajectoryRow): FindStopState | undefined =>
      row.id === stop ? "active" : stops.has(row.id) ? "stop" : undefined,
  };
}

/** What a row reads as in the list, which is what find matches. */
function rowFindTexts(
  row: TrajectoryRow,
  service: RequestServiceIdentity | undefined,
): string[] {
  const texts = [row.summary, row.result];
  if (service && (row.chip === "UPSTREAM" || row.chip === "RETRY")) {
    texts.push(service.name);
  }
  return texts;
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
  visible,
  timeline,
  rowById,
  scrollerRef,
  selectedRow,
  followSelectionRef,
  suppressListFromStripUntilRef,
  onScroll,
  onSelectRow,
}: {
  visible: boolean;
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
    if (!visible || !followSelectionRef.current) return;
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
    visible,
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
  // The phase's title is worded in the current language: re-render with it.
  useT();
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
      title={`${trajectoryChipLabel(phase.chip, row?.title)} · ${phase.summary}${row?.result ? ` · ${row.result}` : ""} · ${formatDuration(phase.durationMs)}`}
      type="button"
      variant="ghost"
    />
  );
});

/** `current` with each turn opened, or `current` itself if all already were. */
function withTurnsOpen(
  current: ReadonlyMap<string, boolean>,
  turns: readonly (string | undefined)[],
): ReadonlyMap<string, boolean> {
  const closed = turns.filter(
    (turn): turn is string => turn !== undefined && current.get(turn) !== true,
  );
  if (closed.length === 0) return current;
  const next = new Map(current);
  for (const turn of closed) next.set(turn, true);
  return next;
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
