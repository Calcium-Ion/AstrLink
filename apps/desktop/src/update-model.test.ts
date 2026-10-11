import { describe, expect, it } from "vitest";
import {
  browserUpdateSnapshot,
  parseUpdatePreferences,
  parseUpdateSnapshot,
  updateBusy,
  updateNotice,
  updateRetryWait,
} from "./update-model";

describe("update IPC", () => {
  it("accepts browser snapshots and optional download lengths", () => {
    const value = browserUpdateSnapshot();
    expect(parseUpdateSnapshot(value)).toEqual(value);
    expect(updateBusy({ ...value, phase: "downloading" })).toBe(true);
    expect(updateBusy({ ...value, phase: "ready" })).toBe(false);
  });
  it("rejects malformed states, preferences and external release URLs", () => {
    for (const patch of [
      { phase: "done" },
      { downloaded_bytes: -1 },
      { total_bytes: "100" },
      { revision: NaN },
      { configured: 1 },
      { error_code: undefined },
      { latest_version: 123 },
      { retry_at: undefined },
      { retry_at: 60 },
    ]) {
      expect(() =>
        parseUpdateSnapshot({ ...browserUpdateSnapshot(), ...patch }),
      ).toThrow();
    }
    expect(() =>
      parseUpdatePreferences({
        auto_check: true,
        auto_download: true,
        channel: "nightly",
        official_mirror: false,
      }),
    ).toThrow();
    expect(() =>
      parseUpdatePreferences({
        auto_check: true,
        auto_download: true,
        channel: "stable",
        official_mirror: "yes",
      }),
    ).toThrow();
    expect(() =>
      parseUpdateSnapshot({
        ...browserUpdateSnapshot(),
        release: {
          version: "2.0.0",
          notes: "",
          published_at: null,
          url: "https://example.com/installer",
        },
      }),
    ).toThrow();
  });
  it("keeps the checked latest version even when no installation is needed", () => {
    const value = {
      ...browserUpdateSnapshot(),
      phase: "up_to_date" as const,
      current_version: "1.2.0",
      latest_version: "1.1.0",
    };
    expect(parseUpdateSnapshot(value).latest_version).toBe("1.1.0");
    expect(
      parseUpdateSnapshot({ ...value, latest_version: undefined })
        .latest_version,
    ).toBeNull();
  });
  it("only announces update states that wait on the operator", () => {
    const release = {
      version: "1.1.0",
      notes: "",
      published_at: null,
      url: "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0",
    };
    const base = { ...browserUpdateSnapshot(), release };
    const manualDownload = {
      ...base.preferences,
      auto_download: false,
    };
    expect(updateNotice({ ...base, phase: "available" })).toBeNull();
    expect(
      updateNotice({
        ...base,
        phase: "available",
        preferences: manualDownload,
      }),
    ).toBe("available");
    expect(updateNotice({ ...base, phase: "manual" })).toBe("manual");
    expect(updateNotice({ ...base, phase: "ready" })).toBe("ready");
    expect(updateNotice({ ...base, phase: "downloading" })).toBeNull();
    expect(updateNotice({ ...base, phase: "ready", release: null })).toBeNull();
  });
  it("counts whole seconds until the host accepts another check", () => {
    const now = Date.parse("2026-10-07T04:00:00Z");
    const at = (retry_at: string | null) =>
      updateRetryWait({ ...browserUpdateSnapshot(), retry_at }, now);
    expect(at(null)).toBe(0);
    expect(at("invalid")).toBe(0);
    expect(at("2026-10-07T03:59:59Z")).toBe(0);
    expect(at("2026-10-07T04:00:00.200Z")).toBe(1);
    expect(at("2026-10-07T04:12:17Z")).toBe(737);
  });
});
