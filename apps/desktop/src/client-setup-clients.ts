import type { StatusTone } from "@/components/StatusDot";
import {
  AmpColor,
  BaiduColor,
  CherryStudioColor,
  ClaudeCodeColor,
  ClineMono,
  CodeBuddyColor,
  CodexColor,
  CursorMono,
  DeepSeekHarnessMono,
  DimAgentMono,
  GeminiColor,
  GithubCopilotMono,
  GooseMono,
  GrokMono,
  KiloCodeMono,
  KimiMono,
  LobeHubColor,
  MinimaxColor,
  MistralColor,
  OpenCodeMono,
  OpenClawColor,
  PiColor,
  QoderColor,
  QwenColor,
  RooCodeMono,
  StepfunMono,
  TencentColor,
  TraeColor,
  ZhipuColor,
} from "@/components/brand-icons";

import type {
  BuiltinClient,
  ClientCatalog,
  ClientConfigClient,
  ClientConfigStatus,
} from "./client-config-model";
import { i18n } from "./i18n";

/** The six clients AstrLink wrote first, for the views that list only them. */
export const clientSetupClients = [
  { id: "claude", label: "Claude Code", Icon: ClaudeCodeColor },
  { id: "codex", label: "Codex", Icon: CodexColor },
  { id: "gemini", label: "Gemini CLI", Icon: GeminiColor },
  { id: "opencode", label: "OpenCode", Icon: OpenCodeMono },
  { id: "openclaw", label: "OpenClaw", Icon: OpenClawColor },
  { id: "pi", label: "Pi", Icon: PiColor },
] as const satisfies readonly {
  id: BuiltinClient;
  label: string;
  Icon: unknown;
}[];

export type ClientBrandMark = typeof CursorMono;

/**
 * Brand marks by client id. A client without one shows its initials; see
 * `ClientMark`.
 */
const brandMarks: Record<string, ClientBrandMark> = {
  ...Object.fromEntries(clientSetupClients.map(({ id, Icon }) => [id, Icon])),
  workbuddy: TencentColor,
  codebuddy: CodeBuddyColor,
  deepseek_harness: DeepSeekHarnessMono,
  dimagent: DimAgentMono,
  qwen_code: QwenColor,
  kimi_code: KimiMono,
  grok_build: GrokMono,
  copilot_cli: GithubCopilotMono,
  vscode_copilot: GithubCopilotMono,
  kilo_code: KiloCodeMono,
  cline: ClineMono,
  mistral_vibe: MistralColor,
  zcode: ZhipuColor,
  minimax_code: MinimaxColor,
  step_code: StepfunMono,
  qoder_cli: QoderColor,
  trae_cli: TraeColor,
  goose: GooseMono,
  cherry_studio: CherryStudioColor,
  trae: TraeColor,
  roo_code: RooCodeMono,
  lobehub: LobeHubColor,
  cursor: CursorMono,
  amp: AmpColor,
  trae_plugin: TraeColor,
  comate: BaiduColor,
  qoder: QoderColor,
};

export function clientBrandMark(
  client: ClientConfigClient,
): ClientBrandMark | undefined {
  return brandMarks[client];
}

/** Up to two letters for a client without a mark: ChatWise → CW, Warp → WA. */
export function clientInitials(name: string): string {
  const capitals = name.match(/[A-Z]/g) ?? [];
  if (capitals.length >= 2) return capitals.slice(0, 2).join("");
  const letters = name.replace(/[^\p{L}\p{N}]/gu, "");
  return letters.slice(0, 2).toLocaleUpperCase();
}

// Names as the host's catalog gives them; read once a catalog is loaded.
const catalogNames = new Map<string, string>();

/** Keeps the catalog's client names for labels shown outside the dialog. */
export function rememberClientNames(catalog: ClientCatalog): void {
  for (const entry of catalog.clients) catalogNames.set(entry.id, entry.name);
}

export function clientLabel(client: ClientConfigClient): string {
  return (
    catalogNames.get(client) ??
    clientSetupClients.find(({ id }) => id === client)?.label ??
    client
  );
}

/** Pi's filled mark reaches the edges; this insets it to match the others. */
export function clientIconSize(client: ClientConfigClient, size: number) {
  return client === "pi" ? Math.round(size * 0.82) : size;
}

/** This computer, or a WSL distribution. */
export function placeName(wsl: string | null): string {
  return wsl === null
    ? i18n.t("clientSetup.placeLocal")
    : i18n.t("agentDebug.wslPlace", { name: wsl });
}

/** A write target by name, such as 国内版; the id when it has none. */
export function targetName(client: ClientConfigClient, target: string) {
  const key = `clientSetup.target.${client}.${target}`;
  return i18n.exists(key) ? i18n.t(key) : target;
}

/**
 * The client's name, followed by its write target and its WSL distribution
 * when it has them.
 */
export function placeLabel(
  client: ClientConfigClient,
  wsl: string | null,
  target: string | null = null,
): string {
  const name = clientLabel(client);
  const parts = [
    target === null ? "" : targetName(client, target),
    wsl === null ? "" : placeName(wsl),
  ].filter(Boolean);
  return parts.length === 0
    ? name
    : i18n.t("clientSetup.placeClient", {
        client: name,
        place: parts.join(i18n.t("agentDebug.partsSeparator")),
      });
}

/** Why AstrLink cannot write a guide or unsupported client. */
export function clientReason(client: ClientConfigClient): string {
  const key = `clientSetup.reason.${client}`;
  return i18n.exists(key) ? i18n.t(key) : "";
}

/** A path under the home folder, shown from `~`. */
export function homePath(path: string, home: string): string {
  const base = home.replace(/[\\/]+$/, "");
  if (!base || !path.startsWith(base)) return path;
  const rest = path.slice(base.length);
  return rest === "" || /^[\\/]/.test(rest) ? `~${rest}` : path;
}

/** The file name at the end of a path. */
export function fileName(path: string): string {
  return path.split(/[\\/]/).pop() || path;
}

/**
 * A config's state as a badge, from the view of the token being set up.
 * Another token's config says so instead of its state; `missing` names the
 * models of this token's config the gateway no longer serves.
 */
export function configBadge(
  status: ClientConfigStatus,
  tokenId: string,
  missing: readonly string[] = [],
): { tone: StatusTone; label: string } | null {
  const t = i18n.t.bind(i18n);
  if (status.state === "not_configured") return null;
  const otherToken =
    status.token_id !== tokenId &&
    ["configured", "outdated", "modified", "manual"].includes(status.state);
  if (otherToken) {
    return { tone: "neutral", label: t("clientSetup.state.other_token") };
  }
  if (
    missing.length > 0 &&
    (status.state === "configured" || status.state === "outdated")
  ) {
    return {
      tone: "pending",
      label: t("clientSetup.state.model_missing", { count: missing.length }),
    };
  }
  const tones: Record<
    Exclude<ClientConfigStatus["state"], "not_configured">,
    StatusTone
  > = {
    configured: "positive",
    outdated: "pending",
    elsewhere: "neutral",
    modified: "pending",
    invalid: "negative",
    manual: "positive",
  };
  return {
    tone: tones[status.state],
    label: t(`clientSetup.state.${status.state}`),
  };
}
