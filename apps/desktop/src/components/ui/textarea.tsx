import * as React from "react";

import { cn } from "@/lib/utils";

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "flex field-sizing-content min-h-16 w-full rounded-md border border-input bg-card px-2.5 py-2 text-sm transition-[color,box-shadow] outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-45 aria-invalid:border-destructive aria-invalid:ring-destructive/20",
        className,
      )}
      {...props}
    />
  );
}

/** Select and reveal text inside the editor without scrolling the app workspace. */
function selectTextareaRange(
  input: HTMLTextAreaElement,
  start: number,
  end: number,
) {
  input.focus({ preventScroll: true });
  input.setSelectionRange(start, end);

  // setSelectionRange does not scroll to off-screen text in WKWebView/Chromium.
  // A hidden mirror uses the same wrapping and typography to locate the line.
  const style = getComputedStyle(input);
  const mirror = document.createElement("div");
  for (const property of [
    "font-family",
    "font-size",
    "font-weight",
    "font-style",
    "line-height",
    "letter-spacing",
    "word-spacing",
    "text-indent",
    "text-transform",
    "tab-size",
    "padding-top",
    "padding-right",
    "padding-bottom",
    "padding-left",
    "direction",
    "word-break",
  ])
    mirror.style.setProperty(property, style.getPropertyValue(property));
  Object.assign(mirror.style, {
    position: "fixed",
    top: "0",
    left: "0",
    visibility: "hidden",
    pointerEvents: "none",
    boxSizing: "border-box",
    width: `${input.clientWidth}px`,
    whiteSpace: "pre-wrap",
    overflowWrap: "break-word",
  });
  mirror.textContent = input.value.slice(0, start);
  const marker = document.createElement("span");
  marker.textContent = input.value.slice(start, end) || " ";
  mirror.append(marker);
  document.body.append(mirror);
  input.scrollTop = Math.max(0, marker.offsetTop - input.clientHeight / 3);
  mirror.remove();
}

export { Textarea, selectTextareaRange };
