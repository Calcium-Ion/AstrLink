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
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
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
import {
  SKILL_IDS,
  type AgentInstallStatus,
  type AgentSkillId,
  type AgentToolId,
  type AgentToolStatus,
  type AgentToolTarget,
} from "./agent-install-model";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";

const toolIds = ["cursor", "claude", "codex", "grok", "pi"] as const;

// Codex and Pi both read ~/.agents/skills, so installing for one installs for
// the other in the same home.
const sharedSkillTools: readonly AgentToolId[] = ["codex", "pi"];

/** Identifies a tool by where it runs: this computer or a WSL distribution. */
type ToolKey = string;

function toolKey(tool: AgentToolTarget): ToolKey {
  return `${tool.wsl ?? ""}/${tool.id}`;
}

/** This computer's tools always show; a WSL distribution lists what it has. */
function toolRows(status: AgentInstallStatus | null): AgentToolTarget[] {
  return [
    ...toolIds.map((id) => ({ id, wsl: null })),
    ...(status?.tools
      .filter((tool) => tool.wsl !== null)
      .map(({ id, wsl }) => ({ id, wsl })) ?? []),
  ];
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : i18n.t("agentDebug.failed");
}

function skillInstalled(
  tool: AgentToolStatus | undefined,
  id: AgentSkillId,
): boolean {
  return Boolean(
    tool?.skills.some((skill) => skill.id === id && skill.installed),
  );
}

function anySkillInstalled(tool: AgentToolStatus | undefined): boolean {
  return Boolean(tool?.skills.some((skill) => skill.installed));
}

type ToolPart = AgentSkillId | "cliAccess" | "guard";

// Every skill counts, and once the debug skill is there so do the host rules
// that let it run the CLI and keep agents out of AstrLink's files. Hosts that
// ask on first run, never ask, or have no guard location need no rule.
function missingParts(tool: AgentToolStatus): ToolPart[] {
  const missing: ToolPart[] = SKILL_IDS.filter(
    (id) => !skillInstalled(tool, id),
  );
  if (skillInstalled(tool, "astrlink-debug")) {
    if (
      (tool.cli_access === "allow_rules" ||
        tool.cli_access === "exec_policy") &&
      !tool.cli_access_installed
    )
      missing.push("cliAccess");
    if (tool.guard !== "skill_only" && !tool.guard_installed)
      missing.push("guard");
  }
  return missing;
}

function isInstalled(tool: AgentToolStatus): boolean {
  return missingParts(tool).length === 0;
}

function AgentSetupCell({
  checked,
  tool,
}: {
  checked: boolean;
  tool: AgentToolStatus | undefined;
}) {
  const t = useT();
  if (!tool?.detected && !anySkillInstalled(tool)) {
    return (
      <span
        className="text-muted-foreground"
        aria-label={t(
          checked ? "agentDebug.notDetected" : "agentDebug.unavailable",
        )}
      >
        —
      </span>
    );
  }
  if (!tool || !anySkillInstalled(tool)) {
    return (
      <span className="text-xs text-muted-foreground">
        {t("agentDebug.notInstalled")}
      </span>
    );
  }
  const missing = missingParts(tool);
  if (missing.length === 0) {
    return (
      <StatusBadge tone="positive">{t("agentDebug.installed")}</StatusBadge>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
      <span className="text-xs text-muted-foreground">
        {t("agentDebug.missing", {
          items: missing
            .map((part) =>
              part === "cliAccess" || part === "guard"
                ? t(`agentDebug.parts.${part}`)
                : t(`agentDebug.skills.${part}.short`),
            )
            .join(t("agentDebug.partsSeparator")),
        })}
      </span>
      <StatusBadge tone="pending">{t("agentDebug.partial")}</StatusBadge>
    </span>
  );
}

export interface AgentDebugSettingsProps {
  /** Opens the install dialog with only this skill selected once status loads. */
  preselectSkill?: AgentSkillId;
}

export function AgentDebugSettings({
  preselectSkill,
}: AgentDebugSettingsProps) {
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
  const [selectedSkills, setSelectedSkills] = useState<AgentSkillId[]>([]);
  // The cached snapshot may predate a tool install; preselection waits for a
  // fresh status.
  const [fresh, setFresh] = useState(false);
  const [selectedTools, setSelectedTools] = useState<ToolKey[]>([]);

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
      setFresh(true);
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
    if (
      operation === "install" &&
      (selectedSkills.length === 0 || selectedTools.length === 0)
    )
      return;
    refreshGeneration.current += 1;
    setBusy(operation);
    setConfirm(null);
    setError(null);
    try {
      if (operation === "install") {
        await installAgentDebug(
          SKILL_IDS.filter((id) => selectedSkills.includes(id)),
          rows.filter((row) => selectedTools.includes(toolKey(row))),
        );
      } else await uninstallAgentDebug();
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

  const rows = toolRows(status);
  const toolAt = (row: AgentToolTarget) =>
    status?.tools.find((tool) => toolKey(tool) === toolKey(row));
  const detected = status?.tools.filter((tool) => tool.detected) ?? [];
  const installed = detected.filter(isInstalled);
  const anyInstalled = Boolean(
    status?.cli_binary ||
      status?.tools.some(
        (tool) => anySkillInstalled(tool) || tool.cli_access_installed,
      ),
  );
  const installLabel = t(
    anyInstalled ? "agentDebug.manage" : "agentDebug.install",
  );
  const locked = busy !== null || checking;
  const sharedGroup = (key: ToolKey): ToolKey[] => {
    const tool = detected.find((item) => toolKey(item) === key);
    if (!tool || !sharedSkillTools.includes(tool.id)) return [key];
    return detected
      .filter(
        (item) => item.wsl === tool.wsl && sharedSkillTools.includes(item.id),
      )
      .map(toolKey);
  };
  const withShared = (keys: ToolKey[]): ToolKey[] => [
    ...new Set(keys.flatMap((key) => [key, ...sharedGroup(key)])),
  ];
  const sharesSkills = detected.some(
    (tool) => sharedGroup(toolKey(tool)).length > 1,
  );
  const toggleTool = (key: ToolKey, checked: boolean): void => {
    const group = sharedGroup(key);
    setSelectedTools((current) =>
      checked
        ? withShared([...current, key])
        : current.filter((item) => item !== key && !group.includes(item)),
    );
  };
  const previewPaths =
    status && selectedSkills.length > 0 && selectedTools.length > 0
      ? [
          ...new Set([
            ...status.shared_paths,
            ...status.tools
              .filter((tool) => selectedTools.includes(toolKey(tool)))
              .flatMap((tool) => tool.skills)
              .filter((skill) => selectedSkills.includes(skill.id))
              .flatMap((skill) => skill.preview_paths),
          ]),
        ]
      : [];

  // Every skill starts selected, and reopening keeps the tools that already
  // have one, so an update does not silently narrow the setup. A preselected
  // skill starts alone, on every detected tool when nothing is installed yet.
  const openInstall = (skill?: AgentSkillId): void => {
    const configured = detected.filter(
      (tool) => anySkillInstalled(tool) || tool.cli_access_installed,
    );
    setSelectedSkills(skill ? [skill] : [...SKILL_IDS]);
    setSelectedTools(
      withShared(
        (skill && configured.length === 0 ? detected : configured).map(toolKey),
      ),
    );
    setConfirm("install");
  };

  const preselectHandled = useRef(false);
  useEffect(() => {
    if (!preselectSkill || preselectHandled.current || !fresh) return;
    preselectHandled.current = true;
    if (detected.length > 0) openInstall(preselectSkill);
  }, [preselectSkill, fresh]);

  return (
    <>
      <ScrollWorkspace
        className="gap-0"
        contentClassName="pb-2"
        contentSlot="agent-tools-content"
        header={
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
        }
      >
        <p className="mb-4 text-sm text-text-secondary">
          {t("agentDebug.description")}
        </p>
        {error ? (
          <FormMessage className="mb-3 [overflow-wrap:anywhere]" tone="error">
            {error}
          </FormMessage>
        ) : null}

        <div className="grid items-start gap-4 @min-[680px]/workspace-surface:grid-cols-2">
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
                    ? t("agentDebug.installedCount", {
                        count: installed.length,
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
                    <TableHead className="pr-4 text-right">
                      {t("agentDebug.statusColumn")}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => {
                    const { id } = row;
                    const tool = toolAt(row);
                    return (
                      <TableRow key={toolKey(row)}>
                        <TableCell className="py-3 pl-4">
                          <span className="flex items-center gap-2.5">
                            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background">
                              <AgentToolIcon id={id} />
                            </span>
                            <span className="flex min-w-0 items-center gap-0.5">
                              <span className="font-medium">
                                {t(`agentDebug.tools.${id}`)}
                              </span>
                              {row.wsl !== null ? (
                                <span className="ml-1.5 text-xs text-muted-foreground">
                                  {t("agentDebug.wslPlace", { name: row.wsl })}
                                </span>
                              ) : null}
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
                        <TableCell className="pr-4 text-right">
                          <AgentSetupCell
                            checked={status !== null}
                            tool={tool}
                          />
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>

              {status?.wsl_unchecked.length ? (
                <p className="mx-4 mb-3 text-xs text-muted-foreground">
                  {t("agentDebug.wslUnchecked", {
                    names: status.wsl_unchecked.join(
                      t("agentDebug.partsSeparator"),
                    ),
                  })}
                </p>
              ) : null}
              {status && detected.length === 0 ? (
                <FormMessage className="mx-4 mb-3">
                  {t("agentDebug.noTools")}
                </FormMessage>
              ) : status &&
                !status.cli_binary &&
                detected.some((tool) =>
                  skillInstalled(tool, "astrlink-debug"),
                ) ? (
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
                      onClick={() => openInstall()}
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
                    <div className="grid gap-2">
                      <p>{t("agentDebug.installScopeBody")}</p>
                      <p>{t("agentDebug.installScopeHosts")}</p>
                    </div>
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
              <HelpDisclosure title={t("agentDebug.help.connectTitle")}>
                <p>{t("agentDebug.help.connectBody")}</p>
              </HelpDisclosure>
              <HelpDisclosure title={t("agentDebug.help.bodyTitle")}>
                <p>{t("agentDebug.help.bodyBody")}</p>
              </HelpDisclosure>
            </div>
          </Panel>
        </div>
      </ScrollWorkspace>

      <ConfirmDialog
        confirmLabel={
          confirm === "uninstall"
            ? t("agentDebug.remove")
            : t("agentDebug.installSelected", { count: selectedTools.length })
        }
        confirmDisabled={
          confirm === "install" &&
          (selectedSkills.length === 0 || selectedTools.length === 0)
        }
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
                  {t("agentDebug.selectSkills")}
                </legend>
                <div className="grid gap-3">
                  {SKILL_IDS.map((id) => (
                    <Label
                      className="min-w-0 items-start gap-2"
                      htmlFor={`agent-skill-${id}`}
                      key={id}
                    >
                      <Checkbox
                        checked={selectedSkills.includes(id)}
                        disabled={locked}
                        id={`agent-skill-${id}`}
                        onCheckedChange={(checked) =>
                          setSelectedSkills((current) =>
                            checked === true
                              ? [...current, id]
                              : current.filter((item) => item !== id),
                          )
                        }
                      />
                      <span className="grid min-w-0 gap-0.5">
                        <span className="text-foreground">
                          {t(`agentDebug.skills.${id}.name`)}
                        </span>
                        <span className="text-xs font-normal text-muted-foreground">
                          {t(`agentDebug.skills.${id}.scope`)}
                        </span>
                      </span>
                    </Label>
                  ))}
                </div>
              </fieldset>
              <fieldset
                className="grid min-w-0 gap-2 text-left"
                disabled={locked}
              >
                <legend className="mb-2 text-sm font-medium text-foreground">
                  {t("agentDebug.selectTools")}
                </legend>
                <div className="grid grid-cols-2 gap-x-4 gap-y-3">
                  {rows.map((row) => {
                    const { id } = row;
                    const key = toolKey(row);
                    const tool = toolAt(row);
                    const inputId = `agent-install-${row.wsl === null ? "" : `wsl-${row.wsl}-`}${id}`;
                    return (
                      <Label
                        className="min-w-0 items-start gap-2"
                        htmlFor={inputId}
                        key={key}
                      >
                        <Checkbox
                          checked={selectedTools.includes(key)}
                          disabled={locked || !tool?.detected}
                          id={inputId}
                          onCheckedChange={(checked) =>
                            toggleTool(key, checked === true)
                          }
                        />
                        <span className="grid min-w-0 gap-0.5">
                          <span className="text-foreground">
                            {t(`agentDebug.tools.${id}`)}
                            {row.wsl !== null ? (
                              <span className="ml-1.5 text-xs font-normal text-muted-foreground">
                                {t("agentDebug.wslPlace", { name: row.wsl })}
                              </span>
                            ) : null}
                          </span>
                          <span className="text-xs font-normal text-muted-foreground">
                            {t(
                              !tool?.detected
                                ? "agentDebug.notDetected"
                                : isInstalled(tool)
                                  ? "agentDebug.installed"
                                  : anySkillInstalled(tool)
                                    ? "agentDebug.partial"
                                    : "agentDebug.detected",
                            )}
                          </span>
                        </span>
                      </Label>
                    );
                  })}
                </div>
                {sharesSkills ? (
                  <p className="text-xs text-muted-foreground">
                    {t("agentDebug.sharedSkills")}
                  </p>
                ) : null}
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
    </>
  );
}
