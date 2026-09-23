import { useSyncExternalStore } from "react";

type Listener = () => void;

const DEFAULT_TTL_MS = 30_000;

const revisions = new Map<string, number>();
const fetchedAt = new Map<string, number>();
const fetchedRevision = new Map<string, number>();
const listeners = new Map<string, Set<Listener>>();

function matches(resourceKey: string, invalidationKey: string): boolean {
  if (invalidationKey === "*" || resourceKey === invalidationKey) return true;
  return (
    resourceKey.startsWith(`${invalidationKey}:`) ||
    invalidationKey.startsWith(`${resourceKey}:`)
  );
}

function notify(invalidationKey: string): void {
  for (const [key, set] of listeners) {
    if (!matches(key, invalidationKey)) continue;
    for (const listener of set) listener();
  }
}

function bump(key: string): void {
  revisions.set(key, (revisions.get(key) ?? 0) + 1);
  fetchedAt.delete(key);
}

/**
 * Invalidate one or more resource keys and notify every mounted view that
 * depends on them. Keys form a hierarchy: invalidating `service-usage`
 * invalidates `service-usage:<id>` as well, and invalidating a child also
 * wakes a parent namespace subscriber.
 */
export function invalidateResource(...keys: string[]): void {
  if (keys.length === 0) return;
  const known = new Set([
    ...revisions.keys(),
    ...fetchedAt.keys(),
    ...listeners.keys(),
  ]);
  for (const invalidationKey of keys) {
    const bumped = new Set<string>();
    for (const resourceKey of known) {
      if (!matches(resourceKey, invalidationKey)) continue;
      bump(resourceKey);
      bumped.add(resourceKey);
    }
    if (!bumped.has(invalidationKey)) bump(invalidationKey);
    notify(invalidationKey);
  }
}

export function markResourceFetched(...keys: string[]): void {
  const now = Date.now();
  for (const key of keys) {
    fetchedAt.set(key, now);
    fetchedRevision.set(key, revisions.get(key) ?? 0);
  }
}

/**
 * True when a resource was invalidated after its last successful fetch. This
 * is the fresh-read signal for resources that also have a server-side cache.
 */
export function wasInvalidatedSinceFetch(key: string): boolean {
  const current = revisions.get(key) ?? 0;
  if (current === 0) return false;
  const fetched = fetchedRevision.get(key);
  return fetched === undefined || fetched !== current;
}

export function isResourceStale(
  key: string,
  ttlMs: number = DEFAULT_TTL_MS,
): boolean {
  const at = fetchedAt.get(key);
  return at === undefined || Date.now() - at >= ttlMs;
}

function subscribeResource(key: string, listener: Listener): () => void {
  let set = listeners.get(key);
  if (!set) {
    set = new Set();
    listeners.set(key, set);
  }
  set.add(listener);
  return () => {
    set.delete(listener);
    if (set.size === 0) listeners.delete(key);
  };
}

function revisionSnapshot(keys: readonly string[]): string {
  return keys.map((key) => revisions.get(key) ?? 0).join(":");
}

export function useResourceRevision(key: string): number {
  return useSyncExternalStore(
    (listener) => subscribeResource(key, listener),
    () => revisions.get(key) ?? 0,
    () => revisions.get(key) ?? 0,
  );
}

/**
 * Subscribe to a stable set of resource keys and return a fingerprint that
 * changes only when one of those keys is invalidated. Mainly used by views
 * that fan out over a dynamic list of per-service resources.
 */
export function useResourceRevisions(keys: readonly string[]): string {
  return useSyncExternalStore(
    (listener) => {
      const unsubscribers = keys.map((key) => subscribeResource(key, listener));
      return () => {
        for (const unsubscribe of unsubscribers) unsubscribe();
      };
    },
    () => revisionSnapshot(keys),
    () => revisionSnapshot(keys),
  );
}
