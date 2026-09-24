import { beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getCoreStatus: vi.fn(),
  getPreferences: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  getRoutingSettings: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import { loadExportEnvironment } from "./export-environment";

describe("loadExportEnvironment", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    const offline = () => Promise.reject(new Error("offline"));
    bridge.getCoreStatus.mockImplementation(offline);
    bridge.getPreferences.mockImplementation(offline);
    bridge.getPrivacyPolicy.mockImplementation(offline);
    bridge.listPrivacyModelInstallations.mockImplementation(offline);
  });

  it("keeps every model redirect rule with its enabled flag", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      strategy: "priority",
      max_attempts: 3,
      identity_enforcement: true,
      model_redirects: [
        { from: "gpt-5.5", to: "claude-opus-4-1", enabled: true },
        { from: "o3", to: "gpt-5.5-pro", enabled: false },
      ],
    });

    const environment = await loadExportEnvironment();

    expect(environment.routing).toStrictEqual({
      strategy: "priority",
      max_attempts: 3,
      model_redirects: [
        { from: "gpt-5.5", to: "claude-opus-4-1", enabled: true },
        { from: "o3", to: "gpt-5.5-pro", enabled: false },
      ],
    });
    // Sections that failed to load stay null without sinking the export.
    expect(environment.version).toBeNull();
    expect(environment.privacy).toBeNull();
    expect(environment.limits).toBeNull();
  });

  it("reports no rules when settings predate model redirects", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      strategy: "priority",
      max_attempts: 2,
    });

    const environment = await loadExportEnvironment();

    expect(environment.routing).toStrictEqual({
      strategy: "priority",
      max_attempts: 2,
      model_redirects: [],
    });
  });

  it("leaves routing unavailable when the settings cannot be read", async () => {
    bridge.getRoutingSettings.mockRejectedValue(new Error("offline"));

    const environment = await loadExportEnvironment();

    expect(environment.routing).toBeNull();
  });
});
