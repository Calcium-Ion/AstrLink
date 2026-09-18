// Adapted from https://lucide-animated.com/r/map-pin-off.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const SVG_VARIANTS: Variants = {
  normal: {
    y: 0,
  },
  animate: {
    y: [0, -5, -3],
    transition: {
      duration: 0.5,
      times: [0, 0.6, 1],
    },
  },
};

const BAR_VARIANTS: Variants = {
  normal: {
    opacity: 1,
  },
  animate: {
    opacity: [0, 1],
    pathLength: [0, 1],
    transition: {
      delay: 0.3,
      duration: 0.3,
      opacity: { duration: 0.1, delay: 0.3 },
    },
  },
};

export const MapPinOff = createAnimatedIcon("map-pin-off", (controls) => (
  <motion.g animate={controls} initial="normal" variants={SVG_VARIANTS}>
    <path d="M12.75 7.09a3 3 0 0 1 2.16 2.16" />
    <path d="M17.072 17.072c-1.634 2.17-3.527 3.912-4.471 4.727a1 1 0 0 1-1.202 0C9.539 20.193 4 14.993 4 10a8 8 0 0 1 1.432-4.568" />
    <motion.path
      animate={controls}
      d="m2 2 20 20"
      initial="normal"
      variants={BAR_VARIANTS}
    />
    <path d="M8.475 2.818A8 8 0 0 1 20 10c0 1.183-.31 2.377-.81 3.533" />
    <path d="M9.13 9.13a3 3 0 0 0 3.74 3.74" />
  </motion.g>
));
