// Adapted from https://lucide-animated.com/r/search.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Search = createAnimatedIcon("search", (controls) => (
  <motion.g
    animate={controls}
    transition={{
      duration: 1,
      bounce: 0.3,
    }}
    variants={{
      normal: { x: 0, y: 0 },
      animate: {
        x: [0, 0, -3, 0],
        y: [0, -4, 0, 0],
      },
    }}
  >
    <circle cx="11" cy="11" r="8" />
    <path d="m21 21-4.3-4.3" />
  </motion.g>
));
