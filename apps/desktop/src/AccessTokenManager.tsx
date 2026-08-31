import {
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Check,
  Copy,
  KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { FormMessage } from "@/components/FormMessage";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";

import {
  createAccessToken,
  deleteAccessToken,
  listRequestRecords,
  revealAccessToken,
} from "./bridge";
import type { AccessTokenSummary } from "./access-token-model";
import type { RequestRecord } from "./request-record-model";
import { i18n } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import { aggregateTodayUsage, startOfTodayIso } from "./today-usage";

export type AccessTokenCatalogStatus =
  | "blocked"
  | "loading"
  | "ready"
  | "error";

export interface AccessTokenCatalog {
  status: AccessTokenCatalogStatus;
  items: AccessTokenSummary[];
  error: string | null;
  stale: boolean;
}

type TokenUsageSlice = {
  total_tokens: number;
  capped: boolean;
};

type TokenUsageStats = {
  status: "loading" | "ready" | "error";
  today: TokenUsageSlice | null;
  lifetime: TokenUsageSlice | null;
};

const USAGE_PAGE_LIMIT = 200;
const USAGE_MAX_PAGES = 5;

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function createdAtLabel(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return i18n.t("tokens.unknown");
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function formatTokenCount(slice: TokenUsageSlice | null, status: TokenUsageStats["status"]): string {
  if (status === "loading" && slice === null) return "…";
  if (slice === null) return "—";
  const base = slice.total_tokens.toLocaleString();
  return slice.capped ? `${base}+` : base;
}

async function loadTokenUsageSlice(
  tokenId: string,
  from: string | undefined,
): Promise<TokenUsageSlice> {
  const accumulated: RequestRecord[] = [];
  let cursor: string | undefined;
  let nextCursor: string | null = null;
  for (let pageIndex = 0; pageIndex < USAGE_MAX_PAGES; pageIndex += 1) {
    const page = await listRequestRecords({
      local_access_token_id: tokenId,
      limit: USAGE_PAGE_LIMIT,
      ...(from ? { from } : {}),
      ...(cursor ? { cursor } : {}),
    });
    accumulated.push(...page.items);
    nextCursor = page.next_cursor;
    if (!nextCursor) break;
    cursor = nextCursor;
  }
  const summary = aggregateTodayUsage(accumulated, nextCursor !== null);
  return { total_tokens: summary.total_tokens, capped: summary.capped };
}

export function AccessTokenManager({
  catalog,
  coreSessionKey,
  isReady,
  onRefresh,
  onTokenCreated,
  onTokenDeleted,
}: {
  catalog: AccessTokenCatalog;
  coreSessionKey: string | null;
  isReady: boolean;
  onRefresh: () => void;
  onTokenCreated: (token: AccessTokenSummary) => void;
  onTokenDeleted: (tokenId: string) => void;
}) {
  const t = i18n.t.bind(i18n);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [deletingID, setDeletingID] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] =
    useState<AccessTokenSummary | null>(null);
  const [copyingID, setCopyingID] = useState<string | null>(null);
  const [copiedID, setCopiedID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [usageByToken, setUsageByToken] = useState<
    Record<string, TokenUsageStats>
  >({});
  const sessionGeneration = useRef(0);
  const revealGeneration = useRef(0);
  const usageGeneration = useRef(0);
  const nameInput = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    sessionGeneration.current += 1;
    revealGeneration.current += 1;
    usageGeneration.current += 1;
    setCreateOpen(false);
    setName("");
    setCreating(false);
    setDeletingID(null);
    setPendingDelete(null);
    setCopyingID(null);
    setCopiedID(null);
    setError(null);
    setUsageByToken({});
  }, [coreSessionKey]);

  useEffect(() => {
    if (createOpen) nameInput.current?.focus();
  }, [createOpen]);

  const refreshTokenUsage = useCallback(async () => {
    const generation = usageGeneration.current + 1;
    usageGeneration.current = generation;
    if (!isReady || catalog.status !== "ready" || catalog.items.length === 0) {
      setUsageByToken({});
      return;
    }

    const tokenIds = catalog.items.map((token) => token.id);
    setUsageByToken((current) => {
      const next: Record<string, TokenUsageStats> = {};
      for (const id of tokenIds) {
        next[id] = {
          status: "loading",
          today: current[id]?.today ?? null,
          lifetime: current[id]?.lifetime ?? null,
        };
      }
      return next;
    });

    const todayFrom = startOfTodayIso(new Date());
    for (const tokenId of tokenIds) {
      if (usageGeneration.current !== generation) return;
      try {
        const today = await loadTokenUsageSlice(tokenId, todayFrom);
        if (usageGeneration.current !== generation) return;
        const lifetime = await loadTokenUsageSlice(tokenId, undefined);
        if (usageGeneration.current !== generation) return;
        setUsageByToken((current) => ({
          ...current,
          [tokenId]: { status: "ready", today, lifetime },
        }));
      } catch {
        if (usageGeneration.current !== generation) return;
        setUsageByToken((current) => ({
          ...current,
          [tokenId]: {
            status: "error",
            today: current[tokenId]?.today ?? null,
            lifetime: current[tokenId]?.lifetime ?? null,
          },
        }));
      }
    }
  }, [catalog.items, catalog.status, isReady]);

  useEffect(() => {
    void refreshTokenUsage();
  }, [refreshTokenUsage, coreSessionKey]);

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError(i18n.t("tokens.nameRequired"));
      nameInput.current?.focus();
      return;
    }
    if ([...trimmedName].length > 64) {
      setError(i18n.t("tokens.nameTooLong"));
      nameInput.current?.focus();
      return;
    }
    if (!isReady || creating) return;

    const generation = sessionGeneration.current;
    setCreating(true);
    setError(null);
    try {
      const result = await createAccessToken(trimmedName);
      if (sessionGeneration.current !== generation) return;
      onTokenCreated(result.token);
      setCopiedID(null);
      setCreateOpen(false);
      setName("");
      notify.success(i18n.t("tokens.created", { name: result.token.name }));
    } catch (requestError) {
      if (sessionGeneration.current === generation) {
        setError(messageOf(requestError, i18n.t("tokens.createFailed")));
      }
    } finally {
      if (sessionGeneration.current === generation) setCreating(false);
    }
  };

  const copyValue = async (tokenId: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedID(tokenId);
    } catch {
      setCopiedID(null);
      setError(i18n.t("tokens.copyManual"));
    }
  };

  const copyToken = async (tokenId: string) => {
    if (!isReady || copyingID !== null) return;

    const generation = revealGeneration.current + 1;
    const session = sessionGeneration.current;
    revealGeneration.current = generation;
    setCopyingID(tokenId);
    setCopiedID(null);
    setError(null);
    try {
      const result = await revealAccessToken(tokenId);
      if (
        sessionGeneration.current !== session ||
        revealGeneration.current !== generation
      ) {
        return;
      }
      await copyValue(tokenId, result.access_token);
    } catch (requestError) {
      if (
        sessionGeneration.current === session &&
        revealGeneration.current === generation
      ) {
        setError(messageOf(requestError, i18n.t("tokens.copyFailed")));
      }
    } finally {
      if (
        sessionGeneration.current === session &&
        revealGeneration.current === generation
      ) {
        setCopyingID(null);
      }
    }
  };

  const refresh = () => {
    revealGeneration.current += 1;
    setCopyingID(null);
    setCopiedID(null);
    onRefresh();
  };

  const remove = async () => {
    if (pendingDelete === null || deletingID !== null) return;
    const token = pendingDelete;
    const generation = sessionGeneration.current;
    revealGeneration.current += 1;
    setCopyingID(null);
    setCopiedID(null);
    setDeletingID(token.id);
    setError(null);
    try {
      await deleteAccessToken(token.id);
      if (sessionGeneration.current !== generation) return;
      onTokenDeleted(token.id);
      setPendingDelete(null);
      notify.success(i18n.t("tokens.deleted", { name: token.name }));
    } catch (requestError) {
      if (sessionGeneration.current === generation) {
        setError(messageOf(requestError, i18n.t("tokens.deleteFailed")));
      }
    } finally {
      if (sessionGeneration.current === generation) setDeletingID(null);
    }
  };

  const catalogBusy =
    catalog.status === "loading" ||
    creating ||
    deletingID !== null;

  return (
    <section className="flex min-h-0 w-full flex-1 flex-col" aria-labelledby="token-manager-heading">
      <PageHeader
        actions={
          <>
            <Button
              disabled={!isReady || catalogBusy}
              onClick={() => {
                setCreateOpen(true);
                setError(null);
              }}
              type="button"
            >
              <Plus />
              {t("tokens.createToken")}
            </Button>
            <Button
              variant="outline"
              disabled={!isReady || catalogBusy}
              onClick={refresh}
              type="button"
            >
              <RefreshCw className={catalog.status === "loading" ? "animate-spin motion-reduce:animate-none" : undefined} />
              {catalog.status === "loading" ? t("common.refreshing") : t("common.refresh")}
            </Button>
          </>
        }
        description={t("tokens.description")}
        title={t("tokens.title")}
        titleId="token-manager-heading"
      />

      {(!isReady || catalog.status === "blocked") && (
        <FormMessage className="mb-3" tone="notice">
          {catalog.items.length
            ? t("tokens.stale")
            : t("tokens.blocked")}
        </FormMessage>
      )}
      {catalog.status === "error" && catalog.error ? (
        <FormMessage className="mb-3" tone="error">
          {catalog.error}
        </FormMessage>
      ) : null}
      {error ? (
        <FormMessage className="mb-3" tone="error">
          {error}
        </FormMessage>
      ) : null}

      <ScrollArea
        aria-busy={catalog.status === "loading"}
        aria-label={t("tokens.listLabel")}
        className="min-h-0"
      >
        <div className="grid content-start gap-2">
          {catalog.status === "blocked" && catalog.items.length === 0 ? (
            <TokenBoardState
              description={t("tokens.waitingHint")}
              title={t("tokens.waiting")}
            />
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="grid gap-2" aria-label={t("tokens.loading")}>
              <span className="h-[4.75rem] animate-pulse rounded-md border bg-muted" />
              <span className="h-[4.75rem] animate-pulse rounded-md border bg-muted" />
              <span className="h-[4.75rem] animate-pulse rounded-md border bg-muted" />
            </div>
          ) : catalog.status === "error" && catalog.items.length === 0 ? (
            <TokenBoardState
              action={
                <Button
                  variant="outline"
                  disabled={!isReady}
                  onClick={refresh}
                  type="button"
                >
                  {t("common.retry")}
                </Button>
              }
              description={t("tokens.unavailableHint")}
              title={t("tokens.unavailable")}
            />
          ) : catalog.items.length === 0 ? (
            <TokenBoardState
              action={
                <Button
                  disabled={!isReady}
                  onClick={() => setCreateOpen(true)}
                  type="button"
                >
                  <Plus />
                  {t("tokens.createToken")}
                </Button>
              }
              description={t("tokens.emptyHint")}
              title={t("tokens.empty")}
            />
          ) : (
            catalog.items.map((token) => {
              const isCopying = copyingID === token.id;
              const isCopied = copiedID === token.id;
              const usage = usageByToken[token.id];
              const usageStatus = usage?.status ?? (isReady ? "loading" : "error");
              return (
                <article
                  className="min-w-0 rounded-md border bg-card"
                  data-testid="access-token-row"
                  key={token.id}
                >
                  <div className="flex min-w-0 items-center gap-2 px-3.5 py-3 max-[560px]:flex-wrap">
                    <StatusDot tone="positive" />
                    <strong className="truncate text-sm font-medium">{token.name}</strong>
                    <code className="min-w-0 flex-1 truncate font-mono text-xs text-text-secondary">
                      {token.hint}
                    </code>
                    <div className="flex shrink-0 items-center gap-1">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!isReady || deletingID !== null || copyingID !== null}
                      onClick={() => void copyToken(token.id)}
                      type="button"
                    >
                      {isCopying ? (
                        <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                      ) : isCopied ? (
                        <Check />
                      ) : (
                        <Copy />
                      )}
                      {isCopying ? t("common.copying") : isCopied ? t("common.copied") : t("common.copy")}
                    </Button>
                    <Button
                      className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
                      disabled={!isReady || deletingID !== null}
                      onClick={() => {
                        revealGeneration.current += 1;
                        setCopyingID(null);
                        setCopiedID(null);
                        setPendingDelete(token);
                        setError(null);
                      }}
                      type="button"
                      size="sm"
                      variant="ghost"
                    >
                      <Trash2 />
                      {deletingID === token.id ? t("tokens.deleting") : t("common.delete")}
                    </Button>
                    </div>
                  </div>
                  <dl className="grid grid-cols-3 gap-3 border-t px-3.5 py-2 max-[560px]:grid-cols-1">
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        {t("tokens.todayTokens")}
                      </dt>
                      <dd className="mt-0.5 text-xs tabular-nums">
                        {formatTokenCount(usage?.today ?? null, usageStatus)}
                      </dd>
                    </div>
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        {t("tokens.lifetimeTokens")}
                      </dt>
                      <dd className="mt-0.5 text-xs tabular-nums">
                        {formatTokenCount(usage?.lifetime ?? null, usageStatus)}
                      </dd>
                    </div>
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        {t("tokens.createdAt")}
                      </dt>
                      <dd className="mt-0.5 text-xs tabular-nums">
                        {createdAtLabel(token.created_at)}
                      </dd>
                    </div>
                  </dl>
                </article>
              );
            })
          )}
        </div>
      </ScrollArea>

      <Dialog
        open={createOpen}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !creating) {
            setCreateOpen(false);
            setName("");
          }
        }}
      >
        <DialogContent showCloseButton={!creating}>
          <DialogHeader>
            <KeyRound
              aria-hidden="true"
              className="mb-1 size-5 text-muted-foreground"
              strokeWidth={1.5}
            />
            <DialogTitle>{t("tokens.createTitle")}</DialogTitle>
            <DialogDescription>{t("tokens.createHint")}</DialogDescription>
          </DialogHeader>
            <form onSubmit={(event) => void submitCreate(event)}>
              <div className="grid gap-2">
              <Label htmlFor="access-token-name">{t("tokens.name")}</Label>
              <Input
                autoComplete="off"
                id="access-token-name"
                maxLength={64}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder={t("tokens.namePlaceholder")}
                ref={nameInput}
                value={name}
              />
              </div>
              <DialogFooter className="mt-5">
                <Button
                  variant="outline"
                  disabled={creating}
                  onClick={() => {
                    setCreateOpen(false);
                    setName("");
                  }}
                  type="button"
                >
                  {t("common.cancel")}
                </Button>
                <Button disabled={creating} type="submit">
                  {creating ? t("tokens.creating") : t("tokens.create")}
                </Button>
              </DialogFooter>
            </form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        cancelLabel={t("common.cancel")}
        confirmLabel={deletingID === pendingDelete?.id ? t("tokens.deleting") : t("tokens.confirmDelete")}
        description={
          <>
            <p>
              {catalog.items.length === 1
                ? t("tokens.deleteLast", { name: pendingDelete?.name ?? "" })
                : t("tokens.deleteBody", { name: pendingDelete?.name ?? "" })}
            </p>
            <p>{t("tokens.irreversible")}</p>
          </>
        }
        destructive
        disabled={deletingID !== null}
        onCancel={() => setPendingDelete(null)}
        onConfirm={() => void remove()}
        open={pendingDelete !== null}
        title={t("tokens.deleteTitle")}
      />
    </section>
  );
}

function TokenBoardState({
  action,
  description,
  title,
}: {
  action?: ReactNode;
  description: string;
  title: string;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-8 py-14 text-center text-xs text-muted-foreground">
      <KeyRound
        aria-hidden="true"
        className="mb-1 size-5 text-muted-foreground"
        strokeWidth={1.5}
      />
      <p className="text-sm font-medium text-foreground">{title}</p>
      <span>{description}</span>
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
