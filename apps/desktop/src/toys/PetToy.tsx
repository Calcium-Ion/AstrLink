// The big desktop pet: one girl on a transparent window over one screen,
// full-screen apps included. She floats wherever you drag her and stays there,
// on another screen too: her window follows her across. Every conversation in
// progress is an orb beside her. The host lets the mouse through everywhere
// but her, her bubble and her menu, tells the page where the mouse is, and
// how long you have been away.
import { isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { trayAction } from "../bridge";
import { useT } from "../i18n";
import {
  ORB_SCALE,
  parseMemo,
  PetBrain,
  type Bubble,
  type HitRect,
  type PetMemo,
} from "./pet-brain";
import { fitBox, paint, type PaintCache } from "./pet-draw";
import { petWords } from "./pet-words";
import { cfg } from "./toy-config";
import {
  feed,
  POINTER_EVENT,
  reportHits,
  startFeed,
  type ScreenMove,
} from "./toy-feed";
import { readFrames } from "./toy-frames";
import { Board, clamp, DPR, stepPace, type MarkSvgs } from "./toy-kit";
import { MarkSource } from "./ToyMarks";
import { labelOf, nameOf } from "./toy-words";
import "./toys.css";

const MEMO_KEY = "astrlink.toys.pet";
/** Seconds the mouse rests on an orb before its card shows. */
const CARD_DELAY = 0.8;

function loadMemo(): PetMemo | null {
  try {
    return parseMemo(window.localStorage.getItem(MEMO_KEY));
  } catch {
    return null;
  }
}

function saveMemo(memo: PetMemo) {
  try {
    window.localStorage.setItem(MEMO_KEY, JSON.stringify(memo));
  } catch {
    // A full or blocked store only loses today's counts.
  }
}

interface Menu {
  x: number;
  y: number;
  quiet: boolean;
}

interface Pointer {
  x: number;
  y: number;
  down?: boolean;
}

const inside = (r: DOMRect | null, x: number, y: number) =>
  Boolean(r && x >= r.left && x < r.right && y >= r.top && y < r.bottom);

const rectOf = (r: DOMRect, click: boolean): HitRect => ({
  x: Math.floor(r.left),
  y: Math.floor(r.top),
  w: Math.ceil(r.width),
  h: Math.ceil(r.height),
  click,
});

/** Drives a pet on the page: input, the loop, the bubble and card, and what takes the mouse. */
class PetView {
  sent = "";
  sentAt = 0;
  held = false;
  shown: Bubble | null = null;
  bubbleRect: DOMRect | null = null;
  menuRect: DOMRect | null = null;
  menuOpen = false;
  hover: { id: string; since: number } | null = null;
  box = "";
  memo = "";
  savedAt = 0;
  drawn: PaintCache = { sig: "", painted: null };
  /** The device pixel ratio the boards were made for. */
  scale = DPR();

  constructor(
    public brain: PetBrain,
    public pet: Board,
    public orbs: Board,
    public ui: {
      root: HTMLElement;
      bubble: HTMLElement;
      card: HTMLElement;
      /** The right-click menu, while it is open. */
      menu: () => HTMLElement | null;
    },
    public openMenu: (menu: Menu | null) => void,
  ) {}

  /** The window took a new size or scale: the boards follow, she keeps her place. */
  resize(w: number, h: number) {
    const b = this.brain;
    if (w < 300 || h < 300) return;
    if (w === b.W && h === b.H && DPR() === this.scale) return;
    this.scale = DPR();
    b.resize(w, h);
    this.pet.resize(w, h);
    this.orbs.resize(w / ORB_SCALE, h / ORB_SCALE, DPR() * ORB_SCALE);
    fitBox(this.pet);
    fitBox(this.orbs);
    this.drawn = { sig: "", painted: null };
    this.box = "";
  }

  /** You carried her onto another screen and her window came along. */
  carried(move: ScreenMove) {
    this.brain.carried(move.dx, move.dy);
    this.hover = null;
    this.resize(move.w, move.h);
    this.drawn = { sig: "", painted: null };
    this.box = "";
  }

  /** Where the host says the mouse is; a click away from the menu closes it. */
  point(p: Pointer | null) {
    if (p?.down && this.menuOpen && !inside(this.menuRect, p.x, p.y))
      this.openMenu(null);
    this.brain.point(p && { x: p.x, y: p.y });
  }

  bind(): () => void {
    const { root, bubble } = this.ui;
    const b = this.brain;
    const down = (e: PointerEvent) => {
      const target = e.target as HTMLElement;
      if (target.closest("[data-slot=dropdown-menu-content]")) return;
      if (this.menuOpen) this.openMenu(null);
      if (e.button !== 0 || bubble.contains(target)) return;
      if (!b.pressAt(e.clientX, e.clientY)) return;
      root.setPointerCapture(e.pointerId);
      e.preventDefault();
      this.report();
    };
    const move = (e: PointerEvent) => {
      if (b.press || b.grab) b.dragTo(e.clientX, e.clientY);
    };
    const up = (e: PointerEvent) => {
      if (!b.press && !b.grab) return;
      b.releaseAt(e.clientX, e.clientY);
      this.report();
    };
    const cancel = () => {
      b.cancel();
      this.report();
    };
    const twice = (e: MouseEvent) => {
      if (b.hitBody(e.clientX, e.clientY)) b.greet();
    };
    const menu = (e: MouseEvent) => {
      e.preventDefault();
      if (!b.hitBody(e.clientX, e.clientY)) return;
      this.openMenu({ x: e.clientX, y: e.clientY, quiet: b.memo.quiet });
    };
    const open = () => {
      const head = b.bubbles[0];
      if (!head || !petWords(head.say).action) return;
      b.bubbles.shift();
      b.bubbleT = 0;
      void trayAction({ kind: "navigate", page: "records" }).catch(
        () => undefined,
      );
    };
    root.addEventListener("pointerdown", down);
    root.addEventListener("pointermove", move);
    root.addEventListener("pointerup", up);
    root.addEventListener("pointercancel", cancel);
    root.addEventListener("dblclick", twice);
    root.addEventListener("contextmenu", menu);
    bubble.addEventListener("click", open);
    window.addEventListener("blur", cancel);
    return () => {
      root.removeEventListener("pointerdown", down);
      root.removeEventListener("pointermove", move);
      root.removeEventListener("pointerup", up);
      root.removeEventListener("pointercancel", cancel);
      root.removeEventListener("dblclick", twice);
      root.removeEventListener("contextmenu", menu);
      bubble.removeEventListener("click", open);
      window.removeEventListener("blur", cancel);
    };
  }

  frame(dt: number) {
    const b = this.brain;
    stepPace(dt);
    b.step(dt);
    const drawn = paint(b, this.pet, this.orbs, dt, this.drawn);
    const key = `${drawn.box.x},${drawn.box.y}`;
    if (key !== this.box) {
      this.box = key;
      const at = `translate(${drawn.box.x}px, ${drawn.box.y}px)`;
      this.pet.canvas.style.transform = at;
      this.orbs.canvas.style.transform = at;
    }
    this.showBubble(drawn.top, drawn.bottom, drawn.chipY);
    this.showCard();
    this.cursor();
    this.report();
    this.save();
  }

  showBubble(top: number, bottom: number, chipY: number) {
    const el = this.ui.bubble;
    const b = this.brain;
    const head = b.bubbles[0] ?? null;
    if (!head) {
      el.hidden = true;
      this.shown = null;
      this.bubbleRect = null;
      b.bubbleHeld = false;
      return;
    }
    if (head !== this.shown) {
      this.shown = head;
      const words = petWords(head.say);
      const [title, text, action] = el.children as unknown as HTMLElement[];
      title.textContent = words.title;
      text.textContent = words.text;
      text.hidden = !words.text;
      action.textContent = words.action ?? "";
      action.hidden = !words.action;
      el.dataset.action = String(Boolean(words.action));
    }
    el.hidden = false;
    // Over her head; floating near the top of the screen, under her feet.
    const y = Math.min(top - 10, chipY - 18);
    const below = y < b.frames.top + 70;
    el.dataset.below = String(below);
    el.style.left = `${clamp(b.x, 150, b.W - 150)}px`;
    el.style.top = `${below ? bottom + 12 : y}px`;
    this.bubbleRect = el.getBoundingClientRect();
    const p = b.pointer;
    b.bubbleHeld = Boolean(p && inside(this.bubbleRect, p.x, p.y));
  }

  /** Who an orb is and what it is doing, once the mouse rests on it. */
  showCard() {
    const b = this.brain;
    const p = b.pointer;
    let id = "";
    if (p && !b.grab && cfg.prefs.orb)
      for (const o of b.orbs.values())
        if (Math.hypot(p.x - o.x, p.y - o.y) < 24) id = o.id;
    if (id !== this.hover?.id) this.hover = id ? { id, since: b.now } : null;
    const m = id ? b.members().find((o) => o.id === id) : undefined;
    const o = id ? b.orbs.get(id) : undefined;
    const show = Boolean(
      m && o && this.hover && b.now - this.hover.since >= CARD_DELAY,
    );
    const el = this.ui.card;
    el.hidden = !show;
    if (!show || !m || !o) return;
    const [name, label, model] = el.children as unknown as HTMLElement[];
    name.textContent = nameOf(m.client);
    label.textContent = labelOf(m);
    model.textContent = m.model;
    el.style.left = `${clamp(o.x, 110, b.W - 110)}px`;
    el.style.top = `${o.y - 26}px`;
  }

  cursor() {
    const b = this.brain;
    const p = b.pointer;
    this.ui.root.style.cursor = b.grab
      ? "grabbing"
      : p && cfg.prefs.pet.grab && b.hitBody(p.x, p.y)
        ? "grab"
        : "";
  }

  /** Tells the host which parts take the mouse: her, the bubble, the menu. */
  report() {
    const b = this.brain;
    // The menu places itself after it opens and grows in: read it each time.
    this.menuRect = this.menuOpen
      ? (this.ui.menu()?.getBoundingClientRect() ?? null)
      : null;
    const rects: HitRect[] = b.hits();
    if (this.bubbleRect && !this.ui.bubble.hidden)
      rects.push(
        rectOf(this.bubbleRect, this.ui.bubble.dataset.action === "true"),
      );
    if (this.menuOpen && this.menuRect) rects.push(rectOf(this.menuRect, true));
    // A press keeps the mouse for the click; only a pet in your hand takes
    // her window to another screen.
    const hold = Boolean(b.grab || b.press);
    const carry = Boolean(b.grab);
    const key = JSON.stringify([
      rects.map((r) => [r.x >> 2, r.y >> 2, r.w >> 2, r.h >> 2, r.click]),
      hold,
      carry,
    ]);
    const now = performance.now();
    if (key === this.sent) return;
    if (hold === this.held && now - this.sentAt < 80) return;
    reportHits(rects, hold, carry);
    this.sent = key;
    this.sentAt = now;
    this.held = hold;
  }

  /** Keeps today's counts every few seconds, so a restart keeps them. */
  save() {
    const b = this.brain;
    if (b.now - this.savedAt < 2) return;
    this.savedAt = b.now;
    const text = JSON.stringify(b.memo);
    if (text === this.memo) return;
    this.memo = text;
    saveMemo(b.memo);
  }
}

/** Runs `frame` every display frame while she moves, fewer while she sits still. */
function runPetLoop(view: PetView): () => void {
  let last = performance.now();
  let id = 0;
  let stopped = false;
  const tick = (now: number) => {
    if (stopped) return;
    id = requestAnimationFrame(tick);
    const b = view.brain;
    const fps = b.busy() ? 30 : b.sleeping ? 6 : 15;
    const elapsed = now - last;
    if (elapsed < 1000 / fps - 2) return;
    last = now;
    view.frame(Math.min(0.2, elapsed / 1000));
  };
  id = requestAnimationFrame(tick);
  return () => {
    stopped = true;
    cancelAnimationFrame(id);
  };
}

export function PetToy() {
  const t = useT();
  const rootRef = useRef<HTMLDivElement>(null);
  const petRef = useRef<HTMLCanvasElement>(null);
  const orbRef = useRef<HTMLCanvasElement>(null);
  const bubbleRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<PetView | null>(null);
  const [svgs, setSvgs] = useState<MarkSvgs | null>(null);
  const [menu, setMenu] = useState<Menu | null>(null);
  const onMarks = useCallback((next: MarkSvgs) => setSvgs(next), []);

  useEffect(() => {
    const root = rootRef.current;
    const petCanvas = petRef.current;
    const orbCanvas = orbRef.current;
    const bubble = bubbleRef.current;
    const card = cardRef.current;
    if (!root || !petCanvas || !orbCanvas || !bubble || !card || !svgs) return;
    const stops: Array<() => void> = [];
    let gone = false;
    const keep = (stop: () => void) => (gone ? stop() : stops.push(stop));
    let view: PetView | null = null;
    const start = (w: number, h: number) => {
      const pet = new Board(petCanvas, w, h);
      const S = ORB_SCALE;
      const orbs = new Board(orbCanvas, w / S, h / S, DPR() * S);
      fitBox(pet);
      fitBox(orbs);
      const brain = new PetBrain({
        W: w,
        H: h,
        tokens: () => feed.tokens,
        fx: { pet: pet.fx, orbs: orbs.fx },
        memo: loadMemo(),
      });
      const started = new PetView(
        brain,
        pet,
        orbs,
        { root, bubble, card, menu: () => menuRef.current },
        (m) => setMenu(m),
      );
      view = started;
      viewRef.current = started;
      stops.push(started.bind(), () => {
        brain.cancel();
        saveMemo(brain.memo);
      });
      void (async () => {
        await pet.init(svgs);
        await orbs.init(svgs);
        keep(
          await startFeed({
            milestone: (stage) => brain.milestone(stage),
            frames: (payload) => {
              const frames = readFrames(payload);
              if (frames) brain.setFrames(frames);
            },
            idle: (reading) => brain.idle(reading),
            screen: (move) => started.carried(move),
          }),
        );
        if (isTauri())
          keep(
            await listen<Pointer | null>(POINTER_EVENT, ({ payload }) =>
              started.point(payload),
            ),
          );
        if (gone) return;
        keep(runPetLoop(started));
      })();
    };
    // A new size (another screen, a new resolution) keeps her and her
    // counts; only the boards follow.
    const resize = () => {
      const w = window.innerWidth;
      const h = window.innerHeight;
      if (view) view.resize(w, h);
      else if (w >= 300 && h >= 300) start(w, h);
    };
    window.addEventListener("resize", resize);
    stops.push(() => window.removeEventListener("resize", resize));
    resize();
    return () => {
      gone = true;
      stops.forEach((stop) => stop());
      viewRef.current = null;
      reportHits([]);
    };
  }, [svgs]);

  // The open menu takes the mouse; the view reads where it is.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.menuOpen = Boolean(menu);
    if (!menu) {
      view.report();
      return;
    }
    const id = requestAnimationFrame(() => view.report());
    return () => cancelAnimationFrame(id);
  }, [menu]);

  const brain = () => viewRef.current?.brain;

  return (
    <div
      className="toy-pet relative h-full w-full select-none overflow-hidden"
      ref={rootRef}
    >
      <canvas className="absolute top-0 left-0" ref={petRef} />
      <canvas className="absolute top-0 left-0" ref={orbRef} />
      <div
        className="toy-pet-bubble"
        data-action="false"
        hidden
        ref={bubbleRef}
      >
        <b />
        <span />
        <a />
      </div>
      <div
        className="pointer-events-none absolute flex -translate-x-1/2 -translate-y-full flex-col gap-0.5 whitespace-nowrap rounded-[10px] bg-card px-3 py-2 text-xs shadow-[0_8px_24px_-8px_rgb(26_42_61/40%)]"
        hidden
        ref={cardRef}
      >
        <b className="text-foreground" />
        <span className="text-text-secondary" />
        <small className="text-[11px] text-muted-foreground" />
      </div>
      <DropdownMenu
        modal={false}
        onOpenChange={(open) => {
          if (!open) setMenu(null);
        }}
        open={Boolean(menu)}
      >
        <DropdownMenuTrigger asChild>
          <span
            aria-hidden="true"
            className="pointer-events-none absolute size-px"
            style={{ left: menu?.x ?? 0, top: menu?.y ?? 0 }}
          />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="start"
          className="min-w-52"
          ref={menuRef}
          sideOffset={2}
        >
          <DropdownMenuItem inset onSelect={() => brain()?.patHead()}>
            {t("desktopToys.pet.menu.pat")}
          </DropdownMenuItem>
          <DropdownMenuItem inset onSelect={() => brain()?.greet()}>
            {t("desktopToys.pet.menu.greet")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuCheckboxItem
            checked={menu?.quiet ?? false}
            onSelect={() => brain()?.toggleQuiet()}
          >
            {t("desktopToys.pet.menu.quiet")}
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            inset
            onSelect={() =>
              void trayAction({ kind: "open" }).catch(() => undefined)
            }
          >
            {t("desktopToys.pet.menu.open")}
          </DropdownMenuItem>
          <DropdownMenuItem
            inset
            onSelect={() =>
              void trayAction({ kind: "navigate", page: "desktopToys" }).catch(
                () => undefined,
              )
            }
          >
            {t("desktopToys.pet.menu.settings")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <MarkSource onReady={onMarks} />
    </div>
  );
}
