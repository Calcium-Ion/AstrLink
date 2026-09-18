// Adapted from https://lucide-animated.com/r/arrow-left.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const PATH_VARIANTS: Variants = {
  normal: { d: "m12 19-7-7 7-7", translateX: 0 },
  animate: {
    d: "m12 19-7-7 7-7",
    translateX: [0, 3, 0],
    transition: {
      duration: 0.4,
    },
  },
};

const SECOND_PATH_VARIANTS: Variants = {
  normal: { d: "M19 12H5" },
  animate: {
    d: ["M19 12H5", "M19 12H10", "M19 12H5"],
    transition: {
      duration: 0.4,
    },
  },
};

export const ArrowLeft = createAnimatedIcon("arrow-left", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="m12 19-7-7 7-7"
      variants={PATH_VARIANTS}
    />
    <motion.path
      animate={controls}
      d="M19 12H5"
      variants={SECOND_PATH_VARIANTS}
    />
  </>
));
