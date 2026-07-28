import type { ReactNode } from "react";

export function PageHeader({
  actions,
  back,
  description,
  eyebrow,
  title,
  titleId,
  variant = "plain",
}: {
  actions?: ReactNode;
  back?: {
    label: string;
    onClick: () => void;
  };
  description?: ReactNode;
  eyebrow?: ReactNode;
  title: string;
  titleId?: string;
  variant?: "card" | "plain";
}) {
  return (
    <header
      className={`page-header${
        variant === "card" ? " page-header--card" : ""
      }`}
    >
      <div className="page-header__copy">
        {back ? (
          <button
            aria-label={back.label}
            className="page-header__back"
            onClick={back.onClick}
            type="button"
          >
            <svg
              aria-hidden="true"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="1.8"
            >
              <path d="m15 18-6-6 6-6" />
            </svg>
            {back.label}
          </button>
        ) : null}
        {eyebrow ? (
          <span className="page-header__eyebrow">{eyebrow}</span>
        ) : null}
        <h1 id={titleId}>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-header__actions">{actions}</div> : null}
    </header>
  );
}
