import { isWebEdition } from "./edition";
import { WebClientSetup } from "./WebClientSetup";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { ClientMark } from "@/components/ClientMark";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CopyChips } from "@/components/CopyChips";
import { CopyFieldList, type CopyField } from "@/components/CopyFieldList";
import { Field } from "@/components/Field";
import { FileDiff } from "@/components/FileDiff";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { ModelSelect } from "@/components/ModelSelect";
import { NumberedSteps } from "@/components/NumberedSteps";
import { SearchInput } from "@/components/SearchInput";
import { SegmentedControl } from "@/components/SegmentedControl";
import { SplitWorkspace } from "@/components/SplitWorkspace";
import { StatusBadge } from "@/components/StatusBadge";
import {
  ArrowUpRight,
  BadgeAlert,
  Ban,
  ChevronDown,
  ChevronRight,
  Check,
  CircleCheck,
  CircleHelp,
  Copy,
  FileText,
  Hand,
  Key,
  LoaderCircle,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import type { AccessTokenSummary } from "./access-token-model";
import {
  applyClientConfigs,
  checkClientProxy,
  copyAccessToken,
  getClientCatalog,
  getClientConfigStatus,
  getRoutingSettings,
  getService,
  isCCSwitchInstalled,
  listRequestRecords,
  listServices,
  openClientImportLink,
  previewClientConfigChanges,
  removeClientConfig,
  updateService,
} from "./bridge";
import {
  availableModels,
  configAttention,
  configModels,
  reasoningEffortOptions,
  writesConfig,
  type ClientCatalog,
  type ClientCatalogEntry,
  type ClientConfigAction,
  type ClientConfigClient,
  type ClientConfigConfirmation,
  type ClientConfigItem,
  type ClientConfigModels,
  type ClientConfigOverview,
  type ClientConfigPreview,
  type ClientConfigResult,
  type ClientConfigStatus,
  type ClientProxyCheck,
} from "./client-config-model";
import {
  checkedByDefault,
  clientMatches,
  defaultModel,
  draftModels,
  draftProblem,
  entryTarget,
  hasEntry,
  homeKey,
  initialDraft,
  leadPlace,
  listSelection,
  modelCandidates,
  modelProtocol,
  ownedConfig,
  placeKey,
  placeShown,
  placeWritable,
  protocolBaseURL,
  railGroupOf,
  railGroups,
  requiredEntry,
  type ModelCatalog,
  type ModelDraft,
  type PlaceRef,
  type RailGroup,
} from "./client-delivery-model";
import {
  clientLabel,
  clientReason,
  configBadge,
  fileName,
  homePath,
  placeLabel,
  placeName,
  rememberClientNames,
  targetName,
} from "./client-setup-clients";
import { ClientSettingsForm } from "./ClientSetupSettings";
import type { ConversionEngineCapability } from "./core-model";
import type { ClientType } from "./request-record-model";
import { i18n } from "./i18n";
import { notify } from "./notify";
import {
  getOfficialModelLimits,
  type OfficialModelLimits,
} from "./pricing-bridge";
import type { Service, ServiceCapability } from "./service-model";
import { protocolLabel } from "./service-presets";
import { clientConfigsChanged } from "./use-client-configs";

interface Failure {
  message: string;
  detail?: string;
}

/** One place in a write: a client in a home and, if it has several, a target. */
interface Planned {
  status: ClientConfigStatus;
  action: ClientConfigAction;
  models?: ClientConfigModels;
}

/** How a place's write went, with what it was before. */
interface Outcome {
  result: ClientConfigResult;
  before: ClientConfigStatus;
  models?: ClientConfigModels;
}

/**
 * What the pane shows: a written client in one home with its targets, or a
 * client AstrLink only lists.
 */
type Selected =
  | {
      kind: "home";
      key: string;
      entry: ClientCatalogEntry;
      wsl: string | null;
      places: ClientConfigStatus[];
      lead: ClientConfigStatus;
    }
  | { kind: "static"; key: string; entry: ClientCatalogEntry };

type Tab = "settings" | "changes";

const emptyOverview: ClientConfigOverview = {
  clients: [],
  wsl_unchecked: [],
  wsl_localhost: false,
  installed: [],
};

/** The key of a client AstrLink only lists, beside its homes' keys. */
const staticKey = (client: ClientConfigClient) => `${client}:`;

function failure(message: string, cause: unknown): Failure {
  return {
    message,
    detail: cause instanceof Error ? cause.message : undefined,
  };
}

/** Joins names the way the locale lists parts. */
function joinNames(names: readonly string[]): string {
  return names.join(i18n.t("agentDebug.partsSeparator"));
}

/** Translated text when the key exists; empty otherwise. */
function optional(key: string, options?: Record<string, string>): string {
  return i18n.exists(key) ? i18n.t(key, options) : "";
}

/** A protocol id as a translation key part. */
const protocolKey = (protocol: string) => protocol.replace(/\./g, "_");

/** The provider name each import link gives Cherry Studio, by protocol. */
/** The gateway's name for each client the token is pasted into. */
const pasteClientTypes: Partial<Record<string, ClientType>> = {
  zed: "zed",
  goose: "goose",
};

/** A written paste client whose requests can show the paste worked. */
function awaitsPaste(status: ClientConfigStatus) {
  return (
    status.paste_since_unix !== null &&
    status.token_id !== null &&
    pasteClientTypes[status.client] !== undefined &&
    (status.state === "configured" || status.state === "outdated")
  );
}

function pasteKey(status: ClientConfigStatus) {
  return `${placeKey(status)}:${status.token_id}:${status.paste_since_unix}`;
}

const linkProviderNames: Record<string, string> = {
  "openai.chat": "AstrLink",
  "openai.responses": "AstrLink Responses",
  "anthropic.messages": "AstrLink Anthropic",
  "google.generate_content": "AstrLink Gemini",
};

/**
 * Translated text whose `code` placeholders render as code, such as a file
 * and the key inside it; `plain` ones stay text.
 */
/** Rows a place's confirmation shows before the rest folds away. */
const confirmRowsShown = 4;

/** A setting, its value now and after the write, one row each. */
const confirmColumns =
  "grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)] gap-3 px-2.5 text-muted-foreground";

/** One value in the confirmation: a line that truncates, whole on hover. */
function ConfirmValue({
  className,
  value,
}: {
  className?: string;
  value: string | null;
}) {
  return value === null ? (
    <span aria-label={i18n.t("clientSetup.confirm.notShown")}>—</span>
  ) : (
    <code className={cn("block truncate font-mono", className)} title={value}>
      {value}
    </code>
  );
}

/**
 * One place in the write confirmation: the client and where its config
 * lives, what the write changes, and the settings AstrLink did not write,
 * one per line with the value they hold now.
 */
function ConfirmationPlace({
  changes,
  dir,
  fileOf,
  item,
  label,
  pathOf,
}: {
  changes: ReactNode[];
  dir: string;
  fileOf: (path: string) => string;
  item: ClientConfigConfirmation;
  label: string;
  pathOf: (path: string) => string;
}) {
  const [expanded, setExpanded] = useState(false);
  // What the write sets comes before what it only takes out.
  const ordered = [...item.keys].sort(
    (a, b) => Number(a.removed) - Number(b.removed),
  );
  const paths = [...new Set(ordered.map((key) => key.path))];
  // A short list shows whole; folding one or two rows away saves nothing.
  const folds = !expanded && ordered.length > confirmRowsShown + 2;
  const keys = folds ? ordered.slice(0, confirmRowsShown) : ordered;
  const groups = paths
    .map((path) => ({ path, keys: keys.filter((key) => key.path === path) }))
    .filter((group) => group.keys.length > 0);
  const where = paths.length === 1 ? pathOf(paths[0]) : dir;
  return (
    <section
      className="min-w-0 rounded-md border text-xs"
      data-slot="client-confirmation"
    >
      <header className="flex min-w-0 items-center gap-2 px-2.5 py-2 font-medium text-foreground">
        <ClientMark client={item.client} size={16} />
        <span className="shrink-0">{label}</span>
        <code
          className="ml-auto min-w-0 truncate font-mono text-micro font-normal text-muted-foreground"
          title={where}
        >
          {where}
        </code>
      </header>
      {changes.length > 0 ? (
        <ul className="grid gap-0.5 border-t px-2.5 py-2 text-text-secondary">
          {changes.map((change, index) => (
            <li key={index}>{change}</li>
          ))}
        </ul>
      ) : null}
      {item.keys.length > 0 ? (
        <div className="border-t" data-slot="client-confirmation-keys">
          <div className={cn(confirmColumns, "pt-1.5 pb-0.5 text-micro")}>
            <span>{i18n.t("clientSetup.confirm.setting")}</span>
            <span>{i18n.t("clientSetup.confirm.now")}</span>
            <span>{i18n.t("clientSetup.confirm.next")}</span>
          </div>
          {groups.map((group) => (
            <div key={group.path} data-slot="client-confirmation-file">
              {paths.length > 1 ? (
                <div className="px-2.5 pt-1.5 font-mono text-micro text-muted-foreground">
                  {fileOf(group.path)}
                </div>
              ) : null}
              <dl>
                {group.keys.map((key) => (
                  <div key={key.key} className={cn(confirmColumns, "py-0.5")}>
                    <dt className="min-w-0">
                      <ConfirmValue
                        className="text-foreground"
                        value={key.key}
                      />
                    </dt>
                    <dd className="min-w-0">
                      <ConfirmValue value={key.current} />
                    </dd>
                    <dd className="min-w-0">
                      {key.removed ? (
                        <span className="text-warning-foreground">
                          {i18n.t("clientSetup.confirm.removed")}
                        </span>
                      ) : (
                        <ConfirmValue
                          className="text-foreground"
                          value={key.next}
                        />
                      )}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
          {folds ? (
            <Button
              className="mx-1 mb-1 text-muted-foreground"
              onClick={() => setExpanded(true)}
              size="xs"
              type="button"
              variant="ghost"
            >
              <ChevronDown aria-hidden="true" />
              {i18n.t("clientSetup.confirm.more", {
                count: item.keys.length - keys.length,
              })}
            </Button>
          ) : (
            <div className="h-1.5" />
          )}
        </div>
      ) : null}
    </section>
  );
}

function withCode(
  key: string,
  code: Record<string, string>,
  plain: Record<string, string> = {},
): ReactNode {
  const template = i18n.t(key, {
    ...plain,
    ...Object.fromEntries(
      Object.keys(code).map((name) => [name, `{{${name}}}`]),
    ),
  });
  return template.split(/\{\{(\w+)\}\}/).map((part, index) =>
    index % 2 === 1 ? (
      <code key={index} className="font-mono text-foreground">
        {code[part] ?? part}
      </code>
    ) : (
      part
    ),
  );
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

/**
 * A rail row's own button: the row sets its layout, so Button's size,
 * centering and hover give way to it.
 */
const ROW_BUTTON =
  "h-auto min-w-0 flex-1 justify-start rounded-sm p-0 text-left font-normal whitespace-normal hover:bg-transparent hover:text-inherit has-[>svg]:p-0 disabled:pointer-events-auto";

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
  attentionFirst = false,
  onClose,
}: {
  /** The token the dialog opens with; the user can pick another. */
  token: AccessTokenSummary;
  /** The tokens to pick from, which also name other tokens' configs. */
  tokens?: readonly AccessTokenSummary[];
  inferenceURL: string;
  conversionEngine?: ConversionEngineCapability | null;
  /** Lists and checks first the clients whose config needs attention. */
  attentionFirst?: boolean;
  onClose: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const tokenChoices = tokens.some((item) => item.id === token.id)
    ? tokens
    : [token, ...tokens];
  const [tokenChoice, setTokenChoice] = useState(token.id);
  const currentToken =
    tokenChoices.find((item) => item.id === tokenChoice) ?? token;
  const tokenId = currentToken.id;

  const [clientCatalog, setClientCatalog] = useState<ClientCatalog | null>(
    null,
  );
  const [overview, setOverview] = useState<ClientConfigOverview | null>(null);
  const [statusFailed, setStatusFailed] = useState(false);
  // Counts status reads, so the change preview follows each one.
  const [statusRead, setStatusRead] = useState(0);
  // Switching providers in CC Switch overwrites what AstrLink writes.
  const [ccSwitch, setCCSwitch] = useState(false);
  const [fullCatalog, setCatalog] = useState<ModelCatalog>({
    services: [],
    redirects: [],
  });
  // A limited token only reaches its providers' models; offer no others.
  const scopeKey =
    currentToken.service_scope === "selected"
      ? currentToken.service_ids.join("\n")
      : null;
  const catalog = useMemo<ModelCatalog>(() => {
    if (scopeKey === null) return fullCatalog;
    const allowed = new Set(scopeKey.split("\n"));
    return {
      ...fullCatalog,
      services: fullCatalog.services.filter((service) =>
        allowed.has(service.id),
      ),
    };
  }, [fullCatalog, scopeKey]);
  const [catalogStatus, setCatalogStatus] = useState<
    "loading" | "ready" | "error"
  >("loading");
  // The user's checks by place; unchecked places fall back to the defaults.
  const [checks, setChecks] = useState<Record<string, boolean>>({});
  const [selection, setSelection] = useState<string | null>(null);
  // What the user changed, by place. A place left alone shows its config.
  const [drafts, setDrafts] = useState<Record<string, ModelDraft>>({});
  // The model each guide client's values are shown for.
  const [guideModels, setGuideModels] = useState<Record<string, string>>({});
  // The protocol each link client's import link carries.
  const [linkProtocols, setLinkProtocols] = useState<Record<string, string>>(
    {},
  );
  const [linking, setLinking] = useState(false);
  const [query, setQuery] = useState("");
  const [openGroups, setOpenGroups] = useState<Record<RailGroup, boolean>>({
    installed: true,
    missing: false,
    guide: false,
    unsupported: false,
  });
  const [tab, setTab] = useState<Tab>("settings");
  const [busy, setBusy] = useState<"write" | "remove" | null>(null);
  const [writing, setWriting] = useState<readonly string[]>([]);
  const [error, setError] = useState<Failure | null>(null);
  const [confirmation, setConfirmation] = useState<{
    items: ClientConfigConfirmation[];
    plan: Planned[];
    retry: boolean;
  } | null>(null);
  const [outcomes, setOutcomes] = useState<Record<string, Outcome> | null>(
    null,
  );
  const [removal, setRemoval] = useState<ClientConfigStatus[] | null>(null);
  // The rail keeps its order and groups once known, so rows do not jump
  // after a write.
  const [frozen, setFrozen] = useState<{
    order: ClientConfigClient[];
    groups: Record<ClientConfigClient, RailGroup>;
  } | null>(null);
  const [official, setOfficial] = useState<
    Record<string, OfficialModelLimits | null>
  >({});
  const [proxyCheck, setProxyCheck] = useState<{
    url: string;
    result: ClientProxyCheck;
  } | null>(null);
  const [preview, setPreview] = useState<{
    key: string;
    results: { value: ClientConfigPreview | null; detail?: string }[];
  } | null>(null);
  const active = useRef(true);
  const pending = useRef(false);
  const officialAsked = useRef(new Set<string>());
  const prefilled = useRef(new Set<string>());
  const catalogRequest = useRef<Promise<ClientCatalog> | null>(null);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  // The catalog only changes with the app, so it is read once.
  const loadClients = useCallback(() => {
    catalogRequest.current ??= Promise.resolve()
      .then(() => getClientCatalog())
      .then(
        (value) => {
          rememberClientNames(value);
          if (active.current) setClientCatalog(value);
          return value;
        },
        (cause: unknown) => {
          catalogRequest.current = null;
          throw cause;
        },
      );
    return catalogRequest.current;
  }, []);

  const refresh = useCallback(async () => {
    try {
      const clients = await loadClients();
      const next = await getClientConfigStatus(inferenceURL || null, clients);
      if (!active.current) return;
      setOverview(next);
      setStatusFailed(false);
      setStatusRead((count) => count + 1);
    } catch {
      if (!active.current) return;
      setOverview((current) => current ?? emptyOverview);
      setStatusFailed(true);
    }
  }, [inferenceURL, loadClients]);

  useEffect(() => {
    void refresh();
    // CC Switch is optional; a failed check only drops its note.
    void Promise.resolve()
      .then(() => isCCSwitchInstalled())
      .then(
        (installed) => active.current && setCCSwitch(installed),
        () => undefined,
      );
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      Promise.resolve().then(() => listServices()),
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
      });
      setCatalogStatus(services.status === "fulfilled" ? "ready" : "error");
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const entries = useMemo(() => clientCatalog?.clients ?? [], [clientCatalog]);
  const entryById = useMemo(
    () => new Map(entries.map((entry) => [entry.id, entry])),
    [entries],
  );
  const entryOf = (client: ClientConfigClient) => entryById.get(client);
  const catalogKnown = catalogStatus === "ready";
  const available = useMemo(
    () =>
      catalogKnown
        ? availableModels(catalog.services, catalog.redirects)
        : null,
    [catalog, catalogKnown],
  );
  const candidates = useMemo(
    () =>
      Object.fromEntries(
        entries.map((entry) => [
          entry.id,
          modelCandidates(catalog, entry.protocols, conversionEngine),
        ]),
      ) as Record<ClientConfigClient, string[]>,
    [catalog, conversionEngine, entries],
  );
  const candidatesOf = (client: ClientConfigClient) => candidates[client] ?? [];
  // The protocol a model is written with: the client's only one, or the one
  // the gateway serves the model with best (see `modelProtocol`).
  const protocolOf = (entry: ClientCatalogEntry, model: string) =>
    entry.protocols.length > 1
      ? modelProtocol(catalog, model, entry.protocols, conversionEngine)
      : (entry.protocols[0] ?? "openai.chat");
  const protocolOfClient = (client: ClientConfigClient, model: string) => {
    const entry = entryOf(client);
    return entry ? protocolOf(entry, model) : "openai.chat";
  };

  const statuses = overview?.clients ?? [];
  const shown = statuses.filter(placeShown);
  // A paste client waits until the gateway records a request from it with
  // the token, made after the token or its address last changed.
  const [pasteSeen, setPasteSeen] = useState<Record<string, boolean>>({});
  const [pasteCopied, setPasteCopied] = useState<boolean | null>(null);
  useEffect(() => {
    const pending = (overview?.clients ?? []).filter(awaitsPaste);
    if (pending.length === 0) return;
    let cancelled = false;
    let timer = 0;
    const check = async () => {
      const seen = await Promise.all(
        pending.map(async (status) => {
          try {
            const page = await listRequestRecords({
              local_access_token_ids: [status.token_id ?? ""],
              client_type: pasteClientTypes[status.client],
              from: new Date(
                (status.paste_since_unix ?? 0) * 1000,
              ).toISOString(),
              limit: 1,
            });
            return [pasteKey(status), page.items.length > 0] as const;
          } catch {
            // Records it cannot read do not keep the client waiting.
            return [pasteKey(status), true] as const;
          }
        }),
      );
      if (cancelled) return;
      setPasteSeen(Object.fromEntries(seen));
      if (seen.every(([, done]) => done)) window.clearInterval(timer);
    };
    void check();
    timer = window.setInterval(() => void check(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [overview]);
  const waitsForPaste = (status: ClientConfigStatus) =>
    awaitsPaste(status) &&
    status.token_id === tokenId &&
    pasteSeen[pasteKey(status)] !== true;
  const attentionOf = (status: ClientConfigStatus) =>
    configAttention(status, tokenId, available);
  const badgeOf = (status: ClientConfigStatus) =>
    waitsForPaste(status)
      ? { tone: "pending" as const, label: t("clientSetup.state.needs_paste") }
      : configBadge(status, tokenId, attentionOf(status)?.missing);
  const needsAttention = (status: ClientConfigStatus) =>
    attentionFirst && attentionOf(status) !== null;

  // Rail order: the catalog's, with clients that need attention first when
  // the dialog opens on them. It freezes once statuses and models are known.
  const computedOrder = entries.map(({ id }) => id);
  if (attentionFirst) {
    const flagged = (client: ClientConfigClient) =>
      shown.some((status) => status.client === client && attentionOf(status));
    computedOrder.sort((a, b) => Number(flagged(b)) - Number(flagged(a)));
  }
  const layoutReady =
    clientCatalog !== null &&
    overview !== null &&
    !statusFailed &&
    catalogStatus !== "loading";
  useEffect(() => {
    if (!layoutReady || frozen !== null || !overview) return;
    setFrozen({
      order: computedOrder,
      groups: Object.fromEntries(
        entries.map((entry) => [entry.id, railGroupOf(entry, overview)]),
      ),
    });
    // The layout freezes once, when the statuses and models are known.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutReady, frozen]);
  const order = (frozen?.order ?? computedOrder).filter((id) =>
    entryById.has(id),
  );
  const groupOf = (entry: ClientCatalogEntry): RailGroup =>
    frozen?.groups[entry.id] ?? railGroupOf(entry, overview ?? emptyOverview);
  const railEntries = order.flatMap((id) => {
    const entry = entryById.get(id);
    return entry ? [entry] : [];
  });
  const placesOf = (client: ClientConfigClient) =>
    shown.filter((status) => status.client === client);
  /** A client's places, one list per home. */
  const homesOf = (client: ClientConfigClient) => {
    const homes = new Map<string, ClientConfigStatus[]>();
    for (const status of placesOf(client)) {
      const key = homeKey(status);
      homes.set(key, [...(homes.get(key) ?? []), status]);
    }
    return [...homes.values()];
  };
  const railPlaces = railEntries
    .filter(writesConfig)
    .flatMap((entry) => placesOf(entry.id));

  // Models the gateway no longer serves give way to what it serves now.
  const baseDraft = (status: ClientConfigStatus) =>
    initialDraft(
      {
        client: status.client,
        models: available
          ? Object.fromEntries(
              Object.entries(status.models).filter(
                ([key, value]) =>
                  typeof value !== "string" ||
                  key === "reasoningEffort" ||
                  available.has(value),
              ),
            )
          : status.models,
      },
      candidatesOf(status.client),
      catalog.services,
    );
  const draftOf = (status: ClientConfigStatus) =>
    drafts[placeKey(status)] ?? baseDraft(status);
  const listOf = (status: ClientConfigStatus) =>
    listSelection(
      draftOf(status),
      status,
      candidatesOf(status.client),
      catalogKnown,
    );
  /**
   * Edits the form a home shows. Its targets are written together, so the
   * edit applies to each of them.
   */
  const editDraft = (
    places: readonly ClientConfigStatus[],
    lead: ClientConfigStatus,
    patch: Partial<ModelDraft>,
    quiet = false,
  ) => {
    const base = draftOf(lead);
    const list = entryOf(lead.client)?.list ? listOf(lead) : null;
    setDrafts((current) => {
      const fields = current[placeKey(lead)] ?? base;
      // Limits describe a model; another model starts from its own.
      const limits =
        lead.client === "pi" &&
        patch.model !== undefined &&
        patch.model !== fields.model
          ? { contextWindow: "", maxTokens: "" }
          : {};
      const next = {
        ...fields,
        models: fields.models ?? list,
        ...limits,
        ...patch,
      };
      return {
        ...current,
        ...Object.fromEntries(places.map((place) => [placeKey(place), next])),
      };
    });
    if (!quiet) setOutcomes(null);
  };
  const modelsFor = (status: ClientConfigStatus): ClientConfigModels => {
    const entry = entryOf(status.client);
    return entry
      ? draftModels(entry, draftOf(status), listOf(status), (model) =>
          protocolOf(entry, model),
        )
      : {};
  };

  const isChecked = (status: ClientConfigStatus) =>
    placeWritable(status) &&
    (checks[placeKey(status)] ??
      checkedByDefault(status, tokenId, needsAttention(status)));
  // Unchecking a config this token wrote removes it in the same write.
  const isRemoval = (status: ClientConfigStatus) =>
    checks[placeKey(status)] === false && ownedConfig(status, tokenId);
  const setChecked = (
    places: readonly ClientConfigStatus[],
    value: boolean,
  ) => {
    const writable = places.filter(placeWritable);
    if (writable.length === 0) return;
    setChecks((current) => ({
      ...current,
      ...Object.fromEntries(
        writable.map((status) => [placeKey(status), value]),
      ),
    }));
    setOutcomes(null);
    setError(null);
  };
  const checkState = (
    places: readonly ClientConfigStatus[],
  ): boolean | "indeterminate" => {
    const writable = places.filter(placeWritable);
    const count = writable.filter(isChecked).length;
    return writable.length > 0 && count === writable.length
      ? true
      : count > 0
        ? "indeterminate"
        : false;
  };

  const writes = railPlaces.filter(isChecked);
  const removals = railPlaces.filter(isRemoval);
  const blocked = writes.filter(
    (status) => draftProblem(status.client, draftOf(status)) !== null,
  );
  const plan = (places: ClientConfigStatus[]): Planned[] =>
    places.flatMap((status): Planned[] =>
      isChecked(status)
        ? [{ status, action: "write", models: modelsFor(status) }]
        : isRemoval(status)
          ? [{ status, action: "remove" }]
          : [],
    );
  /** Each client and home once, however many targets it writes. */
  const homesIn = (places: readonly PlaceRef[]) => [
    ...new Map(places.map((place) => [homeKey(place), place])).values(),
  ];

  // The pane's subject: what the user picked, else what the dialog opens on.
  const homeSelection = (
    entry: ClientCatalogEntry,
    places: ClientConfigStatus[],
  ): Selected | null => {
    const lead = leadPlace(places, tokenId, needsAttention);
    return lead
      ? {
          kind: "home",
          key: homeKey(lead),
          entry,
          wsl: lead.wsl,
          places,
          lead,
        }
      : null;
  };
  const resolve = (key: string): Selected | null => {
    const cut = key.indexOf(":");
    const entry = entryOf(key.slice(0, cut));
    if (!entry) return null;
    if (!writesConfig(entry)) return { kind: "static", key, entry };
    const home = homesOf(entry.id).find((places) => homeKey(places[0]) === key);
    return home ? homeSelection(entry, home) : null;
  };
  const groupEntries = (group: RailGroup) =>
    railEntries.filter((entry) => groupOf(entry) === group);
  const defaultSelection = (): Selected | null => {
    if (!overview || statusFailed) return null;
    const installed = groupEntries("installed");
    const homes = installed
      .filter(writesConfig)
      .flatMap((entry) =>
        homesOf(entry.id).flatMap(
          (places) => homeSelection(entry, places) ?? [],
        ),
      );
    const withPlace = (test: (status: ClientConfigStatus) => boolean) =>
      homes.find((home) => home.kind === "home" && home.places.some(test)) ??
      null;
    const firstStatic = installed.find((entry) => !writesConfig(entry));
    return (
      (attentionFirst ? withPlace((status) => !!attentionOf(status)) : null) ??
      withPlace(isChecked) ??
      withPlace(placeWritable) ??
      homes[0] ??
      (firstStatic
        ? { kind: "static", key: staticKey(firstStatic.id), entry: firstStatic }
        : null) ??
      (railEntries[0] ? resolve(staticKey(railEntries[0].id)) : null) ??
      (railEntries[0]
        ? homesOf(railEntries[0].id).flatMap(
            (places) => homeSelection(railEntries[0], places) ?? [],
          )[0]
        : null) ??
      null
    );
  };
  const selected =
    (selection === null ? null : resolve(selection)) ?? defaultSelection();
  const selectedKey = selected?.key ?? "";
  const selectedHome = selected?.kind === "home" ? selected : null;
  const selectedDraft = selectedHome ? draftOf(selectedHome.lead) : null;
  const guideEntry =
    selected?.kind === "static" && selected.entry.kind === "guide"
      ? selected.entry
      : null;
  const guideModel = guideEntry
    ? (guideModels[guideEntry.id] ??
      defaultModel(candidatesOf(guideEntry.id), catalog.services))
    : "";

  // Official details: Pi's limits for its default model, the reasoning
  // levels of Codex's, and the context of a guide's model. Each model is
  // looked up once.
  const officialWanted = [
    ...shown
      .filter((status) => status.client === "pi")
      .map((status) => draftOf(status).model.trim()),
    selectedHome?.entry.id === "codex"
      ? (selectedDraft?.model.trim() ?? "")
      : "",
    guideModel.trim(),
  ]
    .filter(Boolean)
    .join("\n");
  useEffect(() => {
    for (const model of officialWanted.split("\n").filter(Boolean)) {
      if (officialAsked.current.has(model)) continue;
      officialAsked.current.add(model);
      void Promise.resolve()
        .then(() => getOfficialModelLimits(model))
        .then(
          (details) =>
            active.current &&
            setOfficial((current) => ({ ...current, [model]: details })),
          () =>
            active.current &&
            setOfficial((current) => ({ ...current, [model]: null })),
        );
    }
  }, [officialWanted]);
  // Pi's limits start from the official model's; the user can change them
  // or clear them for Pi's defaults.
  useEffect(() => {
    for (const status of shown.filter((item) => item.client === "pi")) {
      const draft = draftOf(status);
      const model = draft.model.trim();
      const details = official[model];
      const mark = `${placeKey(status)}\n${model}`;
      if (
        !model ||
        draft.contextWindow ||
        draft.maxTokens ||
        details === undefined ||
        prefilled.current.has(mark)
      ) {
        continue;
      }
      prefilled.current.add(mark);
      const tokens = (count: number) => (count > 0 ? String(count) : "");
      if (!details || (!details.contextTokens && !details.outputTokens)) {
        continue;
      }
      editDraft(
        [status],
        status,
        {
          contextWindow: tokens(details.contextTokens),
          maxTokens: tokens(details.outputTokens),
        },
        true,
      );
    }
    // Runs as details arrive and as Pi's drafts change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [official, drafts, overview, catalog]);

  // The system proxy applies to this computer's Codex, not to one in WSL.
  const checksProxy =
    selectedHome?.entry.id === "codex" &&
    selectedHome.wsl === null &&
    inferenceURL !== "";
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

  // The change preview of the selected home's places, as they would be
  // written now.
  const previewPlaces =
    tab === "changes" && selectedHome
      ? selectedHome.places.filter(
          (status) =>
            (isChecked(status) &&
              draftProblem(status.client, draftOf(status)) === null) ||
            isRemoval(status),
        )
      : [];
  const previewRequests = previewPlaces.map((status) => ({
    tokenId,
    client: status.client,
    wsl: status.wsl,
    target: status.target,
    inferenceUrl: inferenceURL,
    ...(isChecked(status)
      ? { action: "write" as const, models: modelsFor(status) }
      : { action: "remove" as const, models: {} }),
  }));
  const previewKey =
    previewRequests.length > 0
      ? JSON.stringify({ requests: previewRequests, read: statusRead })
      : "";
  useEffect(() => {
    if (!previewKey) return;
    let cancelled = false;
    const { requests } = JSON.parse(previewKey) as {
      requests: Parameters<typeof previewClientConfigChanges>[0][];
    };
    void Promise.all(
      requests.map((request) =>
        Promise.resolve()
          .then(() => previewClientConfigChanges(request))
          .then(
            (value) => ({ value }),
            (cause: unknown) => ({
              value: null,
              detail: cause instanceof Error ? cause.message : undefined,
            }),
          ),
      ),
    ).then((results) => {
      if (!cancelled) setPreview({ key: previewKey, results });
    });
    return () => {
      cancelled = true;
    };
  }, [previewKey]);
  const shownPreview = preview?.key === previewKey ? preview : null;

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

  // Read a fresh record and patch against its ETag so concurrent edits
  // survive; each provider gets one patch for every entry it needs.
  const enableEntries = async (items: Planned[]): Promise<boolean> => {
    const needed = new Map<string, { service: Service; protocols: string[] }>();
    for (const item of items) {
      const models = item.models ?? {};
      for (const model of configModels(models)) {
        const protocol =
          models.modelProtocols?.[model] ??
          protocolOfClient(item.status.client, model);
        const entry = requiredEntry(catalog, model, protocol, conversionEngine);
        if (!entry) continue;
        const current = needed.get(entry.service.id) ?? {
          service: entry.service,
          protocols: [],
        };
        if (!current.protocols.includes(protocol)) {
          current.protocols.push(protocol);
        }
        needed.set(entry.service.id, current);
      }
    }
    for (const { service, protocols } of needed.values()) {
      try {
        const record = await getService(service.id);
        if (!record.service.enabled) {
          throw new Error(t("clientSetup.entryRequired"));
        }
        const added: ServiceCapability[] = [];
        for (const protocol of protocols) {
          if (hasEntry(record.service, protocol)) continue;
          const convertTo = entryTarget(
            record.service,
            protocol,
            conversionEngine,
          );
          if (!convertTo) throw new Error(t("clientSetup.entryRequired"));
          added.push({
            protocol,
            mode: "native",
            streaming: true,
            convert_to: convertTo,
          });
        }
        if (added.length > 0) {
          const updated = await updateService(service.id, record.etag, {
            capabilities: [...record.service.capabilities, ...added],
          });
          record.service = updated.service;
        }
        if (active.current)
          setCatalog((current) => ({
            ...current,
            services: current.services.map((item) =>
              item.id === service.id ? record.service : item,
            ),
          }));
      } catch (cause) {
        if (active.current)
          setError(
            failure(
              t("clientSetup.entryEnableFailed", {
                service: service.name,
                protocol: joinNames(protocols.map(protocolLabel)),
              }),
              cause,
            ),
          );
        return false;
      }
    }
    if (needed.size > 0) {
      // The fresh patched records above also keep the marks right if this fails.
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

  const apply = async (
    items: Planned[],
    confirmed: boolean,
    retry: boolean,
  ) => {
    if (items.length === 0 || !begin("write")) return;
    setWriting(items.map((item) => placeKey(item.status)));
    try {
      // Open missing entries first, so no client is pointed at a model the
      // gateway cannot serve it.
      const writes = items.filter((item) => item.action === "write");
      if (!(await enableEntries(writes))) return;
      const request: ClientConfigItem[] = items.map(
        ({ status, action, models }) => {
          const place = {
            client: status.client,
            wsl: status.wsl,
            target: status.target,
          };
          return action === "write"
            ? { ...place, action, models }
            : { ...place, action };
        },
      );
      const outcome = await applyClientConfigs({
        tokenId,
        inferenceUrl: inferenceURL,
        items: request,
        confirmed,
      });
      if (!active.current) return;
      if (outcome.status === "needs_confirmation") {
        setConfirmation({ items: outcome.items, plan: items, retry });
        return;
      }
      const next = Object.fromEntries(
        outcome.results.map((result, index) => [
          placeKey(items[index].status),
          {
            result,
            before: items[index].status,
            models: items[index].models,
          },
        ]),
      );
      setOutcomes((current) =>
        retry && current ? { ...current, ...next } : next,
      );
      if (outcome.results.some((result) => result.ok)) clientConfigsChanged();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.writeFailed"), cause));
    } finally {
      if (active.current) setWriting([]);
      finish();
    }
  };

  const removePlaces = async (places: ClientConfigStatus[]) => {
    if (places.length === 0 || !begin("remove")) return;
    try {
      for (const status of places) {
        await removeClientConfig(status.client, status.wsl, status.target);
      }
      if (!active.current) return;
      notify.success(
        t("clientSetup.removed", {
          client: placeLabel(places[0].client, places[0].wsl),
        }),
      );
      // Removed on purpose, so the next write leaves them alone.
      setChecks((current) => ({
        ...current,
        ...Object.fromEntries(
          places.map((status) => [placeKey(status), false]),
        ),
      }));
      setOutcomes(null);
      clientConfigsChanged();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.removeFailed"), cause));
    } finally {
      finish();
    }
  };

  const switchToken = (next: string) => {
    // Radix reports a blank while its options settle.
    if (!next || next === tokenId || pending.current) return;
    setTokenChoice(next);
    setChecks({});
    setOutcomes(null);
    setError(null);
    setFrozen(null);
    void refresh();
  };

  const working = busy !== null;
  const otherTokenName = (status: ClientConfigStatus) =>
    tokenChoices.find((item) => item.id === status.token_id)?.name;
  // Another token's config is removed from that token's dialog. A token this
  // gateway lacks has no dialog, so its config can be removed from any. A
  // config set up by hand has no record of what AstrLink would put back.
  const removable = (status: ClientConfigStatus) =>
    status.token_id !== null &&
    status.state !== "invalid" &&
    status.state !== "manual" &&
    (status.token_id === tokenId || !otherTokenName(status));

  const listCount = (status: ClientConfigStatus) =>
    new Set([...listOf(status), draftOf(status).model.trim()].filter(Boolean))
      .size;
  const outcomeLine = (outcome: Outcome) => {
    const { result, before, models } = outcome;
    if (!result.ok) {
      return result.error?.line != null && result.error.path
        ? t("clientSetup.result.fileInvalid", {
            file: fileName(result.error.path),
          })
        : t(
            result.action === "remove"
              ? "clientSetup.result.notRemoved"
              : "clientSetup.result.notWritten",
          );
    }
    if (result.action === "remove") return t("clientSetup.result.removed");
    if (before.token_id === tokenId && before.state === "outdated") {
      return t("clientSetup.result.portUpdated");
    }
    if (ownedConfig(before, tokenId)) return t("clientSetup.result.updated");
    const what = entryOf(before.client)?.list
      ? t("clientSetup.modelCount", {
          count: new Set(
            [...(models?.models ?? []), models?.model].filter(Boolean),
          ).size,
        })
      : (models?.model ?? "");
    return what
      ? t("clientSetup.result.writtenWith", { what })
      : t("clientSetup.result.written");
  };
  const notFoundLine = (status: ClientConfigStatus) =>
    t(
      status.wsl === null
        ? "clientSetup.notFoundLocal"
        : "clientSetup.notFound",
      {
        dir: homePath(status.dir, status.home),
      },
    );
  const subLine = (status: ClientConfigStatus) => {
    const outcome = outcomes?.[placeKey(status)];
    if (waitsForPaste(status) && (!outcome || outcome.result.ok)) {
      return t("clientSetup.result.writtenWith", {
        what: t("clientSetup.paste.waiting"),
      });
    }
    if (outcome) return outcomeLine(outcome);
    if (!status.detected && status.state === "not_configured") {
      return notFoundLine(status);
    }
    if (status.state === "not_configured") {
      const what = entryOf(status.client)?.list
        ? t("clientSetup.modelCount", { count: listCount(status) })
        : draftOf(status).model.trim();
      return isChecked(status) && what
        ? t("clientSetup.willWrite", { what })
        : t("clientSetup.notConfigured");
    }
    if (
      status.token_id !== null &&
      status.token_id !== tokenId &&
      ["configured", "outdated", "modified", "manual"].includes(status.state)
    ) {
      const name = otherTokenName(status);
      return name
        ? t("clientSetup.usingToken", { name })
        : t("clientSetup.usingOtherToken");
    }
    if (isRemoval(status)) return t("clientSetup.willRemove");
    return (
      status.models.model ??
      (status.models.models
        ? t("clientSetup.modelCount", { count: status.models.models.length })
        : homePath(status.dir, status.home))
    );
  };
  /** A home's line: its only place's, or its state and the targets written. */
  const homeLine = (places: ClientConfigStatus[]) => {
    const lead = leadPlace(places, tokenId, needsAttention);
    if (!lead) return "";
    if (places.length === 1) return subLine(lead);
    const done = places.flatMap((status) => {
      const outcome = outcomes?.[placeKey(status)];
      return outcome ? [outcome] : [];
    });
    const shownOutcome = done.find((outcome) => !outcome.result.ok) ?? done[0];
    if (shownOutcome) {
      return t("clientSetup.targetLine", {
        state: outcomeLine(shownOutcome),
        targets: targetName(lead.client, shownOutcome.before.target ?? ""),
      });
    }
    if (!places.some(placeWritable) && lead.state === "not_configured") {
      return notFoundLine(lead);
    }
    const checked = places.filter(isChecked);
    if (checked.length === 0) return subLine(lead);
    return t("clientSetup.targetLine", {
      state:
        lead.state === "not_configured"
          ? t("clientSetup.notConfigured")
          : (badgeOf(lead)?.label ?? t("clientSetup.notConfigured")),
      targets: joinNames(
        checked.map((status) => targetName(status.client, status.target ?? "")),
      ),
    });
  };
  const homeBadge = (places: ClientConfigStatus[]) => {
    const failedOne = places
      .map((status) => outcomes?.[placeKey(status)])
      .find((outcome) => outcome && !outcome.result.ok);
    if (failedOne) {
      return {
        tone: "negative" as const,
        label: t(
          failedOne.result.action === "remove"
            ? "clientSetup.result.notRemoved"
            : "clientSetup.result.notWritten",
        ),
      };
    }
    const lead = leadPlace(places, tokenId, needsAttention);
    return lead ? badgeOf(lead) : null;
  };
  const homeEnd = (places: ClientConfigStatus[]) => {
    const keys = places.map(placeKey);
    if (keys.some((key) => writing.includes(key))) {
      return (
        <LoaderCircle
          aria-label={t("clientSetup.writing")}
          className="size-4 shrink-0 animate-spin text-tide motion-reduce:animate-none"
        />
      );
    }
    const done = keys.flatMap((key) => outcomes?.[key] ?? []);
    if (done.length > 0) {
      return done.every((outcome) => outcome.result.ok) ? (
        <CircleCheck
          aria-label={t("clientSetup.result.ok")}
          className="size-4 shrink-0 text-success"
        />
      ) : (
        <BadgeAlert
          aria-label={t("clientSetup.result.failed")}
          className="size-4 shrink-0 text-destructive"
        />
      );
    }
    const lead = leadPlace(places, tokenId, needsAttention);
    const badge = lead ? badgeOf(lead) : null;
    return badge ? (
      <StatusBadge tone={badge.tone}>{badge.label}</StatusBadge>
    ) : null;
  };

  const select = (key: string) => setSelection(key);

  // 全选 covers the installed clients a write can go to, as the search
  // shows them.
  const checkable = groupEntries("installed")
    .filter((entry) => writesConfig(entry) && clientMatches(entry, query))
    .flatMap((entry) => placesOf(entry.id))
    .filter(placeWritable);
  const allChecked =
    checkable.length > 0 && checkable.every((status) => isChecked(status));

  const rowClass = (isSelected: boolean) =>
    cn(
      "flex min-w-0 items-center gap-2.25 rounded-md px-2 py-1.25",
      isSelected && "bg-accent shadow-[inset_2px_0_0_var(--primary)]",
    );
  const logo = (client: ClientConfigClient) => (
    <span className="flex size-6 shrink-0 items-center justify-center rounded-md border bg-card">
      <ClientMark client={client} size={15} />
    </span>
  );
  const names = (client: ClientConfigClient, sub: ReactNode) => (
    <span className="grid min-w-0 flex-1">
      <span className="truncate text-sm leading-[1.3] font-medium">
        {clientLabel(client)}
      </span>
      <span className="truncate text-micro leading-[1.3] text-muted-foreground">
        {sub}
      </span>
    </span>
  );

  /** A client AstrLink writes: checked per place, a sub-row per WSL home. */
  const writtenRow = (entry: ClientCatalogEntry): ReactNode => {
    const client = entry.id;
    const name = entry.name;
    const homes = homesOf(client);
    const places = homes.flat();
    const several = homes.length > 1;
    const only = several ? undefined : homes[0];
    const state = checkState(places);
    const isSelected =
      !several && only !== undefined && selectedKey === homeKey(only[0]);
    const disabled = !places.some(placeWritable);
    const sub = !only?.length
      ? !several && !statusFailed
        ? t("clientSetup.statusLoading")
        : several
          ? joinNames(homes.map((home) => placeName(home[0].wsl)))
          : ""
      : homeLine(only);
    return (
      <li key={client} className="grid gap-0.5">
        <div
          className={rowClass(isSelected)}
          data-client={client}
          data-selected={isSelected ? "true" : undefined}
        >
          <Checkbox
            aria-label={t("clientSetup.deliver", { client: name })}
            checked={state}
            disabled={working || disabled}
            onCheckedChange={() => setChecked(places, state !== true)}
          />
          <Button
            aria-current={isSelected ? "true" : undefined}
            className={cn(ROW_BUTTON, "gap-2.25", disabled && "opacity-55")}
            disabled={places.length === 0}
            onClick={() => {
              const home =
                homes.find((item) => homeKey(item[0]) === selectedKey) ??
                homes.find((item) => item.some(placeWritable)) ??
                homes[0];
              if (home) select(homeKey(home[0]));
            }}
            type="button"
            variant="ghost"
          >
            {logo(client)}
            {names(
              client,
              several
                ? joinNames(homes.map((home) => placeName(home[0].wsl)))
                : sub,
            )}
            {only ? homeEnd(only) : null}
          </Button>
        </div>
        {several
          ? homes.map((home) => {
              const key = homeKey(home[0]);
              const homeSelected = key === selectedKey;
              const homeState = checkState(home);
              const writable = home.some(placeWritable);
              return (
                <div
                  key={key}
                  className={cn(
                    "flex min-w-0 items-center gap-2.25 rounded-md py-1 pr-2 pl-[2.375rem]",
                    homeSelected &&
                      "bg-accent shadow-[inset_2px_0_0_var(--primary)]",
                  )}
                  data-place={key}
                  data-selected={homeSelected ? "true" : undefined}
                >
                  <Checkbox
                    aria-label={t("clientSetup.deliver", {
                      client: placeLabel(client, home[0].wsl),
                    })}
                    checked={homeState}
                    disabled={working || !writable}
                    onCheckedChange={() => setChecked(home, homeState !== true)}
                  />
                  <Button
                    aria-current={homeSelected ? "true" : undefined}
                    className={cn(
                      ROW_BUTTON,
                      "gap-2",
                      !writable && "opacity-55",
                    )}
                    onClick={() => select(key)}
                    type="button"
                    variant="ghost"
                  >
                    <span className="grid min-w-0 flex-1">
                      <span className="truncate text-xs leading-[1.3] font-medium">
                        {placeName(home[0].wsl)}
                      </span>
                      <span className="truncate text-micro leading-[1.3] text-muted-foreground">
                        {homeLine(home)}
                      </span>
                    </span>
                    {homeEnd(home)}
                  </Button>
                </div>
              );
            })
          : null}
      </li>
    );
  };

  /** A client AstrLink only lists: not checkable, its reason as the line. */
  const listedRow = (entry: ClientCatalogEntry): ReactNode => {
    const key = staticKey(entry.id);
    const isSelected = selectedKey === key;
    const Lead =
      entry.kind === "link"
        ? ArrowUpRight
        : entry.kind === "guide"
          ? Hand
          : Ban;
    const sub =
      entry.kind === "link"
        ? t("clientSetup.importIn", { client: entry.name })
        : clientReason(entry.id);
    return (
      <li key={entry.id}>
        <div
          className={cn(
            rowClass(isSelected),
            entry.kind === "unsupported" && "opacity-55",
          )}
          data-client={entry.id}
          data-kind={entry.kind}
          data-selected={isSelected ? "true" : undefined}
        >
          <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground">
            <Lead aria-hidden="true" className="size-3.5" />
          </span>
          <Button
            aria-current={isSelected ? "true" : undefined}
            className={cn(ROW_BUTTON, "gap-2.25")}
            onClick={() => select(key)}
            type="button"
            variant="ghost"
          >
            {logo(entry.id)}
            {names(entry.id, sub)}
          </Button>
        </div>
      </li>
    );
  };

  const searching = query.trim() !== "";
  const railBody = railGroups.map((group) => {
    const members = groupEntries(group);
    const matches = searching
      ? members.filter((entry) => clientMatches(entry, query))
      : members;
    if (members.length === 0 || (searching && matches.length === 0)) {
      return null;
    }
    const open = searching || openGroups[group];
    const listId = `client-setup-group-${group}`;
    const Chevron = open ? ChevronDown : ChevronRight;
    return (
      <li key={group} className="grid gap-px" data-group={group}>
        <div className="flex min-w-0 items-center gap-1.5 rounded-md px-2 pt-1.5 pb-0.75 text-micro font-semibold text-text-secondary">
          <Button
            aria-controls={open ? listId : undefined}
            aria-expanded={open}
            className={cn(ROW_BUTTON, "gap-1.5 font-semibold text-inherit")}
            disabled={searching}
            onClick={() =>
              setOpenGroups((current) => ({
                ...current,
                [group]: !current[group],
              }))
            }
            type="button"
            variant="ghost"
          >
            <Chevron
              aria-hidden="true"
              className="size-3.25 shrink-0 text-muted-foreground"
            />
            <span className="truncate">{t(`clientSetup.group.${group}`)}</span>
            <span className="font-normal text-muted-foreground tabular-nums">
              {matches.length}
            </span>
          </Button>
          {group === "installed" && open && checkable.length > 0 ? (
            <Button
              className="-my-1"
              disabled={working}
              onClick={() => setChecked(checkable, !allChecked)}
              size="xs"
              type="button"
              variant="ghost"
            >
              {t(allChecked ? "clientSetup.checkNone" : "clientSetup.checkAll")}
            </Button>
          ) : null}
        </div>
        {open ? (
          <ul className="grid gap-px" id={listId}>
            {matches.map((entry) =>
              writesConfig(entry) ? writtenRow(entry) : listedRow(entry),
            )}
          </ul>
        ) : null}
      </li>
    );
  });

  // The pane's notes on a home's lead place, before its settings.
  const notes = (status: ClientConfigStatus): ReactNode[] => {
    const label = clientLabel(status.client);
    const own = status.token_id === tokenId;
    const items: ReactNode[] = [];
    if (status.state === "invalid") {
      items.push(
        <FormMessage key="state" tone="error">
          {t("clientSetup.invalidHint")}
        </FormMessage>,
      );
    } else if (status.state === "elsewhere") {
      items.push(
        <FormMessage key="state" tone="notice">
          {t("clientSetup.elsewhereHint", { client: label })}
        </FormMessage>,
      );
    } else if (status.token_id !== null && !own) {
      const name = otherTokenName(status);
      items.push(
        <FormMessage key="state" tone="notice">
          {name
            ? t("clientSetup.usesToken", {
                client: label,
                name,
                current: currentToken.name,
              })
            : t("clientSetup.usesOtherToken", {
                client: label,
                current: currentToken.name,
              })}
        </FormMessage>,
      );
    } else if (own && status.state === "modified") {
      items.push(
        <FormMessage key="state" tone="warning">
          {t("clientSetup.modifiedHint")}
        </FormMessage>,
      );
    } else if (own && status.state === "manual") {
      items.push(
        <FormMessage key="state" tone="notice">
          {t("clientSetup.manualHint")}
        </FormMessage>,
      );
    }
    if (waitsForPaste(status)) {
      items.push(
        <FormMessage
          key="paste"
          className="flex items-start gap-2"
          tone="warning"
        >
          <Key aria-hidden="true" className="mt-px size-3.5 shrink-0" />
          <span>
            {t(`clientSetup.paste.${status.client}`, {
              providers: joinNames(status.paste_into),
            })}
          </span>
        </FormMessage>,
        <div key="paste-copy">
          <Button
            onClick={() => {
              void copyAccessToken(tokenId).then((copied) => {
                setPasteCopied(copied);
                window.setTimeout(() => setPasteCopied(null), 2000);
              });
            }}
            size="sm"
            type="button"
            variant="outline"
          >
            {pasteCopied ? (
              <Check aria-hidden="true" className="size-3.5" />
            ) : (
              <Copy aria-hidden="true" className="size-3.5" />
            )}
            {pasteCopied === null
              ? t("clientSetup.paste.copy")
              : pasteCopied
                ? t("clientSetup.paste.copied")
                : t("tokens.copyFailed")}
          </Button>
        </div>,
      );
    }
    for (const note of status.notes) {
      const text =
        optional(`clientSetup.notes.${status.client}.${note}`, {
          client: label,
        }) || optional(`clientSetup.notes.${note}`, { client: label });
      if (text) {
        items.push(
          <FormMessage key={`note:${note}`} tone="warning">
            {text}
          </FormMessage>,
        );
      }
    }
    const missing = attentionOf(status)?.missing ?? [];
    if (missing.length > 0) {
      items.push(
        <FormMessage key="missing" tone="warning">
          {t("clientSetup.modelMissingHint", {
            count: missing.length,
            models: joinNames(missing),
          })}
        </FormMessage>,
      );
    }
    const firstProtocol = entryOf(status.client)?.protocols[0] ?? "";
    if (status.client === "codex" && checksProxy && proxyCheck) {
      const hint =
        proxyCheck.url === inferenceURL
          ? proxyHint(
              proxyCheck.result,
              protocolBaseURL(firstProtocol, inferenceURL),
            )
          : null;
      if (hint) {
        items.push(
          <FormMessage key="proxy" tone={hint.tone}>
            {hint.message}
          </FormMessage>,
        );
      }
    }
    if (status.wsl !== null && !overview?.wsl_localhost) {
      items.push(
        <FormMessage key="wsl" tone="warning">
          {t("clientSetup.wslNetwork", {
            url: protocolBaseURL(firstProtocol, inferenceURL),
          })}
        </FormMessage>,
      );
    }
    return items;
  };

  // Footer: what the next write does, or how the last one went.
  const doneOutcomes = outcomes ? Object.values(outcomes) : [];
  const failed = doneOutcomes.filter((outcome) => !outcome.result.ok);
  const labelOf = (place: PlaceRef) =>
    placeLabel(place.client, place.wsl, place.target);
  const failedNames = (action: ClientConfigAction) =>
    joinNames(
      failed
        .filter(({ result }) => result.action === action)
        .map(({ before }) => labelOf(before)),
    );
  const retryLabel =
    failed.length === 1
      ? t("clientSetup.retryOne", { client: labelOf(failed[0].before) })
      : t("clientSetup.retryCount", { count: failed.length });

  const failedOutcome = selectedHome
    ? selectedHome.places
        .map((status) => outcomes?.[placeKey(status)])
        .find((outcome) => outcome && !outcome.result.ok)
    : undefined;
  const failedBanner =
    selectedHome && failedOutcome ? (
      <FormMessage tone="error">
        {failedOutcome.result.error?.path ? (
          <strong className="font-semibold">
            {homePath(
              failedOutcome.result.error.path,
              selectedHome.lead.home,
            )}{" "}
          </strong>
        ) : null}
        {failedOutcome.result.error?.line != null &&
        failedOutcome.result.error.path
          ? t("clientSetup.result.lineError", {
              line: failedOutcome.result.error.line,
              untouched: t(
                failedOutcome.before.paths.length > 1
                  ? "clientSetup.result.untouchedAll"
                  : "clientSetup.result.untouchedOne",
              ),
              action: retryLabel,
            })
          : (failedOutcome.result.error?.message ?? "")}
      </FormMessage>
    ) : null;

  const notInstalledHint = (status: ClientConfigStatus) => (
    <FormMessage tone="notice">
      {t("clientSetup.notInstalledHint", {
        client: clientLabel(status.client),
        dir: homePath(status.dir, status.home),
      })}
    </FormMessage>
  );

  /** One checkbox per write target, the uninstalled ones dimmed. */
  const targetsField = (home: Extract<Selected, { kind: "home" }>) => {
    const fields = (
      <Field group label={t("clientSetup.targets")}>
        <div className="grid gap-1.5">
          {home.places.map((status) => {
            const writable = placeWritable(status);
            const missingClient =
              !status.detected && status.state === "not_configured";
            const badge = missingClient ? null : badgeOf(status);
            return (
              <div
                key={placeKey(status)}
                className={cn(
                  "flex min-w-0 items-center gap-2.5 rounded-md border px-2.5 py-1.75 text-xs leading-normal font-normal",
                  !writable && "opacity-55",
                )}
                data-place={placeKey(status)}
              >
                <Checkbox
                  aria-label={t("clientSetup.deliver", {
                    client: labelOf(status),
                  })}
                  checked={isChecked(status)}
                  disabled={working || !writable}
                  onCheckedChange={(checked) =>
                    setChecked([status], checked === true)
                  }
                />
                <span className="shrink-0 font-medium text-foreground">
                  {targetName(status.client, status.target ?? "")}
                </span>
                <code
                  className="min-w-0 truncate font-mono text-muted-foreground"
                  title={status.paths
                    .map((path) => homePath(path, status.home))
                    .join("\n")}
                >
                  {homePath(status.paths[0], status.home)}
                </code>
                <span className="flex-1" />
                {missingClient ? (
                  <span className="shrink-0 text-muted-foreground">
                    {t("clientSetup.notInstalled")}
                  </span>
                ) : badge ? (
                  <StatusBadge tone={badge.tone}>{badge.label}</StatusBadge>
                ) : null}
              </div>
            );
          })}
        </div>
      </Field>
    );
    if (home.places.length <= 2) return fields;
    const selectedTargets = home.places
      .filter(isChecked)
      .map((status) => targetName(status.client, status.target ?? ""));
    return (
      <Collapsible key={homeKey(home.lead)}>
        <CollapsibleTrigger asChild>
          <Button
            className="w-full justify-start"
            data-testid="client-setup-targets-toggle"
            size="sm"
            type="button"
            variant="outline"
          >
            <ChevronRight aria-hidden="true" className="size-3.5 shrink-0" />
            <span className="truncate">
              {t("clientSetup.targets")}
              {selectedTargets.length > 0
                ? ` · ${joinNames(selectedTargets)}`
                : ""}
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="mt-2">{fields}</CollapsibleContent>
      </Collapsible>
    );
  };

  const settingsBody = (home: Extract<Selected, { kind: "home" }>) => {
    const { lead, places, entry } = home;
    const draft = draftOf(lead);
    const note = optional(`clientSetup.note.${entry.id}`);
    const installed = places.some(
      (status) => status.detected || status.state !== "not_configured",
    );
    return (
      <>
        {places.length > 1 ? targetsField(home) : null}
        {!installed ? (
          notInstalledHint(lead)
        ) : (
          <>
            {notes(lead)}
            {lead.state !== "invalid" ? (
              <ClientSettingsForm
                candidates={candidatesOf(entry.id)}
                catalog={catalog}
                catalogStatus={catalogStatus}
                disabled={working}
                draft={draft}
                efforts={reasoningEffortOptions(
                  official[draft.model.trim()]?.reasoningLevels,
                  draft.reasoningEffort,
                )}
                engine={conversionEngine}
                entry={entry}
                list={listOf(lead)}
                onEdit={(patch) => editDraft(places, lead, patch)}
                protocolOf={(model) => protocolOf(entry, model)}
              />
            ) : null}
            {waitsForPaste(lead) ? (
              <p className="text-xs text-muted-foreground">
                {t("clientSetup.paste.clears", {
                  client: clientLabel(lead.client),
                })}
              </p>
            ) : null}
            {note ? <FormMessage tone="notice">{note}</FormMessage> : null}
          </>
        )}
      </>
    );
  };

  const previewBody = (
    status: ClientConfigStatus,
    result: { value: ClientConfigPreview | null; detail?: string },
  ) => {
    const label = labelOf(status);
    if (!result.value) {
      return (
        <FormMessage tone="error">
          {t("clientSetup.changesFailed")}
          {result.detail ? (
            <span className="mt-1 block break-all">{result.detail}</span>
          ) : null}
        </FormMessage>
      );
    }
    const { files, replaced, restored } = result.value;
    return (
      <>
        {files.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t("clientSetup.changesNone", { client: label })}
          </p>
        ) : (
          files.map((file) => (
            <FileDiff
              key={file.path}
              badge={
                <StatusBadge tone="neutral">
                  {file.deleted
                    ? t("clientSetup.changeDeleted")
                    : file.created
                      ? t("clientSetup.changeCreated")
                      : t(
                          file.unit === "line"
                            ? "clientSetup.changedLines"
                            : "clientSetup.changedKeys",
                          { count: file.changes },
                        )}
                </StatusBadge>
              }
              lines={file.lines}
              path={homePath(file.path, status.home)}
            />
          ))
        )}
        {replaced > 0 ? (
          <FormMessage tone="warning">
            {t("clientSetup.replacedNote", { count: replaced })}
          </FormMessage>
        ) : null}
        {restored > 0 ? (
          <FormMessage tone="notice">
            {t("clientSetup.restoredNote", { count: restored })}
          </FormMessage>
        ) : null}
      </>
    );
  };

  const changesBody = (home: Extract<Selected, { kind: "home" }>) => {
    const { lead, places } = home;
    const label = placeLabel(lead.client, lead.wsl);
    if (!places.some(placeWritable)) {
      return lead.state === "invalid" ? (
        <FormMessage tone="error">{t("clientSetup.invalidHint")}</FormMessage>
      ) : (
        notInstalledHint(lead)
      );
    }
    if (!places.some((status) => isChecked(status) || isRemoval(status))) {
      return (
        <p className="text-xs text-muted-foreground">
          {t("clientSetup.changesUnchecked", { client: label })}
        </p>
      );
    }
    if (previewPlaces.length === 0) {
      return (
        <p className="text-xs text-muted-foreground">
          {t("clientSetup.changesNeedModel")}
        </p>
      );
    }
    if (!shownPreview) {
      return (
        <p className="text-xs text-muted-foreground" role="status">
          {t("clientSetup.changesLoading")}
        </p>
      );
    }
    if (places.length === 1) {
      return previewBody(previewPlaces[0], shownPreview.results[0]);
    }
    return previewPlaces.map((status, index) => (
      <section
        key={placeKey(status)}
        aria-label={labelOf(status)}
        className="grid gap-2"
      >
        <h4 className="text-xs font-medium text-text-secondary">
          {targetName(status.client, status.target ?? "")}
        </h4>
        {previewBody(status, shownPreview.results[index])}
      </section>
    ));
  };

  const helpFor = (client: ClientConfigClient) => {
    const limits = optional(`clientSetup.limits.${client}`);
    if (!limits) return null;
    return (
      <HelpPopover inDialog label={t("clientSetup.limitsTitle")}>
        <div className="grid gap-2">
          <p>{limits}</p>
          {client === "codex" ? <p>{t("clientSetup.codexTools")}</p> : null}
          {client === "pi" ? <p>{t("clientSetup.limitsHint")}</p> : null}
        </div>
      </HelpPopover>
    );
  };

  const paneHeader = (
    client: ClientConfigClient,
    title: string,
    end: ReactNode,
  ) => (
    <div className="flex min-w-0 flex-wrap items-center gap-2.5 px-5 pt-3 pb-2.5">
      <ClientMark client={client} size={22} />
      <h3 className="truncate text-base font-semibold">{title}</h3>
      {end}
    </div>
  );

  const homePane = (home: Extract<Selected, { kind: "home" }>) => {
    const { lead, places } = home;
    const title = placeLabel(lead.client, lead.wsl);
    // The rail tells this state in its sub-line; the pane header names it.
    const badge =
      homeBadge(places) ??
      (lead.state === "not_configured"
        ? { tone: "neutral" as const, label: t("clientSetup.notConfigured") }
        : null);
    const removablePlaces = places.filter(removable);
    return (
      <section
        aria-label={title}
        className="flex min-h-0 min-w-0 flex-col"
        data-testid="client-setup-pane"
      >
        {paneHeader(
          lead.client,
          title,
          <>
            {badge ? (
              <StatusBadge tone={badge.tone}>{badge.label}</StatusBadge>
            ) : null}
            <span className="flex-1" />
            {removablePlaces.length > 0 ? (
              <Button
                className="text-danger-foreground hover:text-danger-foreground"
                disabled={working}
                onClick={() => setRemoval(removablePlaces)}
                size="sm"
                type="button"
                variant="ghost"
              >
                {t(
                  busy === "remove"
                    ? "clientSetup.removing"
                    : "clientSetup.remove",
                )}
              </Button>
            ) : null}
            <SegmentedControl
              label={t("clientSetup.view")}
              onValueChange={setTab}
              options={[
                { value: "settings", label: t("clientSetup.settingsTab") },
                { value: "changes", label: t("clientSetup.changesTab") },
              ]}
              value={tab}
            />
          </>,
        )}
        <div className="min-h-0 flex-1 overflow-y-auto px-5 pt-0.5 pb-4">
          <div className="@container/pane grid content-start gap-3.5">
            {tab === "settings" && places.length === 1 ? (
              <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-1.5 text-xs text-muted-foreground">
                <FileText aria-hidden="true" className="size-3.5 shrink-0" />
                {lead.paths.map((path, index) => (
                  <span
                    key={path}
                    className="inline-flex min-w-0 items-center gap-1"
                  >
                    <code className="min-w-0 truncate font-mono text-foreground">
                      {homePath(path, lead.home)}
                    </code>
                    {lead.exists[index] === false ? (
                      <span>{t("clientSetup.newFile")}</span>
                    ) : null}
                  </span>
                ))}
                {helpFor(lead.client)}
              </div>
            ) : null}
            {error ? (
              <FormMessage tone="error">
                {error.message}
                {error.detail ? (
                  <span className="mt-1 block break-all">{error.detail}</span>
                ) : null}
              </FormMessage>
            ) : null}
            {failedBanner}
            {tab === "settings" ? settingsBody(home) : changesBody(home)}
          </div>
        </div>
      </section>
    );
  };

  /** The values a guide client needs, for the chosen model's protocol. */
  const guideFields = (entry: ClientCatalogEntry): CopyField[] => {
    const model = guideModel.trim();
    const protocol = protocolOf(entry, model);
    const type =
      optional(`clientSetup.guide.${entry.id}.type.${protocolKey(protocol)}`) ||
      t(`clientSetup.guideType.${protocolKey(protocol)}`);
    const context = model ? official[model]?.contextTokens : undefined;
    return [
      {
        key: "type",
        label: t("clientSetup.guideField.type"),
        value: type,
        plain: true,
      },
      {
        key: "url",
        label: t("clientSetup.guideField.url"),
        value: protocolBaseURL(protocol, inferenceURL),
      },
      {
        key: "key",
        label: t("clientSetup.guideField.key"),
        value: currentToken.hint,
        copy: () => copyAccessToken(tokenId),
        copyFailed: t("tokens.copyFailed"),
      },
      ...(model
        ? [
            {
              key: "model",
              label: t("clientSetup.guideField.model"),
              value: model,
            },
          ]
        : []),
      ...(context
        ? [
            {
              key: "context",
              label: t("clientSetup.guideField.context"),
              value: String(context),
            },
          ]
        : []),
    ];
  };

  /** The import link's protocol, steps, and the models to add there. */
  const linkBody = (entry: ClientCatalogEntry) => {
    const protocol =
      linkProtocols[entry.id] ?? entry.protocols[0] ?? "openai.chat";
    const models = modelCandidates(catalog, [protocol], conversionEngine);
    const open = async () => {
      if (linking) return;
      setLinking(true);
      try {
        await openClientImportLink({
          tokenId,
          client: entry.id,
          protocol,
          inferenceUrl: inferenceURL,
        });
      } catch {
        notify.error(t("clientSetup.link.failed", { client: entry.name }));
      } finally {
        if (active.current) setLinking(false);
      }
    };
    const step = (index: number) =>
      t(`clientSetup.link.${entry.id}.step${index}`, {
        client: entry.name,
        provider: linkProviderNames[protocol] ?? "AstrLink",
      });
    return (
      <>
        <Field
          className="max-w-[340px]"
          htmlFor="client-setup-link-protocol"
          label={t("clientSetup.link.protocol")}
        >
          <Select
            onValueChange={(value) =>
              value &&
              setLinkProtocols((current) => ({
                ...current,
                [entry.id]: value,
              }))
            }
            value={protocol}
          >
            <SelectTrigger id="client-setup-link-protocol">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {entry.protocols.map((item) => (
                <SelectItem key={item} value={item}>
                  {t(`clientSetup.guideType.${protocolKey(item)}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <NumberedSteps steps={[step(1), step(2), step(3)]} />
        {overview?.installed.includes(entry.id) ? null : (
          <p className="text-xs text-muted-foreground">
            {t("clientSetup.link.notDetected", { client: entry.name })}
          </p>
        )}
        <div>
          <Button
            disabled={linking || !inferenceURL}
            onClick={() => void open()}
            type="button"
          >
            <ArrowUpRight aria-hidden="true" />
            {t("clientSetup.importIn", { client: entry.name })}
          </Button>
        </div>
        {models.length > 0 ? (
          <div className="grid min-w-0 gap-1.5">
            <span className="text-xs font-medium">
              {t("clientSetup.link.models")}
            </span>
            <CopyChips label={t("clientSetup.link.models")} values={models} />
          </div>
        ) : null}
        <FormMessage tone="warning">
          {t(`clientSetup.link.${entry.id}.again`, { client: entry.name })}
        </FormMessage>
      </>
    );
  };

  const staticPane = (entry: ClientCatalogEntry) => {
    const reason = clientReason(entry.id);
    let body: ReactNode;
    if (entry.kind === "guide") {
      body = (
        <>
          <p className="text-sm">
            {optional(`clientSetup.guide.${entry.id}.where`) ||
              t("clientSetup.guideWhere", { client: entry.name })}
          </p>
          <Field
            className="max-w-[340px]"
            htmlFor="client-setup-guide-model"
            label={t("clientSetup.guideModel")}
          >
            <ModelSelect
              aria-label={t("clientSetup.guideModel")}
              id="client-setup-guide-model"
              maxLength={256}
              onValueChange={(value) =>
                setGuideModels((current) => ({
                  ...current,
                  [entry.id]: value,
                }))
              }
              options={candidatesOf(entry.id)}
              placeholder={t("clientSetup.modelPlaceholder")}
              value={guideModel}
            />
          </Field>
          <CopyFieldList fields={guideFields(entry)} />
          <p className="text-xs text-muted-foreground">
            {reason
              ? t("clientSetup.guideNote", { reason })
              : t("clientSetup.guideRefill")}
          </p>
        </>
      );
    } else if (entry.kind === "unsupported") {
      body = (
        <p className="text-sm text-text-secondary">
          {t("clientSetup.unsupportedPane", { reason })}
        </p>
      );
    } else {
      body = linkBody(entry);
    }
    return (
      <section
        aria-label={entry.name}
        className="flex min-h-0 min-w-0 flex-col"
        data-kind={entry.kind}
        data-testid="client-setup-pane"
      >
        {paneHeader(entry.id, entry.name, null)}
        <div className="min-h-0 flex-1 overflow-y-auto px-5 pt-0.5 pb-4">
          <div className="@container/pane grid content-start gap-3.5">
            {body}
          </div>
        </div>
      </section>
    );
  };

  const writtenCount = homesIn(
    doneOutcomes
      .filter(
        (outcome) => outcome.result.ok && outcome.result.action === "write",
      )
      .map(({ before }) => before),
  ).length;
  const removedCount = homesIn(
    doneOutcomes
      .filter(
        (outcome) => outcome.result.ok && outcome.result.action === "remove",
      )
      .map(({ before }) => before),
  ).length;
  const doneSummary =
    writtenCount > 0 && removedCount > 0
      ? t("clientSetup.result.writtenAndRemoved", {
          written: writtenCount,
          removed: removedCount,
        })
      : writtenCount > 0
        ? t("clientSetup.result.writtenCount", { count: writtenCount })
        : removedCount > 0
          ? t("clientSetup.result.removedCount", { count: removedCount })
          : "";
  // Sentences joined the way the locale spaces them.
  const sentences = (parts: string[]) =>
    parts
      .filter(Boolean)
      .reduce(
        (text, part) =>
          text
            ? t("clientSetup.result.both", { done: text, failed: part })
            : part,
        "",
      );
  const pasteNames = joinNames(
    homesIn(
      doneOutcomes
        .filter(
          (outcome) => outcome.result.ok && outcome.result.action === "write",
        )
        .map(({ before }) => before),
    )
      .filter((home) =>
        statuses.some(
          (status) =>
            status.client === home.client &&
            status.wsl === home.wsl &&
            waitsForPaste(status),
        ),
      )
      .map((home) => placeLabel(home.client, home.wsl)),
  );
  const resultSummary = sentences([
    doneSummary,
    pasteNames ? t("clientSetup.result.needsPaste", { names: pasteNames }) : "",
    failedNames("write")
      ? t("clientSetup.result.notWrittenNames", { names: failedNames("write") })
      : "",
    failedNames("remove")
      ? t("clientSetup.result.notRemovedNames", {
          names: failedNames("remove"),
        })
      : "",
  ]);
  const homeNames = (places: ClientConfigStatus[]) =>
    joinNames(
      homesIn(places).map((place) => placeLabel(place.client, place.wsl)),
    );
  const writeHomes = homesIn(writes).length;
  const removeHomes = homesIn(removals).length;
  const planSummary =
    blocked.length > 0
      ? t("clientSetup.needsModel", { names: homeNames(blocked) })
      : writes.length > 0 && removals.length > 0
        ? t("clientSetup.willWriteAndRemove", {
            written: homeNames(writes),
            removed: homeNames(removals),
          })
        : writes.length > 0
          ? t("clientSetup.willWriteNames", { names: homeNames(writes) })
          : removals.length > 0
            ? t("clientSetup.willRemoveNames", { names: homeNames(removals) })
            : t("clientSetup.nothingChecked");
  const writeLabel =
    busy === "write"
      ? t("clientSetup.writing")
      : writeHomes > 0
        ? t("clientSetup.writeCount", { count: writeHomes })
        : removeHomes > 0
          ? t("clientSetup.removeCount", { count: removeHomes })
          : t("clientSetup.write");
  const retryPlan = () =>
    failed.map(({ result, before }): Planned => {
      const status =
        shown.find((item) => placeKey(item) === placeKey(before)) ?? before;
      return result.action === "write"
        ? { status, action: "write", models: modelsFor(status) }
        : { status, action: "remove" };
    });

  const confirmItems = confirmation?.items ?? [];
  const replacing = confirmItems.some((item) => !item.remove);
  // A token or gateway switch rewrites settings AstrLink wrote; only listed
  // keys are someone else's.
  const writeItems = confirmItems.filter((item) => !item.remove);
  const replacesKeys = writeItems.some((item) => item.keys.length > 0);
  const switches = writeItems.some(
    (item) => item.token_id !== null || item.gateway !== null,
  );
  const replaceKind = !replacing
    ? null
    : !replacesKeys
      ? "switch"
      : switches
        ? "switchAndReplace"
        : "replace";
  const confirmWrites =
    confirmation?.plan.some((item) => item.action === "write") ?? false;
  const homeDirOf = (item: PlaceRef) =>
    statuses.find((status) => placeKey(status) === placeKey(item))?.home ?? "";
  const confirmFile = (path: string, dir: string, home: string) => {
    const base = dir.replace(/[\\/]+$/, "");
    return path.startsWith(base) && /^[\\/]/.test(path.slice(base.length))
      ? path.slice(base.length + 1)
      : fileName(homePath(path, home));
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !pending.current && onClose()}
    >
      <DialogContent
        onInteractOutside={(event) => event.preventDefault()}
        showCloseButton={!working}
        variant="workspace"
      >
        <DialogHeader className="flex-row items-start gap-3.5 border-b px-5 pt-4 pb-3.5 pr-12 text-left">
          <div className="grid min-w-0 flex-1 gap-1">
            <DialogTitle>{t("clientSetup.title")}</DialogTitle>
            <DialogDescription>
              {t("clientSetup.description")}
            </DialogDescription>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Label
              className="text-xs font-medium text-text-secondary"
              htmlFor="client-setup-token"
            >
              {t("clientSetup.token")}
            </Label>
            <Select
              disabled={working}
              onValueChange={switchToken}
              value={tokenId}
            >
              <SelectTrigger className="w-55" id="client-setup-token">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {tokenChoices.map((item) => (
                  <SelectItem key={item.id} value={item.id}>
                    <span className="truncate">{item.name}</span>
                    <span className="font-mono text-muted-foreground">
                      {item.hint}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </DialogHeader>
        <div className="@container min-h-0 flex-1">
          <SplitWorkspace className="gap-0 @[720px]:grid-cols-[300px_minmax(0,1fr)]">
            <aside
              aria-label={t("clientSetup.client")}
              className="flex min-h-0 flex-col border-b @[720px]:border-r @[720px]:border-b-0"
            >
              <div className="flex items-center gap-2 pt-3 pr-3.5 pb-2 pl-4">
                <span className="shrink-0 text-xs font-medium text-text-secondary">
                  {t("clientSetup.client")}
                </span>
                <SearchInput
                  className="flex-1"
                  clearLabel={t("common.clearSearch")}
                  label={t("clientSetup.search")}
                  onValueChange={setQuery}
                  placeholder={t("clientSetup.search")}
                  size="sm"
                  value={query}
                />
              </div>
              <ul className="grid min-h-0 flex-1 content-start gap-px overflow-y-auto px-2 pb-2">
                {railBody}
                {searching && railBody.every((item) => item === null) ? (
                  <li className="flex items-center gap-1.5 px-2 py-2 text-xs text-muted-foreground">
                    <CircleHelp aria-hidden="true" className="size-3.5" />
                    {t("clientSetup.searchEmpty")}
                  </li>
                ) : null}
              </ul>
              {overview?.wsl_unchecked.length ? (
                <p className="px-4 pb-2 text-micro text-muted-foreground">
                  {t("clientSetup.wslUnchecked", {
                    names: joinNames(overview.wsl_unchecked),
                  })}
                </p>
              ) : null}
              <p className="border-t px-4 py-2.5 text-xs text-muted-foreground">
                {t("overview.apiAddress")}{" "}
                <code className="font-mono break-all text-foreground">
                  {inferenceURL}
                </code>
              </p>
            </aside>
            {selected?.kind === "home" ? (
              homePane(selected)
            ) : selected?.kind === "static" ? (
              staticPane(selected.entry)
            ) : (
              <section className="flex min-h-0 min-w-0 flex-col p-5">
                {statusFailed ? (
                  <FormMessage tone="error">
                    {t("clientSetup.statusFailed")}
                  </FormMessage>
                ) : (
                  <p className="text-xs text-muted-foreground" role="status">
                    {t("clientSetup.statusLoading")}
                  </p>
                )}
              </section>
            )}
          </SplitWorkspace>
        </div>
        <DialogFooter className="flex-row items-center gap-2 border-t px-5 py-3 sm:justify-end">
          {outcomes ? (
            <>
              <p
                className={cn(
                  "flex min-w-0 flex-1 items-center gap-1.5 text-xs",
                  writtenCount + removedCount > 0
                    ? "text-success-foreground"
                    : "text-danger-foreground",
                )}
                role="status"
              >
                {writtenCount + removedCount > 0 ? (
                  <CircleCheck
                    aria-hidden="true"
                    className="size-3.5 shrink-0"
                  />
                ) : (
                  <BadgeAlert
                    aria-hidden="true"
                    className="size-3.5 shrink-0"
                  />
                )}
                <span className="min-w-0">{resultSummary}</span>
              </p>
              {failed.length > 0 ? (
                <Button
                  disabled={working}
                  onClick={() => void apply(retryPlan(), false, true)}
                  type="button"
                  variant="outline"
                >
                  {busy === "write" ? t("clientSetup.writing") : retryLabel}
                </Button>
              ) : null}
              <Button disabled={working} onClick={onClose} type="button">
                {t("clientSetup.done")}
              </Button>
            </>
          ) : (
            <>
              <p
                className={cn(
                  "min-w-0 flex-1 truncate text-xs",
                  blocked.length > 0
                    ? "text-warning-foreground"
                    : "text-text-secondary",
                )}
                title={planSummary}
              >
                {planSummary}
              </p>
              <Button
                disabled={working}
                onClick={onClose}
                type="button"
                variant="outline"
              >
                {t("common.cancel")}
              </Button>
              <Button
                disabled={
                  working ||
                  blocked.length > 0 ||
                  writes.length + removals.length === 0
                }
                onClick={() => void apply(plan(railPlaces), false, false)}
                type="button"
              >
                {writeLabel}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
      <ConfirmDialog
        confirmLabel={t(
          replaceKind === "switch"
            ? "clientSetup.switchConfirm"
            : replacing
              ? "clientSetup.replaceConfirm"
              : confirmWrites
                ? "clientSetup.writeAndRemoveConfirm"
                : "clientSetup.remove",
        )}
        description={
          <>
            {replaceKind === "switch" ? null : (
              <p>
                {t(
                  replaceKind === "switchAndReplace"
                    ? "clientSetup.switchAndReplaceBody"
                    : replacing
                      ? "clientSetup.replaceBody"
                      : "clientSetup.removeBody",
                )}
              </p>
            )}
            <div className="grid max-h-[min(22rem,50dvh)] w-full gap-2 overflow-y-auto text-left">
              {confirmItems.map((item) => {
                const home = homeDirOf(item);
                const previous = tokenChoices.find(
                  (choice) => choice.id === item.token_id,
                )?.name;
                const client = clientLabel(item.client);
                const changes = [
                  item.remove ? t("clientSetup.confirm.remove") : null,
                  item.token_id
                    ? previous
                      ? t("clientSetup.confirm.token", {
                          current: currentToken.name,
                          previous,
                          client,
                        })
                      : t("clientSetup.confirm.unknownToken", {
                          current: currentToken.name,
                          client,
                        })
                    : null,
                  item.gateway
                    ? withCode(
                        "clientSetup.confirm.gateway",
                        { url: item.gateway },
                        { client },
                      )
                    : null,
                ].filter((change) => change !== null);
                return (
                  <ConfirmationPlace
                    key={placeKey(item)}
                    changes={changes}
                    dir={homePath(item.dir, home)}
                    fileOf={(path) => confirmFile(path, item.dir, home)}
                    pathOf={(path) => homePath(path, home)}
                    item={item}
                    label={labelOf(item)}
                  />
                );
              })}
            </div>
            {replacing && ccSwitch ? (
              <p>{t("clientSetup.ccSwitchOverwrites")}</p>
            ) : null}
          </>
        }
        destructive={!replacing}
        wide={confirmItems.some((item) => item.keys.length > 0)}
        onCancel={() => setConfirmation(null)}
        onConfirm={() => {
          if (!confirmation) return;
          const { plan: confirmed, retry } = confirmation;
          setConfirmation(null);
          void apply(confirmed, true, retry);
        }}
        open={confirmation !== null}
        title={
          replaceKind === "switch"
            ? t("clientSetup.switchTitle", { current: currentToken.name })
            : replaceKind === "switchAndReplace"
              ? t("clientSetup.switchAndReplaceTitle", {
                  current: currentToken.name,
                })
              : replacing
                ? t("clientSetup.replaceTitle")
                : t("clientSetup.removeManyTitle", {
                    count: confirmItems.length,
                  })
        }
      />
      <ConfirmDialog
        confirmLabel={t("clientSetup.remove")}
        description={<p>{t("clientSetup.removeBody")}</p>}
        destructive
        onCancel={() => setRemoval(null)}
        onConfirm={() => {
          if (!removal) return;
          const places = removal;
          setRemoval(null);
          void removePlaces(places);
        }}
        open={removal !== null}
        title={t("clientSetup.removeTitle", {
          client: removal ? placeLabel(removal[0].client, removal[0].wsl) : "",
        })}
      />
    </Dialog>
  );
}
