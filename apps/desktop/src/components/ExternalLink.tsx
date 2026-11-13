import { isDesktopHost } from "../native-host/core";
import type { ComponentProps, MouseEvent } from "react";

import { ArrowUpRight } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { openExternalURL } from "@/bridge";
import { i18n } from "@/i18n";
import { cn } from "@/lib/utils";
import { notify } from "@/notify";

/** The desktop WebView ignores `target="_blank"`; hand the URL to the OS. */
export function openInSystemBrowser(
  event: MouseEvent<HTMLAnchorElement>,
  href: string,
) {
  if (!isDesktopHost()) return;
  event.preventDefault();
  void openExternalURL(href).catch(() => {
    notify.error(i18n.t("common.openLinkFailed"));
  });
}

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
      className={cn(
        "h-auto p-0 align-baseline font-normal text-primary has-[>svg]:px-0",
        className,
      )}
      variant="link"
    >
      <a
        {...props}
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        onClick={(event) => openInSystemBrowser(event, href)}
      >
        {children}
        <ArrowUpRight aria-hidden="true" className="size-3" />
      </a>
    </Button>
  );
}
