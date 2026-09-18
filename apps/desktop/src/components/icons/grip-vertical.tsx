// Adapted from https://lucide-animated.com/r/grip-vertical.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const CIRCLES = [
  { cx: 9, cy: 5 },
  { cx: 9, cy: 12 },
  { cx: 9, cy: 19 },
  { cx: 15, cy: 5 },
  { cx: 15, cy: 12 },
  { cx: 15, cy: 19 },
];

const ROWS = 3;

const VARIANTS: Variants = {
  normal: {
    opacity: 1,
    scale: 1,
    transition: { duration: 0.25, ease: "easeOut" },
  },
  animate: (data: { index: number }) => {
    const row = data.index % ROWS;
    const col = Math.floor(data.index / ROWS);
    const delay = row * 0.15 + col * (ROWS * 0.15 - 0.2);

    return {
      opacity: [1, 0.4, 1],
      scale: [1, 0.85, 1],
      transition: { delay, duration: 1, ease: "easeInOut" },
    };
  },
};

export const GripVertical = createAnimatedIcon(
  "grip-vertical",
  (controls) => (
    <>
      {CIRCLES.map((circle, index) => (
        <motion.circle
          animate={controls}
          custom={{ index }}
          cx={circle.cx}
          cy={circle.cy}
          initial="normal"
          key={`${circle.cx}-${circle.cy}`}
          r="1"
          variants={VARIANTS}
        />
      ))}
    </>
  ),
  { animate: ["animate", "normal"] },
);
