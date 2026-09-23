// Adapted from https://lucide-animated.com/r/circle-dollar-sign.json; MIT, (c) 2024-2026 pqoqubbw.
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

export const CircleDollarSign = createAnimatedIcon(
  "circle-dollar-sign",
  (controls) => (
    <>
      <circle cx="12" cy="12" r="10" />
      <motion.path
        animate={controls}
        d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8"
        initial="normal"
        variants={PATH_VARIANTS}
      />
      <motion.path
        animate={controls}
        d="M12 18V6"
        initial="normal"
        variants={PATH_VARIANTS}
      />
    </>
  ),
);
