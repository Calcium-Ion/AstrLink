import {
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { clientTypeName } from "@/components/ClientTypeIcon";
import { Brain } from "@/components/icons";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusDot } from "@/components/StatusDot";
import { vendorMarks, type Vendor } from "@/components/vendor-marks";
import { useDevicePixelRatio } from "@/lib/use-device-pixel-ratio";
import { useMediaQuery } from "@/lib/use-media-query";
import { useOnScreen, usePageVisible } from "@/lib/use-on-screen";

import { formatCompactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import {
  BRAIN_MARK,
  clamp,
  DEMO_WIDTH,
  drawCrew,
  markOf,
  Pen,
  readCrewPalette,
  spawn,
  stepOrb,
  U,
  type CrewPalette,
  type CrewWords,
} from "./live-crew-draw";
import {
  celebrate,
  createCrew,
  CREW_STAGES,
  crewGrowth,
  readCrew,
  rescaleCrew,
  settleCrew,
  stagePassed,
  stepCrew,
  syncCrew,
  type Crew,
  type CrewReading,
} from "./live-crew-model";
import {
  createScene,
  CREW_SCENES,
  DEFAULT_CREW_SCENE,
  CREW_HEIGHT,
  type CrewScene,
  type CrewSceneId,
} from "./live-crew-scenes";
import { useRequestActivityPoll } from "./RequestActivityBoard";
import { useResolvedTheme } from "./theme";

export const LIVE_CREW_SCENE_STORAGE_KEY =
  "astrlink.overview.live-crew.scene.v1";

const FRAME_MS = 1000 / 30;
/** The model steps in frames no longer than this, as in the demo. */
const STEP_S = 1 / 30;
/** How long the scene keeps drawing after the last character left. */
const SETTLE_S = 1;
/** How long the milestone banner stays up without motion. */
const STILL_BANNER_MS = 3500;

function readSceneChoice(): CrewSceneId {
  try {
    const saved = localStorage.getItem(LIVE_CREW_SCENE_STORAGE_KEY);
    return CREW_SCENES.find((id) => id === saved) ?? DEFAULT_CREW_SCENE;
  } catch {
    return DEFAULT_CREW_SCENE;
  }
}

function saveSceneChoice(id: CrewSceneId) {
  try {
    localStorage.setItem(LIVE_CREW_SCENE_STORAGE_KEY, id);
  } catch {
    // The choice holds for this session.
  }
}

export interface CrewFit {
  /** Device px per logical px; an art pixel is `U * m` device px. */
  m: number;
  /** Logical width. */
  W: number;
  /** Canvas size in device px. */
  width: number;
  height: number;
  /** Canvas height in CSS px. */
  cssHeight: number;
}

/**
 * Sizes a scene for a CSS width: art pixels shrink with the width down to
 * 0.6 of the demo's, always a whole number of device pixels, and the scene
 * widens to fill the rest.
 */
export function crewFit(cssWidth: number, dpr: number, H: number): CrewFit {
  const k = clamp(cssWidth / DEMO_WIDTH, 0.6, 1);
  const m = Math.max(2, Math.round(U * k * dpr)) / U;
  const width = Math.max(1, Math.round(cssWidth * dpr));
  const height = Math.round(H * m);
  return { m, W: width / m, width, height, cssHeight: height / dpr };
}

/**
 * Runs a crew on a canvas: steps the model and draws at most 30 times a
 * second while it is on screen and has something moving, and draws one still
 * picture per change without motion. React only hands it changes.
 */
export class CrewEngine {
  private crew: Crew = createCrew();
  private sceneId: CrewSceneId;
  private scene: CrewScene | null = null;
  private canvas: HTMLCanvasElement | null = null;
  private g: CanvasRenderingContext2D | null = null;
  private fit: CrewFit | null = null;
  private cssWidth = 0;
  private dpr = 1;
  private palette: CrewPalette | null = null;
  private font = "sans-serif";
  private pen: Pen | null = null;
  private back: HTMLCanvasElement | null = null;
  private front: HTMLCanvasElement | null = null;
  private reading: CrewReading = { members: [], rate: 0 };
  private seen = "";
  private words: CrewWords = { failover: "", name: clientTypeName };
  private running = false;
  private still = false;
  private frame = 0;
  private last = 0;
  private settled = 0;
  private bannerTimer = 0;
  private tokens: number | null = null;
  /** What the cached layers show of today's growth. */
  private grown = "";
  /** The next bake sparkles what it adds. */
  private burst = false;
  private markSvgs: ReadonlyMap<string, string> = new Map();
  private marks = new Map<string, HTMLImageElement>();
  private loading = new Set<string>();
  private markInk = "";

  constructor(sceneId: CrewSceneId) {
    this.sceneId = sceneId;
  }

  attach(canvas: HTMLCanvasElement) {
    this.canvas = canvas;
    this.g = canvas.getContext("2d", { alpha: false });
    this.font = getComputedStyle(canvas).fontFamily || this.font;
    this.palette = readCrewPalette(canvas);
    document.fonts?.addEventListener("loadingdone", this.fontsLoaded);
  }

  destroy() {
    document.fonts?.removeEventListener("loadingdone", this.fontsLoaded);
    this.stop();
    window.clearTimeout(this.bannerTimer);
    this.canvas = null;
    this.g = null;
  }

  setWords(words: CrewWords) {
    this.words = words;
    this.redraw();
  }

  /** The theme changed: read the colors again. */
  repaint() {
    if (!this.canvas) return;
    this.palette = readCrewPalette(this.canvas);
    this.invalidate();
    this.loadMarks();
    this.redraw();
  }

  /** Today's tokens: the scene grows a stage at a time. */
  setTokens(tokens: number | null) {
    // The first read only sets where the day is; growing while watching
    // sparkles.
    const watching = this.tokens !== null;
    this.tokens = tokens;
    const scene = this.scene;
    if (!scene) return;
    const grown = scene.grow(crewGrowth(tokens));
    if (grown === this.grown) return;
    this.grown = grown;
    this.back = null;
    this.front = null;
    this.burst = watching && !this.still;
    this.redraw();
    this.wake();
  }

  /** The vendor marks the crew wears, as SVG; each is rasterised once. */
  setMarks(svgs: ReadonlyMap<string, string>) {
    this.markSvgs = svgs;
    this.loadMarks();
  }

  private loadMarks() {
    const ink = this.palette?.ink;
    if (!ink) return;
    if (ink !== this.markInk) {
      // Mono marks take the crew's ink, so a new theme loads them again.
      this.markInk = ink;
      this.marks = new Map();
      this.loading.clear();
    }
    for (const [mark, svg] of this.markSvgs) {
      if (this.marks.has(mark) || this.loading.has(mark)) continue;
      this.loading.add(mark);
      const image = new Image();
      image.onload = () => {
        if (this.markInk !== ink) return;
        this.loading.delete(mark);
        this.marks.set(mark, image);
        this.redraw();
      };
      image.onerror = () => this.loading.delete(mark);
      image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(
        svg.replaceAll("currentColor", ink),
      )}`;
    }
  }

  resize(cssWidth: number, dpr: number) {
    if (cssWidth <= 0 || (cssWidth === this.cssWidth && dpr === this.dpr))
      return;
    this.cssWidth = cssWidth;
    this.dpr = dpr;
    this.layout(false);
  }

  setScene(id: CrewSceneId) {
    if (id === this.sceneId && this.scene) return;
    this.sceneId = id;
    this.layout(true);
  }

  setStill(still: boolean) {
    if (still === this.still) return;
    this.still = still;
    if (still) {
      this.stop();
      this.settle();
    } else this.wake();
  }

  setRunning(running: boolean) {
    if (running === this.running) return;
    this.running = running;
    if (running) {
      this.redraw();
      this.wake();
    } else this.stop();
  }

  setReading(reading: CrewReading) {
    // Without motion, a read that changes nothing on screen leaves the
    // picture alone; rates show rounded.
    const seen = JSON.stringify(
      reading.members.map((member) => ({
        ...member,
        rate: Math.round(member.rate),
      })),
    );
    const same = seen === this.seen;
    this.seen = seen;
    this.reading = reading;
    if (!this.scene) return;
    syncCrew(this.crew, reading, this.scene.layout);
    if (!this.still) this.wake();
    else if (!same) this.settle();
  }

  celebrate(text: string) {
    celebrate(this.crew, text);
    if (!this.still) return this.wake();
    // Without motion the banner shows on a still picture for a while.
    this.crew.confetti = [];
    for (const member of this.crew.members) member.jump = 0;
    this.draw(0);
    window.clearTimeout(this.bannerTimer);
    this.bannerTimer = window.setTimeout(() => {
      this.crew.banner = 0;
      this.draw(0);
    }, STILL_BANNER_MS);
  }

  /** Lays the scene out for the current width, or anew for another scene. */
  private layout(anew: boolean) {
    if (!this.canvas || this.cssWidth <= 0) return;
    const fit = crewFit(this.cssWidth, this.dpr, CREW_HEIGHT);
    const previous = this.fit;
    this.fit = fit;
    this.canvas.width = fit.width;
    this.canvas.height = fit.height;
    this.canvas.style.height = `${fit.cssHeight}px`;
    if (this.g) this.g.imageSmoothingEnabled = false;
    this.scene = createScene(this.sceneId, fit.W);
    this.grown = this.scene.grow(crewGrowth(this.tokens));
    this.burst = false;
    if (anew || !previous) {
      // Another scene: everyone is already at work in it.
      this.crew = createCrew();
      this.crew.width = fit.W;
      this.crew.height = this.scene.H;
      syncCrew(this.crew, this.reading, this.scene.layout);
      settleCrew(this.crew, this.scene.layout);
    } else {
      rescaleCrew(this.crew, fit.W / previous.W);
    }
    this.invalidate();
    if (this.still) this.settle();
    else {
      this.draw(0);
      this.wake();
    }
  }

  /** Text cached before its font arrived used a fallback; draw it again. */
  private fontsLoaded = () => {
    this.pen = null;
    this.redraw();
  };

  private invalidate() {
    this.back = null;
    this.front = null;
    this.pen = null;
  }

  private settle() {
    const scene = this.scene;
    if (!scene) return;
    settleCrew(this.crew, scene.layout);
    for (const member of this.crew.members)
      stepOrb(member, this.crew.time, 0, scene.stage.fx, this.crew.rand, true);
    this.draw(0);
  }

  private redraw() {
    if (!this.frame) this.draw(0);
  }

  private moving(): boolean {
    const crew = this.crew;
    return (
      crew.members.length > 0 ||
      crew.pops.length > 0 ||
      crew.confetti.length > 0 ||
      crew.banner > 0 ||
      !this.scene?.quiet()
    );
  }

  private wake() {
    if (this.frame || !this.running || this.still || !this.scene) return;
    if (!this.moving()) return;
    this.settled = 0;
    this.last = performance.now();
    this.frame = requestAnimationFrame(this.tick);
  }

  private stop() {
    if (this.frame) cancelAnimationFrame(this.frame);
    this.frame = 0;
  }

  private tick = (now: number) => {
    this.frame = 0;
    const scene = this.scene;
    if (!scene || !this.running || this.still) return;
    const elapsed = now - this.last;
    // At most 30 frames a second, whatever the display's rate.
    if (elapsed < FRAME_MS - 2) {
      this.frame = requestAnimationFrame(this.tick);
      return;
    }
    this.last = now;
    const dt = Math.min(0.1, elapsed / 1000);
    for (let left = dt; left > 1e-6; left -= STEP_S) {
      const step = Math.min(STEP_S, left);
      stepCrew(this.crew, scene.layout, step);
      for (const member of this.crew.members)
        stepOrb(member, this.crew.time, step, scene.stage.fx, this.crew.rand);
      scene.step(this.crew, step);
    }
    this.draw(dt);
    if (this.moving()) this.settled = 0;
    else this.settled += dt;
    // An empty, settled scene stays as it was drawn last.
    if (this.settled < SETTLE_S) this.frame = requestAnimationFrame(this.tick);
  };

  private layer(paint: (pen: Pen) => void): HTMLCanvasElement | null {
    const fit = this.fit;
    if (!fit || !this.palette) return null;
    const canvas = document.createElement("canvas");
    canvas.width = fit.width;
    canvas.height = fit.height;
    const g = canvas.getContext("2d");
    if (!g) return null;
    paint(this.penFor(g));
    return canvas;
  }

  private penFor(g: CanvasRenderingContext2D): Pen {
    const fit = this.fit!;
    return new Pen(g, {
      m: fit.m,
      dpr: this.dpr,
      W: fit.W,
      H: this.scene!.H,
      font: this.font,
      palette: this.palette!,
      host: g.canvas === this.canvas ? this.canvas.parentElement : null,
    });
  }

  private draw(dt: number) {
    const { g, scene, fit, palette } = this;
    if (!g || !scene || !fit || !palette) return;
    if (!this.back) {
      this.back = this.layer((pen) => scene.back(pen));
      this.front = scene.front ? this.layer((pen) => scene.front!(pen)) : null;
      // Whatever the scene just grew gets a burst of sparkles.
      if (this.burst) {
        const fx = scene.stage.fx;
        const rand = this.crew.rand;
        for (const [x, y] of scene.fresh) {
          spawn(fx, rand, x, y, 14, "yellow", 120, 70, 0.9, 4);
          spawn(fx, rand, x, y, 6, "paper", 90, 50, 0.7, 4);
        }
      }
      this.burst = false;
    }
    this.pen ??= this.penFor(g);
    const pen = this.pen;
    pen.T = this.crew.time;
    pen.dt = dt;
    g.globalAlpha = 1;
    if (this.back) g.drawImage(this.back, 0, 0);
    scene.movers?.(pen);
    if (this.front) g.drawImage(this.front, 0, 0);
    scene.props(pen, this.crew);
    drawCrew(pen, this.crew, scene.stage, this.words, this.marks);
  }
}

/** Reads the shared activity poll and hands each read to the engine. */
function CrewFeed({
  coreSessionKey,
  enabled,
  engine,
  onRead,
}: {
  coreSessionKey: string | null;
  enabled: boolean;
  engine: CrewEngine;
  onRead: (count: number, rate: number, marks: string) => void;
}) {
  const activity = useRequestActivityPoll(coreSessionKey, enabled);
  useEffect(() => {
    if (!activity) return;
    const reading = readCrew(activity);
    engine.setReading(reading);
    const marks = new Set(
      reading.members.flatMap((member) =>
        member.model ? [markOf(member.model)] : [],
      ),
    );
    onRead(
      reading.members.length,
      Math.round(reading.rate),
      [...marks].sort().join(" "),
    );
  }, [activity, engine, onRead]);
  return null;
}

/**
 * 「正在进行」: one pixel character per conversation in progress, acting out
 * what it is doing at a station of the chosen scene.
 */
export const LiveCrew = memo(function LiveCrew({
  coreSessionKey,
  isReady,
  todayTokens,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  /** Today's tokens as the overview last read them; null when unknown. */
  todayTokens: number | null;
}) {
  const t = useT();
  const headingId = useId();
  const theme = useResolvedTheme();
  const dpr = useDevicePixelRatio();
  const still = useMediaQuery("(prefers-reduced-motion: reduce)");
  const [sceneId, setSceneId] = useState(readSceneChoice);
  const [engine] = useState(() => new CrewEngine(sceneId));
  const [header, setHeader] = useState({ count: 0, rate: 0 });
  const [marks, setMarks] = useState("");
  const sectionRef = useRef<HTMLElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const marksRef = useRef<HTMLDivElement>(null);
  const onScreen = useOnScreen(sectionRef);
  const pageVisible = usePageVisible();
  const running = onScreen && pageVisible;

  const onRead = useCallback((count: number, rate: number, marks: string) => {
    setHeader((current) =>
      current.count === count && current.rate === rate
        ? current
        : { count, rate },
    );
    setMarks(marks);
  }, []);

  useLayoutEffect(() => {
    const canvas = canvasRef.current;
    const box = boxRef.current;
    if (!canvas || !box) return;
    engine.attach(canvas);
    const measure = () =>
      engine.resize(
        box.getBoundingClientRect().width,
        window.devicePixelRatio || 1,
      );
    measure();
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(measure);
    observer?.observe(box);
    return () => {
      observer?.disconnect();
      engine.destroy();
    };
  }, [engine]);

  useLayoutEffect(() => engine.setScene(sceneId), [engine, sceneId]);
  useLayoutEffect(() => {
    const box = boxRef.current;
    if (box) engine.resize(box.getBoundingClientRect().width, dpr);
  }, [dpr, engine]);
  useEffect(() => engine.repaint(), [engine, theme]);
  useEffect(() => engine.setStill(still), [engine, still]);
  useEffect(() => engine.setRunning(running), [engine, running]);
  useEffect(() => {
    engine.setWords({
      failover: t("overview.liveCrew.failover"),
      name: clientTypeName,
    });
  }, [engine, t]);

  // Only a stage passed while watching counts; the first read sets the mark.
  const lastTokens = useRef<number | null>(null);
  useEffect(() => {
    const passed = stagePassed(lastTokens.current, todayTokens);
    lastTokens.current = todayTokens;
    engine.setTokens(todayTokens);
    if (passed !== null) {
      engine.celebrate(
        t("overview.liveCrew.milestone", {
          amount: formatCompactNumber(CREW_STAGES[passed]),
        }),
      );
    }
  }, [engine, t, todayTokens]);

  // Each mark is drawn once by its icon component and read back as SVG.
  useLayoutEffect(() => {
    const box = marksRef.current;
    if (!box) return;
    const serializer = new XMLSerializer();
    const svgs = new Map<string, string>();
    for (const span of box.querySelectorAll<HTMLElement>("[data-mark]")) {
      const svg = span.querySelector("svg");
      if (svg && span.dataset.mark)
        svgs.set(span.dataset.mark, serializer.serializeToString(svg));
    }
    engine.setMarks(svgs);
  }, [engine, marks]);

  const choose = useCallback((next: CrewSceneId) => {
    setSceneId(next);
    saveSceneChoice(next);
  }, []);

  return (
    <section
      aria-labelledby={headingId}
      className="min-w-0 overflow-hidden rounded-md"
      data-slot="live-crew"
      ref={sectionRef}
      style={{
        background: "var(--crew-bg)",
        outline: "1px solid var(--crew-outline)",
        outlineOffset: -1,
      }}
    >
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-1.5">
        <div className="flex min-w-0 items-center gap-2.5 text-xs text-muted-foreground">
          <StatusDot
            className="size-2"
            pulse={header.count > 0}
            tone={header.count > 0 ? "positive" : "neutral"}
          />
          <h2 className="text-sm font-semibold text-foreground" id={headingId}>
            {t("overview.liveCrew.title")}
          </h2>
          <span className="tabular-nums" data-slot="live-crew-count">
            {t("overview.liveCrew.conversations", { count: header.count })}
          </span>
          <span className="tabular-nums" data-slot="live-crew-rate">
            {`${header.rate} tok/s`}
          </span>
        </div>
        <SegmentedControl
          label={t("overview.liveCrew.sceneLabel")}
          onValueChange={choose}
          options={CREW_SCENES.map((id) => ({
            value: id,
            label: t(`overview.liveCrew.scenes.${id}`),
          }))}
          value={sceneId}
          variant="line"
        />
      </div>
      <div ref={boxRef}>
        <canvas
          aria-hidden="true"
          className="block w-full [image-rendering:pixelated]"
          data-slot="live-crew-canvas"
          ref={canvasRef}
        />
      </div>
      <div hidden ref={marksRef}>
        {marks
          ? marks.split(" ").map((mark) => {
              const Mark =
                mark === BRAIN_MARK ? Brain : vendorMarks[mark as Vendor];
              return (
                <span data-mark={mark} key={mark}>
                  <Mark size={24} />
                </span>
              );
            })
          : null}
      </div>
      <CrewFeed
        coreSessionKey={coreSessionKey}
        enabled={isReady && running}
        engine={engine}
        onRead={onRead}
      />
    </section>
  );
});
