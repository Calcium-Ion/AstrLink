import { act } from "react";

/**
 * happy-dom has no Tailwind styles. Give dialog overlays and content the
 * distinct enter/exit animations they have in the app, so Radix keeps the
 * closing frame mounted until animationend. Returns the cleanup.
 */
export function installDialogAnimations(): () => void {
  const style = document.createElement("style");
  style.textContent = `
    [data-slot$="dialog-overlay"][data-state="open"],
    [data-slot$="dialog-content"][data-state="open"] { animation-name: enter; }
    [data-slot$="dialog-overlay"][data-state="closed"],
    [data-slot$="dialog-content"][data-state="closed"] { animation-name: exit; }
  `;
  document.head.append(style);
  return () => style.remove();
}

/** Ends every running exit animation, as the browser would after it plays. */
export async function finishExitAnimations(): Promise<void> {
  await act(async () => {
    for (const node of document.querySelectorAll('[data-state="closed"]')) {
      node.dispatchEvent(
        new AnimationEvent("animationend", { animationName: "exit" }),
      );
    }
  });
}
