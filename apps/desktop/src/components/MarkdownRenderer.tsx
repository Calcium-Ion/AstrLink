import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

import { Checkbox } from "./ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

const components: Components = {
  h1: ({ children }) => <h3 className="text-base font-semibold">{children}</h3>,
  h2: ({ children }) => <h4 className="text-sm font-semibold">{children}</h4>,
  h3: ({ children }) => <h5 className="text-sm font-semibold">{children}</h5>,
  h4: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  h5: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  h6: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  p: ({ children }) => <p className="whitespace-pre-wrap">{children}</p>,
  ul: ({ children }) => (
    <ul className="list-disc space-y-1 pl-5">{children}</ul>
  ),
  ol: ({ children, start }) => (
    <ol className="list-decimal space-y-1 pl-5" start={start}>
      {children}
    </ol>
  ),
  li: ({ children }) => <li className="[&>p]:inline">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="border-l-2 pl-3 text-muted-foreground">
      {children}
    </blockquote>
  ),
  pre: ({ children }) => (
    <pre className="max-w-full overflow-x-auto rounded-md border bg-background p-3 text-xs [&>code]:bg-transparent [&>code]:p-0">
      {children}
    </pre>
  ),
  code: ({ children, className }) => (
    <code
      className={`rounded-sm bg-background px-1 font-mono text-xs ${className ?? ""}`}
    >
      {children}
    </code>
  ),
  a: ({ children, href }) =>
    href ? (
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline underline-offset-2"
      >
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  // Model output must not fetch remote resources merely by being displayed.
  img: ({ alt, src }) => (
    <span className="text-muted-foreground">{alt || src}</span>
  ),
  input: ({ checked }) => (
    <Checkbox
      checked={checked ?? false}
      disabled
      className="mr-1 inline-flex align-middle"
    />
  ),
  table: ({ children }) => <Table>{children}</Table>,
  thead: ({ children }) => <TableHeader>{children}</TableHeader>,
  tbody: ({ children }) => <TableBody>{children}</TableBody>,
  tr: ({ children }) => <TableRow>{children}</TableRow>,
  th: ({ children, style }) => <TableHead style={style}>{children}</TableHead>,
  td: ({ children, style }) => <TableCell style={style}>{children}</TableCell>,
};

export default function MarkdownRenderer({ content }: { content: string }) {
  return (
    <div className="min-w-0 space-y-3">
      {/* Keep raw HTML as text and retain react-markdown's safe URL handling. */}
      <Markdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </Markdown>
    </div>
  );
}
