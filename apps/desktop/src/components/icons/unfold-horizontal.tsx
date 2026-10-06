// Artwork from Lucide's `unfold-horizontal`
// (https://lucide.dev/icons/unfold-horizontal); ISC, (c) Lucide Contributors.
// lucide-animated has no horizontal unfold, so the motion here is local: the
// arrows push outward from the fold line, as the window is about to.
import type { Transition } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const TRANSITION: Transition = { duration: 0.5, ease: "easeInOut" };

export const UnfoldHorizontal = createAnimatedIcon(
  "unfold-horizontal",
  (controls) => (
    <>
      <path d="M12 2v2" />
      <path d="M12 8v2" />
      <path d="M12 14v2" />
      <path d="M12 20v2" />
      <motion.g
        animate={controls}
        transition={TRANSITION}
        variants={{ normal: { x: 0 }, animate: { x: [0, -2, 0] } }}
      >
        <path d="M8 12H2" />
        <path d="m5 9-3 3 3 3" />
      </motion.g>
      <motion.g
        animate={controls}
        transition={TRANSITION}
        variants={{ normal: { x: 0 }, animate: { x: [0, 2, 0] } }}
      >
        <path d="M16 12h6" />
        <path d="m19 15 3-3-3-3" />
      </motion.g>
    </>
  ),
);
