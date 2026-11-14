// Sample conversations: the settings scene plays them when nothing is in
// progress, and debug builds can feed the pet from them.
import type {
  CrewAct,
  CrewMemberReading,
  CrewReading,
} from "../live-crew-model";
import type { ClientType } from "../request-record-model";

type Kind = "tool" | "think" | "write" | "wait";
type Step = [Kind, string | number, number];
interface Def {
  id: string;
  client: ClientType;
  models: string[];
  script: Step[];
  life?: { every: number; from: number; to: number };
  fail?: number;
}
const DEFS: Def[] = [
  {
    id: "sample-cc",
    client: "claude_code",
    models: ["claude-opus-5-5"],
    script: [
      ["tool", "Bash", 7],
      ["think", 0, 5],
      ["write", 110, 7],
      ["tool", "Read", 5],
      ["tool", "Edit", 6],
      ["think", 0, 4],
      ["tool", "WebSearch", 6],
      ["write", 150, 6],
      ["wait", 0, 10],
    ],
  },
  {
    id: "sample-codex",
    client: "codex",
    models: ["gpt-5.5"],
    script: [
      ["think", 0, 6],
      ["tool", "Edit", 7],
      ["tool", "Bash", 0.8],
      ["think", 0, 4],
      ["write", 95, 8],
      ["wait", 0, 10],
    ],
  },
  {
    id: "sample-ds",
    client: "deepseek_harness",
    models: ["kimi-k3", "deepseek-v4"],
    script: [
      ["tool", "Read", 6],
      ["think", 0, 5],
      ["write", 86, 8],
      ["tool", "WebSearch", 1.2],
      ["tool", "Bash", 5],
      ["write", 125, 6],
      ["wait", 0, 9],
    ],
    fail: 45,
  },
];
const ACT_OF: Record<string, CrewAct> = {
  Bash: "bash",
  Read: "read",
  Edit: "edit",
  WebSearch: "search",
};

export class Sample {
  T = 0;
  convos = DEFS.map((d) => ({
    ...d,
    alive: false,
    idx: 0,
    left: 0,
    m: 0,
    since: 0,
    fails: [] as string[],
  }));

  constructor(public count = DEFS.length) {}

  step(dt: number) {
    this.T += dt;
    for (const c of this.convos.slice(0, this.count)) {
      if (!c.alive) {
        c.alive = true;
        c.idx = 0;
        c.left = c.script[0][2];
        c.since = Date.now();
      }
      c.left -= dt;
      while (c.left <= 0) {
        c.idx += 1;
        if (c.idx >= c.script.length) {
          c.idx = 0;
          c.m = (c.m + 1) % c.models.length;
        }
        c.left += c.script[c.idx][2];
      }
      if (c.fail && Math.floor(this.T / c.fail) > c.fails.length)
        c.fails.push(`${c.id}-f${c.fails.length}`);
    }
  }

  reading(): CrewReading {
    const members: CrewMemberReading[] = [];
    let rate = 0;
    for (const c of this.convos.slice(0, this.count)) {
      if (!c.alive) continue;
      const [kind, arg] = c.script[c.idx];
      const act: CrewAct = kind === "tool" ? ACT_OF[arg as string] : kind;
      const r =
        kind === "write"
          ? (arg as number) *
            (0.85 + 0.25 * Math.sin(this.T * 1.7 + c.id.length))
          : 0;
      rate += r;
      members.push({
        id: c.id,
        client: c.client,
        act,
        tool: kind === "tool" ? (arg as string) : "",
        rate: r,
        model: c.models[c.m],
        clones: [],
        failovers: c.fails.slice(-4),
        since: c.since,
      });
    }
    return { members, rate };
  }
}
