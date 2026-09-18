// Adapted from https://lucide-animated.com/r/chevron-up.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  times: [0, 0.4, 1],
  duration: 0.5,
};

export const ChevronUp = createAnimatedIcon("chevron-up", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="m18 15-6-6-6 6"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { y: 0 },
        animate: { y: [0, -2, 0] },
      }}
    />
  </>
));
