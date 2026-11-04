import { useCallback, useEffect, useRef, useState } from "react";

import {
  getClientCatalog,
  getClientConfigStatus,
  getRoutingSettings,
  listServices,
} from "./bridge";
import {
  availableModels,
  type ClientCatalog,
  type ClientConfigClient,
  type ClientConfigStatus,
} from "./client-config-model";
import { rememberClientNames } from "./client-setup-clients";

/**
 * What AstrLink wrote to the local clients, read again whenever the window
 * comes back: clients and other tools rewrite their configs outside AstrLink.
 * The marks it feeds only annotate lists, so a failed read hides them.
 *
 * `available` holds the models the gateway serves, from the local catalog,
 * while some config names a token; null when unknown.
 */
export function useClientConfigs(inferenceURL: string): {
  configs: ClientConfigStatus[];
  available: ReadonlySet<string> | null;
  refresh: () => void;
} {
  const [configs, setConfigs] = useState<ClientConfigStatus[]>([]);
  const [available, setAvailable] = useState<ReadonlySet<string> | null>(null);
  const generation = useRef(0);
  // The catalog only changes with the app, so it is read once.
  const catalog = useRef<Promise<ClientCatalog> | null>(null);
  const refresh = useCallback(() => {
    const current = ++generation.current;
    catalog.current ??= Promise.resolve()
      .then(() => getClientCatalog())
      .then(
        (value) => {
          rememberClientNames(value);
          return value;
        },
        (cause: unknown) => {
          catalog.current = null;
          throw cause;
        },
      );
    catalog.current
      .then((value) => getClientConfigStatus(inferenceURL || null, value))
      .then(
        (overview) => {
          if (generation.current !== current) return;
          setConfigs(overview.clients);
          if (!overview.clients.some((status) => status.token_id !== null)) {
            setAvailable(null);
            return;
          }
          // Redirect sources are optional; a failed read only drops them.
          void Promise.allSettled([
            Promise.resolve().then(() => listServices()),
            Promise.resolve().then(() => getRoutingSettings()),
          ]).then(([services, routing]) => {
            if (generation.current !== current) return;
            setAvailable(
              services.status === "fulfilled"
                ? availableModels(
                    services.value.items,
                    routing.status === "fulfilled"
                      ? (routing.value.model_redirects ?? [])
                      : [],
                  )
                : null,
            );
          });
        },
        () => {
          if (generation.current !== current) return;
          setConfigs([]);
          setAvailable(null);
        },
      );
  }, [inferenceURL]);

  useEffect(() => {
    refresh();
    window.addEventListener("focus", refresh);
    return () => {
      generation.current += 1;
      window.removeEventListener("focus", refresh);
    };
  }, [refresh]);

  return { configs, available, refresh };
}

/** A client may hold the token on this computer and in WSL distributions. */
export function configsUsing(
  configs: readonly ClientConfigStatus[],
  tokenId: string | undefined,
): ClientConfigStatus[] {
  return configs.filter(
    (status) =>
      status.token_id === tokenId && status.state !== "not_configured",
  );
}

export function clientsUsing(
  configs: readonly ClientConfigStatus[],
  tokenId: string | undefined,
): ClientConfigClient[] {
  return [
    ...new Set(configsUsing(configs, tokenId).map((status) => status.client)),
  ];
}
