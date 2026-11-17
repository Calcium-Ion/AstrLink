import { Metadata, Plugins } from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_plugins.go: the plugin page, its confirmation
// window and the request metadata. Optional arguments the frontend leaves
// out arrive as null, or "" for a key or a name.
export const pluginsCommands = {
  plugin_action: (a: {
    action: string;
    key?: string;
    name?: string;
    input?: unknown;
  }) =>
    Plugins.pluginAction(a.action, a.key ?? "", a.name ?? "", a.input ?? null),
  plugin_approval_pending: () => Plugins.pluginApprovalPending(),
  plugin_approval_decide: (a: {
    id: string;
    approve: boolean;
    kept?: string[];
  }) => Plugins.pluginApprovalDecide(a.id, a.approve, a.kept ?? null),
  open_plugin_approval: () => Plugins.openPluginApproval(),
  session_plugins: (a: {
    sessionId: string;
    key?: string;
    input?: unknown;
    refresh?: boolean;
  }) =>
    Plugins.sessionPlugins(
      a.sessionId,
      a.key ?? "",
      a.input ?? null,
      a.refresh ?? false,
    ),

  metadata_action: (a: { action: string; key?: string; input?: unknown }) =>
    Metadata.metadataAction(a.action, a.key ?? "", a.input ?? null),
  access_token_metadata: (a: { tokenId: string; input?: unknown }) =>
    Metadata.accessTokenMetadata(a.tokenId, a.input ?? null),
  session_metadata: (a: {
    sessionId: string;
    key?: string;
    input?: unknown;
    unbind?: boolean;
  }) =>
    Metadata.sessionMetadata(
      a.sessionId,
      a.key ?? "",
      a.input ?? null,
      a.unbind ?? false,
    ),
} satisfies CommandTable;
