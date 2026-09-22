import type { ReactNode } from "react";
import { motion, useReducedMotion } from "motion/react";

import { GripVertical } from "@/components/icons";
import { cn } from "@/lib/utils";

const rowHeight = 64;

/** A read-only order preview: animation never dispatches real drag events. */
export function ReorderPreview({
  items,
  order,
  lifted,
  firstLabel,
  label,
}: {
  items: { id: string; name: string; icon: ReactNode }[];
  order: string[];
  lifted: string | null;
  firstLabel: string;
  label: string;
}) {
  const reducedMotion = useReducedMotion();
  return (
    <div className="relative min-w-0 py-2">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-2 rounded-md border border-dashed border-primary/60 bg-primary/5"
        style={{ height: rowHeight - 4 }}
      />
      <ol
        aria-label={label}
        className="relative m-0 list-none p-0"
        style={{ height: items.length * rowHeight }}
      >
        {order.map((id, index) => {
          const item = items.find((candidate) => candidate.id === id)!;
          const dragging = lifted === id;
          return (
            <motion.li
              key={id}
              initial={false}
              animate={{
                y: index * rowHeight,
                x: dragging ? 8 : 0,
                scale: dragging ? 1.015 : 1,
              }}
              transition={{
                duration: reducedMotion ? 0 : 0.85,
                ease: [0.22, 1, 0.36, 1],
              }}
              data-preview-item={id}
              data-lifted={dragging || undefined}
              className={cn(
                "absolute inset-x-2 top-0 flex min-w-0 items-center gap-3 rounded-md px-3",
                dragging
                  ? "z-20 bg-card shadow-lg ring-1 ring-primary/60"
                  : "bg-card/70",
              )}
              style={{ height: rowHeight - 8 }}
            >
              <span
                className="relative flex size-6 shrink-0 items-center justify-center text-muted-foreground"
                aria-hidden="true"
              >
                <motion.span
                  initial={false}
                  animate={{
                    opacity: dragging ? 1 : 0,
                    scale: dragging ? 1 : 0.6,
                  }}
                  transition={{ duration: reducedMotion ? 0 : 0.2 }}
                  className="absolute -inset-1 rounded-full border-2 border-primary bg-primary/10"
                />
                <GripVertical
                  className={cn("size-4", dragging && "text-primary")}
                />
                {dragging ? (
                  <svg
                    viewBox="0 0 24 24"
                    className="absolute top-3 left-3 size-6 fill-primary stroke-primary-foreground drop-shadow-sm"
                    strokeWidth="1.5"
                  >
                    <path d="m4 2 15 12-7 1-3 7Z" strokeLinejoin="round" />
                  </svg>
                ) : null}
              </span>
              <span className="w-3 shrink-0 text-xs tabular-nums text-muted-foreground">
                {index + 1}
              </span>
              {item.icon}
              <span className="min-w-0 flex-1 truncate text-sm font-medium">
                {item.name}
              </span>
              <span
                aria-hidden={index !== 0 || undefined}
                className={cn(
                  "shrink-0 text-xs font-medium text-primary",
                  index !== 0 && "invisible",
                )}
              >
                {firstLabel}
              </span>
            </motion.li>
          );
        })}
      </ol>
    </div>
  );
}
