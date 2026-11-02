// Adapted from https://lucide-animated.com/r/terminal.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const LINE_VARIANTS: Variants = {
  normal: { opacity: 1 },
  animate: {
    opacity: [1, 0, 1],
    transition: {
      duration: 0.8,
      repeat: Number.POSITIVE_INFINITY,
      ease: "linear",
    },
  },
};

export const Terminal = createAnimatedIcon("terminal", (controls) => (
  <>
    <polyline points="4 17 10 11 4 5" />
    <motion.line
      animate={controls}
      initial="normal"
      variants={LINE_VARIANTS}
      x1="12"
      x2="20"
      y1="19"
      y2="19"
    />
  </>
));
