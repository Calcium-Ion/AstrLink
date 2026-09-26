import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const Link = createAnimatedIcon("link", (controls) => (
  <motion.g
    animate={controls}
    variants={{ normal: { scale: 1 }, animate: { scale: 1.08 } }}
  >
    <path d="M10 13a4 4 0 0 0 6 0l4-4a4 4 0 0 0-6-6l-2 2" />
    <path d="M14 11a4 4 0 0 0-6 0l-4 4a4 4 0 0 0 6 6l2-2" />
  </motion.g>
));
