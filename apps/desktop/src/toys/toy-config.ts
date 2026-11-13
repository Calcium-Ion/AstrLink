// How much the desktop pet moves, by level, and the preferences she reads
// live; the feed replaces them when settings change.
import {
  defaultDesktopToys,
  type DesktopToysPreferences,
  type ToyLevel,
} from "../preferences-model";

/** What a level changes about how much the pet moves. */
export interface Motion {
  /** Celebration and milestone hops, as a share; 0 for none. */
  hop: number;
  /** Dust, sparks and confetti, as a share; 0 for none. */
  fx: number;
  /** Seconds a conversation waves after its reply. */
  wave: number;
}

export const MOTION: Record<ToyLevel, Motion> = {
  calm: { hop: 0, fx: 0, wave: 3 },
  normal: { hop: 1, fx: 1, wave: 7 },
  lively: { hop: 1.5, fx: 1.8, wave: 9 },
};

export const cfg: { prefs: DesktopToysPreferences } = {
  prefs: defaultDesktopToys(),
};

export const motion = (): Motion => MOTION[cfg.prefs.level];

/** Must match the labels in `src-tauri/src/toys.rs`. */
export const TOY_LABELS = { pet: "toy-pet" } as const;
export type ToyLabel = (typeof TOY_LABELS)[keyof typeof TOY_LABELS];
