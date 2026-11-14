import { useState } from "react";

import { OrderedList } from "@/components/OrderedList";
import { PluginIcon } from "@/components/PluginIcon";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { useT } from "./i18n";
import type { PluginSummary } from "./plugin-model";

/**
 * Plugins in the order their extension points run: those the order lists
 * first, in its order, then the rest by key, as Core runs them.
 */
export function orderedPlugins(
  plugins: PluginSummary[],
  order: string[] | undefined,
): PluginSummary[] {
  const rank = (key: string) => {
    const index = (order ?? []).indexOf(key);
    return index < 0 ? Number.MAX_SAFE_INTEGER : index;
  };
  return [...plugins].sort(
    (a, b) => rank(a.key) - rank(b.key) || a.key.localeCompare(b.key),
  );
}

/**
 * Sets the order the plugins' extension points run in: each plugin gets
 * what the one above it returned.
 */
export function PluginOrderDialog({
  plugins,
  order,
  saving,
  onCancel,
  onSave,
}: {
  plugins: PluginSummary[];
  order: string[] | undefined;
  saving: boolean;
  onCancel: () => void;
  onSave: (order: string[]) => void;
}) {
  const t = useT();
  const [items, setItems] = useState(() =>
    orderedPlugins(plugins, order).map((plugin) => ({
      id: plugin.key,
      plugin,
    })),
  );
  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader className="text-left">
          <DialogTitle>{t("plugins.order.title")}</DialogTitle>
          <DialogDescription>
            {t("plugins.order.description")}
          </DialogDescription>
        </DialogHeader>
        <OrderedList
          compact
          disabled={saving}
          itemLabel={(item) => item.plugin.name}
          items={items}
          label={t("plugins.order.title")}
          onChange={setItems}
        >
          {(item, _index, controls) => (
            <div
              className="flex min-w-0 items-center gap-2 py-1"
              data-testid="plugin-order-row"
            >
              {controls}
              <PluginIcon icon={item.plugin.icon} />
              <span className="min-w-0 truncate text-sm">
                {item.plugin.name}
              </span>
              {!item.plugin.enabled ? (
                <span className="shrink-0 text-micro text-muted-foreground">
                  {t("plugins.state.disabled")}
                </span>
              ) : null}
            </div>
          )}
        </OrderedList>
        <DialogFooter>
          <Button
            disabled={saving}
            onClick={onCancel}
            size="sm"
            type="button"
            variant="outline"
          >
            {t("common.cancel")}
          </Button>
          <Button
            disabled={saving}
            onClick={() => onSave(items.map((item) => item.id))}
            size="sm"
            type="button"
          >
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
