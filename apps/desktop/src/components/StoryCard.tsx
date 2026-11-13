import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * Something the agent did in answer to the user: what the user said, in the
 * conversation's message bubble, then what came of it with its time and a
 * line under it. The whole card opens it. A fresh card fades in.
 */
export function StoryCard({
  prompt,
  promptMissing,
  title,
  hook,
  time,
  dateTime,
  fresh = false,
  onOpen,
}: {
  /** What the user said; absent shows `promptMissing` quietly instead. */
  prompt?: string;
  promptMissing: string;
  title: ReactNode;
  hook?: ReactNode;
  time: string;
  dateTime: string;
  fresh?: boolean;
  onOpen: () => void;
}) {
  return (
    <Button
      className={cn(
        "flex h-auto min-w-0 flex-col items-stretch gap-0 rounded-lg border bg-transparent px-3.25 pt-2.75 pb-2.5 text-left font-normal whitespace-normal text-foreground hover:bg-muted/40 hover:text-foreground",
        fresh && "motion-safe:animate-[panel-reveal_600ms_ease-out]",
      )}
      data-fresh={fresh || undefined}
      data-slot="story-card"
      onClick={onOpen}
      type="button"
      variant="ghost"
    >
      <span
        className={cn(
          "relative mb-3 block rounded-lg bg-muted px-2.5 py-1.5 text-xs leading-5",
          "after:absolute after:-bottom-1 after:left-4 after:size-2.25 after:rotate-45 after:rounded-[1px] after:bg-muted after:content-['']",
          !prompt && "text-muted-foreground",
        )}
      >
        <span className="block truncate">{prompt || promptMissing}</span>
      </span>
      <span className="flex min-w-0 items-baseline gap-1.5">
        <span aria-hidden="true" className="shrink-0 text-sm text-tide">
          ↳
        </span>
        <strong className="min-w-0 truncate text-sm font-semibold">
          {title}
        </strong>
        <time
          className="ml-auto shrink-0 pl-2 text-xs text-accent-foreground tabular-nums"
          dateTime={dateTime}
        >
          {time}
        </time>
      </span>
      {hook ? (
        <span className="mt-0.5 ml-4.75 block truncate text-xs text-muted-foreground">
          {hook}
        </span>
      ) : null}
    </Button>
  );
}
