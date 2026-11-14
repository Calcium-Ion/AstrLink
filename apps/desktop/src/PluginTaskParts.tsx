import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { useT } from "./i18n";
import {
  TASK_PROTOCOL_ENDPOINTS,
  taskModesLabel,
  taskProtocolLabel,
} from "./plugin-task-labels";
import type { PluginTaskProtocol, PluginTaskRoute } from "./plugin-model";

/** Models as quiet chips; past a dozen, the rest behind a toggle. */
export function ModelChips({ models }: { models: string[] }) {
  const t = useT();
  const [all, setAll] = useState(false);
  const shown = all ? models : models.slice(0, 12);
  return (
    <div className="flex flex-wrap gap-1" data-testid="plugin-models">
      {shown.map((model) => (
        <Badge
          className="px-1.5 py-0 font-mono text-micro font-normal"
          key={model}
          variant="secondary"
        >
          {model}
        </Badge>
      ))}
      {models.length > 12 ? (
        <Button
          className="h-5 px-1.5 text-micro"
          onClick={() => setAll((current) => !current)}
          size="xs"
          type="button"
          variant="ghost"
        >
          {all
            ? t("plugins.market.fewerModels")
            : t("plugins.market.moreModels", { count: models.length - 12 })}
        </Button>
      ) : null}
    </div>
  );
}

/** The gateway endpoints a task plugin answers on, then its native routes. */
export function TaskEndpoints({
  protocols,
  routes,
}: {
  protocols: PluginTaskProtocol[];
  routes: PluginTaskRoute[];
}) {
  const t = useT();
  if (protocols.length === 0 && routes.length === 0)
    return (
      <p className="text-xs text-muted-foreground">
        {t("plugins.task.noEndpoints")}
      </p>
    );
  return (
    <div className="@container min-w-0">
      <ul
        className="grid divide-y overflow-hidden rounded-md border text-xs"
        data-testid="plugin-task-endpoints"
      >
        {protocols.map((protocol) => (
          <li
            className="grid gap-0.5 px-3 py-2 @[480px]:grid-cols-[8rem_minmax(0,1fr)_auto] @[480px]:items-center @[480px]:gap-3"
            key={protocol.name}
          >
            <span className="font-medium">
              {taskProtocolLabel(protocol.name, t)}
            </span>
            <code className="min-w-0 truncate font-mono text-micro text-muted-foreground">
              {(TASK_PROTOCOL_ENDPOINTS[protocol.name] ?? []).join(" · ")}
            </code>
            <span className="text-micro text-muted-foreground">
              {[
                taskModesLabel(protocol, t),
                protocol.models?.length
                  ? t("plugins.market.models", {
                      count: protocol.models.length,
                    })
                  : "",
              ]
                .filter(Boolean)
                .join(" · ")}
            </span>
          </li>
        ))}
        {routes.map((route) => (
          <li
            className="grid min-w-0 grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-3 px-3 py-1.5"
            key={`${route.method} ${route.path}`}
          >
            <span className="font-mono text-micro text-muted-foreground">
              {route.method}
            </span>
            <code className="min-w-0 truncate font-mono text-micro">
              {route.path}
            </code>
            <span className="text-micro text-muted-foreground">
              {t(`plugins.task.routeTypes.${route.type}`, {
                defaultValue: route.type,
              })}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
