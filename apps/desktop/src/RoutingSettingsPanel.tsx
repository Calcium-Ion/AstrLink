import { useEffect, useRef, useState } from "react";
import { getRoutingSettings, updateRoutingSettings } from "./bridge";
import { ChannelStickinessEditor } from "./components/ChannelStickinessEditor";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import {
  FailoverToggle,
  RecoveryOrderControls,
} from "./components/FailoverEditor";
import { FormMessage } from "./components/FormMessage";
import { Panel, PanelHeader } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import {
  parseRoutingSettings,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";
import { notify } from "./notify";

export function RoutingSettingsPanel({
  ready,
  onDirtyChange,
}: {
  ready: boolean;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [tab, setTab] = useState("recovery");
  const [draft, setDraft] = useState<RoutingSettings | null>(null);
  const [baseline, setBaseline] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [reload, setReload] = useState(0);
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!ready) return;
    let active = true;
    void getRoutingSettings()
      .then((settings) => {
        if (!active) return;
        // Reconnection and retry must never replace an unsaved draft.
        if (!dirtyRef.current) {
          setDraft(settings);
          setBaseline(JSON.stringify(settings));
        }
        setLoadError(null);
      })
      .catch((error) => {
        if (active)
          setLoadError(
            error instanceof Error ? error.message : t("failure.loadFailed"),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t]);

  const save = async () => {
    if (!draft || !ready || saving) return;
    try {
      parseRoutingSettings(draft);
    } catch {
      setError(t("failure.invalid"));
      return;
    }
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
      ] as const) {
        if (JSON.stringify(draft[key]) !== JSON.stringify(original[key]))
          Object.assign(patch, { [key]: draft[key] });
      }
      const saved = await updateRoutingSettings(patch);
      setDraft(saved);
      setBaseline(JSON.stringify(saved));
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
            {["recovery", "rules", "session"].map((value) => (
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
                  <div className="p-4">
                    <RecoveryOrderControls
                      value={draft}
                      onChange={(order) => setDraft({ ...draft, ...order })}
                    />
                  </div>
                </Panel>
                <Panel>
                  <PanelHeader>
                    <h2 className="text-sm font-semibold">
                      {t("failure.globalTitle")}
                    </h2>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t("failure.globalHint")}
                    </p>
                  </PanelHeader>
                  <div className="grid gap-4 p-4">
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
              <fieldset disabled={!ready || saving} className="flex min-h-0 min-w-0 flex-1 flex-col">
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
