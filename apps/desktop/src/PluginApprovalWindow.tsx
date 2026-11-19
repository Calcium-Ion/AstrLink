import { useCallback, useEffect, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { LoadingState } from "@/components/LoadingState";
import { PluginIcon } from "@/components/PluginIcon";
import { Button } from "@/components/ui/button";

import {
  decidePluginApproval,
  getPlugin,
  listenPluginApprovalChanged,
  pendingPluginApproval,
} from "./bridge";
import { useT } from "./i18n";
import { PluginApprovalSummary } from "./PluginApprovalSummary";
import { PluginInstallReviewView } from "./PluginInstallReview";
import type { PendingPluginApproval, PluginDetail } from "./plugin-model";

/**
 * The one place a change that grants a plugin more is confirmed (plugin
 * platform v2, §2.4): enabling it, capabilities, addresses, plain HTTP, the
 * privacy check skipped, and new code, as an install from source, an update
 * or a rollback puts in place. The host opens this window without focus when the
 * main window asks for such a change; plugin interface code runs in the
 * main window and can click nothing here. There is no form, so Enter
 * confirms nothing; closing the window decides nothing.
 */
export function PluginApprovalWindow() {
  const t = useT();
  const [pending, setPending] = useState<PendingPluginApproval | null>(null);
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [kept, setKept] = useState<string[]>([]);
  const [deciding, setDeciding] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const next = await pendingPluginApproval();
      setPending(next);
      setKept(next?.approval.added_capabilities ?? []);
      setError(null);
      setDetail(null);
      if (next) {
        void getPlugin(next.key)
          .then(setDetail)
          .catch(() => setDetail(null));
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    let unlisten: (() => void) | undefined;
    let stopped = false;
    void listenPluginApprovalChanged(() => void load()).then((stop) => {
      if (stopped) stop();
      else unlisten = stop;
    });
    return () => {
      stopped = true;
      unlisten?.();
    };
  }, [load]);

  async function decide(approve: boolean) {
    if (!pending) return;
    setDeciding(true);
    setError(null);
    try {
      await decidePluginApproval(
        pending.id,
        approve,
        approve && !pending.approval.install ? kept : undefined,
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setDeciding(false);
    }
  }

  // Below the window's own title bar; what it asks scrolls between its
  // title and its buttons, which stay in view.
  const frame =
    "flex h-dvh min-h-0 flex-col overflow-hidden pt-[var(--window-chrome-height)]";
  if (loading)
    return (
      <main className={frame}>
        <LoadingState
          className="flex-1"
          label={t("plugins.approval.loading")}
        />
      </main>
    );
  if (!pending)
    return (
      <main
        className={`${frame} gap-3 px-4 pb-4`}
        data-testid="plugin-approval-window"
      >
        <EmptyState className="flex-1" title={t("plugins.approval.nothing")} />
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      </main>
    );
  const { approval } = pending;
  const install = approval.install;
  const update =
    install?.replaces?.origin === "source" &&
    install.replaces.repository === install.source.repository;
  const title = install
    ? approval.rollback
      ? t("plugins.install.rollbackTitle", { name: approval.name })
      : update
        ? t("plugins.install.updateTitle", { name: approval.name })
        : t("plugins.install.installTitle", { name: approval.name })
    : approval.enable
      ? t("plugins.approval.enableTitle", { name: approval.name })
      : t("plugins.approval.grantTitle", { name: approval.name });
  const confirm = install
    ? approval.rollback
      ? t("plugins.install.rollbackTo")
      : update
        ? t("plugins.install.update")
        : t("plugins.install.install")
    : approval.enable
      ? t("plugins.approval.confirmEnable")
      : t("plugins.approval.confirm");
  return (
    <main className={frame} data-testid="plugin-approval-window">
      <header className="flex shrink-0 items-center gap-2 px-4 pt-2 pb-3">
        {/* An install's review shows the plugin's own icon and name. */}
        {install ? null : <PluginIcon icon={detail?.icon} />}
        <h1 className="min-w-0 truncate text-sm font-semibold">{title}</h1>
      </header>
      <div
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pb-4"
        data-slot="plugin-approval-body"
      >
        {install ? (
          <PluginInstallReviewView
            review={install}
            rollback={approval.rollback}
          />
        ) : (
          <PluginApprovalSummary
            approval={approval}
            detail={detail}
            kept={kept}
            onKept={setKept}
          />
        )}
      </div>
      <footer
        className="grid shrink-0 gap-2 border-t bg-muted/40 px-4 py-3"
        data-slot="plugin-approval-decision"
      >
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
        <div className="flex justify-end gap-2">
          <Button
            disabled={deciding}
            onClick={() => void decide(false)}
            size="sm"
            type="button"
            variant="outline"
          >
            {t("plugins.approval.deny")}
          </Button>
          <Button
            disabled={deciding}
            onClick={() => void decide(true)}
            size="sm"
            type="button"
          >
            {confirm}
          </Button>
        </div>
      </footer>
    </main>
  );
}
