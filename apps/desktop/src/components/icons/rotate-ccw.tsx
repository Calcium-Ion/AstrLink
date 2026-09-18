// Adapted from https://lucide-animated.com/r/rotate-ccw.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const RotateCcw = createAnimatedIcon("rotate-ccw", (controls) => (
  <motion.g
    animate={controls}
    transition={{ type: "spring", stiffness: 250, damping: 25 }}
    variants={{
      normal: { rotate: "0deg" },
      animate: { rotate: "-50deg" },
    }}
  >
    <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
    <path d="M3 3v5h5" />
  </motion.g>
));
