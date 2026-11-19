import { Component, lazy, memo, Suspense, type ReactNode } from "react";

import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { FormMessage } from "./FormMessage";

// Loading and rendering both stay inside the local boundary: a broken parser
// must never replace the surrounding page with the app-wide error screen.
//
// The parser loads with this module, not with the first reply, and renders
// directly once loaded. A lazy component suspends on its first mount even
// when its import has settled, and React holds that fallback for about
// 300 ms, so the first reply on screen flashed as raw monospace text.
type Renderer = (typeof import("./MarkdownRenderer"))["default"];
let loadedRenderer: Renderer | null = null;
const rendererModule = import("./MarkdownRenderer");
rendererModule.then(
  (module) => {
    loadedRenderer = module.default;
  },
  // The lazy renderer rethrows the failure inside the boundary.
  () => {},
);
const LazyRenderer = lazy(() => rendererModule);

function PlainContent({
  content,
  failed = false,
}: {
  content: string;
  failed?: boolean;
}) {
  const t = useT();
  return (
    <>
      {failed ? (
        <FormMessage className="mb-2">
          {t("markdownContent.fallback")}
        </FormMessage>
      ) : null}
      <pre className="whitespace-pre-wrap break-words font-mono text-xs">
        {content}
      </pre>
    </>
  );
}

class ContentBoundary extends Component<
  { content: string; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? (
      <PlainContent content={this.props.content} failed />
    ) : (
      this.props.children
    );
  }
}

/**
 * Rich model text with a local, escaped plain-text fallback. Parsing is the
 * expensive part and depends only on the text, so a parent that re-renders
 * every second (a running session) or on every scroll must not reparse it.
 */
export const MarkdownContent = memo(function MarkdownContent({
  content,
  className,
}: {
  content: string;
  className?: string;
}) {
  const MarkdownRenderer = loadedRenderer ?? LazyRenderer;
  return (
    <div
      data-slot="markdown-content"
      className={cn("min-w-0 break-words text-sm leading-relaxed", className)}
    >
      <ContentBoundary key={content} content={content}>
        <Suspense fallback={<PlainContent content={content} />}>
          <MarkdownRenderer content={content} />
        </Suspense>
      </ContentBoundary>
    </div>
  );
});
