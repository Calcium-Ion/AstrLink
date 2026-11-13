import { useCallback, useEffect, useRef } from "react";

import type { ClientType, GatewayRewrite } from "./request-record-model";

/** A finished request the sky flies as a comet. */
export interface SkyCometRequest {
  /** Stable per request; also seeds the comet's path. */
  id: string;
  client_type: ClientType | null;
  service_id: string | null;
  failed_service_id: string | null;
  failed: boolean;
  rewrites: GatewayRewrite[];
}

export interface SkyComet extends SkyCometRequest {
  /** `performance.now()` when it left the client. */
  start: number;
  /** Requests on the same route merged into this comet while it waited. */
  count: number;
}

/** Milliseconds from the client to the sun, and from the sun to a planet. */
export const COMET_IN_MS = 800;
export const COMET_OUT_MS = 950;
/** The gold hop from a failed planet to the one that took the request. */
export const COMET_HOP_MS = 450;
/** The landed trail fades out over this long. */
export const COMET_FADE_MS = 420;

const MAX_FLYING = 6;
const MAX_WAITING = 12;
const LAUNCH_GAP_MS = 160;

export function cometDuration(comet: SkyCometRequest): number {
  return (
    COMET_IN_MS +
    (comet.service_id === null ? 0 : COMET_OUT_MS) +
    (comet.failed_service_id !== null &&
    comet.failed_service_id !== comet.service_id
      ? COMET_HOP_MS
      : 0) +
    COMET_FADE_MS
  );
}

function routeKey(request: SkyCometRequest): string {
  return [
    request.client_type ?? "",
    request.service_id ?? "",
    request.failed_service_id ?? "",
    request.failed ? 1 : 0,
    ...request.rewrites,
  ].join("|");
}

/** Draws one frame; an empty list clears what the last frame drew. */
export type SkyCometFrame = (now: number, comets: readonly SkyComet[]) => void;

/**
 * Queues comets so a burst does not strobe: at most six fly at once, launches
 * keep a short gap, and a request whose route already waits joins that comet.
 * Frames go to `draw` without a React render, and the animation frame loop
 * runs only while a comet is queued or flying. Nothing flies while disabled
 * (reduced motion, hidden window, the sky scrolled away).
 */
export function useSkyComets(enabled: boolean, draw: SkyCometFrame) {
  const waiting = useRef<SkyComet[]>([]);
  const flying = useRef<SkyComet[]>([]);
  const lastLaunch = useRef(-Infinity);
  const frame = useRef<number | null>(null);
  const drawRef = useRef(draw);
  drawRef.current = draw;

  const tick = useCallback(() => {
    frame.current = null;
    const now = performance.now();
    flying.current = flying.current.filter(
      (comet) => now - comet.start < cometDuration(comet),
    );
    while (
      waiting.current.length > 0 &&
      flying.current.length < MAX_FLYING &&
      now - lastLaunch.current >= LAUNCH_GAP_MS
    ) {
      const next = waiting.current.shift()!;
      flying.current.push({ ...next, start: now });
      lastLaunch.current = now;
    }
    drawRef.current(now, flying.current);
    if (flying.current.length > 0 || waiting.current.length > 0)
      frame.current = requestAnimationFrame(tick);
  }, []);

  const clear = useCallback(() => {
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
    waiting.current = [];
    flying.current = [];
    drawRef.current(performance.now(), []);
  }, []);

  useEffect(() => {
    if (!enabled) {
      clear();
      return;
    }
    // A hidden window drops what was queued instead of replaying it later.
    const onVisibility = () => {
      if (document.hidden) clear();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      clear();
    };
  }, [clear, enabled]);

  return useCallback(
    (requests: readonly SkyCometRequest[]) => {
      if (!enabled || document.hidden || requests.length === 0) return;
      for (const request of requests) {
        const key = routeKey(request);
        const same = waiting.current.find((comet) => routeKey(comet) === key);
        if (same) {
          same.count += 1;
          continue;
        }
        waiting.current.push({ ...request, start: 0, count: 1 });
      }
      // Under heavy traffic the oldest waiting comets give way.
      if (waiting.current.length > MAX_WAITING)
        waiting.current.splice(0, waiting.current.length - MAX_WAITING);
      if (frame.current === null) frame.current = requestAnimationFrame(tick);
    },
    [enabled, tick],
  );
}
