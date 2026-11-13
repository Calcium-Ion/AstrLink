import { cn } from "@/lib/utils";

import {
  clientBrandMark,
  clientIconSize,
  clientInitials,
  clientLabel,
} from "../client-setup-clients";

/**
 * A client's brand mark, or its initials when it has none. Decorative: the
 * client's name is always shown or spoken beside it.
 */
export function ClientMark({
  client,
  size,
  className,
}: {
  client: string;
  size: number;
  className?: string;
}) {
  const Mark = clientBrandMark(client);
  if (Mark) {
    return (
      <Mark
        aria-hidden="true"
        className={className}
        size={clientIconSize(client, size)}
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center leading-none font-semibold text-text-secondary",
        className,
      )}
      data-slot="client-initials"
      style={{
        width: size,
        height: size,
        fontSize: Math.max(7, Math.round(size * 0.72)),
      }}
    >
      {clientInitials(clientLabel(client))}
    </span>
  );
}
