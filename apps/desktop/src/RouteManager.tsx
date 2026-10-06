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
  return (
    <section
      aria-labelledby="route-manager-title"
      className="gutter-frame flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      <RoutingSettingsPanel
        ready={isReady}
        services={services}
        titleId="route-manager-title"
        onDirtyChange={onDirtyChange}
      />
    </section>
  );
}
