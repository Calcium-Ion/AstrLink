import { describe, expect, it } from "vitest";

import { parseAuditSettings } from "./audit-settings-model";

const settings = {
  request_body_enabled: false,
  response_content_enabled: true,
  http_meta_enabled: true,
  request_body_max_bytes: 4096,
  response_content_max_bytes: 8192,
  metadata_retention_days: 30,
  content_retention_days: 7,
};

describe("audit-settings IPC contract", () => {
  it("parses settings and tolerates extensions", () => {
    expect(
      parseAuditSettings({
        ...settings,
        extensions: { extra: true },
      }),
    ).toEqual(settings);
  });

  it("rejects missing or mistyped fields", () => {
    expect(() =>
      parseAuditSettings({
        ...settings,
        request_body_enabled: "yes",
      }),
    ).toThrow("应为布尔值");
    const { content_retention_days: _days, ...missing } = settings;
    expect(() => parseAuditSettings(missing)).toThrow("应为整数");
  });
});
