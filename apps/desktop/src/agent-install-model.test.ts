import { describe, expect, it } from "vitest";

import {
  parseAgentInstallReceipt,
  parseAgentInstallStatus,
} from "./agent-install-model";

const status = {
  canonical_skill: true,
  mcp_binary: true,
  mcp_command: "/tmp/astrlink-mcp",
  tools: [
    {
      id: "cursor",
      detected: true,
      skill_installed: true,
      mcp_installed: false,
    },
  ],
  preview_paths: ["/tmp/.agents/skills/astrlink-debug"],
};

describe("agent-install-model", () => {
  it("parses a status snapshot", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.id).toBe("cursor");
  });

  it("rejects unexpected fields", () => {
    expect(() =>
      parseAgentInstallStatus({ ...status, extra: true }),
    ).toThrow(/unexpected field/);
  });

  it("parses an install receipt", () => {
    const receipt = parseAgentInstallReceipt({
      version: 1,
      bundle: "astrlink-debug",
      bundle_version: "0.1.0",
      installed_at_unix: 1,
      mcp_binary: "/tmp/astrlink-mcp",
      files: ["/tmp/a"],
    });
    expect(receipt.bundle).toBe("astrlink-debug");
  });
});
