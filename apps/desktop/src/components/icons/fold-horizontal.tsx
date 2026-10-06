// Artwork from Lucide's `fold-horizontal`
// (https://lucide.dev/icons/fold-horizontal); ISC, (c) Lucide Contributors.
// lucide-animated has no horizontal fold, so the motion here is local: the
// arrows press in toward the fold line, as the window is about to.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const TRANSITION: Transition = { duration: 0.5, ease: "easeInOut" };

export const FoldHorizontal = createAnimatedIcon(
  "fold-horizontal",
  (controls) => (
    <>
      <path d="M12 2v2" />
      <path d="M12 8v2" />
      <path d="M12 14v2" />
      <path d="M12 20v2" />
      <motion.g
        animate={controls}
        transition={TRANSITION}
        variants={{ normal: { x: 0 }, animate: { x: [0, 2, 0] } }}
      >
        <path d="M2 12h6" />
        <path d="m5 15 3-3-3-3" />
      </motion.g>
      <motion.g
        animate={controls}
        transition={TRANSITION}
        variants={{ normal: { x: 0 }, animate: { x: [0, -2, 0] } }}
      >
        <path d="M22 12h-6" />
        <path d="m19 9-3 3 3 3" />
      </motion.g>
    </>
  ),
);
