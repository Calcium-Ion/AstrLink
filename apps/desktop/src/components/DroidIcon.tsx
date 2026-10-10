/**
 * Monogram tile for Factory's Droid, which publishes no vector mark. It
 * matches the fallback tile a client without a logo gets in request records.
 */
export function DroidIcon({ size = 20 }: { size?: number }) {
  return (
    <svg
      aria-hidden="true"
      className="shrink-0"
      height={size}
      viewBox="0 0 24 24"
      width={size}
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect fill="currentColor" height="24" rx="5" width="24" />
      <text
        className="fill-background"
        fontFamily="inherit"
        fontSize="14"
        fontWeight="600"
        textAnchor="middle"
        x="12"
        y="17"
      >
        D
      </text>
    </svg>
  );
}
