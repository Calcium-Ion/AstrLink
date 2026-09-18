// Adapted from https://lucide-animated.com/r/boxes.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Boxes = createAnimatedIcon("boxes", (controls) => (
  <motion.g style={{ overflow: "visible" }}>
    <motion.path
      animate={controls}
      d="M2.97 12.92A2 2 0 0 0 2 14.63v3.24a2 2 0 0 0 .97 1.71l3 1.8a2 2 0 0 0 2.06 0L12 19v-5.5l-5-3-4.03 2.42Z m4.03 3.58 -4.74 -2.85 m4.74 2.85 5-3 m-5 3v5.17"
      variants={{
        normal: { translateX: 0, translateY: 0 },
        animate: { translateX: -1.5, translateY: 1.5 },
      }}
    />
    <motion.path
      animate={controls}
      d="M12 13.5V19l3.97 2.38a2 2 0 0 0 2.06 0l3-1.8a2 2 0 0 0 .97-1.71v-3.24a2 2 0 0 0-.97-1.71L17 10.5l-5 3Z m5 3-5-3 m5 3 4.74-2.85 M17 16.5v5.17"
      variants={{
        normal: { translateX: 0, translateY: 0 },
        animate: { translateX: 1.5, translateY: 1.5 },
      }}
    />
    <motion.path
      animate={controls}
      d="M7.97 4.42A2 2 0 0 0 7 6.13v4.37l5 3 5-3V6.13a2 2 0 0 0-.97-1.71l-3-1.8a2 2 0 0 0-2.06 0l-3 1.8Z M12 8 7.26 5.15 m4.74 2.85 4.74-2.85 M12 13.5V8"
      variants={{
        normal: { translateX: 0, translateY: 0 },
        animate: { translateX: 0, translateY: -1.5 },
      }}
    />
  </motion.g>
));
