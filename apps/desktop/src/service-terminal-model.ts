import { defaultModel } from "./client-delivery-model";
import type { Service } from "./service-model";

/** Clients a terminal session can start; AstrLink sets up both itself. */
export const terminalClients = ["claude", "codex"] as const;
export type TerminalClient = (typeof terminalClients)[number];

export const terminalApps = [
  "terminal",
  "iterm2",
  "warp",
  "ghostty",
  "wezterm",
  "kitty",
  "alacritty",
  "gnome_terminal",
  "konsole",
  "xfce4_terminal",
  "x_terminal_emulator",
  "windows_terminal",
  "powershell",
] as const;
export type TerminalApp = (typeof terminalApps)[number];

export interface TerminalChoice {
  id: TerminalApp;
  name: string;
}

export interface ServiceTerminalRequest {
  service_id: string;
  /** Shown in the terminal's first line. */
  service_name: string;
  client: TerminalClient;
  directory: string;
  /** Null keeps the client's own model. */
  model: string | null;
  terminal: TerminalApp;
  inference_url: string;
}

export function parseTerminalChoices(value: unknown): TerminalChoice[] {
  if (!Array.isArray(value)) throw new Error("Invalid terminal list");
  return value.map((item: unknown) => {
    const { id, name } = (item ?? {}) as Record<string, unknown>;
    if (
      !(terminalApps as readonly unknown[]).includes(id) ||
      typeof name !== "string" ||
      name === ""
    ) {
      throw new Error("Invalid terminal entry");
    }
    return { id: id as TerminalApp, name };
  });
}

export const SERVICE_TERMINAL_STORAGE_KEY = "astrlink.serviceTerminal.v1";
const maxDirectories = 5;

/** The choices the dialog starts from next time. */
export interface TerminalMemory {
  terminal: TerminalApp | null;
  /** Folders sessions started in, most recent first. */
  directories: string[];
  /** The client last opened for each provider. */
  clients: Record<string, TerminalClient>;
  /** The model last chosen by provider and client; blank keeps the client's own. */
  models: Record<string, string>;
}

const emptyMemory: TerminalMemory = {
  terminal: null,
  directories: [],
  clients: {},
  models: {},
};

function entries<T>(
  value: unknown,
  keep: (item: unknown) => item is T,
): Record<string, T> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return {};
  }
  return Object.fromEntries(
    Object.entries(value).filter((entry): entry is [string, T] =>
      keep(entry[1]),
    ),
  );
}

const isClient = (value: unknown): value is TerminalClient =>
  (terminalClients as readonly unknown[]).includes(value);
const isText = (value: unknown): value is string => typeof value === "string";

export function readTerminalMemory(): TerminalMemory {
  try {
    const saved = JSON.parse(
      localStorage.getItem(SERVICE_TERMINAL_STORAGE_KEY) ?? "null",
    ) as Partial<Record<keyof TerminalMemory, unknown>> | null;
    if (saved === null || typeof saved !== "object") return emptyMemory;
    return {
      terminal: (terminalApps as readonly unknown[]).includes(saved.terminal)
        ? (saved.terminal as TerminalApp)
        : null,
      directories: Array.isArray(saved.directories)
        ? saved.directories.filter(isText).slice(0, maxDirectories)
        : [],
      clients: entries(saved.clients, isClient),
      models: entries(saved.models, isText),
    };
  } catch {
    // Malformed or unavailable storage only loses the remembered choices.
    return emptyMemory;
  }
}

export function modelMemoryKey(serviceId: string, client: TerminalClient) {
  return `${serviceId}:${client}`;
}

/** Remembers an opened session's choices, its folder first. */
export function rememberTerminalSession(
  memory: TerminalMemory,
  session: Pick<
    ServiceTerminalRequest,
    "service_id" | "client" | "directory" | "model" | "terminal"
  >,
): TerminalMemory {
  const next: TerminalMemory = {
    terminal: session.terminal,
    directories: [
      session.directory,
      ...memory.directories.filter((dir) => dir !== session.directory),
    ].slice(0, maxDirectories),
    clients: { ...memory.clients, [session.service_id]: session.client },
    models: {
      ...memory.models,
      [modelMemoryKey(session.service_id, session.client)]: session.model ?? "",
    },
  };
  try {
    localStorage.setItem(SERVICE_TERMINAL_STORAGE_KEY, JSON.stringify(next));
  } catch {
    // The session still opens; only the choices are not kept.
  }
  return next;
}

/** Models the provider answers, including names its own redirects take. */
export function serviceModelOptions(service: Service): string[] {
  return [
    ...new Set([
      ...service.models,
      ...(service.model_redirects ?? [])
        .filter((redirect) => redirect.enabled)
        .map((redirect) => redirect.from),
    ]),
  ];
}

/** How the models a client asks for by itself begin. */
const ownModelPrefix: Record<TerminalClient, string> = {
  claude: "claude-",
  codex: "gpt-",
};

/** The client a provider fits: its subscription's, else its models'. */
export function suggestedClient(service: Service): TerminalClient {
  if (service.kind === "codex_subscription") return "codex";
  if (service.kind === "claude_subscription") return "claude";
  const options = serviceModelOptions(service);
  const serves = (client: TerminalClient) =>
    options.some((model) => model.startsWith(ownModelPrefix[client]));
  return serves("codex") && !serves("claude") ? "codex" : "claude";
}

/**
 * The model a session starts with: the last one chosen for this provider and
 * client, else the client's own when the provider serves models like it,
 * else the provider's default model (see `defaultModel`).
 */
export function suggestedModel(
  service: Service,
  client: TerminalClient,
  memory: TerminalMemory,
): string {
  const remembered = memory.models[modelMemoryKey(service.id, client)];
  if (remembered !== undefined) return remembered;
  const options = serviceModelOptions(service);
  if (options.some((model) => model.startsWith(ownModelPrefix[client]))) {
    return "";
  }
  return defaultModel(options, [{ kind: service.kind, models: options }]);
}
