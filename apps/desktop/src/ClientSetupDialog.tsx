import { isWebEdition } from "./edition";
import { WebClientSetup } from "./WebClientSetup";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";

import { Field } from "@/components/Field";
import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import {
  ClaudeCodeColor,
  CodexColor,
  GeminiColor,
  OpenCodeMono,
  OpenClawColor,
  PiColor,
} from "@/components/brand-icons";
import { CCSwitchIcon } from "@/components/CCSwitchIcon";
import { ArrowUpRight, Key, Server, Settings } from "@/components/icons";
import { RadioGroup } from "@/components/ui/radio-group";
import { SegmentedControl } from "@/components/SegmentedControl";
import { ModelSelect } from "@/components/ModelSelect";
import { FormMessage } from "@/components/FormMessage";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { cn } from "@/lib/utils";
import type { AccessTokenSummary } from "./access-token-model";
import {
  applyClientConfig,
  checkClientProxy,
  getClientConfigStatus,
  getRoutingSettings,
  isCCSwitchInstalled,
  listServices,
  openCCSwitchImport,
  removeClientConfig,
  type ClientConfigTarget,
} from "./bridge";
import {
  ccSwitchImports,
  isDirectClient,
  type ClientConfigClient,
  type ClientConfigModels,
  type ClientConfigOverview,
  type ClientConfigState,
  type ClientConfigStatus,
  type ClientProxyCheck,
  type DirectClient,
  maxPiLimit,
} from "./client-config-model";
import { getOfficialModelLimits } from "./pricing-bridge";
import {
  astrlinkAutoModelId,
  type ModelRedirect,
} from "./failure-policy-model";
import type { Service } from "./service-model";
import {
  enableServiceEntry,
  serviceEntryTarget,
  serviceHasEntry,
  serviceSupportsEntry,
} from "./service-entry-model";
import { routingMemberEntry, usedMembers } from "./model-routing-format-model";
import {
  modelRoutingPublicModelID,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import type { ConversionEngineCapability } from "./core-model";
import { protocolLabel } from "./service-presets";
import { i18n } from "./i18n";
import { notify } from "./notify";

export const clientSetupClients = [
  { id: "claude", label: "Claude Code", Icon: ClaudeCodeColor },
  { id: "codex", label: "Codex", Icon: CodexColor },
  { id: "gemini", label: "Gemini CLI", Icon: GeminiColor },
  { id: "opencode", label: "OpenCode", Icon: OpenCodeMono },
  { id: "openclaw", label: "OpenClaw", Icon: OpenClawColor },
  { id: "pi", label: "Pi", Icon: PiColor },
] as const satisfies readonly {
  id: ClientConfigClient;
  label: string;
  Icon: unknown;
}[];

export function clientLabel(client: ClientConfigClient): string {
  return clientSetupClients.find(({ id }) => id === client)?.label ?? client;
}

/** The client's name, followed by its WSL distribution when it runs in one. */
function placeLabel(client: ClientConfigClient, wsl: string | null): string {
  const name = clientLabel(client);
  return wsl === null
    ? name
    : i18n.t("clientSetup.placeClient", {
        client: name,
        place: placeName(wsl),
      });
}

/** This computer, or a WSL distribution, as the location control names it. */
function placeName(wsl: string | null): string {
  return wsl === null
    ? i18n.t("clientSetup.placeLocal")
    : i18n.t("agentDebug.wslPlace", { name: wsl });
}

/** Windows matches WSL distribution names in any case. */
function samePlace(a: string | null, b: string | null): boolean {
  return a?.toLowerCase() === b?.toLowerCase();
}

const protocols: Record<ClientConfigClient, string> = {
  claude: "anthropic.messages",
  codex: "openai.responses",
  gemini: "google.generate_content",
  opencode: "openai.chat",
  openclaw: "openai.chat",
  pi: "openai.responses",
};

const claudeTiers = [
  "haikuModel",
  "sonnetModel",
  "opusModel",
  "fableModel",
] as const;

const piLimits = ["contextWindow", "maxTokens"] as const;

/** What Pi assumes for a model whose entry names no limit. */
const piDefaults: Record<(typeof piLimits)[number], number> = {
  contextWindow: 128000,
  maxTokens: 16384,
};

/**
 * The model fields as the form holds them; only Claude Code shows the tiers,
 * and only Pi the limits.
 */
type ModelFields = Record<
  "model" | (typeof claudeTiers)[number] | (typeof piLimits)[number],
  string
>;

const noModels: ModelFields = {
  model: "",
  haikuModel: "",
  sonnetModel: "",
  opusModel: "",
  fableModel: "",
  contextWindow: "",
  maxTokens: "",
};

/** A typed limit in tokens: undefined when blank, null when invalid. */
function piLimit(text: string): number | null | undefined {
  const value = text.trim();
  if (!value) return undefined;
  const tokens = /^[0-9]{1,9}$/.test(value) ? Number(value) : 0;
  return tokens >= 1 && tokens <= maxPiLimit ? tokens : null;
}

/** A badge's label: the config's state, or that another token holds it. */
type ConfigLabel = Exclude<ClientConfigState, "not_configured"> | "other_token";

const stateTones: Record<ConfigLabel, StatusTone> = {
  configured: "positive",
  outdated: "pending",
  elsewhere: "neutral",
  modified: "pending",
  cc_switch: "neutral",
  invalid: "negative",
  other_token: "neutral",
};

/**
 * A config's state, once AstrLink or CC Switch wrote it. The dialog sets up
 * one token, so a config written with another token says so instead.
 */
function stateBadge(status: ClientConfigStatus, tokenId: string) {
  if (status.state === "not_configured") return null;
  const label: ConfigLabel =
    status.token_id !== tokenId &&
    ["configured", "outdated", "modified"].includes(status.state)
      ? "other_token"
      : status.state;
  return (
    <StatusBadge tone={stateTones[label]}>
      {i18n.t(`clientSetup.state.${label}`)}
    </StatusBadge>
  );
}

/** How a client card can be used right now. */
type Availability = "loading" | "direct" | "cc-switch" | "missing";

/** A config write, on this computer (`wsl: null`) or in a WSL distribution. */
type WriteTarget = ClientConfigTarget & { wsl: string | null };

/** The value the location control uses for this computer. */
const localPlace = "local";

/**
 * Where a client can be configured: this computer when it is installed or
 * configured there, and each WSL distribution where it is.
 */
function placesOf(
  overview: ClientConfigOverview | null,
  client: ClientConfigClient,
): ClientConfigStatus[] {
  return (overview?.clients ?? []).filter(
    (status) =>
      status.client === client &&
      (status.detected || status.state !== "not_configured"),
  );
}

interface Failure {
  message: string;
  detail?: string;
}

/**
 * Explains a system proxy that gets between Codex and the gateway. Only the
 * user changes proxy settings; AstrLink just points at what to change.
 */
function proxyHint(
  check: ClientProxyCheck,
  baseURL: string,
): { tone: "warning" | "notice"; message: string } | null {
  const t = i18n.t.bind(i18n);
  const { client, numeric } = check;
  if (client.route === "blocked") {
    return {
      tone: "warning",
      message: t("clientSetup.proxy.blocked", { proxy: client.proxy }),
    };
  }
  if (client.route === "proxied") {
    return {
      tone: "notice",
      message: t("clientSetup.proxy.proxied", { proxy: client.proxy }),
    };
  }
  if (numeric?.route === "blocked") {
    return {
      tone: "notice",
      message: t("clientSetup.proxy.numericBlocked", {
        proxy: numeric.proxy,
        url: baseURL,
      }),
    };
  }
  return null;
}

function failure(message: string, cause: unknown): Failure {
  return {
    message,
    detail: cause instanceof Error ? cause.message : undefined,
  };
}

export function ClientSetupDialog(
  props: Parameters<typeof DesktopClientSetupDialog>[0],
) {
  return isWebEdition ? (
    <WebClientSetup onClose={props.onClose} />
  ) : (
    <DesktopClientSetupDialog {...props} />
  );
}

function DesktopClientSetupDialog({
  token,
  tokens = [],
  inferenceURL,
  conversionEngine,
  onClose,
  onChanged,
}: {
  token: AccessTokenSummary;
  /** Names the token another client is configured with. */
  tokens?: readonly AccessTokenSummary[];
  inferenceURL: string;
  conversionEngine?: ConversionEngineCapability | null;
  onClose: () => void;
  /** Called after a client config was written or removed. */
  onChanged?: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const [overview, setOverview] = useState<ClientConfigOverview | null>(null);
  const [statusFailed, setStatusFailed] = useState(false);
  const [ccSwitch, setCCSwitch] = useState(false);
  const [choice, setChoice] = useState<ClientConfigClient | null>(null);
  // `undefined` follows the first place the client is installed.
  const [placeChoice, setPlaceChoice] = useState<string | null | undefined>(
    undefined,
  );
  // What the user typed, by client and place. A config left alone shows the
  // models it holds now.
  const [drafts, setDrafts] = useState<Record<string, ModelFields>>({});
  const [busy, setBusy] = useState<"write" | "remove" | "cc-switch" | null>(
    null,
  );
  const [error, setError] = useState<Failure | null>(null);
  const [conflict, setConflict] = useState<{
    target: WriteTarget;
    keys: string[];
    /** The base URL of the other AstrLink the config connects to now. */
    gateway: string | null;
  } | null>(null);
  const [removal, setRemoval] = useState<{
    client: DirectClient;
    wsl: string | null;
  } | null>(null);
  const [catalog, setCatalog] = useState<{
    services: Service[];
    redirects: ModelRedirect[];
    groups: ModelRoutingGroup[];
  }>({ services: [], redirects: [], groups: [] });
  const [catalogStatus, setCatalogStatus] = useState<
    "loading" | "ready" | "error"
  >("loading");
  const [proxyCheck, setProxyCheck] = useState<{
    url: string;
    result: ClientProxyCheck;
  } | null>(null);
  const active = useRef(true);
  const pending = useRef(false);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    try {
      const next = await getClientConfigStatus(inferenceURL || null);
      if (!active.current) return;
      setOverview(next);
      setStatusFailed(false);
    } catch {
      if (!active.current) return;
      setOverview(
        (current) =>
          current ?? {
            clients: [],
            wsl_unchecked: [],
            wsl_localhost: false,
            cc_switch_wsl: { claude: null, codex: null },
          },
      );
      setStatusFailed(true);
    }
  }, [inferenceURL]);

  useEffect(() => {
    void refresh();
    // CC Switch is optional; a failed check only hides its import.
    void isCCSwitchInstalled().then(
      (installed) => active.current && setCCSwitch(installed),
      () => undefined,
    );
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listServices(),
      // Redirect sources are optional suggestions; any failure only drops them.
      Promise.resolve().then(() => getRoutingSettings()),
    ]).then(([services, routing]) => {
      if (cancelled) return;
      setCatalog({
        services: services.status === "fulfilled" ? services.value.items : [],
        redirects:
          routing.status === "fulfilled"
            ? (routing.value.model_redirects ?? [])
            : [],
        groups:
          routing.status === "fulfilled"
            ? (routing.value.model_routing_groups ?? []).filter(
                (group) => group.enabled,
              )
            : [],
      });
      setCatalogStatus(services.status === "fulfilled" ? "ready" : "error");
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // The place a card stands for: the chosen one, else where this token is
  // configured, else where it is installed.
  const statusOf = (id: ClientConfigClient, place?: string | null) => {
    const places = placesOf(overview, id);
    return (
      (place !== undefined
        ? places.find((status) => status.wsl === place)
        : undefined) ??
      places.find((status) => status.token_id === token.id) ??
      places.find((status) => status.detected) ??
      overview?.clients.find(
        (status) => status.client === id && status.wsl === null,
      )
    );
  };
  const availability = (id: ClientConfigClient): Availability => {
    if (!isDirectClient(id)) return ccSwitch ? "cc-switch" : "missing";
    // A failed status read says nothing about whether the client exists.
    if (!overview?.clients.some((status) => status.client === id))
      return "loading";
    return placesOf(overview, id).some((status) => status.detected)
      ? "direct"
      : "missing";
  };
  const usable = (id: ClientConfigClient) =>
    ["direct", "cc-switch"].includes(availability(id));
  const firstUsable = (preferred: (id: ClientConfigClient) => boolean) =>
    clientSetupClients.find(({ id }) => usable(id) && preferred(id))?.id;
  // Wait for the status before picking a card, so the choice does not jump.
  // Open on a client this token is configured in, else on one nobody set up,
  // so a first write leaves other tokens' clients alone.
  const client =
    (choice && usable(choice) ? choice : null) ??
    (overview === null
      ? null
      : (firstUsable((id) => statusOf(id)?.token_id === token.id) ??
        firstUsable((id) => statusOf(id)?.state === "not_configured") ??
        firstUsable(() => true) ??
        null));
  const mode = client ? availability(client) : null;
  const places = client ? placesOf(overview, client) : [];
  const status = client ? statusOf(client, placeChoice) : undefined;
  const place = status?.wsl ?? null;
  const inWSL = place !== null;
  const label = client ? clientLabel(client) : "";
  const isClaude = client === "claude";
  const draftKey = `${client}:${place ?? ""}`;
  const currentModels: ModelFields = {
    ...noModels,
    ...Object.fromEntries(
      Object.entries(status?.models ?? {}).map(([key, value]) => [
        key,
        String(value),
      ]),
    ),
  };
  const modelFields = drafts[draftKey] ?? currentModels;
  const editModel = (field: keyof ModelFields, value: string) =>
    setDrafts((current) => {
      const fields = current[draftKey] ?? currentModels;
      // Limits describe a model; another model starts from its own.
      const limits =
        client === "pi" && field === "model" && value !== fields.model
          ? { contextWindow: "", maxTokens: "" }
          : {};
      return {
        ...current,
        [draftKey]: { ...fields, ...limits, [field]: value },
      };
    });
  const modelRequired = !isClaude && !modelFields.model.trim();
  const limitsInvalid =
    client === "pi" &&
    piLimits.some((key) => piLimit(modelFields[key]) === null);
  const cannotWrite = modelRequired || limitsInvalid;
  // Pi's limits start from the official model's; the user can change them or
  // clear them for Pi's defaults. Each model is looked up once per place.
  const prefilled = useRef(new Set<string>());
  const piModel = client === "pi" ? modelFields.model.trim() : "";
  const limitsBlank = !modelFields.contextWindow && !modelFields.maxTokens;
  useEffect(() => {
    const key = `${draftKey}\n${piModel}`;
    if (!piModel || !limitsBlank || prefilled.current.has(key)) return;
    prefilled.current.add(key);
    void getOfficialModelLimits(piModel).then(
      (limits) => {
        if (!active.current || !limits) return;
        const tokens = (count: number) => (count > 0 ? String(count) : "");
        setDrafts((current) => {
          const fields = current[draftKey] ?? currentModels;
          if (
            fields.model.trim() !== piModel ||
            fields.contextWindow ||
            fields.maxTokens
          ) {
            return current;
          }
          return {
            ...current,
            [draftKey]: {
              ...fields,
              contextWindow: tokens(limits.contextTokens),
              maxTokens: tokens(limits.outputTokens),
            },
          };
        });
      },
      () => undefined,
    );
    // currentModels is rebuilt each render; the place and model decide.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftKey, piModel, limitsBlank]);
  const configured = status?.token_id != null;
  // Writing over another token's config moves the client to this token.
  const owned = status?.token_id === token.id;
  // CC Switch writes a client where its directory settings point, which for
  // Claude Code and Codex can be one WSL distribution. Only that place
  // offers its import.
  const ccSwitchPlace =
    client === "claude" || client === "codex"
      ? (overview?.cc_switch_wsl[client] ?? null)
      : null;
  const ccSwitchable = ccSwitch && client !== null && ccSwitchImports(client);
  const offersCCSwitch = ccSwitchable && samePlace(ccSwitchPlace, place);
  // The system proxy applies to this computer's Codex, not to one in WSL.
  const checksProxy = client === "codex" && !inWSL && inferenceURL !== "";

  useEffect(() => {
    if (!checksProxy) return;
    let cancelled = false;
    // Optional and read-only: a failed check only hides its hint.
    void Promise.resolve()
      .then(() => checkClientProxy(inferenceURL))
      .then(
        (result) =>
          !cancelled && result && setProxyCheck({ url: inferenceURL, result }),
        () => undefined,
      );
    return () => {
      cancelled = true;
    };
  }, [checksProxy, inferenceURL]);

  // Removing needs a readable file; an unreadable one is left for the user.
  // Another token's config is removed from that token's dialog. A token this
  // gateway lacks has no dialog, so its config can be removed from any.
  const removable =
    mode === "direct" &&
    configured &&
    status &&
    status.state !== "invalid" &&
    (owned || !tokens.some((item) => item.id === status.token_id))
      ? { client: status.client, wsl: status.wsl }
      : null;
  const otherToken =
    configured && !owned
      ? tokens.find((item) => item.id === status?.token_id)
      : undefined;

  const hasEntry = serviceHasEntry;
  const entryTarget = (service: Service, protocol: string) =>
    serviceEntryTarget(service, protocol, conversionEngine);
  const supportsEntry = (service: Service, protocol: string) =>
    serviceSupportsEntry(service, protocol, conversionEngine);

  // Preserve the existing route when any provider already accepts this entry.
  const modelService = (modelID: string, protocol: string) => {
    const upstreamModel =
      catalog.redirects.find(
        (redirect) => redirect.enabled && redirect.from === modelID,
      )?.to ?? modelID;
    const candidates = catalog.services.filter(
      (service) =>
        service.models.includes(upstreamModel) &&
        supportsEntry(service, protocol),
    );
    return (
      candidates.find((service) => hasEntry(service, protocol)) ?? candidates[0]
    );
  };
  const requiredEntry = (modelID: string, protocol: string) => {
    const service = modelService(modelID.trim(), protocol);
    if (!service || hasEntry(service, protocol)) return undefined;
    const target = entryTarget(service, protocol);
    return target ? { service, target } : undefined;
  };
  // A routing group names its models' providers outright, so each member is
  // checked against its own provider: an entry to open, or no way to accept
  // this client's format at all.
  const groupOf = (modelID: string) =>
    catalog.groups.find(
      (group) => modelRoutingPublicModelID(group.id) === modelID.trim(),
    );
  const groupEntries = (modelID: string, protocol: string) => {
    const group = groupOf(modelID);
    if (!group) return null;
    const entries = new Map<string, { service: Service; target: string }>();
    const incompatible: { model: string; service: string }[] = [];
    // The editor's format check, so the two never disagree about a group.
    for (const member of usedMembers(group)) {
      const entry = routingMemberEntry(
        member,
        catalog.services,
        protocol,
        conversionEngine,
      );
      if (entry.kind === "accepts") continue;
      if (entry.kind === "convertible")
        entries.set(entry.service.id, {
          service: entry.service as Service,
          target: entry.target,
        });
      else
        incompatible.push({
          model: member.model,
          service: entry.service?.name ?? t("binding.removedService"),
        });
    }
    return { entries: [...entries.values()], incompatible };
  };
  const requiredEntries = (modelID: string, protocol: string) => {
    const group = groupEntries(modelID, protocol);
    if (group) return group.entries;
    const entry = requiredEntry(modelID, protocol);
    return entry ? [entry] : [];
  };
  // Gemini's native path cannot carry the "/" a routing group's name has.
  const groupOptions =
    client && client !== "gemini"
      ? catalog.groups.map((group) => modelRoutingPublicModelID(group.id))
      : [];
  const modelOptions = client
    ? [
        ...groupOptions,
        ...[
          ...catalog.services
            .filter((service) => supportsEntry(service, protocols[client]))
            .flatMap((service) => service.models),
          ...catalog.redirects
            .filter(
              (redirect) =>
                redirect.enabled &&
                redirect.from !== astrlinkAutoModelId &&
                // Gemini paths cannot carry "/" in the model segment.
                !(client === "gemini" && redirect.from.includes("/")) &&
                catalog.services.some(
                  (service) =>
                    service.models.includes(redirect.to) &&
                    supportsEntry(service, protocols[client]),
                ),
            )
            .map((redirect) => redirect.from),
        ]
          .filter((option) => !groupOptions.includes(option))
          .sort(),
      ]
    : [];
  const optionGroup = (modelID: string) =>
    groupOptions.length === 0
      ? undefined
      : t(
          groupOptions.includes(modelID)
            ? "clientSetup.groupsHeading"
            : "clientSetup.modelsHeading",
        );
  const optionAdornment = (modelID: string) => {
    if (!client) return null;
    const protocol = protocols[client];
    const group = groupEntries(modelID, protocol);
    if (group)
      return (
        <>
          {group.entries.length > 0 ? (
            <Badge
              variant="secondary"
              className="ml-auto shrink-0 text-micro"
              title={group.entries
                .map(({ service, target }) =>
                  t("clientSetup.entryWillEnable", {
                    service: service.name,
                    protocol: protocolLabel(protocol),
                    target: protocolLabel(target),
                  }),
                )
                .join("\n")}
              data-client-setup-entries={group.entries.length}
            >
              {t("clientSetup.groupEntries", { count: group.entries.length })}
            </Badge>
          ) : null}
          {group.incompatible.length > 0 ? (
            <Badge
              variant="outline"
              className={cn(
                "shrink-0 text-micro text-warning-foreground",
                group.entries.length === 0 && "ml-auto",
              )}
              title={group.incompatible
                .map((item) =>
                  t("clientSetup.groupIncompatibleItem", {
                    model: item.model,
                    service: item.service,
                    protocol: protocolLabel(protocol),
                  }),
                )
                .join("\n")}
              data-client-setup-incompatible={group.incompatible.length}
            >
              {t("clientSetup.groupIncompatible", {
                count: group.incompatible.length,
              })}
            </Badge>
          ) : null}
        </>
      );
    return requiredEntry(modelID, protocol) ? (
      <Badge variant="secondary" className="ml-auto shrink-0 text-micro">
        {t("clientSetup.entryRequired")}
      </Badge>
    ) : null;
  };
  const entryNote = (modelID: string) => {
    if (!client) return null;
    const entries = requiredEntries(modelID, protocols[client]);
    return entries.length > 0 ? (
      <FormMessage tone="notice">
        {entries
          .map(({ service, target }) =>
            t("clientSetup.entryWillEnable", {
              service: service.name,
              protocol: protocolLabel(protocols[client]),
              target: protocolLabel(target),
            }),
          )
          .join(t("clientSetup.noteSeparator"))}
      </FormMessage>
    ) : null;
  };

  const models = (): ClientConfigModels => {
    if (isClaude) {
      return Object.fromEntries(
        (["model", ...claudeTiers] as const)
          .map((key) => [key, modelFields[key].trim()])
          .filter(([, value]) => value),
      );
    }
    const fields: ClientConfigModels = { model: modelFields.model.trim() };
    if (client === "pi") {
      for (const key of piLimits) {
        const tokens = piLimit(modelFields[key]);
        if (tokens) fields[key] = tokens;
      }
    }
    return fields;
  };

  const begin = (next: NonNullable<typeof busy>) => {
    if (pending.current) return false;
    pending.current = true;
    setBusy(next);
    setError(null);
    return true;
  };
  const finish = () => {
    pending.current = false;
    if (active.current) setBusy(null);
  };

  // Read a fresh record and patch against its ETag so concurrent edits survive.
  const enableEntries = async (target: {
    client: ClientConfigClient;
    models: ClientConfigModels;
  }): Promise<boolean> => {
    const protocol = protocols[target.client];
    const entries = new Map(
      Object.values(target.models).flatMap((modelID) => {
        if (typeof modelID !== "string") return [];
        return requiredEntries(modelID, protocol).map(
          (entry) => [entry.service.id, entry.service] as const,
        );
      }),
    );
    for (const service of entries.values()) {
      try {
        const updated = await enableServiceEntry(
          service.id,
          protocol,
          conversionEngine,
        );
        if (active.current)
          setCatalog((current) => ({
            ...current,
            services: current.services.map((item) =>
              item.id === service.id ? updated : item,
            ),
          }));
      } catch (cause) {
        if (active.current)
          setError(
            failure(
              t("clientSetup.entryEnableFailed", {
                service: service.name,
                protocol: protocolLabel(protocol),
              }),
              cause,
            ),
          );
        return false;
      }
    }
    if (entries.size > 0) {
      // The fresh patched records above also keep the marker correct if refresh fails.
      try {
        const next = await listServices();
        if (active.current)
          setCatalog((current) => ({ ...current, services: next.items }));
      } catch {
        /* Keep the freshly read records. */
      }
    }
    return active.current;
  };

  const write = async (target: WriteTarget, replace: boolean) => {
    if (!begin("write")) return;
    try {
      if (!(await enableEntries(target))) return;
      const outcome = await applyClientConfig({ ...target, replace });
      if (!active.current) return;
      if (outcome.status === "needs_confirmation") {
        setConflict({ target, keys: outcome.keys, gateway: outcome.gateway });
        return;
      }
      notify.success(
        t("clientSetup.written", {
          client: placeLabel(target.client, target.wsl),
        }),
      );
      onChanged?.();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.writeFailed"), cause));
    } finally {
      finish();
    }
  };

  const remove = async (target: {
    client: DirectClient;
    wsl: string | null;
  }) => {
    if (!begin("remove")) return;
    try {
      await removeClientConfig(target.client, target.wsl);
      if (!active.current) return;
      notify.success(
        t("clientSetup.removed", {
          client: placeLabel(target.client, target.wsl),
        }),
      );
      onChanged?.();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.removeFailed"), cause));
    } finally {
      finish();
    }
  };

  const importWithCCSwitch = async () => {
    if (!client || cannotWrite || !begin("cc-switch")) return;
    try {
      const target = {
        tokenId: token.id,
        client,
        models: models(),
        inferenceUrl: inferenceURL,
      };
      if (!(await enableEntries(target))) return;
      await openCCSwitchImport(target);
      if (!active.current) return;
      notify.successWithSteps(t("clientSetup.ccSwitchOpened"));
      onClose();
    } catch {
      if (active.current)
        setError({ message: t("clientSetup.ccSwitchFailed") });
    } finally {
      finish();
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!client || cannotWrite) return;
    if (!isDirectClient(client)) {
      void importWithCCSwitch();
      return;
    }
    if (mode !== "direct" || status?.state === "invalid") return;
    void write(
      {
        tokenId: token.id,
        client,
        models: models(),
        inferenceUrl: inferenceURL,
        wsl: place,
      },
      false,
    );
  };

  const cardNote = (id: ClientConfigClient) => {
    // The chosen card follows the chosen place.
    const card = id === client ? status : statusOf(id);
    switch (availability(id)) {
      case "loading":
        return null;
      case "cc-switch":
        return t("clientSetup.ccSwitchOnly");
      case "missing":
        return t(
          isDirectClient(id)
            ? "clientSetup.notInstalled"
            : "clientSetup.unsupported",
        );
      case "direct":
        if (!card || card.state === "not_configured") return null;
        // Reads like the clients only CC Switch configures.
        if (card.state === "cc_switch") return t("clientSetup.state.cc_switch");
        return stateBadge(card, token.id);
    }
  };

  const working = busy !== null;
  const baseURL = `${inferenceURL}${
    ["codex", "opencode", "openclaw", "pi"].includes(client ?? "") ? "/v1" : ""
  }`;
  const proxy =
    checksProxy && proxyCheck?.url === inferenceURL
      ? proxyHint(proxyCheck.result, baseURL)
      : null;

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !pending.current && onClose()}
    >
      <DialogContent
        showCloseButton={!working}
        className="w-[calc(100vw_-_2rem)] max-w-none sm:max-w-3xl"
      >
        <DialogHeader className="text-left">
          <DialogTitle>
            {t("clientSetup.configureToken", { name: token.name })}
          </DialogTitle>
          <DialogDescription>{t("clientSetup.description")}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          <fieldset className="grid min-w-0 gap-2">
            <legend className="mb-2 text-xs font-medium text-text-secondary">
              {t("clientSetup.client")}
            </legend>
            <RadioGroup
              aria-label={t("clientSetup.client")}
              className="grid grid-cols-2 gap-2 min-[480px]:grid-cols-3 min-[720px]:grid-cols-6"
              value={client ?? ""}
              onValueChange={(value) => {
                setChoice(value as ClientConfigClient);
                setPlaceChoice(undefined);
                setError(null);
              }}
              disabled={working}
            >
              {clientSetupClients.map(({ id, label: name, Icon }) => (
                <ChoiceCard
                  key={id}
                  id={`client-setup-${id}`}
                  value={id}
                  label={name}
                  description={cardNote(id)}
                  selected={client === id}
                  disabled={working || !usable(id)}
                  layout="tile"
                  // Pi's filled mark reaches the edges; inset it to match.
                  icon={<Icon size={id === "pi" ? 21 : 26} />}
                />
              ))}
            </RadioGroup>
            {statusFailed ? (
              <FormMessage tone="error">
                {t("clientSetup.statusFailed")}
              </FormMessage>
            ) : null}
            {!ccSwitch ? (
              <p className="text-xs text-muted-foreground">
                {t("clientSetup.unsupportedHint")}
              </p>
            ) : null}
            {overview?.wsl_unchecked.length ? (
              <p className="text-xs text-muted-foreground">
                {t("clientSetup.wslUnchecked", {
                  names: overview.wsl_unchecked.join(
                    t("agentDebug.partsSeparator"),
                  ),
                })}
              </p>
            ) : null}
          </fieldset>
          {client ? (
            <>
              {mode === "direct" && places.some((item) => item.wsl !== null) ? (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-xs font-medium text-text-secondary">
                    {t("clientSetup.place")}
                  </span>
                  <SegmentedControl
                    label={t("clientSetup.place")}
                    disabled={working}
                    options={places.map((item) => ({
                      value: item.wsl ?? localPlace,
                      label:
                        item.wsl === null
                          ? t("clientSetup.placeLocal")
                          : t("agentDebug.wslPlace", { name: item.wsl }),
                      badge: stateBadge(item, token.id),
                    }))}
                    value={place ?? localPlace}
                    onValueChange={(value) => {
                      setPlaceChoice(value === localPlace ? null : value);
                      setError(null);
                    }}
                  />
                </div>
              ) : null}
              <div className="grid gap-x-3 gap-y-2 rounded-md border bg-muted/40 px-3 py-2.5 text-xs min-[540px]:grid-cols-2">
                <div className="flex min-w-0 items-start gap-2">
                  <Server
                    aria-hidden="true"
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <span className="shrink-0 text-muted-foreground">
                    {t("overview.apiAddress")}
                  </span>
                  <code className="min-w-0 break-all">{baseURL}</code>
                </div>
                <div className="flex min-w-0 items-start gap-2">
                  <Key
                    aria-hidden="true"
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <span className="shrink-0 text-muted-foreground">
                    {t("clientSetup.token")}
                  </span>
                  <span className="min-w-0 truncate" title={token.name}>
                    {token.name}{" "}
                    <span className="font-mono text-muted-foreground">
                      {token.hint}
                    </span>
                  </span>
                </div>
                {mode === "direct" && status ? (
                  <div className="flex min-w-0 items-start gap-2 min-[540px]:col-span-2">
                    <Settings
                      aria-hidden="true"
                      className="size-4 shrink-0 text-muted-foreground"
                    />
                    <span className="shrink-0 text-muted-foreground">
                      {t("clientSetup.configFile")}
                    </span>
                    <span className="grid min-w-0 gap-1">
                      {status.paths.map((path) => (
                        <code key={path} className="break-all">
                          {path}
                        </code>
                      ))}
                    </span>
                  </div>
                ) : null}
              </div>
              {mode === "cc-switch" ? (
                <FormMessage tone="notice">
                  {t("clientSetup.ccSwitchOnlyHint", { client: label })}
                </FormMessage>
              ) : status?.state === "invalid" ? (
                <FormMessage tone="error">
                  {t("clientSetup.invalidHint")}
                </FormMessage>
              ) : status?.state === "cc_switch" ? (
                <FormMessage tone="notice">
                  {t("clientSetup.ccSwitchManagedHint", { client: label })}
                </FormMessage>
              ) : status?.state === "elsewhere" ? (
                <FormMessage tone="notice">
                  {t("clientSetup.elsewhereHint", { client: label })}
                </FormMessage>
              ) : configured && !owned ? (
                <FormMessage tone="notice">
                  {otherToken
                    ? t("clientSetup.usesToken", {
                        client: label,
                        name: otherToken.name,
                        current: token.name,
                      })
                    : t("clientSetup.usesOtherToken", {
                        client: label,
                        current: token.name,
                      })}
                </FormMessage>
              ) : status?.state === "modified" ? (
                <FormMessage tone="warning">
                  {t("clientSetup.modifiedHint")}
                </FormMessage>
              ) : null}
              {proxy ? (
                <FormMessage tone={proxy.tone}>{proxy.message}</FormMessage>
              ) : null}
              {mode === "direct" && inWSL && !overview?.wsl_localhost ? (
                <FormMessage tone="warning">
                  {t("clientSetup.wslNetwork", { url: baseURL })}
                </FormMessage>
              ) : null}
              <Field
                htmlFor="client-setup-model"
                label={t(
                  isClaude ? "clientSetup.defaultModel" : "clientSetup.model",
                )}
                hint={t(
                  catalogStatus === "loading"
                    ? "clientSetup.modelsLoading"
                    : catalogStatus === "error"
                      ? "clientSetup.modelsFailed"
                      : isClaude
                        ? "clientSetup.claudeModelHint"
                        : "clientSetup.modelHint",
                )}
              >
                <ModelSelect
                  id="client-setup-model"
                  aria-label={t(
                    isClaude ? "clientSetup.defaultModel" : "clientSetup.model",
                  )}
                  options={modelOptions}
                  optionAdornment={optionAdornment}
                  optionGroup={optionGroup}
                  value={modelFields.model}
                  placeholder={t("clientSetup.modelPlaceholder")}
                  onValueChange={(value) => editModel("model", value)}
                  maxLength={256}
                  disabled={working}
                />
              </Field>
              {entryNote(modelFields.model)}
              {client === "pi" && (
                <div className="grid gap-1.5">
                  <div className="grid gap-3 min-[540px]:grid-cols-2">
                    {piLimits.map((key) => (
                      <Field
                        key={key}
                        htmlFor={`client-setup-${key}`}
                        label={t(`clientSetup.${key}`)}
                      >
                        <Input
                          id={`client-setup-${key}`}
                          inputMode="numeric"
                          autoComplete="off"
                          value={modelFields[key]}
                          placeholder={t("clientSetup.piDefault", {
                            tokens: piDefaults[key],
                          })}
                          aria-invalid={piLimit(modelFields[key]) === null}
                          onChange={(event) =>
                            editModel(key, event.target.value)
                          }
                          maxLength={9}
                          disabled={working}
                        />
                      </Field>
                    ))}
                  </div>
                  {limitsInvalid ? (
                    <FormMessage tone="error">
                      {t("clientSetup.limitInvalid")}
                    </FormMessage>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      {t("clientSetup.limitsHint")}
                    </span>
                  )}
                </div>
              )}
              {isClaude && (
                <div className="grid gap-3 min-[540px]:grid-cols-2">
                  {claudeTiers.map((key) => (
                    <Field
                      key={key}
                      htmlFor={`client-setup-${key}`}
                      label={t(`clientSetup.${key}`)}
                      hint={
                        key === "fableModel" && offersCCSwitch
                          ? t("clientSetup.fableNotImported")
                          : undefined
                      }
                    >
                      <ModelSelect
                        id={`client-setup-${key}`}
                        aria-label={t(`clientSetup.${key}`)}
                        options={modelOptions}
                        optionAdornment={optionAdornment}
                        optionGroup={optionGroup}
                        value={modelFields[key]}
                        onValueChange={(value) => editModel(key, value)}
                        placeholder={t("clientSetup.modelPlaceholder")}
                        maxLength={256}
                        disabled={working}
                      />
                      {entryNote(modelFields[key])}
                    </Field>
                  ))}
                </div>
              )}
              {mode === "direct" && isDirectClient(client) ? (
                <HelpDisclosure title={t("clientSetup.limitsTitle")}>
                  <p>{t(`clientSetup.limits.${client}`)}</p>
                  {client === "codex" ? (
                    <p>{t("clientSetup.codexTools")}</p>
                  ) : null}
                  {offersCCSwitch ? (
                    <p>{t("clientSetup.ccSwitchOverwrites")}</p>
                  ) : null}
                  {offersCCSwitch && client === "codex" ? (
                    <p>{t("clientSetup.codexToolsCCSwitch")}</p>
                  ) : null}
                  {ccSwitchable && !offersCCSwitch && status ? (
                    <p>
                      {t("clientSetup.ccSwitchElsewhere", {
                        client: label,
                        target: placeName(ccSwitchPlace),
                        place: placeName(place),
                        // The folder holding the config file.
                        dir: status.paths[0].replace(/[\\/][^\\/]*$/, ""),
                      })}
                    </p>
                  ) : null}
                </HelpDisclosure>
              ) : null}
            </>
          ) : null}
          {error && (
            <FormMessage tone="error">
              {error.message}
              {error.detail ? (
                <span className="mt-1 block break-all">{error.detail}</span>
              ) : null}
            </FormMessage>
          )}
          <DialogFooter className="border-t pt-4 min-[540px]:flex-row min-[540px]:justify-end">
            {removable ? (
              <Button
                type="button"
                variant="ghost"
                className="min-[540px]:mr-auto"
                disabled={working}
                onClick={() => setRemoval(removable)}
              >
                {t(
                  busy === "remove"
                    ? "clientSetup.removing"
                    : "clientSetup.remove",
                )}
              </Button>
            ) : null}
            <Button
              type="button"
              variant="outline"
              disabled={working}
              onClick={onClose}
            >
              {t("common.close")}
            </Button>
            {mode === "direct" && offersCCSwitch ? (
              <Button
                type="button"
                variant="outline"
                disabled={working || cannotWrite}
                onClick={() => void importWithCCSwitch()}
              >
                <CCSwitchIcon size={16} />
                {t(
                  busy === "cc-switch"
                    ? "clientSetup.ccSwitchOpening"
                    : "clientSetup.ccSwitchImport",
                )}
                <ArrowUpRight />
              </Button>
            ) : null}
            {mode === "cc-switch" ? (
              <Button type="submit" disabled={working || cannotWrite}>
                <CCSwitchIcon size={16} />
                {t(
                  busy === "cc-switch"
                    ? "clientSetup.ccSwitchOpening"
                    : "clientSetup.ccSwitchImport",
                )}
                <ArrowUpRight />
              </Button>
            ) : (
              <Button
                type="submit"
                disabled={
                  working ||
                  mode !== "direct" ||
                  cannotWrite ||
                  status?.state === "invalid"
                }
              >
                {t(
                  busy === "write"
                    ? "clientSetup.writing"
                    : owned
                      ? "clientSetup.update"
                      : configured
                        ? "clientSetup.useThisToken"
                        : "clientSetup.write",
                )}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
      <ConfirmDialog
        open={conflict !== null}
        title={t(
          conflict?.gateway
            ? "clientSetup.switchTitle"
            : "clientSetup.replaceTitle",
        )}
        description={
          conflict ? (
            <>
              {conflict.gateway ? (
                <p>
                  {t("clientSetup.switchBody", {
                    client: clientLabel(conflict.target.client),
                    url: conflict.gateway,
                  })}
                </p>
              ) : null}
              {conflict.keys.length > 0 ? (
                <>
                  <p>
                    {t("clientSetup.replaceBody", {
                      client: clientLabel(conflict.target.client),
                    })}
                  </p>
                  <ul className="grid gap-1">
                    {conflict.keys.map((key) => (
                      <li key={key}>
                        <code className="break-all text-xs">{key}</code>
                      </li>
                    ))}
                  </ul>
                </>
              ) : null}
            </>
          ) : null
        }
        confirmLabel={t(
          conflict?.gateway
            ? "clientSetup.switchConfirm"
            : "clientSetup.replaceConfirm",
        )}
        onCancel={() => setConflict(null)}
        onConfirm={() => {
          if (!conflict) return;
          setConflict(null);
          void write(conflict.target, true);
        }}
      />
      <ConfirmDialog
        open={removal !== null}
        destructive
        title={t("clientSetup.removeTitle", {
          client: removal ? placeLabel(removal.client, removal.wsl) : "",
        })}
        description={
          <p>
            {t("clientSetup.removeBody", {
              client: removal ? clientLabel(removal.client) : "",
            })}
          </p>
        }
        confirmLabel={t("clientSetup.remove")}
        onCancel={() => setRemoval(null)}
        onConfirm={() => {
          if (!removal) return;
          setRemoval(null);
          void remove(removal);
        }}
      />
    </Dialog>
  );
}
