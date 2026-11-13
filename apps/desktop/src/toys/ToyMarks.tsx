import { useLayoutEffect, useRef } from "react";

import { Brain } from "@/components/icons";
import { vendorMarks, type Vendor } from "@/components/vendor-marks";

import { BRAIN_MARK } from "../live-crew-draw";
import type { MarkSvgs } from "./toy-kit";

const MARKS = [...(Object.keys(vendorMarks) as Vendor[]), BRAIN_MARK];

/** Draws every vendor mark once, hidden, and hands them over as SVG text. */
export function MarkSource({ onReady }: { onReady: (svgs: MarkSvgs) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const done = useRef(false);
  useLayoutEffect(() => {
    const box = ref.current;
    if (!box || done.current) return;
    done.current = true;
    const serializer = new XMLSerializer();
    const svgs = new Map<string, string>();
    for (const span of box.querySelectorAll<HTMLElement>("[data-mark]")) {
      const svg = span.querySelector("svg");
      if (svg && span.dataset.mark)
        svgs.set(span.dataset.mark, serializer.serializeToString(svg));
    }
    onReady(svgs);
  }, [onReady]);
  return (
    <div hidden ref={ref}>
      {MARKS.map((mark) => {
        const Mark = mark === BRAIN_MARK ? Brain : vendorMarks[mark as Vendor];
        return (
          <span data-mark={mark} key={mark}>
            <Mark size={24} />
          </span>
        );
      })}
    </div>
  );
}
