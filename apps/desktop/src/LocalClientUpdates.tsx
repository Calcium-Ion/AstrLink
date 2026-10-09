import { useEffect, useId, useState } from "react";
import { isTauri } from "@tauri-apps/api/core";
import { Panel, PanelBody, PanelHeader } from "@/components/Panel";
import { DataField } from "@/components/DataRow";
import { AgentToolIcon } from "@/components/AgentToolIcon";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormMessage } from "@/components/FormMessage";
import { ArrowUpRight, ChevronDown, RefreshCw } from "@/components/icons";
import { cn } from "@/lib/utils";
import { openExternalURL } from "./bridge";
import { useT } from "./i18n";
import {
  getLocalClients,
  listenLocalClients,
  refreshLocalClients,
  updateLocalClients,
} from "./local-client-bridge";
import {
  acceptLocalClients,
  emptyLocalClients,
  localClientGuide,
  localClientLabel,
  type LocalClientId,
  type LocalClientSnapshot,
} from "./local-client-model";

const ERROR_KEYS = new Set([
  "network",
  "proxy",
  "rate_limit",
  "version",
  "unsupported",
  "command",
  "timeout",
  "changed",
  "unchanged",
  "damaged",
]);

export function LocalClientUpdates({ compact = false }: { compact?: boolean }) {
  const t = useT();
  const id = useId();
  const native = isTauri();
  const [snapshot, setSnapshot] = useState(emptyLocalClients);
  const [open, setOpen] = useState<boolean | null>(null);
  const expanded = open ?? !compact;
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const busy = pending || snapshot.busy;
  const available = snapshot.clients
    .filter((c) => c.can_update)
    .map((c) => c.id);

  useEffect(() => {
    let cancelled = false;
    let stop: (() => void) | undefined;
    const accept = (next: LocalClientSnapshot) => {
      if (!cancelled)
        setSnapshot((current) => acceptLocalClients(current, next));
    };
    // Subscribe before reading. Tasks remain owned by Rust after unmount.
    void listenLocalClients(accept)
      .then(async (unlisten) => {
        if (cancelled) {
          unlisten();
          return;
        }
        stop = unlisten;
        const next = await getLocalClients();
        accept(next);
        if (
          !cancelled &&
          native &&
          !next.busy &&
          next.clients.every((c) => c.phase === "idle")
        )
          accept(await refreshLocalClients());
      })
      .catch((e) => {
        if (!cancelled) setError(String(e));
      });
    return () => {
      cancelled = true;
      stop?.();
    };
  }, [native]);

  async function run(action: () => Promise<LocalClientSnapshot>) {
    if (busy) return;
    setPending(true);
    setError(null);
    try {
      const next = await action();
      setSnapshot((current) => acceptLocalClients(current, next));
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setPending(false);
    }
  }
  async function guide(client: LocalClientId) {
    try {
      await openExternalURL(localClientGuide(client));
    } catch (error) {
      setError(String(error));
    }
  }

  return (
    <Panel className="shrink-0" data-slot="local-clients">
      <PanelHeader
        size="sm"
        className={cn("px-3 py-2", !expanded && "border-b-0")}
        actions={
          expanded ? (
            <>
              <Button
                variant="ghost"
                size="sm"
                disabled={!native || busy}
                onClick={() => void run(refreshLocalClients)}
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn(
                    snapshot.busy &&
                      !snapshot.clients.some((c) => c.phase === "updating") &&
                      "motion-safe:animate-spin",
                  )}
                />
                {t("localClients.refresh")}
              </Button>
              {available.length > 1 ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  onClick={() => void run(() => updateLocalClients(available))}
                >
                  {t("localClients.updateAll", { count: available.length })}
                </Button>
              ) : null}
            </>
          ) : undefined
        }
      >
        <Button
          variant="ghost"
          size="sm"
          className="-ml-1 gap-2 px-1 font-medium"
          aria-expanded={expanded}
          aria-controls={`${id}-clients`}
          onClick={() => setOpen(!expanded)}
        >
          {t("localClients.title")}
          {!expanded && available.length > 0 ? (
            <Badge variant="secondary">{available.length}</Badge>
          ) : null}
          <ChevronDown
            aria-hidden="true"
            className={cn(
              "text-muted-foreground transition-transform",
              expanded && "rotate-180",
            )}
          />
        </Button>
      </PanelHeader>
      {expanded ? (
        <PanelBody className="flex-none space-y-3 p-3" id={`${id}-clients`}>
          {!native ? (
            <p className="text-xs text-muted-foreground">
              {t("localClients.browser")}
            </p>
          ) : null}
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <div className="grid gap-3 @min-[600px]/workspace-surface:grid-cols-2">
            {snapshot.clients.map((client) => (
              <Panel
                key={client.id}
                className="flex min-w-0 flex-col gap-3 p-3"
                data-client={client.id}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <AgentToolIcon id={client.id} size={20} />
                  <h3 className="text-sm font-medium">
                    {localClientLabel(client.id)}
                  </h3>
                  <StatusBadge
                    className="ml-auto"
                    tone={
                      client.phase === "available"
                        ? "pending"
                        : client.phase === "error"
                          ? "negative"
                          : ["updated", "up_to_date"].includes(client.phase)
                            ? "positive"
                            : "neutral"
                    }
                  >
                    {t(`localClients.phase.${client.phase}`)}
                  </StatusBadge>
                </div>
                <dl className="space-y-1.5 text-xs">
                  <div className="flex justify-between gap-2">
                    <dt className="text-muted-foreground">
                      {t("localClients.current")}
                    </dt>
                    <dd className="break-all text-right font-mono">
                      {client.current_version ?? "—"}
                    </dd>
                  </div>
                  <div className="flex justify-between gap-2">
                    <dt className="text-muted-foreground">
                      {t("localClients.latest")}
                    </dt>
                    <dd className="break-all text-right font-mono">
                      {client.latest_version ?? "—"}
                    </dd>
                  </div>
                </dl>
                {client.phase === "error" ? (
                  <p
                    role="alert"
                    className="text-xs leading-relaxed text-danger-foreground"
                  >
                    {t(
                      `localClients.errors.${ERROR_KEYS.has(client.error_code ?? "") ? client.error_code : "command"}`,
                    )}
                  </p>
                ) : null}
                {client.phase === "updated" ? (
                  <p className="text-xs text-muted-foreground">
                    {t("localClients.reopen")}
                  </p>
                ) : null}
                <div className="mt-auto flex items-start justify-between gap-2">
                  {client.executable || client.error_detail ? (
                    <details className="min-w-0 flex-1 pt-1.5 text-xs text-muted-foreground">
                      <summary
                        className="w-fit cursor-pointer"
                        aria-label={t("localClients.details")}
                      >
                        {t("localClients.details")}
                      </summary>
                      <div className="mt-3 space-y-3">
                        {client.executable ? (
                          <DataField
                            label={t("localClients.installationPath")}
                            value={
                              <span className="break-all font-mono text-xs">
                                {client.executable}
                              </span>
                            }
                          />
                        ) : null}
                        {client.install_method !== "unknown" ? (
                          <DataField
                            label={t("localClients.installationMethod")}
                            value={t(
                              `localClients.method.${client.install_method}`,
                            )}
                          />
                        ) : null}
                        {client.other_installations.length > 0 ? (
                          <>
                            <p className="break-normal">
                              {t("localClients.multiple")}
                            </p>
                            {client.other_installations.map((path) => (
                              <p key={path} className="break-all font-mono">
                                {path}
                              </p>
                            ))}
                          </>
                        ) : null}
                        {client.error_detail ? (
                          <DataField
                            label={t("localClients.errorDetails")}
                            value={
                              <span className="whitespace-pre-wrap break-all text-xs">
                                {client.error_detail}
                              </span>
                            }
                          />
                        ) : null}
                      </div>
                    </details>
                  ) : (
                    <span />
                  )}
                  {client.can_update ? (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      aria-label={t("localClients.updateNamed", {
                        name: localClientLabel(client.id),
                      })}
                      onClick={() =>
                        void run(() => updateLocalClients([client.id]))
                      }
                    >
                      {t("localClients.update")}
                    </Button>
                  ) : client.phase === "updating" ? (
                    <Button variant="outline" size="sm" disabled>
                      <RefreshCw
                        aria-hidden="true"
                        className="motion-safe:animate-spin"
                      />
                      {t("localClients.phase.updating")}
                    </Button>
                  ) : client.phase === "error" ? (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      onClick={() => void run(refreshLocalClients)}
                    >
                      {t("localClients.retry")}
                    </Button>
                  ) : ["not_installed", "manual"].includes(client.phase) ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void guide(client.id)}
                    >
                      {t(
                        client.phase === "not_installed"
                          ? "localClients.installGuide"
                          : "localClients.manualGuide",
                      )}
                      <ArrowUpRight aria-hidden="true" />
                    </Button>
                  ) : null}
                </div>
              </Panel>
            ))}
          </div>
        </PanelBody>
      ) : null}
    </Panel>
  );
}
