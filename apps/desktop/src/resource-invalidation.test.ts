import { afterEach, describe, expect, it, vi } from "vitest";
import {
  invalidateResource,
  isResourceStale,
  markResourceFetched,
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
});