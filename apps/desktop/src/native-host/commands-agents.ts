import {
  AgentTools,
  LocalClients,
  LocalImport,
  ProviderImport,
  Terminal,
  type AgentSkillID,
  type AgentToolID,
  type ClientTarget,
  type ServiceTerminalRequest,
} from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_agents.go: agent tool installs, link and local
// provider imports, local client updates and service terminals. Optional
// arguments the frontend leaves out arrive as null.
export const agentsCommands = {
  agent_debug_status: () => AgentTools.agentDebugStatus(),
  install_agent_debug: (a: {
    skillIds: AgentSkillID[];
    tools: { id: AgentToolID; wsl: string | null }[];
  }) =>
    AgentTools.installAgentDebug(
      a.skillIds,
      // The desktop user's home is a target without a distribution.
      a.tools.map(({ id, wsl }) => (wsl === null ? { id } : { id, wsl })),
    ),
  uninstall_agent_debug: () => AgentTools.uninstallAgentDebug(),

  pending_provider_import: () => ProviderImport.pendingProviderImport(),
  dismiss_provider_import: (a: { id: string }) =>
    ProviderImport.dismissProviderImport(a.id),
  confirm_provider_import: (a: { id: string; input: unknown }) =>
    ProviderImport.confirmProviderImport(a.id, a.input),

  local_import_sources: () => LocalImport.localImportSources(),
  read_local_import: (a: { session: string; sources: string[] }) =>
    LocalImport.readLocalImport(a.session, a.sources),
  probe_local_import_models: (a: {
    session: string;
    candidate: string;
    input: unknown;
  }) => LocalImport.probeLocalImportModels(a.session, a.candidate, a.input),
  confirm_local_import: (a: {
    session: string;
    candidate: string;
    input: unknown;
  }) => LocalImport.confirmLocalImport(a.session, a.candidate, a.input),
  dismiss_local_import: (a: { session: string }) =>
    LocalImport.dismissLocalImport(a.session),

  local_client_status: () => LocalClients.localClientStatus(),
  refresh_local_clients: () => LocalClients.refreshLocalClients(),
  update_local_clients: (a: { targets: ClientTarget[] }) =>
    LocalClients.updateLocalClients(a.targets),
  repair_local_clients: (a: { targets: ClientTarget[] }) =>
    LocalClients.repairLocalClients(a.targets),

  list_terminal_apps: () => Terminal.listTerminalApps(),
  pick_terminal_directory: (a: { start?: string | null }) =>
    Terminal.pickTerminalDirectory(a.start ?? null),
  open_service_terminal: (a: { request: ServiceTerminalRequest }) =>
    Terminal.openServiceTerminal(a.request),
} satisfies CommandTable;
