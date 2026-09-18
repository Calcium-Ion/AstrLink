// Adapted from https://lucide-animated.com/r/ban.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const CIRCLE_VARIANTS: Variants = {
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

const LINE_VARIANTS: Variants = {
  normal: {
    opacity: 1,
    pathLength: 1,
    transition: {
      duration: 0.3,
      opacity: { duration: 0.1 },
    },
  },
  slash: () => ({
    opacity: [0, 1],
    pathLength: [0, 1],
    transition: {
      duration: 0.4,
      opacity: { duration: 0.1 },
    },
  }),
};

export const Ban = createAnimatedIcon(
  "ban",
  (controls) => (
    <>
      <motion.circle
        animate={controls}
        cx="12"
        cy="12"
        initial="normal"
        r="10"
        variants={CIRCLE_VARIANTS}
      />
      <motion.path
        animate={controls}
        d="m4.9 4.9 14.2 14.2"
        initial="normal"
        variants={LINE_VARIANTS}
      />
    </>
  ),
  { animate: ["animate", "slash"] },
);
