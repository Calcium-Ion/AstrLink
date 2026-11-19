// Adapted from https://lucide-animated.com/r/waypoints.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const VARIANTS: Variants = {
  normal: {
    pathLength: 1,
    opacity: 1,
  },
  animate: (custom: number) => ({
    pathLength: [0, 1],
    opacity: [0, 1],
    transition: {
      delay: 0.15 * custom,
      opacity: { delay: 0.1 * custom },
    },
  }),
};

export const Waypoints = createAnimatedIcon("waypoints", (controls) => (
  <>
    <motion.circle
      animate={controls}
      custom={0}
      cx="12"
      cy="4.5"
      r="2.5"
      variants={VARIANTS}
    />
    <motion.path
      animate={controls}
      custom={1}
      d="m10.2 6.3-3.9 3.9"
      variants={VARIANTS}
    />
    <motion.circle
      animate={controls}
      custom={0}
      cx="4.5"
      cy="12"
      r="2.5"
      variants={VARIANTS}
    />
    <motion.path
      animate={controls}
      custom={2}
      d="M7 12h10"
      variants={VARIANTS}
    />
    <motion.circle
      animate={controls}
      custom={0}
      cx="19.5"
      cy="12"
      r="2.5"
      variants={VARIANTS}
    />
    <motion.path
      animate={controls}
      custom={3}
      d="m13.8 17.7 3.9-3.9"
      variants={VARIANTS}
    />
    <motion.circle
      animate={controls}
      custom={0}
      cx="12"
      cy="19.5"
      r="2.5"
      variants={VARIANTS}
    />
  </>
));
