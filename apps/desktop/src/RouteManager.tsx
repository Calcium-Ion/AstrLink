import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { RoutingSettingsPanel } from "./RoutingSettingsPanel";
import type { RoutableService } from "./service-model";
import type { ProtocolDescriptor } from "./service-presets";

export function RouteManager({
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
  return (
    <section
      aria-labelledby="route-manager-title"
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader title={t("nav.routing")} titleId="route-manager-title" />
      <div className="min-h-0 flex-1 overflow-y-auto" data-tab-scroller>
        <RoutingSettingsPanel ready={isReady} onDirtyChange={onDirtyChange} />
      </div>
    </section>
  );
}
