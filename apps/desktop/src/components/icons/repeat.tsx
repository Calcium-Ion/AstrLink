// Artwork from Lucide's `repeat` (https://lucide.dev/icons/repeat); ISC, (c)
// Lucide Contributors. lucide-animated has no repeat, so the motion here is
// local: the two arrows nudge along their loop, once.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const TOP_VARIANTS: Variants = {
  normal: { x: 0 },
  animate: { x: [0, 1.5, 0], transition: { duration: 0.4 } },
};

const BOTTOM_VARIANTS: Variants = {
  normal: { x: 0 },
  animate: { x: [0, -1.5, 0], transition: { duration: 0.4 } },
};

export const Repeat = createAnimatedIcon("repeat", (controls) => (
  <>
    <motion.g animate={controls} initial="normal" variants={TOP_VARIANTS}>
      <path d="m17 2 4 4-4 4" />
      <path d="M3 11v-1a4 4 0 0 1 4-4h14" />
    </motion.g>
    <motion.g animate={controls} initial="normal" variants={BOTTOM_VARIANTS}>
      <path d="m7 22-4-4 4-4" />
      <path d="M21 13v1a4 4 0 0 1-4 4H3" />
    </motion.g>
  </>
));
