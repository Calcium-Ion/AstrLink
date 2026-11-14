// Adapted from https://lucide-animated.com/r/blocks.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Blocks = createAnimatedIcon("blocks", (controls) => (
  <>
    <path d="M10 21V8a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-5a1 1 0 0 0-1-1H3" />
    <motion.path
      animate={controls}
      d="M14 3h7v7h-7z"
      variants={{
        normal: { translateX: 0, translateY: 0 },
        animate: { translateX: -4, translateY: 4 },
      }}
    />
  </>
));
