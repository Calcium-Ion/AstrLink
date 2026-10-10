import { describe, expect, it } from "vitest";

import {
  parseAgentInstallReceipt,
  parseAgentInstallStatus,
} from "./agent-install-model";

const status = {
  cli_binary: true,
  tools: [
    {
      id: "codex",
      wsl: null,
      detected: true,
      skills: [
        {
          id: "astrlink-debug",
          installed: true,
          preview_paths: [
            "/tmp/.agents/skills/astrlink-debug",
            "/tmp/.astrlink/bin/astrlink",
            "/tmp/.codex/rules/astrlink.rules",
            "/tmp/.codex/AGENTS.md",
          ],
        },
        {
          id: "redaction-placeholders",
          installed: false,
          preview_paths: ["/tmp/.agents/skills/redaction-placeholders"],
        },
      ],
      cli_access: "exec_policy",
      cli_access_installed: false,
      guard: "instructions",
      guard_installed: false,
    },
  ],
  shared_paths: ["/tmp/.astrlink/agent-installs.json"],
  wsl_unchecked: [],
};

const receipt = {
  version: 2,
  skills: [
    { id: "astrlink-debug", version: "0.3.0" },
    { id: "redaction-placeholders", version: "0.1.0" },
  ],
  installed_at_unix: 1,
  cli_binary: "/tmp/.astrlink/bin/astrlink",
  files: ["/tmp/a"],
  wsl: [],
};

describe("agent-install-model", () => {
  it("parses a status snapshot", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.id).toBe("codex");
  });

  it("rejects unexpected fields", () => {
    expect(() => parseAgentInstallStatus({ ...status, extra: true })).toThrow(
      /unexpected field/,
    );
  });

  it("validates shared and per-skill installation paths", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.skills).toEqual(
      status.tools[0].skills,
    );
    expect(() =>
      parseAgentInstallStatus({ ...status, shared_paths: [null] }),
    ).toThrow(/shared_paths/);
    const [debug, placeholder] = status.tools[0].skills;
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [
          {
            ...status.tools[0],
            skills: [{ ...debug, preview_paths: "not an array" }, placeholder],
          },
        ],
      }),
    ).toThrow(/skills\[0\]\.preview_paths/);
  });

  it("validates the per-skill status", () => {
    const [debug, placeholder] = status.tools[0].skills;
    const withSkills = (skills: unknown[]) => ({
      ...status,
      tools: [{ ...status.tools[0], skills }],
    });
    expect(() =>
      parseAgentInstallStatus(
        withSkills([debug, { ...placeholder, id: "other-skill" }]),
      ),
    ).toThrow(/skills\[1\]\.id/);
    expect(() =>
      parseAgentInstallStatus(
        withSkills([{ ...debug, installed: "yes" }, placeholder]),
      ),
    ).toThrow(/skills\[0\]\.installed/);
    // A status from the single-skill installer is rejected, not half-read.
    const { skills: _skills, ...current } = status.tools[0];
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...current, skill_installed: true }],
      }),
    ).toThrow(/unexpected field/);
    expect(() =>
      parseAgentInstallStatus({ ...status, canonical_skill: true }),
    ).toThrow(/unexpected field/);
  });

  it("accepts Pi, which runs commands without asking", () => {
    const pi = {
      ...status.tools[0],
      id: "pi",
      cli_access: "unrestricted",
      guard: "skill_only",
    };
    expect(
      parseAgentInstallStatus({ ...status, tools: [pi] }).tools[0],
    ).toMatchObject({ id: "pi", cli_access: "unrestricted" });
  });

  it("validates the CLI access fields", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.cli_access).toBe(
      "exec_policy",
    );
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...status.tools[0], cli_access: "mcp" }],
      }),
    ).toThrow(/cli_access/);
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...status.tools[0], cli_access_installed: 1 }],
      }),
    ).toThrow(/cli_access_installed/);
    // A status from the MCP-based installer is rejected, not half-read.
    const {
      cli_access: _access,
      cli_access_installed: _installed,
      ...current
    } = status.tools[0];
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...current, mcp_installed: true }],
      }),
    ).toThrow(/unexpected field/);
  });

  it("validates the host guard fields", () => {
    expect(parseAgentInstallStatus(status).tools[0]?.guard).toBe(
      "instructions",
    );
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...status.tools[0], guard: "sandbox" }],
      }),
    ).toThrow(/guard/);
    expect(() =>
      parseAgentInstallStatus({
        ...status,
        tools: [{ ...status.tools[0], guard_installed: "yes" }],
      }),
    ).toThrow(/guard_installed/);
    const { guard: _guard, ...legacy } = status.tools[0];
    expect(() =>
      parseAgentInstallStatus({ ...status, tools: [legacy] }),
    ).toThrow(/missing field/);
  });

  it("parses an install receipt", () => {
    expect(parseAgentInstallReceipt(receipt).skills).toEqual(receipt.skills);
  });

  it("parses a skill-only receipt without a CLI", () => {
    const skillOnly = parseAgentInstallReceipt({
      ...receipt,
      skills: [{ id: "redaction-placeholders", version: "0.1.0" }],
      cli_binary: null,
    });
    expect(skillOnly.cli_binary).toBeNull();
    expect(() =>
      parseAgentInstallReceipt({ ...receipt, cli_binary: "" }),
    ).toThrow(/cli_binary/);
  });

  it("rejects receipt skills it does not know", () => {
    expect(() =>
      parseAgentInstallReceipt({
        ...receipt,
        skills: [{ id: "other-skill", version: "1.0.0" }],
      }),
    ).toThrow(/skills\[0\]\.id/);
    expect(() =>
      parseAgentInstallReceipt({
        ...receipt,
        skills: [{ id: "astrlink-debug" }],
      }),
    ).toThrow(/missing field/);
    // The single-bundle receipt shape is not read.
    const { skills: _skills, ...rest } = receipt;
    expect(() =>
      parseAgentInstallReceipt({
        ...rest,
        bundle: "astrlink-debug",
        bundle_version: "0.3.0",
      }),
    ).toThrow(/unexpected field/);
  });
});
