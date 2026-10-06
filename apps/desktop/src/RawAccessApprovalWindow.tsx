import { useCallback, useEffect, useRef, useState } from "react";

import { DataField } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import {
  ProofPasswordField,
  proofPasswordTooLong,
} from "@/components/ProofConfirmDialog";
import { Button } from "@/components/ui/button";

import { ActiveRawGrants } from "./ActiveRawGrants";
import {
  decideRawAccess,
  getRawSealingStatus,
  getRequestRecord,
  listRawAccess,
  listenRawSealingChanged,
} from "./bridge";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import {
  oldestPendingGrant,
  type RawAccessDecision,
  type RawAccessGrant,
  type RawAccessList,
  type RawAccessProofOutcome,
} from "./raw-access-model";
import { rawPasswordUnset, type RawSealingState } from "./raw-sealing-model";
import { rawSealingErrorMessage } from "./raw-sealing-ui";

export const RAW_ACCESS_POLL_MS = 2_000;

type Approval = Exclude<RawAccessDecision, "deny">;

const approvals: ReadonlyArray<{ decision: Approval; label: string }> = [
  { decision: "once", label: "rawAccess.once" },
  { decision: "window_5m", label: "rawAccess.window5m" },
  { decision: "window_1h", label: "rawAccess.window1h" },
];

const approvedKeys: Record<Approval, string> = {
  once: "rawAccess.approvedOnce",
  window_5m: "rawAccess.approved5m",
  window_1h: "rawAccess.approved1h",
};

/** How to prove on one request; `state` is null if it failed to load. */
interface SealingFacts {
  grantId: string;
  state: RawSealingState | null;
}

interface RequestFacts {
  requestId: string;
  model: string | null;
  startedAt: string | null;
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(date);
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * The one place an agent's raw access request is decided (plan §5.11.6,
 * D14). The host opens this window without focus and nothing here takes
 * it, and there is no form: a keystroke meant for another window, or Enter,
 * approves nothing. While Core's unlock session is open an approval is one
 * click; otherwise it needs the raw password, which lives only in this
 * state until it is sent. Closing the window decides nothing.
 */
export function RawAccessApprovalWindow() {
  const t = useT();
  const [list, setList] = useState<RawAccessList | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  // Requests decided here, hidden until the next list stops returning them.
  const [settled, setSettled] = useState<ReadonlySet<string>>(new Set());
  const [facts, setFacts] = useState<RequestFacts | null>(null);
  const [sealing, setSealing] = useState<SealingFacts | null>(null);
  const [password, setPassword] = useState("");
  // Core locked after the list said it was open; ask for the password.
  const [proofDemanded, setProofDemanded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryAt, setRetryAt] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const busyRef = useRef(false);
  const generation = useRef(0);

  const refresh = useCallback(async () => {
    const current = ++generation.current;
    try {
      const next = await listRawAccess();
      if (current !== generation.current) return;
      setList(next);
      setLoadError(null);
      setSettled((previous) => {
        const live = new Set(next.pending.map((grant) => grant.grant_id));
        const kept = [...previous].filter((id) => live.has(id));
        return kept.length === previous.size ? previous : new Set(kept);
      });
    } catch (reason) {
      // The last list stays; the next poll retries.
      if (current === generation.current) setLoadError(messageOf(reason));
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), RAW_ACCESS_POLL_MS);
    let active = true;
    let stop: (() => void) | null = null;
    // Locking and unlocking change whether an approval needs the password.
    void listenRawSealingChanged(() => void refresh()).then(
      (unlisten) => {
        if (active) stop = unlisten;
        else unlisten();
      },
      (reason: unknown) =>
        console.error("Unable to observe the AstrLink raw lock", reason),
    );
    return () => {
      active = false;
      clearInterval(timer);
      stop?.();
    };
  }, [refresh]);

  useEffect(() => {
    if (retryAt === null) return;
    const timer = setInterval(() => {
      const current = Date.now();
      setNow(current);
      if (current >= retryAt) setRetryAt(null);
    }, 250);
    return () => clearInterval(timer);
  }, [retryAt]);

  const pending = (list?.pending ?? []).filter(
    (candidate) => !settled.has(candidate.grant_id),
  );
  const grant = oldestPendingGrant(pending);
  const grantId = grant?.grant_id ?? null;
  const requestId = grant?.request_id ?? null;

  // Every request starts clean: no password or refusal carries over.
  useEffect(() => {
    setPassword("");
    setError(null);
    setRetryAt(null);
    setProofDemanded(false);
  }, [grantId]);

  // Read how to prove on every new request, since the raw password may have
  // been set or reset since the last one.
  useEffect(() => {
    if (grantId === null) return;
    let cancelled = false;
    void getRawSealingStatus().then(
      (state) => {
        if (!cancelled) setSealing({ grantId, state });
      },
      () => {
        if (!cancelled) setSealing({ grantId, state: null });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [grantId]);

  useEffect(() => {
    if (requestId === null) return;
    let cancelled = false;
    void getRequestRecord(requestId).then(
      (record) => {
        if (cancelled) return;
        setFacts({
          requestId,
          model: record.requested_model,
          startedAt: record.started_at,
        });
      },
      () => {
        if (!cancelled) setFacts({ requestId, model: null, startedAt: null });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [requestId]);

  const needsPassword = proofDemanded || list?.unlocked !== true;
  // The field goes once Core is open again; so does what was typed into it.
  useEffect(() => {
    if (!needsPassword) setPassword("");
  }, [needsPassword]);

  const settle = (id: string) =>
    setSettled((previous) => new Set(previous).add(id));
  const unsettle = (id: string) =>
    setSettled((previous) => {
      const next = new Set(previous);
      next.delete(id);
      return next;
    });

  const currentSealing = sealing?.grantId === grantId ? sealing : null;
  const sealingState = currentSealing?.state ?? null;
  // Core reads raw content only through the raw password; without one only
  // denying is left.
  const unreachable = sealingState !== null && rawPasswordUnset(sealingState);
  const approveBlocked =
    busy ||
    currentSealing === null ||
    unreachable ||
    retryAt !== null ||
    (needsPassword && (password === "" || proofPasswordTooLong(password)));

  const approve = async (target: RawAccessGrant, decision: Approval) => {
    if (busyRef.current || approveBlocked) return;
    const proof = needsPassword
      ? ({ kind: "password", password } as const)
      : undefined;
    // Nothing keeps the password once it is on its way.
    setPassword("");
    setError(null);
    busyRef.current = true;
    setBusy(true);
    let outcome: RawAccessProofOutcome;
    try {
      outcome = await decideRawAccess(target.grant_id, decision, proof);
    } catch (reason) {
      setError(rawSealingErrorMessage(reason));
      return;
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
    const client = target.client_name || t("rawAccess.unnamedClient");
    switch (outcome.outcome) {
      case "decided":
        settle(target.grant_id);
        notify.success(t(approvedKeys[decision], { client }));
        break;
      case "not_pending":
        settle(target.grant_id);
        notify.info(t("rawAccess.notPending"));
        break;
      case "proof_required":
        setProofDemanded(true);
        break;
      case "password_invalid":
        setError(t("proofDialog.passwordInvalid"));
        return;
      case "backoff": {
        // One clock read: a second read a millisecond later shows N+1 seconds.
        const current = Date.now();
        setNow(current);
        setRetryAt(current + outcome.retry_after_seconds * 1000);
        return;
      }
    }
    void refresh();
  };

  const deny = (target: RawAccessGrant) => {
    if (busyRef.current) return;
    settle(target.grant_id);
    void decideRawAccess(target.grant_id, "deny").then(
      (outcome) => {
        if (outcome.outcome === "decided") notify.info(t("rawAccess.denied"));
        void refresh();
      },
      (reason: unknown) => {
        unsettle(target.grant_id);
        notify.error(t("rawAccess.denyFailed", { message: messageOf(reason) }));
      },
    );
  };

  const client = grant?.client_name || t("rawAccess.unnamedClient");
  const currentFacts = facts?.requestId === requestId ? facts : null;
  const queued = pending.length - 1;
  const retrySeconds =
    retryAt === null ? 0 : Math.max(1, Math.ceil((retryAt - now) / 1000));

  return (
    <main className="flex h-dvh min-h-0 flex-col overflow-hidden pt-[var(--window-chrome-height)]">
      <div
        className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto overscroll-contain px-4 pt-2 pb-4"
        data-slot="raw-access-approval-body"
      >
        {loadError !== null ? (
          <FormMessage tone="error">
            {t("rawAccess.loadFailed", { message: loadError })}
          </FormMessage>
        ) : null}
        {grant ? (
          <section className="grid gap-3" data-slot="raw-access-request">
            <div className="grid gap-1">
              <h1 className="text-sm font-semibold">{t("rawAccess.title")}</h1>
              <p className="text-xs text-text-secondary">
                {t("rawAccess.description", { client })}
              </p>
              {queued > 0 ? (
                <p className="text-micro text-muted-foreground">
                  {t("rawAccess.queued", { count: queued })}
                </p>
              ) : null}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <DataField
                label={t("rawAccess.request")}
                value={
                  <code className="font-mono text-xs break-all">
                    {grant.request_id}
                  </code>
                }
              />
              <DataField
                label={t("rawAccess.model")}
                value={currentFacts?.model ?? t("common.unknown")}
              />
              <DataField
                label={t("rawAccess.requestTime")}
                value={
                  currentFacts?.startedAt
                    ? formatTimestamp(currentFacts.startedAt)
                    : t("common.unknown")
                }
              />
              <DataField
                label={t("rawAccess.expires")}
                value={formatTimestamp(grant.expires_at)}
              />
            </div>
            <DataField
              label={t("rawAccess.reason")}
              value={
                <span className="block max-h-24 overflow-y-auto text-xs whitespace-pre-wrap text-text-secondary [overflow-wrap:anywhere]">
                  {grant.reason}
                </span>
              }
            />
            <FormMessage tone="notice">{t("rawAccess.onceHint")}</FormMessage>
            <FormMessage data-slot="raw-access-timed-warning" tone="warning">
              {t("rawAccess.timedWarning")}
            </FormMessage>
            <FormMessage tone="warning">
              {t("rawAccess.providerWarning")}
            </FormMessage>
            {unreachable ? (
              <FormMessage data-testid="raw-access-unreachable" tone="warning">
                {t("rawAccess.proofUnavailable")}
              </FormMessage>
            ) : null}
          </section>
        ) : list !== null ? (
          <EmptyState
            description={t("rawAccess.emptyDescription")}
            title={t("rawAccess.emptyTitle")}
          />
        ) : null}
        <ActiveRawGrants
          grants={list?.active ?? []}
          onRevoked={(revoked) => {
            notify.success(
              t("rawAccess.active.revoked", {
                client: revoked.client_name || t("rawAccess.unnamedClient"),
              }),
            );
            void refresh();
          }}
          title={t("rawAccess.active.title")}
        />
      </div>
      {grant ? (
        <footer
          className="grid gap-3 border-t bg-muted/40 px-4 py-3"
          data-slot="raw-access-decision"
        >
          {needsPassword && !unreachable ? (
            <div className="grid gap-2">
              <FormMessage tone="notice">
                {t(
                  proofDemanded
                    ? "rawAccess.lockedMeanwhile"
                    : "rawAccess.locked",
                )}
              </FormMessage>
              <ProofPasswordField
                disabled={busy}
                onChange={setPassword}
                value={password}
              />
            </div>
          ) : null}
          {retryAt !== null ? (
            <FormMessage data-slot="proof-backoff" tone="warning">
              {t("proofDialog.backoff", { seconds: retrySeconds })}
            </FormMessage>
          ) : null}
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <div className="grid grid-cols-2 gap-2">
            {approvals.map(({ decision, label }) => (
              <Button
                data-decision={decision}
                disabled={approveBlocked}
                key={decision}
                onClick={() => void approve(grant, decision)}
                // Approving takes a click: Enter on a focused button does
                // nothing here.
                onKeyDown={(event) => {
                  if (event.key === "Enter") event.preventDefault();
                }}
                type="button"
                variant={decision === "once" ? "default" : "outline"}
              >
                {t(label)}
              </Button>
            ))}
            <Button
              data-decision="deny"
              disabled={busy}
              onClick={() => deny(grant)}
              type="button"
              variant="outline"
            >
              {t("rawAccess.deny")}
            </Button>
          </div>
          <p className="text-micro text-muted-foreground">
            {t("rawAccess.closeHint")}
          </p>
        </footer>
      ) : null}
    </main>
  );
}
