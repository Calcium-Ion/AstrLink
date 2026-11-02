import {
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import { ClientTypeIcon, clientTypeName } from "@/components/ClientTypeIcon";
import { HelpPopover } from "@/components/HelpPopover";
import { ChevronRight } from "@/components/icons";
import { SegmentedControl } from "@/components/SegmentedControl";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useDevicePixelRatio } from "@/lib/use-device-pixel-ratio";
import { useMediaQuery } from "@/lib/use-media-query";
import { useOnScreen } from "@/lib/use-on-screen";

import { getUsageSummary } from "./bridge";
import { useT } from "./i18n";
import { useRequestActivityPoll } from "./RequestActivityBoard";
import {
  COMET_FADE_MS,
  COMET_HOP_MS,
  COMET_IN_MS,
  COMET_OUT_MS,
  useSkyComets,
  type SkyComet,
  type SkyCometRequest,
} from "./request-sky-comets";
import {
  cubicAt,
  cubicPath,
  hopCurve,
  hopPath,
  SKY_HEIGHT,
  SKY_MUTED,
  SKY_NODE_R,
  SKY_REWRITE_COLORS,
  skyCometRoute,
  skyCourse,
  skyFlare,
  skyGrid,
  skyPlan,
  skyPlanetSlots,
  skyRecency,
  skyScene,
  skyStars,
  sparklePath,
  type Point,
  type SkyCourse,
  type SkyPlan,
  type SkyScene,
  type SkyService,
} from "./request-sky-model";
import {
  GATEWAY_REWRITES,
  type GatewayRewrite,
  type RequestActivity,
} from "./request-record-model";
import type { Service, ServiceKind } from "./service-model";
import { useResolvedTheme } from "./theme";
import type { ResolvedTheme } from "./theme-model";
import {
  localDayKey,
  resolveUsageWindow,
  type UsageRoute,
  type UsageSummary,
} from "./usage-range";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

export type SkyRange = "today" | "30d";

const REWRITE_LABELS: Record<GatewayRewrite, string> = {
  model_redirect: "overview.skyRewriteModelRedirect",
  privacy: "overview.skyRewritePrivacy",
  image_compression: "overview.skyRewriteImageCompression",
  global_prompt: "overview.skyRewriteGlobalPrompt",
  reasoning_effort: "overview.skyRewriteReasoningEffort",
  conversion: "overview.skyRewriteConversion",
  client_identity: "overview.skyRewriteClientIdentity",
};

/**
 * What differs between the themes beyond the `--sky-*` colors: the light
 * sky is a star chart on paper, the dark one a night sky.
 */
const PAPER_LOOK = {
  hairMax: 0.3,
  wash: [0.07, 0.09],
  dim: 0.45,
  stars: 0.7,
  sparkles: true,
  grid: true,
  warmSun: false,
  halo: 0.16,
  flare: [0.32, 0.128],
  flash: 0.2,
} as const;

const NIGHT_LOOK = {
  hairMax: 0.42,
  wash: [0.22, 0.26],
  dim: 0.38,
  stars: 1,
  sparkles: false,
  grid: false,
  warmSun: true,
  halo: 0.55,
  flare: [1, 0.5],
  flash: 0.35,
} as const;

type SkyLook = typeof PAPER_LOOK | typeof NIGHT_LOOK;

function skyLook(theme: ResolvedTheme): SkyLook {
  return theme === "dark" ? NIGHT_LOOK : PAPER_LOOK;
}

/** A finished call re-reads today once its comet has landed. */
const EXPOSE_DELAY_MS = 2_400;
const CLOCK_MS = 15_000;
const SVG_NS = "http://www.w3.org/2000/svg";

const NAME_TEXT = {
  fill: "var(--sky-ink)",
  fontSize: 12,
  fontWeight: 550,
  paintOrder: "stroke",
  stroke: "var(--sky-halo)",
  strokeLinejoin: "round",
  strokeWidth: 4,
} as const;

const clamp01 = (value: number) => Math.min(1, Math.max(0, value));

/** Cuts a name to about `room` pixels at `size`, with an ellipsis. */
function fitLabel(text: string, room: number, size = 12): string {
  const widthOf = (char: string) =>
    char.codePointAt(0)! >= 0x2e80 ? size : size * 0.6;
  const chars = [...text];
  if (chars.reduce((sum, char) => sum + widthOf(char), 0) <= room) return text;
  let used = size * 0.6;
  let fitted = "";
  for (const char of chars) {
    if (used + widthOf(char) > room) break;
    used += widthOf(char);
    fitted += char;
  }
  return `${fitted.trimEnd()}…`;
}

function useWidth() {
  const ref = useRef<HTMLElement>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;
    // The observer reports once per frame; same widths change nothing.
    const measure = () => {
      const next = Math.round(node.getBoundingClientRect().width);
      if (next > 0) setWidth((current) => (current === next ? current : next));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
}

/** The local day, updated at midnight without ticking in between. */
function useLocalDay() {
  const [day, setDay] = useState(() => localDayKey(new Date()));
  useEffect(() => {
    const next = new Date();
    next.setHours(24, 0, 1, 0);
    const timer = window.setTimeout(
      () => setDay(localDayKey(new Date())),
      next.getTime() - Date.now(),
    );
    return () => window.clearTimeout(timer);
  }, [day]);
  return day;
}

let clockFormat: Intl.DateTimeFormat | null = null;

/** The clock beside today's title; its ticks re-render only this text. */
function SkyTime() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_MS);
    return () => window.clearInterval(timer);
  }, []);
  clockFormat ??= new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  });
  return (
    <time
      className="text-xs text-muted-foreground tabular-nums"
      dateTime={now.toISOString()}
    >
      {clockFormat.format(now)}
    </time>
  );
}

/** `color` with `alpha`, for the canvas, which cannot read CSS variables. */
function withAlpha(color: string, alpha: number): string {
  const hex = /^#([\da-f]{3,8})$/i.exec(color);
  if (hex) {
    const digits =
      hex[1].length <= 4
        ? [...hex[1]].map((digit) => digit + digit).join("")
        : hex[1];
    const [r, g, b] = [0, 2, 4].map((at) =>
      Number.parseInt(digits.slice(at, at + 2), 16),
    );
    return `rgba(${r},${g},${b},${alpha})`;
  }
  const rgb = /^rgba?\(([^)]+)\)$/i.exec(color);
  if (rgb) {
    const [r, g, b] = rgb[1].split(/[\s,/]+/);
    return `rgba(${r},${g},${b},${alpha})`;
  }
  return color;
}

/**
 * Paints the grid, the stars and every trail. A canvas keeps each hairline's
 * own alpha, so a route's trails build up where they meet the nodes, as many
 * faint strokes do, and it paints once per data, width or theme change.
 */
function paintSky(
  canvas: HTMLCanvasElement,
  plan: SkyPlan,
  scene: SkyScene,
  look: SkyLook,
  ratio: number,
) {
  canvas.width = Math.round(scene.width * ratio);
  canvas.height = Math.round(SKY_HEIGHT * ratio);
  // Test DOMs have no 2D canvas.
  let context: CanvasRenderingContext2D | null = null;
  try {
    context = canvas.getContext("2d");
  } catch {
    return;
  }
  if (!context) return;
  const style = getComputedStyle(canvas);
  const resolve = (value: string) => {
    const name = /^var\((--[\w-]+)\)$/.exec(value)?.[1];
    return name ? style.getPropertyValue(name).trim() || "#8a96a8" : value;
  };
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  context.clearRect(0, 0, scene.width, SKY_HEIGHT);
  const { sun } = scene;

  if (look.grid) {
    const grid = skyGrid(scene);
    context.strokeStyle = resolve("var(--sky-grid)");
    context.lineWidth = 1;
    context.globalAlpha = 0.09;
    for (const ring of grid.rings) {
      context.setLineDash(ring.dash);
      context.beginPath();
      context.ellipse(sun.x, sun.y, ring.rx, ring.ry, 0, 0, Math.PI * 2);
      context.stroke();
    }
    context.setLineDash([]);
    context.globalAlpha = 0.06;
    context.beginPath();
    for (const [[x0, y0], [x1, y1]] of grid.meridians) {
      context.moveTo(x0, y0);
      context.lineTo(x1, y1);
    }
    context.stroke();
  }

  context.fillStyle = resolve("var(--sky-star)");
  for (const star of skyStars(scene.width, look.sparkles)) {
    context.globalAlpha = look.stars * star.opacity;
    if (star.sparkle)
      context.fill(new Path2D(sparklePath(star.x, star.y, star.size)));
    else {
      context.beginPath();
      context.arc(star.x, star.y, star.size, 0, Math.PI * 2);
      context.fill();
    }
  }

  // Trails fade into the hub color at the sun and back out of it.
  const hub = resolve("var(--sky-hub)");
  const incoming = plan.clients.map((client, index) => {
    const spot = scene.clients[index];
    const gradient = context.createLinearGradient(spot.x, spot.y, sun.x, sun.y);
    gradient.addColorStop(0, withAlpha(resolve(client.color), 1));
    gradient.addColorStop(1, withAlpha(hub, 0.25));
    return gradient;
  });
  const landings = [
    ...plan.planets.map((planet, index) => ({
      color: planet.color,
      spot: scene.planets[index],
    })),
    ...(scene.cluster ? [{ color: SKY_MUTED, spot: scene.cluster }] : []),
  ];
  const outgoing = landings.map(({ color, spot }) => {
    const gradient = context.createLinearGradient(sun.x, sun.y, spot.x, spot.y);
    gradient.addColorStop(0, withAlpha(hub, 0.25));
    gradient.addColorStop(1, withAlpha(resolve(color), 1));
    return gradient;
  });
  const stroke = (curve: SkyCourse["in"], alpha: number) => {
    const [[x0, y0], [x1, y1], [x2, y2], [x3, y3]] = curve;
    context.globalAlpha = alpha;
    context.beginPath();
    context.moveTo(x0, y0);
    context.bezierCurveTo(x1, y1, x2, y2, x3, y3);
    context.stroke();
  };
  context.lineWidth = 0.9;
  for (const strand of scene.strands) {
    context.strokeStyle = incoming[strand.client];
    stroke(strand.in, strand.opacity);
    if (strand.out && strand.landing !== null && outgoing[strand.landing]) {
      context.strokeStyle = outgoing[strand.landing];
      stroke(strand.out, strand.outOpacity);
    }
  }
  context.globalAlpha = 1;
}

interface NodeView {
  key: string;
  slot: string;
  x: number;
  y: number;
  share: number | null;
  color: string;
  name: string;
  /** Name beside the node rather than below it. */
  beside: boolean;
  room: number;
  icon: { kind: ServiceKind } | { client: SkyPlan["clients"][number] } | null;
  /** Stands for several clients. */
  more: boolean;
}

/** Clients and providers as drawn: position, wash, name and icon. */
function nodeViews(
  plan: SkyPlan,
  scene: SkyScene,
  t: ReturnType<typeof useT>,
): NodeView[] {
  const clients = plan.clients.map((client, index): NodeView => {
    const spot = scene.clients[index];
    return {
      key: `client-${client.id ?? "rest"}`,
      slot: "request-sky-client",
      ...spot,
      color: client.color,
      name:
        client.folded > 1
          ? t("overview.skyOtherClients", { count: client.folded })
          : clientTypeName(client.id ?? "unknown"),
      beside: scene.compact,
      room: scene.compact ? 96 : Math.min(144, 2 * spot.x - 8),
      icon: client.folded > 1 ? null : { client },
      more: client.folded > 1,
    };
  });
  const planets = plan.planets.map((planet, index): NodeView => {
    const spot = scene.planets[index];
    return {
      key: `planet-${planet.id ?? `removed-${index}`}`,
      slot: "request-sky-planet",
      ...spot,
      color: planet.color,
      name: planet.name ?? t("overview.skyRemovedProvider"),
      beside: spot.side,
      room: spot.side
        ? scene.width - spot.x - SKY_NODE_R - 14
        : Math.min(144, 2 * (scene.width - spot.x) - 8),
      icon: planet.kind ? { kind: planet.kind } : null,
      more: false,
    };
  });
  return [...clients, ...planets];
}

/** Three dots inside a node that stands for several. */
function morePath(x: number, y: number) {
  return [-4, 0, 4]
    .map(
      (dx) => `M${x + dx - 1.3},${y}a1.3,1.3 0 1,0 2.6,0a1.3,1.3 0 1,0 -2.6,0`,
    )
    .join("");
}

/**
 * Everything that changes only with the data, the width or the theme: the
 * canvas of trails, the sun with its flares, the nodes and their names.
 * Clock ticks, comet frames and the activity poll never render it.
 */
const SkyStatic = memo(function SkyStatic({
  gid,
  plan,
  scene,
  theme,
}: {
  gid: string;
  plan: SkyPlan;
  scene: SkyScene;
  theme: ResolvedTheme;
}) {
  const t = useT();
  const look = skyLook(theme);
  const canvas = useRef<HTMLCanvasElement>(null);
  const ratio = useDevicePixelRatio();
  useLayoutEffect(() => {
    if (canvas.current) paintSky(canvas.current, plan, scene, look, ratio);
  }, [look, plan, ratio, scene]);

  const { sun, width } = scene;
  const nodes = nodeViews(plan, scene, t);
  const cluster = scene.cluster;
  const washed = [
    ...nodes,
    ...(cluster
      ? [{ key: "cluster", color: SKY_MUTED, share: cluster.share }]
      : []),
  ].filter((node) => node.share !== null);
  const fill = (key: string) => `url(#${gid}-wash-${key})`;
  const wash = (key: string, x: number, y: number, share: number | null) =>
    share === null ? null : (
      <circle
        cx={x}
        cy={y}
        fill={fill(key)}
        opacity={look.wash[0] + share * look.wash[1]}
        r={SKY_NODE_R + 6 + share * 18}
      />
    );

  return (
    <>
      <canvas
        aria-hidden="true"
        className="absolute inset-0 block"
        data-slot="request-sky-trails"
        ref={canvas}
        style={{ height: SKY_HEIGHT, width }}
      />
      <svg
        aria-hidden="true"
        className="absolute inset-0 block"
        height={SKY_HEIGHT}
        width={width}
      >
        <defs>
          {look.warmSun ? (
            <radialGradient id={`${gid}-sun`}>
              <stop offset="0" stopColor="var(--sky-sun-core)" />
              <stop offset="0.6" stopColor="var(--sky-sun)" />
              <stop offset="1" stopColor="var(--sky-sun-edge)" />
            </radialGradient>
          ) : null}
          <radialGradient id={`${gid}-halo`}>
            <stop
              offset="0"
              stopColor="var(--sky-sun-halo)"
              stopOpacity={look.halo}
            />
            <stop offset="1" stopColor="var(--sky-sun-halo)" stopOpacity="0" />
          </radialGradient>
          <radialGradient
            cx={sun.x}
            cy={sun.y}
            gradientUnits="userSpaceOnUse"
            id={`${gid}-flare`}
            r="80"
          >
            <stop
              offset="0.3"
              stopColor="var(--sky-flare-core)"
              stopOpacity={look.flare[0]}
            />
            <stop
              offset="0.75"
              stopColor="var(--sky-flare)"
              stopOpacity={look.flare[1]}
            />
            <stop offset="1" stopColor="var(--sky-flare)" stopOpacity="0" />
          </radialGradient>
          {washed.map((node) => (
            <radialGradient id={`${gid}-wash-${node.key}`} key={node.key}>
              <stop offset="0.35" stopColor={node.color} />
              <stop offset="1" stopColor={node.color} stopOpacity="0" />
            </radialGradient>
          ))}
        </defs>
        <circle cx={sun.x} cy={sun.y} fill={`url(#${gid}-halo)`} r={72} />
        {scene.flares.map((flare) => (
          <g
            data-kind={flare.kind}
            data-slot="request-sky-flare"
            key={flare.kind}
          >
            <path d={flare.path} fill={`url(#${gid}-flare)`} opacity={0.85} />
            <circle
              cx={flare.dot[0]}
              cy={flare.dot[1]}
              fill={SKY_REWRITE_COLORS[flare.kind]}
              r={2.6}
            />
            <text
              {...NAME_TEXT}
              fill="var(--sky-label)"
              fontSize={11}
              fontWeight={600}
              strokeWidth={5}
              textAnchor={flare.anchor}
              x={flare.label[0]}
              y={flare.label[1]}
            >
              {t(REWRITE_LABELS[flare.kind])}
            </text>
          </g>
        ))}
        {look.warmSun ? (
          <circle cx={sun.x} cy={sun.y} fill={`url(#${gid}-sun)`} r={28} />
        ) : (
          <circle
            cx={sun.x}
            cy={sun.y}
            fill="var(--sky-node-fill)"
            r={28}
            stroke="var(--sky-sun-ring)"
          />
        )}
        <image
          height={30}
          href={astrlinkLogo}
          width={30}
          x={sun.x - 15}
          y={sun.y - 15}
        />
        {scene.hops.length > 0 ? (
          <path
            d={scene.hops.map(hopPath).join("")}
            data-slot="request-sky-failover"
            fill="none"
            opacity={0.85}
            stroke="var(--sky-failover)"
            strokeLinecap="round"
            strokeWidth={1.4}
          />
        ) : null}
        {scene.fails.length > 0 ? (
          <path
            d={scene.fails.map(([x, y]) => sparklePath(x, y, 3)).join("")}
            data-slot="request-sky-fail"
            fill="var(--sky-fail)"
            opacity={0.95}
          />
        ) : null}
        {nodes.map((node) => (
          // A node without requests in the range stays in place, dimmed.
          <g
            data-idle={node.share === null || undefined}
            data-slot={node.slot}
            key={node.key}
            opacity={node.share === null ? look.dim : undefined}
          >
            {wash(node.key, node.x, node.y, node.share)}
            <circle
              cx={node.x}
              cy={node.y}
              fill="var(--sky-node-fill)"
              r={SKY_NODE_R}
              stroke={node.color}
              strokeWidth={1.6}
            />
            {node.more ? (
              <path d={morePath(node.x, node.y)} fill="var(--sky-muted)" />
            ) : null}
            <text
              {...NAME_TEXT}
              textAnchor={node.beside ? "start" : "middle"}
              x={node.beside ? node.x + SKY_NODE_R + 6 : node.x}
              y={node.beside ? node.y + 4 : node.y + SKY_NODE_R + 17}
            >
              {fitLabel(node.name, node.room)}
            </text>
          </g>
        ))}
        {cluster ? (
          <g
            data-slot="request-sky-cluster"
            opacity={cluster.share === null ? look.dim : undefined}
          >
            {wash("cluster", cluster.x, cluster.y, cluster.share)}
            <circle
              cx={cluster.x}
              cy={cluster.y}
              fill="var(--sky-node-fill)"
              r={SKY_NODE_R}
              stroke={SKY_MUTED}
              strokeWidth={1.6}
            />
            <path d={morePath(cluster.x, cluster.y)} fill="var(--sky-muted)" />
          </g>
        ) : null}
      </svg>
      <div aria-hidden="true" className="pointer-events-none absolute inset-0">
        {nodes.map((node) =>
          node.icon ? (
            <span
              className="absolute flex size-3.25 items-center justify-center"
              key={node.key}
              style={{
                color: "var(--sky-node-ink)",
                left: node.x - 6.5,
                opacity: node.share === null ? look.dim : undefined,
                top: node.y - 6.5,
              }}
            >
              {"kind" in node.icon ? (
                <ServiceKindIcon kind={node.icon.kind} size={13} />
              ) : (
                <ClientTypeIcon
                  clientType={node.icon.client.id}
                  decorative
                  size={13}
                />
              )}
            </span>
          ) : null,
        )}
      </div>
      <ClusterButton plan={plan} scene={scene} />
    </>
  );
});

function ClusterButton({ plan, scene }: { plan: SkyPlan; scene: SkyScene }) {
  const t = useT();
  if (!scene.cluster || plan.cluster.length === 0) return null;
  const label = t("overview.skyMoreProviders", { count: plan.cluster.length });
  // Beside the arc the name follows the node, as planet names do.
  const beside = scene.planets[0]?.side ?? false;
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          className="absolute flex h-6 items-center gap-0.5 rounded-full px-1.5 text-xs font-[550] text-(--sky-ink) [text-shadow:0_0_3px_var(--sky-halo),0_0_2px_var(--sky-halo)] hover:bg-foreground/6 hover:text-(--sky-ink) focus-visible:outline-2 focus-visible:outline-ring has-[>svg]:px-1.5 aria-expanded:bg-foreground/6"
          data-slot="request-sky-more"
          style={
            beside
              ? {
                  left: scene.cluster.x + SKY_NODE_R + 1,
                  top: scene.cluster.y - 12,
                }
              : {
                  left: scene.cluster.x,
                  top: scene.cluster.y + SKY_NODE_R + 4,
                  transform: "translateX(-50%)",
                }
          }
          size="xs"
          type="button"
          variant="ghost"
        >
          {label}
          <ChevronRight
            aria-hidden="true"
            className="size-3 text-muted-foreground"
          />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" animated className="w-64 p-2 text-xs">
        <p className="px-1.5 pb-1.5 font-medium">{label}</p>
        <ul className="grid gap-0.5">
          {plan.cluster.map((provider, index) => (
            <li
              className="flex items-center gap-2 rounded-sm px-1.5 py-1"
              key={provider.id ?? `removed-${index}`}
            >
              {provider.kind ? (
                <ServiceKindIcon kind={provider.kind} size={14} />
              ) : (
                <span className="size-3.5" />
              )}
              <span className="min-w-0 flex-1 truncate">
                {provider.name ?? t("overview.skyRemovedProvider")}
              </span>
              <span className="text-muted-foreground tabular-nums">
                {t("overview.skyRequests", { count: provider.requests })}
              </span>
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  );
}

/** A flare lights up as a comet with that rewrite passes the sun. */
function flareHeat(comets: readonly SkyComet[], now: number) {
  const heat = new Map<GatewayRewrite, number>();
  for (const comet of comets) {
    for (const kind of comet.rewrites) {
      const incoming = GATEWAY_REWRITES.indexOf(kind) < 5;
      const peak = comet.start + COMET_IN_MS + (incoming ? -120 : 160);
      const value = clamp01(1 - Math.abs(now - peak) / 380);
      if (value > (heat.get(kind) ?? 0)) heat.set(kind, value);
    }
  }
  return heat;
}

/** Ease in toward the sun, then fling out of it. */
const easeIn = (t: number) => t ** 1.6;
const easeOut = (t: number) => 1 - (1 - t) ** 1.8;

function svg<K extends keyof SVGElementTagNameMap>(
  tag: K,
  attributes: Record<string, string | number>,
  parent: Element,
): SVGElementTagNameMap[K] {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [name, value] of Object.entries(attributes))
    node.setAttribute(name, String(value));
  parent.append(node);
  return node;
}

function set(node: Element, name: string, value: string | number) {
  const text = String(value);
  if (node.getAttribute(name) !== text) node.setAttribute(name, text);
}

interface CometNodes {
  group: SVGGElement;
  course: SkyCourse | null;
  scene: SkyScene;
  trailIn: SVGPathElement;
  trailOut: SVGPathElement;
  hop: SVGPathElement;
  glow: SVGCircleElement;
  head: SVGCircleElement;
  star: SVGPathElement;
}

interface FlashNodes {
  group: SVGGElement;
  ray: SVGPathElement;
  glow: SVGCircleElement;
  dot: SVGCircleElement;
  label: SVGTextElement;
}

interface CometContext {
  gid: string;
  look: SkyLook;
  plan: SkyPlan;
  scene: SkyScene;
  labels: Record<GatewayRewrite, string>;
}

function cometNodes(
  layer: SVGGElement,
  comet: SkyComet,
  { plan, scene }: CometContext,
): CometNodes {
  const route = skyCometRoute(plan, comet);
  const course = route
    ? skyCourse(
        scene,
        comet.id,
        route.client,
        route.target,
        route.failedAt,
        comet.failed,
      )
    : null;
  const landing = route ? (route.failedAt ?? route.target) : null;
  const group = svg(
    "g",
    { "data-slot": "request-sky-comet", fill: "none" },
    layer,
  );
  const trail = (color: string) =>
    svg("path", { stroke: color, "stroke-width": 1, opacity: 0 }, group);
  return {
    group,
    course,
    scene,
    trailIn: trail(route ? plan.clients[route.client].color : SKY_MUTED),
    trailOut: trail(
      landing === null
        ? SKY_MUTED
        : (plan.planets[landing]?.color ?? SKY_MUTED),
    ),
    hop: svg(
      "path",
      {
        stroke: "var(--sky-failover)",
        "stroke-linecap": "round",
        "stroke-width": 1.4,
        opacity: 0,
      },
      group,
    ),
    star: svg("path", { fill: "var(--sky-fail)", opacity: 0 }, group),
    glow: svg(
      "circle",
      { fill: "var(--sky-head-glow)", opacity: 0, r: 8 },
      group,
    ),
    head: svg("circle", { fill: "var(--sky-head)", opacity: 0, r: 2.6 }, group),
  };
}

function drawComet(nodes: CometNodes, comet: SkyComet, now: number) {
  const { course } = nodes;
  if (!course) return;
  const elapsed = now - comet.start;
  const outMs = course.out ? COMET_OUT_MS : 0;
  const hopMs = course.hop ? COMET_HOP_MS : 0;
  const travel = COMET_IN_MS + outMs + hopMs;
  const fade = clamp01(1 - (elapsed - travel) / COMET_FADE_MS);

  const p = easeIn(clamp01(elapsed / COMET_IN_MS));
  set(nodes.trailIn, "d", cubicPath(course.in, p));
  set(nodes.trailIn, "opacity", (0.6 * fade).toFixed(3));
  let at: Point = cubicAt(course.in, p);
  if (course.out && elapsed >= COMET_IN_MS) {
    const q = easeOut(clamp01((elapsed - COMET_IN_MS) / COMET_OUT_MS));
    set(nodes.trailOut, "d", cubicPath(course.out, q));
    set(nodes.trailOut, "opacity", (0.6 * fade).toFixed(3));
    at = cubicAt(course.out, q);
    if (comet.failed && q >= 1) {
      set(nodes.star, "d", sparklePath(course.out[3][0], course.out[3][1], 3));
      set(nodes.star, "opacity", (0.95 * fade).toFixed(3));
    }
  }
  if (course.hop && elapsed >= COMET_IN_MS + outMs) {
    const h = easeOut(clamp01((elapsed - COMET_IN_MS - outMs) / COMET_HOP_MS));
    const curve = hopCurve(course.hop);
    set(nodes.hop, "d", cubicPath(curve, h));
    set(nodes.hop, "opacity", (0.85 * fade).toFixed(3));
    at = cubicAt(curve, h);
  }
  const flying = elapsed < travel;
  for (const head of [nodes.glow, nodes.head]) {
    set(head, "cx", at[0].toFixed(1));
    set(head, "cy", at[1].toFixed(1));
  }
  set(nodes.glow, "opacity", flying ? 0.22 : 0);
  set(nodes.head, "opacity", flying ? 1 : 0);
}

function flashNodes(
  layer: SVGGElement,
  kind: GatewayRewrite,
  { gid, labels }: CometContext,
): FlashNodes {
  const group = svg(
    "g",
    { "data-kind": kind, "data-slot": "request-sky-flash" },
    layer,
  );
  const label = svg(
    "text",
    {
      fill: "var(--sky-label)",
      "font-size": 11,
      "font-weight": 600,
      "paint-order": "stroke",
      stroke: "var(--sky-halo)",
      "stroke-linejoin": "round",
      "stroke-width": 5,
    },
    group,
  );
  label.textContent = labels[kind];
  return {
    group,
    ray: svg("path", { fill: `url(#${gid}-flare)` }, group),
    glow: svg("circle", { fill: SKY_REWRITE_COLORS[kind] }, group),
    dot: svg("circle", { fill: SKY_REWRITE_COLORS[kind], r: 2.6 }, group),
    label,
  };
}

/**
 * A flare grows while a comet carrying it passes. A rewrite unused in the
 * range has no flare yet, so this draws its whole flare for the moment.
 */
function drawFlash(
  nodes: FlashNodes,
  kind: GatewayRewrite,
  hot: number,
  { look, plan, scene }: CometContext,
) {
  const count = plan.rewrites[kind];
  const flare = skyFlare(scene, kind, count, hot);
  const rest = count > 0 ? skyFlare(scene, kind, count).dot : flare.dot;
  set(nodes.ray, "d", flare.path);
  set(
    nodes.ray,
    "opacity",
    (count > 0 ? 0.5 * hot : (0.85 + 0.15 * hot) * hot).toFixed(3),
  );
  set(nodes.glow, "cx", rest[0].toFixed(1));
  set(nodes.glow, "cy", rest[1].toFixed(1));
  set(nodes.glow, "r", (6 + 8 * hot).toFixed(1));
  set(nodes.glow, "opacity", (look.flash * hot).toFixed(3));
  const own = count === 0;
  set(nodes.dot, "cx", flare.dot[0].toFixed(1));
  set(nodes.dot, "cy", flare.dot[1].toFixed(1));
  set(nodes.dot, "opacity", own ? hot.toFixed(3) : 0);
  set(nodes.label, "x", flare.label[0].toFixed(1));
  set(nodes.label, "y", flare.label[1].toFixed(1));
  set(nodes.label, "text-anchor", flare.anchor);
  set(nodes.label, "opacity", own ? hot.toFixed(3) : 0);
}

function callKey(call: { session_id: string; started_at: string }) {
  return `${call.session_id}|${call.started_at}`;
}

/**
 * Calls that finished since the last read. The first read after the feed
 * starts only marks what already finished, so opening the page or scrolling
 * back to the sky does not replay the last minutes.
 */
function useFinishedCalls(
  activity: RequestActivity | null,
  enabled: boolean,
  onFinished: (requests: SkyCometRequest[]) => void,
) {
  const seen = useRef<Set<string> | null>(null);
  // The snapshot held from before the feed started is not a fresh read.
  const stale = useRef<RequestActivity | null>(activity);
  const handler = useRef(onFinished);
  handler.current = onFinished;
  useEffect(() => {
    if (!enabled) {
      seen.current = null;
      stale.current = activity;
      return;
    }
    if (!activity || activity === stale.current) return;
    const finished = activity.calls.filter(
      (call) =>
        call.completed_at !== null &&
        (call.status === "succeeded" || call.status === "failed"),
    );
    const known = seen.current;
    seen.current = new Set(finished.map(callKey));
    if (!known) return;
    const fresh = finished
      .filter((call) => !known.has(callKey(call)))
      .reverse()
      .map(
        (call): SkyCometRequest => ({
          id: callKey(call),
          client_type: call.client_type ?? null,
          service_id: call.service_id ?? null,
          failed_service_id: call.failed_service_id ?? null,
          failed: call.status === "failed",
          rewrites: call.rewrites ?? [],
        }),
      );
    if (fresh.length > 0) handler.current(fresh);
  }, [activity, enabled]);
}

/**
 * Flies comets for calls that just finished, while the sky is on screen.
 * Frames write SVG attributes directly; React renders this layer only when
 * the activity feed changes.
 */
function SkyComets({
  coreSessionKey,
  enabled,
  labels,
  look,
  gid,
  onFinished,
  plan,
  scene,
  sectionRef,
}: CometContext & {
  coreSessionKey: string | null;
  enabled: boolean;
  onFinished: () => void;
  sectionRef: RefObject<HTMLElement | null>;
}) {
  const visible = useOnScreen(sectionRef);
  const active = enabled && visible;
  const host = useRef<SVGSVGElement>(null);
  const layer = useRef<SVGGElement>(null);
  const context = useRef<CometContext>({ gid, labels, look, plan, scene });
  context.current = { gid, labels, look, plan, scene };
  const comets = useRef(new Map<string, CometNodes>());
  const flashes = useRef(new Map<GatewayRewrite, FlashNodes>());

  const draw = useCallback((now: number, flying: readonly SkyComet[]) => {
    const root = layer.current;
    if (!root) return;
    const current = context.current;
    const live = new Set<string>();
    for (const comet of flying) {
      const key = `${comet.id}@${comet.start}`;
      live.add(key);
      let nodes = comets.current.get(key);
      // A new width moves the nodes; the comet follows.
      if (nodes && nodes.scene !== current.scene) {
        nodes.group.remove();
        nodes = undefined;
      }
      if (!nodes) {
        nodes = cometNodes(root, comet, current);
        comets.current.set(key, nodes);
      }
      drawComet(nodes, comet, now);
    }
    for (const [key, nodes] of comets.current)
      if (!live.has(key)) {
        nodes.group.remove();
        comets.current.delete(key);
      }
    const heat = flareHeat(flying, now);
    for (const kind of GATEWAY_REWRITES) {
      const hot = heat.get(kind) ?? 0;
      let nodes = flashes.current.get(kind);
      if (hot <= 0) {
        nodes?.group.remove();
        flashes.current.delete(kind);
        continue;
      }
      if (!nodes) {
        nodes = flashNodes(root, kind, current);
        flashes.current.set(kind, nodes);
      }
      drawFlash(nodes, kind, hot, current);
    }
    // Its own layer while comets fly, so frames never repaint the sky.
    host.current?.style.setProperty(
      "will-change",
      flying.length > 0 ? "transform" : "auto",
    );
  }, []);
  const launch = useSkyComets(active, draw);
  const activity = useRequestActivityPoll(coreSessionKey, active);
  const finished = useRef(onFinished);
  finished.current = onFinished;
  useFinishedCalls(activity, active, (requests) => {
    launch(requests);
    finished.current();
  });

  return (
    <svg
      aria-hidden="true"
      className="pointer-events-none absolute inset-0"
      height={SKY_HEIGHT}
      ref={host}
      width={scene.width}
    >
      <g ref={layer} />
    </svg>
  );
}

/** The sky's own read: today's whole local day, or the last 30 days. */
function skyWindow(range: SkyRange, now: Date) {
  const today = localDayKey(now);
  return resolveUsageWindow(
    range === "today" ? { from: today, to: today } : "30d",
    now,
  );
}

interface SkyRead {
  routes: UsageRoute[];
  days: { date: string; requests: number }[];
  /** When it was read, for the age of the 30-day trails. */
  at: number;
}

/** A read; "error" when it failed with nothing to keep; null until then. */
type SkySlot = SkyRead | "error" | null;

interface SkyData {
  today: SkySlot;
  month: SkySlot;
  /** The overview's read the month came from, if it did. */
  monthFrom: UsageSummary | null;
}

const landed = (slot: SkySlot) => (slot === "error" ? null : slot);

function skyRead(summary: UsageSummary): SkyRead {
  return {
    routes: summary.by_route,
    days: summary.by_day.map((day) => ({
      date: day.date,
      requests: day.requests,
    })),
    at: Date.now(),
  };
}

async function readSky(range: SkyRange): Promise<SkyRead> {
  return skyRead(await getUsageSummary(skyWindow(range, new Date())));
}

/** A read of the same range again, rather than a switch to another one. */
function sameRange(left: UsageSummary, right: UsageSummary): boolean {
  return (
    left.window.preset === right.window.preset &&
    (left.window.preset !== "custom" ||
      (left.window.from === right.window.from &&
        left.window.to === right.window.to))
  );
}

/** The catalog as the sky needs it, the same array while that is unchanged. */
function useSkyServices(services: readonly Service[]): SkyService[] {
  const key = services
    .map((service) =>
      [service.id, service.kind, service.enabled ? 1 : 0, service.name].join(
        "\u0001",
      ),
    )
    .join("\u0002");
  return useMemo(
    () =>
      key
        ? key.split("\u0002").map((row) => {
            const [id, kind, enabled, ...name] = row.split("\u0001");
            return {
              id,
              kind: kind as ServiceKind,
              enabled: enabled === "1",
              name: name.join("\u0001"),
            };
          })
        : [],
    [key],
  );
}

/**
 * 「今天的请求」: clients on the left, AstrLink as the sun, providers on the
 * right, and every request as a hairline between them. The nodes sit where
 * the last 30 days put them, so switching ranges only changes the light.
 */
export function RequestSky({
  coreSessionKey,
  isReady,
  overviewMonth,
  revision,
  services,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  /**
   * The overview's own read of the last 30 days, shown here instead of a
   * second read: null while the overview reads them, undefined while it
   * shows another range.
   */
  overviewMonth?: UsageSummary | null;
  /** The overview's own read; a new read of the same range is new traffic. */
  revision: UsageSummary | null;
  services: readonly Service[];
}) {
  const t = useT();
  const gid = useId();
  const theme = useResolvedTheme();
  const look = skyLook(theme);
  const [range, setRange] = useState<SkyRange>("today");
  const day = useLocalDay();
  const reducedMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const [data, setData] = useWorkspaceSnapshot<SkyData>(
    `request-sky:${coreSessionKey}`,
    { today: null, month: null, monthFrom: null },
  );
  const [exposure, setExposure] = useState(0);
  const exposeTimer = useRef<number | null>(null);

  // The overview's first read runs alongside the sky's own, and switching
  // its range is no new traffic; only a new read of the same range is.
  const lastRevision = useRef(revision);
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    const last = lastRevision.current;
    if (Object.is(last, revision)) return;
    lastRevision.current = revision;
    if (last && revision && sameRange(last, revision))
      setGeneration((value) => value + 1);
  }, [revision]);

  // A failed read keeps what the sky already shows.
  const settle = useCallback(
    (slot: "today" | "month", read: Promise<SkyRead>) => {
      let cancelled = false;
      read.then(
        (value) => {
          if (!cancelled)
            setData((current) => ({
              ...current,
              [slot]: value,
              ...(slot === "month" ? { monthFrom: null } : {}),
            }));
        },
        () => {
          if (!cancelled)
            setData((current) => ({
              ...current,
              [slot]: current[slot] ?? "error",
            }));
        },
      );
      return () => {
        cancelled = true;
      };
    },
    [setData],
  );

  // Today, again on new traffic. The month places the nodes for both
  // ranges, so switching between them needs no read.
  useEffect(() => {
    if (isReady) return settle("today", readSky("today"));
  }, [day, generation, isReady, settle]);

  // The last 30 days: the overview's read when it shows them, the sky's own
  // otherwise.
  const ownMonth = overviewMonth === undefined;
  useEffect(() => {
    if (isReady && ownMonth) return settle("month", readSky("30d"));
  }, [day, generation, isReady, ownMonth, settle]);
  useEffect(() => {
    if (!overviewMonth) return;
    setData((current) =>
      current.monthFrom === overviewMonth
        ? current
        : {
            ...current,
            month: skyRead(overviewMonth),
            monthFrom: overviewMonth,
          },
    );
  }, [overviewMonth, setData]);

  // Landed comets add their trails to today; the month catches up with the
  // overview's next read.
  useEffect(() => {
    if (isReady && exposure > 0) return settle("today", readSky("today"));
  }, [exposure, isReady, settle]);

  useEffect(
    () => () => {
      if (exposeTimer.current !== null)
        window.clearTimeout(exposeTimer.current);
    },
    [],
  );
  const onFinished = useCallback(() => {
    // One re-read after the comets land adds their trails.
    if (exposeTimer.current !== null) return;
    exposeTimer.current = window.setTimeout(() => {
      exposeTimer.current = null;
      setExposure((value) => value + 1);
    }, EXPOSE_DELAY_MS);
  }, []);

  const [ref, width] = useWidth();
  const skyServices = useSkyServices(services);
  // Nothing draws until both ranges are in, so no node moves once it does.
  const today = landed(data.today);
  const month = landed(data.month);
  const layout = useMemo(
    () => (today && month ? [...month.routes, ...today.routes] : []),
    [month, today],
  );
  const shown = today && month ? (range === "today" ? today : month) : null;
  const routes = shown?.routes ?? null;
  const slots = skyPlanetSlots(width);
  const plan = useMemo(
    () => skyPlan(routes ?? [], skyServices, slots, layout),
    [layout, routes, skyServices, slots],
  );
  const recency = useMemo(
    () => (range === "30d" && month ? skyRecency(month.days, month.at) : null),
    [month, range],
  );
  const scene = useMemo(
    () =>
      width > 0 && routes !== null
        ? skyScene(plan, width, { hairMax: look.hairMax, recency })
        : null,
    [look.hairMax, plan, recency, routes, width],
  );
  const labels = Object.fromEntries(
    GATEWAY_REWRITES.map((kind) => [kind, t(REWRITE_LABELS[kind])]),
  ) as Record<GatewayRewrite, string>;

  const title = t(
    range === "today" ? "overview.skyToday" : "overview.skyMonth",
  );
  const empty = routes !== null && routes.length === 0;
  const failed =
    routes === null && (data.today === "error" || data.month === "error");
  const providers = plan.planets.length + plan.cluster.length;

  return (
    <section
      aria-busy={routes === null && !failed}
      aria-labelledby="request-sky-heading"
      className="relative min-w-0 overflow-hidden rounded-md"
      data-slot="request-sky"
      ref={ref}
      style={{
        background: "var(--sky-bg)",
        height: SKY_HEIGHT,
        outline: "1px solid var(--sky-outline)",
        outlineOffset: -1,
      }}
    >
      {scene ? (
        <>
          <SkyStatic gid={gid} plan={plan} scene={scene} theme={theme} />
          <SkyComets
            coreSessionKey={coreSessionKey}
            enabled={isReady && range === "today" && !reducedMotion}
            gid={gid}
            labels={labels}
            look={look}
            onFinished={onFinished}
            plan={plan}
            scene={scene}
            sectionRef={ref}
          />
        </>
      ) : null}
      {empty || failed ? (
        <p className="absolute inset-x-0 bottom-5 text-center text-xs text-muted-foreground">
          {failed
            ? t("overview.skyLoadFailed")
            : t(
                range === "today"
                  ? "overview.skyEmptyToday"
                  : "overview.skyEmptyMonth",
              )}
        </p>
      ) : null}
      <div className="absolute inset-x-0 top-0 flex items-center justify-between gap-3 px-4 pt-3">
        <div className="flex min-w-0 items-center gap-2">
          <h2
            className="truncate text-sm font-semibold text-foreground"
            id="request-sky-heading"
          >
            {title}
          </h2>
          {range === "today" ? <SkyTime /> : null}
          <HelpPopover label={t("overview.skyHelpLabel")}>
            <div className="grid gap-2">
              <p>{t("overview.skyHelpTrails")}</p>
              <p>{t("overview.skyHelpFlares")}</p>
              <p>{t("overview.skyHelpFailover")}</p>
              <p>{t("overview.skyHelpComets")}</p>
              <p className="text-muted-foreground">
                {t("overview.skyHelpHistory")}
              </p>
            </div>
          </HelpPopover>
        </div>
        <SegmentedControl
          label={t("overview.skyRange")}
          onValueChange={setRange}
          options={[
            { value: "today", label: t("overview.skyRangeToday") },
            { value: "30d", label: t("overview.skyRangeMonth") },
          ]}
          value={range}
        />
      </div>
      {routes && routes.length > 0 ? (
        <p className="sr-only">
          {t("overview.skySummary", {
            requests: plan.requests,
            clients: plan.clients.reduce(
              (sum, client) => sum + client.folded,
              0,
            ),
            providers,
          })}
        </p>
      ) : null}
    </section>
  );
}
