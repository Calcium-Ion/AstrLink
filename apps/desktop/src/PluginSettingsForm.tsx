import { useId, useMemo, useState } from "react";

import { ChevronRight, Plus, Shredder as Trash2 } from "@/components/icons";
import { clientTypeName } from "@/components/ClientTypeIcon";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { PanelFooter } from "@/components/Panel";
import {
  SettingGroup,
  SettingRow,
  settingControlWidth,
} from "@/components/SettingRows";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { MultiFilterSelect } from "@/components/MultiFilterSelect";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { useT } from "./i18n";
import { hookLabel } from "./plugin-labels";
import {
  configBytes,
  MAX_PLUGIN_CONFIG_BYTES,
  PLUGIN_TURN_WAITS,
  plaintextOrigin,
  settingValue,
  stableJSON,
  validOrigin,
  withSettingValue,
  type HostSettingKey,
  type PluginCredentialStatus,
  type PluginDetail,
  type PluginSetting,
  type PluginSettingsUpdate,
} from "./plugin-model";
import { CLIENT_TYPES } from "./request-record-model";

type Translate = ReturnType<typeof useT>;
/** A field's value as the form holds it: numbers as the text typed. */
type Value = string | boolean;

/** A credential the form saves: to its server's origin unless one is given. */
export interface CredentialInput {
  name: string;
  secret: string;
  origin?: string;
}

interface Draft {
  /** The configuration the declared settings lie over; JSON edits replace it. */
  config: Record<string, unknown>;
  values: Record<string, Value>;
  wait: number;
  trusted: boolean;
  /** The client types the plugin is off for. */
  excluded: string[];
  workspace: boolean;
  refresh: boolean;
  lookup: boolean;
  /** The user's own origins, beside those of the servers. */
  origins: Array<{ origin: string; allow_plaintext: boolean }>;
  /**
   * The plain http origin the user agrees to reach for each server, by
   * setting key: an address moved elsewhere asks again.
   */
  consent: Record<string, string>;
  secrets: Record<string, string>;
  /** The origin a credential no server sends goes to. */
  credentialOrigins: Record<string, string>;
  /** The user's time limits by hook, as typed; "" follows the plugin. */
  limits: Record<string, string>;
  failClosed: boolean;
}

type Item =
  | { kind: "field"; setting: PluginSetting }
  | { kind: "row"; name: string; settings: PluginSetting[] }
  | { kind: "host"; key: HostSettingKey }
  | {
      kind: "credential";
      credential: PluginCredentialStatus;
      server?: PluginSetting;
    };

interface Group {
  title?: string;
  items: Item[];
}

const hostKeys: HostSettingKey[] = [
  "$wait",
  "$trusted",
  "$clients",
  "$workspace",
  "$refresh",
  "$lookup",
  "$limits",
  "$failClosed",
  "$origins",
];

/** The origin of an address, "" for none, to tell two servers apart. */
function originOf(raw: string): string {
  try {
    return new URL(raw.trim()).origin;
  } catch {
    return "";
  }
}

function declared(detail: PluginDetail): PluginSetting[] {
  return detail.settings.filter((setting) => !setting.key.startsWith("$"));
}

function serversOf(detail: PluginDetail): PluginSetting[] {
  return declared(detail).filter((setting) => setting.type === "server");
}

/** The origin of the address the saved configuration gives a server. */
function serverOrigin(detail: PluginDetail, setting: PluginSetting): string {
  const address = settingValue(detail.config, setting.key);
  return originOf(typeof address === "string" ? address : "");
}

/**
 * The user's own origins: neither one Core marks as a server's nor one at a
 * server's address, which Core marks on the next save.
 */
function ownOrigins(detail: PluginDetail) {
  const servers = serversOf(detail).map((setting) =>
    serverOrigin(detail, setting),
  );
  return detail.network_origins
    .filter(
      (origin) => !origin.setting && !servers.includes(originOf(origin.origin)),
    )
    .map(({ origin, allow_plaintext }) => ({ origin, allow_plaintext }));
}

/** Which host settings the plugin page offers for this plugin. */
function hostApplies(detail: PluginDetail, key: HostSettingKey): boolean {
  switch (key) {
    case "$wait":
      return (
        detail.hooks.includes("prepareTurn") ||
        detail.hooks.includes("prepareSession")
      );
    case "$trusted":
      return detail.requested_capabilities.includes("request.read");
    case "$origins":
      return detail.needs_network;
    case "$clients":
      return ["prepareSession", "prepareTurn", "observeTurn"].some((hook) =>
        detail.hooks.includes(hook),
      );
    case "$workspace":
      return detail.metadata.length > 0;
    case "$refresh":
      return (
        detail.hooks.includes("prepareSession") &&
        detail.requested_capabilities.includes("context.inject")
      );
    case "$lookup":
      return detail.tools.length > 0;
    case "$limits":
      return detail.limits.length > 0;
    case "$failClosed":
      return detail.fail_closed_requested === true;
  }
}

function valueOf(setting: PluginSetting, stored: unknown): Value {
  const present = stored === undefined ? setting.default : stored;
  switch (setting.type) {
    case "switch":
      return present === true;
    case "number":
      return typeof present === "number" ? String(present) : "";
    default:
      return typeof present === "string" ? present : "";
  }
}

function valuesOf(
  detail: PluginDetail,
  config: Record<string, unknown>,
): Record<string, Value> {
  return Object.fromEntries(
    declared(detail).map((setting) => [
      setting.key,
      valueOf(setting, settingValue(config, setting.key)),
    ]),
  );
}

function draftOf(detail: PluginDetail): Draft {
  // Consent given before holds for the same origin, also one the user
  // allowed before the server had a setting of its own.
  const consent: Record<string, string> = {};
  const credentialOrigins: Record<string, string> = {};
  for (const setting of serversOf(detail)) {
    const origin = serverOrigin(detail, setting);
    if (
      origin !== "" &&
      detail.network_origins.some(
        (allowed) =>
          allowed.allow_plaintext && originOf(allowed.origin) === origin,
      )
    )
      consent[setting.key] = origin;
  }
  for (const credential of detail.credentials) {
    if (credential.origin)
      credentialOrigins[credential.name] = credential.origin;
  }
  return {
    config: detail.config,
    values: valuesOf(detail, detail.config),
    wait: detail.turn_wait_ms,
    trusted: detail.privacy_trusted,
    excluded: detail.excluded_clients,
    workspace: detail.workspace_binding,
    refresh: detail.auto_refresh,
    lookup: detail.lookup_enabled,
    origins: ownOrigins(detail),
    consent,
    secrets: {},
    credentialOrigins,
    limits: Object.fromEntries(
      detail.limits.map((limit) => [
        limit.hook,
        limit.ms ? String(limit.ms) : "",
      ]),
    ),
    failClosed: detail.fail_closed === true,
  };
}

/** The time limits the draft changes, 0 where it follows the plugin again. */
function changedLimits(
  detail: PluginDetail,
  draft: Draft,
): Record<string, number> {
  const changed: Record<string, number> = {};
  for (const limit of detail.limits) {
    const typed = (draft.limits[limit.hook] ?? "").trim();
    const value = typed === "" ? 0 : Number(typed);
    if (value !== (limit.ms ?? 0)) changed[limit.hook] = value;
  }
  return changed;
}

/** Whether a typed time limit is one Core takes. */
function validLimit(limit: PluginDetail["limits"][number], typed: string) {
  const text = typed.trim();
  if (text === "") return true;
  const value = Number(text);
  return Number.isInteger(value) && value >= 1 && value <= limit.max_ms;
}

/** The value a field saves; undefined leaves its place empty. */
function typedValue(setting: PluginSetting, value: Value | undefined): unknown {
  if (setting.type === "switch") return value === true;
  const text = typeof value === "string" ? value.trim() : "";
  if (text === "") return undefined;
  if (setting.type === "number") {
    const number = Number(text);
    return Number.isFinite(number) ? number : undefined;
  }
  return text;
}

/**
 * The configuration the form saves: the base with each field's value. A
 * field left at its default where the base had nothing stays implicit, so
 * a later default still applies.
 */
function configOf(detail: PluginDetail, draft: Draft): Record<string, unknown> {
  let config = draft.config;
  for (const setting of declared(detail)) {
    const typed = typedValue(setting, draft.values[setting.key]);
    const stored = settingValue(draft.config, setting.key);
    if (
      typed !== undefined &&
      stored === undefined &&
      setting.default !== undefined &&
      typed === setting.default
    )
      continue;
    config = withSettingValue(config, setting.key, typed);
  }
  return config;
}

function fieldLabel(setting: PluginSetting): string {
  return setting.row
    ? `${setting.row} · ${setting.label ?? setting.key}`
    : (setting.label ?? setting.key);
}

/** What keeps the form from saving, by setting key. */
function problems(
  detail: PluginDetail,
  draft: Draft,
  t: Translate,
): Record<string, string> {
  const found: Record<string, string> = {};
  for (const limit of detail.limits) {
    if (!validLimit(limit, draft.limits[limit.hook] ?? ""))
      found[`$limits.${limit.hook}`] = t("plugins.limits.invalid", {
        point: hookLabel(limit.hook, t),
        max: limit.max_ms,
      });
  }
  for (const setting of declared(detail)) {
    if (setting.type === "switch") continue;
    const label = fieldLabel(setting);
    const raw = draft.values[setting.key];
    const text = typeof raw === "string" ? raw.trim() : "";
    if (text === "") {
      if (setting.required)
        found[setting.key] = t("plugins.settings.required", { label });
      continue;
    }
    if (setting.type === "number") {
      const number = Number(text);
      const { min, max } = setting;
      if (!Number.isFinite(number)) {
        found[setting.key] = t("plugins.settings.number", { label });
      } else if (
        (min !== undefined && number < min) ||
        (max !== undefined && number > max)
      ) {
        found[setting.key] =
          min !== undefined && max !== undefined
            ? t("plugins.settings.between", { label, min, max })
            : min !== undefined
              ? t("plugins.settings.atLeast", { label, min })
              : t("plugins.settings.atMost", { label, max });
      }
    }
    if (setting.type === "server") {
      if (!validOrigin(text)) {
        found[setting.key] = t("plugins.settings.address", { label });
      } else if (
        plaintextOrigin(text) &&
        draft.consent[setting.key] !== originOf(text)
      ) {
        found[setting.key] = t("plugins.settings.consent", { label });
      }
    }
  }
  return found;
}

/** Lays the settings out in their groups, in order, with the host's own. */
function layoutOf(detail: PluginDetail, t: Translate) {
  const main: Group[] = [];
  const advanced: Group[] = [];
  const groupFor = (groups: Group[], title: string | undefined) => {
    let group = groups.find((existing) => existing.title === title);
    if (!group) {
      group = { title, items: [] };
      groups.push(group);
    }
    return group;
  };
  const placed = new Set<string>();
  const attached = new Set<string>();
  const settings = detail.settings;
  for (let index = 0; index < settings.length; index++) {
    const setting = settings[index];
    const group = groupFor(setting.advanced ? advanced : main, setting.group);
    if (setting.key.startsWith("$")) {
      const key = setting.key as HostSettingKey;
      placed.add(key);
      if (hostApplies(detail, key)) group.items.push({ kind: "host", key });
      continue;
    }
    if (setting.row) {
      const row: PluginSetting[] = [setting];
      while (settings[index + 1]?.row === setting.row)
        row.push(settings[++index]);
      group.items.push({ kind: "row", name: setting.row, settings: row });
      continue;
    }
    group.items.push({ kind: "field", setting });
    const credential = detail.credentials.find(
      (slot) => slot.name === setting.credential,
    );
    if (setting.type === "server" && credential) {
      attached.add(credential.name);
      group.items.push({ kind: "credential", credential, server: setting });
    }
  }
  const loose = detail.credentials.filter((slot) => !attached.has(slot.name));
  if (loose.length > 0) {
    const group = groupFor(main, t("plugins.credentials.title"));
    for (const credential of loose)
      group.items.push({ kind: "credential", credential });
  }
  for (const key of hostKeys) {
    if (placed.has(key) || !hostApplies(detail, key)) continue;
    const group =
      key === "$origins"
        ? groupFor(advanced, undefined)
        : groupFor(main, t("plugins.settings.gatewayGroup"));
    group.items.push({ kind: "host", key });
  }
  return {
    main: main.filter((group) => group.items.length > 0),
    advanced: advanced.filter((group) => group.items.length > 0),
  };
}

/**
 * A plugin's settings: a form laid out from the settings its manifest
 * declares, with the host's own and the credentials beside its servers.
 * Everything saves together; the JSON of the configuration is a second view
 * of the same draft.
 */
export function PluginSettingsForm({
  detail,
  busy,
  advancedOpen,
  onAdvancedOpen,
  onSave,
  onClearCredential,
}: {
  detail: PluginDetail;
  busy: boolean;
  advancedOpen: boolean;
  onAdvancedOpen: (open: boolean) => void;
  onSave: (
    update: PluginSettingsUpdate,
    credentials: CredentialInput[],
  ) => Promise<boolean>;
  onClearCredential: (name: string) => Promise<boolean>;
}) {
  const t = useT();
  const [draft, setDraft] = useState(() => draftOf(detail));
  const [showProblems, setShowProblems] = useState(false);
  const [jsonOpen, setJsonOpen] = useState(false);
  const [replacing, setReplacing] = useState<Record<string, boolean>>({});
  const layout = useMemo(() => layoutOf(detail, t), [detail, t]);
  const config = configOf(detail, draft);
  const found = problems(detail, draft, t);
  const firstProblem = Object.values(found)[0];
  const servers = declared(detail).filter(
    (setting) => setting.type === "server",
  );
  const credentials = Object.entries(draft.secrets)
    .filter(([, secret]) => secret.trim() !== "")
    .map(([name, secret]) => ({ name, secret: secret.trim() }));
  const originsChanged =
    stableJSON(draft.origins) !== stableJSON(ownOrigins(detail));
  const initial = draftOf(detail).consent;
  const consentChanged = servers.some(
    (setting) =>
      (draft.consent[setting.key] ?? "") !== (initial[setting.key] ?? ""),
  );
  const configChanged = stableJSON(config) !== stableJSON(detail.config);
  const clientsChanged =
    stableJSON([...draft.excluded].sort()) !==
    stableJSON([...detail.excluded_clients].sort());
  const limitsChanged = Object.keys(changedLimits(detail, draft)).length > 0;
  const dirty =
    configChanged ||
    limitsChanged ||
    draft.failClosed !== (detail.fail_closed === true) ||
    draft.wait !== detail.turn_wait_ms ||
    draft.trusted !== detail.privacy_trusted ||
    clientsChanged ||
    draft.workspace !== detail.workspace_binding ||
    draft.refresh !== detail.auto_refresh ||
    draft.lookup !== detail.lookup_enabled ||
    originsChanged ||
    consentChanged ||
    credentials.length > 0;

  function setValue(key: string, value: Value) {
    setDraft((current) => ({
      ...current,
      values: { ...current.values, [key]: value },
    }));
  }

  async function save() {
    setShowProblems(true);
    if (firstProblem) return;
    const update: PluginSettingsUpdate = {};
    if (configChanged) update.config = config;
    if (draft.wait !== detail.turn_wait_ms) update.turn_wait_ms = draft.wait;
    if (draft.trusted !== detail.privacy_trusted)
      update.privacy_trusted = draft.trusted;
    if (clientsChanged) update.excluded_clients = draft.excluded;
    if (draft.workspace !== detail.workspace_binding)
      update.workspace_binding = draft.workspace;
    if (draft.refresh !== detail.auto_refresh)
      update.auto_refresh = draft.refresh;
    if (draft.lookup !== detail.lookup_enabled)
      update.lookup_enabled = draft.lookup;
    if (limitsChanged) update.hook_limits_ms = changedLimits(detail, draft);
    if (draft.failClosed !== (detail.fail_closed === true))
      update.fail_closed = draft.failClosed;
    if (originsChanged) update.network_origins = draft.origins;
    const consent = servers
      .map((setting) => {
        const address = typedValue(setting, draft.values[setting.key]);
        return typeof address === "string" &&
          plaintextOrigin(address) &&
          draft.consent[setting.key] === originOf(address)
          ? address
          : undefined;
      })
      .filter((address): address is string => address !== undefined);
    if (
      consent.length > 0 &&
      (configChanged || originsChanged || consentChanged)
    )
      update.plaintext_consent = consent;
    const inputs: CredentialInput[] = credentials.map(({ name, secret }) => {
      const attachedToServer = servers.some(
        (setting) => setting.credential === name,
      );
      return attachedToServer
        ? { name, secret }
        : { name, secret, origin: draft.credentialOrigins[name] };
    });
    await onSave(update, inputs);
  }

  function renderItem(item: Item, index: number) {
    switch (item.kind) {
      case "field":
        return (
          <FieldRow
            consent={
              draft.consent[item.setting.key] ===
              originOf(String(draft.values[item.setting.key] ?? ""))
            }
            invalid={showProblems && found[item.setting.key] !== undefined}
            key={item.setting.key}
            onConsent={(consent) =>
              setDraft((current) => ({
                ...current,
                consent: {
                  ...current.consent,
                  [item.setting.key]: consent
                    ? originOf(String(current.values[item.setting.key] ?? ""))
                    : "",
                },
              }))
            }
            onValue={(value) => setValue(item.setting.key, value)}
            setting={item.setting}
            value={draft.values[item.setting.key] ?? ""}
          />
        );
      case "row":
        // Switches side by side read as a choice of several: one select.
        if (item.settings.every((setting) => setting.type === "switch"))
          return (
            <SwitchesRow
              key={`row:${item.name}`}
              name={item.name}
              onValue={setValue}
              settings={item.settings}
              values={draft.values}
            />
          );
        return (
          <SettingRow
            help={item.settings.find((setting) => setting.help)?.help}
            helpLabel={item.name}
            key={`row:${item.name}`}
            label={item.name}
          >
            {item.settings.map((setting) => (
              <RowMember
                invalid={showProblems && found[setting.key] !== undefined}
                key={setting.key}
                onValue={(value) => setValue(setting.key, value)}
                setting={setting}
                showUnit={
                  setting.unit !== undefined &&
                  !item.settings.every((other) => other.unit === setting.unit)
                }
                value={draft.values[setting.key] ?? ""}
              />
            ))}
            {item.settings[0].unit &&
            item.settings.every(
              (other) => other.unit === item.settings[0].unit,
            ) ? (
              <span className="text-xs text-muted-foreground">
                {item.settings[0].unit}
              </span>
            ) : null}
          </SettingRow>
        );
      case "credential":
        return (
          <CredentialRow
            busy={busy}
            credential={item.credential}
            key={`credential:${item.credential.name}`}
            onClear={() => onClearCredential(item.credential.name)}
            onOrigin={(origin) =>
              setDraft((current) => ({
                ...current,
                credentialOrigins: {
                  ...current.credentialOrigins,
                  [item.credential.name]: origin,
                },
              }))
            }
            onReplacing={(on) => {
              setReplacing((current) => ({
                ...current,
                [item.credential.name]: on,
              }));
              if (!on)
                setDraft((current) => ({
                  ...current,
                  secrets: { ...current.secrets, [item.credential.name]: "" },
                }));
            }}
            onSecret={(secret) =>
              setDraft((current) => ({
                ...current,
                secrets: { ...current.secrets, [item.credential.name]: secret },
              }))
            }
            origin={draft.credentialOrigins[item.credential.name] ?? ""}
            origins={
              item.server
                ? undefined
                : draft.origins.map((entry) => entry.origin)
            }
            replacing={replacing[item.credential.name] ?? false}
            required={item.server?.required ?? false}
            secret={draft.secrets[item.credential.name] ?? ""}
          />
        );
      case "host":
        return (
          <HostRow
            detail={detail}
            draft={draft}
            hostKey={item.key}
            key={`host:${item.key}:${index}`}
            onDraft={setDraft}
          />
        );
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="plugin-settings">
      <div
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-4"
        data-tab-scroller
      >
        <div className="grid gap-6">
          {layout.main.map((group, index) => (
            <SettingGroup
              key={`${group.title ?? ""}:${index}`}
              title={group.title}
            >
              {group.items.map(renderItem)}
            </SettingGroup>
          ))}
          <section className="grid gap-3">
            <Button
              aria-expanded={advancedOpen}
              className="w-fit px-1.5 text-primary"
              onClick={() => onAdvancedOpen(!advancedOpen)}
              size="sm"
              type="button"
              variant="ghost"
            >
              <ChevronRight
                className={cn(
                  "transition-transform motion-reduce:transition-none",
                  advancedOpen && "rotate-90",
                )}
              />
              {t("plugins.settings.more")}
            </Button>
            {advancedOpen ? (
              <>
                {layout.advanced.map((group, index) => (
                  <SettingGroup
                    key={`${group.title ?? ""}:${index}`}
                    title={group.title}
                  >
                    {group.items.map(renderItem)}
                  </SettingGroup>
                ))}
                <div>
                  <Button
                    onClick={() => setJsonOpen(true)}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {t("plugins.settings.json")}
                  </Button>
                </div>
              </>
            ) : null}
          </section>
        </div>
      </div>
      <PanelFooter
        actions={
          <>
            <Button
              disabled={!dirty || busy}
              onClick={() => {
                setDraft(draftOf(detail));
                setReplacing({});
                setShowProblems(false);
              }}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("plugins.settings.revert")}
            </Button>
            <Button
              disabled={!dirty || busy}
              onClick={() => void save()}
              size="sm"
              type="button"
            >
              {t("plugins.settings.save")}
            </Button>
          </>
        }
        className="bg-transparent px-0 pt-3 pb-1"
      >
        {showProblems && firstProblem ? (
          <FormMessage tone="error">{firstProblem}</FormMessage>
        ) : dirty ? (
          <span className="text-xs text-text-secondary">
            {t("plugins.settings.unsaved")}
          </span>
        ) : null}
      </PanelFooter>
      {jsonOpen ? (
        <JsonDialog
          config={config}
          onApply={(next) => {
            setDraft((current) => ({
              ...current,
              config: next,
              values: valuesOf(detail, next),
            }));
            setJsonOpen(false);
          }}
          onClose={() => setJsonOpen(false)}
        />
      ) : null}
    </div>
  );
}

function FieldRow({
  setting,
  value,
  invalid,
  consent,
  onValue,
  onConsent,
}: {
  setting: PluginSetting;
  value: Value;
  invalid: boolean;
  consent: boolean;
  onValue: (value: Value) => void;
  onConsent: (consent: boolean) => void;
}) {
  const t = useT();
  const id = useId();
  const label = setting.label ?? setting.key;
  const address = typeof value === "string" ? value.trim() : "";
  return (
    <SettingRow
      help={setting.help}
      helpLabel={label}
      htmlFor={id}
      label={label}
      required={setting.required}
    >
      <Control
        id={id}
        invalid={invalid}
        onValue={onValue}
        setting={setting}
        value={value}
      />
      {setting.type === "number" && setting.unit ? (
        <span className="-ml-2 text-xs text-muted-foreground">
          {setting.unit}
        </span>
      ) : null}
      {setting.type === "server" && plaintextOrigin(address) ? (
        <span className="flex items-center gap-1">
          <Label className="flex items-center gap-1.5 text-xs font-normal">
            <Checkbox
              checked={consent}
              onCheckedChange={(checked) => onConsent(checked === true)}
            />
            {t("plugins.network.allowPlaintext")}
          </Label>
          <HelpPopover label={t("plugins.network.plaintextHelpLabel")}>
            <p>{t("plugins.network.plaintextHelp")}</p>
          </HelpPopover>
        </span>
      ) : null}
    </SettingRow>
  );
}

/**
 * A row of switches as one multi-select: each switch an option, the ones on
 * selected; the trigger names them.
 */
function SwitchesRow({
  name,
  settings,
  values,
  onValue,
}: {
  name: string;
  settings: PluginSetting[];
  values: Record<string, Value>;
  onValue: (key: string, value: Value) => void;
}) {
  const t = useT();
  return (
    <SettingRow
      help={settings.find((setting) => setting.help)?.help}
      helpLabel={name}
      label={name}
    >
      <MultiFilterSelect
        allLabel={t("plugins.settings.choices.all")}
        ariaLabel={name}
        className={settingControlWidth}
        clearLabel={t("plugins.settings.choices.clear")}
        emptyMessage=""
        everyLabel={t("plugins.settings.choices.all")}
        label=""
        noneLabel={t("plugins.settings.choices.none")}
        onChange={(next) => {
          for (const setting of settings)
            onValue(setting.key, next.includes(setting.key));
        }}
        options={settings.map((setting) => ({
          value: setting.key,
          label: setting.label ?? setting.key,
        }))}
        searchable={false}
        selectAllLabel={t("plugins.settings.choices.selectAll")}
        selectedCountLabel={(count) =>
          t("plugins.settings.choices.count", { count })
        }
        selectedText={(selected) =>
          selected
            .map((option) => option.label)
            .join(t("plugins.settings.choices.separator"))
        }
        value={settings
          .filter((setting) => values[setting.key] === true)
          .map((setting) => setting.key)}
      />
    </SettingRow>
  );
}

function RowMember({
  setting,
  value,
  invalid,
  showUnit,
  onValue,
}: {
  setting: PluginSetting;
  value: Value;
  invalid: boolean;
  showUnit: boolean;
  onValue: (value: Value) => void;
}) {
  const id = useId();
  return (
    <span className="inline-flex items-center gap-1.5">
      <Label className="text-xs font-normal text-text-secondary" htmlFor={id}>
        {setting.label}
      </Label>
      <Control
        id={id}
        invalid={invalid}
        onValue={onValue}
        setting={setting}
        value={value}
      />
      {showUnit ? (
        <span className="text-xs text-muted-foreground">{setting.unit}</span>
      ) : null}
    </span>
  );
}

/** The control of one declared field. */
function Control({
  id,
  setting,
  value,
  invalid,
  onValue,
}: {
  id: string;
  setting: PluginSetting;
  value: Value;
  invalid: boolean;
  onValue: (value: Value) => void;
}) {
  const t = useT();
  switch (setting.type) {
    case "switch":
      return (
        <Switch
          checked={value === true}
          id={id}
          onCheckedChange={(checked) => onValue(checked)}
        />
      );
    case "choice":
      return (
        <Select
          onValueChange={(next) => onValue(next)}
          value={typeof value === "string" && value !== "" ? value : undefined}
        >
          <SelectTrigger
            aria-invalid={invalid || undefined}
            className={settingControlWidth}
            id={id}
          >
            <SelectValue placeholder={setting.placeholder} />
          </SelectTrigger>
          <SelectContent>
            {(setting.options ?? []).map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      );
    case "number":
      return (
        <Input
          aria-invalid={invalid || undefined}
          className="h-8 w-24 text-right tabular-nums"
          id={id}
          inputMode={
            setting.step !== undefined && setting.step < 1
              ? "decimal"
              : "numeric"
          }
          max={setting.max}
          min={setting.min}
          onChange={(event) => onValue(event.target.value)}
          placeholder={setting.placeholder}
          step={setting.step ?? "any"}
          type="number"
          value={typeof value === "string" ? value : ""}
        />
      );
    default:
      return (
        <Input
          aria-invalid={invalid || undefined}
          className={cn(
            "h-8",
            settingControlWidth,
            setting.type === "server" && "font-mono",
          )}
          id={id}
          onChange={(event) => onValue(event.target.value)}
          placeholder={
            setting.placeholder ??
            (setting.type === "server"
              ? t("plugins.network.originLabel")
              : undefined)
          }
          spellCheck={false}
          value={typeof value === "string" ? value : ""}
        />
      );
  }
}

function CredentialRow({
  credential,
  required,
  busy,
  replacing,
  secret,
  origins,
  origin,
  onReplacing,
  onSecret,
  onOrigin,
  onClear,
}: {
  credential: PluginCredentialStatus;
  required: boolean;
  busy: boolean;
  replacing: boolean;
  secret: string;
  /** The origins to bind to, for a credential no server sends. */
  origins?: string[];
  origin: string;
  onReplacing: (on: boolean) => void;
  onSecret: (secret: string) => void;
  onOrigin: (origin: string) => void;
  onClear: () => Promise<boolean>;
}) {
  const t = useT();
  const id = useId();
  const editing = !credential.configured || replacing;
  return (
    <SettingRow
      help={<p>{t("plugins.credentials.help")}</p>}
      helpLabel={t("plugins.credentials.helpLabel")}
      htmlFor={editing ? id : undefined}
      label={credential.label}
      required={required}
    >
      {editing ? (
        <>
          {/* The secret's field wraps the input with its reveal button. */}
          <span className={settingControlWidth}>
            <Input
              autoComplete="off"
              className="h-8"
              id={id}
              onChange={(event) => onSecret(event.target.value)}
              placeholder={t("plugins.credentials.secret")}
              type="password"
              value={secret}
            />
          </span>
          {origins ? (
            <Select
              disabled={origins.length === 0}
              onValueChange={onOrigin}
              value={origin || undefined}
            >
              <SelectTrigger
                aria-label={t("plugins.credentials.origin")}
                className="w-56"
              >
                <SelectValue placeholder={t("plugins.credentials.origin")} />
              </SelectTrigger>
              <SelectContent>
                {origins.map((item) => (
                  <SelectItem key={item} value={item}>
                    {item}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : null}
          {credential.configured ? (
            <Button
              onClick={() => onReplacing(false)}
              size="sm"
              type="button"
              variant="ghost"
            >
              {t("plugins.settings.keep")}
            </Button>
          ) : null}
        </>
      ) : (
        <>
          <StatusBadge tone="positive">
            {credential.hint
              ? t("plugins.credentials.configured", { hint: credential.hint })
              : t("plugins.credentials.configuredNoHint")}
          </StatusBadge>
          <span className="flex items-center gap-1">
            <Button
              onClick={() => onReplacing(true)}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("plugins.settings.replace")}
            </Button>
            <Button
              disabled={busy}
              onClick={() => void onClear()}
              size="sm"
              type="button"
              variant="ghost"
            >
              {t("plugins.credentials.clear")}
            </Button>
          </span>
        </>
      )}
    </SettingRow>
  );
}

function HostRow({
  detail,
  draft,
  hostKey,
  onDraft,
}: {
  detail: PluginDetail;
  draft: Draft;
  hostKey: HostSettingKey;
  onDraft: (change: (current: Draft) => Draft) => void;
}) {
  const t = useT();
  const id = useId();
  switch (hostKey) {
    case "$wait":
      return (
        <SettingRow
          help={<p>{t("plugins.wait.help")}</p>}
          helpLabel={t("plugins.wait.helpLabel")}
          htmlFor={id}
          label={t("plugins.wait.label")}
        >
          <Select
            onValueChange={(value) =>
              onDraft((current) => ({ ...current, wait: Number(value) }))
            }
            value={String(draft.wait)}
          >
            <SelectTrigger className={settingControlWidth} id={id}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PLUGIN_TURN_WAITS.map((waitMs) => (
                <SelectItem key={waitMs} value={String(waitMs)}>
                  {t("plugins.wait.seconds", { value: waitMs / 1000 })}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
      );
    case "$trusted":
      return (
        <SettingRow
          help={<p>{t("plugins.network.trustedHelp")}</p>}
          helpLabel={t("plugins.network.trustedHelpLabel")}
          htmlFor={id}
          label={t("plugins.network.trusted")}
        >
          <Switch
            checked={draft.trusted}
            id={id}
            onCheckedChange={(checked) =>
              onDraft((current) => ({ ...current, trusted: checked }))
            }
          />
        </SettingRow>
      );
    case "$origins":
      return (
        <OriginsRow
          draft={draft}
          onDraft={onDraft}
          purpose={detail.network_purpose}
        />
      );
    case "$clients":
      return <ClientsRow draft={draft} onDraft={onDraft} />;
    case "$workspace":
      return (
        <SettingRow
          help={<p>{t("plugins.workspace.help")}</p>}
          helpLabel={t("plugins.workspace.helpLabel")}
          htmlFor={id}
          label={t("plugins.workspace.label")}
        >
          <Switch
            checked={draft.workspace}
            id={id}
            onCheckedChange={(checked) =>
              onDraft((current) => ({ ...current, workspace: checked }))
            }
          />
        </SettingRow>
      );
    case "$refresh":
      return <RefreshRow draft={draft} onDraft={onDraft} />;
    case "$lookup":
      return (
        <SettingRow
          help={
            <>
              <p>{t("plugins.lookup.help")}</p>
              <ul className="mt-2 grid gap-1">
                {detail.tools.map((tool) => (
                  <li key={tool.name}>
                    <span className="font-mono">{tool.name}</span>
                    {tool.writes ? t("plugins.lookup.reportMark") : ""}
                    {t("plugins.lookup.toolSeparator")}
                    {tool.description}
                  </li>
                ))}
              </ul>
            </>
          }
          helpLabel={t("plugins.lookup.helpLabel")}
          htmlFor={id}
          label={t("plugins.lookup.label")}
        >
          <Switch
            checked={draft.lookup}
            id={id}
            onCheckedChange={(checked) =>
              onDraft((current) => ({ ...current, lookup: checked }))
            }
          />
        </SettingRow>
      );
    case "$limits":
      return <LimitRows detail={detail} draft={draft} onDraft={onDraft} />;
    case "$failClosed":
      return (
        <SettingRow
          help={<p>{t("plugins.limits.failClosedHelp")}</p>}
          helpLabel={t("plugins.limits.failClosedHelpLabel")}
          htmlFor={id}
          label={t("plugins.limits.failClosed")}
        >
          <Switch
            checked={draft.failClosed}
            id={id}
            onCheckedChange={(checked) =>
              onDraft((current) => ({ ...current, failClosed: checked }))
            }
          />
        </SettingRow>
      );
  }
}

/**
 * The time limits of the extension points the plugin serves, one per row:
 * empty follows what the plugin asks for, which shows as the placeholder.
 */
function LimitRows({
  detail,
  draft,
  onDraft,
}: {
  detail: PluginDetail;
  draft: Draft;
  onDraft: (change: (current: Draft) => Draft) => void;
}) {
  const t = useT();
  const id = useId();
  return (
    <SettingRow
      help={<p>{t("plugins.limits.help")}</p>}
      helpLabel={t("plugins.limits.helpLabel")}
      htmlFor={`${id}-0`}
      label={t("plugins.limits.label")}
    >
      <div className="grid gap-1.5" data-testid="plugin-limits">
        {detail.limits.map((limit, index) => {
          const typed = draft.limits[limit.hook] ?? "";
          return (
            <div className="flex items-center gap-2" key={limit.hook}>
              <Label
                className="w-28 shrink-0 text-xs font-normal text-muted-foreground"
                htmlFor={`${id}-${index}`}
              >
                {hookLabel(limit.hook, t)}
              </Label>
              <Input
                aria-invalid={!validLimit(limit, typed) || undefined}
                className="h-7 w-24 text-right tabular-nums"
                id={`${id}-${index}`}
                inputMode="numeric"
                onChange={(event) => {
                  const value = event.target.value;
                  onDraft((current) => ({
                    ...current,
                    limits: { ...current.limits, [limit.hook]: value },
                  }));
                }}
                placeholder={String(limit.plugin_ms ?? limit.default_ms)}
                value={typed}
              />
              <span className="text-xs text-muted-foreground">
                {t("plugins.limits.ms")}
              </span>
            </div>
          );
        })}
      </div>
    </SettingRow>
  );
}

/**
 * The clients the plugin takes part for: every one by default. The list
 * holds the ones it is on for; the plugin saves those it is off for, so a
 * client AstrLink learns later starts on.
 */
function ClientsRow({
  draft,
  onDraft,
}: {
  draft: Draft;
  onDraft: (change: (current: Draft) => Draft) => void;
}) {
  const t = useT();
  const options = CLIENT_TYPES.map((client) => ({
    value: client,
    label:
      client === "unknown"
        ? t("plugins.clients.other")
        : clientTypeName(client),
  }));
  const enabled = CLIENT_TYPES.filter(
    (client) => !draft.excluded.includes(client),
  );
  return (
    <SettingRow
      help={<p>{t("plugins.clients.help")}</p>}
      helpLabel={t("plugins.clients.helpLabel")}
      label={t("plugins.clients.label")}
    >
      <MultiFilterSelect
        allLabel={t("plugins.clients.all")}
        ariaLabel={t("plugins.clients.label")}
        className={settingControlWidth}
        clearLabel={t("plugins.clients.clear")}
        emptyMessage={t("plugins.clients.empty")}
        everyLabel={t("plugins.clients.all")}
        label=""
        noneLabel={t("plugins.clients.none")}
        onChange={(value) =>
          onDraft((current) => ({
            ...current,
            excluded: CLIENT_TYPES.filter((client) => !value.includes(client)),
          }))
        }
        options={options}
        searchPlaceholder={t("plugins.clients.search")}
        selectAllLabel={t("plugins.clients.selectAll")}
        selectedCountLabel={(count) => t("plugins.clients.count", { count })}
        value={enabled}
      />
    </SettingRow>
  );
}

/** Automatic refresh rewrites a session's prompt cache whenever it swaps a
 * block, so turning it on asks first. */
function RefreshRow({
  draft,
  onDraft,
}: {
  draft: Draft;
  onDraft: (change: (current: Draft) => Draft) => void;
}) {
  const t = useT();
  const id = useId();
  const [asking, setAsking] = useState(false);
  return (
    <SettingRow
      help={<p>{t("plugins.refresh.help")}</p>}
      helpLabel={t("plugins.refresh.helpLabel")}
      htmlFor={id}
      label={t("plugins.refresh.label")}
    >
      <Switch
        checked={draft.refresh}
        id={id}
        onCheckedChange={(checked) => {
          if (checked) setAsking(true);
          else onDraft((current) => ({ ...current, refresh: false }));
        }}
      />
      <ConfirmDialog
        confirmLabel={t("plugins.refresh.confirm")}
        description={<p>{t("plugins.refresh.confirmBody")}</p>}
        onCancel={() => setAsking(false)}
        onConfirm={() => {
          setAsking(false);
          onDraft((current) => ({ ...current, refresh: true }));
        }}
        open={asking}
        title={t("plugins.refresh.confirmTitle")}
        tone="warning"
      />
    </SettingRow>
  );
}

function OriginsRow({
  draft,
  purpose,
  onDraft,
}: {
  draft: Draft;
  purpose?: string;
  onDraft: (change: (current: Draft) => Draft) => void;
}) {
  const t = useT();
  const [adding, setAdding] = useState("");
  const [addError, setAddError] = useState<string | null>(null);

  function add() {
    const origin = adding.trim();
    if (!validOrigin(origin)) {
      setAddError(t("plugins.network.invalidOrigin"));
      return;
    }
    setAddError(null);
    onDraft((current) => ({
      ...current,
      origins: [...current.origins, { origin, allow_plaintext: false }],
    }));
    setAdding("");
  }

  return (
    <SettingRow
      help={
        <>
          <p>{t("plugins.network.originsHelp")}</p>
          {purpose ? (
            <p>{t("plugins.enableDialog.purpose", { purpose })}</p>
          ) : null}
        </>
      }
      helpLabel={t("plugins.network.originsHelpLabel")}
      label={t("plugins.settings.origins")}
    >
      <div className="grid w-full min-w-0 gap-1.5">
        {draft.origins.map((entry, index) => (
          <div
            className="flex min-w-0 flex-wrap items-center gap-2"
            data-testid="plugin-origin"
            key={`${entry.origin}:${index}`}
          >
            <code
              className="min-w-0 flex-1 truncate font-mono text-xs"
              title={entry.origin}
            >
              {entry.origin}
            </code>
            {plaintextOrigin(entry.origin) ? (
              <Label className="flex items-center gap-1.5 text-xs font-normal">
                <Checkbox
                  checked={entry.allow_plaintext}
                  onCheckedChange={(checked) =>
                    onDraft((current) => ({
                      ...current,
                      origins: current.origins.map((item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, allow_plaintext: checked === true }
                          : item,
                      ),
                    }))
                  }
                />
                {t("plugins.network.allowPlaintext")}
              </Label>
            ) : null}
            <IconButton
              label={t("plugins.network.remove", { origin: entry.origin })}
              onClick={() =>
                onDraft((current) => ({
                  ...current,
                  origins: current.origins.filter(
                    (_, itemIndex) => itemIndex !== index,
                  ),
                }))
              }
              size="icon-sm"
              type="button"
              variant="ghost"
            >
              <Trash2 />
            </IconButton>
          </div>
        ))}
        <div className="flex min-w-0 gap-2">
          <Input
            aria-label={t("plugins.network.originLabel")}
            className="h-8 min-w-0 flex-1 font-mono"
            onChange={(event) => setAdding(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                add();
              }
            }}
            placeholder="https://memory.example.com:8443"
            value={adding}
          />
          <Button
            disabled={adding.trim() === ""}
            onClick={add}
            size="sm"
            type="button"
            variant="outline"
          >
            <Plus />
            {t("plugins.network.add")}
          </Button>
        </div>
        {addError ? <FormMessage tone="error">{addError}</FormMessage> : null}
      </div>
    </SettingRow>
  );
}

/** The configuration as JSON: applying it puts it back into the form. */
function JsonDialog({
  config,
  onApply,
  onClose,
}: {
  config: Record<string, unknown>;
  onApply: (config: Record<string, unknown>) => void;
  onClose: () => void;
}) {
  const t = useT();
  const [text, setText] = useState(() => JSON.stringify(config, null, 2));
  const [error, setError] = useState<string | null>(null);
  const bytes = configBytes(text);

  function apply() {
    let value: unknown;
    try {
      value = JSON.parse(text);
    } catch {
      setError(t("plugins.config.invalid"));
      return;
    }
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      setError(t("plugins.config.invalid"));
      return;
    }
    if (bytes > MAX_PLUGIN_CONFIG_BYTES) {
      setError(t("plugins.config.tooLarge"));
      return;
    }
    onApply(value as Record<string, unknown>);
  }

  return (
    <Dialog onOpenChange={(open) => (open ? undefined : onClose())} open>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("plugins.settings.jsonTitle")}</DialogTitle>
          <DialogDescription>{t("plugins.config.help")}</DialogDescription>
        </DialogHeader>
        <Textarea
          aria-label={t("plugins.settings.jsonTitle")}
          className="h-80 resize-none font-mono text-xs"
          onChange={(event) => setText(event.target.value)}
          spellCheck={false}
          value={text}
        />
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
        <DialogFooter className="items-center sm:justify-between">
          <span
            className={cn(
              "text-micro tabular-nums text-muted-foreground",
              bytes > MAX_PLUGIN_CONFIG_BYTES && "text-danger-foreground",
            )}
          >
            {t("plugins.config.size", { size: (bytes / 1024).toFixed(1) })}
          </span>
          <span className="flex gap-2">
            <Button onClick={onClose} size="sm" type="button" variant="outline">
              {t("common.cancel")}
            </Button>
            <Button onClick={apply} size="sm" type="button">
              {t("plugins.settings.apply")}
            </Button>
          </span>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
