// Adapted from https://lucide-animated.com/r/loader-circle.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition, Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const G_VARIANTS: Variants = {
  normal: { rotate: 0 },
  animate: {
    rotate: 360,
    transition: {
      repeat: Number.POSITIVE_INFINITY,
      duration: 0.8,
      ease: "linear",
    },
  },
};

const DEFAULT_TRANSITION: Transition = {
  type: "spring",
  stiffness: 50,
  damping: 10,
};

export const LoaderCircle = createAnimatedIcon("loader-circle", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="M21 12a9 9 0 1 1-6.219-8.56"
      style={{ transformOrigin: "12px 12px" }}
      transition={DEFAULT_TRANSITION}
      variants={G_VARIANTS}
    />
  </>
));
