import { useT } from "../i18n";
import type {
  ServicePerformance,
  UsageRangePreset,
  UsageStatus,
} from "../usage-range";
import { UsagePerformanceMeter } from "./UsagePerformanceMeter";

export function ServicePerformanceMeter({
  performance,
  status,
  preset,
  serviceId,
  serviceName,
  ready,
}: {
  performance?: ServicePerformance;
  status: UsageStatus;
  preset: UsageRangePreset;
  serviceId: string;
  serviceName: string;
  ready: boolean;
}) {
  const t = useT();
  return (
    <UsagePerformanceMeter
      performance={performance}
      status={status}
      periodLabel={t(`overview.range.${preset}`)}
      scopeDescription={t("services.performanceScope")}
      testId="service-performance"
      layout="service"
      target={{ kind: "service", id: serviceId, name: serviceName }}
      ready={ready}
    />
  );
}
