import { useMemo, useSyncExternalStore } from "react";

const serverSnapshot = () => false;

/** Tracks a viewport preference without keeping a second copy in React state. */
export function useMediaQuery(query: string): boolean {
  const store = useMemo(() => {
    const media =
      typeof window !== "undefined" && typeof window.matchMedia === "function"
        ? window.matchMedia(query)
        : null;

    return {
      getSnapshot: () => media?.matches ?? false,
      subscribe: (onChange: () => void) => {
        media?.addEventListener("change", onChange);
        return () => media?.removeEventListener("change", onChange);
      },
    };
  }, [query]);

  return useSyncExternalStore(
    store.subscribe,
    store.getSnapshot,
    serverSnapshot,
  );
}
