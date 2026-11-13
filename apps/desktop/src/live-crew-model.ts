import { crownOf, smallU, spriteOf } from "./live-crew-sprites";
import {
  busyUntil,
  laneAction,
  outputThroughput,
  rootOf,
  type LaneAction,
} from "./request-activity-model";
import type {
  ClientType,
  RequestActivity,
  RequestActivityCall,
} from "./request-record-model";

/**
 * The live crew: one character per conversation in progress, acting out what
 * it does at a station of the scene. This module holds the readings, the
 * queue that paces what a character shows, and the time step; it draws
 * nothing and reads no clock, so tests drive it with any `dt`.
 */

/** What a character does: a tool's kind, or what the model does. */
export const CREW_ACTS = [
  "bash",
  "read",
  "edit",
  "search",
  "write",
  "think",
  "wait",
] as const;
export type CrewAct = (typeof CREW_ACTS)[number];
export type CrewStation = Exclude<CrewAct, "wait">;

/** A step stays on screen at least this long once its character arrives. */
export const MIN_HOLD = 3;
/** Rest between calls lasts at least this long before the next step. */
export const MIN_REST = 1;
/** Ladder speed in px/s. */
export const CLIMB = 62;
/** Walks take distance / `WALK_SPEED` seconds, within the bounds below. */
export const WALK_SPEED = 150;
export const WALK_MIN = 0.9;
export const WALK_MAX = 2.8;
/** Characters on screen at once; the rest show as `+N`. */
export const CREW_LIMIT = 6;
/** Subagent clones a character takes along. */
export const CLONE_LIMIT = 2;
/** Steps waiting behind the current one; a burst keeps only the newest. */
export const QUEUE_LIMIT = 2;
/** Seconds between characters coming in through the door, one at a time. */
export const ENTRY_GAP = 1;
/**
 * Today's tokens set how far each scene has come: round numbers on a log
 * scale, from nothing to a billion.
 */
export const CREW_STAGES = [
  0, 100_000, 500_000, 2_000_000, 10_000_000, 50_000_000, 200_000_000,
  1_000_000_000,
] as const;
export const CREW_TOP = CREW_STAGES.length - 1;

const WEB = new Set(["web", "fetch", "browse", "browser", "url", "http"]);
const SHELL = new Set([
  "bash",
  "shell",
  "sh",
  "exec",
  "stdin",
  "terminal",
  "command",
  "run",
  "powershell",
  "cmd",
]);
const EDIT = new Set([
  "edit",
  "write",
  "patch",
  "replace",
  "create",
  "insert",
  "delete",
  "rename",
]);
const READ = new Set([
  "read",
  "grep",
  "glob",
  "ls",
  "list",
  "view",
  "cat",
  "find",
  "search",
  "open",
]);

/** A tool name in lower-case words: `WebSearch` and `web_search` alike. */
function toolWords(name: string): string[] {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

/**
 * The station a tool sends its character to: commands to the terminal,
 * reads, edits and web searches to theirs. Any other tool runs like a
 * command; the bubble still shows its real name.
 */
export function toolAct(name: string): Exclude<CrewStation, "write" | "think"> {
  const words = toolWords(name);
  const has = (set: ReadonlySet<string>) => words.some((word) => set.has(word));
  if (has(WEB)) return "search";
  if (has(SHELL)) return "bash";
  if (has(EDIT)) return "edit";
  if (has(READ)) return "read";
  return "bash";
}

/** What a conversation's lane action looks like on stage. */
export function crewAction(action: LaneAction): { act: CrewAct; tool: string } {
  switch (action.kind) {
    case "waiting":
    case "thinking":
      return { act: "think", tool: "" };
    case "writing":
      return { act: "write", tool: "" };
    case "calling":
      return action.tool
        ? { act: toolAct(action.tool), tool: action.tool }
        : { act: "think", tool: "" };
    case "running": {
      const tool = action.tools.at(-1) ?? "";
      return tool ? { act: toolAct(tool), tool } : { act: "think", tool: "" };
    }
    default:
      return { act: "wait", tool: "" };
  }
}

export interface CrewCloneReading {
  id: string;
  act: CrewAct;
  tool: string;
}

export interface CrewMemberReading {
  /** The top-level conversation. */
  id: string;
  client: ClientType;
  act: CrewAct;
  /** The real tool name for a tool step; empty otherwise. */
  tool: string;
  /** Output tokens per second over the last minute. */
  rate: number;
  /** The model its latest call ran with; empty when none is known. */
  model: string;
  /** Subagents in progress, oldest first. */
  clones: CrewCloneReading[];
  /** Calls that failed over to another provider, by call. */
  failovers: string[];
  /** When the conversation's first listed call started. */
  since: number;
}

export interface CrewReading {
  /** Conversations in progress, oldest first. */
  members: CrewMemberReading[];
  /** Output tokens per second over the last minute, all calls. */
  rate: number;
}

const callKey = (call: RequestActivityCall) =>
  `${call.session_id}@${call.started_at}`;

/** A conversation's action from its own calls, as its lane says it. */
function ownAction(calls: readonly RequestActivityCall[]) {
  const latest = calls.at(-1);
  if (!latest) return { act: "wait" as const, tool: "" };
  const running = calls.filter((call) => call.completed_at === null);
  return crewAction(
    laneAction({
      status: running.length > 0 ? "pending" : latest.status,
      active_request_starts: running.map((call) => call.started_at),
      output: (running.at(-1) ?? latest).output,
    }),
  );
}

/**
 * The conversations in progress at the activity's `now`, as the board counts
 * them: a conversation is in progress while a call runs and for its grace or
 * tool wait after. Subagents go with the conversation that started them.
 */
export function readCrew(activity: RequestActivity): CrewReading {
  const now = Date.parse(activity.now);
  const bySession = new Map<string, RequestActivityCall[]>();
  const parentOf = new Map<string, string>();
  for (const call of activity.calls) {
    const own = bySession.get(call.session_id);
    if (own) own.push(call);
    else bySession.set(call.session_id, [call]);
    if (call.parent_session_id) {
      parentOf.set(call.session_id, call.parent_session_id);
    }
  }
  const trees = new Map<
    string,
    { live: Set<string>; calls: RequestActivityCall[] }
  >();
  for (const [session, own] of bySession) {
    own.sort(
      (left, right) =>
        Date.parse(left.started_at) - Date.parse(right.started_at),
    );
    const root = rootOf(parentOf, session);
    const tree = trees.get(root) ?? { live: new Set<string>(), calls: [] };
    trees.set(root, tree);
    tree.calls.push(...own);
    const live = own.some((call) => {
      const start = Date.parse(call.started_at);
      return start <= now && now < busyUntil(call, own);
    });
    if (live) tree.live.add(session);
  }
  const members: CrewMemberReading[] = [];
  for (const [root, tree] of trees) {
    if (tree.live.size === 0) continue;
    tree.calls.sort(
      (left, right) =>
        Date.parse(left.started_at) - Date.parse(right.started_at),
    );
    const own = bySession.get(root) ?? [];
    const clones = [...tree.live]
      .filter((session) => session !== root)
      .map((session) => ({
        id: session,
        since: Date.parse(bySession.get(session)![0].started_at),
        ...ownAction(bySession.get(session)!),
      }))
      .sort(
        (left, right) =>
          left.since - right.since || left.id.localeCompare(right.id),
      )
      .map(({ id, act, tool }) => ({ id, act, tool }));
    members.push({
      id: root,
      client: tree.calls.at(-1)?.client_type ?? "unknown",
      ...ownAction(own),
      rate: outputThroughput(tree.calls, now),
      model: latestModel(own),
      clones,
      failovers: tree.calls
        .filter((call) => call.failed_service_id)
        .map(callKey),
      since: Date.parse(tree.calls[0].started_at),
    });
  }
  members.sort(
    (left, right) =>
      left.since - right.since || left.id.localeCompare(right.id),
  );
  return { members, rate: outputThroughput(activity.calls, now) };
}

/** The model of a conversation's latest call that names one. */
function latestModel(calls: readonly RequestActivityCall[]): string {
  for (let n = calls.length - 1; n >= 0; n -= 1) {
    const model = calls[n].model;
    if (model) return model;
  }
  return "";
}

/**
 * How far today's tokens have come: the stage reached, and `level`, the stage
 * plus how far it is on its way to the next one on a log scale.
 */
export interface CrewGrowth {
  stage: number;
  level: number;
}

export function crewGrowth(tokens: number | null): CrewGrowth {
  const value = Math.max(0, tokens ?? 0);
  let stage = 0;
  while (stage < CREW_TOP && value >= CREW_STAGES[stage + 1]) stage += 1;
  if (stage === CREW_TOP) return { stage, level: CREW_TOP };
  const low = CREW_STAGES[stage] || 10_000;
  const high = CREW_STAGES[stage + 1];
  const part = Math.log(Math.max(value, low) / low) / Math.log(high / low);
  return { stage, level: stage + clamp(part) };
}

/**
 * The stage today's tokens just reached, or null. The first read of a page
 * has nothing to compare with and passes nothing.
 */
export function stagePassed(
  previous: number | null,
  next: number | null,
): number | null {
  if (previous === null || next === null) return null;
  const reached = crewGrowth(next).stage;
  return reached > crewGrowth(previous).stage ? reached : null;
}

/** Where a character stands: a floor, a place on it, and the way it faces. */
export interface CrewSpot {
  x: number;
  level: number;
  face?: 1 | -1;
  /** The site's excavator seat: the driver sits inside, out of sight. */
  drive?: boolean;
  /** The way out; reaching it removes the character. */
  exit?: boolean;
}

/** The places a scene offers; the scene draws them. */
export interface CrewLayout {
  /** Floor lines, top down; characters stand with their feet on them. */
  floors: readonly number[];
  /** Where the ladder between floors stands, if there are two. */
  ladder?: number;
  /** Where characters come in and go out, on floor `doorLevel`. */
  door: number;
  exit: number;
  doorLevel?: number;
  /** The `slot`th place at a station, up to `CREW_LIMIT` of them. */
  spot(act: CrewStation, slot: number): CrewSpot;
  /** A character finished a step at `act`. */
  onDone?(act: CrewAct): void;
}

interface Move {
  from: number;
  to: number;
  t: number;
  dur: number;
}

/** Anything that walks and climbs. */
export interface CrewWalker {
  x: number;
  level: number;
  /** Foot line; between floors while climbing. */
  fy: number;
  face: 1 | -1;
  /** Advances with each step, for the legs. */
  walk: number;
  /** Seconds into what it does now. */
  t: number;
  move: Move | null;
  climb: boolean;
  /** Art pixel size: its sprite's, halved for clones. */
  u: number;
}

export interface CrewStep {
  act: CrewAct;
  tool: string;
  count: number;
  rate: number;
  done?: boolean;
}

export interface CrewClone extends CrewWalker {
  id: string;
  act: CrewAct;
  tool: string;
  /** Out to a station, working there, carrying it back, or heading home. */
  phase: "go" | "work" | "back" | "gone";
  /** Reached its owner after its subagent ended. */
  home: boolean;
  goal: CrewSpot | null;
  /** Crew time to head back with what it fetched. */
  next: number;
  slot: number;
}

/** The orb that shows a character's model. */
export interface CrewOrb {
  x: number;
  y: number;
  /** Fade-in after a model change, 0 → 1. */
  in: number;
  /** The model it shows. */
  model: string;
  /** Ringed: the model is writing. */
  glow: boolean;
  /** Dimmed: the conversation waits. */
  dim: boolean;
}

export interface CrewMember extends CrewWalker {
  id: string;
  client: ClientType;
  /** The model its conversation runs on now; empty when none is known. */
  model: string;
  /** Its model's orb, once drawn; null without a model. */
  orb: CrewOrb | null;
  /** When it joined, among everyone so far: sets apart its motions. */
  order: number;
  act: CrewAct | "leave";
  tool: string;
  count: number;
  rate: number;
  /** Seconds shown since arriving. */
  shown: number;
  /**
   * Waiting its turn behind the door, so newcomers come in one at a time;
   * until then it is neither drawn nor moves.
   */
  waiting: boolean;
  queue: CrewStep[];
  /** The step on screen ended; celebrate once it has been shown. */
  done: boolean;
  /** Celebration jump, 1 → 0. */
  jump: number;
  /** Failover slip, seconds left. */
  slip: number;
  spot: CrewSpot | null;
  arrived: boolean;
  lastKey: string;
  /** The act its conversation last read as. */
  reading: CrewAct;
  clones: CrewClone[];
  leaving: boolean;
  /** Name tag opacity, fading in and out. */
  alpha: number;
  /** Drawing offset while a scene animates it in place. */
  dx?: number;
  /** Scratch the scenes keep per character. */
  beat?: number;
}

export interface CrewPop {
  x: number;
  y: number;
  t: number;
  client: ClientType;
}

export interface CrewConfetti {
  x: number;
  y: number;
  vx: number;
  vy: number;
  /** Index into the scene's confetti colors. */
  color: number;
}

export interface Crew {
  members: CrewMember[];
  pops: CrewPop[];
  confetti: CrewConfetti[];
  /** Banner fade, 1 → 0, and what it says. */
  banner: number;
  bannerText: string;
  /** Seconds since the crew started; drives every animation. */
  time: number;
  /** Conversations in progress beyond `CREW_LIMIT`. */
  overflow: number;
  rand: () => number;
  failovers: Set<string>;
  /** Whether a reading came in; the first one only sets what was seen. */
  primed: boolean;
  /** Logical scene size, for confetti. */
  width: number;
  height: number;
  /** Characters that joined so far. */
  joined: number;
  /** Crew time the last newcomer came in through the door. */
  entered: number;
}

/** A small seeded generator, so a test or a frame sequence repeats. */
export function seededRandom(seed: number): () => number {
  let a = seed | 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function createCrew(rand: () => number = seededRandom(5)): Crew {
  return {
    members: [],
    pops: [],
    confetti: [],
    banner: 0,
    bannerText: "",
    time: 0,
    overflow: 0,
    rand,
    failovers: new Set(),
    primed: false,
    width: 1006,
    height: 280,
    joined: 0,
    entered: -Infinity,
  };
}

const clamp = (value: number, low = 0, high = 1) =>
  Math.min(high, Math.max(low, value));
const easeInOut = (t: number) =>
  t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2;

/** How long a walk of `distance` px takes. */
export function walkSeconds(distance: number): number {
  return clamp(Math.abs(distance) / WALK_SPEED, WALK_MIN, WALK_MAX);
}

/** Walks at a steady pace with a soft start and stop, however far it is. */
function walkTo(walker: CrewWalker, to: number, dt: number): boolean {
  if (!walker.move || walker.move.to !== to) {
    if (Math.abs(to - walker.x) <= 1) {
      walker.x = to;
      walker.move = null;
      return true;
    }
    walker.move = { from: walker.x, to, t: 0, dur: walkSeconds(to - walker.x) };
  }
  const move = walker.move;
  move.t += dt;
  const k = clamp(move.t / move.dur);
  walker.x = move.from + (move.to - move.from) * easeInOut(k);
  walker.face = move.to >= move.from ? 1 : -1;
  walker.walk += dt * 7;
  if (k >= 1) {
    walker.x = move.to;
    walker.move = null;
    return true;
  }
  return false;
}

/** Walks to `x` on floor `level`, by the ladder when it is another floor. */
function go(
  walker: CrewWalker,
  layout: CrewLayout,
  x: number,
  level: number,
  dt: number,
): boolean {
  if (level !== walker.level && layout.ladder !== undefined) {
    if (!walkTo(walker, layout.ladder, dt)) return false;
    const target = layout.floors[level];
    walker.climb = true;
    walker.fy +=
      Math.sign(target - walker.fy) *
      Math.min(Math.abs(target - walker.fy), CLIMB * dt);
    walker.walk += dt * 5;
    if (Math.abs(walker.fy - target) < 1) {
      walker.fy = target;
      walker.level = level;
      walker.climb = false;
    }
    return false;
  }
  return walkTo(walker, x, dt);
}

/** A station's first place nobody else stands near. */
function claim(
  crew: Crew,
  member: CrewMember,
  layout: CrewLayout,
  act: CrewStation,
): CrewSpot {
  for (let slot = 0; slot < CREW_LIMIT; slot += 1) {
    const spot = layout.spot(act, slot);
    const free = crew.members.every(
      (other) =>
        other === member ||
        other.leaving ||
        !other.spot ||
        other.spot.level !== spot.level ||
        Math.abs(other.spot.x - spot.x) > ROOM,
    );
    if (free) return spot;
  }
  return layout.spot(act, 0);
}

/** Closer than this to someone else's place is taken. */
const ROOM = 44;
/** How far inside the edges a character counts as in the scene. */
const INSIDE = 20;

/**
 * Where a character rests: where it stands, or just inside the door when it
 * has not come in yet, beside anyone already resting there. One still behind
 * the door has not come in, even where the door is inside the scene.
 */
function rest(crew: Crew, member: CrewMember, layout: CrewLayout) {
  member.move = null;
  if (
    !member.waiting &&
    member.x >= INSIDE &&
    member.x <= crew.width - INSIDE
  ) {
    member.spot = { x: member.x, level: member.level };
    member.arrived = true;
    return;
  }
  const level = layout.doorLevel ?? 0;
  const door = clamp(layout.door, 0, crew.width);
  const inward = door < crew.width / 2 ? 1 : -1;
  let x = door + inward * 24;
  for (let n = 0; n < 8; n += 1) {
    const at = door + inward * (24 + n * (ROOM + 4));
    const free = crew.members.every(
      (other) =>
        other === member ||
        other.leaving ||
        !other.spot ||
        other.spot.level !== level ||
        Math.abs(other.spot.x - at) > ROOM,
    );
    if (free) {
      x = at;
      break;
    }
  }
  member.spot = { x, level };
  member.arrived = false;
}

function pop(crew: Crew, member: CrewMember) {
  crew.pops.push({
    x: member.x,
    y: crownOf(member, member.client) - 18,
    t: 0,
    client: member.client,
  });
}

/** The character finished what it showed: a jump and a star. */
function finish(crew: Crew, member: CrewMember, layout: CrewLayout) {
  member.done = false;
  member.jump = 1;
  pop(crew, member);
  if (member.act !== "leave") layout.onDone?.(member.act);
}

/** Puts the next step on screen and sends the character to its station. */
function show(
  crew: Crew,
  member: CrewMember,
  layout: CrewLayout,
  step: CrewStep,
) {
  if (member.done) finish(crew, member, layout);
  member.act = step.act;
  member.tool = step.tool;
  member.count = step.count;
  member.rate = step.rate;
  member.done = step.done ?? false;
  member.t = 0;
  member.shown = 0;
  member.move = null;
  if (step.act === "wait") rest(crew, member, layout);
  else {
    member.spot = claim(crew, member, layout, step.act);
    member.arrived = false;
  }
}

function walker(x: number, level: number, fy: number, u: number): CrewWalker {
  return { x, level, fy, face: 1, walk: 0, t: 0, move: null, climb: false, u };
}

function joinMember(
  crew: Crew,
  reading: CrewMemberReading,
  layout: CrewLayout,
): CrewMember {
  const level = layout.doorLevel ?? 0;
  crew.joined += 1;
  return {
    ...walker(
      layout.door,
      level,
      layout.floors[level],
      spriteOf(reading.client).u,
    ),
    id: reading.id,
    client: reading.client,
    model: reading.model,
    orb: null,
    order: crew.joined,
    act: "wait",
    tool: "",
    count: 1,
    rate: 0,
    // Its turn at the door comes: it heads for its first step at once.
    shown: MIN_REST,
    waiting: true,
    queue: [],
    done: false,
    jump: 0,
    slip: 0,
    spot: null,
    arrived: true,
    lastKey: "",
    reading: "wait",
    clones: [],
    leaving: false,
    alpha: 0,
  };
}

/** Queues what the conversation does now, the way the demo paces it. */
function enqueue(member: CrewMember, reading: CrewMemberReading) {
  const key = `${reading.act}:${reading.tool}`;
  if (key !== member.lastKey) {
    // A tool run or a reply that ended gets its celebration once shown.
    const ended = member.reading;
    if (
      member.lastKey &&
      ended !== "think" &&
      ended !== "wait" &&
      ended !== reading.act
    ) {
      const tail = member.queue.at(-1);
      if (tail) tail.done = true;
      else member.done = true;
    }
    member.lastKey = key;
    member.reading = reading.act;
    const queue = member.queue;
    // A model thinks briefly before each call; a think nobody saw yet does
    // not split a run of the same step.
    const brief = queue.at(-1)?.act === "think" && reading.act !== "think";
    const before = brief ? queue.at(-2) : queue.at(-1);
    // Repeats add up; a burst keeps only its newest steps.
    if (before?.act === reading.act) {
      if (brief) queue.pop();
      before.count += 1;
      before.tool = reading.tool;
    } else if (
      member.act === reading.act &&
      (queue.length === 0 || (brief && queue.length === 1))
    ) {
      queue.length = 0;
      member.count += 1;
      member.tool = reading.tool;
    } else {
      queue.push({ act: reading.act, tool: reading.tool, count: 1, rate: 0 });
    }
    if (member.queue.length > QUEUE_LIMIT) {
      member.queue.splice(0, member.queue.length - QUEUE_LIMIT);
    }
  }
  const latest = member.queue.at(-1);
  if (latest?.act === "write")
    latest.rate = Math.max(latest.rate, reading.rate);
  if (member.act === "write" && member.queue.length === 0)
    member.rate = reading.rate;
}

function syncClones(
  member: CrewMember,
  readings: readonly CrewCloneReading[],
  crew: Crew,
) {
  const shown = readings.slice(0, CLONE_LIMIT);
  for (const clone of member.clones) {
    const reading = shown.find((candidate) => candidate.id === clone.id);
    if (!reading) clone.phase = "gone";
    else {
      clone.act = reading.act;
      clone.tool = reading.tool;
    }
  }
  for (const reading of shown) {
    if (member.clones.some((clone) => clone.id === reading.id)) continue;
    const slot = member.clones.length;
    member.clones.push({
      ...walker(
        member.x + (slot ? 20 : -20),
        member.level,
        member.fy,
        smallU(member.u),
      ),
      id: reading.id,
      act: reading.act,
      tool: reading.tool,
      phase: "go",
      home: false,
      goal: null,
      next: crew.time + 3 + slot,
      slot,
    });
  }
}

/**
 * Brings the crew in line with a reading: characters come in for new
 * conversations and leave for ended ones, steps queue, subagents become
 * clones, and new failovers trip their character.
 */
export function syncCrew(crew: Crew, reading: CrewReading, layout: CrewLayout) {
  const live = new Map(reading.members.map((member) => [member.id, member]));
  for (const member of crew.members) {
    if (live.has(member.id)) {
      if (member.leaving) {
        // Back before it got out: it stays where it is.
        member.leaving = false;
        member.act = "wait";
        member.lastKey = "";
        member.reading = "wait";
        member.shown = 0;
        rest(crew, member, layout);
      }
      continue;
    }
    if (member.leaving) continue;
    member.leaving = true;
    member.queue = [];
    member.done = false;
    member.act = "leave";
    member.spot = { x: layout.exit, level: layout.doorLevel ?? 0, exit: true };
    member.arrived = false;
    member.move = null;
    for (const clone of member.clones) clone.phase = "gone";
  }
  let active = crew.members.filter((member) => !member.leaving).length;
  for (const next of reading.members) {
    if (crew.members.some((member) => member.id === next.id)) continue;
    if (active >= CREW_LIMIT) continue;
    crew.members.push(joinMember(crew, next, layout));
    active += 1;
  }
  const shown = new Set(crew.members.map((member) => member.id));
  crew.overflow = reading.members.filter(
    (member) => !shown.has(member.id),
  ).length;
  for (const member of crew.members) {
    const next = live.get(member.id);
    if (!next || member.leaving) continue;
    if (member.client !== next.client) {
      member.client = next.client;
      member.u = spriteOf(next.client).u;
    }
    member.model = next.model;
    enqueue(member, next);
    // Resting when it first shows up: it comes in and rests by the door.
    if (!member.spot && member.act === "wait" && member.queue.length === 0) {
      rest(crew, member, layout);
    }
    syncClones(member, next.clones, crew);
  }
  // A call that failed over trips its character once; calls from before the
  // first reading only count as seen.
  const seen = new Set<string>();
  for (const next of reading.members) {
    const member = crew.members.find((candidate) => candidate.id === next.id);
    for (const key of next.failovers) {
      seen.add(key);
      if (crew.failovers.has(key) || !crew.primed) continue;
      if (member && !member.leaving) member.slip = 1.3;
    }
  }
  crew.failovers = seen;
  crew.primed = true;
}

function stepClones(
  crew: Crew,
  member: CrewMember,
  layout: CrewLayout,
  dt: number,
) {
  for (const clone of member.clones) {
    clone.t += dt;
    if (clone.phase === "gone") {
      if (go(clone, layout, member.x, member.level, dt)) clone.home = true;
      continue;
    }
    if (clone.phase === "back") {
      // It brings what it fetched to its owner, then heads out again.
      const to = clamp(member.x - member.face * 34, 12, crew.width - 12);
      if (go(clone, layout, to, member.level, dt)) {
        pop(crew, member);
        clone.phase = "go";
        clone.goal = null;
      }
      continue;
    }
    if (clone.act === "wait") continue;
    clone.goal ??= layout.spot(clone.act, 2 + (clone.slot % 2));
    if (clone.phase === "go") {
      if (go(clone, layout, clone.goal.x, clone.goal.level, dt)) {
        clone.phase = "work";
        clone.t = 0;
        clone.face = clone.goal.face ?? clone.face;
        clone.next = crew.time + 4 + crew.rand() * 3;
      }
    } else if (crew.time > clone.next) {
      clone.phase = "back";
    }
  }
  member.clones = member.clones.filter((clone) => !clone.home);
}

/** Advances the crew by `dt` seconds. */
export function stepCrew(crew: Crew, layout: CrewLayout, dt: number) {
  crew.time += dt;
  // The door lets the next one waiting in once the last is clear of it; one
  // that ends before its turn never comes in.
  const turn = crew.members.find((member) => member.waiting && !member.leaving);
  if (turn && crew.time - crew.entered >= ENTRY_GAP) {
    turn.waiting = false;
    crew.entered = crew.time;
  }
  for (const member of crew.members) {
    if (member.waiting) continue;
    member.jump = Math.max(0, member.jump - dt * 1.6);
    member.slip = Math.max(0, member.slip - dt);
    member.t += dt;
    member.alpha = clamp(member.alpha + (member.leaving ? -1 : 1) * dt * 2.2);
    if (!member.leaving) {
      const still = member.slip <= 0 && member.jump <= 0;
      if (member.arrived && still) member.shown += dt;
      const hold = member.act === "wait" ? MIN_REST : MIN_HOLD;
      const next = member.queue[0];
      if (next && member.shown >= hold && still) {
        member.queue.shift();
        show(crew, member, layout, next);
      } else if (
        !next &&
        member.done &&
        member.shown >= hold &&
        member.act !== "wait"
      ) {
        finish(crew, member, layout);
      }
    }
    if (
      member.spot &&
      !member.arrived &&
      member.slip <= 0 &&
      member.jump <= 0
    ) {
      if (go(member, layout, member.spot.x, member.spot.level, dt)) {
        member.arrived = true;
        member.t = 0;
        if (member.spot.face) member.face = member.spot.face;
      }
    }
    stepClones(crew, member, layout, dt);
  }
  // Done leaving, or ended before it came in.
  crew.members = crew.members.filter(
    (member) => !(member.leaving && (member.arrived || member.waiting)),
  );
  for (const item of crew.pops) item.t += dt;
  crew.pops = crew.pops.filter((item) => item.t < 1.4);
  for (const piece of crew.confetti) {
    piece.x += piece.vx * dt;
    piece.y += piece.vy * dt;
  }
  crew.confetti = crew.confetti.filter((piece) => piece.y < crew.height - 30);
  crew.banner = Math.max(0, crew.banner - dt / 3.5);
}

/** Today's tokens passed a milestone: a banner, confetti, and everyone jumps. */
export function celebrate(crew: Crew, text: string) {
  crew.banner = 1;
  crew.bannerText = text;
  for (const member of crew.members) if (!member.leaving) member.jump = 1;
  for (let n = 0; n < 60; n += 1) {
    crew.confetti.push({
      x: crew.rand() * crew.width,
      y: -crew.rand() * 40,
      vy: 55 + crew.rand() * 40,
      vx: (crew.rand() - 0.5) * 30,
      color: n % 6,
    });
  }
}

/**
 * Drops a character's clones that head home and places the others at work,
 * except those in `keep`, which stay as they are.
 */
function settleClones(
  member: CrewMember,
  layout: CrewLayout,
  keep?: ReadonlySet<string>,
) {
  member.clones = member.clones.filter((clone) => clone.phase !== "gone");
  for (const clone of member.clones) {
    if (clone.act === "wait" || keep?.has(clone.id)) continue;
    clone.goal = layout.spot(clone.act, 2 + (clone.slot % 2));
    clone.x = clone.goal.x;
    clone.level = clone.goal.level;
    clone.fy = layout.floors[clone.goal.level];
    clone.face = clone.goal.face ?? clone.face;
    clone.phase = "work";
    clone.move = null;
    clone.climb = false;
  }
}

/** A character takes its latest step at once and stands where it happens. */
function settleMember(crew: Crew, member: CrewMember, layout: CrewLayout) {
  const latest = member.queue.at(-1);
  member.queue = [];
  member.done = false;
  if (latest) show(crew, member, layout, latest);
  if (member.spot) {
    member.x = member.spot.x;
    member.level = member.spot.level;
    member.fy = layout.floors[member.spot.level];
    if (member.spot.face) member.face = member.spot.face;
  }
  member.arrived = true;
  member.move = null;
  member.climb = false;
  member.jump = 0;
  member.slip = 0;
  member.waiting = false;
  member.alpha = 1;
  settleClones(member, layout);
}

/**
 * Without motion: everyone takes their latest step at once and stands where
 * it happens; leavers are gone and nothing jumps.
 */
export function settleCrew(crew: Crew, layout: CrewLayout) {
  crew.members = crew.members.filter((member) => !member.leaving);
  for (const member of crew.members) settleMember(crew, member, layout);
  crew.pops = [];
  crew.confetti = [];
}

/**
 * Brings in line a crew nobody watched for a while, such as when the
 * overview comes back. Conversations that ended meanwhile are gone at once,
 * those that moved on stand at their latest step, those that did not stay
 * as they were, and only those that started meanwhile come in through the
 * door. The very first reading finds everyone already at work.
 */
export function catchUpCrew(
  crew: Crew,
  reading: CrewReading,
  layout: CrewLayout,
) {
  const first = !crew.primed;
  // Who was in: what its conversation did, and the clones it had out.
  const known = new Map(
    crew.members
      .filter((member) => !member.leaving && !member.waiting)
      .map((member) => [
        member.id,
        {
          key: member.lastKey,
          clones: new Set(
            member.clones
              .filter((clone) => clone.phase !== "gone")
              .map((clone) => clone.id),
          ),
        },
      ]),
  );
  // Failovers from while nobody watched only count as seen.
  crew.primed = false;
  syncCrew(crew, reading, layout);
  crew.members = crew.members.filter((member) => !member.leaving);
  for (const member of crew.members) {
    const was = known.get(member.id);
    if (first || (was && was.key !== member.lastKey))
      settleMember(crew, member, layout);
    else if (was) settleClones(member, layout, was.clones);
  }
}

/** Moves everyone along when the scene's width changes. */
export function rescaleCrew(crew: Crew, ratio: number) {
  crew.width *= ratio;
  for (const member of crew.members) {
    member.x *= ratio;
    member.move = null;
    if (member.orb) member.orb.x *= ratio;
    if (member.spot) member.spot = { ...member.spot, x: member.spot.x * ratio };
    for (const clone of member.clones) {
      clone.x *= ratio;
      clone.move = null;
      clone.goal = null;
    }
  }
}
