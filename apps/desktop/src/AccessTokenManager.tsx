import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { ActionGroup } from "@/components/ActionGroup";
import {
  type FormEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Check,
  Copy,
  Key as KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
  Shredder as Trash2,
} from "@/components/icons";

import { CompactCount } from "@/components/CompactCount";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CopyableValue } from "@/components/CopyableValue";
import { FormMessage } from "@/components/FormMessage";
import { DataField, DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { Field } from "@/components/Field";
import { ListToolbar } from "@/components/ListToolbar";
import { Panel } from "@/components/Panel";
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

import {
  createAccessToken,
  deleteAccessToken,
  listAccessTokenUsage,
  revealAccessToken,
} from "./bridge";
import type { AccessTokenSummary } from "./access-token-model";
import { i18n } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import { startOfTodayIso } from "./usage-range";
import { CCSwitchImportDialog } from "./CCSwitchImportDialog";
import { CCSwitchIcon } from "@/components/CCSwitchIcon";

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
};

type TokenUsageStats = {
  status: "loading" | "ready" | "error";
  today: TokenUsageSlice | null;
  lifetime: TokenUsageSlice | null;
};

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

function tokenCountPlaceholder(
  slice: TokenUsageSlice | null,
  status: TokenUsageStats["status"],
): string {
  return status === "loading" && slice === null ? "…" : "—";
}

export function AccessTokenManager({
  catalog,
  coreSessionKey,
  inferenceURL,
  isReady,
  onRefresh,
  onTokenCreated,
  onTokenDeleted,
}: {
  catalog: AccessTokenCatalog;
  coreSessionKey: string | null;
  inferenceURL: string;
  isReady: boolean;
  onRefresh: () => void;
  onTokenCreated: (token: AccessTokenSummary) => void;
  onTokenDeleted: (tokenId: string) => void;
}) {
  const t = i18n.t.bind(i18n);
  const [query, setQuery] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [deletingID, setDeletingID] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<AccessTokenSummary | null>(
    null,
  );
  const [copyingID, setCopyingID] = useState<string | null>(null);
  const [copiedID, setCopiedID] = useState<string | null>(null);
  const [importToken, setImportToken] = useState<AccessTokenSummary | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [usageByToken, setUsageByToken] = useWorkspaceSnapshot<
    Record<string, TokenUsageStats>
  >(`token-usage:${coreSessionKey}`, {});
  const sessionGeneration = useRef(0);
  const revealGeneration = useRef(0);
  const usageGeneration = useRef(0);
  const nameInput = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    sessionGeneration.current += 1;
    revealGeneration.current += 1;
    usageGeneration.current += 1;
    setCreateOpen(false);
    setQuery("");
    setName("");
    setCreating(false);
    setDeletingID(null);
    setPendingDelete(null);
    setCopyingID(null);
    setCopiedID(null);
    setImportToken(null);
    setError(null);
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

    try {
      const response = await listAccessTokenUsage(startOfTodayIso(new Date()));
      if (usageGeneration.current !== generation) return;
      const totals = new Map(
        response.items.map((item) => [item.token_id, item]),
      );
      const next: Record<string, TokenUsageStats> = {};
      for (const tokenId of tokenIds) {
        const usage = totals.get(tokenId);
        next[tokenId] = {
          status: "ready",
          today: { total_tokens: usage?.today_tokens ?? 0 },
          lifetime: { total_tokens: usage?.total_tokens ?? 0 },
        };
      }
      setUsageByToken(next);
    } catch {
      if (usageGeneration.current !== generation) return;
      setUsageByToken((current) => {
        const next: Record<string, TokenUsageStats> = {};
        for (const tokenId of tokenIds) {
          next[tokenId] = {
            status: "error",
            today: current[tokenId]?.today ?? null,
            lifetime: current[tokenId]?.lifetime ?? null,
          };
        }
        return next;
      });
    }
  }, [catalog.items, catalog.status, isReady, setUsageByToken]);

  useEffect(() => {
    void refreshTokenUsage();
    return () => {
      usageGeneration.current += 1;
    };
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

  const search = query.trim().toLocaleLowerCase();
  const visibleTokens = catalog.items.filter((token) =>
    `${token.name} ${token.hint}`.toLocaleLowerCase().includes(search),
  );

  const catalogBusy =
    catalog.status === "loading" || creating || deletingID !== null;

  return (
    <section
      className="@container flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
      aria-labelledby="token-manager-heading"
    >
      <PageHeader
        variant="compact"
        className="@max-[560px]:flex-wrap @max-[560px]:items-start @max-[560px]:gap-3"
        actions={
          <>
            <Button
              disabled={!isReady || catalogBusy}
              onClick={() => {
                setCreateOpen(true);
                setError(null);
              }}
              size="sm"
              type="button"
            >
              <Plus />
              {t("tokens.createToken")}
            </Button>
            <Button
              variant="outline"
              disabled={!isReady || catalogBusy}
              onClick={refresh}
              size="sm"
              type="button"
            >
              <RefreshCw
                className={
                  catalog.status === "loading"
                    ? "animate-spin motion-reduce:animate-none"
                    : undefined
                }
              />
              {catalog.status === "loading"
                ? t("common.refreshing")
                : t("common.refresh")}
            </Button>
          </>
        }
        description={t("tokens.description")}
        title={t("tokens.title")}
        titleId="token-manager-heading"
      />

      <Panel className="mb-3 shrink-0">
        <DataRow>
          <CopyableValue
            label={t("overview.apiAddress")}
            value={inferenceURL}
            placeholder={t("overview.waitingReady")}
            copyLabel={t("overview.copyApiAddress")}
          />
        </DataRow>
      </Panel>

      {(!isReady || catalog.status === "blocked") && (
        <FormMessage className="mb-3" tone="notice">
          {catalog.items.length ? t("tokens.stale") : t("tokens.blocked")}
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

      <div className="mb-3 shrink-0">
        <ListToolbar
          title={t("tokens.listLabel")}
          count={
            search
              ? `${visibleTokens.length} / ${catalog.items.length}`
              : catalog.items.length
          }
          query={query}
          onQueryChange={setQuery}
          searchLabel={t("tokens.search")}
          placeholder={t("tokens.searchPlaceholder")}
          clearLabel={t("common.clearSearch")}
          help={{ label: t("tokens.title"), content: t("tokens.description") }}
        />
      </div>
      <div
        aria-busy={catalog.status === "loading"}
        aria-label={t("tokens.listLabel")}
        className="min-h-0 min-w-0 flex-1 overflow-y-auto pb-1 pr-1"
      >
        {catalog.status === "blocked" && catalog.items.length === 0 ? (
          <EmptyState
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
          <EmptyState
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
          <EmptyState
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
        ) : visibleTokens.length === 0 ? (
          <EmptyState
            title={t("common.noSearchResults")}
            description={t("tokens.noSearchResults")}
            action={
              <Button
                variant="outline"
                size="sm"
                onClick={() => setQuery("")}
                type="button"
              >
                {t("common.clearSearch")}
              </Button>
            }
          />
        ) : (
          <Panel>
            {visibleTokens.map((token) => {
              const isCopying = copyingID === token.id;
              const isCopied = copiedID === token.id;
              const usage = usageByToken[token.id];
              const usageStatus =
                usage?.status ?? (isReady ? "loading" : "error");
              return (
                <DataRow
                  asChild
                  className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-5 gap-y-3 py-4 @[720px]:grid-cols-[minmax(0,1fr)_minmax(160px,0.6fr)_auto]"
                  key={token.id}
                >
                  <article data-testid="access-token-row">
                    <div className="flex min-w-0 items-start gap-3">
                      <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-text-secondary">
                        <KeyRound aria-hidden="true" className="size-4" />
                      </span>
                      <div className="grid min-w-0 gap-1">
                        <strong
                          className="truncate text-sm font-semibold"
                          title={token.name}
                        >
                          {token.name}
                        </strong>
                        <code
                          className="truncate font-mono text-xs text-muted-foreground"
                          title={token.hint}
                        >
                          {token.hint}
                        </code>
                        <span className="text-micro text-muted-foreground tabular-nums">
                          {t("tokens.createdAt")} ·{" "}
                          {createdAtLabel(token.created_at)}
                        </span>
                      </div>
                    </div>
                    <div className="order-3 col-span-2 grid grid-cols-2 gap-5 pl-12 @[720px]:order-none @[720px]:col-span-1 @[720px]:pl-0">
                      <DataField
                        label={t("tokens.todayTokens")}
                        value={
                          <CompactCount
                            placeholder={tokenCountPlaceholder(
                              usage?.today ?? null,
                              usageStatus,
                            )}
                            value={usage?.today?.total_tokens}
                          />
                        }
                      />
                      <DataField
                        label={t("tokens.lifetimeTokens")}
                        value={
                          <CompactCount
                            placeholder={tokenCountPlaceholder(
                              usage?.lifetime ?? null,
                              usageStatus,
                            )}
                            value={usage?.lifetime?.total_tokens}
                          />
                        }
                      />
                    </div>
                    <ActionGroup className="col-span-2 shrink-0 gap-1 @[560px]:col-span-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={
                          !isReady ||
                          catalog.status !== "ready" ||
                          catalog.stale ||
                          deletingID !== null ||
                          !inferenceURL
                        }
                        onClick={() => setImportToken(token)}
                        type="button"
                        aria-label={t("ccSwitch.importToken", {
                          name: token.name,
                        })}
                      >
                        <CCSwitchIcon size={16} />
                        CC Switch
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
                        {deletingID === token.id
                          ? t("tokens.deleting")
                          : t("common.delete")}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={
                          !isReady || deletingID !== null || copyingID !== null
                        }
                        onClick={() => void copyToken(token.id)}
                        type="button"
                      >
                        {isCopying ? (
                          <LoaderCircle
                            animateOnHover={false}
                            className="animate-spin motion-reduce:animate-none"
                          />
                        ) : isCopied ? (
                          <Check />
                        ) : (
                          <Copy />
                        )}
                        {isCopying
                          ? t("common.copying")
                          : isCopied
                            ? t("common.copied")
                            : t("common.copy")}
                      </Button>
                    </ActionGroup>
                  </article>
                </DataRow>
              );
            })}
          </Panel>
        )}
      </div>

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
            <Field htmlFor="access-token-name" label={t("tokens.name")}>
              <Input
                autoComplete="off"
                id="access-token-name"
                maxLength={64}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder={t("tokens.namePlaceholder")}
                ref={nameInput}
                value={name}
              />
            </Field>
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
      {importToken &&
        isReady &&
        catalog.status === "ready" &&
        !catalog.stale &&
        catalog.items.some((token) => token.id === importToken.id) && (
          <CCSwitchImportDialog
            key={`${coreSessionKey}:${inferenceURL}:${importToken.id}`}
            token={importToken}
            inferenceURL={inferenceURL}
            onClose={() => setImportToken(null)}
          />
        )}
      <ConfirmDialog
        cancelLabel={t("common.cancel")}
        confirmLabel={
          deletingID === pendingDelete?.id
            ? t("tokens.deleting")
            : t("tokens.confirmDelete")
        }
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
