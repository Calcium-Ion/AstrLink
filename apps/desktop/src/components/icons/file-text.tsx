// Artwork from Lucide's `file-text` (https://lucide.dev/icons/file-text); ISC,
// (c) Lucide Contributors. lucide-animated has no file-text, so the motion
// here is local: the lines of text nudge along, once, as when a file is
// edited.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const LINE_VARIANTS: Variants = {
  normal: { x: 0 },
  animate: { x: [0, 1.5, 0], transition: { duration: 0.4 } },
};

export const FileText = createAnimatedIcon("file-text", (controls) => (
  <>
    <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" />
    <path d="M14 2v4a2 2 0 0 0 2 2h4" />
    <motion.g animate={controls} initial="normal" variants={LINE_VARIANTS}>
      <path d="M10 9H8" />
      <path d="M16 13H8" />
      <path d="M16 17H8" />
    </motion.g>
  </>
));
