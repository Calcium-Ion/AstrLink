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
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusDot } from "@/components/StatusDot";
import { useDevicePixelRatio } from "@/lib/use-device-pixel-ratio";
import { useMediaQuery } from "@/lib/use-media-query";
import { useOnScreen, usePageVisible } from "@/lib/use-on-screen";

import { formatCompactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import {
  clamp,
  DEMO_WIDTH,
  drawCrew,
  Pen,
  readCrewPalette,
  U,
  type CrewPalette,
  type CrewWords,
} from "./live-crew-draw";
import {
  celebrate,
  createCrew,
  CREW_ACTS,
  milestonePassed,
  readCrew,
  rescaleCrew,
  settleCrew,
  stepCrew,
  syncCrew,
  type Crew,
  type CrewReading,
  type CrewStation,
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
  private labels: Record<CrewSceneId, Record<CrewStation, string>> | null =
    null;
  private running = false;
  private still = false;
  private frame = 0;
  private last = 0;
  private settled = 0;
  private bannerTimer = 0;

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

  setWords(
    words: CrewWords,
    labels: Record<CrewSceneId, Record<CrewStation, string>>,
  ) {
    this.words = words;
    this.labels = labels;
    this.redraw();
  }

  /** The theme changed: read the colors again. */
  repaint() {
    if (!this.canvas) return;
    this.palette = readCrewPalette(this.canvas);
    this.invalidate();
    this.redraw();
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
    if (!this.scene) return;
    settleCrew(this.crew, this.scene.layout);
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
    const { g, scene, fit, palette, labels } = this;
    if (!g || !scene || !fit || !palette || !labels) return;
    if (!this.back) {
      this.back = this.layer((pen) => scene.back(pen));
      this.front = scene.front ? this.layer((pen) => scene.front!(pen)) : null;
    }
    this.pen ??= this.penFor(g);
    const pen = this.pen;
    pen.T = this.crew.time;
    pen.dt = dt;
    g.globalAlpha = 1;
    if (this.back) g.drawImage(this.back, 0, 0);
    scene.movers?.(pen);
    if (this.front) g.drawImage(this.front, 0, 0);
    scene.props(pen, this.crew, labels[scene.id]);
    drawCrew(pen, this.crew, scene.stage, this.words);
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
  onRead: (count: number, rate: number) => void;
}) {
  const activity = useRequestActivityPoll(coreSessionKey, enabled);
  useEffect(() => {
    if (!activity) return;
    const reading = readCrew(activity);
    engine.setReading(reading);
    onRead(reading.members.length, Math.round(reading.rate));
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
  const sectionRef = useRef<HTMLElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const onScreen = useOnScreen(sectionRef);
  const pageVisible = usePageVisible();
  const running = onScreen && pageVisible;

  const onRead = useCallback((count: number, rate: number) => {
    setHeader((current) =>
      current.count === count && current.rate === rate
        ? current
        : { count, rate },
    );
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
    const stations = (scene: CrewSceneId) =>
      Object.fromEntries(
        CREW_ACTS.filter((act) => act !== "wait").map((act) => [
          act,
          t(`overview.liveCrew.stations.${scene}.${act}`),
        ]),
      ) as Record<CrewStation, string>;
    engine.setWords(
      { failover: t("overview.liveCrew.failover"), name: clientTypeName },
      Object.fromEntries(CREW_SCENES.map((id) => [id, stations(id)])) as Record<
        CrewSceneId,
        Record<CrewStation, string>
      >,
    );
  }, [engine, t]);

  // Only a milestone passed while watching counts; the first read sets the mark.
  const lastTokens = useRef<number | null>(null);
  useEffect(() => {
    const passed = milestonePassed(lastTokens.current, todayTokens);
    lastTokens.current = todayTokens;
    if (passed !== null) {
      engine.celebrate(
        t("overview.liveCrew.milestone", {
          amount: formatCompactNumber(passed),
        }),
      );
    }
  }, [engine, t, todayTokens]);

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
      <CrewFeed
        coreSessionKey={coreSessionKey}
        enabled={isReady && running}
        engine={engine}
        onRead={onRead}
      />
    </section>
  );
});
