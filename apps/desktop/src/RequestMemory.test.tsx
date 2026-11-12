// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getProjectMemory: vi.fn(),
  getProjectMemoryEntry: vi.fn(),
  getRequestContextTexts: vi.fn(),
}));
vi.mock("./bridge", () => bridgeMocks);

import { settleClosedLayer } from "./lib/test-layer-settle";
import { RequestMemory } from "./RequestMemory";
import type { MemoryFocus } from "./request-memory-model";
import type {
  MemoryEntry,
  MemoryEntryDetail,
  ProjectMemory,
  RequestContextTexts,
} from "./request-record-model";

// A Friday afternoon, local time.
const NOW = new Date(2026, 9, 30, 15, 20);
const local = (day: number, hour = 9, minute = 0) =>
  new Date(2026, 9, day, hour, minute).toISOString();
const DAY_START = new Date(2026, 9, 30).toISOString();
// The toolbox counts the calls of the 30 days that end today.
const SINCE = new Date(2026, 9, 1).toISOString();
const memoryPath = "~/.claude/projects/~-GolandProjects-astrlink/memory";
const quiet = Array<number>(14).fill(0);

const entries: MemoryEntry[] = [
  {
    file: "no-jitter.md",
    path: `${memoryPath}/no-jitter.md`,
    title: "No jitter-sensitive tests",
    hook: "wait on done channels, not short wall-clock deadlines",
    noted_at: local(30, 15, 5),
    reads: [...quiet.slice(1), 2],
    total_reads: 2,
  },
  {
    file: "workspace-commit.md",
    path: `${memoryPath}/workspace-commit.md`,
    title: "Workspace commit attribution",
    hook: "Paseo reports find each hunk's session",
    noted_at: local(26, 11),
    reads: quiet,
    total_reads: 0,
  },
  {
    file: "shared-checkout.md",
    path: `${memoryPath}/shared-checkout.md`,
    title: "Shared checkout, concurrent work",
    hook: "never add -A; git commit --only",
    reads: [1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
    total_reads: 18,
  },
];

const memory: ProjectMemory = {
  projects: [
    {
      project: "~/GolandProjects/astrlink",
      client_types: ["claude_code", "codex"],
      last_seen_at: local(30, 15, 5),
    },
    { client_types: ["claude_code"], last_seen_at: local(29) },
  ],
  project: "~/GolandProjects/astrlink",
  client_type: "claude_code",
  day_start: DAY_START,
  recent: 2,
  entries,
  stories: [
    {
      file: "no-jitter.md",
      title: "No jitter-sensitive tests",
      hook: "wait on done channels, not short wall-clock deadlines",
      at: local(30, 15, 5),
      request_id: "req_note",
      session_id: "session_2",
      prompt: "把 failover 的单测补完，跑一遍 go test",
    },
    {
      file: "compressed.md",
      title: "Compressed request bodies are required",
      at: local(29, 21, 40),
      request_id: "req_gzip",
      session_id: "session_1",
    },
    {
      file: "workspace-commit.md",
      title: "Workspace commit attribution",
      at: local(26, 11),
      request_id: "req_commit",
      session_id: "session_0",
      prompt: "提交工作区",
    },
  ],
  rules: [
    {
      name: "AGENTS.md",
      path: "~/GolandProjects/astrlink/AGENTS.md",
      scope: "project",
      first_seen_at: local(21),
      changed_at: local(30, 14, 58),
      request_id: "req_rules",
      session_id: "session_2",
      before_request_id: "req_old",
      captured: true,
    },
    {
      name: "CLAUDE.md",
      path: "~/.claude/CLAUDE.md",
      scope: "global",
      first_seen_at: local(21),
      changed_at: local(24, 18, 30),
    },
  ],
  toolbox: {
    since: SINCE,
    skills: [
      { name: "frontend-design", uses: 4, carried: true },
      { name: "dataviz", uses: 2, carried: true },
      { name: "paseo-handoff", uses: 1, carried: true },
      { name: "humanizer", uses: 1, carried: true },
      { name: "canvas-design", uses: 0, carried: true },
      { name: "docx", uses: 0, carried: true },
    ],
    mcp: [
      {
        server: "paseo",
        tools: [
          { name: "create_agent", uses: 12, carried: true },
          { name: "send_agent_prompt", uses: 9, carried: true },
          { name: "create_schedule", uses: 0, carried: true },
        ],
      },
    ],
    idle_tokens: 13_000,
  },
  has_memory: true,
  has_rules: true,
};

const detail: MemoryEntryDetail = {
  entry: entries[0]!,
  events: [
    {
      kind: "noted",
      at: local(30, 15, 5),
      request_id: "req_note",
      session_id: "session_2",
      prompt: "把 failover 的单测补完",
      captured: true,
    },
    {
      kind: "read",
      at: local(30, 15, 24),
      request_id: "req_read",
      session_id: "session_3",
      prompt: "CI 上 TestFailoverOrder 偶发失败，查一下",
      captured: true,
    },
    { kind: "read", at: local(30, 16, 40), request_id: "req_bare" },
  ],
};

function texts(
  requestId: string,
  fields: Partial<RequestContextTexts>,
): RequestContextTexts {
  return { request_id: requestId, items: [], writes: [], reads: [], ...fields };
}

function optionText(option: Element): string | undefined {
  const copy = option.cloneNode(true) as Element;
  copy
    .querySelectorAll('[aria-hidden="true"]')
    .forEach((node) => node.remove());
  return copy.textContent?.trim();
}

describe("project memory tab", () => {
  let container: HTMLDivElement;
  let root: Root;
  const onOpenCall = vi.fn();

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(NOW);
    bridgeMocks.getProjectMemory.mockReset();
    bridgeMocks.getProjectMemoryEntry.mockReset();
    bridgeMocks.getRequestContextTexts.mockReset();
    bridgeMocks.getProjectMemory.mockResolvedValue(memory);
    bridgeMocks.getProjectMemoryEntry.mockResolvedValue(detail);
    onOpenCall.mockReset();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
  });

  async function render({
    reloadRef,
    focus = null,
  }: {
    reloadRef?: { current: (() => void) | null };
    focus?: (MemoryFocus & { key: number }) | null;
  } = {}) {
    await act(async () => {
      root.render(
        <RequestMemory
          focus={focus}
          isReady
          onOpenCall={onOpenCall}
          reloadRef={reloadRef}
        />,
      );
    });
    await settle();
  }

  async function settle() {
    for (let step = 0; step < 4; step += 1) {
      await act(async () => await Promise.resolve());
    }
  }

  const trigger = (label: string) =>
    container.querySelector<HTMLButtonElement>(
      `button[role="combobox"][aria-label="${label}"]`,
    )!;
  const texts_ = (selector: string, root: ParentNode = container) =>
    [...root.querySelectorAll<HTMLElement>(selector)].map(
      (node) => node.textContent ?? "",
    );
  const button = (label: string) =>
    [...container.querySelectorAll("button")].find(
      (candidate) =>
        candidate.textContent === label ||
        candidate.getAttribute("aria-label") === label,
    )!;

  it("tells what the agent learned, with what the user said", async () => {
    await render();
    // Core picks the project and the client that called last; the days
    // follow the local day.
    expect(bridgeMocks.getProjectMemory).toHaveBeenCalledWith({
      day_start: DAY_START,
    });
    expect(trigger("项目筛选").textContent).toBe(
      "项目astrlink~/GolandProjects/astrlink",
    );
    expect(optionText(trigger("客户端筛选"))).toBe("客户端Claude Code");
    expect(
      container.querySelector('[data-testid="memory-count"]')?.textContent,
    ).toBe("记了 3 件事，最近 7 天学会 2 件");

    const stories = container.querySelector('[data-testid="memory-stories"]')!;
    expect(stories.querySelector("h2")?.nextElementSibling?.textContent).toBe(
      "你说的话，和它顺手记下的",
    );
    expect(texts_('[data-slot="story-card"]', stories)).toStrictEqual([
      "把 failover 的单测补完，跑一遍 go test↳No jitter-sensitive tests今天 15:05wait on done channels, not short wall-clock deadlines",
      "没看到这一轮你说的话↳Compressed request bodies are required昨天 21:40",
    ]);
    expect(stories.textContent).toContain("1–2 / 3");
    expect(button("上一页").disabled).toBe(true);
    await act(async () => button("下一页").click());
    expect(texts_('[data-slot="story-card"]', stories)).toStrictEqual([
      "提交工作区↳Workspace commit attribution10月26日 11:00",
    ]);
    expect(stories.textContent).toContain("3 / 3");
  });

  it("lists every memory with the days it was read again", async () => {
    await render();
    const section = container.querySelector('[data-testid="memory-entries"]')!;
    expect(section.querySelector("h2")?.parentElement?.textContent).toBe(
      "全部记忆3 条近 14 天翻过记下那天",
    );
    const rows = [
      ...section.querySelectorAll<HTMLElement>('[data-testid="memory-entry"]'),
    ];
    expect(
      rows.map((row) => [
        row.querySelector('[role="img"][aria-label="最近 7 天记下的"]') !==
          null,
        row.textContent,
      ]),
    ).toStrictEqual([
      [
        true,
        "No jitter-sensitive testswait on done channels, not short wall-clock deadlines2 次",
      ],
      [
        true,
        "Workspace commit attributionPaseo reports find each hunk's session没翻过",
      ],
      [
        false,
        "Shared checkout, concurrent worknever add -A; git commit --only18 次",
      ],
    ]);
    const strip = (row: HTMLElement) =>
      [...row.querySelectorAll('[data-slot="day-strip"] > span')]
        .map((cell) =>
          cell.hasAttribute("data-count")
            ? `${cell.getAttribute("data-count")}${cell.hasAttribute("data-marked") ? "*" : ""}`
            : ".",
        )
        .join(" ");
    // Noted today: nothing before it, the day outlined.
    expect(strip(rows[0]!)).toBe(". . . . . . . . . . . . . 2*");
    expect(strip(rows[1]!)).toBe(". . . . . . . . . 0* 0 0 0 0");
    // Known before the gateway saw it: every day drawn.
    expect(strip(rows[2]!)).toBe("1 2 0 0 0 0 0 0 0 0 0 0 0 1");
    expect(
      rows[2]!
        .querySelector('[data-slot="day-strip"]')
        ?.getAttribute("aria-label"),
    ).toBe("近 14 天翻过 4 次");
    // The list scrolls inside the panel.
    expect(
      section.querySelector('[data-testid="memory-entries-scroll"]')?.className,
    ).toContain("overflow-y-auto");
  });

  it("searches memories by title, line and file", async () => {
    await render();
    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索记忆"]',
    )!;
    const type = async (value: string) => {
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!;
        setter.call(search, value);
        search.dispatchEvent(new Event("input", { bubbles: true }));
      });
    };
    const titles = () => texts_('[data-testid="memory-entry"] .font-medium');
    await type("git commit");
    expect(titles()).toStrictEqual(["Shared checkout, concurrent work"]);
    await type("NO-JITTER.md");
    expect(titles()).toStrictEqual(["No jitter-sensitive tests"]);
    await type("nothing like it");
    expect(
      container.querySelector('[data-testid="memory-entries-scroll"]')
        ?.textContent,
    ).toBe("没有和「nothing like it」相关的记忆");
  });

  it("shows the rules and what the toolbox carried and used", async () => {
    await render();
    expect(texts_('[data-testid="memory-rules-file"]')).toStrictEqual([
      "AGENTS.md项目看变化今天 14:58 改了",
      "CLAUDE.md全局10月24日改了",
    ]);
    const groups = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="memory-toolbox-group"]',
      ),
    ];
    expect(
      groups.map((group) => [
        group.firstElementChild?.textContent,
        texts_('[data-slot="breakdown-line"]', group),
        group.querySelector("p")?.textContent,
      ]),
    ).toStrictEqual([
      [
        "Skills6 个",
        ["frontend-design4", "dataviz2", "paseo-handoff1"],
        "另有 1 个用过；没碰过 2 个：canvas-design、docx",
      ],
      [
        "MCP · paseo3 个工具",
        ["create_agent12", "send_agent_prompt9"],
        "没碰过 1 个：create_schedule",
      ],
    ]);
    expect(
      [
        ...container.querySelectorAll<HTMLElement>(
          '[data-testid="memory-toolbox-group"] [data-slot="breakdown-line-share"]',
        ),
      ].map((bar) => bar.style.width),
    ).toStrictEqual(["100%", "50%", "25%", "100%", "75%"]);
    expect(
      container.querySelector('[data-testid="memory-toolbox"]')?.textContent,
    ).toContain("没碰过的这些里，现在还带着的每次调用多带约 1.3万 token。");
    // The days it counts say where they begin and how they move on.
    const days = container.querySelector<HTMLElement>(
      '[data-testid="memory-toolbox-days"]',
    );
    expect(days?.textContent).toBe("近 30 天 · 10月1日起");
    expect(days?.title).toBe("每天零点往后挪一天，更早的调用不再计入");
  });

  it("opens a memory with its text and what happened to it", async () => {
    bridgeMocks.getRequestContextTexts.mockImplementation(async (id) =>
      texts(id, {
        reads: [
          {
            name: "no-jitter.md",
            path: `${memoryPath}/no-jitter.md`,
            content:
              "---\nname: no-jitter\n---\n\nTests must not depend on short deadlines.\n",
          },
        ],
      }),
    );
    await render();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="memory-entry"] button',
        )!
        .click(),
    );
    await settle();
    expect(bridgeMocks.getProjectMemoryEntry).toHaveBeenCalledWith({
      project: "~/GolandProjects/astrlink",
      client_type: "claude_code",
      day_start: DAY_START,
      file: "no-jitter.md",
    });
    // The newest captured read shows the whole file.
    expect(bridgeMocks.getRequestContextTexts).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getRequestContextTexts).toHaveBeenCalledWith("req_read");
    const dialog = document.querySelector<HTMLElement>(
      '[data-testid="memory-entry-dialog"]',
    )!;
    expect(dialog.querySelector("h2")?.textContent).toBe(
      "No jitter-sensitive tests",
    );
    expect(dialog.textContent).toContain(
      "wait on done channels, not short wall-clock deadlines",
    );
    expect(dialog.textContent).toContain(`${memoryPath}/no-jitter.md`);
    const body = dialog.querySelector('[data-testid="memory-entry-body"]')!;
    expect(body.textContent).toContain(
      "Tests must not depend on short deadlines.",
    );
    expect(body.textContent).not.toContain("name: no-jitter");
    expect(texts_('[data-testid="memory-event"]', dialog)).toStrictEqual([
      "记下你说「把 failover 的单测补完」今天 15:05",
      "翻过你说「CI 上 TestFailoverOrder 偶发失败，查一下」今天 15:24",
      "翻过今天 16:40",
    ]);
    await act(async () =>
      dialog
        .querySelectorAll<HTMLButtonElement>(
          '[data-testid="memory-event"] button',
        )[1]!
        .click(),
    );
    await settleClosedLayer();
    expect(onOpenCall).toHaveBeenCalledWith("session_3", "req_read");
    expect(
      document.querySelector('[data-testid="memory-entry-dialog"]'),
    ).toBeNull();
  });

  it("says why a memory's text cannot be shown", async () => {
    const openFirst = async () => {
      await act(async () =>
        container
          .querySelector<HTMLButtonElement>(
            '[data-testid="memory-entry"] button',
          )!
          .click(),
      );
      await settle();
    };
    const body = () =>
      document.querySelector('[data-testid="memory-entry-body"]')?.textContent;
    bridgeMocks.getProjectMemoryEntry.mockResolvedValue({
      ...detail,
      events: detail.events.map((event) => ({ ...event, captured: false })),
    });
    await render();
    await openFirst();
    expect(bridgeMocks.getRequestContextTexts).not.toHaveBeenCalled();
    expect(body()).toMatch(/^没有捕获到这条记忆的正文它的经历/);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    await settleClosedLayer();

    bridgeMocks.getProjectMemoryEntry.mockResolvedValue(detail);
    bridgeMocks.getRequestContextTexts.mockImplementation(async (id) =>
      texts(id, { withheld: true, reason: "raw_locked" }),
    );
    await openFirst();
    // Each captured call was tried before it gave up.
    expect(bridgeMocks.getRequestContextTexts).toHaveBeenCalledTimes(2);
    expect(body()).toMatch(/^原文已封存，输入原文口令解锁后才能查看。/);
  });

  it("compares a rules file's two versions", async () => {
    bridgeMocks.getRequestContextTexts.mockImplementation(async (id) =>
      texts(id, {
        items: [
          {
            kind: "rules",
            name: "AGENTS.md",
            path: "~/GolandProjects/astrlink/AGENTS.md",
            text:
              id === "req_rules"
                ? "# Rules\nRun gofmt.\nRun go vet.\n"
                : "# Rules\nRun gofmt.\n",
          },
        ],
      }),
    );
    await render();
    await act(async () => button("看变化").click());
    await settle();
    expect(bridgeMocks.getRequestContextTexts).toHaveBeenCalledWith("req_old");
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.querySelector("h2")?.textContent).toBe("AGENTS.md 的变化");
    expect(
      [...(dialog?.querySelectorAll("[data-diff]") ?? [])].map(
        (line) => `${line.getAttribute("data-diff")}:${line.textContent}`,
      ),
    ).toStrictEqual(["same:# Rules", "same:Run gofmt.", "added:Run go vet."]);
  });

  it("fades in a memory noted while the view is open", async () => {
    const reloadRef: { current: (() => void) | null } = { current: null };
    await render({ reloadRef });
    await act(async () => button("下一页").click());
    expect(
      container.querySelectorAll('[data-slot="story-card"][data-fresh]'),
    ).toHaveLength(0);
    bridgeMocks.getProjectMemory.mockResolvedValue({
      ...memory,
      stories: [
        {
          file: "reply.md",
          title: "Reply in Chinese",
          at: local(30, 15, 19),
          request_id: "req_reply",
          session_id: "session_4",
          prompt: "以后都用中文回复",
        },
        ...memory.stories,
      ],
    });
    await act(async () => reloadRef.current?.());
    await settle();
    // Back on the first page, the new one first and only it fading in.
    const cards = [
      ...container.querySelectorAll<HTMLElement>('[data-slot="story-card"]'),
    ];
    expect(cards.map((card) => card.hasAttribute("data-fresh"))).toStrictEqual([
      true,
      false,
    ]);
    expect(cards[0]!.textContent).toContain("Reply in Chinese");
    expect(cards[0]!.className).toContain("panel-reveal");
  });

  it("opens the memory a conversation asked for, in its project", async () => {
    bridgeMocks.getRequestContextTexts.mockResolvedValue(texts("req", {}));
    await render({
      focus: {
        key: 1,
        project: "~/GolandProjects/astrlink",
        clientType: "codex",
        file: "no-jitter.md",
      },
    });
    expect(bridgeMocks.getProjectMemory).toHaveBeenLastCalledWith({
      project: "~/GolandProjects/astrlink",
      client_type: "codex",
      day_start: DAY_START,
    });
    expect(bridgeMocks.getProjectMemoryEntry).toHaveBeenCalledWith({
      project: "~/GolandProjects/astrlink",
      client_type: "codex",
      day_start: DAY_START,
      file: "no-jitter.md",
    });
    expect(
      document.querySelector('[data-testid="memory-entry-dialog"] h2')
        ?.textContent,
    ).toBe("No jitter-sensitive tests");
  });

  it("switches projects and clients, and reloads on request", async () => {
    const reloadRef: { current: (() => void) | null } = { current: null };
    await render({ reloadRef });
    const options = async (label: string) => {
      await act(async () => {
        trigger(label).dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
        await Promise.resolve();
      });
      return [...document.querySelectorAll<HTMLElement>('[role="option"]')];
    };
    const pick = async (label: string, option: string) => {
      const item = (await options(label)).find(
        (candidate) => optionText(candidate) === option,
      );
      if (!item) throw new Error(`Missing option ${option}`);
      await act(async () => item.click());
      await settleClosedLayer();
      await settle();
    };
    // One client at a time: no option for all of them.
    expect((await options("客户端筛选")).map(optionText)).toStrictEqual([
      "Claude Code",
      "Codex",
    ]);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    await settleClosedLayer();
    await pick("客户端筛选", "Codex");
    expect(bridgeMocks.getProjectMemory).toHaveBeenLastCalledWith({
      client_type: "codex",
      day_start: DAY_START,
    });
    // Another project starts on the client that called there last.
    await pick("项目筛选", "没有项目目录");
    expect(bridgeMocks.getProjectMemory).toHaveBeenLastCalledWith({
      no_project: true,
      day_start: DAY_START,
    });
    const calls = bridgeMocks.getProjectMemory.mock.calls.length;
    await act(async () => reloadRef.current?.());
    expect(bridgeMocks.getProjectMemory).toHaveBeenCalledTimes(calls + 1);
  });

  it("shows a client that keeps no memory by the skills it carries", async () => {
    bridgeMocks.getProjectMemory.mockResolvedValue({
      ...memory,
      client_type: "deepseek_harness",
      recent: 0,
      entries: [],
      stories: [],
      rules: [],
      toolbox: {
        since: SINCE,
        skills: [
          {
            name: "brand-guidelines",
            description: "Apply the brand's colors and type",
            uses: 0,
            carried: true,
          },
          {
            name: "humanizer",
            description: "Rewrite UI copy in plain words",
            uses: 5,
            carried: true,
          },
          { name: "paseo", uses: 2, carried: true },
        ],
        mcp: [],
        idle_tokens: 0,
      },
      has_memory: false,
      has_rules: false,
    });
    await render();
    const view = container.querySelector(
      '[data-testid="memory-empty-client"]',
    )!;
    expect(view.querySelector('[data-slot="empty-state"]')?.textContent).toBe(
      "DeepSeek Harness 在这个项目里还没记过东西请求里没有记忆文件，也没有规则文件。",
    );
    expect(view.textContent).toContain("近 30 天带过 3 个 Skills，用过 2 个");
    expect(
      view.querySelector('[data-testid="memory-toolbox-days"]')?.textContent,
    ).toBe("近 30 天 · 10月1日起");
    expect(texts_('[data-slot="tool-tile"]', view)).toStrictEqual([
      "humanizer用过 5 次Rewrite UI copy in plain words",
      "paseo用过 2 次",
      "brand-guidelines没碰过Apply the brand's colors and type",
    ]);
    // Nothing to count or search.
    expect(container.querySelector('[data-testid="memory-count"]')).toBeNull();
    expect(container.querySelector('input[type="search"]')).toBeNull();
  });

  it("explains an empty view", async () => {
    bridgeMocks.getProjectMemory.mockResolvedValue({
      projects: [],
      day_start: DAY_START,
      recent: 0,
      entries: [],
      stories: [],
      rules: [],
      toolbox: { since: SINCE, skills: [], mcp: [], idle_tokens: 0 },
      has_memory: false,
      has_rules: false,
    });
    await render();
    expect(container.textContent).toContain("还没看到带规则或记忆的调用");
    expect(trigger("项目筛选").disabled).toBe(true);
  });
});
