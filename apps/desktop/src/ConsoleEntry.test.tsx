// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConsoleEntry, type ConsoleStatus } from "./ConsoleEntry";
import { CONSOLE_SIGNED_OUT } from "./web-transport";
import { applyLocale } from "./i18n";
const status: ConsoleStatus = {
  status: "setup_required",
  signed_in: false,
  setup_seconds_left: 60,
  password_reset: false,
  reset_variable: "ASTRLINK_RESET_PASSWORD",
  password_min_length: 8,
  password_max_length: 12,
};
let root: Root, container: HTMLDivElement;
const fetchMock = vi.fn();
const response = (value: unknown, code = 200, headers = {}) =>
  new Response(JSON.stringify(value), { status: code, headers });
beforeEach(async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  await applyLocale("en");
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  fetchMock.mockReset();
});
async function render(value: ConsoleStatus = status) {
  fetchMock.mockImplementation(async () => response(value));
  await act(async () =>
    root.render(
      <ConsoleEntry>
        <div data-testid="app">Console app</div>
      </ConsoleEntry>,
    ),
  );
}
async function fill(name: string, value: string) {
  const input = container.querySelector<HTMLInputElement>(
    `input[name="${name}"]`,
  )!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
it("uses status password bounds and requires matching setup confirmation", async () => {
  await render();
  expect(container.textContent).toContain("8 to 12");
  expect(container.textContent).toContain("1:00");
  await fill("password", "1234567");
  await fill("confirmation", "1234567");
  expect(
    container.querySelector<HTMLButtonElement>('button[type="submit"]')!
      .disabled,
  ).toBe(true);
  await fill("password", "12345678");
  await fill("confirmation", "12345678");
  expect(
    container.querySelector<HTMLButtonElement>('button[type="submit"]')!
      .disabled,
  ).toBe(false);
  fetchMock.mockImplementation(async () =>
    response({ ...status, status: "login_required", signed_in: true }),
  );
  await act(async () =>
    container
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/console/v1/setup",
    expect.objectContaining({
      body: '{"password":"12345678"}',
      headers: expect.objectContaining({ "X-AstrLink-Console": "1" }),
    }),
  );
  expect(container.querySelector('[data-testid="app"]')).not.toBeNull();
});
it("shows expiry and reset instructions in both languages", async () => {
  await render({ ...status, status: "setup_expired", password_reset: true });
  expect(container.textContent).toContain("Restart the container");
  expect(container.textContent).toContain("ASTRLINK_RESET_PASSWORD");
  expect(container.querySelector("form")).toBeNull();
  await act(async () => {
    await applyLocale("zh-CN");
  });
  expect(container.textContent).toContain("重启容器");
});
it("honors Retry-After and disables login until the wait ends", async () => {
  await render({ ...status, status: "login_required" });
  await fill("password", "wrong-password");
  fetchMock.mockResolvedValueOnce(
    response({ error: { code: "raw_password_backoff" } }, 429, {
      "Retry-After": "7",
    }),
  );
  await act(async () =>
    container
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })),
  );
  expect(container.textContent).toContain("7 seconds");
  expect(
    container.querySelector<HTMLButtonElement>('button[type="submit"]')!
      .disabled,
  ).toBe(true);
  expect(
    container.querySelector<HTMLInputElement>('input[name="password"]')!.value,
  ).toBe("");
});
it("unmounts authenticated content on session expiry", async () => {
  await render({ ...status, status: "login_required", signed_in: true });
  expect(container.querySelector('[data-testid="app"]')).not.toBeNull();
  fetchMock.mockImplementation(async () =>
    response({ ...status, status: "login_required" }),
  );
  await act(async () => window.dispatchEvent(new Event(CONSOLE_SIGNED_OUT)));
  expect(container.querySelector('[data-testid="app"]')).toBeNull();
  expect(container.textContent).toContain("Forgot your password?");
});

it("rechecks the server when the setup deadline expires", async () => {
  vi.useFakeTimers();
  await render({ ...status, setup_seconds_left: 1 });
  fetchMock.mockImplementation(async () =>
    response({ ...status, status: "setup_expired", setup_seconds_left: 0 }),
  );
  await act(async () => {
    vi.advanceTimersByTime(1100);
  });
  expect(container.textContent).toContain("Setup time has passed");
  expect(container.querySelector("form")).toBeNull();
});
