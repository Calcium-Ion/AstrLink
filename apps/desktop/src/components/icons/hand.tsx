// Adapted from https://lucide-animated.com/r/hand.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const SVG_VARIANTS: Variants = {
  normal: { rotate: 0, originX: "50%", originY: "90%" },
  animate: {
    rotate: [0, -15, 10, -5, 0],
    transition: {
      duration: 0.8,
      ease: "easeInOut",
    },
  },
};

export const Hand = createAnimatedIcon("hand", (controls) => (
  <motion.g animate={controls} initial="normal" variants={SVG_VARIANTS}>
    <path d="M18 11V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2" />
    <path d="M14 10V4a2 2 0 0 0-2-2a2 2 0 0 0-2 2v2" />
    <path d="M10 10.5V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2v8" />
    <path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15" />
  </motion.g>
));
