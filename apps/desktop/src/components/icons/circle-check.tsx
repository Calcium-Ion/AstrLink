// Adapted from https://lucide-animated.com/r/circle-check.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const PATH_VARIANTS: Variants = {
  normal: {
    opacity: 1,
    pathLength: 1,
    transition: {
      duration: 0.3,
      opacity: { duration: 0.1 },
    },
  },
  animate: {
    opacity: [0, 1],
    pathLength: [0, 1],
    transition: {
      duration: 0.4,
      opacity: { duration: 0.1 },
    },
  },
};

export const CircleCheck = createAnimatedIcon("circle-check", (controls) => (
  <>
    <circle cx="12" cy="12" r="10" />
    <motion.path
      animate={controls}
      d="m9 12 2 2 4-4"
      initial="normal"
      variants={PATH_VARIANTS}
    />
  </>
));
