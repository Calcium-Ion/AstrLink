import { useCallback, useEffect, useId, useMemo, useState } from "react";

import { ActionGroup } from "@/components/ActionGroup";
import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { Panel, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import {
  configureSync,
  forgetSyncPassphrase,
  getSyncConfig,
  getSyncStatus,
  runSync,
  setSyncPassphrase,
} from "./bridge";
import { currentLocale, i18n, useT } from "./i18n";
import { notify } from "./notify";
import {
  defaultSyncFolder,
  maxSyncDebounceSeconds,
  maxSyncDeviceNameLength,
  minSyncDebounceSeconds,
  minSyncIntervalMinutes,
  minSyncPassphraseLength,
  syncConfigIssue,
  syncS3Addressings,
  syncTriggerModes,
  type SyncConfig,
  type SyncS3Addressing,
  type SyncScope,
  type SyncStatus,
  type SyncTriggerMode,
} from "./sync-model";

/** How often the status panel re-reads while the tab is open. */
const STATUS_REFRESH_MS = 15_000;

const scopeKeys = [
  "services",
  "credentials",
  "policies",
  "pricing",
  "tokens",
] as const satisfies readonly (keyof SyncScope)[];

// A group that names another is only meaningful with it: a credential without
// its provider, or a price binding without its service, would land on the other
// device as a dangling reference. The engine intersects the two devices' scopes,
// so showing the dependency here matches what a run will actually do.
const scopeDependencies: Partial<Record<keyof SyncScope, keyof SyncScope>> = {
  credentials: "services",
  pricing: "services",
};

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function timestampLabel(value: string | undefined): string {
  if (!value) return i18n.t("settings.sync.never");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(currentLocale(), {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function statusTone(status: SyncStatus | null): StatusTone {
  if (status?.running) return "pending";
  if (status?.last_error) return "negative";
  if (status?.configured) return "positive";
  return "neutral";
}

function statusLabel(status: SyncStatus | null): string {
  if (!status) return i18n.t("settings.loading");
  if (status.running) return i18n.t("settings.sync.state.running");
  if (!status.enabled) return i18n.t("settings.sync.state.off");
  if (!status.configured) return i18n.t("settings.sync.state.incomplete");
  return i18n.t("settings.sync.state.ready");
}

function matchesStoredPassphraseError(error: unknown): boolean {
  const message = messageOf(error, "");
  return (
    message.includes("different sync passphrase") ||
    message.includes("sync passphrase")
  );
}

/**
 * A WebDAV remote is configured through the endpoint alone, so the folder it
 * writes under is filled in here rather than left blank, which would put the
 * package at the server root.
 */
function filledRemote(config: SyncConfig): SyncConfig {
  if (config.remote.kind === "webdav" && config.remote.path.trim() === "") {
    return {
      ...config,
      remote: { ...config.remote, path: defaultSyncFolder },
    };
  }
  return config;
}

export function SyncSettings({
  onDirtyChange,
}: {
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [stored, setStored] = useState<SyncConfig | null>(null);
  const [draft, setDraft] = useState<SyncConfig | null>(null);
  const [status, setStatus] = useState<SyncStatus | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "sync" | "passphrase" | null>(null);
  const [passphrase, setPassphrase] = useState("");
  const [forgetOpen, setForgetOpen] = useState(false);
  const [forcePushOpen, setForcePushOpen] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const passphraseId = useId();

  const baseline = useMemo(
    () => (stored === null ? "" : JSON.stringify(stored)),
    [stored],
  );
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;

  /**
   * Reads the stored configuration again, so a flag a run just spent stops
   * showing in the form.
   */
  const reloadStored = useCallback(async (): Promise<void> => {
    try {
      const next = filledRemote(await getSyncConfig());
      setStored(next);
      setDraft(next);
    } catch {
      // Leave the form as it stands; reopening the page reads it again.
    }
  }, []);

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    let cancelled = false;
    void getSyncConfig()
      .then((config) => {
        if (cancelled) return;
        const filled = filledRemote(config);
        setStored(filled);
        setDraft(filled);
        setLoadError(null);
      })
      .catch((error) => {
        if (cancelled) return;
        // A build without the sync surface answers 503; say that rather than
        // showing a form that can never save.
        if (messageOf(error, "").includes("sync_unavailable")) {
          setUnavailable(true);
          return;
        }
        setLoadError(messageOf(error, t("settings.sync.loadFailed")));
      });
    return () => {
      cancelled = true;
    };
  }, [t]);

  const storedForcePush = stored?.force_push ?? false;
  useEffect(() => {
    let cancelled = false;
    const load = () => {
      void getSyncStatus()
        .then((next) => {
          if (cancelled) return;
          setStatus(next);
          // A run spends an armed force push, and a scheduled run spends it
          // without this form being the caller, so a status that disagrees with
          // the form's copy is what brings the stored one back.
          if (!dirty && next.force_push_armed !== storedForcePush)
            void reloadStored();
        })
        .catch(() => {
          // A failed status read keeps the last one on screen; the run itself
          // reports its own failure.
        });
    };
    load();
    const timer = window.setInterval(load, STATUS_REFRESH_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [dirty, storedForcePush, reloadStored]);

  if (unavailable) {
    return (
      <FormMessage className="m-2" tone="notice">
        {t("settings.sync.unavailable")}
      </FormMessage>
    );
  }
  if (loadError) {
    return (
      <FormMessage className="m-2" tone="error">
        {loadError}
      </FormMessage>
    );
  }
  if (draft === null) {
    return (
      <p className="p-4 text-xs text-text-secondary">{t("settings.loading")}</p>
    );
  }

  const issue = syncConfigIssue(draft);
  const passphraseTooShort =
    passphrase.length > 0 && [...passphrase].length < minSyncPassphraseLength;

  const patch = (next: Partial<SyncConfig>): void => {
    setDraft({ ...draft, ...next });
  };
  const patchRemote = (next: Partial<SyncConfig["remote"]>): void => {
    setDraft({ ...draft, remote: { ...draft.remote, ...next } });
  };
  // Switching the remote kind fills the fields the new kind reads but the old
  // one left blank, so an S3 remote does not inherit a folder name as its
  // bucket and a WebDAV remote does not inherit a bucket as its folder.
  const patchRemoteKind = (kind: SyncConfig["remote"]["kind"]): void => {
    const path =
      draft.remote.path.trim() === ""
        ? kind === "webdav"
          ? defaultSyncFolder
          : ""
        : draft.remote.path;
    setDraft({ ...draft, remote: { ...draft.remote, kind, path } });
  };
  const patchTrigger = (next: Partial<SyncConfig["trigger"]>): void => {
    setDraft({ ...draft, trigger: { ...draft.trigger, ...next } });
  };
  /** Turning a group off takes the groups that depend on it with it. */
  const patchScope = (key: keyof SyncScope, enabled: boolean): void => {
    const scope: SyncScope = { ...draft.scope, [key]: enabled };
    if (!enabled)
      for (const [dependent, source] of Object.entries(scopeDependencies))
        if (source === key) scope[dependent as keyof SyncScope] = false;
    setDraft({ ...draft, scope });
  };
  // Arming a force push overwrites the remote, so the switch asks first;
  // disarming it takes nothing away.
  const patchForcePush = (armed: boolean): void => {
    if (armed) {
      setForcePushOpen(true);
      return;
    }
    patch({ force_push: false });
  };

  const save = async (): Promise<void> => {
    setBusy("save");
    setActionError(null);
    try {
      const next = await configureSync(draft);
      setStored(next);
      setDraft(next);
      notify.success(t("settings.sync.saved"));
      void getSyncStatus().then(setStatus, () => undefined);
    } catch (error) {
      setActionError(
        t("settings.sync.saveFailed", { error: messageOf(error, "") }),
      );
    } finally {
      setBusy(null);
    }
  };

  const syncNow = async (): Promise<void> => {
    setBusy("sync");
    setActionError(null);
    try {
      setStatus(await runSync());
      notify.success(t("settings.sync.syncDone"));
      // The run may have spent an armed force push, so the form reads the
      // stored flag again rather than showing one the next run will not use.
      if (!dirty) await reloadStored();
    } catch (error) {
      const message = messageOf(error, "");
      setActionError(
        message.includes("remote lock")
          ? t("settings.sync.syncBusy")
          : t("settings.sync.syncFailed", { error: message }),
      );
      void getSyncStatus().then(setStatus, () => undefined);
    } finally {
      setBusy(null);
    }
  };

  const savePassphrase = async (): Promise<void> => {
    setBusy("passphrase");
    setActionError(null);
    try {
      await setSyncPassphrase(passphrase);
      setPassphrase("");
      notify.success(t("settings.sync.passphraseSaved"));
      void getSyncStatus().then(setStatus, () => undefined);
    } catch (error) {
      setActionError(
        matchesStoredPassphraseError(error)
          ? t("settings.sync.passphraseMismatch")
          : messageOf(error, ""),
      );
    } finally {
      setBusy(null);
    }
  };

  const forgetPassphrase = async (): Promise<void> => {
    setBusy("passphrase");
    setActionError(null);
    try {
      await forgetSyncPassphrase();
      void getSyncStatus().then(setStatus, () => undefined);
    } catch (error) {
      setActionError(messageOf(error, ""));
    } finally {
      setBusy(null);
    }
  };

  const webdav = draft.remote.kind === "webdav";
  const s3 = draft.remote.kind === "s3";
  const busyAny = busy !== null;

  return (
    <div className="@container/sync grid gap-3 pb-2 pr-1">
      <div className="grid items-start gap-3 @[920px]/sync:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-3">
          <Panel className="min-w-0">
            <PanelHeader>
              <SectionKicker>{t("settings.sync.title")}</SectionKicker>
              <strong className="mt-1 block text-sm font-semibold tracking-tight">
                {t("settings.sync.remoteTitle")}
              </strong>
            </PanelHeader>

            <div className="border-b px-4 py-3">
              <RadioGroup
                aria-label={t("settings.sync.remoteKind")}
                className="grid grid-cols-2 gap-2"
                disabled={busyAny}
                onValueChange={(value) =>
                  patchRemoteKind(value as SyncConfig["remote"]["kind"])
                }
                value={draft.remote.kind}
              >
                <ChoiceCard
                  description={t("settings.sync.remoteKindHints.webdav")}
                  disabled={busyAny}
                  label={t("settings.sync.remoteKinds.webdav")}
                  selected={webdav}
                  value="webdav"
                />
                <ChoiceCard
                  description={t("settings.sync.remoteKindHints.s3")}
                  disabled={busyAny}
                  label={t("settings.sync.remoteKinds.s3")}
                  selected={s3}
                  value="s3"
                />
              </RadioGroup>
            </div>

            <div className="grid gap-3 border-b px-4 py-3">
              <Field
                hint={
                  s3 && draft.remote.addressing === "suffix"
                    ? t("settings.sync.endpointDomainHint")
                    : undefined
                }
                label={t("settings.sync.endpoint")}
              >
                <Input
                  onChange={(event) =>
                    patchRemote({ endpoint: event.target.value })
                  }
                  placeholder={t(
                    `settings.sync.endpoint${s3 ? "S3" : "Webdav"}Placeholder`,
                  )}
                  type={
                    s3 && draft.remote.addressing === "suffix" ? "text" : "url"
                  }
                  value={draft.remote.endpoint}
                />
              </Field>

              {webdav ? (
                <Field label={t("settings.sync.folder")}>
                  <Input
                    onChange={(event) =>
                      patchRemote({ path: event.target.value })
                    }
                    value={draft.remote.path}
                  />
                </Field>
              ) : null}

              {s3 ? (
                <>
                  <div className="grid grid-cols-2 gap-3 max-[560px]:grid-cols-1">
                    <Field label={t("settings.sync.bucket")}>
                      <Input
                        onChange={(event) =>
                          patchRemote({ path: event.target.value })
                        }
                        value={draft.remote.path}
                      />
                    </Field>
                    <Field label={t("settings.sync.region")}>
                      <Input
                        onChange={(event) =>
                          patchRemote({ region: event.target.value })
                        }
                        placeholder="us-east-1"
                        value={draft.remote.region}
                      />
                    </Field>
                  </div>
                  <Field label={t("settings.sync.addressing")}>
                    <RadioGroup
                      aria-label={t("settings.sync.addressing")}
                      className="grid grid-cols-2 gap-2"
                      disabled={busyAny}
                      onValueChange={(value) =>
                        patchRemote({ addressing: value as SyncS3Addressing })
                      }
                      value={draft.remote.addressing}
                    >
                      {syncS3Addressings.map((addressing) => (
                        <ChoiceCard
                          description={t(
                            `settings.sync.addressingHints.${addressing}`,
                          )}
                          disabled={busyAny}
                          key={addressing}
                          label={t(`settings.sync.addressings.${addressing}`)}
                          selected={draft.remote.addressing === addressing}
                          value={addressing}
                        />
                      ))}
                    </RadioGroup>
                  </Field>
                </>
              ) : null}

              {webdav ? (
                <>
                  <Field label={t("settings.sync.username")}>
                    <Input
                      onChange={(event) =>
                        patchRemote({ username: event.target.value })
                      }
                      value={draft.remote.username}
                    />
                  </Field>
                  <Field
                    hint={
                      draft.remote_password_set
                        ? t("settings.sync.secretSaved")
                        : undefined
                    }
                    label={t("settings.sync.password")}
                  >
                    <Input
                      onChange={(event) =>
                        patchRemote({ password: event.target.value })
                      }
                      type="password"
                      value={draft.remote.password}
                    />
                  </Field>
                </>
              ) : (
                <>
                  <Field label={t("settings.sync.accessKey")}>
                    <Input
                      onChange={(event) =>
                        patchRemote({ access_key: event.target.value })
                      }
                      value={draft.remote.access_key}
                    />
                  </Field>
                  <Field
                    hint={
                      draft.remote_secret_key_set
                        ? t("settings.sync.secretSaved")
                        : undefined
                    }
                    label={t("settings.sync.secretKey")}
                  >
                    <Input
                      onChange={(event) =>
                        patchRemote({ secret_key: event.target.value })
                      }
                      type="password"
                      value={draft.remote.secret_key}
                    />
                  </Field>
                </>
              )}
            </div>

            <DataRow>
              <Label
                className="min-w-0 flex-1 cursor-pointer text-sm font-normal"
                htmlFor="sync-enabled"
              >
                {t("settings.sync.on")}
                <span className="mt-0.5 block text-xs text-muted-foreground">
                  {t("settings.sync.onHint")}
                </span>
              </Label>
              <Switch
                checked={draft.enabled}
                className="shrink-0"
                disabled={busyAny}
                id="sync-enabled"
                onCheckedChange={(enabled) => patch({ enabled })}
              />
            </DataRow>
          </Panel>

          <Panel className="min-w-0">
            <PanelHeader>
              <SectionKicker>{t("settings.sync.title")}</SectionKicker>
              <strong className="mt-1 block text-sm font-semibold tracking-tight">
                {t("settings.sync.deviceTitle")}
              </strong>
            </PanelHeader>

            <div className="grid gap-3 px-4 py-3">
              <Field
                hint={t("settings.sync.deviceNameHint")}
                label={t("settings.sync.deviceName")}
              >
                <Input
                  maxLength={maxSyncDeviceNameLength}
                  onChange={(event) =>
                    patch({ device_name: event.target.value })
                  }
                  placeholder={status?.host_name ?? ""}
                  value={draft.device_name}
                />
              </Field>
            </div>
          </Panel>

          <Panel className="min-w-0">
            <PanelHeader>
              <SectionKicker>{t("settings.sync.title")}</SectionKicker>
              <strong className="mt-1 block text-sm font-semibold tracking-tight">
                {t("settings.sync.scheduleTitle")}
              </strong>
            </PanelHeader>

            <div className="grid gap-2 border-b px-4 py-3">
              <span className="text-xs font-medium text-text-secondary">
                {t("settings.sync.trigger")}
              </span>
              <RadioGroup
                aria-label={t("settings.sync.trigger")}
                className="grid grid-cols-3 gap-2 max-[560px]:grid-cols-1"
                disabled={busyAny}
                onValueChange={(value) =>
                  patchTrigger({ mode: value as SyncTriggerMode })
                }
                value={draft.trigger.mode}
              >
                {syncTriggerModes.map((mode) => (
                  <ChoiceCard
                    description={t(`settings.sync.triggerHints.${mode}`)}
                    disabled={busyAny}
                    key={mode}
                    label={t(`settings.sync.triggers.${mode}`)}
                    selected={draft.trigger.mode === mode}
                    value={mode}
                  />
                ))}
              </RadioGroup>
            </div>

            <div className="grid grid-cols-2 gap-3 border-b px-4 py-3 max-[560px]:grid-cols-1">
              {draft.trigger.mode === "interval" ? (
                <Field
                  hint={t("settings.sync.intervalHint", {
                    count: minSyncIntervalMinutes,
                  })}
                  label={t("settings.sync.intervalField")}
                >
                  <Input
                    className="font-mono tabular-nums"
                    min={minSyncIntervalMinutes}
                    onChange={(event) =>
                      patchTrigger({
                        interval_minutes: Number(event.target.value),
                      })
                    }
                    type="number"
                    value={draft.trigger.interval_minutes}
                  />
                </Field>
              ) : null}

              {draft.trigger.mode === "cron" ? (
                <Field
                  hint={t("settings.sync.cronHint")}
                  label={t("settings.sync.cronField")}
                >
                  <Input
                    className="font-mono"
                    onChange={(event) =>
                      patchTrigger({ cron: event.target.value })
                    }
                    placeholder="0 */6 * * *"
                    value={draft.trigger.cron}
                  />
                </Field>
              ) : null}

              <Field
                hint={t("settings.sync.debounceHint", {
                  min: minSyncDebounceSeconds,
                  max: maxSyncDebounceSeconds,
                })}
                label={t("settings.sync.debounceField")}
              >
                <Input
                  className="font-mono tabular-nums"
                  max={maxSyncDebounceSeconds}
                  min={0}
                  onChange={(event) =>
                    patchTrigger({
                      debounce_seconds: Number(event.target.value),
                    })
                  }
                  type="number"
                  value={draft.trigger.debounce_seconds}
                />
              </Field>
            </div>

            <div className="grid gap-2 px-4 py-3">
              <span className="text-xs font-medium text-text-secondary">
                {t("settings.sync.scopeTitle")}
              </span>
              <p className="text-xs text-muted-foreground">
                {t("settings.sync.scopeHint")}
              </p>
              <div className="grid gap-2">
                {scopeKeys.map((key) => {
                  const source = scopeDependencies[key];
                  const locked = source !== undefined && !draft.scope[source];
                  return (
                    <DataRow
                      className={cn(
                        "rounded-sm border px-3",
                        locked && "opacity-60",
                      )}
                      key={key}
                    >
                      <Label
                        className="min-w-0 flex-1 cursor-pointer text-sm font-normal"
                        htmlFor={`sync-scope-${key}`}
                      >
                        {t(`settings.sync.scope.${key}`)}
                        <span className="mt-0.5 block text-xs text-muted-foreground">
                          {locked
                            ? t("settings.sync.scopeNeedsServices")
                            : t(`settings.sync.scope.${key}Hint`)}
                        </span>
                      </Label>
                      <Switch
                        checked={draft.scope[key]}
                        className="shrink-0"
                        disabled={busyAny || locked}
                        id={`sync-scope-${key}`}
                        onCheckedChange={(checked) => patchScope(key, checked)}
                      />
                    </DataRow>
                  );
                })}
              </div>

              {/* An armed force push is cleared on save while sync is off, so
                  the switch stays locked until sync is on. */}
              <DataRow className="rounded-sm border px-3">
                <Label
                  className="min-w-0 flex-1 cursor-pointer text-sm font-normal"
                  htmlFor="sync-force-push"
                >
                  {t("settings.sync.forcePush")}
                  <span
                    className={cn(
                      "mt-0.5 block text-xs",
                      draft.force_push
                        ? "text-warning-foreground"
                        : "text-muted-foreground",
                    )}
                  >
                    {draft.force_push
                      ? t("settings.sync.forcePushArmedHint")
                      : t("settings.sync.forcePushHint")}
                  </span>
                </Label>
                <Switch
                  checked={draft.force_push}
                  className="shrink-0"
                  disabled={busyAny || !draft.enabled}
                  id="sync-force-push"
                  onCheckedChange={patchForcePush}
                />
              </DataRow>
            </div>
          </Panel>
        </div>

        <div className="flex min-w-0 flex-col gap-3">
          <Panel className="min-w-0">
            <PanelHeader>
              <SectionKicker>{t("settings.sync.title")}</SectionKicker>
              <strong className="mt-1 block text-sm font-semibold tracking-tight">
                {t("settings.sync.passphraseTitle")}
              </strong>
              <p className="mt-1 text-xs text-text-secondary">
                {t("settings.sync.passphraseHint")}
              </p>
            </PanelHeader>

            <div className="flex items-center gap-2.5 border-b px-4 py-3">
              <StatusDot
                tone={status?.passphrase_set ? "positive" : "neutral"}
              />
              <strong className="text-sm font-medium">
                {status?.passphrase_set
                  ? t("settings.sync.passphraseSet")
                  : t("settings.sync.passphraseMissing")}
              </strong>
            </div>

            <div className="grid gap-3 px-4 py-3">
              <Field
                hint={
                  passphraseTooShort
                    ? t("settings.sync.passphraseTooShort", {
                        count: minSyncPassphraseLength,
                      })
                    : undefined
                }
                htmlFor={passphraseId}
                label={t("settings.sync.passphraseField")}
              >
                <Input
                  aria-invalid={passphraseTooShort}
                  id={passphraseId}
                  onChange={(event) => setPassphrase(event.target.value)}
                  placeholder={t("settings.sync.passphrasePlaceholder", {
                    count: minSyncPassphraseLength,
                  })}
                  type="password"
                  value={passphrase}
                />
              </Field>
              <ActionGroup className="justify-start">
                <Button
                  disabled={busyAny || passphraseTooShort || passphrase === ""}
                  onClick={() => void savePassphrase()}
                  type="button"
                >
                  {busy === "passphrase"
                    ? t("settings.sync.passphraseSaving")
                    : t("settings.sync.passphraseSave")}
                </Button>
                <Button
                  disabled={busyAny || !status?.passphrase_set}
                  onClick={() => setForgetOpen(true)}
                  type="button"
                  variant="outline"
                >
                  {t("settings.sync.passphraseForget")}
                </Button>
              </ActionGroup>
            </div>
          </Panel>

          <Panel className="min-w-0">
            <PanelHeader>
              <SectionKicker>{t("settings.sync.title")}</SectionKicker>
              <strong className="mt-1 block text-sm font-semibold tracking-tight">
                {t("settings.sync.statusTitle")}
              </strong>
            </PanelHeader>

            <div className="flex items-center gap-2.5 border-b px-4 py-3">
              <StatusDot tone={statusTone(status)} />
              <strong className="text-sm font-medium">
                {statusLabel(status)}
              </strong>
              <ActionGroup className="gap-1.5">
                <Button
                  disabled={busyAny || !status?.configured}
                  onClick={() => void syncNow()}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {busy === "sync"
                    ? t("settings.sync.syncing")
                    : t("settings.sync.syncNow")}
                </Button>
              </ActionGroup>
            </div>

            {status?.force_push_armed ? (
              <div className="border-b px-4 py-3">
                <FormMessage tone="warning">
                  {t("settings.sync.forcePushArmed")}
                </FormMessage>
              </div>
            ) : status?.first_sync_pending ? (
              <div className="border-b px-4 py-3">
                <FormMessage tone="notice">
                  {t("settings.sync.firstSyncPending")}
                </FormMessage>
              </div>
            ) : null}

            <DataRow>
              <span className="min-w-0 flex-1 text-xs text-text-secondary">
                {t("settings.sync.device")}
              </span>
              <code className="min-w-0 font-mono text-xs [overflow-wrap:anywhere]">
                {status?.device_name || status?.host_name || "—"}
              </code>
            </DataRow>
            <DataRow>
              <span className="min-w-0 flex-1 text-xs text-text-secondary">
                {t("settings.sync.lastSynced")}
              </span>
              <span className="text-xs">
                {timestampLabel(status?.last_synced_at)}
              </span>
            </DataRow>
            <DataRow>
              <span className="min-w-0 flex-1 text-xs text-text-secondary">
                {t("settings.sync.nextRun")}
              </span>
              <span className="text-xs">
                {timestampLabel(status?.next_run_at)}
              </span>
            </DataRow>
            <DataRow>
              <span className="min-w-0 flex-1 text-xs text-text-secondary">
                {t("settings.sync.pendingChanges_other", {
                  count: status?.pending_changes ?? 0,
                })}
              </span>
              <span className="font-mono text-xs tabular-nums">
                {status?.pending_changes ?? 0}
              </span>
            </DataRow>

            {status?.last_error ? (
              <div className="px-4 py-3">
                <FormMessage tone="error">
                  {t("settings.sync.lastError")}: {status.last_error}
                </FormMessage>
              </div>
            ) : null}
          </Panel>
        </div>
      </div>

      <Panel className="min-w-0">
        <PanelHeader>
          <SectionKicker>{t("settings.sync.title")}</SectionKicker>
          <strong className="mt-1 block text-sm font-semibold tracking-tight">
            {t("settings.sync.conflictsTitle")}
          </strong>
          <p className="mt-1 text-xs text-text-secondary">
            {t("settings.sync.conflictsHint")}
          </p>
        </PanelHeader>
        <div className="px-4 py-3">
          {(status?.conflicts.length ?? 0) === 0 ? (
            <p className="text-xs text-muted-foreground">
              {t("settings.sync.conflictsEmpty")}
            </p>
          ) : (
            <ul className="grid gap-1.5">
              {status?.conflicts.map((conflict, index) => (
                <li
                  className="flex min-w-0 flex-wrap items-center gap-2 text-xs"
                  key={`${conflict.kind}-${conflict.name}-${conflict.at}-${index}`}
                >
                  <span className="text-muted-foreground">
                    {t(`settings.sync.kind.${conflict.kind}`, {
                      defaultValue: conflict.kind,
                    })}
                  </span>
                  <code className="min-w-0 font-mono [overflow-wrap:anywhere]">
                    {conflict.name}
                  </code>
                  <span className="text-muted-foreground">
                    {t(`settings.sync.resolution.${conflict.resolution}`, {
                      defaultValue: conflict.resolution,
                    })}
                  </span>
                  <span className="ml-auto text-muted-foreground">
                    {timestampLabel(conflict.at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Panel>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p
          className={cn(
            "min-w-0 flex-1 text-xs",
            issue || dirty
              ? "text-warning-foreground"
              : "text-muted-foreground",
          )}
        >
          {issue
            ? t(`settings.sync.issues.${issue}`, {
                count: minSyncIntervalMinutes,
                min: minSyncDebounceSeconds,
                max: maxSyncDebounceSeconds,
              })
            : dirty
              ? t("settings.sync.dirty")
              : ""}
        </p>
        {actionError ? (
          <FormMessage className="max-w-[52ch]" tone="error">
            {actionError}
          </FormMessage>
        ) : null}
        <ActionGroup className="shrink-0">
          <Button
            disabled={!dirty || busyAny}
            onClick={() => setDraft(stored)}
            type="button"
            variant="outline"
          >
            {t("settings.sync.discard")}
          </Button>
          <Button
            disabled={!dirty || issue !== null || busyAny}
            onClick={() => void save()}
            type="button"
          >
            {busy === "save"
              ? t("settings.sync.saving")
              : t("settings.sync.save")}
          </Button>
        </ActionGroup>
      </div>

      <ConfirmDialog
        confirmLabel={t("settings.sync.passphraseForgetConfirm")}
        description={<p>{t("settings.sync.passphraseForgetBody")}</p>}
        onCancel={() => setForgetOpen(false)}
        onConfirm={() => {
          setForgetOpen(false);
          void forgetPassphrase();
        }}
        open={forgetOpen}
        title={t("settings.sync.passphraseForgetTitle")}
        tone="warning"
      />

      <ConfirmDialog
        confirmLabel={t("settings.sync.forcePushConfirm")}
        description={<p>{t("settings.sync.forcePushBody")}</p>}
        onCancel={() => setForcePushOpen(false)}
        onConfirm={() => {
          setForcePushOpen(false);
          patch({ force_push: true });
        }}
        open={forcePushOpen}
        title={t("settings.sync.forcePushTitle")}
        tone="warning"
      />
    </div>
  );
}
