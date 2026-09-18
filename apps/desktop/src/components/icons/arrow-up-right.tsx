// Adapted from https://lucide-animated.com/r/arrow-up-right.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const ARROW_VARIANTS: Variants = {
  normal: {
    scale: 1,
    translateX: 0,
    translateY: 0,
  },
  animate: {
    scale: [1, 0.85, 1],
    translateX: [0, -4, 0],
    translateY: [0, 4, 0],
    originX: 1,
    originY: 0,
    transition: {
      duration: 0.5,
      ease: "easeInOut",
    },
  },
};

export const ArrowUpRight = createAnimatedIcon("arrow-up-right", (controls) => (
  <>
    <motion.g animate={controls} variants={ARROW_VARIANTS}>
      <path d="M7 7H17" />
      <path d="M17 7V17" />
      <path d="M7 17L17 7" />
    </motion.g>
  </>
));
