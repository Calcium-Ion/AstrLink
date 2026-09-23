import { afterEach, describe, expect, it, vi } from "vitest";
import {
  invalidateResource,
  isResourceStale,
  markResourceFetched,
  wasInvalidatedSinceFetch,
} from "./resource-invalidation";

describe("resource invalidation", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("expires fetched resources after their TTL", () => {
    vi.useFakeTimers();
    const key = "billing-ttl:resource";
    expect(isResourceStale(key, 1_000)).toBe(true);

    markResourceFetched(key);
    expect(isResourceStale(key, 1_000)).toBe(false);

    vi.advanceTimersByTime(1_000);
    expect(isResourceStale(key, 1_000)).toBe(true);
  });

  it("invalidates a resource namespace and its children", () => {
    const key = "billing-ns:child";
    markResourceFetched(key);

    invalidateResource("billing-ns");

    expect(isResourceStale(key)).toBe(true);
  });

  it("reports invalidation after the last successful fetch", () => {
    const key = "usage-report:grok";
    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);

    invalidateResource(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(true);

    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);
  });

  it("child invalidation wakes a parent namespace subscriber", () => {
    markResourceFetched("usage-parent");
    invalidateResource("usage-parent:grok");
    expect(wasInvalidatedSinceFetch("usage-parent")).toBe(true);
  });

  it("invalidates only the addressed child", () => {
    markResourceFetched("usage-children:grok");
    markResourceFetched("usage-children:codex");

    invalidateResource("usage-children:grok");

    expect(wasInvalidatedSinceFetch("usage-children:grok")).toBe(true);
    expect(wasInvalidatedSinceFetch("usage-children:codex")).toBe(false);
  });

  it("treats invalidation before the first fetch as fresh", () => {
    const key = "usage-new:child";
    invalidateResource(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(true);

    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);
  });

  it("keeps parent invalidation fresh for children created later", () => {
    invalidateResource("usage-late-parent");

    expect(wasInvalidatedSinceFetch("usage-late-parent:new")).toBe(true);

    markResourceFetched("usage-late-parent:new");
    expect(wasInvalidatedSinceFetch("usage-late-parent:new")).toBe(false);
  });
});