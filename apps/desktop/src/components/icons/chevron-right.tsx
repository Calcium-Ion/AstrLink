// Adapted from https://lucide-animated.com/r/chevron-right.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  times: [0, 0.4, 1],
  duration: 0.5,
};

export const ChevronRight = createAnimatedIcon("chevron-right", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="m9 18 6-6-6-6"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { x: 0 },
        animate: { x: [0, 2, 0] },
      }}
    />
  </>
));
