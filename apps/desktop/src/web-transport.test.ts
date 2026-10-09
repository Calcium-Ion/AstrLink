// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  CONSOLE_SIGNED_OUT,
  ConsoleError,
  consoleRequest,
  webInvoke,
} from "./web-transport";
const fetchMock = vi.fn();
function respond(
  value: unknown,
  status = 200,
  headers: Record<string, string> = {},
) {
  fetchMock.mockResolvedValueOnce(
    new Response(status === 204 ? null : JSON.stringify(value), {
      status,
      headers,
    }),
  );
}
beforeEach(() => vi.stubGlobal("fetch", fetchMock));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  fetchMock.mockReset();
});
describe("web command transport", () => {
  it("merges every service page and detects repeated cursors", async () => {
    respond({ items: [{ id: "svc_a" }], next_cursor: "cursor&1" });
    respond({ items: [{ id: "svc_b" }], next_cursor: null });
    expect(await webInvoke("list_services")).toEqual({
      items: [{ id: "svc_a" }, { id: "svc_b" }],
      next_cursor: null,
    });
    expect(fetchMock.mock.calls[1][0]).toBe(
      "/control/v1/services?limit=200&cursor=cursor%261",
    );
    respond({ items: [], next_cursor: "loop" });
    respond({ items: [], next_cursor: "loop" });
    await expect(webInvoke("list_services")).rejects.toThrow("pagination");
  });
  it.each([
    ["get_service", { serviceId: "svc_one" }, "/services/svc_one", "service"],
    ["get_privacy_policy", {}, "/policies/policy_privacy_default", "policy"],
    [
      "logout_service",
      { serviceId: "svc_one" },
      "/services/svc_one/logout",
      "service",
    ],
    [
      "clear_service_risk",
      { serviceId: "svc_one" },
      "/services/svc_one/risk/clear",
      "service",
    ],
  ])("wraps %s with the HTTP ETag", async (command, args, path, wrapper) => {
    respond({ id: "one" }, 200, { ETag: '"v2"' });
    expect(
      await webInvoke(command as string, args as Record<string, unknown>),
    ).toEqual({ [wrapper as string]: { id: "one" }, etag: '"v2"' });
    expect(fetchMock.mock.calls[0][0]).toBe(`/control/v1${path}`);
  });
  it("sends same-origin cookies and the console header with optimistic writes", async () => {
    respond({ id: "svc_one" }, 200, { ETag: '"v3"' });
    await webInvoke("update_service", {
      serviceId: "svc_one",
      etag: '"v2"',
      patch: { name: "new" },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/control/v1/services/svc_one",
      expect.objectContaining({
        method: "PATCH",
        credentials: "same-origin",
        redirect: "error",
        headers: {
          "Content-Type": "application/merge-patch+json",
          "If-Match": '"v2"',
          "X-AstrLink-Console": "1",
        },
        body: '{"name":"new"}',
      }),
    );
  });
  it("keeps repeated token query parameters and encodes cursors", async () => {
    respond({ items: [] });
    await webInvoke("list_request_sessions", {
      query: {
        local_access_token_ids: ["token_a", "token_b"],
        cursor: "x+y",
        kind: "agent",
      },
    });
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/control/v1/request-sessions?local_access_token_id=token_a&local_access_token_id=token_b&cursor=x%2By&kind=agent",
    );
  });
  it.each(["POST", "PUT", "PATCH", "DELETE"] as const)(
    "protects %s requests including login and empty bodies",
    async (method) => {
      respond(null, 204);
      await consoleRequest("/console/v1/login", method);
      expect(fetchMock.mock.calls[0][1].headers["X-AstrLink-Console"]).toBe(
        "1",
      );
    },
  );
  it("signals session expiry from all endpoints", async () => {
    const listener = vi.fn();
    window.addEventListener(CONSOLE_SIGNED_OUT, listener);
    respond({ error: { code: "login_required" } }, 401);
    await expect(webInvoke("get_audit_settings")).rejects.toBeInstanceOf(
      ConsoleError,
    );
    expect(listener).toHaveBeenCalledOnce();
    window.removeEventListener(CONSOLE_SIGNED_OUT, listener);
  });
  it("adapts raw proofs and refusals without retrying or signing out", async () => {
    respond({ error: { code: "raw_password_invalid" } }, 403);
    expect(
      await webInvoke("unlock_raw", {
        proof: { kind: "password", password: "wrong-test" },
      }),
    ).toEqual({ outcome: "password_invalid" });
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
      proof: { password: "wrong-test" },
    });
    respond(
      {
        error: {
          code: "raw_password_backoff",
          details: [{ retry_after_seconds: 5 }],
        },
      },
      429,
    );
    expect(
      await webInvoke("unlock_raw", {
        proof: { kind: "password", password: "wrong-test" },
      }),
    ).toEqual({ outcome: "backoff", retry_after_seconds: 5 });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
  it("keeps raw content locked on login and wraps successful unlock", async () => {
    respond({ signed_in: true });
    await consoleRequest("/console/v1/login", "POST", {
      password: "test-password",
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    respond({ unlocked: true });
    expect(
      await webInvoke("unlock_raw", {
        proof: { kind: "password", password: "test-password" },
      }),
    ).toEqual({ outcome: "sealing", status: { unlocked: true } });
  });
  it("copies the revealed token and returns the host boolean shape", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    respond({ access_token: "test-token" });
    expect(await webInvoke("copy_access_token", { tokenId: "token_one" })).toBe(
      true,
    );
    expect(writeText).toHaveBeenCalledWith("test-token");
  });
  it("wraps authorization and opens the device verification URL", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const session = {
      flow: "device_code",
      device_code: { verification_url: "https://example.com/device" },
    };
    respond(session, 202);
    expect(
      await webInvoke("begin_service_authorization", {
        serviceId: "svc_one",
        flow: "browser",
      }),
    ).toEqual({ kind: "session", session });
    expect(open).toHaveBeenCalledWith(
      "https://example.com/device",
      "_blank",
      "noopener,noreferrer",
    );
  });
  it.each([
    [
      "pricing",
      { operation: "configure", serviceId: "svc_one", input: {} },
      "/pricing/services/svc_one",
      "PUT",
    ],
    [
      "pricing",
      { operation: "backfill", serviceId: "svc_one" },
      "/pricing/services/svc_one/backfill",
      "POST",
    ],
    [
      "builtin_tool_action",
      { kind: "web_search", action: "delete_key" },
      "/builtin-tools/web_search/credential",
      "DELETE",
    ],
    [
      "list_service_risk_events",
      { serviceId: "svc_one", limit: 10 },
      "/services/svc_one/risk-events?limit=10",
      "GET",
    ],
    [
      "resume_privacy_model_installation",
      { installationId: "model_one" },
      "/privacy-models/model_one/resume",
      "POST",
    ],
  ])("routes %s", async (command, args, path, method) => {
    respond({});
    await webInvoke(command as string, args as Record<string, unknown>);
    expect(fetchMock).toHaveBeenCalledWith(
      `/control/v1${path}`,
      expect.objectContaining({ method }),
    );
  });
  it("rejects native commands, password reset, unsafe URLs and resource paths before fetching", async () => {
    for (const [command, args] of [
      ["restart_core", {}],
      ["set_raw_password", { action: "reset" }],
      ["get_service", { serviceId: "../health" }],
      ["open_external_url", { url: "javascript:alert(1)" }],
      ["toString", {}],
    ] as const) {
      await expect(webInvoke(command, args)).rejects.toThrow();
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
