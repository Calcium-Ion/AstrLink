// Adapted from https://lucide-animated.com/r/route.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion, type Variants } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const CIRCLE_VARIANTS: Variants = {
  normal: {
    r: 3,
    transition: { duration: 0.15 },
  },
  animate: (delay: number) => ({
    r: [3, 4, 3],
    transition: { duration: 0.4, delay, ease: "easeInOut" },
  }),
};

export const Route = createAnimatedIcon("route", (controls) => (
  <>
    <motion.circle
      animate={controls}
      cx="6"
      cy="19"
      r="3"
      custom={0}
      initial="normal"
      variants={CIRCLE_VARIANTS}
    />
    <path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15" />
    <motion.circle
      animate={controls}
      cx="18"
      cy="5"
      r="3"
      custom={0.16}
      initial="normal"
      variants={CIRCLE_VARIANTS}
    />
  </>
));
