// Adapted from https://lucide-animated.com/r/lock-keyhole.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const LockKeyhole = createAnimatedIcon("lock-keyhole", (controls) => (
  <motion.g
    animate={controls}
    initial="normal"
    transition={{
      duration: 1,
      ease: [0.4, 0, 0.2, 1],
    }}
    variants={{
      normal: {
        rotate: 0,
        scale: 1,
      },
      animate: {
        rotate: [-3, 1, -2, 0],
        scale: [0.95, 1.05, 0.98, 1],
      },
    }}
  >
    <circle cx="12" cy="16" r="1" />
    <rect height="12" rx="2" width="18" x="3" y="10" />
    <motion.path
      animate={controls}
      d="M7 10V7a5 5 0 0 1 10 0v3"
      initial="normal"
      transition={{
        duration: 0.3,
        ease: [0.4, 0, 0.2, 1],
      }}
      variants={{
        normal: {
          pathLength: 1,
        },
        animate: {
          pathLength: 0.7,
        },
      }}
    />
  </motion.g>
));
