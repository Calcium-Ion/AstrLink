import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { useEffect, useRef, useState } from "react";

import { AgentToolIcon } from "@/components/AgentToolIcon";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CopyableValue } from "@/components/CopyableValue";
import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { RefreshCw, ShieldCheck } from "@/components/icons";
import { Panel, PanelFooter, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

import {
  getAgentDebugStatus,
  installAgentDebug,
  uninstallAgentDebug,
} from "./bridge";
import type { AgentInstallStatus, AgentToolId } from "./agent-install-model";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";

const toolIds = ["cursor", "claude", "codex", "grok"] as const;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : i18n.t("agentDebug.failed");
}

export function AgentDebugSettings() {
  const t = useT();
  const [status, setStatus] = useWorkspaceSnapshot<AgentInstallStatus | null>(
    "agent-tools",
    null,
    "desktop",
  );
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(status === null);
  const [busy, setBusy] = useState<"install" | "uninstall" | null>(null);
  const [confirm, setConfirm] = useState<"install" | "uninstall" | null>(null);
  const [selectedTools, setSelectedTools] = useState<AgentToolId[]>([]);

  const refreshGeneration = useRef(0);
  const refresh = async (
    background = false,
    isCurrent = () => true,
  ): Promise<boolean> => {
    const generation = ++refreshGeneration.current;
    const current = () =>
      isCurrent() && refreshGeneration.current === generation;
    if (!background || status === null) setChecking(true);
    try {
      const next = await getAgentDebugStatus();
      if (!current()) return false;
      setStatus(next);
      setError(null);
      return true;
    } catch (next) {
      if (current()) setError(messageOf(next));
      return false;
    } finally {
      if (current()) setChecking(false);
    }
  };

  useEffect(() => {
    let active = true;
    void refresh(true, () => active);
    return () => {
      active = false;
    };
  }, []);

  const run = async (operation: "install" | "uninstall"): Promise<void> => {
    if (operation === "install" && selectedTools.length === 0) return;
    refreshGeneration.current += 1;
    setBusy(operation);
    setConfirm(null);
    setError(null);
    try {
      if (operation === "install") await installAgentDebug(selectedTools);
      else await uninstallAgentDebug();
      if (await refresh()) {
        notify.success(
          i18n.t(
            operation === "install"
              ? "agentDebug.notifyInstalled"
              : "agentDebug.notifyRemoved",
          ),
        );
      }
    } catch (next) {
      setError(messageOf(next));
    } finally {
      setBusy(null);
    }
  };

  const detected = status?.tools.filter((tool) => tool.detected) ?? [];
  const configured = detected.filter(
    (tool) => tool.skill_installed && tool.mcp_installed,
  );
  const anyInstalled = Boolean(
    status?.canonical_skill ||
      status?.mcp_binary ||
      status?.tools.some((tool) => tool.skill_installed || tool.mcp_installed),
  );
  const installLabel = t(
    anyInstalled ? "agentDebug.manage" : "agentDebug.install",
  );
  const locked = busy !== null || checking;
  const previewPaths =
    status && selectedTools.length > 0
      ? [
          ...new Set([
            ...status.shared_paths,
            ...status.tools
              .filter((tool) => selectedTools.includes(tool.id))
              .flatMap((tool) => tool.preview_paths),
          ]),
        ]
      : [];

  const openInstall = (): void => {
    setSelectedTools(
      detected
        .filter((tool) => tool.skill_installed || tool.mcp_installed)
        .map((tool) => tool.id),
    );
    setConfirm("install");
  };

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t("agentDebug.title")}
        variant="compact"
        actions={
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <ShieldCheck aria-hidden="true" className="size-3.5" />
            <span>{t("agentDebug.readOnly")}</span>
            <HelpPopover label={t("agentDebug.permissionsTitle")}>
              {t("agentDebug.permissionsBody")}
            </HelpPopover>
          </div>
        }
      />

      <div
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-2"
        data-slot="agent-tools-content"
      >
        <p className="mb-4 text-sm text-text-secondary">
          {t("agentDebug.description")}
        </p>
        {error ? (
          <FormMessage className="mb-3 [overflow-wrap:anywhere]" tone="error">
            {error}
          </FormMessage>
        ) : null}

        <div className="grid items-start gap-4 @min-[680px]/workspace-surface:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
          <div className="grid min-w-0 gap-4">
            <Panel aria-labelledby="agent-install-heading">
              <PanelHeader
                className="items-center"
                actions={
                  <Button
                    disabled={locked}
                    onClick={() => void refresh()}
                    size="sm"
                    type="button"
                    variant="ghost"
                  >
                    <RefreshCw
                      aria-hidden="true"
                      className={
                        checking
                          ? "animate-spin motion-reduce:animate-none"
                          : undefined
                      }
                    />
                    {checking ? t("common.checking") : t("agentDebug.refresh")}
                  </Button>
                }
              >
                <h2
                  className="text-sm font-semibold"
                  id="agent-install-heading"
                >
                  {t("agentDebug.panelTitle")}
                </h2>
                <p
                  aria-live="polite"
                  className="mt-1 text-xs text-muted-foreground"
                >
                  {status
                    ? t("agentDebug.configuredCount", {
                        count: configured.length,
                        total: detected.length,
                      })
                    : t("agentDebug.statusHint")}
                </p>
              </PanelHeader>

              <Table aria-label={t("agentDebug.panelTitle")}>
                <TableHeader>
                  <TableRow className="bg-muted/40 hover:bg-muted/40">
                    <TableHead className="pl-4">
                      {t("agentDebug.toolColumn")}
                    </TableHead>
                    <TableHead>Skill</TableHead>
                    <TableHead className="pr-4">MCP</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {toolIds.map((id) => {
                    const tool = status?.tools.find((item) => item.id === id);
                    return (
                      <TableRow key={id}>
                        <TableCell className="py-3 pl-4">
                          <span className="flex items-center gap-2.5">
                            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background">
                              <AgentToolIcon id={id} />
                            </span>
                            <span className="flex min-w-0 items-center gap-0.5">
                              <span className="font-medium">
                                {t(`agentDebug.tools.${id}`)}
                              </span>
                              <StatusDot
                                label={t(
                                  !status
                                    ? checking
                                      ? "common.checking"
                                      : "agentDebug.unavailable"
                                    : tool?.detected
                                      ? "agentDebug.detected"
                                      : "agentDebug.notDetected",
                                )}
                                tone={
                                  tool?.detected
                                    ? "positive"
                                    : !status && checking
                                      ? "pending"
                                      : "neutral"
                                }
                              />
                            </span>
                          </span>
                        </TableCell>
                        {(["skill_installed", "mcp_installed"] as const).map(
                          (part) => (
                            <TableCell className="last:pr-4" key={part}>
                              {tool?.detected || tool?.[part] ? (
                                <StatusBadge
                                  tone={tool[part] ? "positive" : "pending"}
                                >
                                  {t(
                                    tool[part]
                                      ? "agentDebug.installed"
                                      : "agentDebug.notInstalled",
                                  )}
                                </StatusBadge>
                              ) : (
                                <span
                                  className="text-muted-foreground"
                                  aria-label={t(
                                    status
                                      ? "agentDebug.notDetected"
                                      : "agentDebug.unavailable",
                                  )}
                                >
                                  —
                                </span>
                              )}
                            </TableCell>
                          ),
                        )}
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>

              {status && detected.length === 0 ? (
                <FormMessage className="mx-4 mb-3">
                  {t("agentDebug.noTools")}
                </FormMessage>
              ) : status && !status.mcp_binary && configured.length > 0 ? (
                <FormMessage className="mx-4 mb-3" tone="warning">
                  {t("agentDebug.missingRuntime")}
                </FormMessage>
              ) : null}

              <PanelFooter
                className="gap-2"
                actions={
                  <>
                    {anyInstalled ? (
                      <Button
                        disabled={locked}
                        onClick={() => setConfirm("uninstall")}
                        size="sm"
                        type="button"
                        variant="ghost"
                      >
                        {busy === "uninstall"
                          ? t("agentDebug.removing")
                          : t("agentDebug.remove")}
                      </Button>
                    ) : null}
                    <Button
                      disabled={locked || !status || detected.length === 0}
                      onClick={openInstall}
                      type="button"
                    >
                      {busy === "install"
                        ? t("agentDebug.installing")
                        : installLabel}
                    </Button>
                  </>
                }
              >
                <div className="flex items-center gap-1 text-xs text-muted-foreground">
                  <span>{t("agentDebug.installScopeShort")}</span>
                  <HelpPopover label={t("agentDebug.installScopeTitle")}>
                    {t("agentDebug.installScopeBody")}
                  </HelpPopover>
                </div>
              </PanelFooter>
            </Panel>

            <Panel className="p-4" tone="inset">
              <CopyableValue
                copyLabel={t("agentDebug.copyPrompt")}
                label={t("agentDebug.promptTitle")}
                placeholder=""
                value={t("agentDebug.prompt")}
                variant="block"
              />
              <p className="mt-3 text-xs text-muted-foreground">
                {t("agentDebug.promptHint")}
              </p>
            </Panel>
          </div>

          <Panel aria-labelledby="agent-start-heading">
            <PanelHeader>
              <h2 className="text-sm font-semibold" id="agent-start-heading">
                {t("agentDebug.startTitle")}
              </h2>
            </PanelHeader>
            <ol>
              {(["gateway", "session", "ask"] as const).map((step, index) => (
                <DataRow asChild className="items-start py-4" key={step}>
                  <li>
                    <span
                      aria-hidden="true"
                      className="flex size-5 shrink-0 items-center justify-center rounded-full bg-accent text-xs font-medium text-accent-foreground"
                    >
                      {index + 1}
                    </span>
                    <div className="min-w-0">
                      <h3 className="text-sm font-medium">
                        {t(`agentDebug.steps.${step}.title`)}
                      </h3>
                      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                        {t(`agentDebug.steps.${step}.body`)}
                      </p>
                    </div>
                  </li>
                </DataRow>
              ))}
            </ol>
            <div className="grid gap-4 border-t px-4 py-4">
              <HelpDisclosure title={t("agentDebug.help.installTitle")}>
                <p>{t("agentDebug.help.installBody")}</p>
              </HelpDisclosure>
              <HelpDisclosure title={t("agentDebug.help.loginTitle")}>
                <p>{t("agentDebug.help.loginBody")}</p>
              </HelpDisclosure>
              <HelpDisclosure title={t("agentDebug.help.bodyTitle")}>
                <p>{t("agentDebug.help.bodyBody")}</p>
              </HelpDisclosure>
            </div>
          </Panel>
        </div>
      </div>

      <ConfirmDialog
        confirmLabel={
          confirm === "uninstall"
            ? t("agentDebug.remove")
            : t("agentDebug.installSelected", { count: selectedTools.length })
        }
        confirmDisabled={confirm === "install" && selectedTools.length === 0}
        description={
          confirm === "uninstall" ? (
            <p>{t("agentDebug.removeBody")}</p>
          ) : (
            <div className="grid gap-3">
              <p>{t("agentDebug.installBody")}</p>
              <fieldset
                className="grid min-w-0 gap-2 text-left"
                disabled={locked}
              >
                <legend className="mb-2 text-sm font-medium text-foreground">
                  {t("agentDebug.selectTools")}
                </legend>
                <div className="grid grid-cols-2 gap-x-4 gap-y-3">
                  {toolIds.map((id) => {
                    const tool = status?.tools.find((item) => item.id === id);
                    return (
                      <Label
                        className="min-w-0 items-start gap-2"
                        htmlFor={`agent-install-${id}`}
                        key={id}
                      >
                        <Checkbox
                          checked={selectedTools.includes(id)}
                          disabled={locked || !tool?.detected}
                          id={`agent-install-${id}`}
                          onCheckedChange={(checked) =>
                            setSelectedTools((current) =>
                              checked === true
                                ? [...current, id]
                                : current.filter((item) => item !== id),
                            )
                          }
                        />
                        <span className="grid min-w-0 gap-0.5">
                          <span className="text-foreground">
                            {t(`agentDebug.tools.${id}`)}
                          </span>
                          <span className="text-xs font-normal text-muted-foreground">
                            {t(
                              !tool?.detected
                                ? "agentDebug.notDetected"
                                : tool.skill_installed && tool.mcp_installed
                                  ? "agentDebug.installed"
                                  : "agentDebug.detected",
                            )}
                          </span>
                        </span>
                      </Label>
                    );
                  })}
                </div>
              </fieldset>
              <p>{t("agentDebug.selectionHint")}</p>
              {previewPaths.length ? (
                <HelpDisclosure title={t("agentDebug.pathsTitle")}>
                  <ul className="max-h-40 overflow-y-auto list-disc pl-4 text-left font-mono text-xs text-text-secondary">
                    {previewPaths.map((path) => (
                      <li key={path} className="[overflow-wrap:anywhere]">
                        {path}
                      </li>
                    ))}
                  </ul>
                </HelpDisclosure>
              ) : null}
            </div>
          )
        }
        destructive={confirm === "uninstall"}
        disabled={locked}
        onCancel={() => setConfirm(null)}
        onConfirm={() =>
          void run(confirm === "uninstall" ? "uninstall" : "install")
        }
        open={confirm !== null}
        title={t(
          confirm === "uninstall"
            ? "agentDebug.removeTitle"
            : "agentDebug.installTitle",
        )}
      />
    </section>
  );
}
