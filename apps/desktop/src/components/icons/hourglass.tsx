// Adapted from https://lucide-animated.com/r/hourglass.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Hourglass = createAnimatedIcon("hourglass", (controls) => (
  <motion.g
    animate={controls}
    style={{ transformOrigin: "12px 12px" }}
    transition={{ type: "spring", stiffness: 100, damping: 15, mass: 1 }}
    variants={{
      normal: { rotate: 0 },
      animate: { rotate: 180 },
    }}
  >
    <path d="M5 22h14" />
    <path d="M5 2h14" />
    <path d="M17 22v-4.172a2 2 0 0 0-.586-1.414L12 12l-4.414 4.414A2 2 0 0 0 7 17.828V22" />
    <path d="M7 2v4.172a2 2 0 0 0 .586 1.414L12 12l4.414-4.414A2 2 0 0 0 17 6.172V2" />
  </motion.g>
));
