import { afterEach, describe, expect, it } from "vitest";

import { parseAuditSettings } from "./audit-settings-model";
import { applyLocale } from "./i18n";
import { parseLocalDataStatus } from "./local-data-model";
import { parseRequestRecordPage } from "./request-record-model";
import { parseRawAccessList } from "./raw-access-model";
import { parseRawSealingStatus } from "./raw-sealing-model";

afterEach(async () => {
  await applyLocale("zh-CN");
});

const status = {
  raw_available: true,
  configured: true,
  password_set: true,
  password_required: false,
  envelopes: ["password"],
  key_verified: true,
  unlocked: false,
  unlock_expires_at: null,
  unlock_idle_seconds: 900,
  retry_after_seconds: 0,
  password_min_length: 8,
  password_max_length: 128,
};

describe("host data errors", () => {
  it("speaks the operator's language", async () => {
    await applyLocale("en");

    expect(() => parseRawSealingStatus({ ...status, configured: 1 })).toThrow(
      "Invalid raw sealing data ($.configured): expected a boolean",
    );
    expect(() =>
      parseRawSealingStatus({ ...status, unlock_idle_seconds: -1 }),
    ).toThrow(
      "Invalid raw sealing data ($.unlock_idle_seconds): expected an integer of at least 0",
    );
    expect(() => parseRawAccessList({ items: "none" })).toThrow(
      "Invalid raw access request data ($.items): expected an array",
    );
    expect(() => parseLocalDataStatus({ services: 1 })).toThrow(
      "Invalid local data status ($.services): unsupported field",
    );
    expect(() => parseRequestRecordPage({ items: {} })).toThrow(
      "Invalid request record data ($.items): expected an array",
    );
    expect(() => parseAuditSettings([])).toThrow(
      "Invalid audit settings data ($): expected an object",
    );

    await applyLocale("zh-CN");
    expect(() => parseRawSealingStatus({ ...status, configured: 1 })).toThrow(
      "原文封存数据无效（$.configured）：应为布尔值",
    );
    expect(() =>
      parseRawSealingStatus({ ...status, unlock_idle_seconds: -1 }),
    ).toThrow("原文封存数据无效（$.unlock_idle_seconds）：应为不小于 0 的整数");
    expect(() => parseLocalDataStatus({ services: 1 })).toThrow(
      "本地数据状态无效（$.services）：不支持的字段",
    );
    expect(() => parseRequestRecordPage({ items: {} })).toThrow(
      "请求记录数据无效（$.items）：应为数组",
    );
  });
});
