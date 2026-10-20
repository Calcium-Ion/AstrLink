import { afterEach, describe, expect, it } from "vitest";

import { controlErrorCode, controlErrorMessage } from "./control-error";
import { applyLocale } from "./i18n";
import { ConsoleError } from "./web-transport";

function hostError(code: string, message = "x"): Error {
  return new Error(
    `PATCH /control/v1/routing-settings returned 422 Unprocessable Entity: {"error":{"code":"${code}","message":"${message}","retryable":false,"details":[]},"request_id":"req_0"}`,
  );
}

afterEach(async () => {
  await applyLocale("zh-CN");
});

describe("controlErrorCode", () => {
  it("reads the code from a desktop host error and from the web console", () => {
    expect(
      controlErrorCode(
        new Error(
          'POST /control/v1/audit/raw-unlock returned 409 Conflict: {"error":{"code":"raw_access_unavailable","message":"x"}}',
        ),
      ),
    ).toBe("raw_access_unavailable");
    expect(
      controlErrorCode(new ConsoleError(422, "image_provider_unsupported", 0)),
    ).toBe("image_provider_unsupported");
    expect(controlErrorCode(new ConsoleError(502, "HTTP 502", 0))).toBeNull();
    expect(controlErrorCode("core did not answer")).toBeNull();
  });
});

describe("controlErrorMessage", () => {
  it("words a known code in the operator's language on either transport", async () => {
    const zh =
      "图片生成选用的 API 提供商已不存在。请换一个提供商，或关闭图片生成。";
    expect(
      controlErrorMessage(hostError("image_provider_not_found"), "fallback"),
    ).toBe(zh);
    expect(
      controlErrorMessage(
        new ConsoleError(422, "image_provider_not_found", 0),
        "fallback",
      ),
    ).toBe(zh);

    await applyLocale("en");
    expect(
      controlErrorMessage(hostError("image_provider_unsupported"), "fallback"),
    ).toMatch(
      /^The API provider chosen for image generation has no Images API/,
    );
  });

  it("says which change a routing group blocks on a service", () => {
    expect(
      controlErrorMessage(
        new Error(
          'DELETE /control/v1/services/service_x returned 409 Conflict: {"error":{"code":"model_routing_reference","message":"a model routing group depends on the service: group \\"coding\\" member \\"fast\\" service \\"service_x\\" is unavailable"}}',
        ),
        "fallback",
      ),
    ).toBe(
      "智能路由的路由组正在使用这个 API 提供商的模型。请先在「智能路由 › 路由组」里换掉用到它的成员，再删除或停用它、或移除对应的模型。",
    );
  });

  it("keeps the message of a code without a translation", () => {
    const error = hostError(
      "invalid_routing_settings",
      "max_attempts: too many",
    );
    expect(controlErrorMessage(error, "fallback")).toBe(error.message);
    expect(controlErrorMessage("not an error", "fallback")).toBe("fallback");
    expect(controlErrorMessage(new Error(""), "fallback")).toBe("fallback");
  });
});
