// Adapted from https://lucide-animated.com/r/maximize.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  type: "spring",
  stiffness: 250,
  damping: 25,
};

export const Maximize = createAnimatedIcon("maximize", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="M8 3H5a2 2 0 0 0-2 2v3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "-2px", translateY: "-2px" },
      }}
    />

    <motion.path
      animate={controls}
      d="M21 8V5a2 2 0 0 0-2-2h-3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "2px", translateY: "-2px" },
      }}
    />

    <motion.path
      animate={controls}
      d="M3 16v3a2 2 0 0 0 2 2h3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "-2px", translateY: "2px" },
      }}
    />

    <motion.path
      animate={controls}
      d="M16 21h3a2 2 0 0 0 2-2v-3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "2px", translateY: "2px" },
      }}
    />
  </>
));
