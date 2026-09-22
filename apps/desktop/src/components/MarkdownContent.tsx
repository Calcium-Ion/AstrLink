import { Component, lazy, Suspense, type ReactNode } from "react";

import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { FormMessage } from "./FormMessage";

// Loading and rendering both stay inside the local boundary: a broken parser
// must never replace the surrounding page with the app-wide error screen.
const MarkdownRenderer = lazy(() => import("./MarkdownRenderer"));

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

/** Rich model text with a local, escaped plain-text fallback. */
export function MarkdownContent({
  content,
  className,
}: {
  content: string;
  className?: string;
}) {
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
}
