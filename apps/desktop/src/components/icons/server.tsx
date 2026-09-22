// Adapted from https://lucide-animated.com/r/server.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

// Keep the server silhouette still; a short activity-light pulse is enough
// feedback for a compact navigation icon.
const LIGHT_VARIANTS: Variants = {
  normal: { opacity: 1, transition: { duration: 0.15 } },
  animate: {
    opacity: [1, 0.45, 1],
    transition: {
      duration: 0.45,
      ease: "easeInOut",
    },
  },
};

export const Server = createAnimatedIcon("server", (controls) => (
  <>
    <rect height="8" rx="2" ry="2" width="20" x="2" y="2" />
    <motion.line
      animate={controls}
      initial="normal"
      variants={LIGHT_VARIANTS}
      x1="6"
      x2="10"
      y1="6"
      y2="6"
    />
    <rect height="8" rx="2" ry="2" width="20" x="2" y="14" />
    <motion.line
      animate={controls}
      initial="normal"
      variants={LIGHT_VARIANTS}
      x1="6"
      x2="10"
      y1="18"
      y2="18"
    />
  </>
));
