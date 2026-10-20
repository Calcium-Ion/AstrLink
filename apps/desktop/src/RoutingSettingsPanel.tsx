import { BuiltinToolsEditor } from "./components/BuiltinToolsEditor";
import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  getRoutingSettings,
  updateRoutingSettings,
  getClassifierSettings,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { ChannelStickinessEditor } from "./components/ChannelStickinessEditor";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import {
  FailoverToggle,
  RecoveryOrderControls,
} from "./components/FailoverEditor";
import { FormMessage } from "./components/FormMessage";
import { ImageCompressionEditor } from "./components/ImageCompressionEditor";
import { ModelRedirectEditor } from "./components/ModelRedirectEditor";
import { Panel, PanelHeader } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import {
  Boxes,
  Fingerprint,
  SquarePen,
  type AnimatedIcon,
} from "./components/icons";
import { defaultBuiltinTools } from "./builtin-tools-model";
import {
  defaultImageCompression,
  identityLearningKeys,
  identitySettingKeys,
  identityVersionKeys,
  parseRoutingSettings,
  subscriptionProtectionKeys,
  thinkingPromptKeys,
  type RoutingSettings,
} from "./failure-policy-model";
import type { ClassifierDimension } from "./classifier-settings-model";
import { currentLocale, useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import type { RoutableService } from "./service-model";
import { ThinkingPromptSettings } from "./ThinkingPromptSettings";
import { UpstreamIdentitySettings } from "./UpstreamIdentitySettings";
import { useGatewayResponseTimeout } from "./use-gateway-response-timeout";

export const routingAutosaveDelay = 500;

const routingSettingKeys = [
  "default_failure_policy",
  "allow_unmatched_failover",
  "strategy",
  "max_attempts",
  "channel_stickiness",
  "model_redirects",
  "builtin_tools",
  "image_compression",
  ...identitySettingKeys,
  ...subscriptionProtectionKeys,
  ...identityLearningKeys,
  ...identityVersionKeys,
  ...thinkingPromptKeys,
] as const;
type RoutingSettingKey = (typeof routingSettingKeys)[number];

const routingTabs: ReadonlyArray<{
  value: string;
  icon?: AnimatedIcon;
  keys: readonly RoutingSettingKey[];
}> = [
  {
    value: "rewrite",
    icon: SquarePen,
    keys: ["image_compression", ...thinkingPromptKeys],
  },
  {
    value: "redirects",
    icon: Boxes,
    keys: ["model_redirects", "builtin_tools"],
  },
  {
    value: "recovery",
    keys: [
      "default_failure_policy",
      "allow_unmatched_failover",
      "strategy",
      "max_attempts",
    ],
  },
  { value: "rules", keys: ["default_failure_policy"] },
  { value: "session", keys: ["channel_stickiness"] },
  {
    value: "identity",
    icon: Fingerprint,
    keys: [
      ...identitySettingKeys,
      ...subscriptionProtectionKeys,
      ...identityLearningKeys,
      ...identityVersionKeys,
    ],
  },
];

const same = (left: unknown, right: unknown) =>
  JSON.stringify(left) === JSON.stringify(right);

/**
 * What an autosave sends: each changed setting, and each built-in tool on its
 * own, that is valid on top of the saved document. An incomplete one stays
 * pending in the draft without holding back the rest.
 */
function planAutosave(
  original: RoutingSettings,
  draft: RoutingSettings,
  dimensions: ClassifierDimension[] | null,
) {
  type Unit = (document: RoutingSettings) => RoutingSettings;
  let units: { key: RoutingSettingKey; apply: Unit }[] = [];
  for (const key of routingSettingKeys) {
    if (same(draft[key], original[key])) continue;
    const tools = draft.builtin_tools;
    const kinds =
      key === "builtin_tools" && tools
        ? (["web_search", "image_generation"] as const).filter(
            (kind) => !same(tools[kind], original.builtin_tools?.[kind]),
          )
        : [];
    if (tools && kinds.length)
      for (const kind of kinds)
        units.push({
          key,
          apply: (document) => ({
            ...document,
            builtin_tools: {
              ...(document.builtin_tools ?? defaultBuiltinTools()),
              [kind]: tools[kind],
            },
          }),
        });
    else
      units.push({
        key,
        apply: (document) => ({ ...document, [key]: draft[key] }),
      });
  }
  let sent = original;
  // Settings validated together, such as the adaptive range, may only fit
  // once another has gone in, so passes repeat while one is accepted.
  for (let accepted = true; accepted; ) {
    accepted = false;
    units = units.filter(({ apply }) => {
      const candidate = apply(sent);
      try {
        parseRoutingSettings(candidate, dimensions);
      } catch {
        return true;
      }
      sent = candidate;
      accepted = true;
      return false;
    });
  }
  const patch: Partial<RoutingSettings> = {};
  for (const key of routingSettingKeys)
    if (!same(sent[key], original[key]))
      Object.assign(patch, { [key]: sent[key] });
  return { patch, sent, pending: units.map(({ key }) => key) };
}

// A document without the key has no redirects; compare and edit it as [].
function withRedirects(settings: RoutingSettings): RoutingSettings {
  return { ...settings, model_redirects: settings.model_redirects ?? [] };
}

export function RoutingSettingsPanel({
  ready,
  services,
  titleId,
  onDirtyChange,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  titleId?: string;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const gatewayTimeout = useGatewayResponseTimeout();
  const [tab, setTab] = useState("rewrite");
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  // null until known, or when the classifier settings cannot be read; rules
  // naming a dimension are then left to Core.
  const [dimensions, setDimensions] = useState<ClassifierDimension[] | null>(
    null,
  );
  const [draft, setDraft] = useState<RoutingSettings | null>(() =>
    settings ? withRedirects(settings) : null,
  );
  const [baseline, setBaseline] = useState(() =>
    settings ? JSON.stringify(withRedirects(settings)) : "",
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [editingRedirect, setEditingRedirect] = useState(false);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const [reload, setReload] = useState(0);
  const [showRedirectIssues, setShowRedirectIssues] = useState(false);
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
  const plan = useMemo(
    () =>
      draft && dirty
        ? planAutosave(
            JSON.parse(baseline) as RoutingSettings,
            draft,
            dimensions,
          )
        : undefined,
    [draft, dirty, baseline, dimensions],
  );
  const pending = plan?.pending ?? [];
  const incompleteTabs = routingTabs.filter(
    ({ value, keys }) =>
      value !== tab && keys.some((key) => pending.includes(key)),
  );
  const modelOptions = useMemo(
    () =>
      [
        ...new Set(
          services
            .filter((service) => service.enabled)
            .flatMap((service) => service.models),
        ),
      ].sort(),
    [services],
  );
  const mutationVersion = useRef(0);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!ready) return;
    let active = true;
    const version = mutationVersion.current;
    void Promise.all([
      getRoutingSettings(),
      getClassifierSettings().catch(() => null),
    ])
      .then(([settings, classifierSettings]) => {
        if (!active || mutationVersion.current !== version) return;
        setSettings(settings);
        setDimensions(
          classifierSettings === null
            ? null
            : (classifierSettings.dimensions ?? []),
        );
        // Reconnection and retry must never replace an unsaved draft.
        if (!dirtyRef.current) {
          setDraft(withRedirects(settings));
          setBaseline(JSON.stringify(withRedirects(settings)));
        }
        setLoadError(null);
      })
      .catch((error) => {
        if (active && mutationVersion.current === version)
          setLoadError(
            error instanceof Error ? error.message : t("failure.loadFailed"),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t, setSettings]);

  const changeDraft = (next: RoutingSettings) => {
    setError(null);
    setShowRedirectIssues(false);
    setDraft(next);
  };

  useEffect(() => {
    if (!draft || !plan || !ready || saving || error || editingRedirect) return;
    const submitted = draft;
    const { patch, sent } = plan;
    const timer = setTimeout(() => {
      if (plan.pending.includes("model_redirects")) setShowRedirectIssues(true);
      if (Object.keys(patch).length === 0) return;
      mutationVersion.current += 1;
      setSaving(true);
      void updateRoutingSettings(patch)
        .then((result) => {
          const saved = withRedirects(result);
          setSettings(saved);
          setBaseline(JSON.stringify(saved));
          // A response acknowledges only what it sent. Keep the rest of the
          // draft: pending settings and edits made while it was in flight,
          // including a change back to the old value.
          setDraft((current) => {
            if (!current) return saved;
            const merged = { ...saved };
            for (const key of routingSettingKeys) {
              if (!same(current[key], sent[key]))
                Object.assign(merged, { [key]: current[key] });
            }
            return merged;
          });
          setSavedOnce(true);
        })
        .catch((error: unknown) => {
          // Newer edits get their own attempt; an unchanged failed draft pauses
          // until the user edits it or retries, rather than looping requests.
          if (draftRef.current === submitted)
            setError(controlErrorMessage(error, t("routing.saveFailed")));
        })
        .finally(() => setSaving(false));
    }, routingAutosaveDelay);
    return () => clearTimeout(timer);
  }, [draft, plan, ready, saving, error, editingRedirect, setSettings, t]);

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="routing-defaults-panel"
    >
      <PageHeader
        variant="compact"
        className="mb-0"
        title={t("routing.providerTitle")}
        titleId={titleId}
        actions={
          <span
            role="status"
            className="shrink-0 text-xs text-muted-foreground"
            aria-live="polite"
          >
            {error
              ? t("routing.notSaved")
              : !ready && dirty
                ? t("routing.waitingConnection")
                : editingRedirect && dirty
                  ? t("routing.editing")
                  : saving
                    ? t("common.saving")
                    : pending.length
                      ? t("routing.incomplete")
                      : dirty
                        ? t("common.saving")
                        : savedOnce
                          ? t("routing.autoSaved")
                          : t("routing.autosave")}
          </span>
        }
      />
      {loadError ? (
        <FormMessage tone="error">
          {loadError}
          <Button
            type="button"
            variant="ghost"
            onClick={() => setReload((value) => value + 1)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {error ? (
        <FormMessage tone="error">
          {t("routing.autosaveFailed")} {error}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={!ready}
            onClick={() => setError(null)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {incompleteTabs.length ? (
        <FormMessage tone="warning">
          {t("routing.autosaveInvalid", {
            tabs: new Intl.ListFormat(currentLocale()).format(
              incompleteTabs.map(({ value }) =>
                t("routing.tabMention", { tab: t(`routing.tabs.${value}`) }),
              ),
            ),
          })}
        </FormMessage>
      ) : null}
      <Tabs
        value={tab}
        onValueChange={setTab}
        className="min-h-0 min-w-0 flex-1 gap-3 overflow-y-clip"
      >
        <TabsList
          scrollable
          aria-label={t("routing.providerTitle")}
          className="min-w-0 shrink-0"
        >
          {routingTabs.map(({ value, icon: Icon }) => (
            <TabsTrigger
              key={value}
              value={value}
              onClick={() => setTab(value)}
            >
              {Icon ? <Icon aria-hidden="true" /> : null}
              {t(`routing.tabs.${value}`)}
            </TabsTrigger>
          ))}
        </TabsList>
        {draft ? (
          <>
            <TabsContent
              value="rewrite"
              className="gutter-scroller min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready} className="grid min-w-0 gap-3">
                <ImageCompressionEditor
                  value={draft.image_compression ?? defaultImageCompression()}
                  disabled={!ready}
                  onChange={(image_compression) =>
                    changeDraft({ ...draft, image_compression })
                  }
                />
                <ThinkingPromptSettings value={draft} onChange={changeDraft} />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="redirects"
              className="gutter-scroller flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset
                disabled={!ready}
                className="flex max-h-96 min-h-0 min-w-0 shrink-0 flex-col"
              >
                <ModelRedirectEditor
                  value={draft.model_redirects ?? []}
                  modelOptions={modelOptions}
                  disabled={!ready}
                  showAllIssues={showRedirectIssues}
                  onEditingChange={setEditingRedirect}
                  onChange={(model_redirects) =>
                    changeDraft({ ...draft, model_redirects })
                  }
                />
              </fieldset>
              <BuiltinToolsEditor
                value={draft.builtin_tools}
                services={services}
                disabled={!ready}
                onChange={(builtin_tools) =>
                  changeDraft({ ...draft, builtin_tools })
                }
              />
            </TabsContent>
            <TabsContent
              value="recovery"
              className="gutter-scroller min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready} className="grid min-w-0 gap-3">
                <Panel>
                  <PanelHeader>
                    <h2 className="text-sm font-semibold">
                      {t("routing.orderTitle")}
                    </h2>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t("routing.orderHint")}
                    </p>
                  </PanelHeader>
                  <div className="grid gap-4 p-4">
                    <div className="grid gap-2">
                      <FailoverToggle
                        checked={draft.allow_unmatched_failover}
                        label={t("failure.globalSwitch")}
                        onCheckedChange={(allow_unmatched_failover) =>
                          changeDraft({ ...draft, allow_unmatched_failover })
                        }
                      />
                      <p className="text-xs text-muted-foreground">
                        {t("failure.globalOffHint")}
                      </p>
                    </div>
                    <RecoveryOrderControls
                      value={draft}
                      onChange={(order) => changeDraft({ ...draft, ...order })}
                    />
                  </div>
                </Panel>
                {(["retry", "repair"] as const).map((section) => (
                  <FailurePolicyEditor
                    key={section}
                    section={section}
                    title={t(`routing.${section}Title`)}
                    hint={
                      section === "repair"
                        ? t("failure.repairHint")
                        : draft.strategy === "failover_only"
                          ? t("failure.onceHint")
                          : t("failure.allServicesHint")
                    }
                    headingLevel={2}
                    value={draft.default_failure_policy}
                    gatewayTimeoutSeconds={gatewayTimeout}
                    onChange={(default_failure_policy) =>
                      changeDraft({ ...draft, default_failure_policy })
                    }
                  />
                ))}
              </fieldset>
            </TabsContent>
            <TabsContent
              value="rules"
              className="gutter-scroller flex min-h-0 flex-1 flex-col overflow-hidden pb-1"
              data-tab-scroller
            >
              <fieldset
                disabled={!ready}
                className="flex min-h-0 min-w-0 flex-1 flex-col"
              >
                <FailurePolicyEditor
                  section="rules"
                  title={t("routing.rulesTitle")}
                  hint={t("routing.rulesHint")}
                  headingLevel={2}
                  value={draft.default_failure_policy}
                  gatewayTimeoutSeconds={gatewayTimeout}
                  onChange={(default_failure_policy) =>
                    changeDraft({ ...draft, default_failure_policy })
                  }
                />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="session"
              className="gutter-scroller min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready} className="min-w-0">
                <ChannelStickinessEditor
                  value={
                    draft.channel_stickiness ?? {
                      enabled: true,
                      ttl_seconds: 3600,
                    }
                  }
                  onChange={(channel_stickiness) =>
                    changeDraft({ ...draft, channel_stickiness })
                  }
                />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="identity"
              className="gutter-scroller min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready} className="grid min-w-0 gap-3">
                <UpstreamIdentitySettings
                  value={draft}
                  onChange={changeDraft}
                />
              </fieldset>
            </TabsContent>
          </>
        ) : (
          <TabsContent
            value={tab}
            className="gutter-scroller min-h-0 flex-1 overflow-y-auto"
            data-tab-scroller
          >
            <p className="text-xs text-muted-foreground">
              {ready ? t("common.loading") : t("services.gatewayNotReady")}
            </p>
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
