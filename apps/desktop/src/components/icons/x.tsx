// Adapted from https://lucide-animated.com/r/x.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const PATH_VARIANTS: Variants = {
  normal: {
    opacity: 1,
    pathLength: 1,
  },
  animate: {
    opacity: [0, 1],
    pathLength: [0, 1],
  },
};

export const X = createAnimatedIcon("x", (controls) => (
  <>
    <motion.path animate={controls} d="M18 6 6 18" variants={PATH_VARIANTS} />
    <motion.path
      animate={controls}
      d="m6 6 12 12"
      transition={{ delay: 0.2 }}
      variants={PATH_VARIANTS}
    />
  </>
));
