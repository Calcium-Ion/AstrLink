// The big pet's own poses. She is there to be looked at and played with, so
// she moves gently: a small wave by her cheek, hands clasped for good news,
// a slow stretch on waking, hands and feet swaying slowly while held.
import { cheerPose } from "../live-crew-poses";
import { CX, plainLook, type Pose } from "../live-crew-rig";

/** A small wave by her cheek, the palm swaying slowly, her head tilted to it. */
export function greetPose(t: number): Pose {
  const f = Math.floor(t * 1.6) % 2;
  return {
    near: {
      x: CX + 6.2 + f * 0.6,
      y: 16.8 - f * 0.3,
      open: true,
      front: true,
      elbow: [CX + 8.6, 21.8],
    },
    lean: 1,
    look: { ...plainLook(), eyes: "happy", mouth: "smile", blush: 1 },
    tail: f,
  };
}

/** Hands clasped at her chest, pleased. */
export function gladPose(): Pose {
  return {
    near: { x: CX + 1.1, y: 22.8, front: true },
    far: { x: CX - 1.1, y: 22.9 },
    look: { ...plainLook(), eyes: "happy", mouth: "cat", blush: 1 },
  };
}

/** A slow stretch on waking: arms up, a yawn. */
export function stretchPose(): Pose {
  return {
    ...cheerPose(true),
    legs: [0, 0],
    sway: 0,
    nod: 0,
    look: { ...plainLook(), eyes: "closed", mouth: "o" },
  };
}

/** Held up or flying: hands out a little, feet paddling slowly. */
export function danglePose(t: number): Pose {
  const k = Math.floor(t * 2.5) % 2;
  return {
    near: { x: CX + 7.6, y: 19.4 + k * 0.6, open: true },
    far: { x: CX - 7.6, y: 20.0 - k * 0.6, open: true },
    legs: k ? [1, 0] : [0, 1],
    tail: k,
  };
}

/** Nodding off where she sits, her head bowed a little, breathing slowly. */
export function dozePose(t: number, sit: boolean): Pose {
  const f = Math.floor(t * 0.7) % 2;
  return {
    sit,
    nod: 1,
    look: {
      ...plainLook(),
      eyes: "closed",
      mouth: f ? "o" : "dot",
      blush: 1,
    },
  };
}
