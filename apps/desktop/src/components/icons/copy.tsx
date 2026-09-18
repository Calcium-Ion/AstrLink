// Adapted from https://lucide-animated.com/r/copy.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  type: "spring",
  stiffness: 160,
  damping: 17,
  mass: 1,
};

export const Copy = createAnimatedIcon("copy", (controls) => (
  <>
    <motion.rect
      animate={controls}
      height="14"
      rx="2"
      ry="2"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateY: 0, translateX: 0 },
        animate: { translateY: -3, translateX: -3 },
      }}
      width="14"
      x="8"
      y="8"
    />
    <motion.path
      animate={controls}
      d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { x: 0, y: 0 },
        animate: { x: 3, y: 3 },
      }}
    />
  </>
));
