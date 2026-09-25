import { useT } from "./i18n";
import { lazy, Suspense, useCallback, useRef, useState } from "react";
import { PageHeader } from "./PageHeader";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { DataRow } from "./components/DataRow";
import { Panel } from "./components/Panel";
import { ChevronRight, Route } from "./components/icons";
import { Button } from "./components/ui/button";
import { RoutingSettingsPanel } from "./RoutingSettingsPanel";
import type { RoutableService } from "./service-model";
import type { ProtocolDescriptor } from "./service-presets";

const RoutingGraphEditor = lazy(() =>
  import("./RoutingGraphEditor").then((module) => ({
    default: module.RoutingGraphEditor,
  })),
);

export function RouteManager({
  coreSessionKey,
  services,
  isReady,
  onDirtyChange,
}: {
  coreSessionKey: string | null;
  services: RoutableService[];
  isReady: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onManageServices: () => void;
  protocols: ProtocolDescriptor[];
}) {
  const t = useT();
  const [settings, setSettings] = useState(true);
  const [pendingSettings, setPendingSettings] = useState<boolean | null>(null);
  const dirty = useRef(false);
  const reportDirty = useCallback(
    (value: boolean) => {
      dirty.current = value;
      onDirtyChange(value);
    },
    [onDirtyChange],
  );
  function switchSettings(next: boolean) {
    if (dirty.current) setPendingSettings(next);
    else setSettings(next);
  }
  return (
    <section
      aria-labelledby="route-manager-title"
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      {settings ? (
        <>
          <PageHeader
            variant="compact"
            title={t("nav.routing")}
            titleId="route-manager-title"
          />
          <RoutingSettingsPanel
            ready={isReady}
            services={services}
            onDirtyChange={reportDirty}
          />
          <Panel className="mt-3 shrink-0">
            <DataRow asChild>
              <Button
                type="button"
                variant="ghost"
                className="h-auto w-full justify-start rounded-none text-left whitespace-normal"
                aria-label={t("graph.advanced")}
                aria-describedby="routing-advanced-description"
                onClick={() => switchSettings(false)}
              >
                <Route
                  aria-hidden="true"
                  className="size-4 text-muted-foreground"
                />
                <span className="min-w-0 flex-1">
                  <span className="block font-medium">
                    {t("graph.advanced")}
                  </span>
                  <span
                    id="routing-advanced-description"
                    className="mt-0.5 block text-xs font-normal text-muted-foreground"
                  >
                    {t("graph.advancedHint")}
                  </span>
                </span>
                <ChevronRight
                  aria-hidden="true"
                  className="size-4 text-muted-foreground"
                />
              </Button>
            </DataRow>
          </Panel>
        </>
      ) : (
        <Suspense
          fallback={
            <>
              <PageHeader
                title={t("graph.advanced")}
                titleId="route-manager-title"
                back={{
                  label: t("graph.backToSettings"),
                  onClick: () => switchSettings(true),
                }}
              />
              <p role="status" className="p-4 text-sm text-muted-foreground">
                {t("common.loading")}
              </p>
            </>
          }
        >
          <RoutingGraphEditor
            key={coreSessionKey ?? "offline"}
            ready={isReady}
            services={services}
            onDirtyChange={reportDirty}
            onSettings={() => switchSettings(true)}
          />
        </Suspense>
      )}
      <ConfirmDialog
        cancelLabel={t("common.continueEditing")}
        confirmLabel={t("common.discardAndLeave")}
        description={<p>{t("app.unsavedBody")}</p>}
        onCancel={() => setPendingSettings(null)}
        onConfirm={() => {
          if (pendingSettings !== null) setSettings(pendingSettings);
          setPendingSettings(null);
        }}
        open={pendingSettings !== null}
        title={t("common.discardUnsaved")}
      />
    </section>
  );
}
