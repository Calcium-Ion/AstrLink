import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { CrewPalette, Pen } from "./live-crew-draw";
import {
  blueprintPose,
  carryPose,
  cheerPose,
  climbPose,
  coffeePose,
  hammerPose,
  lensPose,
  lyingPose,
  notePose,
  penmarkPose,
  readPose,
  scopePose,
  scribblePose,
  standPose,
  thinkPose,
  typePose,
  walkPose,
  wrenchPose,
} from "./live-crew-poses";
import { drawGirl, hardHat, type Pose } from "./live-crew-rig";
import { gearOf } from "./toys/pet-gear";
import {
  danglePose,
  dozePose,
  gladPose,
  greetPose,
  stretchPose,
} from "./toys/pet-poses";
import type { ClientType } from "./request-record-model";

// Every pose the scenes and the pet draw, at every grid, facing both ways,
// standing and lying, hashed: the baked art must draw the same cells the
// shape functions did.

const palette = new Proxy({} as CrewPalette, {
  get: (_, key) => {
    const h = createHash("sha1").update(String(key)).digest("hex");
    return `#${h.slice(0, 6)}`;
  },
});

function capture(
  client: ClientType,
  scale: number,
  face: number,
  pose: Pose,
  lie: boolean,
) {
  const out: string[] = [];
  const pen = {
    palette,
    stamp: (
      _k: string,
      _x: number,
      _y: number,
      box: readonly number[],
      paint: (p: Pen) => void,
    ) => {
      out.push(box.map((v) => v.toFixed(3)).join(","));
      paint({
        rectsOf: (color: string, list: ReadonlyArray<readonly number[]>) =>
          out.push(`${color}:${list.map((r) => r.join(",")).join(";")}`),
      } as unknown as Pen);
    },
  } as unknown as Pen;
  const hands = drawGirl(pen, client, 0, 0, scale, face, pose, lie);
  out.push(JSON.stringify(hands));
  return out.join("\n");
}

const T = Array.from({ length: 160 }, (_, i) => i * 0.07);
const SEEDS = [0, 13, 58];

function poses(): Array<[string, Pose[]]> {
  const p = palette;
  const each = (f: (t: number, s: number) => Pose) =>
    SEEDS.flatMap((s) => T.map((t) => f(t, s)));
  const hat = hardHat({
    color: p.yellow,
    light: p.yellowLight,
    shade: p.shade,
  });
  return [
    ["stand", each((t, s) => standPose(t, s))],
    ["sit", each((t, s) => standPose(t, s, true))],
    ["walk", each((t, s) => walkPose(t * 3, t, s).pose)],
    ["type", each((t, s) => typePose(t, s, p, false))],
    ["lap", each((t, s) => typePose(t, s, p, true))],
    ["read", each((t, s) => readPose(t, s, p))],
    ["hammer", each((t, s) => hammerPose(t, s, p, false).pose)],
    ["hammerLow", each((t, s) => hammerPose(t, s, p, true).pose)],
    ["wrench", each((t, s) => wrenchPose(t, s, p).pose)],
    ["blueprint", each((t, s) => blueprintPose(t, s, p))],
    ["scope", each((t) => scopePose(t, p).pose)],
    ["note", each((t, s) => notePose(t, s, p))],
    ["lens", each((t, s) => lensPose(t, s, p))],
    ["think", each((t, s) => thinkPose(t, s, false))],
    ["thinkSit", each((t, s) => thinkPose(t, s, true))],
    ["scribble", each((t, s) => scribblePose(t, s, p).pose)],
    ["penmark", each((t, s) => penmarkPose(t, s, p))],
    ["coffee", each((t, s) => coffeePose(t, s, p).pose)],
    ["lying", each((t) => lyingPose(t, false))],
    ["dizzy", each((t) => lyingPose(t, true))],
    ["cheer", [cheerPose(true), cheerPose(false)]],
    ["climb", T.map((t) => climbPose(t * 3))],
    [
      "carry",
      T.flatMap((t) => [
        carryPose(t * 3, true).pose,
        carryPose(t * 3, false).pose,
      ]),
    ],
    [
      "hat",
      each((t, s) => ({ ...standPose(t, s), onHead: [hat], key: "hat" })),
    ],
    ["greet", T.map((t) => greetPose(t))],
    ["glad", [gladPose()]],
    ["stretch", [stretchPose()]],
    ["dangle", T.map((t) => danglePose(t))],
    ["doze", T.flatMap((t) => [dozePose(t, true), dozePose(t, false)])],
    [
      "perch",
      [0, 1, 3, 5, 7].flatMap((stage) =>
        T.map((t, i) => ({
          ...standPose(t, 0, true),
          perch: true,
          legs: (
            [
              [1, 0],
              [0, 0],
              [0, 1],
            ] as const
          )[i % 3],
          onHead: gearOf(stage),
          key: `gear${stage}`,
        })),
      ),
    ],
    [
      "gear",
      [1, 3, 5, 7].flatMap((stage) =>
        T.map((t) => ({
          ...greetPose(t),
          onHead: gearOf(stage),
          key: `gear${stage}`,
        })),
      ),
    ],
  ];
}

/** Poses that draw alike once: same fields and the same prop boxes. */
function unique(list: Pose[]): Pose[] {
  const seen = new Map<string, Pose>();
  for (const pose of list) seen.set(JSON.stringify(pose), pose);
  return [...seen.values()];
}

const CLIENTS: ClientType[] = ["claude_code", "codex", "deepseek_harness"];
// Coarse, fine, the pet's grid, and big girls on the fine and coarse grids.
const SCALES = [1, 1.5, 2, 3, 4.5, 5, 5.25];
const FIXTURE = new URL("./live-crew-art.fixture.json", import.meta.url);

function hashes() {
  const out: Record<string, string> = {};
  for (const client of CLIENTS)
    for (const [name, list] of poses())
      for (const scale of SCALES) {
        const h = createHash("sha1");
        for (const pose of unique(list))
          for (const face of [1, -1])
            for (const lie of [false, true])
              h.update(capture(client, scale, face, pose, lie));
        out[`${client}|${name}|${scale}`] = h.digest("hex").slice(0, 16);
      }
  return out;
}

describe("baked girl art", () => {
  it(
    "draws every pose as the shape functions did",
    { timeout: 120_000 },
    () => {
      const got = hashes();
      if (process.env.BAKE_FIXTURE) {
        writeFileSync(FIXTURE, JSON.stringify(got, null, 2) + "\n");
        return;
      }
      const want = JSON.parse(readFileSync(FIXTURE, "utf8")) as Record<
        string,
        string
      >;
      expect(got).toEqual(want);
    },
  );
});
