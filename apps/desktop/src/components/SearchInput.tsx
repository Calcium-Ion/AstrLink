import { Search, X } from "@/components/icons";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/** Plain search field for lists that already show their filtered results. */
export function SearchInput({
  value,
  onValueChange,
  label,
  placeholder,
  clearLabel,
  className,
}: {
  value: string;
  onValueChange: (value: string) => void;
  label: string;
  placeholder: string;
  clearLabel: string;
  className?: string;
}) {
  return (
    <div className={cn("relative min-w-0", className)}>
      <Search
        aria-hidden="true"
        className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
      />
      <Input
        aria-label={label}
        className="h-8 pr-8 pl-8 [&::-webkit-search-cancel-button]:appearance-none"
        onChange={(event) => onValueChange(event.currentTarget.value)}
        placeholder={placeholder}
        type="search"
        value={value}
      />
      {value ? (
        <Button
          aria-label={clearLabel}
          className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
          onClick={() => onValueChange("")}
          size="icon-xs"
          type="button"
          variant="ghost"
        >
          <X aria-hidden="true" />
        </Button>
      ) : null}
    </div>
  );
}
