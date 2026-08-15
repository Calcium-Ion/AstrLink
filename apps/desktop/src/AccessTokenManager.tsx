import {
  type FormEvent,
  type ReactNode,
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
import { Badge } from "@/components/ui/badge";
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
  revealAccessToken,
} from "./bridge";
import type { AccessTokenSummary } from "./access-token-model";
import { PageHeader } from "./PageHeader";

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

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function createdAtLabel(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未知";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
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
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [deletingID, setDeletingID] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] =
    useState<AccessTokenSummary | null>(null);
  const [copyingID, setCopyingID] = useState<string | null>(null);
  const [copiedID, setCopiedID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const sessionGeneration = useRef(0);
  const revealGeneration = useRef(0);
  const nameInput = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    sessionGeneration.current += 1;
    revealGeneration.current += 1;
    setCreateOpen(false);
    setName("");
    setCreating(false);
    setDeletingID(null);
    setPendingDelete(null);
    setCopyingID(null);
    setCopiedID(null);
    setError(null);
    setNotice(null);
  }, [coreSessionKey]);

  useEffect(() => {
    if (createOpen) nameInput.current?.focus();
  }, [createOpen]);

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError("请输入令牌名称。");
      nameInput.current?.focus();
      return;
    }
    if ([...trimmedName].length > 64) {
      setError("令牌名称最多 64 个字符。");
      nameInput.current?.focus();
      return;
    }
    if (!isReady || creating) return;

    const generation = sessionGeneration.current;
    setCreating(true);
    setError(null);
    setNotice(null);
    try {
      const result = await createAccessToken(trimmedName);
      if (sessionGeneration.current !== generation) return;
      onTokenCreated(result.token);
      setCopiedID(null);
      setCreateOpen(false);
      setName("");
      setNotice(`已创建“${result.token.name}”。`);
    } catch (requestError) {
      if (sessionGeneration.current === generation) {
        setError(messageOf(requestError, "无法创建访问令牌。"));
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
      setError("无法自动复制，请手动选择令牌。");
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
        setError(messageOf(requestError, "无法复制访问令牌。"));
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
    setNotice(null);
    try {
      await deleteAccessToken(token.id);
      if (sessionGeneration.current !== generation) return;
      onTokenDeleted(token.id);
      setPendingDelete(null);
      setNotice(`已删除“${token.name}”。`);
    } catch (requestError) {
      if (sessionGeneration.current === generation) {
        setError(messageOf(requestError, "无法删除访问令牌。"));
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
              创建令牌
            </Button>
            <Button
              variant="outline"
              disabled={!isReady || catalogBusy}
              onClick={refresh}
              type="button"
            >
              <RefreshCw className={catalog.status === "loading" ? "animate-spin motion-reduce:animate-none" : undefined} />
              {catalog.status === "loading" ? "刷新中…" : "刷新"}
            </Button>
          </>
        }
        description="为 IDE、CLI 或其他本机客户端分配独立令牌。"
        eyebrow="本地接入"
        title="管理访问令牌"
        titleId="token-manager-heading"
      />

      {(!isReady || catalog.status === "blocked") && (
        <FormMessage className="mb-3" tone="notice">
          {catalog.items.length
            ? "Core 尚未就绪，当前显示上次读取的令牌。"
            : "Core 就绪后才能管理访问令牌。"}
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
      {notice ? (
        <FormMessage className="mb-3" tone="success">
          {notice}
        </FormMessage>
      ) : null}

      <ScrollArea
        aria-busy={catalog.status === "loading"}
        aria-label="访问令牌列表"
        className="min-h-0"
      >
        <div className="grid content-start gap-2">
          {catalog.status === "blocked" && catalog.items.length === 0 ? (
            <TokenBoardState
              description="Core 就绪后将读取访问令牌。"
              title="等待本地网关"
            />
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="grid gap-2" aria-label="正在加载访问令牌">
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
                  重试
                </Button>
              }
              description="请检查 Core 连接后重试。"
              title="暂时无法显示访问令牌。"
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
                  创建令牌
                </Button>
              }
              description="创建一个令牌即可连接本机客户端。"
              title="还没有访问令牌。"
            />
          ) : (
            catalog.items.map((token) => {
              const isCopying = copyingID === token.id;
              const isCopied = copiedID === token.id;
              return (
                <article
                  className="min-w-0 rounded-md border bg-card"
                  data-testid="access-token-row"
                  key={token.id}
                >
                  <div className="flex min-w-0 items-center gap-2 px-3.5 py-3 max-[560px]:flex-wrap">
                    <StatusDot
                      tone={token.source === "system_default" ? "neutral" : "positive"}
                    />
                    <strong className="truncate text-sm font-medium">{token.name}</strong>
                    {token.source === "system_default" ? (
                      <Badge variant="outline">默认</Badge>
                    ) : null}
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
                      {isCopying ? "复制中…" : isCopied ? "已复制" : "复制"}
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
                        setNotice(null);
                      }}
                      type="button"
                      size="sm"
                      variant="ghost"
                    >
                      <Trash2 />
                      {deletingID === token.id ? "删除中…" : "删除"}
                    </Button>
                    </div>
                  </div>
                  <dl className="grid grid-cols-3 gap-3 border-t px-3.5 py-2 max-[560px]:grid-cols-1">
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        今日 Token
                      </dt>
                      <dd className="mt-0.5 text-xs tabular-nums">—</dd>
                    </div>
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        累计 Token
                      </dt>
                      <dd className="mt-0.5 text-xs tabular-nums">—</dd>
                    </div>
                    <div className="min-w-0">
                      <dt className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                        创建时间
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
            <DialogTitle>创建访问令牌</DialogTitle>
            <DialogDescription>用客户端名称标记用途，创建后可随时从列表复制。</DialogDescription>
          </DialogHeader>
            <form onSubmit={(event) => void submitCreate(event)}>
              <div className="grid gap-2">
              <Label htmlFor="access-token-name">令牌名称</Label>
              <Input
                autoComplete="off"
                id="access-token-name"
                maxLength={64}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder="例如：VS Code"
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
                  取消
                </Button>
                <Button disabled={creating} type="submit">
                  {creating ? "创建中…" : "创建"}
                </Button>
              </DialogFooter>
            </form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        cancelLabel="取消"
        confirmLabel={deletingID === pendingDelete?.id ? "删除中…" : "确认删除"}
        description={
          <>
            <p>
              {catalog.items.length === 1
                ? `“${pendingDelete?.name ?? ""}”是最后一个访问令牌。删除后，所有客户端都将无法连接，直到创建新令牌。`
                : `删除“${pendingDelete?.name ?? ""}”后，使用它的客户端将立即无法连接。`}
            </p>
            <p>此操作无法撤销。</p>
          </>
        }
        destructive
        disabled={deletingID !== null}
        onCancel={() => setPendingDelete(null)}
        onConfirm={() => void remove()}
        open={pendingDelete !== null}
        title="删除访问令牌？"
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
