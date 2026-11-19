// The big pet's speech bubble: a title, a line, an action, and for the
// day's summary its lines and hours. The pet's loop fills it in place, so
// the page keeps its frame rate.
import { i18n } from "../i18n";
import type { Words } from "./pet-words";

/** The parts `fillBubble` fills, in order. */
export function BubbleParts() {
  return (
    <>
      <b />
      <span />
      <ul className="toy-pet-day" hidden />
      <div aria-hidden="true" className="toy-pet-hours" hidden />
      <a />
    </>
  );
}

/** Puts `words` into a bubble made of `BubbleParts`. */
export function fillBubble(el: HTMLElement, words: Words) {
  const [title, text, lines, hours, action] =
    el.children as unknown as HTMLElement[];
  title.textContent = words.title;
  text.textContent = words.text;
  text.hidden = !words.text;
  const day = words.day;
  lines.replaceChildren(
    ...(day?.lines ?? []).map((line) => {
      const li = document.createElement("li");
      li.textContent = line;
      return li;
    }),
  );
  lines.hidden = !day?.lines.length;
  fillHours(hours, day?.hours ?? [], day?.hour ?? 0);
  action.textContent = words.action ?? "";
  action.hidden = !words.action;
  el.dataset.day = String(Boolean(day));
}

/** 24 bars, one per local hour: the busiest stands out, a dot marks now. */
function fillHours(el: HTMLElement, tokens: number[], now: number) {
  const max = Math.max(0, ...tokens);
  el.hidden = tokens.length !== 24 || max <= 0;
  if (el.hidden) {
    el.replaceChildren();
    return;
  }
  const peak = tokens.indexOf(max);
  el.title = i18n.t("tray.hourlyChart");
  el.replaceChildren(
    ...tokens.map((value, hour) => {
      const bar = document.createElement("i");
      bar.style.height = `${value <= 0 ? 2 : Math.max(3, Math.round((value / max) * 22))}px`;
      bar.dataset.at = hour === peak ? "peak" : value > 0 ? "used" : "none";
      if (hour === now) bar.dataset.now = "";
      return bar;
    }),
  );
}
