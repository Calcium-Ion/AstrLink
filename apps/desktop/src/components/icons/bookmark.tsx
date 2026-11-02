// Artwork from Lucide's `bookmark` (https://lucide.dev/icons/bookmark); ISC,
// (c) Lucide Contributors. lucide-animated has no bookmark, so the motion
// here is local: the ribbon drops a little, as when a page is marked.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const SVG_VARIANTS: Variants = {
  normal: {
    y: 0,
  },
  animate: {
    y: [0, 1.5, -0.5, 0],
    transition: {
      duration: 0.4,
      times: [0, 0.45, 0.8, 1],
    },
  },
};

export const Bookmark = createAnimatedIcon("bookmark", (controls) => (
  <motion.g animate={controls} initial="normal" variants={SVG_VARIANTS}>
    <path d="m19 21-7-4-7 4V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v16z" />
  </motion.g>
));
