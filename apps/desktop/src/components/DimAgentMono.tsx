import { forwardRef, type SVGProps } from "react";

import dimAgentLogo from "@/assets/dimagent-logo.png";

// Official website artwork: https://dimagent.cn/dim/logo.png
export const DimAgentMono = forwardRef<
  SVGSVGElement,
  SVGProps<SVGSVGElement> & { size?: number | string }
>(function DimAgentMono({ size = "1em", style, ...props }, ref) {
  return (
    <svg
      height={size}
      ref={ref}
      style={{ flex: "none", lineHeight: 1, ...style }}
      viewBox="0 0 1024 1024"
      width={size}
      xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <title>DimAgent</title>
      <image height="1024" href={dimAgentLogo} width="1024" />
    </svg>
  );
});
