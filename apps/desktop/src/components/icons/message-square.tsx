// Adapted from https://lucide-animated.com/r/message-square.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const ICON_VARIANTS: Variants = {
  normal: {
    scale: 1,
    rotate: 0,
  },
  animate: {
    scale: 1.05,
    rotate: [0, -7, 7, 0],
    transition: {
      rotate: {
        duration: 0.5,
        ease: "easeInOut",
      },
      scale: {
        type: "spring",
        stiffness: 400,
        damping: 10,
      },
    },
  },
};

export const MessageSquare = createAnimatedIcon(
  "message-square",
  (controls) => (
    <motion.g animate={controls} variants={ICON_VARIANTS}>
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
    </motion.g>
  ),
);
