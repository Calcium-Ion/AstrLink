// Adapted from https://lucide-animated.com/r/plus.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Plus = createAnimatedIcon("plus", (controls) => (
  <motion.g
    animate={controls}
    transition={{ type: "spring", stiffness: 100, damping: 15 }}
    variants={{
      normal: {
        rotate: 0,
      },
      animate: {
        rotate: 180,
      },
    }}
  >
    <path d="M5 12h14" />
    <path d="M12 5v14" />
  </motion.g>
));
