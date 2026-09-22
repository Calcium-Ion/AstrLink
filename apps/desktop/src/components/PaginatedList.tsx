import { useState, type ReactNode } from "react";

import { ChevronRight } from "@/components/icons";
import { PanelFooter } from "@/components/Panel";
import { Button } from "@/components/ui/button";
import { i18n } from "@/i18n";

/** A short list with local paging; the surrounding workspace owns scrolling. */
export function PaginatedList<T>({
  items,
  pageSize = 5,
  label,
  itemsClassName,
  footer,
  children,
}: {
  items: readonly T[];
  pageSize?: number;
  label: string;
  itemsClassName?: string;
  footer: ReactNode;
  children: (visibleItems: T[]) => ReactNode;
}) {
  const [page, setPage] = useState(0);
  const size = Math.max(1, pageSize);
  const pageCount = Math.max(1, Math.ceil(items.length / size));
  const currentPage = Math.min(page, pageCount - 1);
  // A refreshed list may have fewer pages. Keep the visible page valid, including
  // if the list subsequently grows again.
  if (page !== currentPage) setPage(currentPage);

  return (
    <div className="min-w-0" data-slot="paginated-list">
      <div className={itemsClassName} data-slot="paginated-list-items">
        {children(items.slice(currentPage * size, (currentPage + 1) * size))}
      </div>
      <PanelFooter
        className="min-h-12 bg-transparent px-4 py-2"
        actions={
          pageCount > 1 ? (
            <nav aria-label={label} className="flex items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={i18n.t("common.previousPage")}
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                <ChevronRight aria-hidden="true" className="rotate-180" />
              </Button>
              <span
                className="min-w-9 text-center text-xs text-muted-foreground tabular-nums"
                aria-live="polite"
                aria-atomic="true"
              >
                {currentPage + 1} / {pageCount}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={i18n.t("common.nextPage")}
                disabled={currentPage === pageCount - 1}
                onClick={() => setPage(currentPage + 1)}
              >
                <ChevronRight aria-hidden="true" />
              </Button>
            </nav>
          ) : undefined
        }
      >
        {footer}
      </PanelFooter>
    </div>
  );
}
