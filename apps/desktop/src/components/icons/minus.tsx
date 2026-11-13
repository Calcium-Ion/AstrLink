// Artwork from Lucide's `minus` (https://lucide.dev/icons/minus); ISC, (c)
// Lucide Contributors. lucide-animated has no minus, so the motion here is
// local: the bar narrows a little and returns, once.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const BAR_VARIANTS: Variants = {
  normal: { scaleX: 1 },
  animate: { scaleX: [1, 0.7, 1], transition: { duration: 0.4 } },
};

export const Minus = createAnimatedIcon("minus", (controls) => (
  <motion.g animate={controls} initial="normal" variants={BAR_VARIANTS}>
    <path d="M5 12h14" />
  </motion.g>
));
