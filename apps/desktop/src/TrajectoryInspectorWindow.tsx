import { useCallback, useEffect, useRef, useState } from "react";
import type { UnlistenFn } from "@tauri-apps/api/event";

import { getRequestAuditContent } from "./bridge";
import { useCopyFeedback } from "./copy-feedback";
import { i18n } from "./i18n";
import { notify } from "./notify";
import type { AuditContent, RequestRecord } from "./request-record-model";
import { TrajectoryInspector } from "./TrajectoryInspector";
import {
  listenInspectorSelection,
  setTrajectoryInspectorPinned,
  trajectoryInspectorState,
  type TrajectoryInspectorSelection,
} from "./trajectory-inspector-window";

/**
 * The whole app in a detached inspector window: one selected call, driven by
 * the main window over the event channel. The clicked chip is only a scroll
 * target; the pane always stacks that request's whole chain.
 *
 * Pinning freezes the call. The host stops routing selections here, and this
 * side ignores any that still arrive, so the two cannot disagree about what a
 * pinned window shows.
 *
 * Audit content is decrypted here rather than forwarded, so captured bodies
 * never cross the channel and the main window's cache stays the main window's.
 */
export function TrajectoryInspectorWindow() {
  const t = i18n.t.bind(i18n);
  const [selection, setSelection] =
    useState<TrajectoryInspectorSelection | null>(null);
  const [pinned, setPinned] = useState(false);
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

  const audit = useRequestAudit(
    selection?.record.id ?? null,
    selection?.record.status ?? null,
    auditCaptureKey(selection?.record ?? null),
  );

  return (
    <main className="flex h-dvh min-h-0 flex-col overflow-hidden pt-[var(--window-chrome-height)]">
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
          onTogglePin={togglePin}
          pinned={pinned}
          record={selection.record}
          row={selection.row}
        />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-1.5 p-8 text-center">
          <strong className="text-xs">{t("trajectory.inspectorWindowEmpty")}</strong>
          <span className="text-xs text-muted-foreground">
            {t("trajectory.inspectorWindowEmptyHint")}
          </span>
        </div>
      )}
    </main>
  );
}

interface AuditState {
  content: AuditContent | null;
  loading: boolean;
  error: string | null;
}

/**
 * Refetches when the request changes, when a running request settles, and
 * when a pending record's captured flags flip — request-side blobs can land
 * before the call finishes.
 */
function useRequestAudit(
  requestId: string | null,
  status: string | null,
  captureKey: string,
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
  }, [requestId, status, captureKey]);

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
