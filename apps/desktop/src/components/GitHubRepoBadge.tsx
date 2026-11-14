import { GithubMono } from "@/components/brand-icons";
import { openInSystemBrowser } from "@/components/ExternalLink";
import { cn } from "@/lib/utils";

/**
 * A link to a GitHub repository as GitHub draws one: its mark, then the
 * stars once they are known. It opens in the system browser.
 */
export function GitHubRepoBadge({
  href,
  label,
  stars,
  starsText,
  title,
  className,
}: {
  href: string;
  /** What the link opens, for screen readers. */
  label: string;
  stars?: number;
  /** The stars as the page writes them. */
  starsText?: string;
  title?: string;
  className?: string;
}) {
  return (
    <a
      aria-label={label}
      className={cn(
        "inline-flex h-6 shrink-0 items-center gap-1.5 rounded-sm border bg-card px-2 text-micro font-medium text-foreground transition-colors outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/30",
        className,
      )}
      data-slot="github-repo-badge"
      href={href}
      onClick={(event) => openInSystemBrowser(event, href)}
      rel="noopener noreferrer"
      target="_blank"
      title={title}
    >
      <GithubMono aria-hidden="true" size={14} />
      {stars !== undefined ? (
        <span className="inline-flex items-center gap-1 border-l pl-1.5">
          {/* Lucide's star, MIT. */}
          <svg
            aria-hidden="true"
            className="size-3 text-muted-foreground"
            fill="none"
            viewBox="0 0 24 24"
          >
            <path
              d="M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z"
              stroke="currentColor"
              strokeLinejoin="round"
              strokeWidth="2"
            />
          </svg>
          <span className="tabular-nums">{starsText ?? stars}</span>
        </span>
      ) : null}
    </a>
  );
}
