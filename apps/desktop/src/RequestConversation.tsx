import {
  isValidElement,
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";

import {
  ArrowDown,
  ArrowUpRight,
  ChevronRight,
  Copy,
  LockKeyhole,
} from "@/components/icons";
import { ClientTypeIcon } from "@/components/ClientTypeIcon";
import { FadeLine } from "@/components/FadeLine";
import { FormMessage } from "@/components/FormMessage";
import { IconButton } from "@/components/IconButton";
import { MarkdownContent } from "@/components/MarkdownContent";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { ModelLabel } from "@/components/ModelLabel";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  CodeBlock,
  CodeBlockActions,
  CodeBlockContent,
  CodeBlockCopyButton,
  CodeBlockHeader,
  CodeBlockLine,
  CodeBlockTitle,
} from "@/components/ui/code-block";
import {
  Trace,
  TraceCollapsible,
  TraceCollapsibleContent,
  TraceCollapsibleTrigger,
  TraceDescription,
  TraceLine,
  TraceTitle,
} from "@/components/ui/trace";
import { useHighlightedCode } from "@/hooks/use-highlighted-code";
import { cn } from "@/lib/utils";

import { copyButtonLabel, type CopyFeedback } from "./copy-feedback";
import {
  compactFilePaths,
  splitFilePaths,
  type FilePathSpan,
} from "./file-path-spans";
import { formatCompactNumber } from "./format-compact-number";
import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
import type { RawSealingState } from "./raw-sealing-model";
import {
  cancelledLabel,
  conversationTurns,
  replyExcerpt,
  toolCountLabel,
  TOOL_TEXT_LIMIT,
  type ConversationCall,
  type ConversationSegment,
  type ConversationToolCall,
  type ConversationTurn,
  type TokenUsage,
} from "./request-conversation-model";
import { formatDuration } from "./request-live-model";
import type {
  AuditContent,
  RequestRecord,
  RequestSession,
} from "./request-record-model";
import {
  requestServiceIdentity,
  routeServices,
  type RequestServiceMap,
} from "./request-service-model";
import {
  inspectorChainRows,
  splitPrivacyHighlights,
  type PrivacyHighlightSpan,
  type TrajectoryRow,
  type TrajectoryTone,
} from "./request-trajectory-model";
import { TrajectoryInspector } from "./TrajectoryInspector";
import { useDetachedInspector } from "./trajectory-inspector-window";
import { useConversationContent } from "./use-conversation-content";

const NO_SERVICES: RequestServiceMap = {};
/** How far from the end the reader may drift and still count as following. */
const FOLLOW_SLACK_PX = 48;
/** The longest a session's first paint waits for the turn on screen. */
const REVEAL_WAIT_MS = 300;
/** Tools a turn's process line names before counting the rest. */
const TOOL_KINDS_SHOWN = 3;

/**
 * The conversation read turn by turn: what the operator asked, the agent loop
 * that answered (routine calls folded, anomalies open), the reply. It shares
 * the records and the call inspector with the trajectory; only the altitude
 * differs.
 */
export function RequestConversation({
  session,
  turns,
  childrenByRoot,
  services = NO_SERVICES,
  selectedRequestId,
  onSelectRequest,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
  rawSealing,
  onUnlockRaw,
  outlineOpen,
}: {
  session: RequestSession;
  turns: RequestRecord[];
  childrenByRoot: Record<string, RequestRecord[]>;
  services?: RequestServiceMap;
  selectedRequestId: string | null;
  onSelectRequest: (requestId: string) => void;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
  rawSealing: RawSealingState | null;
  onUnlockRaw?: () => void;
  outlineOpen: boolean;
}) {
  const t = useT();
  const sessionPending =
    session.status === "pending" || turns.some((r) => r.status === "pending");
  const nowMs = useLiveClock(sessionPending, 1000);
  const content = useConversationContent(
    `${session.id}:${rawSealing?.unlocked ? "unlocked" : "locked"}`,
  );
  const model = useMemo(
    () => conversationTurns(turns, childrenByRoot, content.parsed, nowMs),
    [turns, childrenByRoot, content.parsed, nowMs],
  );
  const latestTurnId = model[model.length - 1]?.id ?? null;

  // A session paints once the turn on screen has its words. Its user text,
  // reply and tool rows arrive a moment after the turns, and painting the
  // turns first made them pop in as the view opened. A slow read holds the
  // view back only briefly; once shown, the session stays shown.
  const latest = model[model.length - 1];
  const latestRead =
    latest === undefined ||
    [latest.calls[0]!, latest.calls[latest.calls.length - 1]!].every(
      (call) =>
        content.parsed.has(call.record.id) ||
        content.errors.has(call.record.id),
    );
  const [revealedId, setRevealedId] = useState<string | null>(null);
  const revealed = revealedId === session.id || latestRead;
  useEffect(() => {
    if (revealed) {
      setRevealedId(session.id);
      return;
    }
    const timer = window.setTimeout(
      () => setRevealedId(session.id),
      REVEAL_WAIT_MS,
    );
    return () => window.clearTimeout(timer);
  }, [revealed, session.id]);

  // Turns the operator opened or closed by hand; the rest follow the default:
  // the latest turn and any turn still running are open.
  const [turnOverrides, setTurnOverrides] = useState<
    ReadonlyMap<string, boolean>
  >(() => new Map());
  const [showAllTurns, setShowAllTurns] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [openRuns, setOpenRuns] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  // Turns that have come near the viewport. Once seen, a folded turn reads
  // every call, so its process line names all the tools it used.
  const [seenTurns, setSeenTurns] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [currentTurnId, setCurrentTurnId] = useState<string | null>(null);
  const [newTurns, setNewTurns] = useState(0);
  const [atBottom, setAtBottom] = useState(true);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const turnRefs = useRef(new Map<string, HTMLElement>());
  const atBottomRef = useRef(true);
  const turnCountRef = useRef(model.length);
  const seenObserverRef = useRef<IntersectionObserver | null>(null);

  useEffect(() => {
    setTurnOverrides(new Map());
    setShowAllTurns(new Set());
    setOpenRuns(new Set());
    setSeenTurns(new Set());
    setInspectorOpen(false);
    setNewTurns(0);
    turnCountRef.current = 0;
  }, [session.id]);

  const turnOpen = useCallback(
    (turn: ConversationTurn) =>
      turnOverrides.get(turn.id) ??
      (turn.id === latestTurnId || turn.stats.pending),
    [turnOverrides, latestTurnId],
  );

  useEffect(() => {
    const scroller = scrollerRef.current;
    if (!scroller || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        const seen = entries
          .filter((entry) => entry.isIntersecting)
          .map((entry) => (entry.target as HTMLElement).dataset.turnId ?? "")
          .filter(Boolean);
        if (seen.length === 0) return;
        setSeenTurns((current) =>
          seen.every((id) => current.has(id))
            ? current
            : new Set([...current, ...seen]),
        );
      },
      { root: scroller, rootMargin: "50% 0px" },
    );
    seenObserverRef.current = observer;
    return () => {
      observer.disconnect();
      seenObserverRef.current = null;
    };
  }, [session.id]);

  // Observing a turn twice is a no-op, so every new model just adds the turns
  // that have appeared since.
  useEffect(() => {
    const observer = seenObserverRef.current;
    if (!observer) return;
    for (const element of turnRefs.current.values()) observer.observe(element);
  }, [model]);

  // Open turns and folded ones in view read every call; the rest read just
  // enough for the user text and the reply, which a failed last call leaves
  // on an earlier one. The latest turn goes first because it is on screen.
  useEffect(() => {
    const wanted: RequestRecord[] = [];
    for (const turn of [...model].reverse()) {
      const records = turn.calls.map((call) => call.record);
      wanted.push(records[0]!, turn.reply.record, records[records.length - 1]!);
      if (turnOpen(turn) || seenTurns.has(turn.id)) wanted.push(...records);
    }
    content.request(wanted);
  }, [model, turnOpen, seenTurns, content.request]);

  // Follow the latest turn while the reader sits at the end, as a terminal
  // does; a reader who scrolled up keeps their place and is told what landed.
  // The outline marks the turn being read: the latest one while following,
  // otherwise the turn under the top of the viewport.
  const measure = useCallback(() => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    const bottom =
      scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <=
      FOLLOW_SLACK_PX;
    atBottomRef.current = bottom;
    setAtBottom(bottom);
    if (bottom) setNewTurns(0);
    if (bottom) {
      setCurrentTurnId(latestTurnId);
      return;
    }
    const anchor = scroller.scrollTop + 56;
    let current: string | null = null;
    for (const [id, element] of turnRefs.current) {
      if (element.offsetTop <= anchor) current = id;
    }
    setCurrentTurnId(current ?? model[0]?.id ?? null);
  }, [model, latestTurnId]);

  const scrollToEnd = useCallback(() => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    scroller.scrollTop = scroller.scrollHeight;
    atBottomRef.current = true;
    setAtBottom(true);
    setNewTurns(0);
  }, []);

  // A new session opens at its end. Only the session may do this: the model
  // is rebuilt every second while a call runs, and that must never scroll.
  useLayoutEffect(() => {
    scrollToEnd();
  }, [session.id, scrollToEnd]);

  // Content grows as calls land and bodies load. Keep the end in view for a
  // reader who was there; leave everyone else where they are and count what
  // arrived. The ref, not the state, decides: a scroll that moved the reader
  // away has already updated it, so no tick can pull them back.
  useLayoutEffect(() => {
    const previous = turnCountRef.current;
    turnCountRef.current = model.length;
    if (atBottomRef.current) {
      scrollToEnd();
    } else if (model.length > previous && previous > 0) {
      setNewTurns((count) => count + (model.length - previous));
    }
    measure();
  }, [model, scrollToEnd, measure]);

  const scrollToTurn = useCallback(
    (turnId: string) => {
      const scroller = scrollerRef.current;
      const element = turnRefs.current.get(turnId);
      if (!scroller || !element) return;
      scroller.scrollTop = element.offsetTop - 8;
      // Settle the reading position now rather than on the scroll event, so
      // a model tick in between cannot treat the jump as still following.
      measure();
    },
    [measure],
  );

  // The call inspector: docked in the context pane, or in its own window
  // where the host offers one, exactly as the trajectory does.
  const selectedCall = useMemo(() => {
    for (const turn of model) {
      for (const call of turn.calls) {
        if (call.record.id === selectedRequestId) return call;
        const child = call.children.find((c) => c.id === selectedRequestId);
        if (child) return { ...call, record: child };
      }
    }
    return null;
  }, [model, selectedRequestId]);
  const selection = useMemo(() => {
    if (!selectedCall) return null;
    const row = inspectorRow(selectedCall.record);
    if (!row) return null;
    return {
      row,
      record: selectedCall.record,
      service: requestServiceIdentity(selectedCall.record, services),
      services: routeServices(selectedCall.record, services),
    };
  }, [selectedCall, services]);
  const inspectorWindow = useDetachedInspector(selection);

  const selectCall = useCallback(
    (record: RequestRecord) => {
      onSelectRequest(record.id);
      if (inspectorWindow.enabled) {
        const row = inspectorRow(record);
        if (row) {
          inspectorWindow.show({
            row,
            record,
            service: requestServiceIdentity(record, services),
            services: routeServices(record, services),
          });
        }
        return;
      }
      setInspectorOpen(true);
    },
    [inspectorWindow, onSelectRequest, services],
  );

  const toggleTurn = useCallback(
    (turn: ConversationTurn) => {
      const open = turnOpen(turn);
      setTurnOverrides((current) => new Map(current).set(turn.id, !open));
    },
    [turnOpen],
  );
  const toggleShowAll = useCallback((turnId: string) => {
    setShowAllTurns((current) => {
      const next = new Set(current);
      if (next.has(turnId)) next.delete(turnId);
      else next.add(turnId);
      return next;
    });
  }, []);
  const toggleRun = useCallback((runId: string) => {
    setOpenRuns((current) => {
      const next = new Set(current);
      if (next.has(runId)) next.delete(runId);
      else next.add(runId);
      return next;
    });
  }, []);

  const read = model.flatMap((turn) => turn.calls).filter((c) => c.parsed);
  const anyLocked = read.some((call) => call.parsed?.locked);
  const noneCaptured =
    turns.length > 0 &&
    turns.every(
      (record) =>
        !record.audit.request_body_captured &&
        !record.audit.response_content_captured &&
        !record.audit.upstream_response_content_captured,
    );
  const failedTurns = model.filter((turn) => turn.stats.failed > 0).length;
  const showInspector =
    inspectorOpen && selection !== null && !inspectorWindow.enabled;
  const showPane = showInspector || outlineOpen;

  return (
    <div
      className="@container/conversation flex min-h-0 min-w-0 flex-1"
      data-testid="request-conversation"
    >
      <div className="relative flex min-h-0 min-w-0 flex-1">
        <div
          aria-busy={!revealed}
          aria-label={t("conversation.stream")}
          className={cn(
            "min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain pr-5 [scrollbar-gutter:stable]",
            !revealed && "invisible",
          )}
          data-testid="conversation-stream"
          onScroll={measure}
          ref={scrollerRef}
        >
          {anyLocked ? (
            <FormMessage
              className="mt-3 flex items-center gap-3 py-1.5"
              data-testid="conversation-locked"
            >
              <LockKeyhole aria-hidden="true" className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1">
                {t("conversation.lockedNotice")}
              </span>
              {onUnlockRaw ? (
                <Button
                  onClick={onUnlockRaw}
                  size="xs"
                  type="button"
                  variant="outline"
                >
                  {t("rawSealing.unlockTitle")}
                </Button>
              ) : null}
            </FormMessage>
          ) : noneCaptured ? (
            <FormMessage className="mt-3" data-testid="conversation-uncaptured">
              {t("conversation.uncapturedNotice")}
            </FormMessage>
          ) : null}
          <ol>
            {model.map((turn) => (
              <TurnView
                call={selectedCall}
                copyFeedback={copyFeedback}
                inspectorOpen={showInspector}
                key={turn.id}
                onSelectCall={selectCall}
                onToggle={toggleTurn}
                onToggleRun={toggleRun}
                onToggleShowAll={toggleShowAll}
                open={turnOpen(turn)}
                openRuns={openRuns}
                reading={content.reading}
                refFor={(element) => {
                  if (element) turnRefs.current.set(turn.id, element);
                  else turnRefs.current.delete(turn.id);
                }}
                services={services}
                session={session}
                showAll={showAllTurns.has(turn.id)}
                turn={turn}
              />
            ))}
          </ol>
        </div>
        {!atBottom && (newTurns > 0 || sessionPending) ? (
          <Button
            className="absolute right-8 bottom-3 shadow-md"
            data-testid="conversation-latest"
            onClick={scrollToEnd}
            size="sm"
            type="button"
          >
            <ArrowDown aria-hidden="true" />
            {newTurns > 0
              ? t("conversation.newTurns", { count: newTurns })
              : t("conversation.latest")}
          </Button>
        ) : null}
      </div>
      {showPane ? (
        <aside
          className={cn(
            "flex min-h-0 shrink-0 flex-col border-l",
            showInspector
              ? "w-[min(100%,26rem)]"
              : "hidden w-60 @min-[880px]/conversation:flex",
          )}
          data-testid="conversation-pane"
        >
          {showInspector && selection ? (
            <TrajectoryInspector
              auditContent={
                auditContent?.request_id === selection.record.id
                  ? auditContent
                  : null
              }
              auditError={
                selectedRequestId === selection.record.id ? auditError : null
              }
              auditLoading={
                selectedRequestId === selection.record.id &&
                (auditLoading ||
                  auditContent?.request_id !== selection.record.id)
              }
              copyFeedback={copyFeedback}
              onClose={() => setInspectorOpen(false)}
              onUnlockRaw={onUnlockRaw}
              record={selection.record}
              row={selection.row}
              service={selection.service}
              services={selection.services}
            />
          ) : (
            <Outline
              currentTurnId={currentTurnId}
              failedTurns={failedTurns}
              onSelect={scrollToTurn}
              turns={model}
            />
          )}
        </aside>
      ) : null}
    </div>
  );
}

/** The phase the inspector opens on: where it failed, else the result. */
function inspectorRow(record: RequestRecord): TrajectoryRow | null {
  const rows = inspectorChainRows(record);
  if (rows.length === 0) return null;
  const failed = rows.find(
    (row) => row.tone === "failed" || row.tone === "blocked",
  );
  return (
    failed ??
    rows.find((row) => row.chip === "RESULT") ??
    rows[rows.length - 1] ??
    null
  );
}

function timeLabel(iso: string | null): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toLocaleTimeString(i18n.language, {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** The most used tools by name, then how many other kinds there were. */
function toolsLabel(tools: Array<[string, number]>): string {
  const shown = toolCountLabel(tools.slice(0, TOOL_KINDS_SHOWN));
  const rest = tools.length - TOOL_KINDS_SHOWN;
  return rest > 0
    ? `${shown} · ${i18n.t("conversation.moreTools", { count: rest })}`
    : shown;
}

function tokenLabel(tokens: TokenUsage | null): string {
  if (!tokens) return "";
  return `${formatCompactNumber(tokens.input)} / ${formatCompactNumber(tokens.output)}`;
}

function dotTone(tone: TrajectoryTone): StatusTone {
  switch (tone) {
    case "ok":
      return "positive";
    case "failed":
      return "negative";
    case "blocked":
      return "blocked";
    case "cancelled":
      return "neutral";
    case "pending":
      return "pending";
  }
}

/**
 * Items marked `data-wide` only show once the conversation is wide enough;
 * their separator hides with them.
 */
function Dots({ children }: { children: ReactNode[] }) {
  const items = children.filter(
    (child) => child !== null && child !== undefined && child !== false,
  );
  return (
    <>
      {items.map((child, index) => (
        <span
          className={cn(
            "inline-flex min-w-0 items-center",
            isValidElement<{ "data-wide"?: boolean }>(child) &&
              child.props["data-wide"] &&
              "hidden @min-[720px]/conversation:inline-flex",
          )}
          key={index}
        >
          {index > 0 ? (
            <span aria-hidden="true" className="mx-1.5 text-border">
              ·
            </span>
          ) : null}
          {child}
        </span>
      ))}
    </>
  );
}

const TurnView = memo(function TurnView({
  turn,
  session,
  services,
  open,
  showAll,
  openRuns,
  reading,
  call,
  inspectorOpen,
  copyFeedback,
  refFor,
  onToggle,
  onToggleShowAll,
  onToggleRun,
  onSelectCall,
}: {
  turn: ConversationTurn;
  session: RequestSession;
  services: RequestServiceMap;
  open: boolean;
  showAll: boolean;
  openRuns: ReadonlySet<string>;
  reading: ReadonlySet<string>;
  call: ConversationCall | null;
  inspectorOpen: boolean;
  copyFeedback: CopyFeedback;
  refFor: (element: HTMLElement | null) => void;
  onToggle: (turn: ConversationTurn) => void;
  onToggleShowAll: (turnId: string) => void;
  onToggleRun: (runId: string) => void;
  onSelectCall: (record: RequestRecord) => void;
}) {
  const t = useT();
  const first = turn.calls[0]!;
  const reply = turn.reply;
  const selectedId = inspectorOpen ? call?.record.id : undefined;
  const unread = turn.calls.filter((c) => !c.parsed).length;
  const readingHere = turn.calls.some((c) => reading.has(c.record.id));
  const segments: ConversationSegment[] = showAll
    ? turn.calls.map((c) => ({ kind: "call", call: c }))
    : turn.segments;
  const userCopyKey = `conversation-user:${turn.id}`;
  // Every call has been read, so the tools can be named rather than counted.
  const toolsNamed =
    !turn.stats.toolCallsPartial && turn.stats.tools.length > 0;
  // A folded turn keeps one line of its reply under the process line.
  const excerpt = !open && reply.text !== null ? replyExcerpt(reply.text) : "";
  // The reply hangs beneath the call that wrote it, so folding that row folds
  // the text; folding the process folds every row, the reply's included.
  const shownSegments = open ? segments : [];
  return (
    <li
      className="min-w-0 border-b py-5 last:border-b-0"
      data-testid="conversation-turn"
      data-turn-id={turn.id}
      ref={refFor}
    >
      <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
        <ClientTypeIcon
          clientType={first.record.client_type ?? session.client_type}
          decorative
          size={14}
        />
        <strong className="text-sm font-semibold text-foreground">
          {t("conversation.you")}
        </strong>
        <time className="tabular-nums" dateTime={turn.startedAt}>
          {timeLabel(turn.startedAt)}
        </time>
        {turn.user.text ? (
          <Button
            className="ml-auto h-6 px-1.5 text-muted-foreground"
            onClick={() => copyFeedback.copy(userCopyKey, turn.user.text)}
            size="xs"
            type="button"
            variant="ghost"
          >
            <Copy aria-hidden="true" />
            {copyButtonLabel(copyFeedback, userCopyKey, t("common.copy"))}
          </Button>
        ) : null}
      </div>
      <UserBlock turn={turn} />

      <div className="my-3.5" data-testid="conversation-process">
        <div className="flex min-h-7 items-center gap-2 text-xs text-muted-foreground">
          <Button
            aria-expanded={open}
            className="h-auto min-h-7 min-w-0 flex-1 flex-col items-stretch gap-0.5 px-2 py-1 font-normal text-muted-foreground hover:bg-transparent hover:text-foreground"
            onClick={() => onToggle(turn)}
            size="sm"
            type="button"
            variant="ghost"
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <ChevronRight
                aria-hidden="true"
                className={cn(
                  "size-3 transition-transform",
                  open && "rotate-90",
                )}
              />
              <Dots>
                {[
                  <span className="tabular-nums" key="calls">
                    {t("conversation.calls", { count: turn.stats.calls })}
                  </span>,
                  // A turn that called no tools leaves the count out.
                  !turn.stats.toolCalls ? null : toolsNamed ? (
                    <span key="tools" title={toolCountLabel(turn.stats.tools)}>
                      {toolsLabel(turn.stats.tools)}
                    </span>
                  ) : (
                    <span className="tabular-nums" key="tools">
                      {t("conversation.toolCalls", {
                        count: turn.stats.toolCalls,
                      })}
                    </span>
                  ),
                  turn.stats.failed > 0 ? (
                    <span
                      className="tabular-nums text-danger-foreground"
                      key="failed"
                    >
                      {t("conversation.failures", { count: turn.stats.failed })}
                    </span>
                  ) : null,
                  turn.stats.cancelled > 0 ? (
                    <span
                      className="tabular-nums text-warning-foreground"
                      data-testid="conversation-cancelled"
                      key="cancelled"
                    >
                      {cancelledLabel(turn.stats)}
                    </span>
                  ) : null,
                  <span className="tabular-nums" key="duration">
                    {formatDuration(turn.stats.durationMs)}
                  </span>,
                  turn.stats.tokens ? (
                    <span
                      className="tabular-nums"
                      data-wide
                      key="tokens"
                      title={t("conversation.tokens", {
                        input: formatCompactNumber(turn.stats.tokens.input),
                        output: formatCompactNumber(turn.stats.tokens.output),
                      })}
                    >
                      {tokenLabel(turn.stats.tokens)}
                    </span>
                  ) : null,
                  turn.stats.ttftMs !== null ? (
                    <span
                      className="tabular-nums"
                      data-testid="conversation-ttft"
                      data-wide
                      key="ttft"
                      title={t("records.averageTTFT")}
                    >
                      {`${t("records.ttft")} ${formatDuration(turn.stats.ttftMs)}`}
                    </span>
                  ) : null,
                  turn.stats.outputTokensPerSecond !== null ? (
                    <span
                      className="tabular-nums"
                      data-testid="conversation-output-speed"
                      data-wide
                      key="speed"
                      title={t("records.outputSpeed")}
                    >
                      {`${turn.stats.outputTokensPerSecond.toFixed(1)} tok/s`}
                    </span>
                  ) : null,
                  open && readingHere && unread > 0 ? (
                    <span key="reading" role="status">
                      {t("conversation.reading", {
                        done: turn.calls.length - unread,
                        total: turn.calls.length,
                      })}
                    </span>
                  ) : null,
                ]}
              </Dots>
            </span>
            {excerpt ? (
              <span
                className="mt-1 flex min-w-0 flex-col items-start gap-1 pl-[18px] text-left"
                data-testid="conversation-excerpt"
              >
                <ModelLabel
                  className="font-medium text-text-secondary"
                  model={reply.record.requested_model}
                  reasoningEffort={reply.record.reasoning_effort}
                  redirectedTo={reply.record.model_redirect?.to}
                />
                <FadeLine className="w-full" text={excerpt} />
              </span>
            ) : null}
          </Button>
          {open && turn.segments.some((segment) => segment.kind === "run") ? (
            <Button
              className="h-6 px-1.5"
              onClick={() => onToggleShowAll(turn.id)}
              size="xs"
              type="button"
              variant="link"
            >
              {showAll
                ? t("conversation.showAnomalies")
                : t("conversation.showAll", { count: turn.calls.length })}
            </Button>
          ) : null}
        </div>
        {shownSegments.length > 0 ? (
          <ol
            aria-label={t("conversation.callList")}
            className="ml-1 border-l pl-3.5"
          >
            {shownSegments.map((segment) =>
              segment.kind === "run" ? (
                <RunRow
                  key={`run:${segment.calls[0]!.record.id}`}
                  onSelectCall={onSelectCall}
                  onToggle={onToggleRun}
                  open={openRuns.has(segment.calls[0]!.record.id)}
                  segment={segment}
                  selectedId={selectedId}
                  services={services}
                />
              ) : (
                <CallRow
                  call={segment.call}
                  copyFeedback={copyFeedback}
                  key={segment.call.record.id}
                  onSelect={onSelectCall}
                  reply={segment.call.reply ? reply : undefined}
                  selected={selectedId === segment.call.record.id}
                  services={services}
                />
              ),
            )}
          </ol>
        ) : null}
      </div>
    </li>
  );
});

/** The model's thinking, folded to one line until the reader wants it. */
function ReasoningRow({
  texts,
  durationMs,
}: {
  texts: string[];
  durationMs: number | null;
}) {
  const t = useT();
  return (
    <TraceCollapsible data-testid="conversation-reasoning">
      <TraceCollapsibleTrigger className="h-7">
        <TraceLine className="text-xs">
          <TraceTitle>
            {durationMs === null
              ? t("conversation.reasoning")
              : t("conversation.reasoningTimed", {
                  duration: formatDuration(durationMs),
                })}
          </TraceTitle>
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent className="mt-0.5 mb-2 max-w-[76ch] space-y-2 pl-3 text-xs italic whitespace-pre-wrap">
        {texts.map((text, index) => (
          <p key={index}>{text}</p>
        ))}
      </TraceCollapsibleContent>
    </TraceCollapsible>
  );
}

/** The model, provider and time that answered: one line above a reply. */
function ReplyIdentity({
  record,
  service,
  at,
}: {
  record: RequestRecord;
  service: ReturnType<typeof requestServiceIdentity>;
  at: string | null;
}) {
  const t = useT();
  return (
    <>
      <ModelBrandIcon
        className="text-muted-foreground"
        model={record.requested_model}
        size={14}
      />
      <strong className="truncate text-sm font-semibold text-foreground">
        {record.model_redirect?.to ??
          record.requested_model ??
          t("records.unspecifiedModel")}
      </strong>
      <span className="truncate">{service.name}</span>
      {at ? (
        <time className="tabular-nums" dateTime={at}>
          {timeLabel(at)}
        </time>
      ) : null}
    </>
  );
}

function UserBlock({ turn }: { turn: ConversationTurn }) {
  const t = useT();
  const { user } = turn;
  const spans = useMemo(
    () =>
      splitPrivacyHighlights(user.text).flatMap(
        (span): Array<PrivacyHighlightSpan & FilePathSpan> =>
          span.kind ? [span] : splitFilePaths(span.text),
      ),
    [user.text],
  );
  const hits = spans.filter((span) => span.kind).length;
  const pasted = user.wrappers.filter((w) => w.kind === "pasted");
  const system = user.wrappers.filter((w) => w.kind === "system").length;
  const commands = user.wrappers.filter((w) => w.kind === "command");
  const restored = turn.calls[0]?.record.privacy_restore;
  return (
    <div
      className="max-w-[72ch] rounded-lg bg-muted px-3.5 py-2.5 text-sm leading-relaxed"
      data-testid="conversation-user"
    >
      {user.text ? (
        <p className="whitespace-pre-wrap [overflow-wrap:anywhere]">
          {spans.map((span, index) =>
            span.kind ? (
              <mark
                className="rounded-sm bg-transparent font-mono text-micro text-accent-foreground underline decoration-dotted underline-offset-2"
                data-privacy-kind={span.kind}
                key={index}
              >
                {span.text}
              </mark>
            ) : span.file ? (
              <Badge
                className="mx-0.5 max-w-full bg-background align-baseline font-mono font-normal text-text-secondary"
                data-testid="conversation-file"
                key={index}
                title={span.text}
                variant="outline"
              >
                <span className="truncate">{span.file}</span>
              </Badge>
            ) : (
              <span key={index}>{span.text}</span>
            ),
          )}
        </p>
      ) : commands.length === 0 && pasted.length === 0 ? (
        <p className="text-muted-foreground">{"\u2014"}</p>
      ) : null}
      {pasted.length > 0 || system > 0 || commands.length > 0 || hits > 0 ? (
        <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {commands.map((command, index) => (
            <span key={`command:${index}`}>
              {t("conversation.command", { name: command.name ?? "" })}
            </span>
          ))}
          {pasted.map((wrapper, index) => (
            <span key={`pasted:${index}`}>
              {t("conversation.pasted", {
                count: wrapper.chars.toLocaleString(),
              })}
            </span>
          ))}
          {system > 0 ? (
            <span>{t("conversation.systemNotes", { count: system })}</span>
          ) : null}
          {hits > 0 ? (
            <span data-testid="conversation-privacy">
              {t("conversation.privacyHits", { count: hits })}
              {restored && restored.restored_count > 0
                ? ` · ${t("conversation.privacyRestored")}`
                : ""}
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

const ROW_CLASS =
  "grid h-7 min-w-0 flex-1 grid-cols-[8px_minmax(0,1fr)_auto_auto_12px] items-center gap-x-2.5 rounded-sm px-1.5 text-left text-xs font-normal text-foreground hover:bg-muted hover:text-foreground";

const RunRow = memo(function RunRow({
  segment,
  open,
  selectedId,
  services,
  onToggle,
  onSelectCall,
}: {
  segment: Extract<ConversationSegment, { kind: "run" }>;
  open: boolean;
  selectedId: string | undefined;
  services: RequestServiceMap;
  onToggle: (runId: string) => void;
  onSelectCall: (record: RequestRecord) => void;
}) {
  const t = useT();
  const first = segment.calls[0]!;
  const last = segment.calls[segment.calls.length - 1]!;
  return (
    <li data-testid="conversation-run">
      <div className="sticky top-0 z-10 flex items-center gap-1 bg-background">
        <Button
          aria-expanded={open}
          className={cn(ROW_CLASS, "text-muted-foreground")}
          onClick={() => onToggle(first.record.id)}
          size="sm"
          type="button"
          variant="ghost"
        >
          <span
            aria-hidden="true"
            className="size-2 rounded-full border border-input"
          />
          <span className="flex min-w-0 items-baseline gap-2 whitespace-nowrap">
            <span className="font-medium text-text-secondary">
              {t("conversation.callRange", {
                from: first.index,
                to: last.index,
              })}
            </span>
            {segment.toolCounts.length > 0 ? (
              <span className="truncate font-mono text-micro">
                {toolCountLabel(segment.toolCounts)}
              </span>
            ) : segment.toolTotal === 0 ? null : (
              <span className="truncate text-micro">
                {segment.toolTotal === null
                  ? ""
                  : t("conversation.toolsCount", { count: segment.toolTotal })}
              </span>
            )}
          </span>
          <span className="tabular-nums text-micro text-muted-foreground">
            {tokenLabel(segment.tokens)}
          </span>
          <span className="tabular-nums text-micro text-muted-foreground">
            {formatDuration(segment.durationMs)}
          </span>
          <ChevronRight
            aria-hidden="true"
            className={cn(
              "size-3 text-muted-foreground/60 transition-transform",
              open && "rotate-90",
            )}
          />
        </Button>
        <span aria-hidden="true" className="size-6 shrink-0" />
      </div>
      {open ? (
        <ol className="ml-1 border-l pl-3.5">
          {segment.calls.map((call) => (
            <CallRow
              call={call}
              key={call.record.id}
              nested
              onSelect={onSelectCall}
              selected={selectedId === call.record.id}
              services={services}
            />
          ))}
        </ol>
      ) : null}
    </li>
  );
});

const CallRow = memo(function CallRow({
  call,
  selected,
  services,
  nested = false,
  reply,
  copyFeedback,
  onSelect,
}: {
  call: ConversationCall;
  selected: boolean;
  services: RequestServiceMap;
  /** Inside an open run, whose heading already holds the top. */
  nested?: boolean;
  /** The turn's reply when this call wrote it. */
  reply?: ConversationTurn["reply"];
  copyFeedback?: CopyFeedback;
  onSelect: (record: RequestRecord) => void;
}) {
  const t = useT();
  const service = requestServiceIdentity(call.record, services);
  const replyText = reply?.text ?? null;
  const details =
    replyText !== null ||
    call.narration.length > 0 ||
    call.reasoning.length > 0 ||
    call.toolCalls.length > 0 ||
    (call.gapMs !== null && call.gapMs >= 500);
  const [open, setOpen] = useState(true);
  const replyCopyKey = `conversation-reply:${call.record.id}`;
  return (
    <li data-testid="conversation-call" data-tone={call.tone}>
      {/* The heading stays put while its details scroll, so a long reply or
          tool output folds from where it is being read. */}
      <div
        className={cn(
          "sticky z-10 flex items-center gap-1 bg-background",
          nested ? "top-7" : "top-0",
          selected &&
            "before:absolute before:inset-y-1 before:-left-[15px] before:w-0.5 before:rounded-full before:bg-primary",
        )}
      >
        <Button
          aria-current={selected ? "true" : undefined}
          aria-expanded={details ? open : undefined}
          className={cn(ROW_CLASS, selected && "bg-accent hover:bg-accent")}
          onClick={() =>
            details ? setOpen((current) => !current) : onSelect(call.record)
          }
          size="sm"
          type="button"
          variant="ghost"
        >
          <StatusDot className="size-2" tone={dotTone(call.tone)} />
          <span className="flex min-w-0 items-baseline gap-2 whitespace-nowrap">
            {/* A call still in flight shimmers like a running trace step. */}
            <span
              className={cn(
                "font-medium",
                call.tone === "pending" && "shimmer motion-reduce:shimmer-none",
              )}
            >
              {t("conversation.callNumber", { index: call.index })}
            </span>
            <span className="text-muted-foreground">{service.name}</span>
            {call.reason ? (
              <span
                className={cn(
                  "truncate",
                  call.tone === "cancelled"
                    ? "text-warning-foreground"
                    : "text-danger-foreground",
                )}
                title={call.reason}
              >
                {call.reason}
              </span>
            ) : null}
          </span>
          <span className="tabular-nums text-micro text-muted-foreground">
            {tokenLabel(call.tokens)}
          </span>
          <span
            className={cn(
              "tabular-nums text-micro text-muted-foreground",
              call.tone === "pending" && "text-accent-foreground",
            )}
          >
            {call.durationMs !== null ? formatDuration(call.durationMs) : ""}
          </span>
          {details ? (
            <ChevronRight
              aria-hidden="true"
              className={cn(
                "size-3 text-muted-foreground/60 transition-transform",
                open && "rotate-90",
              )}
            />
          ) : (
            <span aria-hidden="true" className="size-3" />
          )}
        </Button>
        <IconButton
          className={cn(
            "size-6 shrink-0 text-muted-foreground/60 hover:text-foreground",
            selected && "text-primary hover:text-primary",
          )}
          data-request-id={call.record.id}
          data-testid="conversation-inspect"
          label={t("conversation.viewCall")}
          onClick={() => onSelect(call.record)}
          size="icon-xs"
          type="button"
        >
          <ArrowUpRight aria-hidden="true" className="size-3" />
        </IconButton>
      </div>
      {details && open ? (
        <>
          {call.narration.map((text, index) => (
            <Narration key={index} text={text} />
          ))}
          {call.reasoning.length > 0 || call.toolCalls.length > 0 ? (
            <Trace className="ml-[18px] w-auto gap-0">
              {call.reasoning.length > 0 ? (
                <ReasoningRow
                  durationMs={call.reasoningMs}
                  texts={call.reasoning}
                />
              ) : null}
              {call.toolCalls.map((tool, index) => (
                <ToolRow key={`${tool.id ?? index}`} tool={tool} />
              ))}
            </Trace>
          ) : null}
          {call.gapMs !== null && call.gapMs >= 500 ? (
            <p className="mb-1.5 ml-[18px] text-micro text-muted-foreground">
              {t("conversation.gap", { duration: formatDuration(call.gapMs) })}
            </p>
          ) : null}
          {replyText !== null && reply ? (
            <div className="mt-1.5 mb-2 ml-[18px]">
              <div className="mb-1.5 flex items-center gap-2 text-xs text-muted-foreground">
                <ReplyIdentity
                  at={reply.at}
                  record={reply.record}
                  service={requestServiceIdentity(reply.record, services)}
                />
                {copyFeedback ? (
                  <Button
                    className="ml-auto h-6 px-1.5 text-muted-foreground"
                    onClick={() => copyFeedback.copy(replyCopyKey, replyText)}
                    size="xs"
                    type="button"
                    variant="ghost"
                  >
                    <Copy aria-hidden="true" />
                    {copyButtonLabel(
                      copyFeedback,
                      replyCopyKey,
                      t("common.copy"),
                    )}
                  </Button>
                ) : null}
              </div>
              <div data-testid="conversation-reply">
                <MarkdownContent className="max-w-[76ch]" content={replyText} />
              </div>
            </div>
          ) : null}
        </>
      ) : null}
    </li>
  );
});

function Narration({ text }: { text: string }) {
  const [full, setFull] = useState(false);
  return (
    <Button
      className="my-1 ml-[18px] block h-auto max-w-[80ch] rounded-sm px-0 py-0 text-left text-xs font-normal leading-relaxed whitespace-normal text-muted-foreground hover:bg-transparent hover:text-foreground"
      data-testid="conversation-narration"
      onClick={() => setFull((current) => !current)}
      type="button"
      variant="ghost"
    >
      <span className={cn("block", !full && "line-clamp-2")}>“{text}”</span>
    </Button>
  );
}

function prettyJson(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/** How tool text is highlighted: JSON, terminal colors, or plain text. */
function toolLanguage(text: string): string {
  try {
    JSON.parse(text);
    return "json";
  } catch {
    return text.includes("\u001b[") ? "ansi" : "";
  }
}

/**
 * A tool's arguments or output, numbered and copyable. Its own actions share
 * the header with the copy button, so a block is no taller than the text.
 */
function ToolCode({
  label,
  text,
  failed = false,
  actions,
  ...props
}: ComponentProps<typeof CodeBlockContent> & {
  label: string;
  text: string;
  failed?: boolean;
  actions?: ReactNode;
}) {
  const language = useMemo(() => toolLanguage(text), [text]);
  const lines = useHighlightedCode(text, language);
  return (
    <CodeBlock
      className="data-[size=sm]:text-micro"
      size="sm"
      variant={failed ? "destructive" : "default"}
    >
      <CodeBlockHeader>
        <CodeBlockTitle>{label}</CodeBlockTitle>
        <CodeBlockActions>
          {actions}
          <CodeBlockCopyButton value={text} />
        </CodeBlockActions>
      </CodeBlockHeader>
      <CodeBlockContent lineNumbers wrap {...props}>
        {lines.map((line, index) => (
          <CodeBlockLine key={index}>{line}</CodeBlockLine>
        ))}
      </CodeBlockContent>
    </CodeBlock>
  );
}

function ToolRow({ tool }: { tool: ConversationToolCall }) {
  const t = useT();
  const [fullResult, setFullResult] = useState(false);
  const resultId = useId();
  const args = useMemo(() => prettyJson(tool.args), [tool.args]);
  const clipped =
    tool.args.length >= TOOL_TEXT_LIMIT ||
    (tool.result?.length ?? 0) >= TOOL_TEXT_LIMIT;
  return (
    <TraceCollapsible
      data-testid="conversation-tool"
      defaultOpen={tool.isError}
    >
      <TraceCollapsibleTrigger className="h-7">
        <TraceLine
          className="text-micro text-foreground"
          status={tool.isError ? "error" : undefined}
        >
          <TraceTitle className="shrink-0 font-mono font-semibold">
            {tool.name}
          </TraceTitle>
          <span className="min-w-0 truncate font-mono text-muted-foreground">
            {tool.args.replace(/\s+/g, " ")}
          </span>
          {/* A missing result is often just a body that was not captured or
              is locked, so only a failure earns a label. */}
          {tool.isError ? (
            <TraceDescription className="font-medium text-danger-foreground">
              {t("conversation.toolFailed")}
            </TraceDescription>
          ) : null}
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent className="mt-0.5 mb-2 space-y-1.5 pl-3">
        {tool.args ? (
          <ToolCode
            className="max-h-48"
            label={t("conversation.toolArgs")}
            text={args}
          />
        ) : null}
        {tool.result !== null ? (
          <ToolCode
            actions={
              tool.result.split("\n").length > 6 || tool.result.length > 400 ? (
                <Button
                  aria-controls={resultId}
                  aria-expanded={fullResult}
                  onClick={() => setFullResult((current) => !current)}
                  size="xs"
                  type="button"
                  variant="ghost"
                >
                  {fullResult
                    ? t("conversation.collapse")
                    : t("conversation.expand")}
                </Button>
              ) : null
            }
            className={cn(!fullResult && "max-h-28")}
            failed={tool.isError}
            id={resultId}
            label={t("conversation.toolOutput")}
            text={tool.result}
          />
        ) : null}
        {clipped ? (
          <p className="text-micro text-muted-foreground">
            {t("conversation.toolTextClipped", { limit: "16 KiB" })}
          </p>
        ) : null}
      </TraceCollapsibleContent>
    </TraceCollapsible>
  );
}

function Outline({
  turns,
  currentTurnId,
  failedTurns,
  onSelect,
}: {
  turns: ConversationTurn[];
  currentTurnId: string | null;
  failedTurns: number;
  onSelect: (turnId: string) => void;
}) {
  const t = useT();
  return (
    <nav
      aria-label={t("conversation.outline")}
      className="flex min-h-0 flex-1 flex-col"
      data-testid="conversation-outline"
    >
      <div className="flex shrink-0 items-center justify-between px-3 py-2 text-micro text-muted-foreground">
        <span>{t("conversation.outlineTitle", { count: turns.length })}</span>
        {failedTurns > 0 ? (
          <span className="text-danger-foreground">
            {t("conversation.failures", { count: failedTurns })}
          </span>
        ) : null}
      </div>
      <ol className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        {turns.map((turn) => {
          const current = turn.id === currentTurnId;
          return (
            <li key={turn.id}>
              <Button
                aria-current={current ? "true" : undefined}
                className={cn(
                  "relative block h-auto w-full rounded-none px-3 py-2 text-left text-xs font-normal whitespace-normal hover:bg-muted/60 hover:text-foreground",
                  current &&
                    "bg-accent before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary hover:bg-accent",
                )}
                onClick={() => onSelect(turn.id)}
                type="button"
                variant="ghost"
              >
                <span className="flex justify-between text-micro text-muted-foreground">
                  <span>
                    {turn.turnIndex === null
                      ? t("conversation.turnUnnumbered")
                      : t("conversation.turn", { index: turn.turnIndex })}
                  </span>
                  <time className="tabular-nums" dateTime={turn.startedAt}>
                    {timeLabel(turn.startedAt)}
                  </time>
                </span>
                <span
                  className={cn(
                    "my-0.5 line-clamp-2 text-text-secondary",
                    current && "text-foreground",
                  )}
                >
                  {compactFilePaths(turn.user.text) || "—"}
                </span>
                <span className="text-micro tabular-nums text-muted-foreground">
                  {t("conversation.calls", { count: turn.stats.calls })}
                  {` · ${formatDuration(turn.stats.durationMs)}`}
                  {turn.stats.pending ? ` · ${i18n.t("status.pending")}` : ""}
                  {turn.stats.failed > 0 ? (
                    <span className="text-danger-foreground">
                      {` · ${t("conversation.failures", { count: turn.stats.failed })}`}
                    </span>
                  ) : null}
                  {turn.stats.cancelled > 0 ? (
                    <span className="text-warning-foreground">
                      {` · ${cancelledLabel(turn.stats)}`}
                    </span>
                  ) : null}
                </span>
              </Button>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
