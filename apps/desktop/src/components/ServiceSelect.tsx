import type { ComponentProps } from "react";
import type { Service } from "../service-model";
import { FilterSelect } from "./FilterSelect";
import { ServiceKindLabel } from "./ServiceKindLabel";

/** Provider selectors share the same brand mark in options and selected values. */
export function ServiceSelect({
  services,
  allLabel,
  ...props
}: Omit<ComponentProps<typeof FilterSelect>, "options"> & {
  services: readonly Pick<Service, "id" | "name" | "kind">[];
  allLabel?: string;
}) {
  return (
    <FilterSelect
      {...props}
      options={[
        ...(allLabel ? [{ label: allLabel, value: "" }] : []),
        ...services.map((service) => ({
          label: service.name,
          value: service.id,
          displayLabel: (
            <ServiceKindLabel kind={service.kind}>
              {service.name}
            </ServiceKindLabel>
          ),
        })),
      ]}
    />
  );
}
