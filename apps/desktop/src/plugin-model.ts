import { invalidData, type DataProblem } from "./ipc-data-error";

/** What a plugin asks for; the user grants each one when enabling it. */
export const PLUGIN_CAPABILITIES = [
  "context.inject",
  "request.read",
  "network",
  "storage",
  "ui.views",
  "model.tools",
  "request.write",
  "request.write.raw",
  "request.gate",
  "routing",
  "response.read",
  "response.write",
  "events",
  "schedule",
  "http.serve",
  "provider",
  "ui.code",
] as const;

/** Capabilities confirmed on their own, in red: they reach what the user sees. */
export const HIGH_RISK_CAPABILITIES: readonly string[] = ["ui.code"];

/**
 * What a change grants a plugin beyond what it had, which the user confirms
 * where nothing the plugin runs can click: enabling it, capabilities,
 * addresses, plain HTTP among them, the privacy check skipped, and refusing
 * requests its entry hook cannot decide.
 */
export interface PluginApproval {
  plugin: string;
  name: string;
  enable?: boolean;
  added_capabilities?: string[];
  added_origins?: string[];
  plaintext?: string[];
  trusted?: boolean;
  fail_closed?: boolean;
  /** The review of the code an install, an update or a rollback puts in place. */
  install?: PluginInstallReview;
  /** Set for a rollback. */
  rollback?: boolean;
}

/** Where a plugin installed from source came from. */
export interface PluginInstallSource {
  /** github, or market for a plugin marketplace in new-api's index format. */
  kind: string;
  /** owner/name on GitHub; a marketplace's name. */
  repository: string;
  url: string;
  /** The tag, branch or marketplace version installed; absent for a commit. */
  ref?: string;
  ref_kind: "tag" | "branch" | "commit" | "version";
  /** The commit built; for a marketplace, the sha256 of the plugin's file. */
  commit: string;
  committed_at?: string;
  /** The plugin's directory in the repository. */
  subdir?: string;
  /** The marketplace index a plugin came from, and its key there. */
  index?: string;
  key?: string;
}

/** A package a build took from the lock file. */
export interface PluginDependency {
  name: string;
  version: string;
  bytes: number;
  /** It has install scripts, which never ran. */
  scripts?: boolean;
}

export interface PluginInstallWarning {
  code: string;
  package?: string;
  detail?: string;
}

/** The plugin an install replaces. */
export interface PluginInstallReplace {
  origin: "builtin" | "source" | "development" | "directory";
  name: string;
  version?: string;
  /** The repository of a plugin installed from source. */
  repository?: string;
}

/** What an install changes of the plugin it replaces. */
export interface PluginInstallChanges {
  from_version?: string;
  added_capabilities?: string[];
  removed_capabilities?: string[];
  client?: "added" | "removed";
  network_purpose_changed?: boolean;
  added_dependencies?: string[];
  removed_dependencies?: string[];
  changed_dependencies?: string[];
}

/** What the user reviews before a plugin is installed from source. */
export interface PluginInstallReview {
  /** Names the build waiting to be installed; empty for a rollback. */
  token: string;
  expires_at: string;
  source: PluginInstallSource;
  key: string;
  name: string;
  version: string;
  description?: string;
  author?: string;
  api_version: number;
  capabilities: string[];
  network_purpose?: string;
  hooks: string[];
  /** The plugin brings interface code. */
  client?: boolean;
  plugin_bytes: number;
  client_bytes?: number;
  lockfile?: string;
  dependencies: PluginDependency[];
  dependency_bytes: number;
  engines?: string;
  warnings?: PluginInstallWarning[];
  replaces?: PluginInstallReplace;
  changes?: PluginInstallChanges;
  /** extension, or task for a task plugin, which task describes. */
  kind?: PluginKind;
  task?: PluginTask;
  /** The icon the plugin installs with, as a data URL. */
  icon?: string;
  /** What the version's changelog says, in the page's language when it has one. */
  changelog?: string;
}

/** Why a plugin could not be fetched, built or installed. */
export interface PluginInstallFailure {
  reason: string;
  detail?: string;
  messages?: string[];
}

/** What checking an installed plugin's repository found. */
export interface PluginUpdateCheck {
  /** Installed from a commit: there is nothing newer to follow. */
  pinned?: boolean;
  available: boolean;
  ref?: string;
  ref_kind?: "tag" | "branch" | "commit" | "version";
  commit?: string;
  committed_at?: string;
  version?: string;
  notes?: string;
  /** The link to review the update with. */
  url?: string;
}

/** A version a later install replaced, kept to go back to. */
export interface PluginInstalledVersion {
  id: string;
  version?: string;
  source?: PluginInstallSource;
  replaced_at: string;
}

/** A change waiting in the plugin confirmation window. */
export interface PendingPluginApproval {
  id: string;
  key: string;
  approval: PluginApproval;
}

/** What the main window hears once a change was decided. */
export interface PluginApprovalFinished {
  id: string;
  key: string;
  approved: boolean;
  error?: string;
}

export type PluginCapability = (typeof PLUGIN_CAPABILITIES)[number];

export type PluginSource = "builtin" | "directory" | "override";

export interface PluginLoadError {
  code: string;
  file?: string;
  line?: number;
  column?: number;
  message: string;
}

export interface PluginSummary {
  key: string;
  name: string;
  version: string;
  source: PluginSource;
  file?: string;
  /** The plugin's icon as a data URL of a PNG or SVG image. */
  icon?: string;
  enabled: boolean;
  state: "loaded" | "failed";
  error?: PluginLoadError;
  requested_capabilities: string[];
  granted_capabilities: string[];
  needs_network: boolean;
  /** The plugin contract the manifest names; absent for a plugin that did not load. */
  api_version?: number;
  /** Set for a plugin `astrlink plugin dev` keeps built from a project on this computer. */
  development?: PluginDevelopment;
  /** Set for a plugin installed from source. */
  installed?: PluginInstallSource;
  /** Set while the plugin's interface code loads; it changes with the code. */
  ui_version?: string;
  /** The kinds of provider the plugin adapts, while it is enabled with provider granted; a task plugin's one, while it is enabled. */
  providers?: PluginProvider[];
  /** extension, or task for a task plugin; absent for a plugin that did not load. */
  kind?: PluginKind;
  /** What a task plugin, written for new-api's task plugin API, declares. */
  task?: PluginTask;
  /** A new-api plugin's request processing part: its hooks and the schema of an instance's config. */
  request?: PluginRequestPart;
  /** The plugin's hook calls since the gateway started, and those that failed. */
  calls?: number;
  failures?: number;
  /** A task plugin's tasks, and those not yet finished. */
  tasks?: number;
  running_tasks?: number;
}

/** An extension of requests, or a task or provider plugin of new-api's API. */
export type PluginKind = "extension" | "task" | "provider" | "request";

/** What a new-api plugin's request processing part offers. */
export interface PluginRequestPart {
  /** ingress, request, outbound, egress, reply and report, as it exports them. */
  stages: string[];
  /** The schema of an instance's config, as the plugin wrote it. */
  config_schema?: unknown;
  /** A request goes on without an instance of it that fails. */
  optional?: boolean;
  description?: string;
  /** The requests the plugin offers a dry run: name, format, body and reply. */
  samples?: RequestPluginSample[];
}

/** A request a request processing plugin offers a dry run. */
export interface RequestPluginSample {
  name?: unknown;
  format: string;
  body: unknown;
  reply?: string;
}

/** What a dry run of a request processing instance did with a request. */
export interface RequestPluginDryRun {
  body?: unknown;
  stopped?: { status: number; code: string; message: string };
  reply?: string;
  records: RequestPluginRecord[];
  failures: string[];
}

/** A step of a plugin's login of a provider. */
export interface PluginLoginView {
  id: string;
  step: "redirect" | "paste" | "device" | "fields" | "done" | "failed" | "";
  url?: string;
  hint?: string;
  user_code?: string;
  verification_uri?: string;
  verification_uri_complete?: string;
  interval?: number;
  fields?: {
    name: string;
    label: string;
    type?: string;
    required?: boolean;
    placeholder?: string;
    description?: string;
  }[];
  error?: string;
  account?: string;
  label?: string;
}

/** The credential a plugin logged a provider in with, as the page shows it. */
export interface PluginCredentialView {
  logged_in: boolean;
  account_id?: string;
  label?: string;
  expires_at?: number;
  needs_login?: boolean;
}

export interface PluginLoginAnswer {
  login?: PluginLoginView;
  credential: PluginCredentialView;
}

export function parsePluginLoginAnswer(value: unknown): PluginLoginAnswer {
  const answer = plugin.object(value, "$");
  const credential = plugin.object(answer.credential, "$.credential");
  let login: PluginLoginView | undefined;
  if (answer.login !== undefined && answer.login !== null) {
    const raw = plugin.object(answer.login, "$.login");
    login = defined({
      id: plugin.string(raw.id, "$.login.id"),
      step: (plugin.optionalString(raw.step, "$.login.step") ??
        "") as PluginLoginView["step"],
      url: plugin.optionalString(raw.url, "$.login.url"),
      hint: plugin.optionalString(raw.hint, "$.login.hint"),
      user_code: plugin.optionalString(raw.user_code, "$.login.user_code"),
      verification_uri: plugin.optionalString(
        raw.verification_uri,
        "$.login.verification_uri",
      ),
      verification_uri_complete: plugin.optionalString(
        raw.verification_uri_complete,
        "$.login.verification_uri_complete",
      ),
      interval: plugin.optionalInteger(raw.interval, "$.login.interval"),
      fields:
        raw.fields === undefined || raw.fields === null
          ? undefined
          : plugin.array(raw.fields, "$.login.fields").map((item, index) => {
              const field = plugin.object(item, `$.login.fields[${index}]`);
              return defined({
                name: plugin.string(
                  field.name,
                  `$.login.fields[${index}].name`,
                ),
                label:
                  plugin.optionalString(
                    field.label,
                    `$.login.fields[${index}].label`,
                  ) ??
                  plugin.string(field.name, `$.login.fields[${index}].name`),
                type: plugin.optionalString(
                  field.type,
                  `$.login.fields[${index}].type`,
                ),
                required: plugin.optionalBoolean(
                  field.required,
                  `$.login.fields[${index}].required`,
                ),
                placeholder: plugin.optionalString(
                  field.placeholder,
                  `$.login.fields[${index}].placeholder`,
                ),
                description: plugin.optionalString(
                  field.description,
                  `$.login.fields[${index}].description`,
                ),
              });
            }),
      error: plugin.optionalString(raw.error, "$.login.error"),
      account: plugin.optionalString(raw.account, "$.login.account"),
      label: plugin.optionalString(raw.label, "$.login.label"),
    });
  }
  return defined({
    login,
    credential: defined({
      logged_in: plugin.boolean(credential.logged_in, "$.credential.logged_in"),
      account_id: plugin.optionalString(
        credential.account_id,
        "$.credential.account_id",
      ),
      label: plugin.optionalString(credential.label, "$.credential.label"),
      expires_at: plugin.optionalInteger(
        credential.expires_at,
        "$.credential.expires_at",
      ),
      needs_login: plugin.optionalBoolean(
        credential.needs_login,
        "$.credential.needs_login",
      ),
    }),
  });
}

/** A text a plugin wrote, one string or one by language. */
export type PluginText = string | Record<string, string>;

/** A quota window of a provider's account: what is used of the limit. */
export interface PluginBalanceWindow {
  name: PluginText;
  used: number;
  limit: number;
  unit?: string;
  /** Unix seconds. */
  reset_at?: number;
}

/** A provider account's balance, as its plugin read it. */
export interface PluginBalance {
  amount?: number;
  currency?: string;
  unlimited?: boolean;
  windows?: PluginBalanceWindow[];
}

/**
 * What the provider page shows of a plugin's provider: its balance and
 * when it was read, the last error, and whether the plugin rests it.
 */
export interface PluginProviderState {
  balance?: PluginBalance;
  updated_at?: string;
  error?: string;
  resting?: boolean;
  notice?: string;
}

function pluginText(value: unknown, path: string): PluginText {
  if (typeof value === "string") return value;
  const texts = plugin.object(value, path);
  return Object.fromEntries(
    Object.entries(texts).map(([language, text]) => [
      language,
      plugin.string(text, `${path}.${language}`),
    ]),
  );
}

export function parsePluginProviderState(value: unknown): PluginProviderState {
  const state = plugin.object(value, "$");
  let balance: PluginBalance | undefined;
  if (state.balance !== undefined && state.balance !== null) {
    const raw = plugin.object(state.balance, "$.balance");
    balance = defined({
      amount: plugin.optionalNumber(raw.amount, "$.balance.amount"),
      currency: plugin.optionalString(raw.currency, "$.balance.currency"),
      unlimited: plugin.optionalBoolean(raw.unlimited, "$.balance.unlimited"),
      windows:
        raw.windows === undefined || raw.windows === null
          ? undefined
          : plugin
              .array(raw.windows, "$.balance.windows")
              .map((item, index) => {
                const path = `$.balance.windows[${index}]`;
                const window = plugin.object(item, path);
                return defined({
                  name: pluginText(window.name, `${path}.name`),
                  used: plugin.optionalNumber(window.used, `${path}.used`) ?? 0,
                  limit:
                    plugin.optionalNumber(window.limit, `${path}.limit`) ?? 0,
                  unit: plugin.optionalString(window.unit, `${path}.unit`),
                  reset_at: plugin.optionalInteger(
                    window.reset_at,
                    `${path}.reset_at`,
                  ),
                });
              }),
    });
  }
  return defined({
    balance,
    updated_at: plugin.optionalString(state.updated_at, "$.updated_at"),
    error: plugin.optionalString(state.error, "$.error"),
    resting: plugin.optionalBoolean(state.resting, "$.resting"),
    notice: plugin.optionalString(state.notice, "$.notice"),
  });
}

/** One configured use of a request processing plugin. */
export interface RequestPluginInstance {
  /** Absent for a new instance, which Core names. */
  id?: string;
  name: string;
  plugin: string;
  enabled: boolean;
  /** The models it runs for; every model when empty. */
  models: string[];
  config?: Record<string, unknown>;
}

/** A gateway protocol a task plugin serves. */
export interface PluginTaskProtocol {
  /** openai_image, openai_video or openai_responses. */
  name: string;
  /** The forms of Responses request: stream, sync, background. */
  supports?: string[];
  /** The models it narrows to; none for all. */
  models?: string[];
}

/** A vendor-native URL a task plugin serves on the inference port. */
export interface PluginTaskRoute {
  method: string;
  path: string;
  /** submit, query or dynamic. */
  type: string;
  models?: string[];
}

/** What a task plugin declares: the models it generates media with and how clients reach them. */
export interface PluginTask {
  /** A LobeHub icon name, or text / text:<label> for a text avatar. */
  icon?: string;
  description?: string;
  author?: string;
  author_url?: string;
  website?: string;
  models: string[];
  /** per_task or batch. */
  fetch_mode: string;
  /** api_key, oauth2_jwt or none. */
  auth: string;
  base_url?: string;
  allowed_hosts?: string[];
  /** The plugin can also drive another new-api gateway that has it. */
  new_api?: boolean;
  protocols: PluginTaskProtocol[];
  routes: PluginTaskRoute[];
  sort_priority?: number;
  capabilities?: string[];
  usage?: { schema?: unknown; examples?: unknown; profiles?: unknown };
}

/** A task a task plugin ran, as the plugin page lists it. */
export interface PluginTaskRecord {
  id: string;
  plugin_key: string;
  service_id: string;
  surface: string;
  action?: string;
  model: string;
  status: string;
  progress?: string;
  fail_reason?: string;
  /** The usage facts the plugin reported, such as seconds or image_count. */
  usage?: Record<string, string | number | boolean>;
  /** The kinds of the task's outputs: video, image, audio, file. */
  artifacts?: string[];
  created_at: string;
  updated_at: string;
  finished_at?: string;
  request_id?: string;
}

/** A page of a plugin's tasks, the newest first. */
export interface PluginTaskPage {
  items: PluginTaskRecord[];
  next_cursor?: string;
  /** The tasks not yet finished. */
  running: number;
}

/** A version a plugin marketplace lists. */
export interface PluginMarketVersion {
  version: string;
  min_api_version?: number;
  base_url?: string;
  auth?: string;
  allowed_hosts?: string[];
  /** The link that installs this version. */
  link: string;
  /** The version has a usage guide to read. */
  readme?: boolean;
  /** The version has release notes to read. */
  changelog?: boolean;
}

/** A plugin a marketplace offers, as its index describes the newest version. */
export interface PluginMarketEntry {
  key: string;
  name: string;
  description?: string;
  /** A LobeHub icon name or text / text:<label>. */
  icon?: string;
  /** The icon as a data URL, when the gateway could fetch it. */
  icon_url?: string;
  website?: string;
  sort_priority?: number;
  latest: string;
  versions: PluginMarketVersion[];
  models: string[];
  protocols: PluginTaskProtocol[];
  routes: PluginTaskRoute[];
  /** The link that installs the newest version. */
  link: string;
  /** The version installed now. */
  installed?: string;
  /**
   * Parts of the newest version AstrLink does not run yet: new-api's
   * provider and request processing parts. Such a plugin cannot be
   * installed.
   */
  unsupported?: string[];
  /** The plugin's 8-digit code on the New API site. */
  code?: string;
  /** The marketplace recommends the plugin. */
  recommended?: boolean;
  /** Who published a community plugin, and the repository it came from. */
  author?: { login: string; url?: string };
  repository?: string;
  /** How many gateways installed it in 30 days, when the marketplace counts. */
  installs_30d?: number;
  /** Set when the marketplace took down the version installed now. */
  revoked?: { reason?: string };
}

/** A marketplace version's usage guide or release notes. */
export interface PluginMarketDoc {
  markdown: string;
  language?: string;
  /** A page where the file can be read. */
  source_url?: string;
}

/** What a plugin marketplace offers. */
export interface PluginMarket {
  index: string;
  name: string;
  plugins: PluginMarketEntry[];
}

/** A kind of provider a plugin adapts, as the add-provider picker offers it. */
export interface PluginProvider {
  id: string;
  name: string;
  base_url?: string;
  /** How the gateway sends the provider's key. */
  auth: { scheme: string; header_name?: string };
  /** The protocols the plugin serves, the first the one others convert to. */
  protocols: { protocol: string; stream: boolean; sync: boolean }[];
  convert?: boolean;
  models?: string[];
  /** The schema of a new-api provider plugin's settings, as it wrote it. */
  config_schema?: unknown;
  /** Shown with the provider's key as its hint. */
  key_hint?: string;
  /** The plugin logs in to an account instead of taking a key. */
  login?: boolean;
  /** The plugin reads the account's balance. */
  balance?: boolean;
}

/** The project a development build comes from. */
export interface PluginDevelopment {
  project: string;
  built_at?: string;
}

/** A log line or failed call of the plugin's recent calls. */
export interface PluginJournalEntry {
  seq: number;
  time: string;
  hook: string;
  level: string;
  message: string;
  reason?: string;
  stack?: string;
  dry_run?: boolean;
}

/** How long a hook's recent calls took, in milliseconds. */
export interface PluginHookTiming {
  hook: string;
  calls: number;
  failures: number;
  p50_ms: number;
  p95_ms: number;
  max_ms: number;
  script_ms: number;
}

/** The development tab's log and timings; ask after `latest` next time. */
export interface PluginJournal {
  entries: PluginJournalEntry[];
  timings: PluginHookTiming[];
  latest: number;
  /** Extension points kept out of requests for now after failing too often. */
  breakers: Array<{ hook: string; until: string }>;
}

/** What a hook run by hand returned, or why it failed. */
export interface PluginDryRunResult {
  result?: unknown;
  error?: { reason: string; message: string; stack?: string };
  elapsed_ms: number;
  script_ms: number;
  logs: PluginJournalEntry[];
}

export interface PluginList {
  directory: string;
  plugins: PluginSummary[];
}

export interface PluginNetworkOrigin {
  origin: string;
  allow_plaintext: boolean;
  /** Plain http to a host other than this machine: it needs the user's consent. */
  plaintext: boolean;
  /** The server setting the origin comes from; the user's own have none. */
  setting?: string;
}

export interface PluginCredentialStatus {
  name: string;
  label: string;
  configured: boolean;
  hint?: string;
  origin?: string;
}

export interface PluginMetadataAttribute {
  key: string;
  label: string;
  pattern: string;
  marker?: string;
}

export interface PluginDetail extends PluginSummary {
  network_purpose?: string;
  notice?: string;
  hooks: string[];
  /** The plugin's configuration, a JSON object. */
  config: Record<string, unknown>;
  network_origins: PluginNetworkOrigin[];
  privacy_trusted: boolean;
  /** How long a request waits for the plugin's prepare hooks. */
  turn_wait_ms: number;
  credentials: PluginCredentialStatus[];
  metadata: PluginMetadataAttribute[];
  /** The fields of the plugin's settings form, in order. */
  settings: PluginSetting[];
  /** Open-source projects the plugin credits. */
  credits: PluginCredit[];
  /** The client types the plugin is off for. */
  excluded_clients: string[];
  /** A new session takes the value its working directory had. */
  workspace_binding: boolean;
  /** The host swaps a session's resident block when newer data changes it. */
  auto_refresh: boolean;
  /** The model may run the plugin's queries, when model.tools is granted. */
  lookup_enabled: boolean;
  /** The queries the plugin offers the model, in the interface language. */
  tools: PluginTool[];
  /** What the plugin says it keeps in its own store. */
  storage: PluginStorageItem[];
  /** What the host keeps for the plugin, with counts where it can clear them. */
  host_storage: PluginHostStorage[];
  /** The time limits of the extension points the plugin serves. */
  limits: PluginHookLimit[];
  /** The plugin asks to refuse requests its entry hook cannot decide. */
  fail_closed_requested?: boolean;
  /** The user agreed to that. */
  fail_closed?: boolean;
  /** The plugin has interface code. */
  client?: boolean;
  /** The user lets its interface code load. */
  ui_enabled: boolean;
  /** Versions of a plugin installed from source that later installs replaced. */
  versions?: PluginInstalledVersion[];
}

/** One extension point's time limit: its bounds, the plugin's own and the user's, in ms. */
export interface PluginHookLimit {
  hook: string;
  default_ms: number;
  max_ms: number;
  /** What the plugin asks for; absent for the default. */
  plugin_ms?: number;
  /** What the user set; absent to follow the plugin. */
  ms?: number;
  effective_ms: number;
}

/** A query a plugin offers the model, or a report it takes from it. */
export interface PluginTool {
  name: string;
  description: string;
  /** It runs before the session has the plugin's values, such as listing projects. */
  unbound?: boolean;
  /** A report: the model hands the plugin something to keep. */
  writes?: boolean;
}

/** Something a plugin keeps, as its manifest declares it. */
export interface PluginStorageItem {
  label: string;
  /** Whether it holds conversation text. */
  content: boolean;
  retention: string;
}

/** What the host keeps for a plugin. */
export const PLUGIN_HOST_STORAGE = [
  "session_bindings",
  "workspace_bindings",
  "plugin_sessions",
  "request_records",
  "resident_block",
  "plugin_values",
  "lookup_credentials",
  "plugin_files",
] as const;
export type PluginHostStorageKind = (typeof PLUGIN_HOST_STORAGE)[number];

export interface PluginHostStorage {
  kind: PluginHostStorageKind;
  count?: number;
  /** The size of what is kept, where the host measures it. */
  bytes?: number;
}

/** What the plugin page clears of what the host keeps for a plugin. */
export type PluginClearTarget = "workspaces" | "values" | "lookups" | "files";

/** The host's own settings a plugin may place in its form. */
export const HOST_SETTINGS = [
  "$wait",
  "$trusted",
  "$origins",
  "$clients",
  "$workspace",
  "$refresh",
  "$lookup",
  "$limits",
  "$failClosed",
] as const;
export type HostSettingKey = (typeof HOST_SETTINGS)[number];

/** The interface languages, which what plugins inject follows. */
export type PluginLanguage = "en" | "zh-CN" | "ja";

/** The settings of every plugin at once. */
export interface PluginHostSettings {
  /** Plugin keys in the order their extension points run; the rest follow by key. */
  order?: string[];
  /** The web console loads plugins' interface code. */
  web_ui?: boolean;
  /** Every plugin stays out of requests, which go out as they came. */
  paused: boolean;
  /** What all plugins together may inject into one request. */
  injection_budget_bytes: number;
  language: PluginLanguage;
  /** Where installs fetch packages; absent for the npm registry. */
  npm_registry?: string;
  /** The request processing instances, in the order they run. */
  request_plugins: RequestPluginInstance[];
}

export const DEFAULT_INJECTION_BUDGET_BYTES = 16 * 1024;
export const MIN_INJECTION_BUDGET_BYTES = 4 * 1024;
export const MAX_INJECTION_BUDGET_BYTES = 64 * 1024;

export interface PluginHostSettingsUpdate {
  order?: string[];
  web_ui?: boolean;
  paused?: boolean;
  injection_budget_bytes?: number;
  language?: PluginLanguage;
  npm_registry?: string;
  /** Replaces every request processing instance. */
  request_plugins?: RequestPluginInstance[];
}

/** The median and the slowest 5% of some durations, in milliseconds. */
export interface PluginPercentiles {
  p50: number;
  p95: number;
}

/** What the prompt cache read of the input of some requests. */
export interface PluginCacheShare {
  requests: number;
  input_tokens: number;
  cache_read_tokens: number;
}

/** A plugin's account of one day, or of all of them. */
export interface PluginDayStats {
  /** YYYY-MM-DD in the gateway's time zone; the total has none. */
  date?: string;
  load: {
    count: number;
    by_trigger: Record<string, number>;
    injected_bytes: number;
    requests: number;
    wait_ms?: PluginPercentiles;
  };
  lookup: {
    ran: number;
    refused: number;
    counted: number;
    with_hits: number;
    ms?: PluginPercentiles;
    sessions: number;
    looking_up: number;
  };
  binding: {
    workspace: number;
    picked: number;
    created: number;
    declined: number;
    unanswered: number;
    changed: number;
  };
  /** Turns and reports written; reported counts the reports among ok. */
  write: { ok: number; failed: number; reported: number };
  problems: {
    failed: number;
    timeouts: number;
    privacy: number;
    over_budget: number;
  };
  cache: { with: PluginCacheShare; without: PluginCacheShare };
}

/** A plugin's account over its last days, from this computer's request records. */
export interface PluginStats {
  days: PluginDayStats[];
  total: PluginDayStats;
}

/** What came of a click on a button of a plugin's record. */
export interface PluginActionResult {
  status: "ok" | "failed";
  message?: string;
  refresh: boolean;
  reason?: string;
}

/** A plugin's part in one session. */
export interface SessionPlugin {
  plugin: string;
  name: string;
  paused: boolean;
  first_turn?: number;
  /** The resident block the session carries; it lives in Core's memory only. */
  block?: {
    text: string;
    bytes: number;
    trigger: string;
    language: string;
    computed_at: string;
  };
  refresh_pending?: boolean;
  /** How the session's last request told the model to run the plugin's queries, its credential masked. */
  lookup?: string;
}

/** A value sessions are bound to, with how many and when one was last used. */
export interface MetadataValueUsage {
  value: string;
  sessions: number;
  last_used_at?: string;
}

export type PluginSettingType =
  | "server"
  | "text"
  | "number"
  | "switch"
  | "choice";

/**
 * A field of a plugin's settings form. `key` is where the value sits in the
 * configuration, a dot stepping into an object; a key of HOST_SETTINGS
 * places one of the host's settings.
 */
export interface PluginSetting {
  key: string;
  label?: string;
  type?: PluginSettingType;
  group?: string;
  row?: string;
  help?: string;
  default?: unknown;
  required?: boolean;
  advanced?: boolean;
  min?: number;
  max?: number;
  step?: number;
  unit?: string;
  placeholder?: string;
  options?: Array<{ value: string; label: string }>;
  /** The credential slot a server sends. */
  credential?: string;
}

/** An open-source project a plugin credits. */
export interface PluginCredit {
  name: string;
  owner?: string;
  /** An SPDX identifier such as Apache-2.0. */
  license?: string;
  url: string;
}

/** The GitHub stars of the repositories a plugin credits, by their address. */
export type PluginCreditStars = Record<string, number>;

/** The waits the plugin page offers, in milliseconds. */
export const PLUGIN_TURN_WAITS = [
  1000, 1500, 2000, 2500, 3000, 3500, 4000, 4500, 5000,
] as const;
export const DEFAULT_PLUGIN_TURN_WAIT_MS = 2500;

export interface PluginSettingsUpdate {
  enabled?: boolean;
  granted_capabilities?: string[];
  network_origins?: Array<{ origin: string; allow_plaintext: boolean }>;
  privacy_trusted?: boolean;
  turn_wait_ms?: number;
  excluded_clients?: string[];
  workspace_binding?: boolean;
  auto_refresh?: boolean;
  lookup_enabled?: boolean;
  config?: Record<string, unknown>;
  /** Plain http server origins the user agrees to reach in the clear. */
  plaintext_consent?: string[];
  /** Time limits by hook in ms; 0 follows the plugin again. */
  hook_limits_ms?: Record<string, number>;
  fail_closed?: boolean;
  ui_enabled?: boolean;
}

export type PluginViewItem =
  | { label: string; value: string; mono?: boolean }
  | { text: string; tone?: "ok" | "warn" | "error" };

export type PluginViewBlock =
  | {
      type: "kv";
      items: Array<{ label: string; value: string; mono?: boolean }>;
    }
  | { type: "table"; columns: string[]; rows: Array<Array<string | number>> }
  | {
      type: "badges";
      items: Array<{ text: string; tone?: "ok" | "warn" | "error" }>;
    }
  | { type: "text"; text: string };

export interface PluginNetworkCount {
  requests: number;
  denied: number;
  errors: number;
  wait_ms: number;
}

export interface PluginTestResult {
  /** What the plugin reported, or failed when the call itself failed. */
  status: "ok" | "error" | "failed";
  summary: string;
  view?: PluginViewBlock[];
  truncated?: boolean;
  reason?: string;
  elapsed_ms: number;
  network: PluginNetworkCount;
}

export interface PluginStatus {
  calls: number;
  failures: Record<string, number>;
  memory_limits: number;
  timeouts: number;
  network: PluginNetworkCount;
  view?: PluginViewBlock[];
  truncated?: boolean;
  view_error?: string;
  /** The last connection test since Core started. */
  last_test?: PluginTestResult & { tested_at: string };
  /** The values sessions are bound to, for each attribute the plugin registers. */
  bindings?: PluginBindings[];
}

/** The values sessions are bound to for one attribute, the most recent first. */
export interface PluginBindings {
  key: string;
  label: string;
  values: Array<{ value: string; sessions: number; last_used_at?: string }>;
}

export interface PluginInjection {
  placement: "system_append" | "turn_message_prefix";
  injected_bytes: number;
  replayed_blocks: number;
  dropped_items: number;
  /** The part of injected_bytes that is the usage of the plugin's lookups. */
  usage_bytes?: number;
}

/** A query the model ran, or a report it made: its size only, never what it sent or got. */
export interface PluginLookup {
  tool: string;
  input_runes: number;
  result_bytes: number;
  /** How many things the result held, when the plugin counts them. */
  hits?: number;
  /** A report: the model handed the plugin something to keep. */
  writes?: boolean;
}

/** A plugin's part in a request: what it reported and what the host did. */
export interface RequestPluginRecord {
  plugin: string;
  plugin_version: string;
  hook: string;
  title: string;
  status: "ok" | "skipped" | "failed";
  summary: string;
  view?: PluginViewBlock[];
  truncated?: boolean;
  skipped_reason?: string;
  elapsed_ms: number;
  injection?: PluginInjection;
  network: PluginNetworkCount;
  observe?: string;
  /** Why a resident block was computed, on the request that first carried it. */
  trigger?: string;
  /** The plugin's buttons for the record's session. */
  actions?: PluginRecordAction[];
  /** A query the model ran through the gateway. */
  lookup?: PluginLookup;
  /** The queries whose commands the plugin's turn block carried. */
  commands?: string[];
  /** What an extension point's call did, as effect codes such as deny or system. */
  effects?: string[];
  /** The tags the entry hook gave the request. */
  tags?: string[];
  /** The stream events a stream hook saw, and those it failed on. */
  events?: number;
  failures?: number;
}

/** A button on a plugin's record; confirm is asked before the click goes out. */
export interface PluginRecordAction {
  id: string;
  label: string;
  confirm?: string;
}

export interface MetadataAttribute {
  key: string;
  label: string;
  description: string;
  pattern: string;
  marker?: string;
  /** user, or the key of the plugin that registered it. */
  owner: string;
  created_at: string;
  updated_at: string;
  /** The request header that sets it. */
  header: string;
  /** The plugin that registered it is gone; the user may delete it. */
  owner_missing?: boolean;
}

export interface MetadataAttributeInput {
  key: string;
  label: string;
  description: string;
  pattern: string;
  marker: string;
}

export type MetadataSource = "header" | "session" | "token";

export interface MetadataValue {
  value: string;
  source: MetadataSource;
  set_by?: string;
  turn?: number;
}

export interface MetadataEvent {
  key: string;
  event: "bound" | "rebound" | "cleared" | "ignored";
  value?: string;
  set_by?: string;
  reason?: string;
  /** A value no session had and nothing offered: a new project. */
  new?: boolean;
  /** The marker's own spelling, when a known value took its place. */
  normalized?: string;
}

/** One binding of a session: its value, how it was set and in which turn. */
export interface SessionMetadataValue {
  value: string;
  /** model_marker, user_marker, user_answer, workspace or manual. */
  set_by: string;
  turn?: number;
  updated_at: string;
}

/** What a session is bound to, by attribute key. */
export interface SessionMetadata {
  values: Record<string, SessionMetadataValue>;
  /** The working directory the session's last request named, and what it is bound to. */
  workspace?: { name: string; values: Record<string, string> };
  /** The values plugins offered the session, by attribute key. */
  choices?: Record<string, string[]>;
}

/** The metadata of one request, kept with its record. */
export interface RequestMetadata {
  values: Record<string, MetadataValue>;
  events: MetadataEvent[];
}

type JsonObject = Record<string, unknown>;

function invalid(
  subject: "plugin" | "metadata",
  path: string,
  problem: DataProblem,
): never {
  return invalidData(subject, path, problem);
}

function reader(subject: "plugin" | "metadata") {
  const object = (value: unknown, path: string): JsonObject => {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      return invalid(subject, path, "object");
    }
    return value as JsonObject;
  };
  const string = (value: unknown, path: string): string => {
    if (typeof value !== "string") return invalid(subject, path, "string");
    return value;
  };
  const optionalString = (value: unknown, path: string): string | undefined =>
    value === undefined ? undefined : string(value, path);
  const integer = (value: unknown, path: string): number => {
    if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
      return invalid(subject, path, "nonNegativeInteger");
    }
    return value;
  };
  const optionalInteger = (value: unknown, path: string): number | undefined =>
    value === undefined ? undefined : integer(value, path);
  const optionalNumber = (value: unknown, path: string): number | undefined => {
    if (value === undefined) return undefined;
    if (typeof value !== "number" || !Number.isFinite(value)) {
      return invalid(subject, path, "number");
    }
    return value;
  };
  const boolean = (value: unknown, path: string): boolean => {
    if (typeof value !== "boolean") return invalid(subject, path, "boolean");
    return value;
  };
  const optionalBoolean = (
    value: unknown,
    path: string,
  ): boolean | undefined =>
    value === undefined ? undefined : boolean(value, path);
  const array = (value: unknown, path: string): unknown[] => {
    if (!Array.isArray(value)) return invalid(subject, path, "array");
    return value;
  };
  const strings = (value: unknown, path: string): string[] =>
    array(value, path).map((item, index) => string(item, `${path}[${index}]`));
  const oneOf = <T extends string>(
    value: unknown,
    allowed: readonly T[],
    path: string,
  ): T => {
    if (typeof value !== "string" || !allowed.includes(value as T)) {
      return invalid(subject, path, "unknownType");
    }
    return value as T;
  };
  return {
    object,
    string,
    optionalString,
    integer,
    optionalInteger,
    optionalNumber,
    boolean,
    optionalBoolean,
    array,
    strings,
    oneOf,
  };
}

const plugin = reader("plugin");
const metadata = reader("metadata");

/** Drops the keys whose value is undefined, so parsed objects compare cleanly. */
function defined<T extends object>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, entry]) => entry !== undefined),
  ) as T;
}

function parseLoadError(value: unknown, path: string): PluginLoadError {
  const error = plugin.object(value, path);
  return defined({
    code: plugin.string(error.code, `${path}.code`),
    file: plugin.optionalString(error.file, `${path}.file`),
    line: plugin.optionalInteger(error.line, `${path}.line`),
    column: plugin.optionalInteger(error.column, `${path}.column`),
    message: plugin.string(error.message, `${path}.message`),
  });
}

/**
 * A plugin's icon, kept only as a data URL of a PNG or SVG image: the page
 * puts it in an img, and anything else is left out rather than refused.
 */
function pluginIcon(value: unknown, path: string): string | undefined {
  const icon = plugin.optionalString(value, path);
  return icon &&
    /^data:image\/(png|svg\+xml);base64,[A-Za-z0-9+/=]+$/.test(icon)
    ? icon
    : undefined;
}

function parseSummaryAt(value: unknown, path: string): PluginSummary {
  const summary = plugin.object(value, path);
  return defined({
    key: plugin.string(summary.key, `${path}.key`),
    name: plugin.string(summary.name, `${path}.name`),
    version: plugin.string(summary.version, `${path}.version`),
    source: plugin.oneOf(
      summary.source,
      ["builtin", "directory", "override"] as const,
      `${path}.source`,
    ),
    file: plugin.optionalString(summary.file, `${path}.file`),
    icon: pluginIcon(summary.icon, `${path}.icon`),
    enabled: plugin.boolean(summary.enabled, `${path}.enabled`),
    state: plugin.oneOf(
      summary.state,
      ["loaded", "failed"] as const,
      `${path}.state`,
    ),
    error:
      summary.error === undefined
        ? undefined
        : parseLoadError(summary.error, `${path}.error`),
    requested_capabilities: plugin.strings(
      summary.requested_capabilities,
      `${path}.requested_capabilities`,
    ),
    granted_capabilities: plugin.strings(
      summary.granted_capabilities,
      `${path}.granted_capabilities`,
    ),
    needs_network: plugin.boolean(
      summary.needs_network,
      `${path}.needs_network`,
    ),
    api_version: plugin.optionalInteger(
      summary.api_version,
      `${path}.api_version`,
    ),
    development:
      summary.development === undefined
        ? undefined
        : parseDevelopment(summary.development, `${path}.development`),
    installed:
      summary.installed === undefined
        ? undefined
        : parseInstallSource(summary.installed, `${path}.installed`),
    ui_version: plugin.optionalString(summary.ui_version, `${path}.ui_version`),
    providers:
      summary.providers === undefined
        ? undefined
        : plugin
            .array(summary.providers, `${path}.providers`)
            .map((item, index) =>
              parseProvider(item, `${path}.providers[${index}]`),
            ),
    kind:
      summary.kind === undefined
        ? undefined
        : plugin.oneOf(summary.kind, pluginKinds, `${path}.kind`),
    task:
      summary.task === undefined
        ? undefined
        : parseTask(summary.task, `${path}.task`),
    request:
      summary.request === undefined
        ? undefined
        : parseRequestPart(summary.request, `${path}.request`),
    calls: plugin.optionalInteger(summary.calls, `${path}.calls`),
    failures: plugin.optionalInteger(summary.failures, `${path}.failures`),
    tasks: plugin.optionalInteger(summary.tasks, `${path}.tasks`),
    running_tasks: plugin.optionalInteger(
      summary.running_tasks,
      `${path}.running_tasks`,
    ),
  });
}

const pluginKinds = ["extension", "task", "provider", "request"] as const;

function parseTaskProtocols(
  value: unknown,
  path: string,
): PluginTaskProtocol[] {
  return plugin.array(value, path).map((item, index) => {
    const at = `${path}[${index}]`;
    const claim = plugin.object(item, at);
    return defined({
      name: plugin.string(claim.name, `${at}.name`),
      supports: optionalStrings(claim.supports, `${at}.supports`),
      models: optionalStrings(claim.models, `${at}.models`),
    });
  });
}

function parseTaskRoutes(value: unknown, path: string): PluginTaskRoute[] {
  return plugin.array(value, path).map((item, index) => {
    const at = `${path}[${index}]`;
    const route = plugin.object(item, at);
    return defined({
      method: plugin.string(route.method, `${at}.method`),
      path: plugin.string(route.path, `${at}.path`),
      type: plugin.string(route.type, `${at}.type`),
      models: optionalStrings(route.models, `${at}.models`),
    });
  });
}

function parseTask(value: unknown, path: string): PluginTask {
  const task = plugin.object(value, path);
  const usage =
    task.usage === undefined
      ? undefined
      : plugin.object(task.usage, `${path}.usage`);
  return defined({
    icon: plugin.optionalString(task.icon, `${path}.icon`),
    description: plugin.optionalString(task.description, `${path}.description`),
    author: plugin.optionalString(task.author, `${path}.author`),
    author_url: plugin.optionalString(task.author_url, `${path}.author_url`),
    website: plugin.optionalString(task.website, `${path}.website`),
    models: plugin.strings(task.models, `${path}.models`),
    fetch_mode: plugin.string(task.fetch_mode, `${path}.fetch_mode`),
    auth: plugin.string(task.auth, `${path}.auth`),
    base_url: plugin.optionalString(task.base_url, `${path}.base_url`),
    allowed_hosts: optionalStrings(task.allowed_hosts, `${path}.allowed_hosts`),
    new_api: plugin.optionalBoolean(task.new_api, `${path}.new_api`),
    protocols: parseTaskProtocols(task.protocols, `${path}.protocols`),
    routes: parseTaskRoutes(task.routes, `${path}.routes`),
    sort_priority: plugin.optionalNumber(
      task.sort_priority,
      `${path}.sort_priority`,
    ),
    capabilities: optionalStrings(task.capabilities, `${path}.capabilities`),
    usage: usage
      ? defined({
          schema: usage.schema,
          examples: usage.examples,
          profiles: usage.profiles,
        })
      : undefined,
  });
}

/** A usage fact: text, a number or a switch. */
function parseFacts(
  value: unknown,
  path: string,
): Record<string, string | number | boolean> | undefined {
  if (value === undefined || value === null) return undefined;
  const facts = plugin.object(value, path);
  return Object.fromEntries(
    Object.entries(facts).filter(
      ([, fact]) =>
        typeof fact === "string" ||
        typeof fact === "number" ||
        typeof fact === "boolean",
    ),
  ) as Record<string, string | number | boolean>;
}

export function parsePluginTaskPage(value: unknown): PluginTaskPage {
  const page = plugin.object(value, "$");
  return defined({
    items: plugin.array(page.items, "$.items").map((item, index) => {
      const at = `$.items[${index}]`;
      const task = plugin.object(item, at);
      return defined({
        id: plugin.string(task.id, `${at}.id`),
        plugin_key: plugin.string(task.plugin_key, `${at}.plugin_key`),
        service_id: plugin.string(task.service_id, `${at}.service_id`),
        surface: plugin.string(task.surface, `${at}.surface`),
        action: plugin.optionalString(task.action, `${at}.action`),
        model: plugin.string(task.model, `${at}.model`),
        status: plugin.string(task.status, `${at}.status`),
        progress: plugin.optionalString(task.progress, `${at}.progress`),
        fail_reason: plugin.optionalString(
          task.fail_reason,
          `${at}.fail_reason`,
        ),
        usage: parseFacts(task.usage, `${at}.usage`),
        artifacts: optionalStrings(task.artifacts, `${at}.artifacts`),
        created_at: plugin.string(task.created_at, `${at}.created_at`),
        updated_at: plugin.string(task.updated_at, `${at}.updated_at`),
        finished_at: plugin.optionalString(
          task.finished_at,
          `${at}.finished_at`,
        ),
        request_id: plugin.optionalString(task.request_id, `${at}.request_id`),
      });
    }),
    next_cursor: plugin.optionalString(page.next_cursor, "$.next_cursor"),
    running: plugin.integer(page.running, "$.running"),
  });
}

export function parsePluginMarket(value: unknown): PluginMarket {
  const market = plugin.object(value, "$");
  return {
    index: plugin.string(market.index, "$.index"),
    name: plugin.string(market.name, "$.name"),
    plugins: plugin.array(market.plugins, "$.plugins").map((item, index) => {
      const at = `$.plugins[${index}]`;
      const entry = plugin.object(item, at);
      return defined({
        key: plugin.string(entry.key, `${at}.key`),
        name: plugin.string(entry.name, `${at}.name`),
        description: plugin.optionalString(
          entry.description,
          `${at}.description`,
        ),
        icon: plugin.optionalString(entry.icon, `${at}.icon`),
        icon_url: pluginIcon(entry.icon_url, `${at}.icon_url`),
        website: plugin.optionalString(entry.website, `${at}.website`),
        sort_priority: plugin.optionalNumber(
          entry.sort_priority,
          `${at}.sort_priority`,
        ),
        latest: plugin.string(entry.latest, `${at}.latest`),
        versions: plugin
          .array(entry.versions, `${at}.versions`)
          .map((version, position) => {
            const where = `${at}.versions[${position}]`;
            const listed = plugin.object(version, where);
            return defined({
              version: plugin.string(listed.version, `${where}.version`),
              min_api_version: plugin.optionalInteger(
                listed.min_api_version,
                `${where}.min_api_version`,
              ),
              base_url: plugin.optionalString(
                listed.base_url,
                `${where}.base_url`,
              ),
              auth: plugin.optionalString(listed.auth, `${where}.auth`),
              allowed_hosts: optionalStrings(
                listed.allowed_hosts,
                `${where}.allowed_hosts`,
              ),
              link: plugin.string(listed.link, `${where}.link`),
              readme: plugin.optionalBoolean(listed.readme, `${where}.readme`),
              changelog: plugin.optionalBoolean(
                listed.changelog,
                `${where}.changelog`,
              ),
            });
          }),
        models: plugin.strings(entry.models, `${at}.models`),
        protocols: parseTaskProtocols(entry.protocols, `${at}.protocols`),
        routes: parseTaskRoutes(entry.routes, `${at}.routes`),
        link: plugin.string(entry.link, `${at}.link`),
        installed: plugin.optionalString(entry.installed, `${at}.installed`),
        unsupported: optionalStrings(entry.unsupported, `${at}.unsupported`),
        code: plugin.optionalString(entry.code, `${at}.code`),
        recommended: plugin.optionalBoolean(
          entry.recommended,
          `${at}.recommended`,
        ),
        author:
          entry.author === undefined
            ? undefined
            : parseMarketAuthor(entry.author, `${at}.author`),
        repository: plugin.optionalString(entry.repository, `${at}.repository`),
        installs_30d: plugin.optionalInteger(
          entry.installs_30d,
          `${at}.installs_30d`,
        ),
        revoked:
          entry.revoked === undefined
            ? undefined
            : defined({
                reason: plugin.optionalString(
                  plugin.object(entry.revoked, `${at}.revoked`).reason,
                  `${at}.revoked.reason`,
                ),
              }),
      });
    }),
  };
}

function parseMarketAuthor(
  value: unknown,
  path: string,
): { login: string; url?: string } {
  const author = plugin.object(value, path);
  return defined({
    login: plugin.string(author.login, `${path}.login`),
    url: plugin.optionalString(author.url, `${path}.url`),
  });
}

export function parsePluginMarketDoc(value: unknown): PluginMarketDoc {
  const doc = plugin.object(value, "$");
  return defined({
    markdown: plugin.string(doc.markdown, "$.markdown"),
    language: plugin.optionalString(doc.language, "$.language"),
    source_url: plugin.optionalString(doc.source_url, "$.source_url"),
  });
}

function parseProvider(value: unknown, path: string): PluginProvider {
  const provider = plugin.object(value, path);
  const auth = plugin.object(provider.auth, `${path}.auth`);
  return defined({
    config_schema: provider.config_schema,
    key_hint: plugin.optionalString(provider.key_hint, `${path}.key_hint`),
    login: plugin.optionalBoolean(provider.login, `${path}.login`),
    balance: plugin.optionalBoolean(provider.balance, `${path}.balance`),
    id: plugin.string(provider.id, `${path}.id`),
    name: plugin.string(provider.name, `${path}.name`),
    base_url: plugin.optionalString(provider.base_url, `${path}.base_url`),
    auth: defined({
      scheme: plugin.string(auth.scheme, `${path}.auth.scheme`),
      header_name: plugin.optionalString(
        auth.header_name,
        `${path}.auth.header_name`,
      ),
    }),
    protocols: plugin
      .array(provider.protocols, `${path}.protocols`)
      .map((item, index) => {
        const at = `${path}.protocols[${index}]`;
        const entry = plugin.object(item, at);
        return {
          protocol: plugin.string(entry.protocol, `${at}.protocol`),
          stream: plugin.boolean(entry.stream, `${at}.stream`),
          sync: plugin.boolean(entry.sync, `${at}.sync`),
        };
      }),
    convert: plugin.optionalBoolean(provider.convert, `${path}.convert`),
    models:
      provider.models === undefined
        ? undefined
        : plugin.strings(provider.models, `${path}.models`),
  });
}

const refKinds = ["tag", "branch", "commit", "version"] as const;

function parseInstallSource(value: unknown, path: string): PluginInstallSource {
  const source = plugin.object(value, path);
  return defined({
    kind: plugin.string(source.kind, `${path}.kind`),
    repository: plugin.string(source.repository, `${path}.repository`),
    url: plugin.string(source.url, `${path}.url`),
    ref: plugin.optionalString(source.ref, `${path}.ref`),
    ref_kind: plugin.oneOf(source.ref_kind, refKinds, `${path}.ref_kind`),
    commit: plugin.string(source.commit, `${path}.commit`),
    committed_at: plugin.optionalString(
      source.committed_at,
      `${path}.committed_at`,
    ),
    subdir: plugin.optionalString(source.subdir, `${path}.subdir`),
    index: plugin.optionalString(source.index, `${path}.index`),
    key: plugin.optionalString(source.key, `${path}.key`),
  });
}

function optionalStrings(value: unknown, path: string): string[] | undefined {
  return value === undefined ? undefined : plugin.strings(value, path);
}

export function parsePluginInstallReview(
  value: unknown,
  path = "$",
): PluginInstallReview {
  const review = plugin.object(value, path);
  const changes =
    review.changes === undefined
      ? undefined
      : plugin.object(review.changes, `${path}.changes`);
  const replaces =
    review.replaces === undefined
      ? undefined
      : plugin.object(review.replaces, `${path}.replaces`);
  return defined({
    token: plugin.string(review.token, `${path}.token`),
    expires_at: plugin.string(review.expires_at, `${path}.expires_at`),
    source: parseInstallSource(review.source, `${path}.source`),
    key: plugin.string(review.key, `${path}.key`),
    name: plugin.string(review.name, `${path}.name`),
    version: plugin.string(review.version, `${path}.version`),
    description: plugin.optionalString(
      review.description,
      `${path}.description`,
    ),
    author: plugin.optionalString(review.author, `${path}.author`),
    api_version: plugin.integer(review.api_version, `${path}.api_version`),
    capabilities: plugin.strings(review.capabilities, `${path}.capabilities`),
    network_purpose: plugin.optionalString(
      review.network_purpose,
      `${path}.network_purpose`,
    ),
    hooks: plugin.strings(review.hooks, `${path}.hooks`),
    client: plugin.optionalBoolean(review.client, `${path}.client`),
    plugin_bytes: plugin.integer(review.plugin_bytes, `${path}.plugin_bytes`),
    client_bytes: plugin.optionalInteger(
      review.client_bytes,
      `${path}.client_bytes`,
    ),
    lockfile: plugin.optionalString(review.lockfile, `${path}.lockfile`),
    dependencies: plugin
      .array(review.dependencies, `${path}.dependencies`)
      .map((item, index) => {
        const at = `${path}.dependencies[${index}]`;
        const dependency = plugin.object(item, at);
        return defined({
          name: plugin.string(dependency.name, `${at}.name`),
          version: plugin.string(dependency.version, `${at}.version`),
          bytes: plugin.integer(dependency.bytes, `${at}.bytes`),
          scripts: plugin.optionalBoolean(dependency.scripts, `${at}.scripts`),
        });
      }),
    dependency_bytes: plugin.integer(
      review.dependency_bytes,
      `${path}.dependency_bytes`,
    ),
    engines: plugin.optionalString(review.engines, `${path}.engines`),
    warnings:
      review.warnings === undefined
        ? undefined
        : plugin
            .array(review.warnings, `${path}.warnings`)
            .map((item, index) => {
              const at = `${path}.warnings[${index}]`;
              const warning = plugin.object(item, at);
              return defined({
                code: plugin.string(warning.code, `${at}.code`),
                package: plugin.optionalString(
                  warning.package,
                  `${at}.package`,
                ),
                detail: plugin.optionalString(warning.detail, `${at}.detail`),
              });
            }),
    replaces: replaces
      ? defined({
          origin: plugin.oneOf(
            replaces.origin,
            ["builtin", "source", "development", "directory"] as const,
            `${path}.replaces.origin`,
          ),
          name: plugin.string(replaces.name, `${path}.replaces.name`),
          version: plugin.optionalString(
            replaces.version,
            `${path}.replaces.version`,
          ),
          repository: plugin.optionalString(
            replaces.repository,
            `${path}.replaces.repository`,
          ),
        })
      : undefined,
    changes: changes
      ? defined({
          from_version: plugin.optionalString(
            changes.from_version,
            `${path}.changes.from_version`,
          ),
          added_capabilities: optionalStrings(
            changes.added_capabilities,
            `${path}.changes.added_capabilities`,
          ),
          removed_capabilities: optionalStrings(
            changes.removed_capabilities,
            `${path}.changes.removed_capabilities`,
          ),
          client:
            changes.client === undefined
              ? undefined
              : plugin.oneOf(
                  changes.client,
                  ["added", "removed"] as const,
                  `${path}.changes.client`,
                ),
          network_purpose_changed: plugin.optionalBoolean(
            changes.network_purpose_changed,
            `${path}.changes.network_purpose_changed`,
          ),
          added_dependencies: optionalStrings(
            changes.added_dependencies,
            `${path}.changes.added_dependencies`,
          ),
          removed_dependencies: optionalStrings(
            changes.removed_dependencies,
            `${path}.changes.removed_dependencies`,
          ),
          changed_dependencies: optionalStrings(
            changes.changed_dependencies,
            `${path}.changes.changed_dependencies`,
          ),
        })
      : undefined,
    kind:
      review.kind === undefined
        ? undefined
        : plugin.oneOf(review.kind, pluginKinds, `${path}.kind`),
    task:
      review.task === undefined
        ? undefined
        : parseTask(review.task, `${path}.task`),
    icon: pluginIcon(review.icon, `${path}.icon`),
    changelog: plugin.optionalString(review.changelog, `${path}.changelog`),
  });
}

export function parsePluginInstallFailure(
  value: unknown,
): PluginInstallFailure {
  const failure = plugin.object(value, "$");
  return defined({
    reason: plugin.string(failure.reason, "$.reason"),
    detail: plugin.optionalString(failure.detail, "$.detail"),
    messages: optionalStrings(failure.messages, "$.messages"),
  });
}

export function parsePluginUpdateCheck(value: unknown): PluginUpdateCheck {
  const check = plugin.object(value, "$");
  return defined({
    pinned: plugin.optionalBoolean(check.pinned, "$.pinned"),
    available: plugin.boolean(check.available, "$.available"),
    ref: plugin.optionalString(check.ref, "$.ref"),
    ref_kind:
      check.ref_kind === undefined
        ? undefined
        : plugin.oneOf(check.ref_kind, refKinds, "$.ref_kind"),
    commit: plugin.optionalString(check.commit, "$.commit"),
    committed_at: plugin.optionalString(check.committed_at, "$.committed_at"),
    version: plugin.optionalString(check.version, "$.version"),
    notes: plugin.optionalString(check.notes, "$.notes"),
    url: plugin.optionalString(check.url, "$.url"),
  });
}

function parseDevelopment(value: unknown, path: string): PluginDevelopment {
  const development = plugin.object(value, path);
  return defined({
    project: plugin.string(development.project, `${path}.project`),
    built_at: plugin.optionalString(development.built_at, `${path}.built_at`),
  });
}

function parseJournalEntry(value: unknown, path: string): PluginJournalEntry {
  const entry = plugin.object(value, path);
  return defined({
    seq: plugin.integer(entry.seq, `${path}.seq`),
    time: plugin.string(entry.time, `${path}.time`),
    hook: plugin.string(entry.hook, `${path}.hook`),
    level: plugin.string(entry.level, `${path}.level`),
    message: plugin.string(entry.message, `${path}.message`),
    reason: plugin.optionalString(entry.reason, `${path}.reason`),
    stack: plugin.optionalString(entry.stack, `${path}.stack`),
    dry_run: plugin.optionalBoolean(entry.dry_run, `${path}.dry_run`),
  });
}

function milliseconds(value: unknown, path: string): number {
  const number = plugin.optionalNumber(value, path);
  if (number === undefined || number < 0)
    return invalid("plugin", path, "number");
  return number;
}

export function parsePluginApproval(
  value: unknown,
  path = "$",
): PluginApproval {
  const approval = plugin.object(value, path);
  const list = (field: string) =>
    approval[field] === undefined
      ? undefined
      : plugin.strings(approval[field], `${path}.${field}`);
  return defined({
    plugin: plugin.string(approval.plugin, `${path}.plugin`),
    name: plugin.string(approval.name, `${path}.name`),
    enable: plugin.optionalBoolean(approval.enable, `${path}.enable`),
    added_capabilities: list("added_capabilities"),
    added_origins: list("added_origins"),
    plaintext: list("plaintext"),
    trusted: plugin.optionalBoolean(approval.trusted, `${path}.trusted`),
    fail_closed: plugin.optionalBoolean(
      approval.fail_closed,
      `${path}.fail_closed`,
    ),
    install:
      approval.install === undefined
        ? undefined
        : parsePluginInstallReview(approval.install, `${path}.install`),
    rollback: plugin.optionalBoolean(approval.rollback, `${path}.rollback`),
  });
}

export function parsePendingPluginApproval(
  value: unknown,
): PendingPluginApproval | null {
  if (value === null || value === undefined) return null;
  const pending = plugin.object(value, "$");
  return {
    id: plugin.string(pending.id, "$.id"),
    key: plugin.string(pending.key, "$.key"),
    approval: parsePluginApproval(pending.approval, "$.approval"),
  };
}

export function parsePluginApprovalFinished(
  value: unknown,
): PluginApprovalFinished {
  const finished = plugin.object(value, "$");
  return defined({
    id: plugin.string(finished.id, "$.id"),
    key: plugin.string(finished.key, "$.key"),
    approved: plugin.boolean(finished.approved, "$.approved"),
    error: plugin.optionalString(finished.error, "$.error"),
  });
}

export function parsePluginJournal(value: unknown): PluginJournal {
  const journal = plugin.object(value, "$");
  return {
    entries: plugin
      .array(journal.entries, "$.entries")
      .map((entry, index) => parseJournalEntry(entry, `$.entries[${index}]`)),
    timings: plugin.array(journal.timings, "$.timings").map((item, index) => {
      const path = `$.timings[${index}]`;
      const timing = plugin.object(item, path);
      return {
        hook: plugin.string(timing.hook, `${path}.hook`),
        calls: plugin.integer(timing.calls, `${path}.calls`),
        failures: plugin.integer(timing.failures, `${path}.failures`),
        p50_ms: milliseconds(timing.p50_ms, `${path}.p50_ms`),
        p95_ms: milliseconds(timing.p95_ms, `${path}.p95_ms`),
        max_ms: milliseconds(timing.max_ms, `${path}.max_ms`),
        script_ms: milliseconds(timing.script_ms, `${path}.script_ms`),
      };
    }),
    latest: plugin.integer(journal.latest, "$.latest"),
    breakers: (journal.breakers === undefined
      ? []
      : plugin.array(journal.breakers, "$.breakers")
    ).map((item, index) => {
      const path = `$.breakers[${index}]`;
      const breaker = plugin.object(item, path);
      return {
        hook: plugin.string(breaker.hook, `${path}.hook`),
        until: plugin.string(breaker.until, `${path}.until`),
      };
    }),
  };
}

export function parsePluginDryRun(value: unknown): PluginDryRunResult {
  const run = plugin.object(value, "$");
  let error: PluginDryRunResult["error"];
  if (run.error !== undefined) {
    const failure = plugin.object(run.error, "$.error");
    error = defined({
      reason: plugin.string(failure.reason, "$.error.reason"),
      message: plugin.string(failure.message, "$.error.message"),
      stack: plugin.optionalString(failure.stack, "$.error.stack"),
    });
  }
  return defined({
    result: run.result,
    error,
    elapsed_ms: milliseconds(run.elapsed_ms, "$.elapsed_ms"),
    script_ms: milliseconds(run.script_ms, "$.script_ms"),
    logs: plugin
      .array(run.logs, "$.logs")
      .map((entry, index) => parseLogLine(entry, `$.logs[${index}]`)),
  });
}

/** A dry run's log line, which has no sequence number. */
function parseLogLine(value: unknown, path: string): PluginJournalEntry {
  const entry = plugin.object(value, path);
  return parseJournalEntry({ ...entry, seq: entry.seq ?? 0 }, path);
}

export function parsePluginList(value: unknown): PluginList {
  const list = plugin.object(value, "$");
  return {
    directory: plugin.string(list.directory, "$.directory"),
    plugins: plugin
      .array(list.plugins, "$.plugins")
      .map((item, index) => parseSummaryAt(item, `$.plugins[${index}]`)),
  };
}

export function parsePluginDetail(value: unknown): PluginDetail {
  const detail = plugin.object(value, "$");
  return defined({
    ...parseSummaryAt(value, "$"),
    network_purpose: plugin.optionalString(
      detail.network_purpose,
      "$.network_purpose",
    ),
    notice: plugin.optionalString(detail.notice, "$.notice"),
    hooks: plugin.strings(detail.hooks, "$.hooks"),
    config: plugin.object(detail.config, "$.config"),
    network_origins: plugin
      .array(detail.network_origins, "$.network_origins")
      .map((item, index) => {
        const path = `$.network_origins[${index}]`;
        const origin = plugin.object(item, path);
        return {
          origin: plugin.string(origin.origin, `${path}.origin`),
          allow_plaintext: plugin.boolean(
            origin.allow_plaintext,
            `${path}.allow_plaintext`,
          ),
          plaintext: plugin.boolean(origin.plaintext, `${path}.plaintext`),
          ...(origin.setting === undefined
            ? {}
            : { setting: plugin.string(origin.setting, `${path}.setting`) }),
        };
      }),
    privacy_trusted: plugin.boolean(
      detail.privacy_trusted,
      "$.privacy_trusted",
    ),
    turn_wait_ms:
      plugin.optionalInteger(detail.turn_wait_ms, "$.turn_wait_ms") ??
      DEFAULT_PLUGIN_TURN_WAIT_MS,
    credentials: plugin
      .array(detail.credentials, "$.credentials")
      .map((item, index) => {
        const path = `$.credentials[${index}]`;
        const credential = plugin.object(item, path);
        return defined({
          name: plugin.string(credential.name, `${path}.name`),
          label: plugin.string(credential.label, `${path}.label`),
          configured: plugin.boolean(
            credential.configured,
            `${path}.configured`,
          ),
          hint: plugin.optionalString(credential.hint, `${path}.hint`),
          origin: plugin.optionalString(credential.origin, `${path}.origin`),
        });
      }),
    metadata: plugin.array(detail.metadata, "$.metadata").map((item, index) => {
      const path = `$.metadata[${index}]`;
      const attribute = plugin.object(item, path);
      return defined({
        key: plugin.string(attribute.key, `${path}.key`),
        label: plugin.string(attribute.label, `${path}.label`),
        pattern: plugin.string(attribute.pattern, `${path}.pattern`),
        marker: plugin.optionalString(attribute.marker, `${path}.marker`),
      });
    }),
    settings: (detail.settings === undefined
      ? []
      : plugin.array(detail.settings, "$.settings")
    ).map((item, index) => parseSetting(item, `$.settings[${index}]`)),
    credits: (detail.credits === undefined
      ? []
      : plugin.array(detail.credits, "$.credits")
    ).map((item, index) => {
      const path = `$.credits[${index}]`;
      const credit = plugin.object(item, path);
      return defined({
        name: plugin.string(credit.name, `${path}.name`),
        owner: plugin.optionalString(credit.owner, `${path}.owner`),
        license: plugin.optionalString(credit.license, `${path}.license`),
        url: plugin.string(credit.url, `${path}.url`),
      });
    }),
    excluded_clients:
      detail.excluded_clients === undefined
        ? []
        : plugin.strings(detail.excluded_clients, "$.excluded_clients"),
    workspace_binding:
      plugin.optionalBoolean(detail.workspace_binding, "$.workspace_binding") ??
      true,
    auto_refresh:
      plugin.optionalBoolean(detail.auto_refresh, "$.auto_refresh") ?? false,
    lookup_enabled:
      plugin.optionalBoolean(detail.lookup_enabled, "$.lookup_enabled") ?? true,
    limits: (detail.limits === undefined
      ? []
      : plugin.array(detail.limits, "$.limits")
    ).map((item, index) => {
      const path = `$.limits[${index}]`;
      const limit = plugin.object(item, path);
      return defined({
        hook: plugin.string(limit.hook, `${path}.hook`),
        default_ms: plugin.integer(limit.default_ms, `${path}.default_ms`),
        max_ms: plugin.integer(limit.max_ms, `${path}.max_ms`),
        plugin_ms: plugin.optionalInteger(limit.plugin_ms, `${path}.plugin_ms`),
        ms: plugin.optionalInteger(limit.ms, `${path}.ms`),
        effective_ms: plugin.integer(
          limit.effective_ms,
          `${path}.effective_ms`,
        ),
      });
    }),
    fail_closed_requested: plugin.optionalBoolean(
      detail.fail_closed_requested,
      "$.fail_closed_requested",
    ),
    fail_closed: plugin.optionalBoolean(detail.fail_closed, "$.fail_closed"),
    client: plugin.optionalBoolean(detail.client, "$.client"),
    ui_enabled:
      plugin.optionalBoolean(detail.ui_enabled, "$.ui_enabled") ?? true,
    versions:
      detail.versions === undefined
        ? undefined
        : plugin.array(detail.versions, "$.versions").map((item, index) => {
            const path = `$.versions[${index}]`;
            const version = plugin.object(item, path);
            return defined({
              id: plugin.string(version.id, `${path}.id`),
              version: plugin.optionalString(
                version.version,
                `${path}.version`,
              ),
              source:
                version.source === undefined
                  ? undefined
                  : parseInstallSource(version.source, `${path}.source`),
              replaced_at: plugin.string(
                version.replaced_at,
                `${path}.replaced_at`,
              ),
            });
          }),
    tools: (detail.tools === undefined
      ? []
      : plugin.array(detail.tools, "$.tools")
    ).map((item, index) => {
      const path = `$.tools[${index}]`;
      const tool = plugin.object(item, path);
      return defined({
        name: plugin.string(tool.name, `${path}.name`),
        description: plugin.string(tool.description, `${path}.description`),
        unbound: plugin.optionalBoolean(tool.unbound, `${path}.unbound`),
        writes: plugin.optionalBoolean(tool.writes, `${path}.writes`),
      });
    }),
    storage: (detail.storage === undefined
      ? []
      : plugin.array(detail.storage, "$.storage")
    ).map((item, index) => {
      const path = `$.storage[${index}]`;
      const kept = plugin.object(item, path);
      return {
        label: plugin.string(kept.label, `${path}.label`),
        content: plugin.boolean(kept.content, `${path}.content`),
        retention: plugin.string(kept.retention, `${path}.retention`),
      };
    }),
    host_storage: (detail.host_storage === undefined
      ? []
      : plugin.array(detail.host_storage, "$.host_storage")
    ).flatMap((item, index) => {
      const path = `$.host_storage[${index}]`;
      const kept = plugin.object(item, path);
      const kind = plugin.string(kept.kind, `${path}.kind`);
      // A kind a newer Core keeps is not one this page can name.
      if (!(PLUGIN_HOST_STORAGE as readonly string[]).includes(kind)) return [];
      return [
        defined({
          kind: kind as PluginHostStorageKind,
          count: plugin.optionalInteger(kept.count, `${path}.count`),
          bytes: plugin.optionalInteger(kept.bytes, `${path}.bytes`),
        }),
      ];
    }),
  });
}

const languages = ["en", "zh-CN", "ja"] as const;

export function parsePluginHostSettings(value: unknown): PluginHostSettings {
  const settings = plugin.object(value, "$");
  return {
    paused: plugin.boolean(settings.paused, "$.paused"),
    injection_budget_bytes: plugin.integer(
      settings.injection_budget_bytes,
      "$.injection_budget_bytes",
    ),
    language: plugin.oneOf(settings.language, languages, "$.language"),
    ...(settings.order === undefined
      ? {}
      : { order: plugin.strings(settings.order, "$.order") }),
    ...(settings.web_ui === undefined
      ? {}
      : { web_ui: plugin.boolean(settings.web_ui, "$.web_ui") }),
    ...(settings.npm_registry === undefined
      ? {}
      : {
          npm_registry: plugin.string(settings.npm_registry, "$.npm_registry"),
        }),
    request_plugins:
      settings.request_plugins === undefined
        ? []
        : plugin
            .array(settings.request_plugins, "$.request_plugins")
            .map((item, index) =>
              parseRequestPluginInstance(item, `$.request_plugins[${index}]`),
            ),
  };
}

function parseRequestPluginInstance(
  value: unknown,
  path: string,
): RequestPluginInstance {
  const instance = plugin.object(value, path);
  const config =
    instance.config === undefined
      ? undefined
      : plugin.object(instance.config, `${path}.config`);
  return defined({
    id: plugin.string(instance.id, `${path}.id`),
    name: plugin.string(instance.name, `${path}.name`),
    plugin: plugin.string(instance.plugin, `${path}.plugin`),
    enabled: plugin.boolean(instance.enabled, `${path}.enabled`),
    models:
      instance.models === undefined || instance.models === null
        ? []
        : plugin.strings(instance.models, `${path}.models`),
    config,
  });
}

function parseRequestPart(value: unknown, path: string): PluginRequestPart {
  const part = plugin.object(value, path);
  return defined({
    stages: plugin.strings(part.stages, `${path}.stages`),
    config_schema: part.config_schema,
    optional: plugin.optionalBoolean(part.optional, `${path}.optional`),
    description: plugin.optionalString(part.description, `${path}.description`),
    samples: Array.isArray(part.samples)
      ? part.samples.flatMap((item): RequestPluginSample[] => {
          if (!item || typeof item !== "object") return [];
          const sample = item as Record<string, unknown>;
          if (typeof sample.format !== "string" || !sample.body) return [];
          return [
            {
              name: sample.name,
              format: sample.format,
              body: sample.body,
              ...(typeof sample.reply === "string"
                ? { reply: sample.reply }
                : {}),
            },
          ];
        })
      : undefined,
  });
}

export function parseRequestPluginDryRun(value: unknown): RequestPluginDryRun {
  const result = plugin.object(value, "$");
  const stopped =
    result.stopped === undefined || result.stopped === null
      ? undefined
      : plugin.object(result.stopped, "$.stopped");
  return defined({
    body: result.body,
    stopped: stopped
      ? {
          status: plugin.integer(stopped.status, "$.stopped.status"),
          code: plugin.string(stopped.code, "$.stopped.code"),
          message: plugin.string(stopped.message, "$.stopped.message"),
        }
      : undefined,
    reply: plugin.optionalString(result.reply, "$.reply"),
    records: parseRequestPluginRecords(result.records ?? [], "$.records"),
    failures:
      result.failures === undefined || result.failures === null
        ? []
        : plugin.strings(result.failures, "$.failures"),
  });
}

export function parsePluginActionResult(value: unknown): PluginActionResult {
  const result = plugin.object(value, "$");
  return defined({
    status: plugin.oneOf(result.status, ["ok", "failed"] as const, "$.status"),
    message: plugin.optionalString(result.message, "$.message"),
    refresh: plugin.boolean(result.refresh, "$.refresh"),
    reason: plugin.optionalString(result.reason, "$.reason"),
  });
}

/** Reads the plugins' part in a session. */
export function parseSessionPlugins(value: unknown): SessionPlugin[] {
  const list = plugin.object(value, "$");
  return plugin.array(list.plugins, "$.plugins").map((item, index) => {
    const path = `$.plugins[${index}]`;
    const part = plugin.object(item, path);
    let block: SessionPlugin["block"];
    if (part.block !== undefined) {
      const blockPath = `${path}.block`;
      const raw = plugin.object(part.block, blockPath);
      block = {
        text: plugin.string(raw.text, `${blockPath}.text`),
        bytes: plugin.integer(raw.bytes, `${blockPath}.bytes`),
        trigger: plugin.string(raw.trigger, `${blockPath}.trigger`),
        language: plugin.string(raw.language, `${blockPath}.language`),
        computed_at: plugin.string(raw.computed_at, `${blockPath}.computed_at`),
      };
    }
    return defined({
      plugin: plugin.string(part.plugin, `${path}.plugin`),
      name: plugin.string(part.name, `${path}.name`),
      paused: plugin.boolean(part.paused, `${path}.paused`),
      first_turn: plugin.optionalInteger(part.first_turn, `${path}.first_turn`),
      block,
      refresh_pending: plugin.optionalBoolean(
        part.refresh_pending,
        `${path}.refresh_pending`,
      ),
      lookup: plugin.optionalString(part.lookup, `${path}.lookup`),
    });
  });
}

function parsePercentiles(
  value: unknown,
  path: string,
): PluginPercentiles | undefined {
  if (value === undefined) return undefined;
  const raw = plugin.object(value, path);
  return {
    p50: plugin.integer(raw.p50, `${path}.p50`),
    p95: plugin.integer(raw.p95, `${path}.p95`),
  };
}

function parseCacheShare(value: unknown, path: string): PluginCacheShare {
  const raw = plugin.object(value, path);
  return {
    requests: plugin.integer(raw.requests, `${path}.requests`),
    input_tokens: plugin.integer(raw.input_tokens, `${path}.input_tokens`),
    cache_read_tokens: plugin.integer(
      raw.cache_read_tokens,
      `${path}.cache_read_tokens`,
    ),
  };
}

function parseDayStats(value: unknown, path: string): PluginDayStats {
  const day = plugin.object(value, path);
  const counts = <K extends string>(
    raw: unknown,
    at: string,
    keys: readonly K[],
  ): Record<K, number> => {
    const object = plugin.object(raw, at);
    return Object.fromEntries(
      keys.map((key) => [key, plugin.integer(object[key], `${at}.${key}`)]),
    ) as Record<K, number>;
  };
  const load = plugin.object(day.load, `${path}.load`);
  const byTrigger = plugin.object(load.by_trigger, `${path}.load.by_trigger`);
  const lookup = plugin.object(day.lookup, `${path}.lookup`);
  const cache = plugin.object(day.cache, `${path}.cache`);
  return defined({
    date: plugin.optionalString(day.date, `${path}.date`),
    load: defined({
      count: plugin.integer(load.count, `${path}.load.count`),
      by_trigger: Object.fromEntries(
        Object.entries(byTrigger).map(([trigger, count]) => [
          trigger,
          plugin.integer(count, `${path}.load.by_trigger.${trigger}`),
        ]),
      ),
      injected_bytes: plugin.integer(
        load.injected_bytes,
        `${path}.load.injected_bytes`,
      ),
      requests: plugin.integer(load.requests, `${path}.load.requests`),
      wait_ms: parsePercentiles(load.wait_ms, `${path}.load.wait_ms`),
    }),
    lookup: defined({
      ...counts(lookup, `${path}.lookup`, [
        "ran",
        "refused",
        "counted",
        "with_hits",
        "sessions",
        "looking_up",
      ] as const),
      ms: parsePercentiles(lookup.ms, `${path}.lookup.ms`),
    }),
    binding: counts(day.binding, `${path}.binding`, [
      "workspace",
      "picked",
      "created",
      "declined",
      "unanswered",
      "changed",
    ] as const),
    write: counts(day.write, `${path}.write`, [
      "ok",
      "failed",
      "reported",
    ] as const),
    problems: counts(day.problems, `${path}.problems`, [
      "failed",
      "timeouts",
      "privacy",
      "over_budget",
    ] as const),
    cache: {
      with: parseCacheShare(cache.with, `${path}.cache.with`),
      without: parseCacheShare(cache.without, `${path}.cache.without`),
    },
  });
}

/** Reads a plugin's account over its last days. */
export function parsePluginStats(value: unknown): PluginStats {
  const stats = plugin.object(value, "$");
  return {
    days: plugin
      .array(stats.days, "$.days")
      .map((day, index) => parseDayStats(day, `$.days[${index}]`)),
    total: parseDayStats(stats.total, "$.total"),
  };
}

/** Reads the GitHub stars of the repositories a plugin credits. */
export function parsePluginCreditStars(value: unknown): PluginCreditStars {
  const answer = plugin.object(value, "$");
  const stars = plugin.object(answer.stars ?? {}, "$.stars");
  return Object.fromEntries(
    Object.entries(stars).map(([url, count]) => [
      url,
      plugin.integer(count, `$.stars.${url}`),
    ]),
  );
}

/** Reads how many a clearing removed. */
export function parsePluginClearResult(value: unknown): number {
  const result = plugin.object(value, "$");
  return plugin.integer(result.removed, "$.removed");
}

const settingTypes = ["server", "text", "number", "switch", "choice"] as const;

function parseSetting(value: unknown, path: string): PluginSetting {
  const setting = plugin.object(value, path);
  return defined({
    key: plugin.string(setting.key, `${path}.key`),
    label: plugin.optionalString(setting.label, `${path}.label`),
    type:
      setting.type === undefined
        ? undefined
        : plugin.oneOf(setting.type, settingTypes, `${path}.type`),
    group: plugin.optionalString(setting.group, `${path}.group`),
    row: plugin.optionalString(setting.row, `${path}.row`),
    help: plugin.optionalString(setting.help, `${path}.help`),
    default: setting.default,
    required: plugin.optionalBoolean(setting.required, `${path}.required`),
    advanced: plugin.optionalBoolean(setting.advanced, `${path}.advanced`),
    min: plugin.optionalNumber(setting.min, `${path}.min`),
    max: plugin.optionalNumber(setting.max, `${path}.max`),
    step: plugin.optionalNumber(setting.step, `${path}.step`),
    unit: plugin.optionalString(setting.unit, `${path}.unit`),
    placeholder: plugin.optionalString(
      setting.placeholder,
      `${path}.placeholder`,
    ),
    options:
      setting.options === undefined
        ? undefined
        : plugin
            .array(setting.options, `${path}.options`)
            .map((item, index) => {
              const optionPath = `${path}.options[${index}]`;
              const option = plugin.object(item, optionPath);
              return {
                value: plugin.string(option.value, `${optionPath}.value`),
                label: plugin.string(option.label, `${optionPath}.label`),
              };
            }),
    credential: plugin.optionalString(setting.credential, `${path}.credential`),
  });
}

function parseNetworkCount(value: unknown, path: string): PluginNetworkCount {
  const network = plugin.object(value, path);
  return {
    requests: plugin.integer(network.requests, `${path}.requests`),
    denied: plugin.integer(network.denied, `${path}.denied`),
    errors: plugin.integer(network.errors, `${path}.errors`),
    wait_ms: plugin.integer(network.wait_ms, `${path}.wait_ms`),
  };
}

const tones = ["ok", "warn", "error"] as const;

/** Reads a view the host already cleaned, failing on anything else. */
export function parsePluginView(
  value: unknown,
  path: string,
): PluginViewBlock[] {
  return plugin.array(value, path).map((item, index) => {
    const blockPath = `${path}[${index}]`;
    const block = plugin.object(item, blockPath);
    switch (block.type) {
      case "kv":
        return {
          type: "kv",
          items: plugin
            .array(block.items, `${blockPath}.items`)
            .map((entry, entryIndex) => {
              const itemPath = `${blockPath}.items[${entryIndex}]`;
              const kv = plugin.object(entry, itemPath);
              return defined({
                label: plugin.string(kv.label, `${itemPath}.label`),
                value:
                  plugin.optionalString(kv.value, `${itemPath}.value`) ?? "",
                mono: plugin.optionalBoolean(kv.mono, `${itemPath}.mono`),
              });
            }),
        };
      case "badges":
        return {
          type: "badges",
          items: plugin
            .array(block.items, `${blockPath}.items`)
            .map((entry, entryIndex) => {
              const itemPath = `${blockPath}.items[${entryIndex}]`;
              const badge = plugin.object(entry, itemPath);
              return defined({
                text: plugin.string(badge.text, `${itemPath}.text`),
                tone:
                  badge.tone === undefined
                    ? undefined
                    : plugin.oneOf(badge.tone, tones, `${itemPath}.tone`),
              });
            }),
        };
      case "table":
        return {
          type: "table",
          columns: plugin.strings(block.columns, `${blockPath}.columns`),
          rows: plugin
            .array(block.rows, `${blockPath}.rows`)
            .map((row, rowIndex) =>
              plugin
                .array(row, `${blockPath}.rows[${rowIndex}]`)
                .map((cell, cellIndex) => {
                  if (typeof cell === "number" && Number.isFinite(cell)) {
                    return cell;
                  }
                  return plugin.string(
                    cell,
                    `${blockPath}.rows[${rowIndex}][${cellIndex}]`,
                  );
                }),
            ),
        };
      case "text":
        return {
          type: "text",
          text: plugin.string(block.text, `${blockPath}.text`),
        };
      default:
        return invalid("plugin", `${blockPath}.type`, "unknownType");
    }
  });
}

function optionalView(
  value: unknown,
  path: string,
): PluginViewBlock[] | undefined {
  return value === undefined ? undefined : parsePluginView(value, path);
}

export function parsePluginTestResult(value: unknown): PluginTestResult {
  const result = plugin.object(value, "$");
  return defined({
    status: plugin.oneOf(
      result.status,
      ["ok", "error", "failed"] as const,
      "$.status",
    ),
    summary: plugin.string(result.summary, "$.summary"),
    view: optionalView(result.view, "$.view"),
    truncated: plugin.optionalBoolean(result.truncated, "$.truncated"),
    reason: plugin.optionalString(result.reason, "$.reason"),
    elapsed_ms: plugin.integer(result.elapsed_ms, "$.elapsed_ms"),
    network: parseNetworkCount(result.network, "$.network"),
  });
}

export function parsePluginStatus(value: unknown): PluginStatus {
  const status = plugin.object(value, "$");
  const failures = plugin.object(status.failures, "$.failures");
  return defined({
    calls: plugin.integer(status.calls, "$.calls"),
    failures: Object.fromEntries(
      Object.entries(failures).map(([reason, count]) => [
        reason,
        plugin.integer(count, `$.failures.${reason}`),
      ]),
    ),
    memory_limits: plugin.integer(status.memory_limits, "$.memory_limits"),
    timeouts: plugin.integer(status.timeouts, "$.timeouts"),
    network: parseNetworkCount(status.network, "$.network"),
    view: optionalView(status.view, "$.view"),
    truncated: plugin.optionalBoolean(status.truncated, "$.truncated"),
    view_error: plugin.optionalString(status.view_error, "$.view_error"),
    last_test:
      status.last_test === undefined
        ? undefined
        : {
            ...parsePluginTestResult(status.last_test),
            tested_at: plugin.string(
              plugin.object(status.last_test, "$.last_test").tested_at,
              "$.last_test.tested_at",
            ),
          },
    bindings:
      status.bindings === undefined
        ? undefined
        : plugin.array(status.bindings, "$.bindings").map((item, index) => {
            const path = `$.bindings[${index}]`;
            const bindings = plugin.object(item, path);
            return {
              key: plugin.string(bindings.key, `${path}.key`),
              label: plugin.string(bindings.label, `${path}.label`),
              values: plugin
                .array(bindings.values, `${path}.values`)
                .map((entry, valueIndex) => {
                  const valuePath = `${path}.values[${valueIndex}]`;
                  const bound = plugin.object(entry, valuePath);
                  return defined({
                    value: plugin.string(bound.value, `${valuePath}.value`),
                    sessions: plugin.integer(
                      bound.sessions,
                      `${valuePath}.sessions`,
                    ),
                    last_used_at: plugin.optionalString(
                      bound.last_used_at,
                      `${valuePath}.last_used_at`,
                    ),
                  });
                }),
            };
          }),
  });
}

/** Reads the plugin_records of a request record. */
export function parseRequestPluginRecords(
  value: unknown,
  path: string,
): RequestPluginRecord[] {
  return plugin.array(value, path).map((item, index) => {
    const recordPath = `${path}[${index}]`;
    const record = plugin.object(item, recordPath);
    let lookup: PluginLookup | undefined;
    if (record.lookup !== undefined) {
      const lookupPath = `${recordPath}.lookup`;
      const raw = plugin.object(record.lookup, lookupPath);
      lookup = defined({
        tool: plugin.string(raw.tool, `${lookupPath}.tool`),
        input_runes: plugin.integer(
          raw.input_runes,
          `${lookupPath}.input_runes`,
        ),
        result_bytes: plugin.integer(
          raw.result_bytes,
          `${lookupPath}.result_bytes`,
        ),
        hits: plugin.optionalInteger(raw.hits, `${lookupPath}.hits`),
        writes: plugin.optionalBoolean(raw.writes, `${lookupPath}.writes`),
      });
    }
    let injection: PluginInjection | undefined;
    if (record.injection !== undefined) {
      const injectionPath = `${recordPath}.injection`;
      const raw = plugin.object(record.injection, injectionPath);
      injection = {
        placement: plugin.oneOf(
          raw.placement,
          ["system_append", "turn_message_prefix"] as const,
          `${injectionPath}.placement`,
        ),
        injected_bytes: plugin.integer(
          raw.injected_bytes,
          `${injectionPath}.injected_bytes`,
        ),
        replayed_blocks: plugin.integer(
          raw.replayed_blocks,
          `${injectionPath}.replayed_blocks`,
        ),
        dropped_items: plugin.integer(
          raw.dropped_items,
          `${injectionPath}.dropped_items`,
        ),
        usage_bytes: plugin.optionalInteger(
          raw.usage_bytes,
          `${injectionPath}.usage_bytes`,
        ),
      };
    }
    return defined({
      plugin: plugin.string(record.plugin, `${recordPath}.plugin`),
      plugin_version: plugin.string(
        record.plugin_version,
        `${recordPath}.plugin_version`,
      ),
      hook: plugin.string(record.hook, `${recordPath}.hook`),
      title: plugin.string(record.title, `${recordPath}.title`),
      status: plugin.oneOf(
        record.status,
        ["ok", "skipped", "failed"] as const,
        `${recordPath}.status`,
      ),
      summary: plugin.string(record.summary, `${recordPath}.summary`),
      view: optionalView(record.view, `${recordPath}.view`),
      truncated: plugin.optionalBoolean(
        record.truncated,
        `${recordPath}.truncated`,
      ),
      skipped_reason: plugin.optionalString(
        record.skipped_reason,
        `${recordPath}.skipped_reason`,
      ),
      elapsed_ms: plugin.integer(record.elapsed_ms, `${recordPath}.elapsed_ms`),
      injection,
      network: parseNetworkCount(record.network, `${recordPath}.network`),
      observe: plugin.optionalString(record.observe, `${recordPath}.observe`),
      trigger: plugin.optionalString(record.trigger, `${recordPath}.trigger`),
      effects:
        record.effects === undefined
          ? undefined
          : plugin.strings(record.effects, `${recordPath}.effects`),
      tags:
        record.tags === undefined
          ? undefined
          : plugin.strings(record.tags, `${recordPath}.tags`),
      events: plugin.optionalInteger(record.events, `${recordPath}.events`),
      failures: plugin.optionalInteger(
        record.failures,
        `${recordPath}.failures`,
      ),
      actions:
        record.actions === undefined
          ? undefined
          : plugin
              .array(record.actions, `${recordPath}.actions`)
              .map((entry, at) => {
                const actionPath = `${recordPath}.actions[${at}]`;
                const action = plugin.object(entry, actionPath);
                return defined({
                  id: plugin.string(action.id, `${actionPath}.id`),
                  label: plugin.string(action.label, `${actionPath}.label`),
                  confirm: plugin.optionalString(
                    action.confirm,
                    `${actionPath}.confirm`,
                  ),
                });
              }),
      lookup,
      commands:
        record.commands === undefined
          ? undefined
          : plugin.strings(record.commands, `${recordPath}.commands`),
    });
  });
}

function parseAttributeAt(value: unknown, path: string): MetadataAttribute {
  const attribute = metadata.object(value, path);
  return defined({
    key: metadata.string(attribute.key, `${path}.key`),
    label: metadata.string(attribute.label, `${path}.label`),
    description: metadata.string(attribute.description, `${path}.description`),
    pattern: metadata.string(attribute.pattern, `${path}.pattern`),
    marker: metadata.optionalString(attribute.marker, `${path}.marker`),
    owner: metadata.string(attribute.owner, `${path}.owner`),
    created_at: metadata.string(attribute.created_at, `${path}.created_at`),
    updated_at: metadata.string(attribute.updated_at, `${path}.updated_at`),
    header: metadata.string(attribute.header, `${path}.header`),
    owner_missing: metadata.optionalBoolean(
      attribute.owner_missing,
      `${path}.owner_missing`,
    ),
  });
}

export function parseMetadataAttributes(value: unknown): MetadataAttribute[] {
  const list = metadata.object(value, "$");
  return metadata
    .array(list.attributes, "$.attributes")
    .map((item, index) => parseAttributeAt(item, `$.attributes[${index}]`));
}

export function parseMetadataAttribute(value: unknown): MetadataAttribute {
  return parseAttributeAt(value, "$");
}

export function parseAccessTokenMetadata(
  value: unknown,
): Record<string, string> {
  const defaults = metadata.object(value, "$");
  const values = metadata.object(defaults.values, "$.values");
  return Object.fromEntries(
    Object.entries(values).map(([key, entry]) => [
      key,
      metadata.string(entry, `$.values.${key}`),
    ]),
  );
}

/** Reads a session's bindings. */
export function parseSessionMetadata(value: unknown): SessionMetadata {
  const raw = metadata.object(value, "$");
  const values = metadata.object(raw.values, "$.values");
  let workspace: SessionMetadata["workspace"];
  if (raw.workspace !== undefined) {
    const place = metadata.object(raw.workspace, "$.workspace");
    const bound = metadata.object(place.values, "$.workspace.values");
    workspace = {
      name: metadata.string(place.name, "$.workspace.name"),
      values: Object.fromEntries(
        Object.entries(bound).map(([key, entry]) => [
          key,
          metadata.string(entry, `$.workspace.values.${key}`),
        ]),
      ),
    };
  }
  let choices: SessionMetadata["choices"];
  if (raw.choices !== undefined) {
    const offered = metadata.object(raw.choices, "$.choices");
    choices = Object.fromEntries(
      Object.entries(offered).map(([key, entry]) => [
        key,
        metadata.strings(entry, `$.choices.${key}`),
      ]),
    );
  }
  return defined({
    workspace,
    choices,
    values: Object.fromEntries(
      Object.entries(values).map(([key, entry]) => {
        const path = `$.values.${key}`;
        const item = metadata.object(entry, path);
        return [
          key,
          defined({
            value: metadata.string(item.value, `${path}.value`),
            set_by: metadata.string(item.set_by, `${path}.set_by`),
            turn: metadata.optionalInteger(item.turn, `${path}.turn`),
            updated_at: metadata.string(item.updated_at, `${path}.updated_at`),
          }),
        ];
      }),
    ),
  });
}

/** Reads the values of an attribute sessions are bound to. */
export function parseMetadataValues(value: unknown): MetadataValueUsage[] {
  const list = metadata.object(value, "$");
  return metadata.array(list.values, "$.values").map((item, index) => {
    const path = `$.values[${index}]`;
    const usage = metadata.object(item, path);
    return defined({
      value: metadata.string(usage.value, `${path}.value`),
      sessions: metadata.integer(usage.sessions, `${path}.sessions`),
      last_used_at: metadata.optionalString(
        usage.last_used_at,
        `${path}.last_used_at`,
      ),
    });
  });
}

const sources = ["header", "session", "token"] as const;
const metadataEvents = ["bound", "rebound", "cleared", "ignored"] as const;

/** Reads the metadata of a request record. */
export function parseRequestMetadata(
  value: unknown,
  path: string,
): RequestMetadata {
  const raw = metadata.object(value, path);
  const values =
    raw.values === undefined
      ? {}
      : metadata.object(raw.values, `${path}.values`);
  return {
    values: Object.fromEntries(
      Object.entries(values).map(([key, entry]) => {
        const valuePath = `${path}.values.${key}`;
        const item = metadata.object(entry, valuePath);
        return [
          key,
          defined({
            value: metadata.string(item.value, `${valuePath}.value`),
            source: metadata.oneOf(item.source, sources, `${valuePath}.source`),
            set_by: metadata.optionalString(item.set_by, `${valuePath}.set_by`),
            turn: metadata.optionalInteger(item.turn, `${valuePath}.turn`),
          }),
        ];
      }),
    ),
    events:
      raw.events === undefined
        ? []
        : metadata.array(raw.events, `${path}.events`).map((entry, index) => {
            const eventPath = `${path}.events[${index}]`;
            const event = metadata.object(entry, eventPath);
            return defined({
              key: metadata.string(event.key, `${eventPath}.key`),
              event: metadata.oneOf(
                event.event,
                metadataEvents,
                `${eventPath}.event`,
              ),
              value: metadata.optionalString(event.value, `${eventPath}.value`),
              set_by: metadata.optionalString(
                event.set_by,
                `${eventPath}.set_by`,
              ),
              reason: metadata.optionalString(
                event.reason,
                `${eventPath}.reason`,
              ),
              new: metadata.optionalBoolean(event.new, `${eventPath}.new`),
              normalized: metadata.optionalString(
                event.normalized,
                `${eventPath}.normalized`,
              ),
            });
          }),
  };
}

/** The UTF-8 size of a JSON configuration, as the 64 KiB limit counts it. */
export function configBytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

export const MAX_PLUGIN_CONFIG_BYTES = 64 * 1024;

/** The value at a setting's key in a configuration; a dot steps into an object. */
export function settingValue(
  config: Record<string, unknown>,
  key: string,
): unknown {
  let current: unknown = config;
  for (const part of key.split(".")) {
    if (
      typeof current !== "object" ||
      current === null ||
      Array.isArray(current)
    )
      return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}

/**
 * A copy of config with value at key, or without the key for undefined; an
 * object emptied on the way goes too.
 */
export function withSettingValue(
  config: Record<string, unknown>,
  key: string,
  value: unknown,
): Record<string, unknown> {
  const copy = JSON.parse(JSON.stringify(config)) as Record<string, unknown>;
  const parts = key.split(".");
  const last = parts.pop() as string;
  const trail: Array<[Record<string, unknown>, string]> = [];
  let current = copy;
  for (const part of parts) {
    const next = current[part];
    if (typeof next !== "object" || next === null || Array.isArray(next)) {
      if (value === undefined) return copy;
      current[part] = {};
    }
    trail.push([current, part]);
    current = current[part] as Record<string, unknown>;
  }
  if (value !== undefined) {
    current[last] = value;
    return copy;
  }
  delete current[last];
  for (const [parent, part] of trail.reverse()) {
    if (Object.keys(parent[part] as object).length > 0) break;
    delete parent[part];
  }
  return copy;
}

/** An http or https address with a host and no user info. */
export function validOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.hostname !== "" &&
      url.username === "" &&
      url.password === ""
    );
  } catch {
    return false;
  }
}

/** Plain http to a host other than this machine, as Core tells it. */
export function plaintextOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:") return false;
    const host = url.hostname.replace(/^\[|\]$/g, "");
    return !(host === "localhost" || host === "::1" || host.startsWith("127."));
  } catch {
    return false;
  }
}

/** A JSON text whose objects list their keys in order, to compare values. */
export function stableJSON(value: unknown): string {
  return JSON.stringify(value, (_, item: unknown) =>
    typeof item === "object" && item !== null && !Array.isArray(item)
      ? Object.fromEntries(
          Object.entries(item as Record<string, unknown>).sort(([a], [b]) =>
            a < b ? -1 : a > b ? 1 : 0,
          ),
        )
      : item,
  );
}
