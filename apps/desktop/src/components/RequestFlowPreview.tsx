import type { ReactNode } from "react";
import { motion, useReducedMotion } from "motion/react";

import { Check, X } from "@/components/icons";
import { cn } from "@/lib/utils";

export interface RequestFlowNode {
  id: string;
  name: string;
  caption?: string;
  icon: ReactNode;
}

export interface RequestFlowPacket {
  /** A new id starts a fresh packet instead of sliding the old one back. */
  id: string;
  protocol: string;
  rewritten: boolean;
  parts: { id: string; label: string; lost: boolean }[];
}

/** A read-only request path: one packet travels across three nodes. */
export function RequestFlowPreview({
  label,
  nodes,
  position,
  packet,
}: {
  label: string;
  nodes: readonly [RequestFlowNode, RequestFlowNode, RequestFlowNode];
  position: 0 | 1 | 2;
  packet: RequestFlowPacket;
}) {
  const reducedMotion = useReducedMotion();
  return (
    <figure aria-label={label} className="relative m-0 min-w-0 py-2">
      {/* Connect the centers of the outer node icons (size-11, 8px down). */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-[16.67%] top-7.5 border-t border-dashed border-input"
      />
      <ol className="relative m-0 grid list-none grid-cols-3 p-0">
        {nodes.map((node, index) => (
          <li
            className="flex min-w-0 flex-col items-center gap-1 px-1 text-center"
            data-active={index === position || undefined}
            data-flow-node={node.id}
            key={node.id}
          >
            <span
              className={cn(
                "flex size-11 items-center justify-center rounded-md border bg-card text-muted-foreground transition-[border-color,box-shadow]",
                index === position &&
                  "border-primary/60 ring-2 ring-primary/15",
              )}
            >
              {node.icon}
            </span>
            <span className="max-w-full truncate text-xs font-medium">
              {node.name}
            </span>
            <span className="min-h-4 max-w-full truncate text-micro text-muted-foreground">
              {node.caption}
            </span>
          </li>
        ))}
      </ol>
      {/* One third wide, so each x step of 100% lands under the next node. */}
      <motion.div
        animate={{ opacity: 1, x: `${position * 100}%` }}
        className="mt-2 w-1/3 px-1.5"
        data-flow-packet={position}
        initial={
          reducedMotion ? false : { opacity: 0, x: `${position * 100}%` }
        }
        key={packet.id}
        transition={{
          duration: reducedMotion ? 0 : 0.75,
          ease: [0.22, 1, 0.36, 1],
        }}
      >
        <div
          className={cn(
            "grid min-w-0 gap-1.5 rounded-md border p-2 shadow-sm transition-colors",
            packet.rewritten
              ? "border-warning/40 bg-warning-wash"
              : "border-primary/30 bg-card",
          )}
          data-rewritten={packet.rewritten || undefined}
        >
          <motion.span
            animate={{ opacity: 1, y: 0 }}
            className="truncate text-xs font-semibold"
            initial={reducedMotion ? false : { opacity: 0, y: -4 }}
            key={packet.protocol}
          >
            {packet.protocol}
          </motion.span>
          <ul className="m-0 grid list-none gap-1 p-0">
            {packet.parts.map((part) => (
              <li
                className={cn(
                  "flex min-w-0 items-center gap-1 text-micro transition-colors",
                  part.lost
                    ? "text-warning-foreground"
                    : "text-muted-foreground",
                )}
                data-lost={part.lost || undefined}
                key={part.id}
              >
                {part.lost ? (
                  <X
                    animateOnHover={false}
                    aria-hidden="true"
                    className="size-3"
                  />
                ) : (
                  <Check
                    animateOnHover={false}
                    aria-hidden="true"
                    className="size-3 text-success"
                  />
                )}
                <span className={cn("truncate", part.lost && "line-through")}>
                  {part.label}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </motion.div>
    </figure>
  );
}
