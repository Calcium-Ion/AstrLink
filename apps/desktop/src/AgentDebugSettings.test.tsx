// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getAgentDebugStatus: vi.fn(),
  installAgentDebug: vi.fn(),
  uninstallAgentDebug: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { applyLocale } from "./i18n";
import { AgentDebugSettings } from "./AgentDebugSettings";

type SkillFlags = { debug?: boolean; placeholder?: boolean };

// Mirrors the Rust status: the debug skill previews the CLI and the host
// files it writes, the placeholder skill only its own directory.
function skills(
  root: string,
  { debug = false, placeholder = false }: SkillFlags,
  hostFiles: string[] = [],
) {
  return [
    {
      id: "astrlink-debug" as const,
      installed: debug,
      preview_paths: [
        `${root}/astrlink-debug`,
        "/tmp/.astrlink/bin/astrlink",
        ...hostFiles,
      ],
    },
    {
      id: "redaction-placeholders" as const,
      installed: placeholder,
      preview_paths: [`${root}/redaction-placeholders`],
    },
  ];
}

const status = {
  cli_binary: false,
  tools: [
    {
      id: "cursor" as const,
      wsl: null,
      detected: true,
      skills: skills("/tmp/.cursor/skills", {}),
      cli_access: "prompt" as const,
      cli_access_installed: false,
      guard: "skill_only" as const,
      guard_installed: false,
    },
    {
      id: "claude" as const,
      wsl: null,
      detected: false,
      skills: skills("/tmp/.claude/skills", {}, ["/tmp/.claude/settings.json"]),
      cli_access: "allow_rules" as const,
      cli_access_installed: false,
      guard: "deny_rules" as const,
      guard_installed: false,
    },
    {
      id: "codex" as const,
      wsl: null,
      detected: true,
      skills: skills("/tmp/.agents/skills", { debug: true }, [
        "/tmp/.codex/rules/astrlink.rules",
        "/tmp/.codex/AGENTS.md",
      ]),
      cli_access: "exec_policy" as const,
      cli_access_installed: true,
      guard: "instructions" as const,
      guard_installed: true,
    },
    {
      id: "grok" as const,
      wsl: null,
      detected: true,
      skills: skills("/tmp/.grok/skills", {}),
      cli_access: "prompt" as const,
      cli_access_installed: false,
      guard: "skill_only" as const,
      guard_installed: false,
    },
    {
      id: "pi" as const,
      wsl: null,
      detected: false,
      skills: skills("/tmp/.agents/skills", { debug: true }),
      cli_access: "unrestricted" as const,
      cli_access_installed: false,
      guard: "skill_only" as const,
      guard_installed: false,
    },
    {
      id: "dsh" as const,
      wsl: null,
      detected: false,
      skills: skills("/tmp/.agents/skills", {}),
      cli_access: "unrestricted" as const,
      cli_access_installed: false,
      guard: "skill_only" as const,
      guard_installed: false,
    },
  ],
  shared_paths: ["/tmp/.astrlink/agent-installs.json"],
  wsl_unchecked: [],
};

type ToolFixture = (typeof status.tools)[number];

/** Install targets on this computer, in table order. */
function local(...ids: string[]) {
  return ids.map((id) => ({ id, wsl: null }));
}

function withSkills(tool: ToolFixture, flags: SkillFlags): ToolFixture {
  return {
    ...tool,
    skills: tool.skills.map((skill) => ({
      ...skill,
      installed:
        (skill.id === "astrlink-debug" ? flags.debug : flags.placeholder) ??
        false,
    })),
  };
}

/** All six tools on this computer, only these detected, nothing installed. */
function detectedOnly(...ids: string[]) {
  return {
    ...status,
    cli_binary: false,
    tools: status.tools.map((tool) => ({
      ...withSkills(tool, {}),
      detected: ids.includes(tool.id),
      cli_access_installed: false,
      guard_installed: false,
    })),
  };
}

describe("AgentDebugSettings", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getAgentDebugStatus.mockReset().mockResolvedValue(status);
    bridge.installAgentDebug.mockReset().mockResolvedValue({
      version: 2,
      skills: [{ id: "astrlink-debug", version: "0.3.0" }],
      installed_at_unix: 1,
      cli_binary: "/tmp/.astrlink/bin/astrlink",
      files: status.shared_paths,
      wsl: [],
    });
    bridge.uninstallAgentDebug.mockReset().mockResolvedValue(undefined);
    notifyMocks.success.mockReset();
    notifyMocks.error.mockReset();
  });

  afterEach(async () => {
    await applyLocale("zh-CN");
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows detected tools and confirms install", async () => {
    await act(async () => {
      root.render(<AgentDebugSettings />);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(container.querySelector("h1")?.textContent).toBe("Agent 工具");
    expect(container.textContent).toContain("工具接入");
    expect(container.textContent).toContain("Cursor");
    expect(
      container.querySelector("[role='img'][aria-label='已检测到']"),
    ).not.toBeNull();
    expect(
      container.querySelector("[role='img'][aria-label='未检测到']"),
    ).not.toBeNull();
    expect(container.textContent).toContain("Codex");
    expect(
      [...container.querySelectorAll("tbody tr")]
        .find((row) => row.textContent?.includes("Codex"))
        ?.querySelectorAll("td")[1]?.textContent,
    ).toBe("缺少隐私脱敏部分安装");
    expect(container.textContent).toContain("Grok Build");
    expect(container.textContent).toContain("0 / 3");
    expect(container.textContent).toContain(
      "AstrLink 命令行工具缺失，请重新安装后再使用。",
    );

    const install = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "安装 / 更新",
    );
    if (!install) throw new Error("missing install button");
    await act(async () => {
      install.click();
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain(
      "/tmp/.agents/skills/astrlink-debug",
    );

    const dialog = document.querySelector("[role='alertdialog']");
    const confirm = dialog
      ? [...dialog.querySelectorAll("button")].find(
          (button) => button.textContent === "安装所选工具（3）",
        )
      : undefined;
    if (!confirm) throw new Error("missing confirm");
    await act(async () => {
      confirm.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(bridge.installAgentDebug).toHaveBeenCalledWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("cursor", "codex", "grok"),
    );
    expect(notifyMocks.success).toHaveBeenCalled();
  });

  it("identifies a missing component and refreshes after repair", async () => {
    const partial = {
      ...status,
      cli_binary: true,
      tools: [
        {
          ...withSkills(status.tools[2], { debug: true, placeholder: true }),
          cli_access_installed: false,
        },
      ],
    };
    bridge.getAgentDebugStatus
      .mockResolvedValueOnce(partial)
      .mockResolvedValue({
        ...partial,
        tools: [{ ...partial.tools[0], cli_access_installed: true }],
      });
    await act(async () => root.render(<AgentDebugSettings />));
    const row = [...container.querySelectorAll("tbody tr")].find((row) =>
      row.textContent?.includes("Codex"),
    );
    expect(row?.querySelectorAll("td")[1]?.textContent).toBe(
      "缺少命令放行规则部分安装",
    );
    expect(container.textContent).toContain("0 / 1");
    await act(async () => button("安装 / 更新").click());
    await act(async () =>
      button(
        "安装所选工具（1）",
        document.querySelector("[role='alertdialog']")!,
      ).click(),
    );
    expect(bridge.installAgentDebug).toHaveBeenCalledTimes(1);
    expect(button("安装 / 更新").disabled).toBe(false);
    expect(row?.querySelectorAll("td")[1]?.textContent).toBe("已安装");
    expect(container.textContent).toContain("1 / 1");
  });

  it("folds the host rules into each tool's status", async () => {
    const both = { debug: true, placeholder: true };
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      cli_binary: true,
      tools: [
        withSkills(status.tools[0], { debug: true }),
        {
          ...withSkills({ ...status.tools[1], detected: true }, both),
          cli_access_installed: true,
        },
        withSkills(status.tools[2], both),
        { ...status.tools[3], detected: false },
        withSkills({ ...status.tools[4], detected: true }, both),
      ],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    const statusCell = (name: string) =>
      [...container.querySelectorAll("tbody tr")]
        .find((row) => row.textContent?.includes(name))
        ?.querySelectorAll("td")[1]?.textContent;
    const header = container.querySelector("thead")?.textContent;
    expect(header).toBe("工具状态");
    // Hosts that ask on first run, never ask, or have no guard location need
    // no rule of their own.
    expect(statusCell("Cursor")).toBe("缺少隐私脱敏部分安装");
    expect(statusCell("Claude")).toBe("缺少文件访问限制部分安装");
    expect(statusCell("Codex")).toBe("已安装");
    // Neither detected nor set up, so it waits behind the toggle.
    expect(statusCell("Grok")).toBeUndefined();
    await act(async () => foldToggle().click());
    expect(statusCell("Grok")).toBe("—");
    expect(statusCell("Pi")).toBe("已安装");
    expect(container.textContent).toContain("2 / 4");

    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: [withSkills({ ...status.tools[1], detected: true }, both)],
    });
    await act(async () => button("重新检测").click());
    expect(statusCell("Claude")).toBe("缺少命令放行规则、文件访问限制部分安装");
  });

  it("installs only Grok and previews only its paths and the shared runtime", async () => {
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    for (const id of ["cursor", "codex", "grok"]) {
      expect(checkbox(id).getAttribute("aria-checked")).toBe("true");
    }
    expect(checkbox("claude").disabled).toBe(true);
    // Pi is not detected, so Codex installs alone.
    expect(dialog.textContent).not.toContain("共享 Skill");
    expect(checkbox("pi").disabled).toBe(true);
    await act(async () => skillCheckbox("redaction-placeholders").click());
    for (const id of ["cursor", "codex", "grok"]) {
      await act(async () => checkbox(id).click());
    }
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    expect(button("取消", dialog).disabled).toBe(false);
    expect(pathsPreview(dialog)).toBeUndefined();
    await act(async () => checkbox("grok").click());
    expect(button("安装所选工具（1）", dialog).disabled).toBe(false);
    expect(dialog.textContent).toContain("/tmp/.grok/skills/astrlink-debug");
    expect(dialog.textContent).toContain("/tmp/.astrlink/bin/astrlink");
    expect(dialog.textContent).toContain("/tmp/.astrlink/agent-installs.json");
    expect(dialog.textContent).not.toContain("redaction-placeholders");
    expect(dialog.textContent).not.toContain("/tmp/.agents");
    expect(dialog.textContent).not.toContain("/tmp/.codex");
    expect(dialog.textContent).not.toContain("/tmp/.cursor");
    await act(async () => button("安装所选工具（1）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug"],
      local("grok"),
    );
  });

  it("starts with every detected tool and supports multiple tools", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: status.tools.map((tool) => ({
        ...withSkills(tool, {}),
        cli_access_installed: false,
      })),
    });
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装工具").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    expect(skillCheckbox("astrlink-debug").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(
      skillCheckbox("redaction-placeholders").getAttribute("aria-checked"),
    ).toBe("true");
    expect(button("安装所选工具（3）", dialog).disabled).toBe(false);
    await act(async () => checkbox("codex").click());
    await act(async () => button("安装所选工具（2）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("cursor", "grok"),
    );
  });

  it("merges both skills into one status without requiring CLI rules for redaction alone", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: [
        status.tools[0],
        withSkills(
          { ...status.tools[1], detected: true },
          { placeholder: true },
        ),
        withSkills(status.tools[2], { placeholder: true }),
        status.tools[3],
        withSkills(
          { ...status.tools[4], detected: true },
          { placeholder: true },
        ),
      ],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    const statusCell = (name: string) =>
      [...container.querySelectorAll("tbody tr")]
        .find((row) => row.textContent?.includes(name))
        ?.querySelectorAll("td")[1]?.textContent;
    expect(statusCell("Cursor")).toBe("未安装");
    expect(statusCell("Claude")).toBe("缺少调试部分安装");
    expect(statusCell("Pi")).toBe("缺少调试部分安装");
    // No skill that runs the CLI is installed, so neither the missing CLI
    // access nor the missing CLI is a problem.
    expect(container.textContent).toContain("0 / 5");
    expect(container.textContent).not.toContain("命令行工具缺失");
  });

  it("installs the placeholder skill alone and previews only its files", async () => {
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    expect(dialog.textContent).toContain("仅 Skill，不含命令行工具和命令权限");
    await act(async () => skillCheckbox("redaction-placeholders").click());
    await act(async () => skillCheckbox("astrlink-debug").click());
    expect(button("安装所选工具（3）", dialog).disabled).toBe(true);
    expect(pathsPreview(dialog)).toBeUndefined();
    await act(async () => skillCheckbox("redaction-placeholders").click());
    expect(button("安装所选工具（3）", dialog).disabled).toBe(false);
    expect(dialog.textContent).toContain(
      "/tmp/.agents/skills/redaction-placeholders",
    );
    expect(dialog.textContent).not.toContain(
      "/tmp/.agents/skills/astrlink-debug",
    );
    expect(dialog.textContent).not.toContain("/tmp/.astrlink/bin/astrlink");
    expect(dialog.textContent).not.toContain("/tmp/.codex");
    await act(async () => button("安装所选工具（3）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["redaction-placeholders"],
      local("cursor", "codex", "grok"),
    );
  });

  it("shows one shared Codex / Pi option with both statuses and install targets", async () => {
    const bothSkills = { debug: true, placeholder: true };
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: [
        ...status.tools.slice(0, 2),
        {
          ...withSkills(status.tools[2], bothSkills),
          cli_access_installed: false,
        },
        status.tools[3],
        withSkills({ ...status.tools[4], detected: true }, bothSkills),
      ],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    // Codex already has a skill in the directory Pi reads too.
    const shared = checkbox("codex-pi");
    expect(shared.getAttribute("aria-checked")).toBe("true");
    expect(dialog.querySelector("#agent-install-codex")).toBeNull();
    expect(dialog.querySelector("#agent-install-pi")).toBeNull();
    expect(shared.closest("label")?.textContent).toBe(
      "Codex / Pi（共享 Skill）Codex · 部分安装Pi · 已安装",
    );
    expect(dialog.textContent).toContain(
      "Codex、Pi 和 DeepSeek Harness 共用 ~/.agents/skills，将一起安装或更新。",
    );
    expect(dialog.textContent).toContain("/tmp/.codex/rules/astrlink.rules");
    expect(
      [...dialog.querySelectorAll("li")].filter(
        (item) => item.textContent === "/tmp/.agents/skills/astrlink-debug",
      ),
    ).toHaveLength(1);
    // Narrow the selection to the shared option.
    await act(async () => checkbox("cursor").click());
    await act(async () => checkbox("grok").click());
    await act(async () => shared.click());
    expect(shared.getAttribute("aria-checked")).toBe("false");
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    expect(pathsPreview(dialog)).toBeUndefined();
    await act(async () => shared.click());
    expect(shared.getAttribute("aria-checked")).toBe("true");
    await act(async () => skillCheckbox("astrlink-debug").click());
    await act(async () => skillCheckbox("astrlink-debug").click());
    await act(async () => button("安装所选工具（2）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("codex", "pi"),
    );
  });

  it("keeps Pi selectable on its own when Codex is not detected", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: status.tools.map((tool) => ({
        ...withSkills(tool, {}),
        detected: tool.id === "pi",
        cli_access_installed: false,
      })),
    });
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装工具").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    expect(dialog.querySelector("#agent-install-codex-pi")).toBeNull();
    expect(checkbox("codex").disabled).toBe(true);
    expect(checkbox("pi").disabled).toBe(false);
    expect(checkbox("pi").getAttribute("aria-checked")).toBe("true");
    expect(dialog.textContent).not.toContain("/tmp/.codex");
    await act(async () => button("安装所选工具（1）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("pi"),
    );
  });

  it("adds DeepSeek Harness to the option it shares with Codex and Pi", async () => {
    const bothSkills = { debug: true, placeholder: true };
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: status.tools.map((tool) =>
        ["codex", "pi", "dsh"].includes(tool.id)
          ? withSkills({ ...tool, detected: true }, bothSkills)
          : tool,
      ),
    });
    await act(async () => root.render(<AgentDebugSettings />));
    expect(container.textContent).toContain("DeepSeek Harness");
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    const shared = checkbox("codex-pi-dsh");
    expect(shared.getAttribute("aria-checked")).toBe("true");
    for (const id of ["codex", "pi", "dsh"]) {
      expect(dialog.querySelector(`#agent-install-${id}`)).toBeNull();
    }
    expect(shared.closest("label")?.textContent).toBe(
      "Codex / Pi / DeepSeek Harness（共享 Skill）Codex · 已安装Pi · 已安装DeepSeek Harness · 已安装",
    );
    // The other detected tools, Cursor and Grok Build, start selected too.
    await act(async () => button("安装所选工具（5）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("cursor", "codex", "grok", "pi", "dsh"),
    );
  });

  it("shows the shared option on Pi when Codex is not detected", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: status.tools.map((tool) => ({
        ...withSkills(tool, {}),
        detected: tool.id === "pi" || tool.id === "dsh",
        cli_access_installed: false,
      })),
    });
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装工具").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    expect(checkbox("codex").disabled).toBe(true);
    expect(dialog.querySelector("#agent-install-pi")).toBeNull();
    expect(dialog.querySelector("#agent-install-dsh")).toBeNull();
    const shared = checkbox("pi-dsh");
    expect(shared.closest("label")?.textContent).toBe(
      "Pi / DeepSeek Harness（共享 Skill）Pi · 已检测到DeepSeek Harness · 已检测到",
    );
    expect(shared.getAttribute("aria-checked")).toBe("true");
    await act(async () => button("安装所选工具（2）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("pi", "dsh"),
    );
  });

  it("lists WSL tools beside this computer's and installs them by distribution", async () => {
    const wslRoot = String.raw`\\wsl$\Ubuntu\home\me`;
    const inUbuntu = (tool: ToolFixture, root: string): ToolFixture => ({
      ...tool,
      wsl: "Ubuntu" as unknown as null,
      detected: true,
      skills: skills(`${wslRoot}\\${root}`, {}),
      cli_access_installed: false,
      guard_installed: false,
    });
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: [
        ...status.tools,
        inUbuntu(status.tools[1]!, ".claude\\skills"),
        inUbuntu(status.tools[2]!, ".agents\\skills"),
        inUbuntu(status.tools[4]!, ".agents\\skills"),
      ],
      wsl_unchecked: ["Debian"],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    const rows = [...container.querySelectorAll("tbody tr")].map(
      (row) => row.querySelector("td")?.textContent ?? "",
    );
    expect(rows).toHaveLength(8);
    ["Claude Code", "Codex", "Pi"].forEach((name, index) => {
      expect(rows[4 + index]).toContain(name);
      expect(rows[4 + index]?.endsWith("WSL · Ubuntu")).toBe(true);
    });
    expect(rows.slice(0, 4).some((row) => row.includes("WSL"))).toBe(false);
    // This computer's Claude Code and DeepSeek Harness are neither detected
    // nor set up.
    expect(rows[7]).toBe("未检测到的工具（2）");
    expect(container.textContent).toContain("0 / 6");
    expect(container.textContent).toContain(
      "Debian 未运行，没有检测其中的工具。启动后点「重新检测」。",
    );

    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    // Detected tools start selected in every home.
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("wsl-Ubuntu-claude").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(checkbox("wsl-Ubuntu-codex-pi").getAttribute("aria-checked")).toBe(
      "true",
    );
    // Codex and Pi share a skill directory only within one home.
    expect(dialog.querySelector("#agent-install-wsl-Ubuntu-pi")).toBeNull();
    expect(dialog.querySelector("#agent-install-wsl-Ubuntu-codex")).toBeNull();
    expect(checkbox("pi").getAttribute("aria-checked")).toBe("false");
    // Unchecking Ubuntu's option leaves this computer's Codex selected.
    await act(async () => checkbox("wsl-Ubuntu-codex-pi").click());
    expect(checkbox("wsl-Ubuntu-codex-pi").getAttribute("aria-checked")).toBe(
      "false",
    );
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    await act(async () => checkbox("wsl-Ubuntu-codex-pi").click());
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("pi").getAttribute("aria-checked")).toBe("false");
    expect(dialog.textContent).toContain(`${wslRoot}\\.agents\\skills`);
    for (const id of ["cursor", "grok", "wsl-Ubuntu-claude"]) {
      await act(async () => checkbox(id).click());
    }
    await act(async () => button("安装所选工具（3）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      [
        { id: "codex", wsl: null },
        { id: "codex", wsl: "Ubuntu" },
        { id: "pi", wsl: "Ubuntu" },
      ],
    );
  });

  it("opens the install dialog with a preselected skill", async () => {
    await act(async () =>
      root.render(
        <AgentDebugSettings preselectSkill="redaction-placeholders" />,
      ),
    );
    const dialog = document.querySelector("[role='alertdialog']");
    if (!dialog) throw new Error("dialog did not open");
    expect(skillCheckbox("astrlink-debug").getAttribute("aria-checked")).toBe(
      "false",
    );
    expect(
      skillCheckbox("redaction-placeholders").getAttribute("aria-checked"),
    ).toBe("true");
    // Every detected tool starts selected, the set-up Codex included.
    await act(async () => button("安装所选工具（3）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["redaction-placeholders"],
      local("cursor", "codex", "grok"),
    );
    // Only the first status opens the dialog.
    await act(async () => button("重新检测").click());
    expect(document.querySelector("[role='alertdialog']")).toBeNull();
  });

  it("preselects every detected tool when nothing is installed yet", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: status.tools.map((tool) => ({
        ...withSkills(tool, {}),
        cli_access_installed: false,
      })),
    });
    await act(async () =>
      root.render(
        <AgentDebugSettings preselectSkill="redaction-placeholders" />,
      ),
    );
    const dialog = document.querySelector("[role='alertdialog']")!;
    await act(async () => button("安装所选工具（3）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["redaction-placeholders"],
      local("cursor", "codex", "grok"),
    );
  });

  it("selects every detected tool when the install dialog opens", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue(
      detectedOnly("claude", "codex"),
    );
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装工具").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    expect(checkbox("claude").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    const confirm = button("安装所选工具（2）", dialog);
    expect(confirm.disabled).toBe(false);
    await act(async () => confirm.click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("claude", "codex"),
    );
  });

  it("keeps undetected tools collapsed in the tool list and the dialog", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue(
      detectedOnly("claude", "codex"),
    );
    await act(async () => root.render(<AgentDebugSettings />));
    expect(toolRowNames()).toEqual([
      "Claude Code",
      "Codex",
      "未检测到的工具（4）",
    ]);
    const toggle = foldToggle();
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    const folded = document.getElementById(
      toggle.getAttribute("aria-controls")!,
    );
    expect(folded?.tagName).toBe("TBODY");
    expect(folded?.children).toHaveLength(0);
    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    // The toggle stays put; the folded tools open below it.
    expect(toolRowNames()).toEqual([
      "Claude Code",
      "Codex",
      "未检测到的工具（4）",
      "Cursor",
      "Grok Build",
      "Pi",
      "DeepSeek Harness",
    ]);
    expect(folded?.children).toHaveLength(4);

    await act(async () => button("安装工具").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    const undetected = checkbox("cursor").closest("details");
    if (!undetected) throw new Error("undetected tools are not folded");
    expect(dialog.contains(undetected)).toBe(true);
    expect(undetected.hasAttribute("open")).toBe(false);
    expect(undetected.querySelector("summary")?.textContent).toBe(
      "未检测到的工具（4）",
    );
    expect(undetected.textContent).toContain(
      "装好并启动一次后，点「重新检测」就能选择。",
    );
    for (const id of ["cursor", "grok", "pi", "dsh"]) {
      expect(undetected.contains(checkbox(id))).toBe(true);
      expect(checkbox(id).disabled).toBe(true);
    }
    for (const id of ["claude", "codex"]) {
      expect(checkbox(id).closest("details")).toBeNull();
    }
  });

  it("folds every tool and says so when none is detected", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue(detectedOnly());
    await act(async () => root.render(<AgentDebugSettings />));
    expect(toolRowNames()).toEqual(["未检测到的工具（6）"]);
    expect(foldToggle().getAttribute("aria-expanded")).toBe("false");
    expect(container.textContent).toContain(
      "暂未检测到支持的工具。请先安装并启动一次 Cursor、Claude Code、Codex、Grok Build、Pi 或 DeepSeek Harness，再重新检测。",
    );
    expect(button("安装工具").disabled).toBe(true);
  });

  it("cancels an empty selection without installing", async () => {
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    for (const id of ["cursor", "codex", "grok"]) {
      await act(async () => checkbox(id).click());
    }
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    await act(async () => button("取消", dialog).click());
    expect(bridge.installAgentDebug).not.toHaveBeenCalled();
    expect(document.querySelector("[role='alertdialog']")).toBeNull();
  });

  it("offers a retry after status failure and prevents installing without detected tools", async () => {
    bridge.getAgentDebugStatus.mockRejectedValueOnce(
      new Error("Status unavailable"),
    );
    await act(async () => root.render(<AgentDebugSettings />));
    expect(container.querySelector("[role='alert']")?.textContent).toBe(
      "Status unavailable",
    );
    expect(button("安装工具").disabled).toBe(true);
    expect(container.textContent).not.toContain("检查中");
    // Without a status nothing is known to be missing, so nothing folds.
    expect(toolRowNames()).toHaveLength(6);
    expect(container.querySelector("tbody button")).toBeNull();
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      cli_binary: false,
      tools: [],
    });
    await act(async () => button("重新检测").click());
    expect(container.querySelector("[role='alert']")).toBeNull();
    expect(container.textContent).toContain("暂未检测到支持的工具");
    expect(button("安装工具").disabled).toBe(true);
  });

  it("copies the diagnostic prompt and keeps uninstall behind confirmation", async () => {
    const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("复制示例").click());
    expect(copy).toHaveBeenCalledWith(
      expect.stringContaining("使用 astrlink-debug"),
    );
    expect(button("已复制")).toBeTruthy();
    await act(async () => button("卸载").click());
    expect(bridge.uninstallAgentDebug).not.toHaveBeenCalled();
    const dialog = document.querySelector("[role='alertdialog']")!;
    await act(async () => button("卸载", dialog).click());
    expect(bridge.uninstallAgentDebug).toHaveBeenCalledTimes(1);
    copy.mockRestore();
  });

  it("keeps the common questions behind one collapsed entry", async () => {
    await act(async () => root.render(<AgentDebugSettings />));
    const help = container.querySelectorAll("details");
    expect(help).toHaveLength(1);
    expect(help[0].hasAttribute("open")).toBe(false);
    expect(help[0].querySelector("summary")?.textContent).toBe("常见问题");
    expect(
      [...help[0].querySelectorAll("dt")].map((term) => term.textContent),
    ).toEqual([
      "未检测到或安装不完整？",
      "Agent 连不上 AstrLink？",
      "为什么看不到请求正文？",
    ]);
  });

  function skillCheckbox(id: string): HTMLButtonElement {
    const found = document.querySelector<HTMLButtonElement>(
      `#agent-skill-${id}`,
    );
    if (!found) throw new Error(`Missing skill checkbox: ${id}`);
    return found;
  }

  function checkbox(id: string): HTMLButtonElement {
    const found = document.querySelector<HTMLButtonElement>(
      `#agent-install-${id}`,
    );
    if (!found) throw new Error(`Missing checkbox: ${id}`);
    return found;
  }

  /** The "将写入的位置" preview, which shows only for a complete selection. */
  function pathsPreview(dialog: ParentNode): HTMLDetailsElement | undefined {
    return [...dialog.querySelectorAll("details")].find(
      (details) =>
        details.querySelector("summary")?.textContent === "查看将写入的位置",
    );
  }

  function foldToggle(): HTMLButtonElement {
    const found = container.querySelector<HTMLButtonElement>(
      "tbody button[aria-controls]",
    );
    if (!found) throw new Error("Missing undetected tools toggle");
    return found;
  }

  /** The first cell of each tool table row, the toggle row included. */
  function toolRowNames(): string[] {
    return [...container.querySelectorAll("tbody tr")].map((row) => {
      // Brand marks carry an SVG title; read only the visible name.
      const cell = row.querySelector("td")?.cloneNode(true) as Element;
      cell
        .querySelectorAll("[aria-hidden='true']")
        .forEach((node) => node.remove());
      return cell.textContent ?? "";
    });
  }

  function button(
    text: string,
    scope: ParentNode = container,
  ): HTMLButtonElement {
    const found = [...scope.querySelectorAll("button")].find(
      (item) => item.textContent === text,
    );
    if (!found) throw new Error(`Missing button: ${text}`);
    return found;
  }
});
