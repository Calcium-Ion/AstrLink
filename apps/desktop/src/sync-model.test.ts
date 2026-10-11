import { describe, expect, it } from "vitest";

import {
  defaultSyncConfig,
  maxSyncDeviceNameLength,
  parseSyncConfig,
  parseSyncPassphraseResult,
  parseSyncStatus,
  syncConfigIssue,
  syncConfigPayload,
  type SyncConfig,
} from "./sync-model";

/** The body Core returns from GET /control/v1/sync/config. */
const storedConfig = {
  enabled: true,
  trigger: {
    mode: "interval",
    interval_minutes: 30,
    debounce_seconds: 20,
  },
  scope: {
    services: true,
    credentials: false,
    policies: true,
    pricing: false,
  },
  remote: {
    kind: "webdav",
    endpoint: "https://dav.example.com/astrlink",
    username: "howie",
  },
  remote_password_set: true,
  remote_secret_key_set: false,
};

describe("sync configuration read model", () => {
  it("parses a stored configuration and fills the omitted fields", () => {
    expect(parseSyncConfig(storedConfig)).toEqual({
      enabled: true,
      device_name: "",
      trigger: {
        mode: "interval",
        interval_minutes: 30,
        cron: "",
        debounce_seconds: 20,
      },
      scope: {
        services: true,
        credentials: false,
        policies: true,
        pricing: false,
      },
      remote: {
        kind: "webdav",
        endpoint: "https://dav.example.com/astrlink",
        path: "",
        region: "",
        username: "howie",
        password: "",
        access_key: "",
        secret_key: "",
        addressing: "path",
      },
      remote_password_set: true,
      remote_secret_key_set: false,
    });
  });

  it("refuses a mode, remote kind, or count the interface cannot render", () => {
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        trigger: { ...storedConfig.trigger, mode: "hourly" },
      }),
    ).toThrow("unknown trigger");
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        remote: { ...storedConfig.remote, kind: "ftp" },
      }),
    ).toThrow("unknown remote");
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        remote: { ...storedConfig.remote, addressing: "host" },
      }),
    ).toThrow("unknown addressing");
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        trigger: { ...storedConfig.trigger, interval_minutes: 1.5 },
      }),
    ).toThrow("sync_config.trigger.interval_minutes");
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        trigger: { ...storedConfig.trigger, debounce_seconds: 7200 },
      }),
    ).toThrow("sync_config.trigger.debounce_seconds");
  });

  it("refuses a scope flag that is not a boolean", () => {
    expect(() =>
      parseSyncConfig({
        ...storedConfig,
        scope: { ...storedConfig.scope, services: "yes" },
      }),
    ).toThrow("sync_config.scope.services");
  });

  it("drops the write-only secret flags from a save", () => {
    const config = parseSyncConfig(storedConfig);
    const payload = syncConfigPayload(config) as Record<string, unknown>;
    expect(payload).not.toHaveProperty("remote_password_set");
    expect(payload).not.toHaveProperty("remote_secret_key_set");
    expect(payload).toEqual({
      enabled: true,
      device_name: "",
      trigger: config.trigger,
      scope: config.scope,
      remote: config.remote,
    });
  });

  it("accepts the default configuration it hands out", () => {
    expect(parseSyncConfig(defaultSyncConfig())).toEqual(defaultSyncConfig());
  });
});

describe("sync configuration validation", () => {
  function config(change: (value: SyncConfig) => void): SyncConfig {
    const value = defaultSyncConfig();
    change(value);
    return value;
  }

  it("accepts a complete remote", () => {
    expect(
      syncConfigIssue(
        config((value) => {
          value.enabled = true;
          value.remote.kind = "webdav";
          value.remote.endpoint = "https://dav.example.com/astrlink";
          value.remote.path = "astrlink";
        }),
      ),
    ).toBeNull();
  });

  it("names the field that stops the save", () => {
    // The interval is checked whether or not sync is on, matching ValidateSyncConfig.
    expect(
      syncConfigIssue(
        config((value) => {
          value.trigger.mode = "interval";
          value.trigger.interval_minutes = 1;
        }),
      ),
    ).toBe("interval");
    expect(
      syncConfigIssue(
        config((value) => {
          value.trigger.mode = "cron";
          value.trigger.cron = "   ";
        }),
      ),
    ).toBe("cron");
    expect(
      syncConfigIssue(
        config((value) => {
          value.trigger.debounce_seconds = 1;
        }),
      ),
    ).toBe("debounce");
    expect(
      syncConfigIssue(
        config((value) => {
          value.enabled = true;
          value.remote.kind = "webdav";
          value.remote.endpoint = "  ";
        }),
      ),
    ).toBe("webdav_endpoint");
    expect(
      syncConfigIssue(
        config((value) => {
          value.enabled = true;
          value.remote.kind = "s3";
          value.remote.endpoint = "https://s3.example.com";
          value.remote.path = "";
        }),
      ),
    ).toBe("s3_endpoint");
    expect(
      syncConfigIssue(
        config((value) => {
          value.enabled = true;
        }),
      ),
    ).toBe("remote_kind");
    expect(
      syncConfigIssue(
        config((value) => {
          value.device_name = "x".repeat(maxSyncDeviceNameLength + 1);
        }),
      ),
    ).toBe("device_name");
  });

  it("leaves debounce 0 to the engine's default", () => {
    expect(
      syncConfigIssue(
        config((value) => {
          value.trigger.debounce_seconds = 0;
        }),
      ),
    ).toBeNull();
  });
});

describe("sync status read model", () => {
  const status = {
    enabled: true,
    passphrase_set: true,
    remote_configured: true,
    configured: true,
    running: false,
    device_id: "device_0123456789abcdef01234567",
    device_name: "studio-mac",
    host_name: "studio-mac",
    pending_changes: 4,
    last_synced_at: "2026-10-11T03:00:00Z",
    conflicts: [],
  };

  it("parses a status without the optional fields", () => {
    expect(parseSyncStatus(status)).toEqual(status);
  });

  it("keeps the optional fields when the engine reports them", () => {
    expect(
      parseSyncStatus({
        ...status,
        next_run_at: "2026-10-11T04:00:00Z",
        last_error: "remote refused the lock",
      }),
    ).toEqual({
      ...status,
      next_run_at: "2026-10-11T04:00:00Z",
      last_error: "remote refused the lock",
    });
  });

  it("treats absent optional fields as absent, not empty", () => {
    const parsed = parseSyncStatus(status);
    expect(parsed).not.toHaveProperty("next_run_at");
    expect(parsed).not.toHaveProperty("last_error");
  });

  it("parses a merge notice", () => {
    expect(
      parseSyncStatus({
        ...status,
        conflicts: [
          {
            kind: "service",
            name: "Anthropic",
            resolution: "took this device",
            at: "2026-10-11T03:00:00Z",
          },
        ],
      }).conflicts,
    ).toEqual([
      {
        kind: "service",
        name: "Anthropic",
        resolution: "took this device",
        at: "2026-10-11T03:00:00Z",
      },
    ]);
  });

  it("refuses a status the interface cannot render", () => {
    expect(() => parseSyncStatus({ ...status, running: "no" })).toThrow(
      "sync_status.running",
    );
    expect(() =>
      parseSyncStatus({ ...status, last_synced_at: " yesterday" }),
    ).toThrow("sync_status.last_synced_at");
    expect(() =>
      parseSyncStatus({ ...status, conflicts: { kind: "service" } }),
    ).toThrow("sync_status.conflicts");
    expect(() =>
      parseSyncStatus({
        ...status,
        conflicts: [{ kind: "service", name: "Anthropic", resolution: "x" }],
      }),
    ).toThrow("sync_status.conflicts[0].at");
  });
});

describe("sync passphrase result", () => {
  it("reads the flag and refuses anything else", () => {
    expect(parseSyncPassphraseResult({ passphrase_set: true })).toEqual({
      passphrase_set: true,
    });
    expect(() => parseSyncPassphraseResult({ passphrase_set: "yes" })).toThrow(
      "sync_passphrase",
    );
    expect(() => parseSyncPassphraseResult(null)).toThrow("sync_passphrase");
  });
});
