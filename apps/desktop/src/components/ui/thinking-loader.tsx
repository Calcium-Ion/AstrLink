/**
 * Adapted from affectUI's free Thinking Loader, MIT licensed.
 * https://www.affectui.com/r/thinking-loader.json
 * Copyright (c) 2026 Florian Höller (adfects).
 * See LICENSES/affectui-MIT.txt.
 *
 * Keeps the original eight-ray spinner and text shimmer. Labels are supplied
 * by real request state; this observer does not invent phases or elapsed time.
 */
import { cn } from "@/lib/utils";

import "./thinking-loader.css";

const RAYS = Array.from({ length: 8 }, (_, index) => index);

export type ThinkingLoaderProps = {
  label: string;
  className?: string;
  /** An icon-only 18px node; the label remains available to assistive technology. */
  compact?: boolean;
};

/** Render only while the observed request is actually pending. */
export function ThinkingLoader({
  label,
  className,
  compact = false,
}: ThinkingLoaderProps) {
  return (
    <span
      className={cn(
        "thinking-loader inline-flex min-w-0 max-w-full items-center overflow-hidden align-middle text-primary",
        compact
          ? "size-[1.125rem] shrink-0 justify-center"
          : "h-7 gap-1.5 rounded-md border bg-card px-2.5 text-xs",
        className,
      )}
      data-compact={compact || undefined}
      role="status"
      aria-label={label}
      title={compact ? label : undefined}
    >
      <svg
        className="thinking-loader__spinner size-3.5 shrink-0 overflow-visible"
        viewBox="0 0 16 16"
        aria-hidden="true"
      >
        {RAYS.map((index) => (
          <line
            key={index}
            x1="8"
            y1="1.75"
            x2="8"
            y2="4.75"
            transform={`rotate(${index * 45} 8 8)`}
            style={{ animationDelay: `${(index - 8) * 100}ms` }}
          />
        ))}
      </svg>
      {!compact && (
        <span key={label} className="thinking-loader__label" aria-hidden="true">
          {label}
        </span>
      )}
    </span>
  );
}
