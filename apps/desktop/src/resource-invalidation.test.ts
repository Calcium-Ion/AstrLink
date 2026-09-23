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
    const key = "service-billing:service_test";
    expect(isResourceStale(key, 1_000)).toBe(true);

    markResourceFetched(key);
    expect(isResourceStale(key, 1_000)).toBe(false);

    vi.advanceTimersByTime(1_000);
    expect(isResourceStale(key, 1_000)).toBe(true);
  });

  it("invalidates a resource namespace and its children", () => {
    const key = "service-billing:service_test";
    markResourceFetched(key);

    invalidateResource("service-billing");

    expect(isResourceStale(key)).toBe(true);
  });

  it("reports invalidation after the last successful fetch", () => {
    const key = "service-usage:grok";
    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);

    invalidateResource(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(true);

    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);
  });

  it("child invalidation wakes a parent namespace subscriber", () => {
    markResourceFetched("service-usage");
    invalidateResource("service-usage:grok");
    expect(wasInvalidatedSinceFetch("service-usage")).toBe(true);
  });

  it("invalidates only the addressed child", () => {
    markResourceFetched("service-usage:grok");
    markResourceFetched("service-usage:codex");

    invalidateResource("service-usage:grok");

    expect(wasInvalidatedSinceFetch("service-usage:grok")).toBe(true);
    expect(wasInvalidatedSinceFetch("service-usage:codex")).toBe(false);
  });

  it("treats invalidation before the first fetch as fresh", () => {
    const key = "service-usage:new";
    invalidateResource(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(true);

    markResourceFetched(key);
    expect(wasInvalidatedSinceFetch(key)).toBe(false);
  });
});
