import { useRef } from "react";

/**
 * Returns `value` while `open` and the last open value afterwards. Callers
 * close an overlay by clearing the state it renders, while Radix keeps the
 * content mounted through its exit animation; without the snapshot, that
 * closing frame shows whatever the cleared state falls back to.
 */
export function useExitSnapshot<T>(value: T, open: boolean): T {
  const snapshot = useRef(value);
  if (open) snapshot.current = value;
  return snapshot.current;
}
