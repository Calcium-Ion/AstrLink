import { isTauri } from "@tauri-apps/api/core";
import type { ComponentProps } from "react";

import { ArrowUpRight } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { openExternalURL } from "@/bridge";
import { i18n } from "@/i18n";
import { cn } from "@/lib/utils";
import { notify } from "@/notify";

export function ExternalLink({
  children,
  className,
  href,
  ...props
}: Omit<ComponentProps<"a">, "href" | "target" | "rel" | "onClick"> & {
  href: string;
}) {
  return (
    <Button
      asChild
      className={cn("h-auto p-0 align-baseline font-normal text-primary has-[>svg]:px-0", className)}
      variant="link"
    >
      <a
        {...props}
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        onClick={(event) => {
          if (!isTauri()) return;
          event.preventDefault();
          void openExternalURL(href).catch(() => {
            notify.error(i18n.t("common.openLinkFailed"));
          });
        }}
      >
        {children}
        <ArrowUpRight aria-hidden="true" className="size-3" />
      </a>
    </Button>
  );
}
