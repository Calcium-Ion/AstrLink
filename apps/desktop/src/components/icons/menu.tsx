// Adapted from https://lucide-animated.com/r/menu.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const LINE_VARIANTS: Variants = {
  normal: {
    rotate: 0,
    y: 0,
    opacity: 1,
  },
  animate: (custom: number) => ({
    rotate: custom === 1 ? 45 : custom === 3 ? -45 : 0,
    y: custom === 1 ? 6 : custom === 3 ? -6 : 0,
    opacity: custom === 2 ? 0 : 1,
    transition: {
      type: "spring",
      stiffness: 260,
      damping: 20,
    },
  }),
};

export const Menu = createAnimatedIcon("menu", (controls) => (
  <>
    <motion.line
      animate={controls}
      custom={1}
      initial="normal"
      variants={LINE_VARIANTS}
      x1="4"
      x2="20"
      y1="6"
      y2="6"
    />
    <motion.line
      animate={controls}
      custom={2}
      initial="normal"
      variants={LINE_VARIANTS}
      x1="4"
      x2="20"
      y1="12"
      y2="12"
    />
    <motion.line
      animate={controls}
      custom={3}
      initial="normal"
      variants={LINE_VARIANTS}
      x1="4"
      x2="20"
      y1="18"
      y2="18"
    />
  </>
));
