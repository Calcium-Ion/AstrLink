import { useCallback, useEffect, useRef, useState } from "react";
import type { UnlistenFn } from "@tauri-apps/api/event";

import { FoldHorizontal, Pin, UnfoldHorizontal } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

import { getRawSealingStatus, getRequestAuditContent } from "./bridge";
import { useCopyFeedback } from "./copy-feedback";
import { i18n } from "./i18n";
import { notify } from "./notify";
import { rawPasswordUnset, type RawSealingState } from "./raw-sealing-model";
import { RawSealingDialogs, type RawDialog } from "./RawSealingControls";
import {
  holdsLockedPart,
  holdsRawPart,
  type AuditContent,
  type RequestRecord,
} from "./request-record-model";
import { TrajectoryInspector } from "./TrajectoryInspector";
import { TrajectoryInspectorTour } from "./TrajectoryInspectorTour";
import {
  listenInspectorSelection,
  setTrajectoryInspectorPinned,
  setTrajectoryInspectorWide,
  trajectoryInspectorState,
  type TrajectoryInspectorSelection,
} from "./trajectory-inspector-window";
import { useRawUnlockWatch } from "./use-raw-unlock-watch";
import { WindowChromeAccessory } from "./WindowChrome";

/**
 * The whole app in a detached inspector window: one selected call, driven by
 * the main window over the event channel. The clicked chip is only a scroll
 * target; the pane always stacks that request's whole chain.
 *
 * Pinning floats the window above the others and freezes the call. The pin
 * sits in the title bar, where window-level controls belong, so it reads as
 * "keep this window on top" rather than as another control over the content.
 * The host stops routing selections here, and this side ignores any that
 * still arrive, so the two cannot disagree about what a pinned window shows.
 *
 * Audit content is decrypted here rather than forwarded, so captured bodies
 * never cross the channel and the main window's cache stays the main window's.
 * The unlock is shared with the main window: this one can open it for the
 * parts on screen and watches it, so raw parts go when the unlock ends —
 * pinned or not — and locked parts fill in after one, wherever it opened.
 *
 * The window opens narrow beside the list; the widen toggle beside the pin
 * stretches it for long bodies and puts it back, and ⌘F finds in the body.
 * A first-visit tour walks through those controls in place.
 */
export function TrajectoryInspectorWindow() {
  const t = i18n.t.bind(i18n);
  const [selection, setSelection] =
    useState<TrajectoryInspectorSelection | null>(null);
  const [pinned, setPinned] = useState(false);
  const [wide, setWide] = useState(false);
  // Bumped on every width change, so each one replays the edge flash.
  const [stretch, setStretch] = useState(0);
  const pinnedRef = useRef(false);
  const copyFeedback = useCopyFeedback();

  pinnedRef.current = pinned;

  useEffect(() => {
    let active = true;
    let stop: UnlistenFn | null = null;
    void listenInspectorSelection((next) => {
      if (active && !pinnedRef.current) setSelection(next);
    })
      .then((unlisten) => {
        if (!active) {
          unlisten();
          return;
        }
        stop = unlisten;
        // Pulled rather than waited for, so there is no window in which the
        // host has already sent the phase and nobody was listening. This is
        // also what restores a pinned window after the dev host reloads it.
        return trajectoryInspectorState().then((state) => {
          if (!active) return;
          setPinned(state.pinned);
          setWide(state.wide ?? false);
          if (state.selection) setSelection(state.selection);
        });
      })
      .catch((error: unknown) => {
        console.error("AstrLink inspector window cannot subscribe", error);
      });
    return () => {
      active = false;
      stop?.();
    };
  }, []);

  const togglePin = useCallback((next: boolean) => {
    // Optimistic, then corrected by whatever the host settled on: the button
    // has to answer the click even though the window level changes in Rust.
    setPinned(next);
    void setTrajectoryInspectorPinned(next)
      .then(setPinned)
      .catch((error: unknown) => {
        setPinned(!next);
        notify.error(i18n.t("trajectory.pinFailed"));
        console.error("AstrLink inspector window cannot change its pin", error);
      });
  }, []);

  const toggleWide = useCallback((next: boolean) => {
    setWide(next);
    setStretch((count) => count + 1);
    void setTrajectoryInspectorWide(next)
      .then(setWide)
      .catch((error: unknown) => {
        setWide(!next);
        notify.error(i18n.t("trajectory.widenFailed"));
        console.error(
          "AstrLink inspector window cannot change its width",
          error,
        );
      });
  }, []);

  const [rawCheck, setRawCheck] = useState(0);
  const audit = useRequestAudit(
    selection?.record.id ?? null,
    selection?.record.status ?? null,
    auditCaptureKey(selection?.record ?? null),
    rawCheck,
  );
  const refetchAudit = useCallback(() => setRawCheck((count) => count + 1), []);
  useRawUnlockWatch(
    selection !== null && !audit.loading,
    {
      holdsRaw: () => audit.content !== null && holdsRawPart(audit.content),
      holdsLocked: () =>
        audit.content !== null && holdsLockedPart(audit.content),
    },
    refetchAudit,
  );

  const [rawSealing, setRawSealing] = useState<RawSealingState | null>(null);
  const [rawDialog, setRawDialog] = useState<RawDialog | null>(null);
  const unlockRaw = useCallback(() => {
    // Read fresh: the unlock may have opened in the main window meanwhile.
    getRawSealingStatus().then(
      (current) => {
        setRawSealing(current);
        if (current.unlocked) {
          refetchAudit();
          return;
        }
        setRawDialog({ kind: rawPasswordUnset(current) ? "set" : "unlock" });
      },
      () => notify.error(i18n.t("records.unlockSealingUnknown")),
    );
  }, [refetchAudit]);

  return (
    <main className="flex h-dvh min-h-0 flex-col overflow-hidden pt-[var(--window-chrome-height)]">
      {selection ? (
        <WindowChromeAccessory>
          <TrajectoryInspectorTour ready />
          <WidenToggle onToggle={toggleWide} wide={wide} />
          <PinToggle onToggle={togglePin} pinned={pinned} />
        </WindowChromeAccessory>
      ) : null}
      {stretch > 0 ? (
        // Rests invisible; the animation alone draws it, once per change.
        <span
          aria-hidden="true"
          className="window-edge-flash pointer-events-none fixed inset-0 z-50 rounded-lg ring-2 ring-tide ring-inset"
          data-testid="trajectory-inspector-edge-flash"
          key={stretch}
        />
      ) : null}
      {selection ? (
        <TrajectoryInspector
          auditContent={
            audit.content?.request_id === selection.record.id
              ? audit.content
              : null
          }
          auditError={audit.error}
          auditLoading={audit.loading}
          copyFeedback={copyFeedback}
          findShortcut
          onUnlockRaw={unlockRaw}
          pinned={pinned}
          record={selection.record}
          row={selection.row}
          service={selection.service}
          services={selection.services}
        />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-1.5 p-8 text-center">
          <strong className="text-xs">
            {t("trajectory.inspectorWindowEmpty")}
          </strong>
          <span className="text-xs text-muted-foreground">
            {t("trajectory.inspectorWindowEmptyHint")}
          </span>
        </div>
      )}
      <RawSealingDialogs
        dialog={rawDialog}
        onClose={(done) => {
          setRawDialog(null);
          if (done) refetchAudit();
        }}
        onStatus={setRawSealing}
        status={rawSealing}
      />
    </main>
  );
}

/**
 * A loose, tilted pin while the window follows the list; pressed upright and
 * filled once it floats. Only the pinned state carries a label, so a floating
 * window says so at a glance while an ordinary one keeps a quiet title bar.
 */
function PinToggle({
  onToggle,
  pinned,
}: {
  onToggle: (next: boolean) => void;
  pinned: boolean;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <TooltipProvider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            aria-label={t("trajectory.pin")}
            aria-pressed={pinned}
            className={cn(
              pinned
                ? "bg-accent text-accent-foreground hover:bg-accent/70"
                : "text-muted-foreground hover:bg-foreground/8 hover:text-foreground",
            )}
            data-testid="trajectory-inspector-pin"
            data-tour-target="inspector-pin"
            onClick={() => onToggle(!pinned)}
            size={pinned ? "xs" : "icon-xs"}
            type="button"
            variant="ghost"
          >
            <Pin
              className={cn(
                "size-3.5 transition-transform duration-200",
                !pinned && "rotate-45",
              )}
              fill={pinned ? "currentColor" : "none"}
            />
            {pinned ? t("trajectory.pinned") : null}
          </Button>
        </TooltipTrigger>
        <TooltipContent className="max-w-60" side="bottom" sideOffset={6}>
          {pinned ? t("trajectory.unpinHint") : t("trajectory.pinHint")}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/**
 * Stretches the window for long bodies and puts it back, quiet like the pin
 * beside it. The arrows point the way the window is about to go: out while
 * narrow, in while wide.
 */
function WidenToggle({
  wide,
  onToggle,
}: {
  wide: boolean;
  onToggle: (next: boolean) => void;
}) {
  const t = i18n.t.bind(i18n);
  const Icon = wide ? FoldHorizontal : UnfoldHorizontal;
  return (
    <TooltipProvider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            aria-label={wide ? t("trajectory.narrow") : t("trajectory.widen")}
            aria-pressed={wide}
            className={cn(
              wide
                ? "bg-accent text-accent-foreground hover:bg-accent/70"
                : "text-muted-foreground hover:bg-foreground/8 hover:text-foreground",
            )}
            data-testid="trajectory-inspector-widen"
            data-tour-target="inspector-widen"
            onClick={() => onToggle(!wide)}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <Icon className="size-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent className="max-w-60" side="bottom" sideOffset={6}>
          {wide ? t("trajectory.narrowHint") : t("trajectory.widenHint")}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

interface AuditState {
  content: AuditContent | null;
  loading: boolean;
  error: string | null;
}

/**
 * Refetches when the request changes, when a running request settles, when a
 * pending record's captured flags flip — request-side blobs can land before
 * the call finishes — and when the raw unlock watch asks. Every refetch drops
 * the content first, so raw parts leave the screen before the read returns.
 */
function useRequestAudit(
  requestId: string | null,
  status: string | null,
  captureKey: string,
  rawCheck: number,
): AuditState {
  const [state, setState] = useState<AuditState>({
    content: null,
    loading: false,
    error: null,
  });

  useEffect(() => {
    if (!requestId) {
      setState({ content: null, loading: false, error: null });
      return;
    }
    let active = true;
    setState({ content: null, loading: true, error: null });
    void getRequestAuditContent(requestId)
      .then((content) => {
        if (active) setState({ content, loading: false, error: null });
      })
      .catch((requestError: unknown) => {
        if (!active) return;
        const message =
          requestError instanceof Error
            ? requestError.message
            : i18n.t("records.auditContentFailed");
        setState({
          content: null,
          loading: false,
          error: message.includes("409")
            ? i18n.t("records.auditKeyBroken")
            : message,
        });
      });
    return () => {
      active = false;
    };
  }, [requestId, status, captureKey, rawCheck]);

  return state;
}

function auditCaptureKey(record: RequestRecord | null): string {
  if (!record) return "";
  return [
    record.audit.request_body_captured,
    record.audit.response_content_captured,
    record.audit.upstream_request_body_captured,
    record.audit.upstream_response_content_captured,
  ].join(":");
}
