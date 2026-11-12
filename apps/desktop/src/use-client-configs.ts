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

const changeListeners = new Set<() => void>();

/**
 * Tells every mounted {@link useClientConfigs} to read again. Whoever writes
 * or removes a client config calls it, whichever page started the change.
 */
export function clientConfigsChanged() {
  for (const listener of changeListeners) listener();
}

/** Calls `listener` after every {@link clientConfigsChanged}. */
export function onClientConfigsChanged(listener: () => void): () => void {
  changeListeners.add(listener);
  return () => {
    changeListeners.delete(listener);
  };
}

/**
 * What AstrLink wrote to the local clients, read again after every
 * {@link clientConfigsChanged} and whenever the window comes back: clients and
 * other tools rewrite their configs outside AstrLink. The marks it feeds only
 * annotate lists, so a failed read hides them.
 *
 * `available` holds the models the gateway serves, from the local catalog,
 * while some config names a token; null when unknown. `loaded` turns true
 * once the first read settled, failed or not.
 */
export function useClientConfigs(
  inferenceURL: string,
  /** Reads nothing and reports no configs while false. */
  enabled = true,
): {
  configs: ClientConfigStatus[];
  available: ReadonlySet<string> | null;
  loaded: boolean;
} {
  const [configs, setConfigs] = useState<ClientConfigStatus[]>([]);
  const [loaded, setLoaded] = useState(false);
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
          setLoaded(true);
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
          setLoaded(true);
        },
      );
  }, [inferenceURL]);

  useEffect(() => {
    if (!enabled) {
      setConfigs((current) => (current.length ? [] : current));
      setAvailable(null);
      setLoaded(false);
      return;
    }
    refresh();
    window.addEventListener("focus", refresh);
    const unsubscribe = onClientConfigsChanged(refresh);
    return () => {
      generation.current += 1;
      window.removeEventListener("focus", refresh);
      unsubscribe();
    };
  }, [enabled, refresh]);

  return { configs, available, loaded };
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

/**
 * Whether a client holds one of `tokens`, by the rule the token lists mark
 * with. Configs another AstrLink wrote name tokens this one does not list.
 */
export function anyTokenConfigured(
  configs: readonly ClientConfigStatus[],
  tokens: readonly { id: string }[],
): boolean {
  return tokens.some((token) => configsUsing(configs, token.id).length > 0);
}

export function clientsUsing(
  configs: readonly ClientConfigStatus[],
  tokenId: string | undefined,
): ClientConfigClient[] {
  return [
    ...new Set(configsUsing(configs, tokenId).map((status) => status.client)),
  ];
}
