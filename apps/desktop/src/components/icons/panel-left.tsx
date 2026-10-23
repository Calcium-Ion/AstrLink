import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

export const PanelLeft = createAnimatedIcon("panel-left", (controls) => (
  <>
    <rect x="3" y="3" width="18" height="18" rx="2" />
    <motion.path
      d="M9 3v18"
      animate={controls}
      transition={{ duration: 0.3, ease: "easeInOut" }}
      variants={{ normal: { x: 0 }, animate: { x: [0, -1, 0] } }}
    />
  </>
));
