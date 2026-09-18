// Adapted from https://lucide-animated.com/r/arrow-right.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const PATH_VARIANTS: Variants = {
  normal: { d: "M5 12h14" },
  animate: {
    d: ["M5 12h14", "M5 12h9", "M5 12h14"],
    transition: {
      duration: 0.4,
    },
  },
};

const SECONDARY_PATH_VARIANTS: Variants = {
  normal: { d: "m12 5 7 7-7 7", translateX: 0 },
  animate: {
    d: "m12 5 7 7-7 7",
    translateX: [0, -3, 0],
    transition: {
      duration: 0.4,
    },
  },
};

export const ArrowRight = createAnimatedIcon("arrow-right", (controls) => (
  <>
    <motion.path animate={controls} d="M5 12h14" variants={PATH_VARIANTS} />
    <motion.path
      animate={controls}
      d="m12 5 7 7-7 7"
      variants={SECONDARY_PATH_VARIANTS}
    />
  </>
));
