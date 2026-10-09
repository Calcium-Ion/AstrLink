import { isWebEdition } from "./edition";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";

import { CapabilityToggle } from "@/components/CapabilityToggle";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { FormMessage } from "@/components/FormMessage";
import { LockKeyhole } from "@/components/icons";
import { Panel, PanelHeader } from "@/components/Panel";
import {
  ProofConfirmDialog,
  type ProofInput,
  type ProofMode,
  type ProofResult,
} from "@/components/ProofConfirmDialog";
import { RawPasswordFields } from "@/components/RawPasswordFields";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

import { acknowledgeRawKey, setRawPassword, unlockRaw } from "./bridge";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import {
  newPasswordIssue,
  rawPasswordUnset,
  rawProofMode,
  rawProtection,
  rawSetupNeeded,
  type RawPasswordAction,
  type RawProtection,
  type RawSealingOutcome,
  type RawSealingState,
} from "./raw-sealing-model";
import {
  rawProofOf,
  rawSealingErrorMessage,
  refusalResult,
} from "./raw-sealing-ui";
import { useRawSealingStatus } from "./use-raw-sealing-status";

/**
 * A raw sealing dialog the page asks for; `capture` also turns capture on,
 * `required` is the one the app keeps up until a raw password is set, and
 * `replaced` the one it keeps up for a key replaced outside the desktop.
 */
export type RawDialog =
  | { kind: "set" }
  | { kind: "change" }
  | { kind: "reset" }
  | { kind: "unlock" }
  | { kind: "capture" }
  | { kind: "required" }
  | { kind: "replaced" };

/** Minutes before Core locks an idle unlock session, for the copy. */
export function unlockIdleMinutes(status: RawSealingState): number {
  return Math.max(1, Math.round(status.unlock_idle_seconds / 60));
}

/** No raw password protects the raw key, so raw parts are not kept. */
export function rawPasswordMissing(status: RawSealingState | null): boolean {
  return status?.password_required === true;
}

/**
 * A raw password still needs setting (see `rawSetupNeeded`); unknown while
 * the status is.
 */
export function useRawSetupNeeded(status: RawSealingState | null): boolean {
  return status !== null && rawSetupNeeded(status);
}

/**
 * The raw password summary: how the raw key opens, the actions on it, and,
 * where the page passes it, whether agent tools may ask for raw content.
 */
export function RawPasswordPanel({
  agentAccess = false,
  busy,
  error,
  onAction,
  onAgentAccessChange,
  status,
}: {
  agentAccess?: boolean;
  busy: boolean;
  error: string | null;
  onAction: (action: RawPasswordAction) => void;
  /** Shows the agent access toggle. */
  onAgentAccessChange?: (enabled: boolean) => void;
  status: RawSealingState | null;
}) {
  const t = useT();
  const titleId = useId();
  const state = status ? rawProtection(status) : null;
  const missing = rawPasswordMissing(status);
  // Unknown is not missing: a failed read leaves the toggle as it was.
  const unprotected = status !== null && rawPasswordUnset(status);

  const tone = protectionTone(state, missing);
  const label = rawProtectionLabel(state);
  const hint = state === "password" ? t("rawSealing.hint.password") : null;

  const action = (id: RawPasswordAction, text: string) => (
    <Button
      disabled={busy}
      key={id}
      onClick={() => onAction(id)}
      size="xs"
      type="button"
      variant="outline"
    >
      {text}
    </Button>
  );
  const actions =
    state === "password"
      ? [
          action("change", t("rawSealing.change")),
          ...(isWebEdition ? [] : [action("reset", t("rawSealing.reset"))]),
        ]
      : null;

  return (
    <Panel
      aria-labelledby={titleId}
      data-slot="raw-password-panel"
      role="region"
    >
      <PanelHeader actions={actions} size="sm">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="text-sm font-semibold" id={titleId}>
            {t("rawSealing.title")}
          </h3>
          <StatusBadge data-slot="raw-password-state" tone={tone}>
            {label}
          </StatusBadge>
        </div>
      </PanelHeader>
      <div className="grid gap-3 px-3 py-2.5">
        {error ? (
          <FormMessage tone="error">
            {t("rawSealing.loadFailed", { message: error })}
          </FormMessage>
        ) : missing && !isWebEdition ? (
          <FormMessage
            className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5"
            data-slot="raw-password-missing"
            tone="warning"
          >
            <span className="min-w-0">{t("rawSealing.hint.unset")}</span>
            <Button
              disabled={busy}
              onClick={() => onAction("set")}
              size="xs"
              type="button"
              variant="outline"
            >
              {t("rawSealing.setupAction")}
            </Button>
          </FormMessage>
        ) : hint ? (
          <p className="text-xs leading-relaxed text-muted-foreground">
            {hint}
          </p>
        ) : null}
        {onAgentAccessChange ? (
          <CapabilityToggle
            checked={agentAccess && !unprotected}
            description={
              unprotected
                ? t("rawSealing.agentAccessNeedsPassword")
                : t("rawSealing.agentAccessHint")
            }
            disabled={busy || unprotected}
            label={t("rawSealing.agentAccess")}
            onCheckedChange={onAgentAccessChange}
            size="default"
          />
        ) : null}
      </div>
    </Panel>
  );
}

/** Protected is positive; unprotected, or unknown, waits for setting up. */
function protectionTone(
  state: RawProtection | null,
  missing: boolean,
): StatusTone {
  if (state === "password") return "positive";
  return missing ? "pending" : "neutral";
}

/** The label for how raw content is protected; null while unknown. */
function rawProtectionLabel(state: RawProtection | null): string {
  switch (state) {
    case "password":
      return i18n.t("rawSealing.state.password");
    case "unset":
      return i18n.t("rawSealing.state.unset");
    case null:
      return i18n.t("rawSealing.state.unknown");
  }
}

/**
 * Raw protection in a page header, such as the Security page's: its state
 * at a glance, and the summary with its actions one click away.
 */
export function RawPasswordEntry({
  coreSessionKey,
  isReady,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
}) {
  const t = useT();
  const { status, error, setStatus } = useRawSealingStatus(
    coreSessionKey,
    isReady,
  );
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<RawDialog | null>(null);
  if (!isReady) return null;
  const state = status ? rawProtection(status) : null;
  const tone = protectionTone(state, rawPasswordMissing(status));
  const stateLabel = rawProtectionLabel(state);
  return (
    <>
      <Popover onOpenChange={setOpen} open={open}>
        <PopoverTrigger asChild>
          <Button
            data-slot="raw-password-entry"
            size="sm"
            type="button"
            variant="outline"
          >
            <LockKeyhole aria-hidden="true" />
            {t("rawSealing.title")}
            <StatusDot tone={tone} />
            {/* The dot's state, for screen readers. */}
            <span className="sr-only">{stateLabel}</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-80 p-0">
          <RawPasswordPanel
            busy={dialog !== null}
            error={error}
            onAction={(action) => {
              // The dialog takes over from the popover.
              setOpen(false);
              setDialog({ kind: action });
            }}
            status={status}
          />
        </PopoverContent>
      </Popover>
      <RawSealingDialogs
        dialog={dialog}
        onClose={() => setDialog(null)}
        onStatus={setStatus}
        status={status}
      />
    </>
  );
}

/**
 * Keeps the raw password setup up until it is done: only setting a raw
 * password closes it, and the gateway keeps serving meanwhile. A key
 * replaced outside the desktop keeps its own warning up the same way, until
 * the operator confirms it with its password or resets it.
 * `suspended` holds it back while the first-run guide asks for the password
 * in its own step, or before it is known whether that guide opens.
 */
export function RawPasswordGate({
  onStatus,
  status,
  suspended,
}: {
  onStatus: (next: RawSealingState) => void;
  status: RawSealingState | null;
  suspended: boolean;
}) {
  const setupNeeded = useRawSetupNeeded(status);
  const replaced = !suspended && status?.key_replaced === true;
  const required = !isWebEdition && !suspended && setupNeeded;
  return (
    <RawSealingDialogs
      dialog={
        replaced ? { kind: "replaced" } : required ? { kind: "required" } : null
      }
      onClose={() => {}}
      onStatus={onStatus}
      status={status}
    />
  );
}

/**
 * The dialogs behind the raw password actions and the unlock button. New
 * passwords live only in this component's state and are cleared on every
 * submit and whenever the dialog closes.
 */
export function RawSealingDialogs({
  dialog,
  onClose,
  onStatus,
  status,
}: {
  dialog: RawDialog | null;
  /** `done` is true once the action went through. */
  onClose: (done: boolean) => void;
  onStatus: (next: RawSealingState) => void;
  status: RawSealingState | null;
}) {
  const t = useT();
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [resetStep, setResetStep] = useState<"confirm" | "password">("confirm");
  // Resetting a key replaced outside the desktop: the destructive
  // confirmation first, then the new password.
  const [replaceStep, setReplaceStep] = useState<"confirm" | "password" | null>(
    null,
  );
  const newPasswordRef = useRef<HTMLInputElement>(null);
  const kind = status ? (dialog?.kind ?? null) : null;

  useEffect(() => {
    setPassword("");
    setConfirmation("");
    setResetStep("confirm");
    setReplaceStep(null);
  }, [kind]);

  const clearNewPassword = () => {
    setPassword("");
    setConfirmation("");
  };

  const close = (done: boolean) => {
    clearNewPassword();
    onClose(done);
  };

  /** Applies a sealing outcome; true once the action went through. */
  const settle = (
    outcome: RawSealingOutcome,
  ): { done: true } | { done: false; result: ProofResult } => {
    if (outcome.outcome !== "sealing") {
      return { done: false, result: refusalResult(outcome) };
    }
    onStatus(outcome.status);
    return { done: true };
  };

  const submitPassword = async (
    action: RawPasswordAction,
    input: ProofInput,
  ): Promise<ProofResult> => {
    const next = password;
    // Nothing keeps the new password once it is on its way.
    clearNewPassword();
    let outcome: RawSealingOutcome;
    try {
      outcome = await setRawPassword(action, next, rawProofOf(input));
    } catch (error) {
      return { kind: "error", message: rawSealingErrorMessage(error) };
    }
    const settled = settle(outcome);
    if (!settled.done) return settled.result;
    if (action === "change") {
      notify.success(i18n.t("rawSealing.changed"));
    } else if (action === "reset") {
      notify.success(resetNotice(outcome));
    } else if (kind === "set" || kind === "required") {
      notify.success(i18n.t("rawSealing.saved"));
    }
    close(true);
    return { kind: "done" };
  };

  const submitUnlock = async (input: ProofInput): Promise<ProofResult> => {
    const proof = rawProofOf(input);
    if (!proof) {
      return { kind: "error", message: i18n.t("rawSealing.unlockUnavailable") };
    }
    let outcome: RawSealingOutcome;
    try {
      outcome = await unlockRaw(proof);
    } catch (error) {
      return { kind: "error", message: rawSealingErrorMessage(error) };
    }
    const settled = settle(outcome);
    if (!settled.done) return settled.result;
    notify.success(i18n.t("rawSealing.unlocked"));
    close(true);
    return { kind: "done" };
  };

  const submitAcknowledge = async (input: ProofInput): Promise<ProofResult> => {
    // Only the replaced key's own password shows the operator set it.
    const proof = rawProofOf(input);
    if (!proof) return { kind: "password_invalid" };
    let outcome: RawSealingOutcome;
    try {
      outcome = await acknowledgeRawKey(proof);
    } catch (error) {
      return { kind: "error", message: rawSealingErrorMessage(error) };
    }
    const settled = settle(outcome);
    if (!settled.done) return settled.result;
    notify.success(i18n.t("rawSealing.replaced.accepted"));
    close(true);
    return { kind: "done" };
  };

  const invalidNew =
    status !== null &&
    newPasswordIssue(password, confirmation, status) !== null;
  const settingUp = kind === "capture" || kind === "set" || kind === "required";
  const fields = status ? (
    <RawPasswordFields
      confirmation={confirmation}
      inputRef={newPasswordRef}
      labels={
        settingUp
          ? {
              confirmation: t("rawSealing.confirmField"),
              password: t("rawSealing.passwordField"),
            }
          : undefined
      }
      onConfirmationChange={setConfirmation}
      onPasswordChange={setPassword}
      password={password}
      policy={status}
    />
  ) : null;
  const passwordWarning = (
    <FormMessage data-slot="raw-password-warning" tone="warning">
      {t("rawSealing.passwordWarning")}
    </FormMessage>
  );

  let proofDialog: ProofDialogCopy | null = null;
  if (status === null || kind === null) {
    proofDialog = null;
  } else if (settingUp) {
    // Nothing opens the key yet, so setting the password needs no proof and
    // ends the setup.
    const description = (
      <>
        {kind === "capture" ? (
          <>
            <p>{t("records.enableCaptureBody")}</p>
            <p>{t("rawSealing.captureNeedsPassword")}</p>
          </>
        ) : (
          <>
            <p>{t("rawSealing.setupBody")}</p>
            {status.password_required ? (
              <p data-slot="raw-setup-pending">{t("rawSealing.hint.unset")}</p>
            ) : null}
          </>
        )}
      </>
    );
    const title =
      kind === "capture"
        ? t("records.enableCaptureTitle")
        : t("rawSealing.setupTitle");
    proofDialog = {
      action:
        kind === "capture"
          ? t("records.confirmEnable")
          : t("rawSealing.setupPasswordAction"),
      description,
      dismissable: kind !== "required",
      focusNew: true,
      note: passwordWarning,
      proof: "confirm",
      submit: (input) => submitPassword("set", input),
      title,
      withFields: true,
    };
  } else if (kind === "replaced" && replaceStep === "password") {
    // The destructive confirmation was the proof; a reset opens nothing.
    proofDialog = {
      action: t("rawSealing.resetAction"),
      cancelLabel: t("common.back"),
      description: <p>{t("rawSealing.resetPasswordDescription")}</p>,
      focusNew: true,
      onCancel: () => {
        clearNewPassword();
        setReplaceStep(null);
      },
      proof: "confirm",
      submit: (input) => submitPassword("reset", input),
      title: t("rawSealing.resetPasswordTitle"),
      withFields: true,
    };
  } else if (kind === "replaced" && replaceStep === null) {
    // The key's own password shows the operator chose it.
    proofDialog = {
      action: t("rawSealing.replaced.acknowledge"),
      description: (
        <>
          <FormMessage data-slot="raw-key-replaced" tone="warning">
            {t("rawSealing.replaced.warning")}
          </FormMessage>
          <p>{t("rawSealing.replaced.cli")}</p>
          <Button
            className="h-auto px-0"
            onClick={() => setReplaceStep("confirm")}
            size="xs"
            type="button"
            variant="link"
          >
            {t("rawSealing.replaced.reset")}
          </Button>
        </>
      ),
      dismissable: false,
      focusNew: false,
      passwordLabel: t("rawSealing.replaced.passwordLabel"),
      proof: "password",
      submit: submitAcknowledge,
      title: t("rawSealing.replaced.title"),
      withFields: false,
    };
  } else if (kind === "change") {
    const proof = rawProofMode(status);
    proofDialog = {
      action: t("rawSealing.changeAction"),
      description: <p>{t("rawSealing.changeDescription")}</p>,
      withFields: true,
      focusNew: proof !== "password",
      passwordLabel: t("rawSealing.currentPassword"),
      proof,
      submit: (input) => submitPassword("change", input),
      title: t("rawSealing.changeTitle"),
    };
  } else if (kind === "reset" && resetStep === "password") {
    // The destructive confirmation was the proof; a reset opens nothing.
    proofDialog = {
      action: t("rawSealing.resetAction"),
      description: <p>{t("rawSealing.resetPasswordDescription")}</p>,
      withFields: true,
      focusNew: true,
      proof: "confirm",
      submit: (input) => submitPassword("reset", input),
      title: t("rawSealing.resetPasswordTitle"),
    };
  } else if (kind === "unlock") {
    proofDialog = {
      action: t("rawSealing.unlock"),
      description: (
        <p>
          {t("rawSealing.unlockDescription", {
            minutes: unlockIdleMinutes(status),
          })}
        </p>
      ),
      focusNew: false,
      proof: rawProofMode(status),
      withFields: false,
      submit: submitUnlock,
      title: t("rawSealing.unlockTitle"),
    };
  }

  const resetConfirmOpen =
    status !== null && kind === "reset" && resetStep === "confirm";
  const replacedResetConfirmOpen =
    status !== null && kind === "replaced" && replaceStep === "confirm";
  return (
    <>
      <ConfirmDialog
        confirmLabel={t("rawSealing.resetConfirm")}
        description={<p>{t("rawSealing.resetDescription")}</p>}
        destructive
        // Back to the warning; it stays until the key is dealt with.
        onCancel={() => setReplaceStep(null)}
        onConfirm={() => setReplaceStep("password")}
        open={replacedResetConfirmOpen}
        title={t("rawSealing.resetTitle")}
      />
      <ConfirmDialog
        confirmLabel={t("rawSealing.resetConfirm")}
        description={<p>{t("rawSealing.resetDescription")}</p>}
        destructive
        onCancel={() => close(false)}
        // A reset always sets the new password with it (D11).
        onConfirm={() => setResetStep("password")}
        open={resetConfirmOpen}
        title={t("rawSealing.resetTitle")}
      />
      <ProofConfirmDialog
        actions={[{ id: "submit", label: proofDialog?.action ?? "" }]}
        cancelLabel={proofDialog?.cancelLabel}
        description={proofDialog?.description ?? null}
        dismissable={proofDialog?.dismissable ?? true}
        fields={
          proofDialog?.withFields ? (
            <>
              {fields}
              <p
                className="text-xs text-muted-foreground"
                data-slot="raw-password-manager-hint"
              >
                {t("rawSealing.passwordManagerHint")}
              </p>
              {proofDialog.note}
            </>
          ) : undefined
        }
        initialFocusRef={proofDialog?.focusNew ? newPasswordRef : undefined}
        onCancel={proofDialog?.onCancel ?? (() => close(false))}
        onSubmit={(_, input) =>
          proofDialog ? proofDialog.submit(input) : Promise.resolve(done)
        }
        open={proofDialog !== null}
        passwordLabel={proofDialog?.passwordLabel}
        proof={proofDialog?.proof ?? "confirm"}
        submitDisabled={proofDialog?.withFields === true && invalidNew}
        title={proofDialog?.title ?? ""}
      />
    </>
  );
}

interface ProofDialogCopy {
  action: string;
  cancelLabel?: string;
  description: ReactNode;
  /** False keeps the dialog up until the action is done. */
  dismissable?: boolean;
  /** Focus the new password instead of the proof on open. */
  focusNew: boolean;
  /** Follows the new password fields. */
  note?: ReactNode;
  /** Replaces closing the dialog, e.g. to step back within it. */
  onCancel?: () => void;
  passwordLabel?: string;
  proof: ProofMode;
  submit: (input: ProofInput) => Promise<ProofResult>;
  title: string;
  /** The dialog asks for a new password. */
  withFields: boolean;
}

const done: ProofResult = { kind: "done" };

function resetNotice(outcome: RawSealingOutcome): string {
  const reset = outcome.outcome === "sealing" ? outcome.reset : null;
  return i18n.t("rawSealing.resetDone", {
    parts: i18n.t("rawSealing.resetParts", {
      count: reset?.deleted_parts ?? 0,
    }),
    records: i18n.t("rawSealing.resetRecords", {
      count: reset?.affected_records ?? 0,
    }),
  });
}
