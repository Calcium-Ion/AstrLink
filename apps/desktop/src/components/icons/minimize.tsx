// Adapted from https://lucide-animated.com/r/minimize.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DEFAULT_TRANSITION: Transition = {
  type: "spring",
  stiffness: 250,
  damping: 25,
};

export const Minimize = createAnimatedIcon("minimize", (controls) => (
  <>
    <motion.path
      animate={controls}
      d="M8 3v3a2 2 0 0 1-2 2H3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "2px", translateY: "2px" },
      }}
    />
    <motion.path
      animate={controls}
      d="M21 8h-3a2 2 0 0 1-2-2V3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "-2px", translateY: "2px" },
      }}
    />
    <motion.path
      animate={controls}
      d="M3 16h3a2 2 0 0 1 2 2v3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "2px", translateY: "-2px" },
      }}
    />
    <motion.path
      animate={controls}
      d="M16 21v-3a2 2 0 0 1 2-2h3"
      transition={DEFAULT_TRANSITION}
      variants={{
        normal: { translateX: "0%", translateY: "0%" },
        animate: { translateX: "-2px", translateY: "-2px" },
      }}
    />
  </>
));
