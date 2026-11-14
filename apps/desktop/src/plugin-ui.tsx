import * as React from "react";
import {
  Component,
  useEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
  type ComponentType,
  type ErrorInfo,
  type ReactNode,
} from "react";
import * as ReactDOM from "react-dom";
import * as JSXRuntime from "react/jsx-runtime";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataField, DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";

import {
  callPluginUi,
  getPlugin,
  getPluginHostSettings,
  listPlugins,
  updatePlugin,
} from "./bridge";
import { isWebEdition } from "./edition";
import { i18n, useT } from "./i18n";
import { notify } from "./notify";
import { useResolvedTheme } from "./theme";
import { getDesktopPlatform } from "./window-chrome";

/**
 * Plugins' interface code (plugin platform v2, §6). A plugin's client.js,
 * which Core builds from its client entry, runs in this window: Core wraps
 * it in a call of `__astrlinkPlugins.define`, and the code registers its
 * components into slots through the SDK it imports as
 * `@astrlink/plugin/client`. React and the component library are this
 * window's own. Every component renders inside an error boundary; a plugin
 * whose interface fails again and again is turned off. Safe mode loads no
 * plugin's interface, and the window enters it by itself when the last
 * load did not finish.
 */

/** The slots plugins' interfaces fill. */
export const PLUGIN_SLOTS = [
  "page",
  "plugin.tab",
  "overview.widget",
  "request.tab",
  "session.section",
  "service.section",
] as const;
export type PluginSlotName = (typeof PLUGIN_SLOTS)[number];

/** A component a plugin registered for a slot. */
export interface PluginSlotEntry {
  id: string;
  plugin: string;
  pluginName: string;
  slot: PluginSlotName;
  title?: string;
  order: number;
  Component: ComponentType<{ context: unknown }>;
}

/**
 * Where a plugin's interface may send the window: a page of AstrLink, or a
 * page the plugin registered, its first unless `entry` names one.
 */
export type PluginNavigation =
  | { page: "overview" | "records" | "plugins" | "settings" }
  | { page: "plugin"; entry?: string };

/** Must match `SCHEME` in plugin_client.rs. */
export const PLUGIN_SCHEME = "astrlink-plugin";

const SAFE_MODE_KEY = "astrlink:plugin-ui-safe-mode";
const LOADING_KEY = "astrlink:plugin-ui-loading";
/** How long after loading the window must run before the load counts as fine. */
const SETTLE_MS = 10_000;
/** An unfinished load older than this no longer means the last start failed. */
const STALE_LOAD_MS = 60 * 60 * 1000;
/** Failures within FAILURE_WINDOW_MS that turn a plugin's interface off. */
const MAX_FAILURES = 3;
const FAILURE_WINDOW_MS = 60_000;

export type SafeMode = "off" | "manual" | "crashed";

function readStorage(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Without storage, safe mode lasts as long as the window.
  }
}

/** Whether the last load of plugins' interfaces left the window running. */
function initialSafeMode(): SafeMode {
  if (readStorage(SAFE_MODE_KEY) === "1") return "manual";
  const loading = Number(readStorage(LOADING_KEY));
  if (loading > 0 && Date.now() - loading < STALE_LOAD_MS) {
    writeStorage(LOADING_KEY, null);
    return "crashed";
  }
  return "off";
}

type Listener = () => void;

interface PluginUiSnapshot {
  entries: PluginSlotEntry[];
  safeMode: SafeMode;
  /** The plugins whose interface runs, by key, with their code's version. */
  loaded: Record<string, string>;
}

/** The registry of the plugins' slots, and what loaded. */
class PluginUiStore {
  private snapshot: PluginUiSnapshot = {
    entries: [],
    safeMode: "off",
    loaded: {},
  };
  private listeners = new Set<Listener>();
  private failures = new Map<string, number[]>();
  private revisionListeners = new Set<Listener>();
  navigator: ((plugin: string, to: PluginNavigation) => void) | null = null;

  subscribe = (listener: Listener) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  get = () => this.snapshot;

  private set(next: Partial<PluginUiSnapshot>) {
    this.snapshot = { ...this.snapshot, ...next };
    this.listeners.forEach((listener) => listener());
  }

  setSafeMode(mode: SafeMode) {
    this.set({ safeMode: mode });
  }

  register(entry: PluginSlotEntry) {
    const entries = [...this.snapshot.entries, entry].sort(
      (a, b) => a.order - b.order || a.pluginName.localeCompare(b.pluginName),
    );
    this.set({ entries });
  }

  unregister(id: string) {
    this.set({
      entries: this.snapshot.entries.filter((entry) => entry.id !== id),
    });
  }

  /** Drops what a plugin registered, as its interface stops. */
  drop(key: string) {
    const loaded = { ...this.snapshot.loaded };
    delete loaded[key];
    this.set({
      entries: this.snapshot.entries.filter((entry) => entry.plugin !== key),
      loaded,
    });
  }

  markLoaded(key: string, version: string) {
    this.set({ loaded: { ...this.snapshot.loaded, [key]: version } });
  }

  /**
   * Counts a failure of a plugin's interface: past MAX_FAILURES within
   * FAILURE_WINDOW_MS it is turned off, here and in Core.
   */
  fail(key: string, name: string) {
    const now = Date.now();
    const recent = (this.failures.get(key) ?? []).filter(
      (at) => now - at < FAILURE_WINDOW_MS,
    );
    recent.push(now);
    this.failures.set(key, recent);
    if (recent.length < MAX_FAILURES || !(key in this.snapshot.loaded)) return;
    this.failures.delete(key);
    this.drop(key);
    notify.warning(i18n.t("plugins.ui.turnedOff", { name }));
    void updatePlugin(key, { ui_enabled: false }).catch(() => {
      // Dropped here at least; the plugin page shows it on.
    });
  }

  /** Asks the loader to read the plugins again. */
  refresh() {
    this.revisionListeners.forEach((listener) => listener());
  }

  onRefresh(listener: Listener) {
    this.revisionListeners.add(listener);
    return () => {
      this.revisionListeners.delete(listener);
    };
  }
}

export const pluginUi = new PluginUiStore();

/** Reads the plugins again: one was enabled, changed or removed. */
export function refreshPluginUi() {
  pluginUi.refresh();
}

/** Turns safe mode on or off; off loads the plugins' interfaces again. */
export function setPluginUiSafeMode(on: boolean) {
  writeStorage(SAFE_MODE_KEY, on ? "1" : null);
  pluginUi.setSafeMode(on ? "manual" : "off");
  if (on) {
    for (const key of Object.keys(pluginUi.get().loaded)) pluginUi.drop(key);
  } else {
    pluginUi.refresh();
  }
}

export function usePluginUi(): PluginUiSnapshot {
  return useSyncExternalStore(pluginUi.subscribe, pluginUi.get, pluginUi.get);
}

/** The pages plugins' interfaces registered, now. */
export function pluginUiPages(): PluginSlotEntry[] {
  return pluginUi.get().entries.filter((entry) => entry.slot === "page");
}

/** The components registered for a slot, of one plugin when `plugin` is set. */
export function usePluginSlot(
  slot: PluginSlotName,
  plugin?: string,
): PluginSlotEntry[] {
  const { entries } = usePluginUi();
  return useMemo(
    () =>
      entries.filter(
        (entry) =>
          entry.slot === slot &&
          (plugin === undefined || entry.plugin === plugin),
      ),
    [entries, slot, plugin],
  );
}

/** Keeps a plugin component's failure to itself. */
class PluginBoundary extends Component<
  { entry: PluginSlotEntry; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, _info: ErrorInfo) {
    console.error(`plugin ${this.props.entry.plugin}: ${error.message}`);
    pluginUi.fail(this.props.entry.plugin, this.props.entry.pluginName);
  }

  render() {
    if (this.state.error) {
      return (
        <FormMessage data-testid="plugin-ui-failed" tone="error">
          {i18n.t("plugins.ui.failed", {
            name: this.props.entry.pluginName,
            message: this.state.error.message,
          })}
        </FormMessage>
      );
    }
    return this.props.children;
  }
}

/** One plugin component, inside its error boundary. */
export function PluginSlotView({
  entry,
  context,
}: {
  entry: PluginSlotEntry;
  context?: unknown;
}) {
  const Rendered = entry.Component;
  return (
    <PluginBoundary entry={entry}>
      <Rendered context={context} />
    </PluginBoundary>
  );
}

/**
 * What the plugins registered for a slot, each with its plugin's name over
 * it and inside its own error boundary.
 */
export function PluginSlot({
  name,
  context,
  className,
}: {
  name: PluginSlotName;
  context?: unknown;
  className?: string;
}) {
  const entries = usePluginSlot(name);
  if (entries.length === 0) return null;
  return (
    <div className={className} data-plugin-slot={name}>
      {entries.map((entry) => (
        <section
          aria-label={entry.title ?? entry.pluginName}
          data-plugin={entry.plugin}
          key={entry.id}
        >
          <PluginSlotView context={context} entry={entry} />
        </section>
      ))}
    </div>
  );
}

/** Where the window loads a plugin's interface code from. */
export function pluginClientURL(key: string, version: string): string {
  const file = `${key}/client.js?v=${encodeURIComponent(version)}`;
  if (isWebEdition) return `/control/v1/plugins/${file}`;
  return getDesktopPlatform() === "windows"
    ? `http://${PLUGIN_SCHEME}.localhost/${file}`
    : `${PLUGIN_SCHEME}://localhost/${file}`;
}

/** The plugin whose interface code a script URL is, if any. */
export function pluginOfScript(url: string | undefined): string | null {
  const match =
    /(?:astrlink-plugin:\/\/localhost|astrlink-plugin\.localhost|\/control\/v1\/plugins)\/([a-z][a-z0-9-]{0,31})\/client\.js/.exec(
      url ?? "",
    );
  return match ? match[1] : null;
}

type ClientFactory = (client: PluginClientHost) => void;

/** What a plugin's wrapped code receives: the modules it imports. */
export interface PluginClientHost {
  module(name: string): unknown;
}

declare global {
  interface Window {
    __astrlinkPlugins?: {
      define(key: string, factory: ClientFactory): void;
    };
  }
}

const defining = new Map<string, (factory: ClientFactory) => void>();

function installDefine() {
  if (window.__astrlinkPlugins) return;
  window.__astrlinkPlugins = {
    define(key, factory) {
      const waiting = defining.get(key);
      if (!waiting || typeof factory !== "function") return;
      defining.delete(key);
      waiting(factory);
    },
  };
}

/** Loads a plugin's wrapped interface code; resolves with its factory. */
function loadScript(key: string, url: string): Promise<ClientFactory> {
  installDefine();
  return new Promise((resolve, reject) => {
    defining.set(key, resolve);
    const script = document.createElement("script");
    script.src = url;
    script.async = true;
    script.dataset.plugin = key;
    script.onerror = () => {
      defining.delete(key);
      script.remove();
      reject(new Error(`could not load the interface of ${key}`));
    };
    script.onload = () => {
      script.remove();
      if (defining.get(key) === resolve) {
        defining.delete(key);
        reject(new Error(`the interface of ${key} defined nothing`));
      }
    };
    document.head.append(script);
  });
}

/** The component library plugins' interfaces share with the window. */
const components = {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  DataField,
  DataRow,
  EmptyState,
  Field,
  FormMessage,
  HelpPopover,
  Input,
  Label,
  LoadingState,
  Panel,
  PanelHeader,
  StatusBadge,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
};

/** Picks the text of the interface language, English or the first else. */
function pickText(
  texts: string | Record<string, string>,
  vars?: Record<string, unknown>,
): string {
  let text: string;
  if (typeof texts === "string") text = texts;
  else {
    const language = i18n.language;
    text =
      texts[language] ??
      texts[language.split("-")[0]] ??
      texts.en ??
      Object.values(texts)[0] ??
      "";
  }
  return text.replace(/\{\{\s*(\w+)\s*\}\}/g, (whole, name: string) =>
    vars && name in vars ? String(vars[name]) : whole,
  );
}

let registrations = 0;

/** The SDK a plugin's interface code imports, bound to the plugin. */
function sdkFor(key: string, name: string) {
  return {
    key,
    registerSlot(
      slot: PluginSlotName,
      component: ComponentType<{ context: unknown }>,
      options: { title?: string; order?: number } = {},
    ): () => void {
      if (!(PLUGIN_SLOTS as readonly string[]).includes(slot))
        throw new Error(`unknown slot ${String(slot)}`);
      if (typeof component !== "function")
        throw new Error("registerSlot takes a React component");
      const id = `${key}:${slot}:${++registrations}`;
      pluginUi.register({
        id,
        plugin: key,
        pluginName: name,
        slot,
        title: typeof options.title === "string" ? options.title : undefined,
        order: Number.isFinite(options.order) ? Number(options.order) : 0,
        Component: component,
      });
      return () => pluginUi.unregister(id);
    },
    /** Runs the plugin's onUiCall hook, in its sandbox, under its grants. */
    call: (callName: string, input?: unknown) =>
      callPluginUi(key, String(callName), input),
    async getSettings(): Promise<Record<string, unknown>> {
      return (await getPlugin(key)).config;
    },
    async saveSettings(
      config: Record<string, unknown>,
    ): Promise<Record<string, unknown>> {
      return (await updatePlugin(key, { config })).config;
    },
    language: () => i18n.language,
    t: pickText,
    navigate(to: PluginNavigation) {
      pluginUi.navigator?.(key, to);
    },
    toast(
      message: unknown,
      tone: "info" | "success" | "warning" | "error" = "info",
    ) {
      const text = String(message).slice(0, 500);
      if (tone === "success") notify.success(text);
      else if (tone === "warning") notify.warning(text);
      else if (tone === "error") notify.error(text);
      else notify.info(text);
    },
    useTheme: useResolvedTheme,
    useLanguage(): string {
      useT();
      return i18n.language;
    },
    components,
    ...components,
  };
}

function hostFor(key: string, name: string): PluginClientHost {
  const sdk = sdkFor(key, name);
  return {
    module(moduleName: string) {
      switch (moduleName) {
        case "react":
          return React;
        case "react/jsx-runtime":
          return JSXRuntime;
        case "react-dom":
          return ReactDOM;
        case "@astrlink/plugin/client":
          return sdk;
      }
      throw new Error(`the host provides no module ${moduleName}`);
    },
  };
}

/**
 * Loads the interfaces of the plugins whose code loads, and keeps them in
 * step with the plugins: a plugin turned off drops its components, a new
 * version replaces them. It renders nothing.
 */
export function PluginUiLoader({
  isReady,
  sessionKey,
  onNavigate,
}: {
  isReady: boolean;
  sessionKey: string | null;
  onNavigate: (plugin: string, to: PluginNavigation) => void;
}) {
  const navigate = useRef(onNavigate);
  navigate.current = onNavigate;
  useEffect(() => {
    pluginUi.navigator = (plugin, to) => navigate.current(plugin, to);
    pluginUi.setSafeMode(initialSafeMode());
    if (pluginUi.get().safeMode === "crashed")
      notify.warning(i18n.t("plugins.ui.crashed"));
    const onError = (event: ErrorEvent) => {
      const key = pluginOfScript(event.filename);
      if (key)
        pluginUi.fail(
          key,
          pluginUi.get().entries.find((entry) => entry.plugin === key)
            ?.pluginName ?? key,
        );
    };
    window.addEventListener("error", onError);
    return () => {
      window.removeEventListener("error", onError);
      pluginUi.navigator = null;
    };
  }, []);

  useEffect(() => {
    if (!isReady) return;
    let cancelled = false;
    let settle: ReturnType<typeof setTimeout> | undefined;
    const sync = async () => {
      if (pluginUi.get().safeMode !== "off") return;
      let wanted: { key: string; name: string; version: string }[] = [];
      try {
        if (isWebEdition && !(await getPluginHostSettings()).web_ui) {
          wanted = [];
        } else {
          wanted = (await listPlugins()).plugins.flatMap((item) =>
            item.ui_version
              ? [{ key: item.key, name: item.name, version: item.ui_version }]
              : [],
          );
        }
      } catch {
        return;
      }
      if (cancelled) return;
      const loaded = pluginUi.get().loaded;
      for (const key of Object.keys(loaded)) {
        const still = wanted.find((item) => item.key === key);
        if (!still || still.version !== loaded[key]) pluginUi.drop(key);
      }
      const toLoad = wanted.filter(
        (item) => pluginUi.get().loaded[item.key] !== item.version,
      );
      if (toLoad.length === 0) return;
      // A load that never finishes puts the next start in safe mode.
      writeStorage(LOADING_KEY, String(Date.now()));
      await Promise.all(
        toLoad.map(async (item) => {
          try {
            const factory = await loadScript(
              item.key,
              pluginClientURL(item.key, item.version),
            );
            if (cancelled || pluginUi.get().safeMode !== "off") return;
            pluginUi.markLoaded(item.key, item.version);
            factory(hostFor(item.key, item.name));
          } catch (error) {
            console.error(
              `plugin ${item.key}: ${error instanceof Error ? error.message : String(error)}`,
            );
            pluginUi.fail(item.key, item.name);
          }
        }),
      );
      clearTimeout(settle);
      settle = setTimeout(() => writeStorage(LOADING_KEY, null), SETTLE_MS);
    };
    void sync();
    const stop = pluginUi.onRefresh(() => void sync());
    return () => {
      cancelled = true;
      clearTimeout(settle);
      stop();
    };
  }, [isReady, sessionKey]);

  return null;
}
