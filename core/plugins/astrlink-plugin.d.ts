// AstrLink plugin API, version 2.
//
// A plugin is one module that exports `meta` and any of the hooks below: a
// single plugin.ts or plugin.js, or a project whose package.json names its
// entries in "astrlink": { "plugin": "src/plugin.ts", "client": … }, which
// AstrLink (and `astrlink plugin build`) bundles into one file, npm
// dependencies included. Imports and eval are never available at run time.
//
// apiVersion 2 hooks may be async: the host functions that wait on the host
// (astrlink.http.fetch, astrlink.store, astrlink.meta.values) return
// promises, and the web APIs of moejs are available: fetch, Request,
// Response (with a streamed body), Headers, the streams, URL,
// URLSearchParams, AbortController, AbortSignal, Event, EventTarget,
// DOMException and performance; with console, crypto, Intl,
// TextEncoder/TextDecoder, structuredClone, atob/btoa and Node's timers.
// apiVersion 1 plugins stay synchronous: the same host functions return their
// results, and async functions, await and the timers are refused.
//
// For editor support and type checks, put this file in one TypeScript
// project with the plugin, as `astrlink plugin new` does. The host strips
// types without checking them.

declare namespace AstrLinkPlugin {
  /** What a plugin asks for; the user grants each one when enabling it. */
  type Capability =
    | "context.inject" // prepareSession and prepareTurn may add blocks to the request
    | "request.read" // the hooks see the turn's text, the request body and metadata; astrlink.request.info
    | "network" // fetch and astrlink.http.fetch, to the origins the user allows
    | "storage" // astrlink.store
    | "ui.views" // views in request records and on the plugin page
    | "model.tools" // the model runs meta.tools through the lookup outlet; callTool answers
    // apiVersion 2:
    | "request.write" // beforeForward: system prompt, turn, parameters, tools
    | "request.write.raw" // beforeForward replaces the body; beforeUpstream changes an attempt
    | "request.gate" // onRequest may refuse a request
    | "routing" // route reorders, excludes, changes the model
    | "response.read" // onResponse sees the response once it ended
    | "response.write" // rewriteResponse, onStreamEvent and onStreamEnd change the response
    | "events" // onEvent sees what happened, without content
    | "schedule" // onSchedule runs at the intervals of meta.schedules
    | "http.serve" // onHttp answers /plugins/<key>/… on the inference listener
    | "provider" // offers the providers of meta.providers
    | "ui.code"; // the interface code of the plugin's client entry loads in the desktop

  /** `export const meta`: the manifest the host checks when it loads a plugin. */
  interface Meta {
    apiVersion: 1 | 2;
    /** A lowercase letter, then up to 31 lowercase letters, digits and hyphens: the directory's name. */
    key: string;
    /** 1 to 64 characters. */
    name: string;
    /** 1 to 32 characters. */
    version: string;
    capabilities: Capability[];
    /** Required with the network capability; shown when the user enables the plugin. */
    network?: { purpose: string };
    /** Secrets the plugin page asks for. They need the network capability. */
    credentials?: CredentialSlot[];
    /** Request attributes the plugin registers when it is enabled. */
    metadata?: MetadataAttribute[];
    /** Shown as written in the dialog that enables the plugin; at most 1000 characters. */
    notice?: string;
    /** The fields of the settings form, in order; at most 64. */
    settings?: Setting[];
    /** Open-source projects the plugin page credits; at most 8. */
    credits?: Credit[];
    /** What the plugin keeps in astrlink.store, shown on the plugin page; at most 8. Needs storage. */
    storage?: StorageItem[];
    /** Read-only queries the model may run; at most 8. Needs model.tools and callTool. */
    tools?: Tool[];
    /**
     * apiVersion 2: how long, in milliseconds, the plugin needs at its
     * extension points, within the host's bounds (onRequest and route 5000,
     * beforeForward 5000, beforeUpstream 5000, rewriteResponse 5000,
     * onStreamEvent 1000 per event, onResponse and onEvent 60000,
     * onSchedule 300000, onHttp and onUiCall 30000). The user may change
     * them.
     */
    limits?: Partial<Record<LimitedHook, number>>;
    /**
     * apiVersion 2: refuse a request when onRequest fails or runs out of
     * time, instead of letting it in. Needs request.gate and the user's
     * consent.
     */
    failClosed?: boolean;
    /** apiVersion 2: the intervals onSchedule runs at; needs schedule. At most 8. */
    schedules?: Schedule[];
    /**
     * apiVersion 2: the kinds of provider the plugin adapts; needs
     * provider. At most 4.
     */
    providers?: Provider[];
  }

  /**
   * A kind of provider the plugin adapts (plugin platform v2, §4). The user
   * adds providers of it like any other, with an address, a key and models;
   * the gateway hands buildRequest the request it would have sent upstream
   * and keeps doing the rest: routing, failover, the privacy check, the
   * records and the price.
   */
  interface Provider {
    /** A lowercase letter, then up to 31 lowercase letters, digits and -. */
    id: string;
    /** What the providers page offers it as; 1 to 64 characters. */
    name: string;
    /** The address a new provider of this kind starts with. */
    baseUrl?: string;
    /**
     * How the gateway sends the provider's key, which the plugin never sees:
     * bearer (Authorization: Bearer), x-api-key, google (X-Goog-Api-Key),
     * none, or a header of its own.
     */
    auth?: "bearer" | "x-api-key" | "google" | "none" | { header: string };
    /**
     * The protocols the plugin serves: whole responses unless sync is false,
     * streams when stream is set. A request the provider cannot serve this
     * way never reaches it.
     */
    protocols: Partial<
      Record<ProviderProtocol, { stream?: boolean; sync?: boolean }>
    >;
    /**
     * Lets the gateway convert a request in another protocol into the first
     * of protocols (in the order of ProviderProtocol), and the answer back.
     */
    convert?: boolean;
    /** The models a new provider of this kind starts with. */
    models?: string[];
  }

  type ProviderProtocol =
    | "openai.chat"
    | "openai.responses"
    | "anthropic.messages"
    | "google.generate_content";

  /** What every hook of a provider adapter gets. */
  interface ProviderContext {
    /** chat for a request, models for the model list. */
    purpose: "chat" | "models";
    /** The id of meta.providers the provider is of. */
    provider: string;
    /** The provider's address, as the user set it. */
    baseUrl: string;
    /** The plugin's settings. */
    settings: Record<string, unknown>;
    service: { id: string; name: string };
    /** chat: the protocol the body speaks, converted already when convert is set. */
    protocol?: ProviderProtocol;
    model?: string;
    stream?: boolean;
    /** chat: which attempt of the request this is, from 0. */
    attempt?: number;
    /** chat: anthropic-version, anthropic-beta and openai-beta, when the client sent them. */
    headers?: Record<string, string>;
    /** chat, buildRequest only: the request body, in protocol. */
    body?: any;
  }

  /**
   * What buildRequest returns: the upstream request. The gateway adds the
   * key, sends it through the provider's proxy, and keeps it on the
   * provider's address. For purpose models, null when the provider lists
   * no models.
   */
  interface ProviderRequest {
    /** POST by default. */
    method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
    /** A path the provider's address takes, "/chat", or an address on its host. */
    url: string;
    /** Never credentials or the gateway's headers, which the gateway drops. */
    headers?: Record<string, string>;
    /** JSON, or a string sent as it is. */
    body?: unknown;
    /** Whether the upstream answers with a stream: the request's own by default. */
    stream?: boolean;
  }

  /** An upstream answer, as parseResponse and parseError get it. */
  interface ProviderResponse {
    status: number;
    /** In lowercase. */
    headers: Record<string, string>;
    body: string;
    /** The body parsed, when it is JSON. */
    json?: any;
  }

  /**
   * What parseResponse returns: the response of ctx.protocol, its usage
   * where the protocol carries it; for purpose models, { models }.
   */
  interface ProviderResult {
    status?: number;
    json?: unknown;
    models?: string[];
  }

  /** One upstream stream event: server-sent, or a line of JSON. */
  interface UpstreamEvent {
    event?: string;
    data: string;
    id?: string;
    json?: any;
  }

  /**
   * What parseStreamEvent and finishStream return: events of ctx.protocol,
   * which the gateway writes as server-sent events (data: a string as it
   * is, anything else as JSON), the state the next call gets, and done to
   * stop reading the upstream.
   */
  interface StreamStep<State = unknown> {
    events?: Array<{ event?: string; data: unknown }>;
    state?: State;
    done?: boolean;
  }

  /** What parseError returns: retryable lets another provider serve the request. */
  interface ProviderError {
    status?: number;
    code?: string;
    message?: string;
    retryable?: boolean;
  }

  /** The extension points with a time limit. */
  type LimitedHook =
    | "onRequest"
    | "route"
    | "beforeForward"
    | "beforeUpstream"
    | "rewriteResponse"
    | "onStreamEvent"
    | "onResponse"
    | "onEvent"
    | "onSchedule"
    | "onHttp"
    | "onUiCall"
    /** buildRequest, parseResponse, parseError and finishStream: 1000, at most 5000. */
    | "provider"
    /** parseStreamEvent, each event: 50, at most 1000. */
    | "providerEvent";

  interface Schedule {
    /** A lowercase letter, then up to 31 lowercase letters, digits, _ and -. */
    name: string;
    /** 1 to 10080. */
    everyMinutes: number;
  }

  /** A text in each interface language, or one string for all of them. */
  type Localized = string | Partial<Record<Language, string>>;

  /**
   * A read-only query the plugin offers the model. The gateway writes how to
   * run it into the request, in the session's language: the model runs it
   * with its own shell, against the gateway on the same computer, under a
   * credential that works for its session and this plugin only.
   */
  interface Tool {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    name: string;
    /** What the query finds, or the report keeps, and when to run it; at most 300 characters in each language. */
    description: Localized;
    /** At most 8. */
    params?: ToolParam[];
    /**
     * Runs before the session has a value of each of the plugin's
     * attributes, such as listing the projects; such a query reaches the
     * model only through a block that names it, as {{lookup:name}}.
     */
    unbound?: boolean;
    /**
     * A report: the model hands the plugin something to keep. Its command
     * posts the parameters; it needs the session's values, has limits of
     * its own (2 a turn, 6 a minute) and answers without the data notice.
     */
    writes?: boolean;
    /** The key of a switch in settings that turns the tool on and off. */
    setting?: string;
  }

  /** A string or number parameter. Strings are trimmed; anything not declared is refused. */
  interface ToolParam {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    name: string;
    type: "string" | "number";
    required?: boolean;
    /** Characters a string takes, at most 2000; 500 by default. */
    maxLength?: number;
    min?: number;
    max?: number;
    /** What the command shows the model to put there; at most 100 characters. A required parameter needs one. */
    example?: Localized;
  }

  /** One kind of thing a plugin keeps. */
  interface StorageItem {
    /** 1 to 100 characters. */
    label: string;
    /** Whether it holds conversation text. */
    content?: boolean;
    /** How long it stays, such as 「停用插件时可以清除」; 1 to 64 characters. */
    retention: string;
  }

  /**
   * A field of the settings form. `key` is where the value sits in the
   * configuration, a dot stepping into an object, such as `quotas.skill`.
   * The keys starting with $ place the host's own settings and take only
   * `group` and `advanced`: `$wait` how long a request waits for the plugin,
   * `$trusted` whether its text skips the privacy check, `$origins` the
   * addresses it may reach (needs network), `$clients` the clients it is off
   * for, `$workspace` whether a new session takes the value its working
   * directory had (needs metadata), `$refresh` whether the host checks a
   * session's resident block for newer data (needs context.inject),
   * `$lookup` whether the model may run the plugin's queries (needs
   * meta.tools), `$limits` the time limits of its extension points,
   * `$failClosed` whether its entry hook fails requests closed (needs
   * meta.failClosed).
   */
  type Setting =
    | {
        key:
          | "$wait"
          | "$trusted"
          | "$origins"
          | "$clients"
          | "$workspace"
          | "$refresh"
          | "$lookup"
          | "$limits"
          | "$failClosed";
        group?: string;
        advanced?: boolean;
      }
    | {
        key: string;
        /** 1 to 32 characters. */
        label: string;
        /**
         * server is an address the plugin reaches: the host lets the plugin
         * reach its origin and sends `credential` there. It has no default.
         */
        type: "server" | "text" | "number" | "switch" | "choice";
        /** The heading the field sits under; at most 16 characters. */
        group?: string;
        /** Fields of one row sit on one line under its name; they follow one another. */
        row?: string;
        /** Shown behind the field's help button; at most 500 characters. */
        help?: string;
        /** What astrlink.config.get() returns while the configuration has no value. */
        default?: string | number | boolean;
        required?: boolean;
        /** Under「更多设置」. */
        advanced?: boolean;
        min?: number;
        max?: number;
        step?: number;
        unit?: string;
        placeholder?: string;
        options?: Array<{ value: string; label: string }>;
        credential?: string;
      };

  /** An open-source project the plugin builds on or talks to. */
  interface Credit {
    name: string;
    owner?: string;
    /** An SPDX identifier, such as Apache-2.0. */
    license?: string;
    /** An https address; a GitHub repository also shows its stars. */
    url: string;
  }

  /**
   * A secret the user enters on the plugin page and binds to one allowed
   * origin. A request naming it gets `header: scheme value` when it goes to
   * that origin; the plugin never reads the value.
   */
  interface CredentialSlot {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    name: string;
    label: string;
    /** Such as Authorization; never Host, Proxy-* or X-AstrLink-*. */
    header: string;
    /** Such as Bearer. */
    scheme?: string;
  }

  /** A request attribute: values come from headers, session markers and token defaults. */
  interface MetadataAttribute {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    key: string;
    label: string;
    /** Validates every value; anchored with ^ and $. `none` is always accepted and leaves a request out. */
    pattern: string;
    /** The line `<marker>value</marker>` a reply carries to bind the session; it must not name the gateway. */
    marker?: string;
  }

  type Protocol =
    | "openai.chat"
    | "openai.responses"
    | "anthropic.messages"
    | "google.generate_content";

  interface MetaValue {
    value: string;
    /** Request header, session binding, or access token default. */
    source: "header" | "session" | "token";
  }

  /** The session and turn of a request. */
  interface SessionRef {
    id: string;
    turn: number;
    /** Set in the first turn the plugin takes part in: the session's first, or the first since the plugin was enabled. */
    first?: boolean;
  }

  interface RequestInfo {
    protocol: Protocol;
    /** The model the client asked for, before any redirect. */
    model: string;
    /** claude_code, codex and so on, or unknown. */
    client: string;
    stream: boolean;
    meta: Record<string, MetaValue>;
    session?: SessionRef;
  }

  /** The interface language, which everything a plugin injects is written in. */
  type Language = "en" | "zh-CN" | "ja";

  /**
   * Why prepareSession runs: the session's first request since the gateway
   * started, a new binding, new settings or version, a compacted history, the
   * user's refresh, or the automatic check for newer data.
   */
  type SessionReason =
    | "start"
    | "bound"
    | "config"
    | "compact"
    | "manual"
    | "auto";

  /** What prepareSession sees. */
  interface SessionView {
    protocol: Protocol;
    model: string;
    client: string;
    meta: Record<string, MetaValue>;
    session: SessionRef;
    /** The interface language the session started with; it does not change within the session. */
    language: Language;
    /** Only in prepareSession. */
    reason?: SessionReason;
    /** The last element of the working directory the client named, never the whole path. Needs request.read. */
    workspace?: { name: string };
    /** Only in prepareSession: the privacy policy replaces what it finds with placeholders, so the plugin's data holds them too. */
    redacted?: boolean;
  }

  /** What prepareTurn and observeTurn see: the turn as the client sent it, before anything was injected. */
  interface TurnView extends SessionView {
    stream: boolean;
    user: {
      /** The turn's opening user message, at most 200,000 characters. */
      text: string;
      /** The same without client wrappers such as <system-reminder>. */
      typed: string;
      /** The previous turn's typed text, at most 4 KiB. */
      previous?: string;
    };
    /**
     * Only in observeTurn: the turn came while the session had no value of
     * the plugin's attributes and is captured now that it has one, with
     * that value in meta.
     */
    late?: boolean;
  }

  /**
   * A block to inject. Items are added one by one within the budget; the
   * ones past it are dropped. In a prepareTurn block, {{lookup:name}} in an
   * item becomes the command that runs the plugin's query of that name; an
   * item whose query the session cannot run, as when the client offers the
   * model no shell, is dropped.
   */
  type Block = string | { open?: string; items: string[]; close?: string };

  type ViewBlock =
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

  /** How a hook describes its part in a request: the event in the gateway lane and its details. */
  interface PluginRecord {
    /** The event's name, such as 「记忆」; at most 32 characters. */
    title: string;
    status: "ok" | "skipped" | "failed";
    /** At most 256 characters. */
    summary: string;
    /** At most 16 KiB as JSON. */
    view?: ViewBlock[];
    /** Buttons for the record's session, at most 3. A click calls onAction. */
    actions?: RecordAction[];
  }

  /** A button on a record. */
  interface RecordAction {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    id: string;
    /** At most 16 characters. */
    label: string;
    /** Asked before the click goes to the plugin; at most 300 characters. */
    confirm?: string;
  }

  interface PrepareSessionResult {
    /** Appended to the system prompt. Keep it stable within a session: a change rewrites the prompt cache. */
    system?: Block;
    record?: PluginRecord;
  }

  interface PrepareTurnResult {
    /** Put at the start of the turn's opening user message, and replayed there on later requests. */
    context?: Block;
    /** Why the turn gets nothing, such as no_project or short_query. */
    skip?: string;
    /**
     * Values the block offers for the plugin's attributes, at most 50 each.
     * A later turn whose whole message is one of them, case aside, binds the
     * session to it there and then.
     */
    choices?: Record<string, string[]>;
    record?: PluginRecord;
  }

  /** What onAction sees: the button clicked and the record's session with its bindings. */
  interface ActionView {
    action: string;
    session: SessionRef;
    /** The session's bindings; needs request.read. */
    meta: Record<string, MetaValue>;
    language: Language;
  }

  interface ActionResult {
    /** The session's next request computes the resident block again. */
    refresh?: boolean;
    /** Shown after the click; at most 256 characters. */
    message?: string;
  }

  /** What callTool sees: the query the model ran, its checked input, and the session's bindings. */
  interface ToolView {
    name: string;
    /** The parameters as the tool declares them, after the privacy check of the strings. */
    input: Record<string, string | number>;
    session: SessionRef;
    /** The session's bindings as its last request had them; needs request.read. */
    meta: Record<string, MetaValue>;
    language: Language;
  }

  interface ToolResult {
    /** What the model reads, at most 8 KiB; the gateway marks a query's as data, not instructions. */
    text: string;
    /** How many things the text holds, for the plugin's statistics. */
    hits?: number;
    record?: PluginRecord;
  }

  /** What describeStatus and testConnection see. */
  interface PageView {
    language: Language;
  }

  /** A value sessions are bound to. */
  interface ValueStat {
    value: string;
    sessions: number;
    /** When one of those sessions last made a request, in milliseconds since the Unix epoch; 0 for never. */
    lastUsedAt: number;
  }

  interface ObserveTurnResult {
    record?: PluginRecord;
  }

  interface TestConnectionResult {
    status: "ok" | "error";
    summary: string;
    view?: ViewBlock[];
  }

  // ---- apiVersion 2: the extension points of a request ----

  /** What every extension point sees of the request. */
  interface RequestSummary {
    /** The request record's id. */
    id: string;
    protocol: Protocol;
    /** The model the client asked for; after route, the one it resolved. */
    model: string;
    client: string;
    stream: boolean;
    session?: { id: string; turn: number };
    meta: Record<string, MetaValue>;
    /** What onRequest hooks tagged the request with so far. */
    tags: string[];
    language: Language;
    /** The size of the client's body. */
    bytes: number;
  }

  /**
   * What onRequest and beforeForward see. body is the client's request
   * body, with request.read (and with request.write.raw in beforeForward),
   * as the privacy check leaves it unless the user trusts the plugin.
   */
  interface RequestContext {
    request: RequestSummary;
    body?: unknown;
  }

  /** onRequest's answer: let in, tagged, or refused (needs request.gate). */
  type RequestDecision =
    | { action?: "allow"; tags?: string[] }
    | {
        action: "deny";
        /** 400 to 599; 403 by default. */
        status?: number;
        /** A lowercase letter, then letters, digits and _; plugin_denied by default. */
        code?: string;
        /** What the client reads; at most 500 characters. */
        message: string;
        tags?: string[];
      };

  /** A provider the request may go to, as route sees it. */
  interface RouteCandidate {
    /** Names the provider in the decision. */
    id: string;
    name: string;
    kind: string;
    model: string;
  }

  interface RouteContext extends RequestContext {
    /** In the order the request would try them. */
    candidates: RouteCandidate[];
  }

  interface RouteDecision {
    /** These first, in this order; the rest after, as they were. */
    order?: string[];
    /** Left out; excluding every candidate excludes none. */
    exclude?: string[];
    /** Resolves the request for this model instead, once. */
    model?: string;
    /** Shown in the request record. */
    reason?: string;
  }

  /** beforeForward's changes. Each plugin sees the body the one before left. */
  interface ForwardEdits {
    /** Appended to the system prompt; needs request.write. Keep it stable within a session. */
    system?: { append: string };
    /**
     * Put at the start of the turn's opening user message; needs
     * request.write. The turn's first call settles it: its later calls (tool
     * loops) and later turns put the same bytes back, so the prompt cache
     * holds.
     */
    turn?: { prepend: string };
    /** Generation parameters in the protocol's own fields; null removes one. Needs request.write. */
    params?: Partial<
      Record<
        | "temperature"
        | "top_p"
        | "top_k"
        | "max_tokens"
        | "stop"
        | "seed"
        | "presence_penalty"
        | "frequency_penalty",
        unknown
      >
    >;
    /** Function tools added or removed by name; needs request.write. */
    tools?: { add?: ToolSpec[]; remove?: string[] };
    /** Replaces the whole body; needs request.write.raw. */
    body?: unknown;
  }

  interface ToolSpec {
    /** 1 to 64 letters, digits, _ and -. */
    name: string;
    description?: string;
    /** A JSON Schema object. */
    parameters?: Record<string, unknown>;
  }

  /** What beforeUpstream sees: one attempt, in its provider's protocol. */
  interface UpstreamContext {
    request: RequestSummary;
    attempt: {
      provider: string;
      kind: string;
      protocol: string;
      model: string;
      index: number;
    };
    /** The body the attempt sends. */
    body: unknown;
  }

  interface UpstreamEdits {
    body?: unknown;
    /** Never credentials, nor headers the transport or the gateway owns. */
    headers?: Record<string, string>;
  }

  /** What rewriteResponse sees: a whole JSON response, in the client's protocol. */
  interface ResponseContext {
    request: RequestSummary;
    response: { status: number; body: unknown };
  }

  interface ResponseEdits {
    body?: unknown;
  }

  /** One event of a server-sent stream; data is parsed when it is JSON. */
  interface StreamEvent {
    event?: string;
    id?: string;
    data: unknown;
  }

  /**
   * onStreamEvent's answer: the events to send in the event's place (none
   * drops it; undefined sends it as it came), and the state the next call
   * gets.
   */
  interface StreamResult<State = unknown> {
    events?: StreamEvent[];
    state?: State;
  }

  /** What onResponse sees once the response ended, and onEvent without text. */
  interface ResponseSummary {
    status: string;
    httpStatus?: number;
    provider?: string;
    model?: string;
    elapsedMs: number;
    usage?: {
      input: number;
      output: number;
      cacheRead?: number;
      cacheWrite?: number;
    };
    error?: string;
    /** The reply's visible text, as the privacy check left it; onResponse only. */
    text?: string;
  }

  interface ObserveContext {
    request: RequestSummary;
    response: ResponseSummary;
  }

  interface GatewayEvent {
    type: "request.finished";
    request: RequestSummary;
    response: ResponseSummary;
  }

  interface ScheduleRun {
    name: string;
    everyMinutes: number;
    language: Language;
  }

  /** A request to /plugins/<key>/… on the inference listener; credentials are left out. */
  interface HttpServeRequest {
    method: string;
    /** What follows /plugins/<key>, "/" at least. */
    path: string;
    query: Record<string, string>;
    /** In lowercase. */
    headers: Record<string, string>;
    body?: string;
    json?: unknown;
  }

  interface HttpServeResponse {
    /** 200 by default. */
    status?: number;
    /** Never cookies or cross-origin headers. */
    headers?: Record<string, string>;
    body?: string;
    json?: unknown;
  }

  /**
   * A call of the plugin's interface code, as onUiCall(call) gets it: what
   * call(name, input) of @astrlink/plugin/client sent. What the hook
   * returns goes back as JSON. Needs ui.code.
   */
  interface UiCall {
    name: string;
    input: unknown;
    language: Language;
  }

  /** What astrlink.textStream takes. */
  interface TextStreamOptions {
    /** Changes each piece of the reply's text as it comes. */
    map?: (text: string) => string;
    /** Replacements that also hold where a piece ends inside a match. */
    replace?: Array<[string | RegExp, string | ((match: string) => string)]>;
    /** The longest a pattern's match may be; 64 by default. */
    maxMatch?: number;
  }

  interface HttpRequest {
    /** HEAD and OPTIONS need apiVersion 2. */
    method: "GET" | "HEAD" | "OPTIONS" | "POST" | "PUT" | "PATCH" | "DELETE";
    /** An absolute http or https URL to an origin the user allows. */
    url: string;
    /** Never Authorization, Cookie, Host, Content-Length, Proxy-* or X-AstrLink-*. */
    headers?: Record<string, string>;
    /** Sent as application/json. A request has json or body, not both; a GET has neither. At most 1 MiB. */
    json?: unknown;
    body?: string;
    /** A slot of meta.credentials. */
    credential?: string;
    /** At most 30000; the hook's network budget can end a request sooner. Default 10000. */
    timeoutMs?: number;
  }

  interface HttpResponse {
    /**
     * 0 when no response arrived; error then says why. HTTP errors come back
     * as they are. A body that could not be read in full (too_large, or a
     * timeout while it came) keeps the status that arrived, with error set
     * and neither json nor text.
     */
    status: number;
    /** The first value of each header, keyed in lowercase. */
    headers: Record<string, string>;
    /** The body when it parses as JSON; text otherwise. At most 1 MiB. */
    json?: unknown;
    text?: string;
    error?: "denied" | "timeout" | "network" | "too_large" | "budget";
    took_ms: number;
  }
}

/**
 * The host functions. They work only while the host runs a hook, not in a
 * module's top level, and each throws a TypeError when its capability was
 * not granted.
 */
/**
 * The host functions. They work only while the host runs a hook, not in a
 * module's top level, and each throws a TypeError when its capability was
 * not granted. In apiVersion 2 the ones that wait on the host return
 * promises; in apiVersion 1 they return their results.
 */
declare const astrlink: {
  request: {
    /** Needs request.read. null outside a request, as on the plugin page. */
    info(): AstrLinkPlugin.RequestInfo | null;
  };
  http: {
    /**
     * Needs network. An array runs in parallel; at most 8 run at once, 16 in
     * an apiVersion 1 call and 32 in an apiVersion 2 one. Waits do not count
     * against the 200 ms of script time. fetch() is the same with the web's
     * Response.
     */
    fetch(
      request: AstrLinkPlugin.HttpRequest,
    ): Promise<AstrLinkPlugin.HttpResponse>;
    fetch(
      requests: AstrLinkPlugin.HttpRequest[],
    ): Promise<AstrLinkPlugin.HttpResponse[]>;
  };
  /** Needs storage. JSON values of at most 64 KiB each, 1 MiB in all; keys of 1 to 256 characters. */
  store: {
    /** null when the key has no value. */
    get(key: string): Promise<unknown>;
    set(key: string, value: unknown): Promise<void>;
    delete(key: string): Promise<void>;
    /** Sorted. */
    keys(prefix?: string): Promise<string[]>;
  };
  config: {
    /** The plugin's configuration, a fresh copy on each call; plugins cannot change it. */
    get(): Record<string, unknown>;
  };
  meta: {
    /**
     * Needs request.read. The values sessions are bound to for one of the
     * attributes in meta.metadata, the most recently used first; limit is 1
     * to 200, 50 by default.
     */
    values(key: string, limit?: number): Promise<AstrLinkPlugin.ValueStat[]>;
  };
  util: {
    /** Lowercase hex of the SHA-256 of the UTF-8 text. */
    sha256(text: string): string;
  };
  /** At most 20 lines a call, 1 KiB each; console writes here too. */
  log(level: "debug" | "info" | "warn", message: string): void;
  clock: {
    /** Milliseconds since the Unix epoch. */
    now(): number;
    /**
     * How many minutes the gateway's time zone is ahead of UTC at a moment
     * in milliseconds since the Unix epoch, now by default. Date keeps to
     * UTC in a plugin; this writes dates as the user reads them.
     */
    offset(at?: number): number;
  };
  /**
   * The stream hooks of a plugin that changes only the reply's visible
   * text, in every protocol: `export const { onStreamEvent, onStreamEnd } =
   * astrlink.textStream({ … })`. Needs response.write.
   */
  textStream(options: AstrLinkPlugin.TextStreamOptions): {
    onStreamEvent(
      ctx: AstrLinkPlugin.RequestSummary,
      event: AstrLinkPlugin.StreamEvent,
      state: unknown,
    ): AstrLinkPlugin.StreamResult;
    onStreamEnd(
      ctx: AstrLinkPlugin.RequestSummary,
      state: unknown,
    ): { events: AstrLinkPlugin.StreamEvent[] };
  };
};

/** fetch() takes two fields of its own beside the web's. */
interface RequestInit {
  /** A slot of meta.credentials, which the gateway writes into the request; the plugin never reads it. */
  credential?: string;
  /** At most 30000; the hook's time limit can end a request sooner. Default 10000. */
  timeoutMs?: number;
}
