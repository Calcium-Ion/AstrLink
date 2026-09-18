import type { ReactNode } from "react";

import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import type { ServiceKind } from "../service-model";

/** A service provider's brand mark alongside its visible label. */
export function ServiceKindLabel({
  kind,
  children,
}: {
  kind: ServiceKind;
  children: ReactNode;
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-2">
      <span aria-hidden="true">
        <ServiceKindIcon kind={kind} size={16} />
      </span>
      <span className="truncate">{children}</span>
    </span>
  );
}
