// Adapted from https://lucide-animated.com/r/circle-help.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const VARIANTS: Variants = {
  normal: { rotate: 0 },
  animate: { rotate: [0, -10, 10, -10, 0] },
};

export const CircleHelp = createAnimatedIcon("circle-help", (controls) => (
  <>
    <circle cx="12" cy="12" r="10" />
    <motion.g
      animate={controls}
      transition={{
        duration: 0.5,
        ease: "easeInOut",
      }}
      variants={VARIANTS}
    >
      <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
      <path d="M12 17h.01" />
    </motion.g>
  </>
));
