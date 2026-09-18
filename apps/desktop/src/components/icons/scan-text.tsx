// Adapted from https://lucide-animated.com/r/scan-text.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const FRAME_VARIANTS: Variants = {
  visible: { opacity: 1 },
  hidden: { opacity: 1 },
};

const LINE_VARIANTS: Variants = {
  visible: { pathLength: 1, opacity: 1 },
  hidden: { pathLength: 0, opacity: 0 },
};

export const ScanText = createAnimatedIcon(
  "scan-text",
  (controls) => (
    <>
      <motion.path d="M3 7V5a2 2 0 0 1 2-2h2" variants={FRAME_VARIANTS} />
      <motion.path d="M17 3h2a2 2 0 0 1 2 2v2" variants={FRAME_VARIANTS} />
      <motion.path d="M21 17v2a2 2 0 0 1-2 2h-2" variants={FRAME_VARIANTS} />
      <motion.path d="M7 21H5a2 2 0 0 1-2-2v-2" variants={FRAME_VARIANTS} />
      <motion.path
        animate={controls}
        custom={0}
        d="M7 8h8"
        initial="visible"
        variants={LINE_VARIANTS}
      />
      <motion.path
        animate={controls}
        custom={1}
        d="M7 12h10"
        initial="visible"
        variants={LINE_VARIANTS}
      />
      <motion.path
        animate={controls}
        custom={2}
        d="M7 16h6"
        initial="visible"
        variants={LINE_VARIANTS}
      />
    </>
  ),
  {
    normal: "visible",
    animate: [
      (i: number) => ({
        pathLength: 0,
        opacity: 0,
        transition: { delay: i * 0.1, duration: 0.3 },
      }),
      (i: number) => ({
        pathLength: 1,
        opacity: 1,
        transition: { delay: i * 0.1, duration: 0.3 },
      }),
    ],
  },
);
