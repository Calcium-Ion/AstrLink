import { ServiceKindLabel } from "@/components/ServiceKindLabel";
import { cn } from "@/lib/utils";
import { useT } from "../i18n";
import type { RequestServiceIdentity } from "../request-service-model";
import { serviceKindLabel } from "../service-model";

/** Provider identity stays distinct from the model's brand mark. */
export function RequestServiceLabel({
  service,
  className,
  label,
  labelClassName,
}: {
  service: RequestServiceIdentity;
  className?: string;
  label?: string;
  labelClassName?: string;
}) {
  const t = useT();
  const prefix = label ?? t("records.provider");
  const title = [service.name, service.kind ? serviceKindLabel(service.kind) : null, service.id]
    .filter(Boolean).join(" · ");
  return (
    <span
      aria-label={`${prefix}: ${service.name}`}
      className={cn("inline-flex min-w-0 max-w-full items-center gap-1.5", className)}
      data-testid="request-service-label"
      title={title}
    >
      <span className={cn("shrink-0 text-muted-foreground", labelClassName)}>{prefix}</span>
      {service.kind ? (
        <ServiceKindLabel kind={service.kind}>{service.name}</ServiceKindLabel>
      ) : (
        <span className="truncate">{service.name}</span>
      )}
    </span>
  );
}
