const IDLE_DELAY_MS = 1000;
const FADE_DURATION_MS = 200;

/** Cover native scrollports, including dynamically mounted and portaled content. */
export function initializeScrollbarAutoHide() {
  const animations = new Map<HTMLElement, Animation>();
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const forcedColors = window.matchMedia("(forced-colors: active)");

  const onScroll = (event: Event) => {
    const target =
      event.target === document ? document.scrollingElement : event.target;
    if (
      !(target instanceof HTMLElement) ||
      target.hasAttribute("data-radix-scroll-area-viewport") ||
      forcedColors.matches
    ) {
      return;
    }

    animations.get(target)?.cancel();
    const duration =
      IDLE_DELAY_MS + (reducedMotion.matches ? 0 : FADE_DURATION_MS);
    // Animate only the thumb's paint. Native scrolling, dragging, gutters and
    // existing element transitions/animations keep their original behavior.
    const animation = target.animate(
      [
        { "--native-scrollbar-opacity": "1", offset: 0 },
        {
          "--native-scrollbar-opacity": "1",
          offset: IDLE_DELAY_MS / duration,
        },
        { "--native-scrollbar-opacity": "0", offset: 1 },
      ],
      { duration },
    );
    animations.set(target, animation);
    const release = () => {
      if (animations.get(target) === animation) animations.delete(target);
    };
    animation.onfinish = release;
    animation.oncancel = release;
  };

  // Scroll does not bubble; capture also reaches textareas and dialog portals.
  document.addEventListener("scroll", onScroll, {
    capture: true,
    passive: true,
  });
  return () => {
    document.removeEventListener("scroll", onScroll, true);
    for (const animation of animations.values()) animation.cancel();
    animations.clear();
  };
}
