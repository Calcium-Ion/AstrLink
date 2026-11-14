// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  defaultDesktopToys,
  defaultTrayPreferences,
  type DesktopToysPreferences,
} from "../preferences-model";
import { defaultUpdatePreferences } from "../update-model";

const bridge = vi.hoisted(() => ({
  getPreferences: vi.fn(),
  updateDesktopToys: vi.fn(),
}));
vi.mock("../bridge", () => bridge);

// The body draws on a canvas; here it only shows what it was given.
vi.mock("./DesktopToysSettings", () => ({
  DesktopToysSettings: ({
    value,
    disabled,
    onChange,
  }: {
    value: DesktopToysPreferences;
    disabled: boolean;
    onChange: (next: DesktopToysPreferences) => void;
  }) => (
    <button
      data-pet={String(value.on.pet)}
      disabled={disabled}
      onClick={() => onChange({ ...value, on: { ...value.on, pet: true } })}
      type="button"
    >
      pet
    </button>
  ),
}));

import { DesktopToysPage } from "./DesktopToysPage";

const snapshot = (toys = defaultDesktopToys()) => ({
  values: {
    close_behavior: "hide_to_tray" as const,
    autostart: false,
    autostart_hidden: false,
    core_auto_start: true,
    core_auto_recover: true,
    use_system_proxy: true,
    inference_port: 9000,
    inference_listen: "loopback" as const,
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    max_request_body_mib: 0,
    theme: "system" as const,
    quota_display_mode: "remaining" as const,
    display_currency: "auto" as const,
    locale: "zh-CN" as const,
    tray: defaultTrayPreferences(),
    updates: defaultUpdatePreferences(),
    desktop_toys: toys,
  },
  load_warning: null,
  autostart_actual: false,
  autostart_error: null,
  data_backups: [],
  local_key_storage: null,
});

describe("DesktopToysPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    bridge.getPreferences.mockReset().mockResolvedValue(snapshot());
    bridge.updateDesktopToys.mockReset();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  const render = () =>
    act(async () => {
      root.render(<DesktopToysPage />);
    });
  const toggle = () => container.querySelector("button")!;

  it("is its own page, named for the pets", async () => {
    await render();
    expect(container.querySelector("h1")?.textContent).toBe("桌宠");
    expect(container.textContent).not.toContain("小人");
    expect(toggle().dataset.pet).toBe("false");
  });

  it("saves a change on its own and keeps what the host saved", async () => {
    await render();
    const saved = {
      ...defaultDesktopToys(),
      on: { ...defaultDesktopToys().on, pet: true },
    };
    bridge.updateDesktopToys.mockResolvedValue(snapshot(saved));
    await act(async () => toggle().click());
    expect(bridge.updateDesktopToys).toHaveBeenCalledWith(saved);
    expect(toggle().dataset.pet).toBe("true");
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("puts the old value back and says why when saving fails", async () => {
    await render();
    bridge.updateDesktopToys.mockRejectedValue(new Error("disk full"));
    await act(async () => toggle().click());
    expect(toggle().dataset.pet).toBe("false");
    expect(container.textContent).toContain("disk full");
    expect(toggle().disabled).toBe(false);
  });
});
