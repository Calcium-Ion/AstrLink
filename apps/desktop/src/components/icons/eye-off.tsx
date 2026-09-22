// Adapted from https://lucide-animated.com/r/eye-off.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const EyeOff = createAnimatedIcon("eye-off", (controls) => (
  <>
    <path d="M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49" />
    <path d="M14.084 14.158a3 3 0 0 1-4.242-4.242" />
    <path d="M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143" />
    <motion.path
      animate={controls}
      d="m2 2 20 20"
      variants={{
        normal: { pathLength: 1, opacity: 1, pathOffset: 0 },
        animate: {
          pathLength: [0, 2],
          opacity: [0, 1],
          pathOffset: [0, 2],
          transition: { duration: 0.6 },
        },
      }}
    />
  </>
));
