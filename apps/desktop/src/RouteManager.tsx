import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { RoutingSettingsPanel } from "./RoutingSettingsPanel";
import type { RoutableService } from "./service-model";

export function RouteManager({
  services,
  isReady,
  onDirtyChange,
}: {
  services: RoutableService[];
  isReady: boolean;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  return (
    <section
      aria-labelledby="route-manager-title"
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader
        variant="compact"
        title={t("nav.routing")}
        titleId="route-manager-title"
      />
      <RoutingSettingsPanel
        ready={isReady}
        services={services}
        onDirtyChange={onDirtyChange}
      />
    </section>
  );
}
