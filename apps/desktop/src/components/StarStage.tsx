import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentProps,
  type CSSProperties,
  type ReactNode,
} from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import "./StarStage.css";

/**
 * A place in the star's system, in world units: x to the right and y toward
 * the viewer across the plane the bodies orbit in, z up from it.
 */
export interface StagePoint {
  x: number;
  y: number;
  z?: number;
}

/** An orbit round the star, as radii in world units. */
export interface StageOrbit {
  rx: number;
  ry: number;
  /** A satellites' orbit, drawn finer. */
  fine?: boolean;
}

/** The star's line to a body; sparks run along it while the body is at work. */
export interface StageRay {
  id: string;
  to: StagePoint;
  /** A CSS color, best a star token such as `var(--star-client-1)`. */
  color: string;
  /** Which way the sparks run: into the star, as requests do, or out of it. */
  sparks?: "in" | "out";
  /** Faded while another part of the map is in focus. */
  dimmed?: boolean;
}

/**
 * Where a mark shows: how much nearer the viewer it is than the star, 1 at
 * the star's distance, and the way to the star on screen, in radians.
 */
export interface MarkView {
  near: number;
  toStar: number;
}

/** A body and its label, flat to the viewer, often a button. */
export interface StageMark {
  id: string;
  at: StagePoint;
  /** Its size on screen, which the stage keeps inside its bounds. */
  box: { width: number; height: number };
  /** The point of the box on its place, from the box's top left; its middle by default. */
  origin?: { x: number; y: number };
  children: ReactNode | ((view: MarkView) => ReactNode);
}

/** Room the stage keeps clear at its edges, for what lies over it. */
export interface StageInset {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/** What the stage shows. */
export interface StageScene {
  /** Where the star stands. */
  center: StagePoint;
  orbits?: StageOrbit[];
  rays?: StageRay[];
  marks?: StageMark[];
  /**
   * What the star holds in its light, such as the logo, and its size; its
   * light is the gateway's color unless the scene gives another.
   */
  star: {
    box: { width: number; height: number };
    tone?: string;
    children: ReactNode;
  };
  /** A card beside one mark, on the side with more room. */
  callout?: { mark: string; children: ReactNode };
  /** Room kept clear at the edges for what lies over the stage. */
  inset?: StageInset;
}

/** The stage's size in CSS pixels. */
export interface StageSize {
  width: number;
  height: number;
}

/** How the system is seen: its plane tilted back, turned a little, and scaled to fit. */
export interface StageCamera {
  world: { width: number; height: number };
  /** Degrees the plane leans back from the viewer. */
  tilt: number;
  /** Degrees the plane turns, clockwise. */
  twist: number;
  scale: number;
  /** Where the world's center falls on the stage, in CSS pixels. */
  center: { x: number; y: number };
}

/** How far the viewer stands from the stage, in CSS pixels. */
const PERSPECTIVE = 1600;
/** Where the viewer looks from: this far above the world's center. */
const EYE = -220;
const RADIANS = Math.PI / 180;

type Placeable = Pick<StageMark, "at" | "box" | "origin">;

/**
 * Where a place shows on the stage, with how near the viewer it is: the
 * same transform the stage gives its plane in CSS, so a body stays on the
 * orbit and the ray drawn for it.
 */
export function projectPoint(
  camera: StageCamera,
  point: StagePoint,
): { x: number; y: number; depth: number; near: number } {
  const u = (point.x - camera.world.width / 2) * camera.scale;
  const v = (point.y - camera.world.height / 2) * camera.scale;
  const w = (point.z ?? 0) * camera.scale;
  // rotateZ(twist), then rotateX(tilt), as CSS applies `rotateX rotateZ`.
  const twist = camera.twist * RADIANS;
  const x1 = u * Math.cos(twist) - v * Math.sin(twist);
  const y1 = u * Math.sin(twist) + v * Math.cos(twist);
  const tilt = camera.tilt * RADIANS;
  const y2 = y1 * Math.cos(tilt) - w * Math.sin(tilt);
  const depth = y1 * Math.sin(tilt) + w * Math.cos(tilt);
  const near = PERSPECTIVE / (PERSPECTIVE - depth);
  return {
    x: camera.center.x + x1 * near,
    y: camera.center.y + EYE + (y2 - EYE) * near,
    depth,
    near,
  };
}

/** Where a mark shows on the stage, as a box. */
function markBox(camera: StageCamera, mark: Placeable) {
  const { x, y } = projectPoint(camera, mark.at);
  const origin = mark.origin ?? {
    x: mark.box.width / 2,
    y: mark.box.height / 2,
  };
  return {
    left: x - origin.x,
    right: x - origin.x + mark.box.width,
    top: y - origin.y,
    bottom: y - origin.y + mark.box.height,
  };
}

/** The screen box of the marks around the world's center at a scale. */
function markBounds(camera: Omit<StageCamera, "center">, marks: Placeable[]) {
  const bounds = { left: 0, right: 0, top: 0, bottom: 0 };
  for (const mark of marks) {
    const box = markBox({ ...camera, center: { x: 0, y: 0 } }, mark);
    bounds.left = Math.min(bounds.left, box.left);
    bounds.right = Math.max(bounds.right, box.right);
    bounds.top = Math.min(bounds.top, box.top);
    bounds.bottom = Math.max(bounds.bottom, box.bottom);
  }
  return bounds;
}

/** Whether two marks would cover each other, with room between them. */
function marksCollide(
  camera: StageCamera,
  marks: Placeable[],
  gap = 4,
): boolean {
  const boxes = marks.map((mark) => markBox(camera, mark));
  return boxes.some((a, index) =>
    boxes
      .slice(index + 1)
      .some(
        (b) =>
          a.left < b.right + gap &&
          b.left < a.right + gap &&
          a.top < b.bottom + gap &&
          b.top < a.bottom + gap,
      ),
  );
}

/**
 * The largest scale, within limits, at which every mark fits the stage
 * inside its inset, and where the world's center then goes.
 */
export function fitCamera({
  world,
  tilt,
  twist,
  size,
  inset,
  marks,
  minScale,
  maxScale,
}: {
  world: StageCamera["world"];
  tilt: number;
  twist: number;
  size: StageSize;
  inset: StageInset;
  marks: Placeable[];
  minScale: number;
  maxScale: number;
}): StageCamera {
  const room = {
    width: Math.max(0, size.width - inset.left - inset.right),
    height: Math.max(0, size.height - inset.top - inset.bottom),
  };
  const fits = (scale: number) => {
    const bounds = markBounds({ world, tilt, twist, scale }, marks);
    return (
      bounds.right - bounds.left <= room.width &&
      bounds.bottom - bounds.top <= room.height
    );
  };
  let scale = maxScale;
  if (!fits(maxScale)) {
    let low = minScale;
    let high = maxScale;
    for (let step = 0; step < 24; step += 1) {
      const middle = (low + high) / 2;
      if (fits(middle)) low = middle;
      else high = middle;
    }
    scale = low;
  }
  const bounds = markBounds({ world, tilt, twist, scale }, marks);
  return {
    world,
    tilt,
    twist,
    scale,
    center: {
      x:
        inset.left +
        (room.width - (bounds.right - bounds.left)) / 2 -
        bounds.left,
      y:
        inset.top +
        (room.height - (bounds.bottom - bounds.top)) / 2 -
        bounds.top,
    },
  };
}

/** How a stage sees its system, unless told otherwise. */
const VIEW = {
  world: { width: 1400, height: 900 },
  tilt: 62,
  twist: -8,
  minScale: 0.2,
  maxScale: 0.75,
};

type StageView = typeof VIEW;

/** The scene's marks with its star, which bodies must not cover either. */
function sceneMarks(scene: StageScene): Placeable[] {
  return [...(scene.marks ?? []), { at: scene.center, box: scene.star.box }];
}

/** The size a stage lays itself out at before it is measured, as in tests. */
const UNMEASURED: StageSize = { width: 960, height: 420 };

/** Between a mark and its callout. */
const CALLOUT_GAP = 12;

/**
 * A sky of stars: points spread over a wide field from a fixed seed, so
 * every stage shows the same sky, as one element's shadows.
 */
function sky(count: number, seed: number): string {
  let state = seed;
  const next = () => {
    state = (state * 1103515245 + 12345) % 2147483648;
    return state / 2147483648;
  };
  return Array.from(
    { length: count },
    () => `${Math.round(next() * 1800)}px ${Math.round(next() * 760)}px`,
  ).join(",");
}

const SKY = [sky(90, 7), sky(26, 11), sky(16, 23)];

/** The upper half of an orbit, behind the star, or the lower, before it. */
function arc(center: StagePoint, orbit: StageOrbit, half: "back" | "front") {
  const { x, y } = center;
  return `M${x - orbit.rx},${y} A${orbit.rx},${orbit.ry} 0 0 ${half === "back" ? 1 : 0} ${x + orbit.rx},${y}`;
}

/**
 * The star map's stage: a star in the middle of its system, the orbits of
 * its bodies tilted away from the viewer, and the bodies on them. The plane
 * of the orbits is drawn in CSS 3D; the star and the bodies stay flat, so
 * their text stays sharp and they work as ordinary buttons. The stage scales
 * the system so every body fits inside its inset, where what lies over it,
 * such as a headline, goes; of the scenes a page offers for its size, it
 * shows the first whose bodies do not cover each other.
 */
export function StarStage({
  scene,
  world = VIEW.world,
  tilt = VIEW.tilt,
  twist = VIEW.twist,
  minScale = VIEW.minScale,
  maxScale = VIEW.maxScale,
  className,
  children,
  ...props
}: Omit<ComponentProps<"div">, "children"> & {
  /**
   * What to show, or the scenes to try at the stage's size, the fullest
   * first; the last is shown if every one is too crowded.
   */
  scene: StageScene | ((size: StageSize) => Iterable<StageScene>);
  world?: StageCamera["world"];
  tilt?: number;
  twist?: number;
  minScale?: number;
  maxScale?: number;
  /** What lies over the stage, such as its headline. */
  children?: ReactNode;
}) {
  const stage = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState(UNMEASURED);
  const ids = `star-stage-${useId().replace(/[^A-Za-z0-9_-]/g, "")}`;

  useLayoutEffect(() => {
    const element = stage.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      const width = element.clientWidth;
      const height = element.clientHeight;
      // Hidden, the stage keeps its last size.
      if (width > 0 && height > 0)
        setSize((current) =>
          current.width === width && current.height === height
            ? current
            : { width, height },
        );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const view: StageView = { world, tilt, twist, minScale, maxScale };
  let shown: { scene: StageScene; camera: StageCamera } | undefined;
  for (const candidate of typeof scene === "function" ? scene(size) : [scene]) {
    const camera = fitCamera({
      ...view,
      size,
      inset: candidate.inset ?? { top: 0, right: 0, bottom: 0, left: 0 },
      marks: sceneMarks(candidate),
    });
    shown = { scene: candidate, camera };
    if (!marksCollide(camera, sceneMarks(candidate))) break;
  }
  if (!shown) throw new Error("A star stage needs a scene");
  const { camera } = shown;
  const {
    center,
    orbits = [],
    rays = [],
    marks = [],
    star,
    callout,
  } = shown.scene;
  const space: CSSProperties = {
    perspective: `${PERSPECTIVE}px`,
    perspectiveOrigin: `${camera.center.x}px ${camera.center.y + EYE}px`,
  };
  const plane: CSSProperties = {
    left: camera.center.x - world.width / 2,
    top: camera.center.y - world.height / 2,
    width: world.width,
    height: world.height,
    transform: `rotateX(${tilt}deg) rotateZ(${twist}deg) scale(${camera.scale})`,
  };
  const sun = projectPoint(camera, center);
  // Marks keep the scene's order, for reading and the tab key; those nearer
  // the viewer lie over those behind them.
  const placed = marks.map((mark) => {
    const at = projectPoint(camera, mark.at);
    return {
      mark,
      at,
      view: { near: at.near, toStar: Math.atan2(sun.y - at.y, sun.x - at.x) },
    };
  });
  const layers = new Map(
    [...placed]
      .sort((a, b) => a.at.depth - b.at.depth)
      .map(({ mark }, index) => [mark.id, index + 2]),
  );
  const widest = Math.max(0, ...orbits.map((orbit) => orbit.rx));
  // The plane, drawn twice: the halves of the orbits behind the star under
  // it, those before it over it.
  const drawPlane = (half: "back" | "front") => (
    <div aria-hidden="true" className="absolute inset-0" style={space}>
      <div className="star-stage-plane" style={plane}>
        <svg
          className="absolute inset-0 overflow-visible"
          height={world.height}
          viewBox={`0 0 ${world.width} ${world.height}`}
          width={world.width}
        >
          {half === "back" ? (
            <>
              <defs>
                <radialGradient id={`${ids}-disc`}>
                  <stop
                    offset="0%"
                    style={{ stopColor: "var(--star-sun)", stopOpacity: 0.28 }}
                  />
                  <stop
                    offset="100%"
                    style={{ stopColor: "var(--star-sun)", stopOpacity: 0 }}
                  />
                </radialGradient>
              </defs>
              <ellipse
                cx={center.x}
                cy={center.y}
                fill={`url(#${ids}-disc)`}
                rx={widest * 1.15}
                ry={widest * 1.15}
              />
              {rays.map((ray) => {
                const d =
                  ray.sparks === "in"
                    ? `M${ray.to.x},${ray.to.y}L${center.x},${center.y}`
                    : `M${center.x},${center.y}L${ray.to.x},${ray.to.y}`;
                return (
                  <g
                    className="star-stage-ray"
                    data-dimmed={ray.dimmed || undefined}
                    key={ray.id}
                    style={{ color: ray.color }}
                  >
                    <path className="star-stage-ray-line" d={d} />
                    {ray.sparks ? (
                      <path className="star-stage-spark" d={d} />
                    ) : null}
                  </g>
                );
              })}
            </>
          ) : null}
          {orbits.map((orbit) => (
            <path
              className="star-stage-orbit"
              d={arc(center, orbit, half)}
              data-fine={orbit.fine || undefined}
              key={`${orbit.rx}:${orbit.ry}`}
            />
          ))}
        </svg>
      </div>
    </div>
  );

  return (
    <div
      className={cn("star-stage relative overflow-hidden", className)}
      data-slot="star-stage"
      ref={stage}
      {...props}
      style={
        {
          ...props.style,
          "--star-sun": star.tone ?? "var(--star-gateway)",
        } as CSSProperties
      }
    >
      <div aria-hidden="true" className="star-stage-sky">
        {SKY.map((shadows, index) => (
          <span
            className="star-stage-stars"
            data-layer={index}
            key={index}
            style={{ boxShadow: shadows }}
          />
        ))}
      </div>
      {drawPlane("back")}
      <div
        className="star-stage-sun absolute"
        data-mark="star"
        style={{
          left: sun.x,
          top: sun.y,
          width: star.box.width,
          height: star.box.height,
          zIndex: 1,
        }}
      >
        {star.children}
      </div>
      <div
        className="pointer-events-none absolute inset-0"
        style={{ zIndex: 1 }}
      >
        {drawPlane("front")}
      </div>
      {placed.map(({ mark, at, view }) => {
        const origin = mark.origin ?? {
          x: mark.box.width / 2,
          y: mark.box.height / 2,
        };
        return (
          <div
            className="absolute"
            data-mark={mark.id}
            key={mark.id}
            style={{
              left: at.x - origin.x,
              top: at.y - origin.y,
              width: mark.box.width,
              height: mark.box.height,
              zIndex: layers.get(mark.id),
            }}
          >
            {typeof mark.children === "function"
              ? mark.children(view)
              : mark.children}
          </div>
        );
      })}
      {/* Over the orbits that pass before the star; only its content takes the pointer. */}
      <div
        className="pointer-events-none absolute inset-0 [&>*]:pointer-events-auto"
        style={{ zIndex: 1 }}
      >
        {children}
      </div>
      {callout ? (
        <Callout
          layer={placed.length + 2}
          placed={placed.find(({ mark }) => mark.id === callout.mark)}
          size={size}
        >
          {callout.children}
        </Callout>
      ) : null}
    </div>
  );
}

/**
 * A card beside a mark: on the side of it with more room, level with its top
 * in the stage's upper half and with its bottom in the lower.
 */
function Callout({
  layer,
  placed,
  size,
  children,
}: {
  /** Over every mark. */
  layer: number;
  placed?: { mark: StageMark; at: { x: number; y: number } };
  size: StageSize;
  children: ReactNode;
}) {
  if (!placed) return null;
  const { mark, at } = placed;
  const origin = mark.origin ?? {
    x: mark.box.width / 2,
    y: mark.box.height / 2,
  };
  const left = at.x - origin.x;
  const top = at.y - origin.y;
  const style: CSSProperties =
    at.x < size.width / 2
      ? { left: left + mark.box.width + CALLOUT_GAP, zIndex: layer }
      : { right: size.width - left + CALLOUT_GAP, zIndex: layer };
  if (top + mark.box.height / 2 < size.height / 2) style.top = Math.max(8, top);
  else style.bottom = Math.max(8, size.height - top - mark.box.height);
  return (
    <div className="pointer-events-none absolute" style={style}>
      {children}
    </div>
  );
}

/**
 * A body's diameter for what it did against the busiest: by square root,
 * so a small one stays visible beside a large one.
 */
export function bodySize(
  value: number,
  busiest: number,
  { least, most }: { least: number; most: number },
): number {
  if (busiest <= 0 || value <= 0) return least;
  return least + (most - least) * Math.sqrt(Math.min(1, value / busiest));
}

/** A moon of a body: its size on screen and its color. */
export interface StarMoon {
  size: number;
  color: string;
}

/**
 * A body of the system: a sphere lit from the side facing the star, its
 * glow in its own color, with its moons going round it. A quiet body, such
 * as a plugin that is off, keeps its place unlit.
 */
export function StarSphere({
  className,
  color,
  moons = [],
  quiet = false,
  size,
  toStar,
}: {
  className?: string;
  color: string;
  moons?: StarMoon[];
  quiet?: boolean;
  /** Its diameter on screen. */
  size: number;
  /** The way to the star on screen, in radians. */
  toStar: number;
}) {
  const light = {
    x: 50 + 30 * Math.cos(toStar),
    y: 50 + 30 * Math.sin(toStar),
  };
  return (
    <span
      aria-hidden="true"
      className={cn("star-stage-sphere", className)}
      data-quiet={quiet || undefined}
      style={
        {
          width: size,
          height: size,
          "--star-sphere": color,
          "--star-light-x": `${light.x}%`,
          "--star-light-y": `${light.y}%`,
        } as CSSProperties
      }
    >
      {moons.map((moon, index) => (
        <span
          className="star-stage-moon-orbit"
          key={index}
          style={
            {
              "--star-moon-orbit": `${size * (1.55 + index * 0.38)}px`,
              "--star-moon-turn": `${9 + index * 5}s`,
              "--star-moon-phase": `${-index * 2.7}s`,
            } as CSSProperties
          }
        >
          <span className="star-stage-moon-path">
            <span
              className="star-stage-moon"
              style={{
                width: moon.size,
                height: moon.size,
                background: moon.color,
              }}
            />
          </span>
        </span>
      ))}
    </span>
  );
}

/**
 * A body with its name and what it did, as one button: the name under the
 * sphere, or beside it toward the outside of the map. Without `onClick` it
 * is a plain label, for a body with no page of its own.
 */
export function StarBody({
  caption,
  captionAt = "below",
  className,
  fact,
  focus,
  sphere,
  ...props
}: Omit<ComponentProps<typeof Button>, "children"> & {
  /** Its name. */
  caption: ReactNode;
  /** Where its name goes: over or under the sphere, or before or after it. */
  captionAt?: "above" | "below" | "before" | "after";
  fact?: ReactNode;
  /** lit: the body in focus; dim: another is. */
  focus?: "lit" | "dim";
  sphere: ReactNode;
}) {
  const content = (
    <>
      <span className="grid shrink-0 place-items-center">{sphere}</span>
      <span
        className={cn(
          "star-stage-label grid min-w-0 gap-px",
          captionAt === "above" || captionAt === "below"
            ? "justify-items-center text-center"
            : captionAt === "before"
              ? "justify-items-end text-right"
              : "justify-items-start text-left",
        )}
      >
        <span className="max-w-full truncate text-xs font-medium">
          {caption}
        </span>
        {fact ? (
          <span className="max-w-full truncate text-micro text-star-dim tabular-nums">
            {fact}
          </span>
        ) : null}
      </span>
    </>
  );
  const shape = cn(
    "star-stage-body h-auto w-full p-0 font-normal",
    captionAt === "below"
      ? "flex-col justify-start gap-1"
      : captionAt === "above"
        ? "flex-col-reverse justify-start gap-1"
        : captionAt === "before"
          ? "flex-row-reverse justify-start gap-2"
          : "flex-row justify-start gap-2",
    className,
  );
  if (!props.onClick)
    return (
      <Button
        asChild
        className={shape}
        data-focus={focus}
        variant="ghost"
        {...props}
      >
        <span>{content}</span>
      </Button>
    );
  return (
    <Button
      className={shape}
      data-focus={focus}
      type="button"
      variant="ghost"
      {...props}
    >
      {content}
    </Button>
  );
}

/**
 * A label on the stage in a pill, as a button: lit while its part of the
 * map is in focus, dim behind another.
 */
export function StarMark({
  asChild,
  className,
  focus,
  tone,
  ...props
}: ComponentProps<typeof Button> & {
  /** lit: the mark in focus; dim: another is. */
  focus?: "lit" | "dim";
  /** The border's color. */
  tone?: string;
}) {
  return (
    <Button
      asChild={asChild}
      className={cn(
        "star-stage-mark h-auto rounded-full font-normal",
        className,
      )}
      data-focus={focus}
      style={tone ? ({ "--star-mark-tone": tone } as CSSProperties) : undefined}
      type={asChild ? undefined : "button"}
      variant="ghost"
      {...props}
    />
  );
}

/** What the body in focus did, in a card beside it. */
export function StarDetail({
  title,
  kind,
  rows,
  className,
}: {
  title: ReactNode;
  kind: ReactNode;
  rows: Array<{ label: ReactNode; value: ReactNode }>;
  className?: string;
}) {
  return (
    <div
      aria-hidden="true"
      className={cn("star-stage-card grid gap-2.5 p-3.5", className)}
      data-slot="star-detail"
    >
      <div className="grid gap-0.5">
        <span className="truncate text-sm font-semibold text-star-ink">
          {title}
        </span>
        <span className="text-xs text-star-dim">{kind}</span>
      </div>
      <dl className="grid gap-1.5 text-xs">
        {rows.map((row, index) => (
          <div className="flex justify-between gap-3" key={index}>
            <dt className="shrink-0 text-star-dim">{row.label}</dt>
            <dd className="min-w-0 truncate text-right text-star-ink tabular-nums">
              {row.value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
