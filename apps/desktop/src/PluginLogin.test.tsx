// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  pluginLogin: vi.fn(),
  openAuthorizationURL: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import { PluginLoginField } from "./PluginLogin";

let container: HTMLDivElement;
let root: Root;
let removeAnimations: () => void;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  removeAnimations = installDialogAnimations();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  bridgeMocks.openAuthorizationURL.mockResolvedValue(undefined);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  removeAnimations();
  vi.clearAllMocks();
});

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement))
    throw new Error(`Missing button: ${label}`);
  return match;
}

describe("PluginLoginField", () => {
  it("walks the plugin's login steps and shows the account it logged in", async () => {
    let loggedIn = false;
    bridgeMocks.pluginLogin.mockImplementation(
      async (
        _key: string,
        input: { action: string; fields?: Record<string, string> },
      ) => {
        const credential = loggedIn
          ? { logged_in: true, label: "Test account" }
          : { logged_in: false };
        switch (input.action) {
          case "start":
            return {
              credential,
              login: {
                id: "l1",
                step: "fields",
                fields: [
                  {
                    name: "token",
                    label: "令牌",
                    type: "password",
                    required: true,
                  },
                ],
              },
            };
          case "submit":
            loggedIn = true;
            return {
              credential: { logged_in: true, label: "Test account" },
              login: { id: "l1", step: "done", label: "Test account" },
            };
          default:
            return { credential };
        }
      },
    );
    await act(async () => {
      root.render(
        <PluginLoginField pluginKey="vendor" serviceId="service_vendor" />,
      );
    });
    const field = document.querySelector(
      '[data-testid="plugin-login-field"]',
    ) as HTMLElement;
    expect(field.textContent).toContain("未登录");
    await act(async () => button("登录").click());
    const dialog = document.querySelector(
      '[data-testid="plugin-login-dialog"]',
    ) as HTMLElement;
    const input = dialog.querySelector("input") as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      setter?.call(input, "secret-token");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => button("继续").click());
    await finishExitAnimations();
    expect(bridgeMocks.pluginLogin).toHaveBeenCalledWith("vendor", {
      service_id: "service_vendor",
      action: "submit",
      fields: { token: "secret-token" },
    });
    expect(
      document.querySelector('[data-testid="plugin-login-dialog"]'),
    ).toBeNull();
    expect(field.textContent).toContain("Test account");
    expect(field.textContent).toContain("重新登录");
  });
});
