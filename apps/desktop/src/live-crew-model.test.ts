import { afterEach, describe, expect, it, vi } from "vitest";

import {
  catchUpCrew,
  celebrate,
  CLONE_LIMIT,
  createCrew,
  CREW_LIMIT,
  CREW_STAGES,
  CREW_TOP,
  crewGrowth,
  ENTRY_GAP,
  MIN_HOLD,
  MIN_REST,
  QUEUE_LIMIT,
  readCrew,
  settleCrew,
  stagePassed,
  stepCrew,
  syncCrew,
  toolAct,
  walkSeconds,
  type Crew,
  type CrewAct,
  type CrewLayout,
  type CrewMemberReading,
  type CrewReading,
  type CrewStation,
} from "./live-crew-model";
import { markOf, stepOrb, type Pixel } from "./live-crew-draw";
import {
  createScene,
  CREW_HEIGHT,
  CREW_SCENES,
  DEFAULT_CREW_SCENE,
} from "./live-crew-scenes";
import type {
  RequestActivityCall,
  OutputActivity,
} from "./request-record-model";

const X: Record<CrewStation, number> = {
  bash: 100,
  read: 240,
  edit: 380,
  search: 520,
  write: 660,
  think: 800,
};
const doneActs: CrewAct[] = [];
const layout: CrewLayout = {
  floors: [190],
  door: -40,
  exit: 1066,
  spot: (act, slot) => {
    const off = [-50, 50, -98, 98][slot % 4];
    return { x: X[act] + off, face: off < 0 ? 1 : -1, level: 0 };
  },
  onDone: (act) => doneActs.push(act),
};

function member(
  id: string,
  act: CrewAct,
  tool = "",
  extra: Partial<CrewMemberReading> = {},
): CrewMemberReading {
  return {
    id,
    client: "codex",
    act,
    tool,
    rate: 0,
    model: "",
    clones: [],
    failovers: [],
    since: 0,
    ...extra,
  };
}
const reading = (...members: CrewMemberReading[]): CrewReading => ({
  members,
  rate: 0,
});

/** Steps the crew in 1/30 s frames, calling `each` after every frame. */
function run(crew: Crew, seconds: number, each?: () => void) {
  for (let left = seconds; left > 1e-9; left -= 1 / 30) {
    stepCrew(crew, layout, Math.min(1 / 30, left));
    each?.();
  }
}

afterEach(() => {
  doneActs.length = 0;
  vi.restoreAllMocks();
});

describe("toolAct", () => {
  it.each([
    ["Bash", "bash"],
    ["shell", "bash"],
    ["exec_command", "bash"],
    ["write_stdin", "bash"],
    ["run_terminal_cmd", "bash"],
    ["Read", "read"],
    ["Grep", "read"],
    ["Glob", "read"],
    ["read_file", "read"],
    ["Edit", "edit"],
    ["MultiEdit", "edit"],
    ["Write", "edit"],
    ["apply_patch", "edit"],
    ["WebSearch", "search"],
    ["WebFetch", "search"],
    ["web_search", "search"],
    ["Task", "bash"],
    ["mcp__linear__get_issue", "bash"],
  ])("sends %s to %s", (tool, act) => {
    expect(toolAct(tool)).toBe(act);
  });
});

describe("readCrew", () => {
  const call = (
    session: string,
    started: string,
    extra: Partial<RequestActivityCall> = {},
  ): RequestActivityCall => ({
    session_id: session,
    client_type: "claude_code",
    started_at: `2026-11-02T08:${started}Z`,
    completed_at: null,
    status: "pending",
    ...extra,
  });
  const output = (kind: OutputActivity["kind"], tools: string[] = []) => ({
    kind,
    tools,
  });

  it("reads each conversation's action, its subagents and its failovers", () => {
    const crew = readCrew({
      now: "2026-11-02T08:10:00Z",
      calls: [
        // Ended long ago with an answer: no longer in progress.
        call("old", "00:00", {
          completed_at: "2026-11-02T08:00:05Z",
          status: "succeeded",
          output: output("text"),
        }),
        call("main", "05:00", {
          completed_at: "2026-11-02T08:05:10Z",
          status: "succeeded",
          output: output("tool", ["Task"]),
          failed_service_id: "first",
        }),
        call("sub-a", "06:00", {
          parent_session_id: "main",
          output: output("tool", ["Grep"]),
        }),
        call("sub-b", "07:00", {
          parent_session_id: "main",
          output: output("reasoning"),
        }),
        call("cx", "09:00", {
          client_type: "codex",
          output: output("text"),
        }),
      ],
    });
    expect(crew.members.map((entry) => entry.id)).toEqual(["main", "cx"]);
    const [main, codex] = crew.members;
    // The parent runs the Task tool while its subagents work.
    expect(main).toMatchObject({
      client: "claude_code",
      act: "bash",
      tool: "Task",
      failovers: ["main@2026-11-02T08:05:00Z"],
    });
    expect(main.clones).toEqual([
      { id: "sub-a", act: "read", tool: "Grep" },
      { id: "sub-b", act: "think", tool: "" },
    ]);
    expect(codex).toMatchObject({ client: "codex", act: "write", tool: "" });
  });

  it("reads the model of each conversation's latest call that names one", () => {
    const crew = readCrew({
      now: "2026-11-02T08:10:00Z",
      calls: [
        call("main", "09:00", { model: "" }),
        call("main", "08:00", {
          completed_at: "2026-11-02T08:08:30Z",
          status: "succeeded",
          output: output("tool", ["Read"]),
          model: "deepseek-v4",
        }),
        call("main", "07:00", {
          completed_at: "2026-11-02T08:07:30Z",
          status: "succeeded",
          output: output("tool", ["Read"]),
          model: "kimi-k3",
        }),
        call("sub", "09:30", { parent_session_id: "main", model: "glm-5.3" }),
        call("bare", "09:00"),
      ],
    });
    expect(crew.members.map(({ id, model }) => ({ id, model }))).toEqual([
      { id: "main", model: "deepseek-v4" },
      { id: "bare", model: "" },
    ]);
  });
});

describe("the crew", () => {
  it("shows every step for at least three seconds and never falls behind", () => {
    vi.spyOn(Date, "now");
    vi.spyOn(performance, "now");
    const crew = createCrew();
    const tools = ["Bash", "Read", "Edit", "WebSearch", "Read", "Bash"];
    const shown: Array<{ act: string; at: number }> = [];
    const watch = () => {
      const [only] = crew.members;
      if (only && only.act !== shown.at(-1)?.act) {
        shown.push({ act: only.act, at: crew.time });
      }
      expect(only?.queue.length ?? 0).toBeLessThanOrEqual(QUEUE_LIMIT);
    };
    syncCrew(crew, reading(member("a", "think")), layout);
    // A burst: a new tool every half second for twelve seconds.
    for (let n = 0; n < 24; n += 1) {
      const tool = tools[n % tools.length];
      syncCrew(crew, reading(member("a", toolAct(tool), tool)), layout);
      run(crew, 0.5, watch);
    }
    const lastBurst = crew.time;
    // Nothing flashes: each step stays its hold plus the walk to it.
    for (let n = 1; n < shown.length - 1; n += 1) {
      expect(shown[n + 1].at - shown[n].at).toBeGreaterThanOrEqual(MIN_HOLD);
    }
    // Far fewer steps showed than happened; the middle ones were dropped.
    expect(shown.length).toBeLessThan(10);
    // Once the burst ends, the latest step is on screen within two holds.
    syncCrew(crew, reading(member("a", "edit", "Edit")), layout);
    run(crew, 2 * MIN_HOLD + 3, watch);
    expect(crew.members[0]).toMatchObject({ act: "edit", tool: "Edit" });
    expect(crew.members[0].queue).toEqual([]);
    expect(shown.at(-1)!.at - lastBurst).toBeLessThan(2 * MIN_HOLD + 3);
    expect(Date.now).not.toHaveBeenCalled();
    expect(performance.now).not.toHaveBeenCalled();
  });

  it("folds repeats into ×N, across the brief thinking between calls", () => {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "read", "Read")), layout);
    run(crew, 0.2);
    for (const [act, tool] of [
      ["think", ""],
      ["read", "Grep"],
      ["think", ""],
      ["read", "Read"],
    ] as const) {
      syncCrew(crew, reading(member("a", act, tool)), layout);
      run(crew, 0.2);
    }
    run(crew, MIN_REST + 3);
    expect(crew.members[0]).toMatchObject({
      act: "read",
      tool: "Read",
      count: 3,
    });
    expect(crew.members[0].queue).toEqual([]);
  });

  it("walks at a steady pace, and rests where it stands", () => {
    expect(walkSeconds(30)).toBe(0.9);
    expect(walkSeconds(300)).toBe(2);
    expect(walkSeconds(2000)).toBe(2.8);
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "edit", "Edit")), layout);
    run(crew, MIN_REST + 4);
    const [character] = crew.members;
    expect(character).toMatchObject({ act: "edit", arrived: true });
    const at = character.x;
    expect(Math.abs(at - X.edit)).toBe(50);

    syncCrew(crew, reading(member("a", "wait")), layout);
    const xs: number[] = [];
    run(crew, MIN_HOLD + 10, () => xs.push(character.x));
    expect(character.act).toBe("wait");
    expect(new Set(xs)).toEqual(new Set([at]));
    // The finished edit got its celebration.
    expect(doneActs).toEqual(["edit"]);
  });

  it("brings a conversation that is already resting in by the door", () => {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "wait"), member("b", "wait")), layout);
    const [first, second] = crew.members;
    run(crew, 4);
    for (const character of [first, second]) {
      expect(character).toMatchObject({ act: "wait", arrived: true });
      expect(character.x).toBeGreaterThanOrEqual(20);
    }
    // Side by side, not on top of each other.
    expect(Math.abs(first.x - second.x)).toBeGreaterThan(44);
  });

  it("clears the queue and walks out when the conversation ends", () => {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "think")), layout);
    syncCrew(crew, reading(member("a", "read", "Read")), layout);
    syncCrew(crew, reading(member("a", "edit", "Edit")), layout);
    expect(crew.members[0].queue.length).toBeGreaterThan(0);
    syncCrew(crew, reading(), layout);
    expect(crew.members[0]).toMatchObject({ act: "leave", queue: [] });
    run(crew, 6);
    expect(crew.members).toEqual([]);
  });

  it("takes subagents along as small clones that bring things back", () => {
    const crew = createCrew();
    const clones = [
      { id: "s1", act: "read" as const, tool: "Read" },
      { id: "s2", act: "bash" as const, tool: "Bash" },
      { id: "s3", act: "edit" as const, tool: "Edit" },
    ];
    syncCrew(crew, reading(member("a", "bash", "Task", { clones })), layout);
    const [owner] = crew.members;
    // Codex is drawn at 1.5 px a cell; its clones at half, never below 1.
    expect(owner.u).toBe(1.5);
    expect(owner.clones.map((clone) => [clone.id, clone.u])).toEqual(
      clones.slice(0, CLONE_LIMIT).map((clone) => [clone.id, 1]),
    );
    run(crew, 4);
    expect(owner.clones[0]).toMatchObject({ phase: "work" });
    // It carries something back to its owner, who gets a star for it.
    let carried = false;
    let delivered = false;
    run(crew, 9, () => {
      carried ||= owner.clones[0].phase === "back";
      delivered ||= carried && crew.pops.length > 0;
    });
    expect(delivered).toBe(true);
    syncCrew(crew, reading(member("a", "bash", "Task")), layout);
    expect(owner.clones.every((clone) => clone.phase === "gone")).toBe(true);
    run(crew, 6);
    expect(owner.clones).toEqual([]);
  });

  it("brings newcomers in through the door one at a time", () => {
    const crew = createCrew();
    syncCrew(crew, reading(), layout);
    syncCrew(
      crew,
      reading(member("a", "edit", "Edit"), member("b", "read", "Read")),
      layout,
    );
    syncCrew(
      crew,
      reading(
        member("a", "edit", "Edit"),
        member("b", "read", "Read"),
        member("c", "think"),
      ),
      layout,
    );
    expect(crew.members.every((character) => character.waiting)).toBe(true);
    // They come in a gap apart, in the order their conversations started.
    const entered: Array<[string, number]> = [];
    // Nobody walks on top of anyone on the way in, nor waits at the door.
    let closest = Infinity;
    let atDoor = 0;
    run(crew, 8, () => {
      for (const character of crew.members)
        if (!character.waiting && !entered.some(([id]) => id === character.id))
          entered.push([character.id, crew.time]);
      const inside = crew.members.filter(
        (character) =>
          !character.waiting &&
          (!character.arrived || character.x === layout.door),
      );
      atDoor = Math.max(
        atDoor,
        inside.filter((character) => character.x === layout.door).length,
      );
      for (const one of inside)
        for (const other of inside)
          if (one !== other)
            closest = Math.min(closest, Math.abs(one.x - other.x));
    });
    expect(entered.map(([id]) => id)).toEqual(["a", "b", "c"]);
    expect(entered[1][1] - entered[0][1]).toBeCloseTo(ENTRY_GAP, 1);
    expect(entered[2][1] - entered[1][1]).toBeCloseTo(ENTRY_GAP, 1);
    expect(closest).toBeGreaterThan(40);
    expect(atDoor).toBeLessThanOrEqual(1);
    expect(crew.members.every((character) => character.arrived)).toBe(true);
    // A turn waits only for those before it, not for long-gone arrivals.
    syncCrew(
      crew,
      reading(
        member("a", "edit", "Edit"),
        member("b", "read", "Read"),
        member("c", "think"),
        member("d", "think"),
      ),
      layout,
    );
    run(crew, 1 / 30);
    expect(crew.members.at(-1)?.waiting).toBe(false);
  });

  it("keeps no turn for newcomers that came and went unwatched", () => {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "think")), layout);
    run(crew, 2);
    // Scrolled out of view: reads keep coming but the crew's clock stands.
    for (let n = 0; n < 20; n += 1) {
      catchUpCrew(
        crew,
        reading(member("a", "think"), member(`brief-${n}`, "think")),
        layout,
      );
      catchUpCrew(crew, reading(member("a", "think")), layout);
    }
    catchUpCrew(
      crew,
      reading(member("a", "think"), member("late", "edit", "Edit")),
      layout,
    );
    expect(crew.members.map((character) => character.id)).toEqual([
      "a",
      "late",
    ]);
    // Back in view, it comes in straight away.
    run(crew, 1 / 30);
    expect(crew.members[1].waiting).toBe(false);
  });

  it.each(CREW_SCENES)(
    "gives resting newcomers their own places by the %s door",
    (id) => {
      const { layout: scene } = createScene(id, 1006);
      const apart = (crew: Crew) => {
        for (const one of crew.members)
          for (const other of crew.members)
            if (one !== other && one.level === other.level)
              expect(Math.abs(one.x - other.x)).toBeGreaterThan(40);
      };
      // The first reading finds three conversations between replies.
      const first = createCrew();
      catchUpCrew(
        first,
        reading(
          member("i1", "wait"),
          member("i2", "wait"),
          member("i3", "wait"),
        ),
        scene,
      );
      apart(first);
      // Back on the overview, two that replied meanwhile come in to rest.
      const crew = createCrew();
      const working = [
        member("a", "edit", "Edit"),
        member("b", "read", "Read"),
      ];
      catchUpCrew(crew, reading(...working), scene);
      catchUpCrew(
        crew,
        reading(...working, member("d", "wait"), member("e", "wait")),
        scene,
      );
      for (let left = 8; left > 1e-9; left -= 1 / 30)
        stepCrew(crew, scene, Math.min(1 / 30, left));
      expect(
        crew.members.every(
          (character) => character.arrived && !character.waiting,
        ),
      ).toBe(true);
      apart(crew);
    },
  );

  it("drops a newcomer whose conversation ends before its turn", () => {
    const crew = createCrew();
    syncCrew(crew, reading(), layout);
    syncCrew(crew, reading(member("a", "think"), member("b", "think")), layout);
    syncCrew(crew, reading(member("a", "think")), layout);
    run(crew, 1 / 30);
    expect(crew.members.map((character) => character.id)).toEqual(["a"]);
  });

  it("finds everyone at work on the first reading", () => {
    const crew = createCrew();
    catchUpCrew(
      crew,
      reading(member("a", "edit", "Edit"), member("b", "read", "Read")),
      layout,
    );
    expect(crew.members).toMatchObject([
      { id: "a", act: "edit", arrived: true, x: X.edit - 50, waiting: false },
      { id: "b", act: "read", arrived: true, x: X.read - 50, waiting: false },
    ]);
  });

  it("catches up at once after nobody watched for a while", () => {
    const crew = createCrew();
    catchUpCrew(
      crew,
      reading(
        member("stays", "edit", "Edit"),
        member("moves", "read", "Read"),
        member("ends", "search", "Grep"),
      ),
      layout,
    );
    run(crew, 2);
    // Mid-walk when the overview went away.
    syncCrew(
      crew,
      reading(
        member("stays", "bash", "Bash"),
        member("moves", "read", "Read"),
        member("ends", "search", "Grep"),
      ),
      layout,
    );
    run(crew, MIN_HOLD + 0.4);
    const stays = crew.members.find((character) => character.id === "stays")!;
    expect(stays).toMatchObject({ act: "bash", arrived: false });
    const before = { ...stays };

    catchUpCrew(
      crew,
      reading(
        member("stays", "bash", "Bash"),
        member("moves", "write"),
        member("new-1", "think"),
        member("new-2", "think"),
      ),
      layout,
    );
    // The one whose conversation went on as it was keeps its place and walk.
    expect(stays).toMatchObject({
      x: before.x,
      act: "bash",
      arrived: false,
      move: before.move,
    });
    // The one that moved on stands at its latest step already.
    expect(crew.members.find((c) => c.id === "moves")).toMatchObject({
      act: "write",
      arrived: true,
      x: X.write - 50,
    });
    // The one that ended is gone without walking out.
    expect(crew.members.some((c) => c.id === "ends")).toBe(false);
    // Only the conversations that started meanwhile come in, one by one.
    const fresh = crew.members.filter((c) => c.id.startsWith("new-"));
    expect(fresh.map((c) => [c.x, c.waiting])).toEqual([
      [layout.door, true],
      [layout.door, true],
    ]);
    run(crew, 1 / 30);
    expect(fresh.map((c) => c.waiting)).toEqual([false, true]);
    run(crew, 6);
    expect(crew.members.every((character) => character.arrived)).toBe(true);
    // A failover from while away does not trip anyone on the way back.
    catchUpCrew(
      crew,
      reading(
        member("stays", "bash", "Bash", { failovers: ["stays@1"] }),
        member("moves", "write"),
        member("new-1", "think"),
        member("new-2", "think"),
      ),
      layout,
    );
    expect(crew.members.every((character) => character.slip === 0)).toBe(true);
  });

  it("caps the characters on screen and counts the rest", () => {
    const crew = createCrew();
    const many = Array.from({ length: CREW_LIMIT + 3 }, (_, n) =>
      member(`c${n}`, "think"),
    );
    syncCrew(crew, reading(...many), layout);
    expect(crew.members).toHaveLength(CREW_LIMIT);
    expect(crew.overflow).toBe(3);
  });

  it("trips a character only for a failover that happens while watching", () => {
    const crew = createCrew();
    syncCrew(
      crew,
      reading(member("a", "think", "", { failovers: ["a@1"] })),
      layout,
    );
    expect(crew.members[0].slip).toBe(0);
    syncCrew(
      crew,
      reading(member("a", "think", "", { failovers: ["a@1", "a@2"] })),
      layout,
    );
    expect(crew.members[0].slip).toBeGreaterThan(0);
  });

  it("settles at once without motion", () => {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "think")), layout);
    syncCrew(crew, reading(member("a", "search", "WebFetch")), layout);
    settleCrew(crew, layout);
    expect(crew.members[0]).toMatchObject({
      act: "search",
      arrived: true,
      x: X.search - 50,
    });
  });

  it("grows a stage at a time on a log scale", () => {
    expect(crewGrowth(null)).toEqual({ stage: 0, level: 0 });
    expect(crewGrowth(10_000)).toEqual({ stage: 0, level: 0 });
    expect(crewGrowth(100_000)).toEqual({ stage: 1, level: 1 });
    expect(crewGrowth(Math.sqrt(2e6 * 1e7)).level).toBeCloseTo(3.5);
    expect(crewGrowth(999_999_999).stage).toBe(CREW_TOP - 1);
    expect(crewGrowth(5e9)).toEqual({ stage: CREW_TOP, level: CREW_TOP });
  });

  it("celebrates stages passed while watching only", () => {
    expect(stagePassed(null, 12_000_000)).toBeNull();
    expect(stagePassed(9_900_000, 10_100_000)).toBe(4);
    expect(CREW_STAGES[4]).toBe(10_000_000);
    expect(stagePassed(1_000_000, 60_000_000)).toBe(5);
    expect(stagePassed(10_100_000, 49_000_000)).toBeNull();
    expect(stagePassed(12_000_000, null)).toBeNull();
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "think")), layout);
    celebrate(crew, "today");
    expect(crew).toMatchObject({ banner: 1, bannerText: "today" });
    expect(crew.members[0].jump).toBe(1);
    expect(crew.confetti).toHaveLength(60);
  });
});

describe("the model orb", () => {
  function joined(model: string) {
    const crew = createCrew();
    syncCrew(crew, reading(member("a", "think", "", { model })), layout);
    settleCrew(crew, layout);
    return crew;
  }

  it("rides along only when a model is known", () => {
    const fx: Pixel[] = [];
    const crew = joined("");
    stepOrb(crew.members[0], 1 / 30, fx, crew.rand);
    expect(crew.members[0].orb).toBeNull();
    syncCrew(
      crew,
      reading(member("a", "think", "", { model: "kimi-k3" })),
      layout,
    );
    stepOrb(crew.members[0], 1 / 30, fx, crew.rand);
    // Appearing for the first time bursts nothing.
    expect(crew.members[0].orb).toMatchObject({ model: "kimi-k3", in: 1 });
    expect(fx).toHaveLength(0);
  });

  it("bursts the old orb and flies the new one in on a model change", () => {
    const fx: Pixel[] = [];
    const crew = joined("kimi-k3");
    const [owner] = crew.members;
    stepOrb(owner, 1 / 30, fx, crew.rand);
    const before = owner.orb!.y;
    syncCrew(
      crew,
      reading(member("a", "think", "", { model: "deepseek-v4" })),
      layout,
    );
    stepOrb(owner, 1 / 30, fx, crew.rand);
    expect(fx).toHaveLength(14);
    expect(owner.orb).toMatchObject({ model: "deepseek-v4" });
    expect(owner.orb!.in).toBeLessThan(0.1);
    expect(owner.orb!.y).toBeLessThan(before - 20);
  });

  it("keeps up with a walk and dozes while waiting", () => {
    const fx: Pixel[] = [];
    const model = "claude-opus-5-5";
    const crew = joined(model);
    const [owner] = crew.members;
    // Codex stands 30 cells wide at 1.5 px: the orb flies 30.5 px aside.
    const side = (30 * 1.5) / 2 + 8;
    let walked = 0;
    const follow = () => {
      stepOrb(owner, 1 / 30, fx, crew.rand);
      const gap = Math.abs(owner.orb!.x - owner.x);
      if (!owner.arrived) {
        walked += 1;
        expect(gap).toBeLessThanOrEqual(side + 30 + 1e-6);
      }
      expect(gap).toBeLessThanOrEqual(side + 2 + 80 + 1e-6);
    };
    syncCrew(crew, reading(member("a", "edit", "Edit", { model })), layout);
    run(crew, MIN_HOLD + 6, follow);
    expect(walked).toBeGreaterThan(0);
    expect(owner).toMatchObject({ act: "edit", arrived: true });
    syncCrew(crew, reading(member("a", "wait", "", { model })), layout);
    run(crew, MIN_HOLD + 2, follow);
    expect(owner.orb).toMatchObject({ dim: true, glow: false });
  });

  it("keeps still beside the shoulder, ringed only while writing", () => {
    const fx: Pixel[] = [];
    const model = "claude-opus-5-5";
    const crew = joined(model);
    const [owner] = crew.members;
    const step = () => stepOrb(owner, 1 / 30, fx, crew.rand);
    for (const act of ["think", "write", "bash"] as const) {
      syncCrew(crew, reading(member("a", act, "", { model })), layout);
      run(crew, MIN_HOLD + 3, step);
      expect(owner.arrived).toBe(true);
      const at = { x: owner.orb!.x, y: owner.orb!.y };
      // No circling, bobbing or sparks: it does not move while she works.
      run(crew, 2, () => {
        step();
        expect(Math.abs(owner.orb!.x - at.x)).toBeLessThan(0.5);
        expect(Math.abs(owner.orb!.y - at.y)).toBeLessThan(0.5);
      });
      expect(owner.orb!.glow).toBe(act === "write");
    }
    expect(fx).toHaveLength(0);
  });

  it("wears its vendor's mark, or the Brain's", () => {
    expect(markOf("claude-opus-5-5")).toBe("claude");
    expect(markOf("gpt-5.4")).toBe("openai");
    expect(markOf("some-local-model")).toBe("brain");
  });
});

describe("the scenes", () => {
  it("opens in the camp", () => {
    expect(DEFAULT_CREW_SCENE).toBe("camp");
  });

  it.each(CREW_SCENES)("%s bakes again only when it grows", (id) => {
    const scene = createScene(id, 1006);
    const keys = CREW_STAGES.map((tokens) => scene.grow(crewGrowth(tokens)));
    expect(new Set(keys).size).toBe(CREW_STAGES.length);
    expect(scene.grow(crewGrowth(CREW_STAGES[3]))).toBe(keys[3]);
    expect(scene.grow(crewGrowth(CREW_STAGES[3] + 1))).toBe(keys[3]);
  });

  it.each(CREW_SCENES)(
    "%s keeps the shared height and its places on the canvas",
    (id) => {
      // The logical widths the canvas takes, from 880 at 1x upward.
      for (const width of [880, 1006, 1208, 1400]) {
        const scene = createScene(id, width);
        expect(scene.H).toBe(CREW_HEIGHT);
        for (const act of [
          "bash",
          "read",
          "edit",
          "search",
          "write",
          "think",
        ] as const) {
          for (let slot = 0; slot < CREW_LIMIT; slot += 1) {
            const { x } = scene.layout.spot(act, slot);
            expect(x).toBeGreaterThanOrEqual(24);
            expect(x).toBeLessThanOrEqual(width - 24);
          }
        }
      }
    },
  );
});
