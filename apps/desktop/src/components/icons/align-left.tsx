// Adapted from https://lucide-animated.com/r/align-left.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  type: "spring",
  stiffness: 150,
  damping: 15,
  mass: 0.3,
};

export const AlignLeft = createAnimatedIcon("align-left", (controls) => (
  <>
    <motion.line
      animate={controls}
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { x2: 21 },
        animate: { x2: 21 },
      }}
      x1="3"
      x2="21"
      y1="6"
      y2="6"
    />
    <motion.line
      animate={controls}
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { x2: 15 },
        animate: { x2: 19 },
      }}
      x1="3"
      x2="15"
      y1="12"
      y2="12"
    />
    <motion.line
      animate={controls}
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { x2: 17 },
        animate: { x2: 12 },
      }}
      x1="3"
      x2="17"
      y1="18"
      y2="18"
    />
  </>
));
