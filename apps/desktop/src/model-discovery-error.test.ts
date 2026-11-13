import { afterEach, describe, expect, it } from "vitest";

import { applyLocale } from "./i18n";
import { modelDiscoveryFailureReason } from "./model-discovery-error";
import { ConsoleError } from "./web-transport";

/** A failed draft probe as the desktop host reports it. */
function hostError(
  status: string,
  code: string,
  message: string,
  reason?: string,
): Error {
  const details = reason ? `,"details":[{"reason":"${reason}"}]` : "";
  return new Error(
    `POST /control/v1/service-model-probes returned ${status}: {"error":{"code":"${code}","message":"${message}","retryable":false${details}},"request_id":"req_probe"}`,
  );
}

function failed(httpStatus: number | null, reason?: string): Error {
  return hostError(
    "502 Bad Gateway",
    "upstream_model_discovery_failed",
    httpStatus === null
      ? "upstream model discovery failed"
      : `upstream model discovery failed (HTTP ${httpStatus})`,
    reason,
  );
}

afterEach(async () => {
  await applyLocale("zh-CN");
});

describe("modelDiscoveryFailureReason", () => {
  it.each([
    [
      "upstream_unauthorized",
      401,
      "上游拒绝了 API Key（HTTP 401）。请检查 Key 是否正确、有没有权限。",
    ],
    [
      "upstream_not_found",
      404,
      "这个地址没有模型列表接口（HTTP 404）。请检查 API 地址。",
    ],
    [
      "upstream_rate_limited",
      429,
      "上游提示请求过多（HTTP 429），请稍后重试。",
    ],
    [
      "upstream_unavailable",
      503,
      "上游服务暂时不可用（HTTP 503），请稍后重试。",
    ],
    ["upstream_status", 400, "上游返回了错误（HTTP 400）。"],
    ["upstream_unreachable", null, "连不上上游。请检查 API 地址、网络和代理。"],
    [
      "upstream_invalid_response",
      null,
      "这个地址有响应，但返回的不是模型列表。请检查 API 地址。",
    ],
  ] as const)("names %s from a desktop host error", (reason, status, text) => {
    expect(modelDiscoveryFailureReason(failed(status, reason))).toBe(text);
  });

  it("reads the upstream status when an older Core sends no details", () => {
    expect(modelDiscoveryFailureReason(failed(403))).toBe(
      "上游拒绝了 API Key（HTTP 403）。请检查 Key 是否正确、有没有权限。",
    );
    expect(modelDiscoveryFailureReason(failed(405))).toBe(
      "这个地址没有模型列表接口（HTTP 405）。请检查 API 地址。",
    );
    expect(modelDiscoveryFailureReason(failed(502))).toBe(
      "上游服务暂时不可用（HTTP 502），请稍后重试。",
    );
    expect(modelDiscoveryFailureReason(failed(null))).toBe(
      "请检查 API 地址、API Key 和网络后重试。",
    );
  });

  it("names a timeout, an unsupported address and an unreadable key by code", () => {
    expect(
      modelDiscoveryFailureReason(
        hostError(
          "504 Gateway Timeout",
          "upstream_model_discovery_timeout",
          "upstream model discovery timed out",
        ),
      ),
    ).toBe("上游没有及时响应。请稍后重试，或检查网络和代理。");
    expect(
      modelDiscoveryFailureReason(
        hostError(
          "422 Unprocessable Entity",
          "model_discovery_unsupported",
          "service does not support this model discovery protocol",
        ),
      ),
    ).toBe("这个地址或提供商不支持获取模型列表，请手动填写模型。");
    expect(
      modelDiscoveryFailureReason(
        hostError(
          "409 Conflict",
          "credential_unavailable",
          "service credential is unavailable",
        ),
      ),
    ).toBe("读不到 API Key，请重新填写。");
  });

  it("reads the code and reason of a web console error", () => {
    expect(
      modelDiscoveryFailureReason(
        new ConsoleError(
          502,
          "upstream_model_discovery_failed",
          0,
          "upstream_unauthorized",
        ),
      ),
    ).toBe("上游拒绝了 API Key。请检查 Key 是否正确、有没有权限。");
    expect(
      modelDiscoveryFailureReason(
        new ConsoleError(504, "upstream_model_discovery_timeout", 0),
      ),
    ).toBe("上游没有及时响应。请稍后重试，或检查网络和代理。");
    expect(
      modelDiscoveryFailureReason(
        new ConsoleError(502, "upstream_model_discovery_failed", 0),
      ),
    ).toBe("请检查 API 地址、API Key 和网络后重试。");
  });

  it("never shows raw Control API JSON and keeps other messages", () => {
    const other = hostError("500 Internal Server Error", "internal", "boom");
    expect(modelDiscoveryFailureReason(other)).toBe(
      "请检查 API 地址、API Key 和网络后重试。",
    );
    expect(modelDiscoveryFailureReason(new Error("网关还没有启动"))).toBe(
      "网关还没有启动",
    );
    expect(modelDiscoveryFailureReason("core did not answer")).toBe(
      "core did not answer",
    );
    expect(modelDiscoveryFailureReason(undefined)).toBe(
      "请检查 API 地址、API Key 和网络后重试。",
    );
  });

  it("follows the operator's language", async () => {
    await applyLocale("en");
    expect(
      modelDiscoveryFailureReason(failed(401, "upstream_unauthorized")),
    ).toBe(
      "Upstream rejected the API key (HTTP 401). Check that the key is correct and has permission.",
    );
    await applyLocale("ja");
    expect(modelDiscoveryFailureReason(failed(404, "upstream_not_found"))).toBe(
      "このアドレスには、モデル一覧のエンドポイントがありません（HTTP 404）。API アドレスを確認してください。",
    );
  });
});
