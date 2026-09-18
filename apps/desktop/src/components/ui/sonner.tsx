"use client"

import {
  CircleCheck as CircleCheckIcon,
  CircleHelp as InfoIcon,
  LoaderCircle as Loader2Icon,
  Ban as OctagonXIcon,
  BadgeAlert as TriangleAlertIcon,
  X,
} from "@/components/icons";
import { Toaster as Sonner, type ToasterProps } from "sonner"

const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      theme="light"
      className="toaster group"
      icons={{
        close: <X className="size-3" />,
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon animateOnHover={false} className="size-4 animate-spin motion-reduce:animate-none" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
