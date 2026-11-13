import { useEffect, useState } from "react";

/** The device pixel ratio, updated when the window moves to another screen. */
export function useDevicePixelRatio() {
  const [ratio, setRatio] = useState(() => window.devicePixelRatio || 1);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(`(resolution: ${ratio}dppx)`);
    const onChange = () => setRatio(window.devicePixelRatio || 1);
    media.addEventListener?.("change", onChange);
    return () => media.removeEventListener?.("change", onChange);
  }, [ratio]);
  return ratio;
}
