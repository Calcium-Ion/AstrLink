import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { markText, useFindScroll } from "@/components/FindBar";
import { ChevronRight } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  FIND_HIT_LIMIT,
  findMatcher,
  findNeedle,
  findOffsets,
  type FindRequest,
} from "@/find-model";
import { useT } from "@/i18n";
import {
  JSON_CHILD_BATCH,
  JSON_STRING_CHUNK,
  JSON_TREE_ROOT,
  childCount,
  containerPreview,
  defaultOpenPaths,
  detectBase64,
  expandAllPaths,
  flattenJsonTree,
  isFoldableString,
  isJsonContainer,
  jsonKeyText,
  revealJsonHit,
  searchJsonTree,
  stringPreview,
  type JsonContainerNode,
  type JsonNode,
  type JsonSearchHit,
  type JsonSearchResult,
  type JsonTreeRow,
} from "@/json-tree-model";
import { cn } from "@/lib/utils";

type RenderText = (text: string) => ReactNode;

const STRING_PREVIEW_CHARS = 96;

/** The hits that show in one row, and how to find more in its previews. */
interface RowHits {
  key: number[];
  value: number[];
  matcher: RegExp;
  length: number;
}

/**
 * A JSON document as a foldable tree. Only open containers render, long
 * strings and inline files fold to one line, and long arrays page in
 * batches, so a large request body stays cheap to show and easy to scan.
 *
 * `find` searches the whole document, folded parts included, and unfolds
 * and pages just enough of it to put the active hit on screen.
 */
export function JsonTreeView({
  root,
  renderText = plainText,
  revealText,
  find,
  toolbar = true,
  className,
}: {
  root: JsonNode;
  /** Renders string content, such as with privacy placeholders marked. */
  renderText?: RenderText;
  /** Strings to unfold at first, along with their ancestors. */
  revealText?: (text: string) => boolean;
  find?: FindRequest;
  /** Expand-all and collapse-all; a small document nested in a list drops them. */
  toolbar?: boolean;
  className?: string;
}) {
  const t = useT();
  const [stored, setView] = useState(() => initialView(root, revealText));
  let view = stored;
  // A new document starts from its own default folding.
  if (view.root !== root) {
    view = initialView(root, revealText);
    setView(view);
  }
  const { open, limits } = view;
  const rows = useMemo(
    () => flattenJsonTree(root, open, limits),
    [root, open, limits],
  );
  const setOpen = useCallback((next: ReadonlySet<string>) => {
    setView((current) => ({ ...current, open: next }));
  }, []);
  const toggle = useCallback((path: string) => {
    setView((current) => {
      const next = new Set(current.open);
      if (!next.delete(path)) next.add(path);
      return { ...current, open: next };
    });
  }, []);
  const setLimit = useCallback((path: string, limit: number) => {
    setView((current) => ({
      ...current,
      limits: new Map(current.limits).set(path, limit),
    }));
  }, []);

  const query = find?.query ?? "";
  const matcher = useMemo(() => findMatcher(query), [query]);
  const length = findNeedle(query).length;
  const search = useMemo(
    () => (matcher ? searchJsonTree(root, matcher, FIND_HIT_LIMIT) : null),
    [matcher, root],
  );
  const hitsByPath = useMemo(
    () => (search && matcher ? rowHits(search, matcher, length) : null),
    [length, matcher, search],
  );
  const onResult = find?.onResult;
  useEffect(() => {
    if (!search) return;
    onResult?.({ count: search.hits.length, capped: search.capped });
  }, [onResult, search]);
  const hitCount = search?.hits.length ?? 0;
  const activeHit =
    hitCount > 0
      ? search!.hits[Math.min(find?.active ?? 0, hitCount - 1)]!
      : null;
  const seq = find?.seq ?? 0;
  useEffect(() => {
    if (!activeHit) return;
    setView((current) => {
      const next = revealJsonHit(
        activeHit,
        length,
        current.open,
        current.limits,
      );
      return next.open === current.open && next.limits === current.limits
        ? current
        : { ...current, ...next };
    });
  }, [activeHit, length, seq]);
  const treeRef = useRef<HTMLDivElement>(null);
  useFindScroll(treeRef, activeHit, seq);

  return (
    <div
      className={cn("grid gap-1", className)}
      data-testid="json-tree"
      ref={treeRef}
    >
      {toolbar && isJsonContainer(root) ? (
        <div className="flex flex-wrap items-center gap-1">
          <Button
            onClick={() => setOpen(expandAllPaths(root))}
            size="xs"
            type="button"
            variant="ghost"
          >
            {t("audit.jsonTree.expandAll")}
          </Button>
          <Button
            onClick={() => setOpen(new Set([JSON_TREE_ROOT]))}
            size="xs"
            type="button"
            variant="ghost"
          >
            {t("audit.jsonTree.collapseAll")}
          </Button>
        </div>
      ) : null}
      <div className="min-w-0 font-mono text-xs leading-5">
        {rows.map((row) => (
          <JsonRow
            active={
              row.type === "node" && activeHit?.path === row.path
                ? activeHit
                : undefined
            }
            hits={row.type === "node" ? hitsByPath?.get(row.path) : undefined}
            key={`${row.type}:${row.path}`}
            onLimit={setLimit}
            onToggle={toggle}
            renderText={renderText}
            row={row}
          />
        ))}
      </div>
    </div>
  );
}

/** Hits grouped by the row that shows them, for marking while rendering. */
function rowHits(
  search: JsonSearchResult,
  matcher: RegExp,
  length: number,
): Map<string, RowHits> {
  const byPath = new Map<string, RowHits>();
  for (const hit of search.hits) {
    let entry = byPath.get(hit.path);
    if (!entry) {
      entry = { key: [], value: [], matcher, length };
      byPath.set(hit.path, entry);
    }
    entry[hit.field].push(hit.offset);
  }
  return byPath;
}

interface TreeViewState {
  root: JsonNode;
  open: ReadonlySet<string>;
  /** Children or characters shown past the first batch, by path. */
  limits: ReadonlyMap<string, number>;
}

function initialView(
  root: JsonNode,
  revealText: ((text: string) => boolean) | undefined,
): TreeViewState {
  return {
    root,
    open: defaultOpenPaths(root, { reveal: revealText }),
    limits: new Map(),
  };
}

interface JsonRowProps {
  row: JsonTreeRow;
  renderText: RenderText;
  /** Find hits in this row, if any. */
  hits?: RowHits;
  /** The hit in view, when it is in this row. */
  active?: JsonSearchHit;
  onToggle: (path: string) => void;
  onLimit: (path: string, limit: number) => void;
}

const JsonRow = memo(function JsonRow({
  row,
  renderText,
  hits,
  active,
  onToggle,
  onLimit,
}: JsonRowProps) {
  const t = useT();
  const guides = <IndentGuides depth={row.depth} />;

  if (row.type === "more") {
    return (
      <div className="flex items-center">
        {guides}
        <span aria-hidden="true" className="size-5.5 shrink-0" />
        <Button
          className="my-0.5 mr-2"
          onClick={() => onLimit(row.path, row.shown + JSON_CHILD_BATCH)}
          size="xs"
          type="button"
          variant="outline"
        >
          {t("audit.jsonTree.showMore", {
            count: Math.min(JSON_CHILD_BATCH, row.total - row.shown),
          })}
        </Button>
        <span className="font-sans text-micro text-muted-foreground">
          {t("audit.jsonTree.shownOf", {
            shown: row.shown.toLocaleString(),
            total: row.total.toLocaleString(),
          })}
        </span>
      </div>
    );
  }

  if (row.type === "close") {
    return (
      <div className="flex items-start">
        {guides}
        {/* Under the toggle that opened it, where its guide line ends. */}
        {row.node.complete ? (
          <span className="flex h-5.5 w-5.5 shrink-0 items-center justify-center text-muted-foreground">
            {row.node.kind === "object" ? "}" : "]"}
          </span>
        ) : (
          <>
            <span aria-hidden="true" className="size-5.5 shrink-0" />
            <span className="py-px">
              <CutMarker innermost={cutsHere(row.node)} />
            </span>
          </>
        )}
      </div>
    );
  }

  const { node } = row;
  const foldable = isJsonContainer(node)
    ? childCount(node) > 0 || !node.complete
    : node.kind === "string" && isFoldableString(node.text);
  const label =
    row.name ??
    (row.index !== null ? `[${row.index}]` : t("audit.jsonTree.root"));
  const length = hits?.length ?? 0;
  const activeKey = active?.field === "key" ? active.offset : null;
  const activeValue = active?.field === "value" ? active.offset : null;

  return (
    <div className="flex items-start" data-path={row.path}>
      {guides}
      {foldable ? (
        <Button
          aria-expanded={row.open}
          aria-label={t(
            row.open ? "audit.jsonTree.collapse" : "audit.jsonTree.expand",
            { name: label },
          )}
          className="text-muted-foreground"
          onClick={() => onToggle(row.path)}
          size="icon-xs"
          type="button"
          variant="ghost"
        >
          <ChevronRight
            aria-hidden="true"
            className={cn("transition-transform", row.open && "rotate-90")}
          />
        </Button>
      ) : (
        <span aria-hidden="true" className="size-5.5 shrink-0" />
      )}
      <div className="min-w-0 flex-1 py-px [overflow-wrap:anywhere]">
        {row.name !== null ? (
          <>
            <span className="text-foreground">
              "{markText(jsonKeyText(row.name), hits?.key, length, activeKey)}"
            </span>
            <span className="text-muted-foreground">: </span>
          </>
        ) : row.index !== null ? (
          <span className="text-muted-foreground">{row.index}: </span>
        ) : null}
        <NodeValue
          activeOffset={activeValue}
          hits={hits}
          node={node}
          open={row.open}
          renderText={renderText}
        />
        {node.kind === "string" && row.open ? (
          <StringBody
            activeOffset={activeValue}
            hitLength={length}
            hits={hits?.value}
            onLimit={(limit) => onLimit(row.path, limit)}
            renderText={renderText}
            shown={row.shownChars}
            text={node.text}
          />
        ) : null}
      </div>
    </div>
  );
}, sameRowProps);

function NodeValue({
  node,
  open,
  renderText,
  hits,
  activeOffset,
}: {
  node: JsonNode;
  open: boolean;
  renderText: RenderText;
  hits?: RowHits;
  activeOffset: number | null;
}) {
  const t = useT();
  const length = hits?.length ?? 0;
  switch (node.kind) {
    case "object":
    case "array": {
      const empty = childCount(node) === 0 && node.complete;
      const [start, end] = node.kind === "object" ? ["{", "}"] : ["[", "]"];
      return (
        <>
          <span className="text-muted-foreground">
            {empty ? `${start}${end}` : open ? start : containerPreview(node)}
          </span>
          {empty ? null : (
            <Meta>
              {t(
                node.kind === "object"
                  ? "audit.jsonTree.keys"
                  : "audit.jsonTree.items",
                { count: childCount(node) },
              )}
            </Meta>
          )}
          {!open && !node.complete ? <CutMarker innermost={false} /> : null}
        </>
      );
    }
    case "string": {
      if (!isFoldableString(node.text)) {
        return (
          <span className="text-success-foreground">
            "
            {markText(node.text, hits?.value, length, activeOffset, renderText)}
            {node.complete ? '"' : <CutMarker innermost />}
          </span>
        );
      }
      const base64 = detectBase64(node.text);
      const preview = base64
        ? stringPreview(node.text, 32)
        : stringPreview(node.text, STRING_PREVIEW_CHARS);
      return (
        <>
          {open ? null : (
            <span className="text-success-foreground">
              "
              {base64
                ? preview
                : markText(
                    preview,
                    // The preview folds newlines and clips, so hits are
                    // found in it again rather than mapped from the text.
                    hits && hits.value.length > 0
                      ? findOffsets(preview, hits.matcher)
                      : undefined,
                    length,
                    null,
                    renderText,
                  )}
              "
            </span>
          )}
          <Meta>
            {base64
              ? ["base64", base64.mediaType, `≈${formatBytes(base64.bytes)}`]
                  .filter(Boolean)
                  .join(" · ")
              : t("audit.jsonTree.chars", {
                  value: node.text.length.toLocaleString(),
                })}
          </Meta>
          {node.complete ? null : <CutMarker innermost />}
        </>
      );
    }
    default:
      return (
        <span className="text-violet-foreground">
          {markText(node.text, hits?.value, length, activeOffset)}
        </span>
      );
  }
}

/** An unfolded string, rendered a chunk at a time. */
function StringBody({
  text,
  shown,
  renderText,
  hits,
  hitLength,
  activeOffset,
  onLimit,
}: {
  text: string;
  shown: number;
  renderText: RenderText;
  hits?: number[];
  hitLength: number;
  activeOffset: number | null;
  onLimit: (limit: number) => void;
}) {
  const t = useT();
  const remaining = text.length - shown;
  return (
    <div className="my-0.5 grid gap-1 rounded-sm bg-card px-2 py-1">
      <div className="whitespace-pre-wrap text-success-foreground">
        {markText(
          remaining > 0 ? text.slice(0, shown) : text,
          hits,
          hitLength,
          activeOffset,
          renderText,
        )}
      </div>
      {remaining > 0 ? (
        <Button
          className="justify-self-start"
          onClick={() => onLimit(shown + JSON_STRING_CHUNK)}
          size="xs"
          type="button"
          variant="outline"
        >
          {t("audit.jsonTree.showMoreText", {
            value: remaining.toLocaleString(),
          })}
        </Button>
      ) : null}
    </div>
  );
}

// One indent step per depth, each carrying a hairline under the centre of
// the 22px fold toggle above it, so an open container's line runs from its
// toggle straight down to its closing bracket.
function IndentGuides({ depth }: { depth: number }) {
  if (depth === 0) return null;
  return (
    <span aria-hidden="true" className="flex shrink-0 self-stretch">
      {Array.from({ length: depth }, (_, step) => (
        <span className="relative w-4 shrink-0" key={step}>
          <span className="absolute inset-y-0 left-[calc(0.6875rem-0.5px)] w-px bg-input" />
        </span>
      ))}
    </span>
  );
}

function Meta({ children }: { children: ReactNode }) {
  return (
    <span className="ml-2 font-sans text-micro whitespace-nowrap text-muted-foreground">
      {children}
    </span>
  );
}

function CutMarker({ innermost }: { innermost: boolean }) {
  const t = useT();
  return (
    <span className="ml-1 font-sans text-micro text-warning-foreground">
      {innermost ? `… ${t("audit.jsonTree.cut")}` : "…"}
    </span>
  );
}

/** The capture ended in this container rather than in one of its children. */
function cutsHere(node: JsonContainerNode): boolean {
  const last =
    node.kind === "object" ? node.entries.at(-1)?.[1] : node.items.at(-1);
  if (!last) return true;
  if (isJsonContainer(last) || last.kind === "string") return last.complete;
  return true;
}

function plainText(text: string): ReactNode {
  return text;
}

function sameRowProps(prev: JsonRowProps, next: JsonRowProps): boolean {
  if (
    prev.renderText !== next.renderText ||
    prev.hits !== next.hits ||
    prev.active !== next.active ||
    prev.onToggle !== next.onToggle ||
    prev.onLimit !== next.onLimit
  )
    return false;
  const left = prev.row as unknown as Record<string, unknown>;
  const right = next.row as unknown as Record<string, unknown>;
  const keys = Object.keys(left);
  return (
    keys.length === Object.keys(right).length &&
    keys.every((key) => left[key] === right[key])
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
