import { useCallback, useEffect, useRef, useState } from "react";
import { listRoutes } from "./bridge";
import { i18n } from "./i18n";
import type { Route } from "./route-model";

type CatalogState = {
  status: "blocked" | "loading" | "ready" | "error";
  items: Route[];
  error: string | null;
  stale: boolean;
};

export function sortRoutes(routes: Route[]): Route[] {
  return [...routes].sort((left, right) => {
    if (left.priority !== right.priority) return left.priority - right.priority;
    const exact =
      Number(Boolean(right.match.model)) - Number(Boolean(left.match.model));
    return exact || left.id.localeCompare(right.id);
  });
}

export function useRouteCatalog(ready: boolean, coreSessionKey: string | null) {
  const [hasLoaded, setHasLoaded] = useState(false);
  const [catalog, setCatalog] = useState<CatalogState>({
    status: ready ? "loading" : "blocked",
    items: [],
    error: null,
    stale: false,
  });
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const requestGeneration = ++generation.current;
    if (!ready) {
      setCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }
    setCatalog((current) => ({
      ...current,
      status: "loading",
      error: null,
      stale: current.items.length > 0,
    }));
    try {
      const page = await listRoutes();
      if (generation.current !== requestGeneration) return;
      setCatalog({
        status: "ready",
        items: sortRoutes(page.items),
        error: null,
        stale: false,
      });
      setHasLoaded(true);
    } catch (error) {
      if (generation.current !== requestGeneration) return;
      setCatalog((current) => ({
        ...current,
        status: "error",
        error:
          error instanceof Error ? error.message : i18n.t("routes.readFailed"),
        stale: current.items.length > 0,
      }));
    }
  }, [ready]);

  useEffect(() => {
    void refresh();
    return () => {
      generation.current += 1;
    };
  }, [coreSessionKey, refresh]);

  return { catalog, setCatalog, refresh, hasLoaded };
}
