import mark from "@/assets/cc-switch.png";

/** CC Switch application mark, from its MIT-licensed src-tauri/icons/128x128.png. */
export function CCSwitchIcon({ size = 20 }: { size?: number }) {
  return (
    <img
      src={mark}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      className="shrink-0"
    />
  );
}
