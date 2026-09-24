import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { useEffect, useMemo, useRef, useState } from "react";
import { getRoutingSettings, updateRoutingSettings } from "./bridge";
import { ChannelStickinessEditor } from "./components/ChannelStickinessEditor";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import {
  FailoverToggle,
  RecoveryOrderControls,
} from "./components/FailoverEditor";
import { FormMessage } from "./components/FormMessage";
import { ModelRedirectEditor } from "./components/ModelRedirectEditor";
import { Panel, PanelHeader } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import {
  identitySettingKeys,
  modelRedirectIssues,
  parseRoutingSettings,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";
import { notify } from "./notify";
import type { RoutableService } from "./service-model";
import { UpstreamIdentitySettings } from "./UpstreamIdentitySettings";

const routingTabs = ["redirects", "recovery", "rules", "session", "identity"];

// A document without the key has no redirects; compare and edit it as [].
function withRedirects(settings: RoutingSettings): RoutingSettings {
  return { ...settings, model_redirects: settings.model_redirects ?? [] };
}

export function RoutingSettingsPanel({
  ready,
  services,
  onDirtyChange,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [tab, setTab] = useState("redirects");
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
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
  const [reload, setReload] = useState(0);
  const [showRedirectIssues, setShowRedirectIssues] = useState(false);
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
  const redirectsInvalid =
    draft !== null &&
    modelRedirectIssues(draft.model_redirects ?? []).some(Boolean);
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
    void getRoutingSettings()
      .then((settings) => {
        if (!active || mutationVersion.current !== version) return;
        setSettings(settings);
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

  const save = async () => {
    if (!draft || !ready || saving) return;
    if (redirectsInvalid) {
      setShowRedirectIssues(true);
      setTab("redirects");
      return;
    }
    try {
      parseRoutingSettings(draft);
    } catch {
      setError(t("failure.invalid"));
      return;
    }
    mutationVersion.current += 1;
    setSaving(true);
    setError(null);
    try {
      const original = JSON.parse(baseline) as RoutingSettings;
      const patch: Partial<RoutingSettings> = {};
      for (const key of [
        "default_failure_policy",
        "allow_unmatched_failover",
        "strategy",
        "max_attempts",
        "channel_stickiness",
        "model_redirects",
        ...identitySettingKeys,
      ] as const) {
        const next = withRedirects(draft)[key];
        if (
          JSON.stringify(next) !== JSON.stringify(withRedirects(original)[key])
        )
          Object.assign(patch, { [key]: next });
      }
      const saved = await updateRoutingSettings(patch);
      setSettings(saved);
      setDraft(withRedirects(saved));
      setBaseline(JSON.stringify(withRedirects(saved)));
      notify.success(t("failure.saved"));
    } catch (error) {
      setError(
        error instanceof Error ? error.message : t("routing.saveFailed"),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden"
      data-testid="routing-defaults-panel"
    >
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
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {showRedirectIssues && redirectsInvalid ? (
        <FormMessage tone="error">{t("routing.redirectsInvalid")}</FormMessage>
      ) : null}
      <Tabs
        value={tab}
        onValueChange={setTab}
        className="min-h-0 min-w-0 flex-1 gap-3 overflow-hidden"
      >
        <div className="flex min-w-0 shrink-0 items-center justify-between gap-3">
          <TabsList
            scrollable
            aria-label={t("nav.routing")}
            className="min-w-0"
          >
            {routingTabs.map((value) => (
              <TabsTrigger
                key={value}
                value={value}
                onClick={() => setTab(value)}
              >
                {t(`routing.tabs.${value}`)}
              </TabsTrigger>
            ))}
          </TabsList>
          <Button
            type="button"
            size="sm"
            className="shrink-0"
            disabled={!dirty || !ready || saving}
            onClick={() => void save()}
          >
            {saving ? t("common.saving") : t("failure.save")}
          </Button>
        </div>
        {draft ? (
          <>
            <TabsContent
              value="redirects"
              className="flex min-h-0 flex-1 flex-col overflow-hidden pb-1"
              data-tab-scroller
            >
              <fieldset
                disabled={!ready || saving}
                className="flex min-h-0 min-w-0 flex-1 flex-col"
              >
                <ModelRedirectEditor
                  value={draft.model_redirects ?? []}
                  modelOptions={modelOptions}
                  disabled={!ready || saving}
                  showAllIssues={showRedirectIssues}
                  onChange={(model_redirects) => {
                    setDraft({ ...draft, model_redirects });
                    if (!modelRedirectIssues(model_redirects).some(Boolean))
                      setShowRedirectIssues(false);
                  }}
                />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="recovery"
              className="min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset
                disabled={!ready || saving}
                className="grid min-w-0 gap-3"
              >
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
                          setDraft({ ...draft, allow_unmatched_failover })
                        }
                      />
                      <p className="text-xs text-muted-foreground">
                        {t("failure.globalOffHint")}
                      </p>
                    </div>
                    <RecoveryOrderControls
                      value={draft}
                      onChange={(order) => setDraft({ ...draft, ...order })}
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
                    onChange={(default_failure_policy) =>
                      setDraft({ ...draft, default_failure_policy })
                    }
                  />
                ))}
              </fieldset>
            </TabsContent>
            <TabsContent
              value="rules"
              className="flex min-h-0 flex-1 flex-col overflow-hidden pb-1"
              data-tab-scroller
            >
              <fieldset
                disabled={!ready || saving}
                className="flex min-h-0 min-w-0 flex-1 flex-col"
              >
                <FailurePolicyEditor
                  section="rules"
                  title={t("routing.rulesTitle")}
                  hint={t("routing.rulesHint")}
                  headingLevel={2}
                  value={draft.default_failure_policy}
                  onChange={(default_failure_policy) =>
                    setDraft({ ...draft, default_failure_policy })
                  }
                />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="session"
              className="min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready || saving} className="min-w-0">
                <ChannelStickinessEditor
                  value={
                    draft.channel_stickiness ?? {
                      enabled: true,
                      ttl_seconds: 3600,
                    }
                  }
                  onChange={(channel_stickiness) =>
                    setDraft({ ...draft, channel_stickiness })
                  }
                />
              </fieldset>
            </TabsContent>
            <TabsContent
              value="identity"
              className="min-h-0 flex-1 overflow-y-auto pb-1"
              data-tab-scroller
            >
              <fieldset disabled={!ready || saving} className="min-w-0">
                <UpstreamIdentitySettings value={draft} onChange={setDraft} />
              </fieldset>
            </TabsContent>
          </>
        ) : (
          <TabsContent
            value={tab}
            className="min-h-0 flex-1 overflow-y-auto"
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
