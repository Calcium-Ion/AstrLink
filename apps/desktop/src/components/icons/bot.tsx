// Adapted from https://lucide-animated.com/r/bot.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Bot = createAnimatedIcon("bot", (controls) => (
  <>
    <path d="M12 8V4H8" />
    <rect height="12" rx="2" width="16" x="4" y="8" />
    <path d="M2 14h2" />
    <path d="M20 14h2" />

    <motion.line
      animate={controls}
      initial="normal"
      variants={{
        normal: { y1: 13, y2: 15 },
        animate: {
          y1: [13, 14, 13],
          y2: [15, 14, 15],
          transition: {
            duration: 0.5,
            ease: "easeInOut",
            delay: 0.2,
          },
        },
      }}
      x1={15}
      x2={15}
    />

    <motion.line
      animate={controls}
      initial="normal"
      variants={{
        normal: { y1: 13, y2: 15 },
        animate: {
          y1: [13, 14, 13],
          y2: [15, 14, 15],
          transition: {
            duration: 0.5,
            ease: "easeInOut",
            delay: 0.2,
          },
        },
      }}
      x1={9}
      x2={9}
    />
  </>
));
