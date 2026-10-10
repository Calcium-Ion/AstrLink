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
          (button) => button.textContent === "安装所选工具（1）",
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
      local("codex"),
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
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("claude").disabled).toBe(true);
    // Pi is not detected, so Codex installs alone.
    expect(dialog.textContent).not.toContain("一起安装");
    await act(async () => skillCheckbox("redaction-placeholders").click());
    await act(async () => checkbox("codex").click());
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    expect(button("取消", dialog).disabled).toBe(false);
    expect(dialog.querySelector("details")).toBeNull();
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

  it("requires an explicit first selection and supports multiple tools", async () => {
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
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    await act(async () => checkbox("cursor").click());
    await act(async () => checkbox("grok").click());
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
    expect(button("安装所选工具（1）", dialog).disabled).toBe(true);
    expect(dialog.querySelector("details")).toBeNull();
    await act(async () => skillCheckbox("redaction-placeholders").click());
    expect(button("安装所选工具（1）", dialog).disabled).toBe(false);
    expect(dialog.textContent).toContain(
      "/tmp/.agents/skills/redaction-placeholders",
    );
    expect(dialog.textContent).not.toContain(
      "/tmp/.agents/skills/astrlink-debug",
    );
    expect(dialog.textContent).not.toContain("/tmp/.astrlink/bin/astrlink");
    expect(dialog.textContent).not.toContain("/tmp/.codex");
    await act(async () => button("安装所选工具（1）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["redaction-placeholders"],
      local("codex"),
    );
  });

  it("selects Codex and Pi together and installs skills in a fixed order", async () => {
    bridge.getAgentDebugStatus.mockResolvedValue({
      ...status,
      tools: [
        ...status.tools.slice(0, 4),
        withSkills({ ...status.tools[4], detected: true }, {}),
      ],
    });
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    // Codex already has a skill in the directory Pi reads too.
    expect(checkbox("pi").getAttribute("aria-checked")).toBe("true");
    expect(dialog.textContent).toContain(
      "Codex 和 Pi 读取同一个 Skill 目录（~/.agents/skills），勾选其中一个会一起安装。",
    );
    await act(async () => checkbox("pi").click());
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("false");
    expect(button("安装所选工具（0）", dialog).disabled).toBe(true);
    await act(async () => checkbox("pi").click());
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    await act(async () => skillCheckbox("astrlink-debug").click());
    await act(async () => skillCheckbox("astrlink-debug").click());
    await act(async () => button("安装所选工具（2）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["astrlink-debug", "redaction-placeholders"],
      local("codex", "pi"),
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
      expect(rows[5 + index]).toContain(name);
      expect(rows[5 + index]?.endsWith("WSL · Ubuntu")).toBe(true);
    });
    expect(rows.slice(0, 5).some((row) => row.includes("WSL"))).toBe(false);
    expect(container.textContent).toContain("0 / 6");
    expect(container.textContent).toContain(
      "Debian 未运行，没有检测其中的工具。启动后点「重新检测」。",
    );

    await act(async () => button("安装 / 更新").click());
    const dialog = document.querySelector("[role='alertdialog']")!;
    // The Codex install on this computer says nothing about Ubuntu's.
    expect(checkbox("codex").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("wsl-Ubuntu-codex").getAttribute("aria-checked")).toBe(
      "false",
    );
    // Codex and Pi share a skill directory only within one home.
    await act(async () => checkbox("wsl-Ubuntu-codex").click());
    expect(checkbox("wsl-Ubuntu-pi").getAttribute("aria-checked")).toBe("true");
    expect(checkbox("pi").getAttribute("aria-checked")).toBe("false");
    expect(dialog.textContent).toContain(`${wslRoot}\\.agents\\skills`);
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
    // The tools that already have a skill stay selected.
    await act(async () => button("安装所选工具（1）", dialog).click());
    expect(bridge.installAgentDebug).toHaveBeenCalledExactlyOnceWith(
      ["redaction-placeholders"],
      local("codex"),
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

  it("cancels an empty selection without installing", async () => {
    await act(async () => root.render(<AgentDebugSettings />));
    await act(async () => button("安装 / 更新").click());
    await act(async () => checkbox("codex").click());
    await act(async () =>
      button("取消", document.querySelector("[role='alertdialog']")!).click(),
    );
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
