import { Clients, type ApplyItem, type Action, type Models } from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_clients.go: client configs, their previews and
// snippets, import links, the client proxy check and CC Switch. Optional
// arguments the frontend leaves out arrive as null.
export const clientsCommands = {
  client_catalog: () => Clients.clientCatalog(),
  client_config_status: (a: { inferenceUrl?: string | null }) =>
    Clients.clientConfigStatus(a.inferenceUrl ?? null),
  check_client_proxy: (a: { inferenceUrl: string }) =>
    Clients.checkClientProxy(a.inferenceUrl),
  apply_client_configs: (a: {
    tokenId?: string | null;
    inferenceUrl: string;
    items: ApplyItem[];
    confirmed: boolean;
  }) =>
    Clients.applyClientConfigs(
      a.tokenId ?? null,
      a.inferenceUrl,
      a.items,
      a.confirmed,
    ),
  preview_client_config_changes: (a: {
    tokenId?: string | null;
    client: string;
    target?: string | null;
    wsl?: string | null;
    action: Action;
    models?: Models | null;
    inferenceUrl: string;
  }) =>
    Clients.previewClientConfigChanges(
      a.tokenId ?? null,
      a.client,
      a.target ?? null,
      a.wsl ?? null,
      a.action,
      a.models ?? null,
      a.inferenceUrl,
    ),
  remove_client_config: (a: {
    client: string;
    target?: string | null;
    wsl?: string | null;
  }) => Clients.removeClientConfig(a.client, a.target ?? null, a.wsl ?? null),
  preview_client_config_snippet: (a: {
    tokenId: string;
    client: string;
    models: Models;
    inferenceUrl: string;
  }) =>
    Clients.previewClientConfigSnippet(
      a.tokenId,
      a.client,
      a.models,
      a.inferenceUrl,
    ),
  copy_client_config_snippet: (a: {
    tokenId: string;
    client: string;
    models: Models;
    inferenceUrl: string;
    file: number;
  }) =>
    Clients.copyClientConfigSnippet(
      a.tokenId,
      a.client,
      a.models,
      a.inferenceUrl,
      a.file,
    ),
  open_client_import_link: (a: {
    tokenId: string;
    client: string;
    protocol: string;
    inferenceUrl: string;
  }) =>
    Clients.openClientImportLink(
      a.tokenId,
      a.client,
      a.protocol,
      a.inferenceUrl,
    ),
  cc_switch_installed: () => Clients.ccSwitchInstalled(),
} satisfies CommandTable;
