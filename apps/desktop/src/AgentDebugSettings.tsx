import { useEffect, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";

import {
  getAgentDebugStatus,
  installAgentDebug,
  uninstallAgentDebug,
} from "./bridge";
import type { AgentInstallStatus, AgentToolId } from "./agent-install-model";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : i18n.t("agentDebug.failed");
}

function toolName(id: AgentToolId): string {
  return i18n.t(`agentDebug.tools.${id}`);
}

function toolState(status: AgentInstallStatus, id: AgentToolId): string {
  const tool = status.tools.find((item) => item.id === id);
  if (!tool?.detected) return i18n.t("agentDebug.notDetected");
  if (tool.skill_installed && tool.mcp_installed) {
    return i18n.t("agentDebug.installed");
  }
  if (tool.skill_installed || tool.mcp_installed) {
    return i18n.t("agentDebug.partial");
  }
  return i18n.t("agentDebug.detected");
}

function toolTone(
  status: AgentInstallStatus,
  id: AgentToolId,
): "positive" | "pending" | "neutral" {
  const tool = status.tools.find((item) => item.id === id);
  if (tool?.skill_installed && tool.mcp_installed) return "positive";
  if (tool?.detected) return "pending";
  return "neutral";
}

export function AgentDebugSettings() {
  const t = useT();
  const [status, setStatus] = useState<AgentInstallStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"install" | "uninstall" | null>(null);
  const [confirm, setConfirm] = useState<"install" | "uninstall" | null>(null);

  const refresh = async (): Promise<void> => {
    try {
      setStatus(await getAgentDebugStatus());
      setError(null);
    } catch (next) {
      setError(messageOf(next));
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const runInstall = async (): Promise<void> => {
    setBusy("install");
    setConfirm(null);
    try {
      await installAgentDebug();
      await refresh();
      notify.success(i18n.t("agentDebug.notifyInstalled"));
    } catch (next) {
      setError(messageOf(next));
    } finally {
      setBusy(null);
    }
  };

  const runUninstall = async (): Promise<void> => {
    setBusy("uninstall");
    setConfirm(null);
    try {
      await uninstallAgentDebug();
      await refresh();
      notify.success(i18n.t("agentDebug.notifyRemoved"));
    } catch (next) {
      setError(messageOf(next));
    } finally {
      setBusy(null);
    }
  };

  const anyInstalled =
    status?.canonical_skill ||
    status?.tools.some((tool) => tool.skill_installed || tool.mcp_installed);

  return (
    <section className="grid gap-4 pb-2">
      <PageHeader
        description={t("agentDebug.description")}
        title={t("agentDebug.title")}
      />

      <Panel className="min-w-0">
        <PanelHeader>
          <strong className="block text-sm font-semibold tracking-tight">
            {t("agentDebug.panelTitle")}
          </strong>
        </PanelHeader>

        {error ? <FormMessage tone="error">{error}</FormMessage> : null}

        <div className="grid gap-0">
          {(["cursor", "claude", "codex"] as const).map((id) => (
            <DataRow key={id}>
              <StatusDot tone={status ? toolTone(status, id) : "neutral"} />
              <div className="grid min-w-0 flex-1 gap-0.5">
                <strong className="text-sm font-medium">{toolName(id)}</strong>
                <span className="text-xs text-text-secondary">
                  {status ? toolState(status, id) : t("common.checking")}
                </span>
              </div>
            </DataRow>
          ))}
        </div>

        <p className="px-4 py-2.5 text-xs text-muted-foreground">
          {t("agentDebug.restartHint")}
        </p>

        <div className="flex flex-wrap items-center gap-2 px-4 py-3">
          <Button
            disabled={busy !== null}
            onClick={() => setConfirm("install")}
            type="button"
          >
            {busy === "install"
              ? t("agentDebug.installing")
              : t("agentDebug.install")}
          </Button>
          <Button
            disabled={busy !== null || !anyInstalled}
            onClick={() => setConfirm("uninstall")}
            type="button"
            variant="outline"
          >
            {busy === "uninstall"
              ? t("agentDebug.removing")
              : t("agentDebug.remove")}
          </Button>
        </div>
      </Panel>

      <ConfirmDialog
        confirmLabel={
          confirm === "uninstall" ? t("agentDebug.remove") : t("agentDebug.install")
        }
        description={
          confirm === "uninstall" ? (
            <p>{t("agentDebug.removeBody")}</p>
          ) : (
            <div className="grid gap-2">
              <p>{t("agentDebug.installBody")}</p>
              {status?.preview_paths.length ? (
                <ul className="list-disc pl-4 font-mono text-xs text-text-secondary">
                  {status.preview_paths.map((path) => (
                    <li key={path} className="[overflow-wrap:anywhere]">
                      {path}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          )
        }
        destructive={confirm === "uninstall"}
        disabled={busy !== null}
        onCancel={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm === "uninstall") void runUninstall();
          else void runInstall();
        }}
        open={confirm !== null}
        title={
          confirm === "uninstall"
            ? t("agentDebug.removeTitle")
            : t("agentDebug.installTitle")
        }
      />
    </section>
  );
}
