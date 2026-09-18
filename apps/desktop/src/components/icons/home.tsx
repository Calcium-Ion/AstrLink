// Adapted from https://lucide-animated.com/r/home.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition, Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  duration: 0.6,
  opacity: { duration: 0.2 },
};

const PATH_VARIANTS: Variants = {
  normal: {
    pathLength: 1,
    opacity: 1,
  },
  animate: {
    opacity: [0, 1],
    pathLength: [0, 1],
  },
};

export const Home = createAnimatedIcon("home", (controls) => (
  <>
    <path d="M3 10a2 2 0 0 1 .709-1.528l7-5.999a2 2 0 0 1 2.582 0l7 5.999A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
    <motion.path
      animate={controls}
      d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"
      transition={DEFAULT_TRANSITION}
      variants={PATH_VARIANTS}
    />
  </>
));
