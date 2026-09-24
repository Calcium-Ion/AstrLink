import {
  useRef,
  type CompositionEventHandler,
  type KeyboardEventHandler,
} from "react";

type TextControl = HTMLInputElement | HTMLTextAreaElement;

type ImeHandlers<T extends TextControl> = {
  onKeyDown?: KeyboardEventHandler<T>;
  onCompositionStart?: CompositionEventHandler<T>;
  onCompositionUpdate?: CompositionEventHandler<T>;
  onCompositionEnd?: CompositionEventHandler<T>;
};

// Pinyin separates syllables with spaces in its marked text: "gpt" shows "g p t".
const SEGMENTED_LATIN = /^[a-zü']+(?:\s+[a-zü']+)+$/i;

/**
 * Whether the IME owns this key. WebKit dispatches the Enter or Escape that
 * ends a composition after compositionend, so only keyCode 229 identifies it.
 */
export function isImeKeyEvent(event: KeyboardEvent) {
  return event.isComposing || event.keyCode === 229;
}

/** Keeps an overlay open when Escape only dismisses the IME candidate list. */
export function ignoreImeEscape(
  onEscapeKeyDown?: (event: KeyboardEvent) => void,
) {
  return (event: KeyboardEvent) => {
    if (isImeKeyEvent(event)) event.preventDefault();
    else onEscapeKeyDown?.(event);
  };
}

/**
 * Keeps IME-owned keys away from field shortcuts, and repairs the segmented
 * marked text WebKit leaves behind when focus or the first responder changes
 * mid-composition ("g p t" becomes "gpt", as if the raw input was confirmed).
 */
export function useImeTextControl<T extends TextControl>({
  onKeyDown,
  onCompositionStart,
  onCompositionUpdate,
  onCompositionEnd,
}: ImeHandlers<T>): Required<ImeHandlers<T>> {
  const composition = useRef<{
    start: number;
    outsideLength: number;
    marked: string;
  } | null>(null);

  return {
    onKeyDown(event) {
      if (isImeKeyEvent(event.nativeEvent)) {
        // Window-level Escape and Enter shortcuts must not see it either.
        event.stopPropagation();
        return;
      }
      onKeyDown?.(event);
    },
    onCompositionStart(event) {
      const { value, selectionStart, selectionEnd } = event.currentTarget;
      const start = selectionStart ?? value.length;
      composition.current = {
        start,
        outsideLength: value.length - ((selectionEnd ?? start) - start),
        marked: "",
      };
      onCompositionStart?.(event);
    },
    onCompositionUpdate(event) {
      if (composition.current) composition.current.marked = event.data;
      onCompositionUpdate?.(event);
    },
    onCompositionEnd(event) {
      const control = event.currentTarget;
      const pending = composition.current;
      composition.current = null;
      onCompositionEnd?.(event);
      if (!pending || !SEGMENTED_LATIN.test(pending.marked)) return;
      // A confirmed candidate or cancellation replaces the marked text; only an
      // abandoned composition leaves it verbatim.
      const { start, outsideLength, marked } = pending;
      const end = start + marked.length;
      if (
        control.value.length !== outsideLength + marked.length ||
        control.value.slice(start, end) !== marked
      )
        return;
      control.setRangeText(marked.replace(/\s+/g, ""), start, end, "end");
      // Notify synchronously, before blur, so commit-on-blur fields see it.
      control.dispatchEvent(
        new InputEvent("input", {
          bubbles: true,
          inputType: "insertReplacementText",
        }),
      );
    },
  };
}
