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
      preview_paths: ["/tmp/.cursor/skills/astrlink-debug", "/tmp/.cursor/mcp.json"],
    },
  ],
  shared_paths: ["/tmp/astrlink-mcp"],
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

  it("validates shared and per-tool installation paths", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.preview_paths).toEqual(status.tools[0].preview_paths);
    expect(() => parseAgentInstallStatus({ ...status, shared_paths: [null] })).toThrow(/shared_paths/);
    expect(() => parseAgentInstallStatus({
      ...status, tools: [{ ...status.tools[0], preview_paths: "not an array" }],
    })).toThrow(/preview_paths/);
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
