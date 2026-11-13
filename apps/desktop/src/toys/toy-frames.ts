// What the host sees on the screen for the desktop pets: the other apps'
// windows, the Dock, the floor and the menu bar, in the pet window's points.

/** Another app's window. */
export interface Win {
  id: number;
  kind: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Frames {
  /** Other apps' windows, front to back. */
  wins: Win[];
  dock: { x0: number; x1: number; y: number } | null;
  floor: number;
  /** The menu bar's height. */
  top: number;
  /** An app is full screen: the pets keep out of its way. */
  full: boolean;
}

const isNum = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v);

export function readFrames(payload: unknown): Frames | null {
  if (typeof payload !== "object" || payload === null) return null;
  const f = payload as Record<string, unknown>;
  if (!Array.isArray(f.wins) || !isNum(f.floor) || !isNum(f.top)) return null;
  const wins = (f.wins as Array<Partial<Win>>).filter(
    (w): w is Win =>
      isNum(w?.id) &&
      typeof w.kind === "string" &&
      isNum(w.x) &&
      isNum(w.y) &&
      isNum(w.w) &&
      isNum(w.h),
  );
  const d = f.dock as Frames["dock"];
  const dock = d && isNum(d.x0) && isNum(d.x1) && isNum(d.y) ? d : null;
  return { wins, dock, floor: f.floor, top: f.top, full: f.full === true };
}

/** `parts` without the span from x0 to x1. */
export function cut(parts: Array<[number, number]>, x0: number, x1: number) {
  return parts.flatMap(([a, b]): Array<[number, number]> => {
    if (b <= x0 || a >= x1) return [[a, b]];
    const left: Array<[number, number]> = a < x0 ? [[a, x0]] : [];
    return b > x1 ? [...left, [x1, b]] : left;
  });
}

/** The windows as drawn: they glide to each new frame instead of jumping. */
export function glideWins(drawn: Win[], next: Win[], dt: number): Win[] {
  const k = Math.min(1, Math.max(0, dt * 18));
  const was = new Map(drawn.map((w) => [w.id, w]));
  return next.map((w) => {
    const o = was.get(w.id);
    return o
      ? { ...w, x: o.x + (w.x - o.x) * k, y: o.y + (w.y - o.y) * k }
      : { ...w };
  });
}
