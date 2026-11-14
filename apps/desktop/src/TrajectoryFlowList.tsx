import {
  memo,
  useCallback,
  useEffect,
  useId,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type Ref,
  type RefObject,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import {
  ArrowRight,
  ChevronRight,
  MessageSquare,
  RotateCcw,
} from "@/components/icons";
import { ConversationIndicator } from "@/components/ConversationIndicator";
import { EmptyState } from "@/components/EmptyState";
import { HighlightedText } from "@/components/FindBar";
import { ModelLabel } from "@/components/ModelLabel";
import { RequestServiceLabel } from "@/components/RequestServiceLabel";
import { Button } from "@/components/ui/button";
import { AnchoredTooltip } from "@/components/ui/tooltip";
import {
  JourneyCall,
  JourneyEmblem,
  JourneyRail,
  JourneySparkline,
  JourneySparkStop,
  JourneyStack,
  JourneyStep,
  RollingText,
  type JourneyStatus,
} from "@/components/ui/journey";
import { ThinkingLoader } from "@/components/ui/thinking-loader";
import { cn } from "@/lib/utils";

import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
import { formatDuration } from "./request-live-model";
import type { RequestServiceIdentity } from "./request-service-model";
import {
  trajectoryChipLabel,
  type TrajectoryRow,
  type TrajectoryTone,
} from "./request-trajectory-model";
import type { TrajectoryCardRow } from "./trajectory-card-model";
import { chipDotClass, chipToneClass } from "./trajectory-chip";
import {
  flowCallSpanMs,
  flowDurationShare,
  flowFocusRow,
  flowHoldScrollDelta,
  phaseDurationMs,
  trajectoryFlowEntries,
  trajectoryFlowRowIndices,
  trajectoryFlowSteps,
  type FlowDurationScale,
  type FlowTurnDigest,
  type TrajectoryFlowCall,
  type TrajectoryFlowMeasurement,
  type TrajectoryFlowStep,
  type TrajectoryFlowTurn,
} from "./trajectory-flow-model";

/**
 * Below this the whole list is mounted, so a conversation short enough to
 * read in one sitting stays in the DOM. Above it calls are windowed by their
 * measured height, which changes as a call opens and its steps settle.
 */
const VIRTUALIZE_AFTER = 40;
/** Where a reveal puts its call: just under the list's top edge. */
const REVEAL_TOP_PX = 8;
/** Room kept under a call grown into view, the list's own bottom padding. */
const REVEAL_BOTTOM_PX = 12;
/**
 * Frames a scroll lock holds its target in place: long enough for a call to
 * open (460 ms) and for the windowed list to measure what moved.
 */
const LOCK_FRAMES = 42;
/** How long a call that just appeared keeps its entrance, in ms. */
const ENTER_MS = 1100;

type FindStop = "stop" | "active" | undefined;

export interface TrajectoryFlowListHandle {
  /** Brings the call holding `id` to the top, opened on that row. */
  revealRow: (id: string, options?: { quiet?: boolean }) => void;
  scrollToLatest: () => void;
  scrollToOffset: (offset: number) => void;
  measurements: () => TrajectoryFlowMeasurement[];
  /** A quiet reveal is still settling; its scrolls are not the reader's. */
  revealing: () => boolean;
}

interface FlowListProps {
  ref: Ref<TrajectoryFlowListHandle>;
  scrollRef: RefObject<HTMLDivElement | null>;
  rows: TrajectoryRow[];
  cards: ReadonlyMap<string, TrajectoryCardRow>;
  summaries: ReadonlyMap<string, string>;
  services: Record<string, RequestServiceIdentity>;
  /** The row the inspector follows. */
  selectedRow: TrajectoryRow | null;
  /** The row whose call is open: a find hit, or else the selection. */
  focusRowId: string | null;
  highlightedRequestId: string | null;
  /** Where the last jump landed, replayed whenever its key changes. */
  flash: { requestId: string; key: number } | null;
  expandedTurns: ReadonlySet<string>;
  turnDigests: ReadonlyMap<string, FlowTurnDigest>;
  durationScale: FlowDurationScale;
  queryFor: (row: TrajectoryRow) => string | undefined;
  stopState: (row: TrajectoryRow) => FindStop;
  onSelect: (row: TrajectoryRow) => void;
  onInspect: (row: TrajectoryRow) => void;
  onScroll: () => void;
}

interface ScrollLock {
  entryId: string;
  /** A step to hold instead of its call, for picks inside an open call. */
  rowId: string | null;
  /** Viewport offset to hold, or null to bring the call to the top. */
  offset: number | null;
  /**
   * Keep the whole call in view as it grows: it was in view before the
   * click, or the click opened it. Otherwise only the picked step and its
   * detail are kept in view.
   */
  wholeCall: boolean;
  frames: number;
  quiet: boolean;
}

/**
 * The conversation as calls, one quiet line each with the route it took.
 * The call being read opens into a card whose route becomes a rail of
 * steps, each with what the gateway recorded for it.
 */
export function TrajectoryFlowList({
  ref,
  scrollRef,
  rows,
  cards,
  summaries,
  services,
  selectedRow,
  focusRowId,
  highlightedRequestId,
  flash,
  expandedTurns,
  turnDigests,
  durationScale,
  queryFor,
  stopState,
  onSelect,
  onInspect,
  onScroll,
}: FlowListProps) {
  const t = useT();
  const entries = useMemo(
    () => trajectoryFlowEntries(rows, cards),
    [rows, cards],
  );
  const indices = useMemo(() => trajectoryFlowRowIndices(entries), [entries]);
  const entryIndex = useMemo(
    () => new Map(entries.map((entry, index) => [entry.id, index])),
    [entries],
  );
  const focusEntry =
    focusRowId === null ? undefined : entries[indices.get(focusRowId) ?? -1];
  const focusCallId = focusEntry?.kind === "call" ? focusEntry.id : null;
  // The reader can fold the open call back into its line; moving the focus
  // to another call forgets that.
  const [folded, setFolded] = useState<string | null>(null);
  useEffect(() => setFolded(null), [focusCallId]);
  const openCallId = focusCallId !== folded ? focusCallId : null;

  // One label follows the pointer along the folded routes, so sweeping across
  // a line reads each phase without waiting on a native title.
  const peekId = useId();
  const [peek, setPeek] = useState<{
    anchor: HTMLElement;
    text: string;
  } | null>(null);
  const peekAt = useCallback(
    (anchor: HTMLElement | null, text?: string) =>
      setPeek(anchor && text ? { anchor, text } : null),
    [],
  );
  const endPeek = useCallback(() => setPeek(null), []);

  const virtualized = entries.length >= VIRTUALIZE_AFTER;
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    getItemKey: (index) => entries[index]!.id,
    estimateSize: (index) => {
      const entry = entries[index];
      if (entry?.kind === "turn") return 36;
      return entry?.id === openCallId ? 64 + entry.rows.length * 30 : 44;
    },
    overscan: 6,
    paddingStart: 8,
    paddingEnd: 12,
    enabled: virtualized,
  });
  const virtualItems = virtualized ? virtualizer.getVirtualItems() : null;

  // Entrances play for calls that just appeared and for a call just opened,
  // never for one the window remounts while scrolling.
  const nowMs = performance.now();
  const seen = useRef<Set<string> | null>(null);
  const enterUntil = useRef(new Map<string, number>());
  const openedUntil = useRef(new Map<string, number>());
  const lastOpen = useRef<string | null | undefined>(undefined);
  for (const entry of entries) {
    if (!seen.current?.has(entry.id) && !enterUntil.current.has(entry.id)) {
      enterUntil.current.set(entry.id, nowMs + ENTER_MS);
    }
  }
  if (openCallId !== null && openCallId !== lastOpen.current) {
    openedUntil.current.set(openCallId, nowMs + ENTER_MS);
  }
  useEffect(() => {
    seen.current = new Set(entries.map((entry) => entry.id));
    lastOpen.current = openCallId;
    const now = performance.now();
    for (const until of [enterUntil.current, openedUntil.current]) {
      for (const [id, end] of until) if (end < now) until.delete(id);
    }
  });
  const enterRank = new Map<string, number>();
  for (const entry of entries) {
    if ((enterUntil.current.get(entry.id) ?? 0) > nowMs) {
      enterRank.set(entry.id, enterRank.size);
    }
  }

  // A pick or a reveal holds its call in place while the calls around it
  // open and close, until the reader scrolls themselves.
  const lock = useRef<ScrollLock | null>(null);
  const lockFrame = useRef<number | null>(null);
  // The reader is at the end, so growth below keeps them there.
  const stick = useRef(false);
  // Where the list last stood, its length unknown until the first scroll. A
  // call folding shortens the list and the browser pulls the offset up, but
  // that scroll event lands after the next call has grown and looks like the
  // reader leaving. A move up is theirs only over a list of the same length,
  // or while they hold the list.
  const mark = useRef({ top: 0, height: Infinity });
  // The reader holds the scrollbar or touches the list; their moves count
  // even while the windowed list measures new rows under them.
  const held = useRef(false);
  /** The reader scrolls up; a list with no room above stays at its end. */
  const leaveEnd = (list: HTMLElement) => {
    if (list.scrollTop > 0) stick.current = false;
  };
  const lockState = useRef({ entryIndex, virtualized, virtualizer });
  lockState.current = { entryIndex, virtualized, virtualizer };

  const lockTarget = (current: ScrollLock) => {
    const list = scrollRef.current;
    if (!list) return null;
    return (
      (current.rowId === null
        ? null
        : list.querySelector<HTMLElement>(
            `[data-testid="trajectory-row"][data-row-id="${current.rowId}"]`,
          )) ??
      list.querySelector<HTMLElement>(`[data-flow-id="${current.entryId}"]`)
    );
  };
  const releaseLock = () => {
    lock.current = null;
    if (lockFrame.current !== null) cancelAnimationFrame(lockFrame.current);
    lockFrame.current = null;
  };
  const holdLock = () => {
    if (lockFrame.current !== null) cancelAnimationFrame(lockFrame.current);
    const step = () => {
      lockFrame.current = null;
      const current = lock.current;
      const list = scrollRef.current;
      if (!current || !list) return;
      const target = lockTarget(current);
      const { entryIndex, virtualized, virtualizer } = lockState.current;
      if (!target) {
        const index = entryIndex.get(current.entryId);
        if (virtualized && index !== undefined) {
          virtualizer.scrollToIndex(index, { align: "start" });
        }
      } else if (list.clientHeight > 0 && target.offsetHeight > 0) {
        const view = list.getBoundingClientRect().top;
        // What grows below the pick follows it up into view, frame by frame
        // with the opening, so a call at the end of the list never ends
        // below its edge.
        const region =
          (current.wholeCall
            ? list.querySelector<HTMLElement>(
                `[data-flow-id="${current.entryId}"]`,
              )
            : target.closest("li")) ?? target;
        const delta = flowHoldScrollDelta({
          pickTop: target.getBoundingClientRect().top - view,
          heldTop: current.offset,
          regionBottom: region.getBoundingClientRect().bottom - view,
          listHeight: list.clientHeight,
          topGap: REVEAL_TOP_PX,
          bottomGap: REVEAL_BOTTOM_PX,
        });
        if (Math.abs(delta) >= 1) list.scrollTop += delta;
      }
      current.frames -= 1;
      if (current.frames > 0) {
        lockFrame.current = requestAnimationFrame(step);
        return;
      }
      lock.current = null;
      // A pick that ends at the bottom keeps following what grows there.
      const range = list.scrollHeight - list.clientHeight;
      if (list.clientHeight > 0 && range > 0) {
        stick.current = range - list.scrollTop <= 2;
      }
    };
    lockFrame.current = requestAnimationFrame(step);
  };
  /** Holds where the clicked call or step sits while the list rearranges. */
  const keepInPlace = (entryId: string, rowId: string | null) => {
    const list = scrollRef.current;
    const current: ScrollLock = {
      entryId,
      rowId,
      offset: null,
      wholeCall: rowId === null,
      frames: LOCK_FRAMES,
      quiet: false,
    };
    const target = lockTarget(current);
    if (!list || !target || list.clientHeight === 0) return;
    const view = list.getBoundingClientRect();
    current.offset = target.getBoundingClientRect().top - view.top;
    const call = list.querySelector<HTMLElement>(`[data-flow-id="${entryId}"]`);
    if (call && call.getBoundingClientRect().bottom <= view.bottom + 1) {
      current.wholeCall = true;
    }
    stick.current = false;
    lock.current = current;
    holdLock();
  };
  useEffect(() => releaseLock, []);
  // A hold ends a frame after the release, since a drag's last scroll
  // events can arrive after it.
  useEffect(() => {
    let frame = 0;
    const release = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        held.current = false;
      });
    };
    const ends = ["pointerup", "touchend", "touchcancel"] as const;
    for (const type of ends) window.addEventListener(type, release);
    return () => {
      cancelAnimationFrame(frame);
      for (const type of ends) window.removeEventListener(type, release);
    };
  }, []);

  const scrollToIndex = (index: number, align: "start" | "end") => {
    if (virtualized) {
      virtualizer.scrollToIndex(index, { align });
      return;
    }
    const list = scrollRef.current;
    const node = list?.querySelector<HTMLElement>(
      `[data-flow-index="${index}"]`,
    );
    if (!list || !node) return;
    list.scrollTop =
      align === "start"
        ? Math.max(0, node.offsetTop - REVEAL_TOP_PX)
        : Math.max(0, node.offsetTop + node.offsetHeight - list.clientHeight);
  };

  useImperativeHandle(ref, () => ({
    revealRow: (id, options) => {
      const index = indices.get(id);
      if (index === undefined) return;
      stick.current = false;
      lock.current = {
        entryId: entries[index]!.id,
        rowId: null,
        offset: null,
        wholeCall: false,
        frames: LOCK_FRAMES,
        quiet: options?.quiet ?? false,
      };
      scrollToIndex(index, "start");
      holdLock();
    },
    scrollToLatest: () => {
      releaseLock();
      if (!entries.length) return;
      stick.current = true;
      if (virtualized) {
        virtualizer.scrollToIndex(entries.length - 1, { align: "end" });
        return;
      }
      const list = scrollRef.current;
      if (list) list.scrollTop = list.scrollHeight;
    },
    scrollToOffset: (offset) => {
      releaseLock();
      const list = scrollRef.current;
      if (list) {
        stick.current = offset >= list.scrollHeight - list.clientHeight - 2;
      }
      if (virtualized) virtualizer.scrollToOffset(offset, { align: "start" });
      else if (list) list.scrollTop = offset;
    },
    measurements: () =>
      virtualized
        ? virtualizer.measurementsCache.map((item) => ({
            id: entries[item.index]!.id,
            requestId: entries[item.index]!.requestId,
            start: item.start,
            size: item.size,
          }))
        : entries.flatMap((entry, index) => {
            const node = scrollRef.current?.querySelector<HTMLElement>(
              `[data-flow-index="${index}"]`,
            );
            return node
              ? [
                  {
                    id: entry.id,
                    requestId: entry.requestId,
                    start: node.offsetTop,
                    size: node.offsetHeight,
                  },
                ]
              : [];
          }),
    revealing: () => Boolean(lock.current?.quiet),
  }));

  // Growth below the reader, or a lower viewport, keeps them at the end only
  // while they are there.
  const contentRef = useRef<HTMLOListElement>(null);
  useEffect(() => {
    const list = scrollRef.current;
    const content = contentRef.current;
    if (!list || !content || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      if (!stick.current || lock.current || list.clientHeight === 0) return;
      const range = list.scrollHeight - list.clientHeight;
      if (range - list.scrollTop > 1) list.scrollTop = range;
      // The scroll event this raises is the list's own, not the reader's.
      mark.current = { top: list.scrollTop, height: list.scrollHeight };
    });
    observer.observe(content);
    observer.observe(list);
    return () => observer.disconnect();
  }, [scrollRef, entries.length > 0]);

  // Arrow keys walk the open call's steps; focus follows once they render.
  const focusAfterRender = useRef<string | null>(null);
  useLayoutEffect(() => {
    const rowId = focusAfterRender.current;
    if (!rowId) return;
    focusAfterRender.current = null;
    const node = scrollRef.current?.querySelector<HTMLElement>(
      `[data-testid="trajectory-row"][data-row-id="${rowId}"]`,
    );
    node?.focus({ preventScroll: true });
    if (node) keepInPlace(focusEntry?.id ?? "", rowId);
  });

  const focusCall = (entry: TrajectoryFlowCall) => {
    if (entry.id === focusCallId) {
      keepInPlace(entry.id, null);
      setFolded((current) => (current === entry.id ? null : entry.id));
      return;
    }
    keepInPlace(entry.id, null);
    onSelect(flowFocusRow(entry, selectedRow?.chip));
  };
  const selectRow = (entry: TrajectoryFlowCall, row: TrajectoryRow) => {
    keepInPlace(entry.id, entry.id === openCallId ? row.id : null);
    if (entry.id === focusCallId) setFolded(null);
    onSelect(row);
  };
  const stepKeys = (
    entry: TrajectoryFlowCall,
    row: TrajectoryRow,
    event: KeyboardEvent,
  ) => {
    const at = entry.rows.indexOf(row);
    const next =
      event.key === "ArrowDown"
        ? entry.rows[at + 1]
        : event.key === "ArrowUp"
          ? entry.rows[at - 1]
          : event.key === "Home"
            ? entry.rows[0]
            : event.key === "End"
              ? entry.rows[entry.rows.length - 1]
              : undefined;
    if (!next || next === row) return;
    event.preventDefault();
    focusAfterRender.current = next.id;
    onSelect(next);
  };

  const renderEntry = (index: number, start?: number) => {
    const entry = entries[index]!;
    return (
      <li
        key={entry.id}
        data-index={index}
        data-flow-index={index}
        data-flow-id={entry.id}
        ref={virtualized ? virtualizer.measureElement : undefined}
        className="px-2 py-px"
        style={
          start === undefined
            ? undefined
            : {
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${start}px)`,
              }
        }
      >
        {entry.kind === "turn" ? (
          <TurnChapter
            entry={entry}
            entering={enterRank.has(entry.id)}
            enterDelayMs={Math.min(enterRank.get(entry.id) ?? 0, 10) * 40}
            open={expandedTurns.has(entry.id)}
            current={selectedRow?.id === entry.id}
            digest={turnDigests.get(entry.id)}
            query={queryFor(entry.row)}
            findStop={stopState(entry.row)}
            onToggle={onSelect}
          />
        ) : (
          <CallJourney
            entry={entry}
            open={entry.id === openCallId}
            animate={(openedUntil.current.get(entry.id) ?? 0) > nowMs}
            entering={enterRank.has(entry.id)}
            enterDelayMs={Math.min(enterRank.get(entry.id) ?? 0, 10) * 40}
            focusRowId={entry.id === focusCallId ? focusRowId : null}
            selectedRowId={
              selectedRow && indices.get(selectedRow.id) === index
                ? selectedRow.id
                : null
            }
            highlightedRequestId={
              entry.attempts.some(
                (attempt) => attempt.requestId === highlightedRequestId,
              )
                ? highlightedRequestId
                : null
            }
            flashKey={
              flash &&
              entry.attempts.some(
                (attempt) => attempt.requestId === flash.requestId,
              )
                ? flash.key
                : null
            }
            durationScale={durationScale}
            cards={cards}
            summaries={summaries}
            services={services}
            queryFor={queryFor}
            stopState={stopState}
            onPeek={peekAt}
            onFocusCall={focusCall}
            onSelectRow={selectRow}
            onStepKey={stepKeys}
            onInspect={onInspect}
          />
        )}
      </li>
    );
  };

  return (
    <div
      ref={scrollRef}
      data-testid="trajectory-list"
      aria-label={t("trajectory.eventList")}
      className="relative min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain [scrollbar-gutter:stable]"
      onScroll={() => {
        const list = scrollRef.current;
        if (list && list.clientHeight > 0) {
          const top = list.scrollTop;
          const height = list.scrollHeight;
          const last = mark.current;
          mark.current = { top, height };
          // A scroll that changed nothing since the last look is the list
          // being pinned to its end; it says nothing about the reader.
          if (!lock.current && (top !== last.top || height !== last.height)) {
            // At the end, or down to where it was before the list grew.
            if (
              height - list.clientHeight - top <= 2 ||
              (top > last.top && top >= last.height - list.clientHeight - 2)
            ) {
              stick.current = true;
            } else if (
              top < last.top &&
              (held.current || height === last.height)
            ) {
              stick.current = false;
            }
          }
        }
        if (!lock.current?.quiet) onScroll();
      }}
      // Anything the reader does to scroll ends a lock at once. The wheel and
      // the keys say where the reader is heading before the list can move.
      onWheel={(event) => {
        releaseLock();
        const list = event.currentTarget;
        if (event.deltaY < 0) leaveEnd(list);
        else if (
          list.scrollTop + list.clientHeight + event.deltaY >=
          list.scrollHeight - 2
        ) {
          stick.current = true;
        }
      }}
      onTouchStart={() => {
        releaseLock();
        held.current = true;
      }}
      onPointerDown={(event) => {
        if (event.target !== event.currentTarget) return;
        releaseLock();
        held.current = true;
      }}
      onKeyDownCapture={(event) => {
        const up =
          ["PageUp", "Home", "ArrowUp"].includes(event.key) ||
          (event.key === " " && event.shiftKey);
        if (up || ["PageDown", "End", "ArrowDown", " "].includes(event.key)) {
          releaseLock();
          if (up) leaveEnd(event.currentTarget);
          else if (event.key === "End") stick.current = true;
        }
      }}
    >
      {!entries.length ? (
        <EmptyState className="m-3" title={t("audit.noEvents")} />
      ) : (
        <ol
          ref={contentRef}
          className="relative"
          style={
            virtualized
              ? { height: virtualizer.getTotalSize() }
              : { paddingTop: 8, paddingBottom: 12 }
          }
        >
          {virtualItems
            ? virtualItems.map((item) => renderEntry(item.index, item.start))
            : entries.map((_, index) => renderEntry(index))}
        </ol>
      )}
      {peek ? (
        <AnchoredTooltip
          anchor={peek.anchor}
          id={peekId}
          onDismiss={endPeek}
          scrollContainer={scrollRef.current}
        >
          {peek.text}
        </AnchoredTooltip>
      ) : null}
    </div>
  );
}

const TurnChapter = memo(function TurnChapter({
  entry,
  entering,
  enterDelayMs,
  open,
  current,
  digest,
  query,
  findStop,
  onToggle,
}: {
  entry: TrajectoryFlowTurn;
  entering: boolean;
  enterDelayMs: number;
  open: boolean;
  current: boolean;
  digest?: FlowTurnDigest;
  query?: string;
  findStop: FindStop;
  onToggle: (row: TrajectoryRow) => void;
}) {
  const row = entry.row;
  const duration = phaseDurationMs(row);
  return (
    <Button
      variant="ghost"
      className={cn(
        "journey-turn h-8 w-full min-w-0 justify-start gap-2 rounded-md px-2 text-left text-xs font-medium text-foreground hover:bg-muted/70 hover:text-foreground",
        findStop && "bg-tide-wash/80 shadow-[inset_2px_0_0_var(--tide)]",
        findStop === "active" && "ring-1 ring-tide ring-inset",
      )}
      data-testid="trajectory-row"
      data-chip="TURN"
      data-request-id={row.requestId}
      data-row-id={row.id}
      data-entering={entering || undefined}
      style={
        entering
          ? ({ "--journey-delay": `${enterDelayMs}ms` } as CSSProperties)
          : undefined
      }
      data-tone={row.tone}
      data-find-stop={findStop}
      aria-current={current ? "true" : undefined}
      aria-expanded={open}
      onClick={() => onToggle(row)}
    >
      <ChevronRight
        aria-hidden="true"
        className={cn(
          "size-3 text-muted-foreground transition-transform duration-300 motion-reduce:transition-none",
          open && "rotate-90",
        )}
      />
      <MessageSquare
        aria-hidden="true"
        animateOnHover={false}
        className="size-3.5 text-muted-foreground"
      />
      <span className="min-w-0 flex-1 truncate">
        <HighlightedText text={row.summary} query={query} />
      </span>
      {!open && digest ? (
        <JourneyStack
          marks={digest.tones.map(({ tone, running }) =>
            toneMark(tone, running),
          )}
        />
      ) : null}
      <span className="shrink-0 text-micro font-normal text-muted-foreground">
        {row.result}
      </span>
      {duration !== null ? (
        <span className="hidden w-14 shrink-0 text-right font-mono text-micro font-normal text-muted-foreground @min-[480px]/trajectory:block">
          {formatDuration(duration)}
        </span>
      ) : null}
    </Button>
  );
});

function toneMark(tone: TrajectoryTone, running: boolean): JourneyStatus {
  if (running) return "pending";
  if (tone === "pending") return "unknown";
  return tone;
}

const CallJourney = memo(function CallJourney({
  entry,
  open,
  animate,
  entering,
  enterDelayMs,
  focusRowId,
  selectedRowId,
  highlightedRequestId,
  flashKey,
  durationScale,
  cards,
  summaries,
  services,
  queryFor,
  stopState,
  onPeek,
  onFocusCall,
  onSelectRow,
  onStepKey,
  onInspect,
}: {
  entry: TrajectoryFlowCall;
  open: boolean;
  animate: boolean;
  entering: boolean;
  enterDelayMs: number;
  focusRowId: string | null;
  selectedRowId: string | null;
  highlightedRequestId: string | null;
  flashKey: number | null;
  durationScale: FlowDurationScale;
  cards: ReadonlyMap<string, TrajectoryCardRow>;
  summaries: ReadonlyMap<string, string>;
  services: Record<string, RequestServiceIdentity>;
  queryFor: (row: TrajectoryRow) => string | undefined;
  stopState: (row: TrajectoryRow) => FindStop;
  /** Pointer-only: each stop's accessible name already says all of it. */
  onPeek: (anchor: HTMLElement | null, text?: string) => void;
  onFocusCall: (entry: TrajectoryFlowCall) => void;
  onSelectRow: (entry: TrajectoryFlowCall, row: TrajectoryRow) => void;
  onStepKey: (
    entry: TrajectoryFlowCall,
    row: TrajectoryRow,
    event: KeyboardEvent,
  ) => void;
  onInspect: (row: TrajectoryRow) => void;
}) {
  const t = useT();
  const now = useLiveClock(entry.running, 1000);
  const steps = trajectoryFlowSteps(entry, now);
  const span = flowCallSpanMs(entry, now);
  const duration = span === null ? "—" : formatDuration(span);
  const status: JourneyStatus = entry.running
    ? "pending"
    : entry.tone === "pending"
      ? "unknown"
      : entry.tone;
  const outcome = entry.running
    ? `${trajectoryChipLabel(entry.activeRow!.chip, entry.activeRow!.title)} · ${t("status.pending")}`
    : status === "unknown"
      ? t("common.unknown")
      : entry.resultRow.result ||
        t(status === "ok" ? "status.succeeded" : `status.${status}`);
  const retries = entry.attempts.length - 1;
  const focusRow = entry.rows.find((row) => row.id === focusRowId) ?? null;
  const bodyId = `journey-${entry.id}`;
  const label = (row: TrajectoryRow) =>
    trajectoryChipLabel(row.chip, row.title);
  const serviceFor = (row: TrajectoryRow) =>
    row.chip === "UPSTREAM" || row.chip === "RETRY"
      ? services[row.requestId]
      : undefined;
  const stepDuration = (step: TrajectoryFlowStep) =>
    step.durationMs === null ? "—" : formatDuration(step.durationMs);
  const accessibleName = (step: TrajectoryFlowStep) => {
    const row = step.row;
    return [
      row === entry.firstRow ||
      (step.retryStart && row.requestId !== entry.requestId)
        ? `${t("conversation.callNumber", { index: entry.callNumber })}${row.child ? ` · ${t("trajectory.chips.RETRY")}` : ""}`
        : "",
      label(row),
      clockTime(row.startedAt),
      serviceFor(row)?.name,
      row.summary,
      row.conversationContinued ? t("trajectory.continuation") : "",
      row.result,
      `${t("records.duration")} ${stepDuration(step)}`,
    ]
      .filter(Boolean)
      .join(" · ");
  };
  const stationProps = (step: TrajectoryFlowStep) => {
    const row = step.row;
    return {
      "aria-label": accessibleName(step),
      "aria-current": row.id === selectedRowId ? ("true" as const) : undefined,
      "data-testid": "trajectory-row",
      "data-chip": row.chip,
      "data-row-id": row.id,
      "data-request-id": row.requestId,
      "data-tone": row.tone,
      "data-call-number": cards.get(row.id)?.callNumber ?? entry.callNumber,
      "data-find-stop": stopState(row),
      "data-highlighted":
        highlightedRequestId === row.requestId ? "true" : undefined,
      "data-selected": row.id === focusRowId ? "true" : undefined,
      "data-pending": step.pending ? "true" : undefined,
    };
  };

  return (
    <JourneyCall
      open={open}
      running={entry.running}
      entering={entering}
      enterDelayMs={enterDelayMs}
      flashKey={flashKey}
      data-testid="trajectory-call-card"
      data-request-id={entry.requestId}
    >
      <div
        className="journey-head"
        // The whole line opens the call; its route's steps pick themselves.
        onClick={() => onFocusCall(entry)}
      >
        <Button
          variant="ghost"
          className="journey-trigger"
          aria-expanded={open}
          aria-controls={open ? bodyId : undefined}
          aria-label={[
            t("conversation.callNumber", { index: entry.callNumber }),
            entry.primaryRow.summary,
            outcome,
            `${t("records.duration")} ${duration}`,
          ].join(" · ")}
        >
          <JourneyEmblem status={status} />
          <span className="journey-number">
            {entry.child ? "↳ " : ""}#{entry.callNumber}
          </span>
          <span className="journey-title">
            <HighlightedText
              text={entry.primaryRow.summary}
              query={queryFor(entry.primaryRow)}
            />
          </span>
          {retries > 0 ? (
            <span
              className="journey-retries"
              title={t("trajectory.chips.RETRY")}
            >
              <RotateCcw aria-hidden="true" animateOnHover={false} />
              {retries}
            </span>
          ) : null}
        </Button>
        {!open ? (
          <div className="journey-spark-slot">
            <JourneySparkline share={flowDurationShare(span, durationScale)}>
              {steps.map((step) => (
                <JourneySparkStop
                  key={step.row.id}
                  {...stationProps(step)}
                  lane={step.lane}
                  fromLane={step.fromLane}
                  grow={step.durationMs ?? 0}
                  pending={step.pending}
                  unknown={step.durationMs === null}
                  mark={
                    step.row.chip === "RESULT" ||
                    step.row.tone === "failed" ||
                    step.row.tone === "blocked"
                  }
                  markClassName={chipDotClass(step.row.chip, step.row.tone)}
                  tabIndex={-1}
                  onPointerEnter={(event) =>
                    onPeek(
                      event.currentTarget,
                      [
                        label(step.row),
                        stepDuration(step),
                        step.row.tone === "ok" &&
                        step.row.chip !== "UPSTREAM" &&
                        step.row.chip !== "RETRY" &&
                        step.row.chip !== "RESULT"
                          ? ""
                          : step.row.result,
                      ]
                        .filter(Boolean)
                        .join(" · "),
                    )
                  }
                  onPointerLeave={() => onPeek(null)}
                  onClick={(event) => {
                    event.stopPropagation();
                    onSelectRow(entry, step.row);
                  }}
                />
              ))}
            </JourneySparkline>
          </div>
        ) : null}
        <span
          className={cn("journey-outcome", entry.running && "shimmer")}
          data-tone={status}
          title={outcome}
        >
          <HighlightedText
            text={outcome}
            query={entry.running ? undefined : queryFor(entry.resultRow)}
          />
        </span>
        <span className="journey-duration">
          {entry.running ? <RollingText text={duration} /> : duration}
        </span>
      </div>
      {open ? (
        <>
          <span className="sr-only" role="status" aria-atomic="true">
            {outcome}
          </span>
          <JourneyRail id={bodyId} animate={animate}>
            {steps.map((step, index) => {
              const row = step.row;
              const selected = row.id === focusRowId;
              const service = serviceFor(row);
              const fact = summaries.get(row.id) ?? row.summary;
              const ended = row.chip === "RESULT";
              const upstream = row.chip === "UPSTREAM" || row.chip === "RETRY";
              // Every gateway hop that went fine says so; only an outcome
              // worth reading takes the column.
              const result =
                step.pending || row.tone !== "ok" || upstream || ended;
              return (
                <JourneyStep
                  key={row.id}
                  {...stationProps(step)}
                  index={index}
                  lane={step.lane}
                  fromLane={step.fromLane}
                  toLane={step.toLane}
                  pending={step.pending}
                  unknown={step.durationMs === null}
                  dwell={
                    upstream
                      ? flowDurationShare(step.durationMs, durationScale)
                      : 0
                  }
                  wait={
                    upstream
                      ? row.tone === "failed"
                        ? "failed"
                        : "upstream"
                      : undefined
                  }
                  retryStart={step.retryStart}
                  markerKind={step.pending ? "live" : ended ? "end" : "dot"}
                  markerClassName={
                    step.pending
                      ? undefined
                      : ended
                        ? chipToneClass(row.chip, row.tone)
                        : chipDotClass(row.chip, row.tone)
                  }
                  marker={ended ? <EndMark tone={row.tone} /> : null}
                  selected={selected}
                  selectionId={`journey-selection-${entry.id}`}
                  aria-pressed={selected}
                  tabIndex={selected || (!focusRow && index === 0) ? 0 : -1}
                  onClick={() => onSelectRow(entry, row)}
                  onDoubleClick={() => onInspect(row)}
                  onKeyDown={(event) => onStepKey(entry, row, event)}
                  detail={
                    selected ? (
                      <StepDetail
                        row={row}
                        shown={[fact, row.result, service?.name ?? ""]}
                        duration={stepDuration(step)}
                        pending={step.pending}
                        query={queryFor(row)}
                        onInspect={onInspect}
                      />
                    ) : null
                  }
                >
                  <span className="journey-step-label">
                    <span className="truncate">{label(row)}</span>
                    {row.conversationContinued ? (
                      <ConversationIndicator kind="continuation" />
                    ) : null}
                  </span>
                  <span className="journey-step-fact">
                    {service ? (
                      <RequestServiceLabel
                        service={service}
                        labelClassName="sr-only"
                        className={cn(
                          "shrink-0 text-foreground",
                          fact && "max-w-[55%]",
                        )}
                      />
                    ) : null}
                    {row.chip === "REDIRECT" && row.redirect ? (
                      <ModelLabel
                        model={row.redirect.from}
                        redirectedTo={row.redirect.to}
                      />
                    ) : (
                      <span className="truncate" title={row.summary}>
                        <HighlightedText
                          text={
                            fact ||
                            (row.chip === "RESULT"
                              ? t("trajectory.clientResponse")
                              : "")
                          }
                          query={queryFor(row)}
                        />
                      </span>
                    )}
                  </span>
                  <span className="journey-step-result" data-tone={row.tone}>
                    {step.pending ? (
                      <ThinkingLoader
                        className="h-auto gap-1 border-0 bg-transparent px-0 text-micro"
                        label={row.result || t("status.pending")}
                      />
                    ) : result ? (
                      <HighlightedText
                        text={row.result}
                        query={queryFor(row)}
                      />
                    ) : null}
                  </span>
                  <span className="journey-step-duration">
                    {step.pending ? (
                      <RollingText text={stepDuration(step)} />
                    ) : (
                      stepDuration(step)
                    )}
                  </span>
                </JourneyStep>
              );
            })}
          </JourneyRail>
        </>
      ) : null}
    </JourneyCall>
  );
});

function EndMark({ tone }: { tone: TrajectoryTone }) {
  if (tone === "ok") {
    return (
      <svg viewBox="0 0 16 16" className="journey-check" aria-hidden="true">
        <path d="M3.5 8.4 6.6 11.3 12.5 4.9" pathLength={1} />
      </svg>
    );
  }
  if (tone === "pending") return null;
  return (
    <svg viewBox="0 0 16 16" className="journey-check" aria-hidden="true">
      <path d={tone === "failed" ? "M4.5 4.5l7 7M11.5 4.5l-7 7" : "M4 8h8"} />
    </svg>
  );
}

function StepDetail({
  row,
  shown,
  duration,
  pending,
  query,
  onInspect,
}: {
  row: TrajectoryRow;
  /** Texts the step's own line already shows. */
  shown: string[];
  duration: string;
  pending: boolean;
  query?: string;
  onInspect: (row: TrajectoryRow) => void;
}) {
  const t = useT();
  const self = useRef<HTMLDivElement>(null);
  // The full text is repeated only for what the step's line leaves out: a
  // part folded into another step, or a line cut off at this width.
  const visible = new Set(shown.flatMap((text) => text.split(" · ")));
  const hidden = row.summary
    .split(" · ")
    .some((part) => part && !visible.has(part));
  const [cut, setCut] = useState(false);
  useLayoutEffect(() => {
    const line = self.current
      ?.closest("li")
      ?.querySelector<HTMLElement>(".journey-step-fact .truncate");
    setCut(Boolean(line && line.scrollWidth > line.clientWidth + 1));
  });
  const full = hidden || cut;
  return (
    <div
      ref={self}
      className="flex min-w-0 flex-col gap-1 text-micro text-muted-foreground"
      data-flow-preview-row-id={row.id}
    >
      {full && row.summary ? (
        <p className="text-xs break-words whitespace-pre-wrap text-foreground/80">
          <HighlightedText text={row.summary} query={query} />
        </p>
      ) : null}
      <div className="flex min-w-0 items-center gap-3">
        <time className="font-mono" dateTime={row.startedAt}>
          {clockTime(row.startedAt)}
        </time>
        <span className={cn("font-mono", pending && "text-primary")}>
          {t("records.duration")} {duration}
        </span>
        <Button
          size="xs"
          variant="ghost"
          className="ml-auto"
          onClick={() => onInspect(row)}
        >
          {t("conversation.viewCall")}
          <ArrowRight aria-hidden="true" className="size-3" />
        </Button>
      </div>
    </div>
  );
}

function clockTime(iso: string): string {
  const value = Date.parse(iso);
  return Number.isFinite(value)
    ? new Date(value).toLocaleTimeString(i18n.language, { hour12: false })
    : "—";
}
