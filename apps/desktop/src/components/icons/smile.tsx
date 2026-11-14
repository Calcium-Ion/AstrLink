// Adapted from https://lucide-animated.com/r/smile.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const FACE_VARIANTS: Variants = {
  normal: {
    scale: 1,
    rotate: 0,
    strokeWidth: 2,
    transition: { duration: 0.3, ease: "easeOut" },
  },
  animate: {
    scale: [1, 1.15, 1.05, 1.1],
    rotate: [0, -3, 3, 0],
    strokeWidth: [2, 2.5, 2.5, 2.5],
    transition: {
      duration: 0.8,
      times: [0, 0.3, 0.6, 1],
      ease: "easeInOut",
    },
  },
};

const MOUTH_VARIANTS: Variants = {
  normal: {
    d: "M8 14s1.5 2 4 2 4-2 4-2",
    pathLength: 1,
    pathOffset: 0,
    strokeWidth: 2,
    transition: { duration: 0.3, ease: "easeOut" },
  },
  animate: {
    d: "M7 13.5s2.5 3.5 5 3.5 5-3.5 5-3.5",
    pathLength: [0.3, 1, 1],
    pathOffset: [0, 0, 0],
    strokeWidth: 2.5,
    transition: {
      d: { duration: 0.4, ease: "easeOut" },
      pathLength: {
        duration: 0.5,
        times: [0, 0.5, 1],
        ease: "easeInOut",
      },
      delay: 0.1,
    },
  },
};

const EYE_VARIANTS: Variants = {
  normal: {
    scale: 1,
    opacity: 1,
    transition: { duration: 0.3, ease: "easeOut" },
  },
  animate: {
    scale: [1, 1.5, 0.8, 1.2],
    opacity: [1, 1, 1, 1],
    transition: {
      duration: 0.5,
      times: [0, 0.3, 0.6, 1],
      ease: "easeInOut",
    },
  },
};

export const Smile = createAnimatedIcon("smile", (controls) => (
  <motion.g animate={controls} initial="normal" variants={FACE_VARIANTS}>
    <circle cx="12" cy="12" r="10" />
    <motion.path
      animate={controls}
      d="M8 14s1.5 2 4 2 4-2 4-2"
      initial="normal"
      variants={MOUTH_VARIANTS}
    />
    <motion.line
      animate={controls}
      initial="normal"
      variants={EYE_VARIANTS}
      x1="9"
      x2="9.01"
      y1="9"
      y2="9"
    />
    <motion.line
      animate={controls}
      initial="normal"
      variants={EYE_VARIANTS}
      x1="15"
      x2="15.01"
      y1="9"
      y2="9"
    />
  </motion.g>
));
