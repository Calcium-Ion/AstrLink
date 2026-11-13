// The picture beside the big pet's settings: the pet herself, drawn by her
// own engine and played from the sample conversations, with the settings as
// they are now.
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";

import { sceneFrameClass } from "@/components/SettingSceneBody";
import { cn } from "@/lib/utils";

import type { ClientType } from "../request-record-model";
import { ORB_SCALE, PetBrain, type Bubble } from "./pet-brain";
import { fitBox, paint, type PaintCache } from "./pet-draw";
import { petWords } from "./pet-words";
import { cfg } from "./toy-config";
import {
  Board,
  DPR,
  runLoop,
  stepPace,
  syncPace,
  type MarkSvgs,
} from "./toy-kit";
import { MarkSource } from "./ToyMarks";
import { Sample } from "./toy-sample";
import "./toys.css";

type Frame = (dt: number) => void;
const frames = new Set<Frame>();
let stopPlayer: (() => void) | null = null;

/** Plays the sample conversations once for every scene on the page. */
function play(frame: Frame): () => void {
  frames.add(frame);
  if (!stopPlayer) {
    const sample = new Sample();
    stopPlayer = runLoop((dt) => {
      sample.step(dt);
      syncPace(sample.reading());
      stepPace(dt);
      for (const f of frames) f(dt);
    });
  }
  return () => {
    frames.delete(frame);
    if (frames.size || !stopPlayer) return;
    stopPlayer();
    stopPlayer = null;
  };
}

function useMarks() {
  const [svgs, setSvgs] = useState<MarkSvgs | null>(null);
  const onMarks = useCallback((next: MarkSvgs) => setSvgs(next), []);
  return [svgs, onMarks] as const;
}

function useWidth(ref: RefObject<HTMLElement | null>) {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const box = ref.current;
    if (!box) return;
    const observer = new ResizeObserver(() =>
      setWidth(Math.round(box.clientWidth)),
    );
    observer.observe(box);
    setWidth(Math.round(box.clientWidth));
    return () => observer.disconnect();
  }, [ref]);
  return width;
}

const PET_H = 240;
/** Every girl the big pet dresses as, and how long each stays in the picture. */
const GIRLS: readonly ClientType[] = [
  "claude_code",
  "codex",
  "deepseek_harness",
];
const TURN_SECONDS = 10;

/**
 * The sample has a conversation for each client, so the busiest is a tie and
 * the real pet would stay one girl; left to choose, here they take turns.
 */
class TakingTurns extends PetBrain {
  override chooseClient() {
    if (cfg.prefs.pet.character !== "auto") super.chooseClient();
  }
}

/** The big pet floating at her size, speaking up as her settings say. */
export function PetScene() {
  const frame = useRef<HTMLDivElement>(null);
  const petRef = useRef<HTMLCanvasElement>(null);
  const orbRef = useRef<HTMLCanvasElement>(null);
  const bubbleRef = useRef<HTMLDivElement>(null);
  const [svgs, onMarks] = useMarks();
  const width = useWidth(frame);

  useEffect(() => {
    const petCanvas = petRef.current;
    const orbCanvas = orbRef.current;
    const bubble = bubbleRef.current;
    if (!petCanvas || !orbCanvas || !bubble || !svgs || width < 160) return;
    const W = width;
    const H = PET_H;
    const pet = new Board(petCanvas, W, H);
    const orbs = new Board(
      orbCanvas,
      W / ORB_SCALE,
      H / ORB_SCALE,
      DPR() * ORB_SCALE,
    );
    fitBox(pet);
    fitBox(orbs);
    const brain = new TakingTurns({
      W,
      H,
      tokens: () => null,
      fx: { pet: pet.fx, orbs: orbs.fx },
    });
    brain.setFrames({
      wins: [],
      dock: null,
      floor: H - 2,
      top: 0,
      full: false,
    });
    brain.x = Math.round(W * 0.7);
    brain.foot = H - 14;
    const cache: PaintCache = { sig: "", painted: null };
    let box = "";
    let turn = 0;
    let girl = Math.max(0, GIRLS.indexOf(brain.client));
    let shown: Bubble | null = null;
    let stop = () => {};
    let gone = false;
    void Promise.all([pet.init(svgs), orbs.init(svgs)]).then(() => {
      if (gone) return;
      stop = play((dt) => {
        turn += dt;
        if (cfg.prefs.pet.character !== "auto") turn = 0;
        else if (turn > TURN_SECONDS) {
          turn = 0;
          girl = (girl + 1) % GIRLS.length;
          brain.setClient(GIRLS[girl]);
        }
        brain.step(dt);
        const drawn = paint(brain, pet, orbs, dt, cache);
        const key = `${drawn.box.x},${drawn.box.y}`;
        if (key !== box) {
          box = key;
          const at = `translate(${drawn.box.x}px, ${drawn.box.y}px)`;
          pet.canvas.style.transform = at;
          orbs.canvas.style.transform = at;
        }
        const head = brain.bubbles[0] ?? null;
        bubble.hidden = !head;
        if (!head) {
          shown = null;
          return;
        }
        if (head !== shown) {
          shown = head;
          const words = petWords(head.say);
          const [title, text, action] =
            bubble.children as unknown as HTMLElement[];
          title.textContent = words.title;
          text.textContent = words.text;
          text.hidden = !words.text;
          action.textContent = words.action ?? "";
          action.hidden = !words.action;
        }
        // Beside her head, so even the large size fits the picture.
        bubble.style.left = `${brain.x - brain.half() - 4}px`;
        bubble.style.top = `${drawn.top + (drawn.bottom - drawn.top) * 0.3}px`;
      });
    });
    return () => {
      gone = true;
      stop();
      brain.cancel();
    };
  }, [svgs, width]);

  return (
    <div
      aria-hidden="true"
      className={cn(sceneFrameClass, "relative overflow-hidden")}
      ref={frame}
      style={{ height: PET_H }}
    >
      <MarkSource onReady={onMarks} />
      <canvas className="absolute top-0 left-0" ref={petRef} />
      <canvas className="absolute top-0 left-0" ref={orbRef} />
      <div className="toy-pet-bubble" data-side="left" hidden ref={bubbleRef}>
        <b />
        <span />
        <a />
      </div>
    </div>
  );
}
