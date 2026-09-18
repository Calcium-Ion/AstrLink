import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import { EmptyState } from "./components/EmptyState";
import { FormMessage } from "./components/FormMessage";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { useT } from "./i18n";
import { autoRoutingStatus } from "./route-model";
import type { RoutableService } from "./service-model";
import { sortRoutes, useRouteCatalog } from "./use-route-catalog";

export function AutoRoutesPanel({
  coreSessionKey,
  services,
  isReady,
  protocolIDs,
  onDirtyChange,
}: {
  coreSessionKey: string | null;
  services: RoutableService[];
  isReady: boolean;
  protocolIDs: string[];
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const { catalog, setCatalog, refresh, hasLoaded } = useRouteCatalog(
    isReady,
    coreSessionKey,
  );
  const status = autoRoutingStatus(catalog.items);
  return (
    <div className="grid gap-4 pb-6">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">{t("routes.autoHint")}</p>
        {catalog.status === "ready" ? (
          <Badge variant="secondary">
            {status === "enabled"
              ? t("common.enabled")
              : status === "disabled"
                ? t("common.disabled")
                : t("routes.unconfigured")}
          </Badge>
        ) : null}
      </div>
      {catalog.error ? (
        <FormMessage tone="error">
          {catalog.error}
          <Button type="button" variant="ghost" onClick={() => void refresh()}>
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {catalog.status === "loading" && !hasLoaded ? (
        <EmptyState title={t("routes.loading")} />
      ) : catalog.status !== "error" || hasLoaded ? (
        <AutoRoutingShowcase
          isReady={isReady && catalog.status === "ready"}
          onDirtyChange={onDirtyChange}
          onRouteSaved={(route) =>
            setCatalog((current) => ({
              status: "ready",
              items: sortRoutes([
                ...current.items.filter((item) => item.id !== route.id),
                route,
              ]),
              error: null,
              stale: false,
            }))
          }
          protocolIDs={protocolIDs}
          routes={catalog.items}
          services={services}
        />
      ) : null}
    </div>
  );
}
