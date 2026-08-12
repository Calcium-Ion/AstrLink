import {
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { FormMessage } from "@/components/FormMessage";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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

interface RevealedToken {
  tokenId: string;
  value: string;
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
  const [revealingID, setRevealingID] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<RevealedToken | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [copyNotice, setCopyNotice] = useState<string | null>(null);
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
    setRevealingID(null);
    setRevealed(null);
    setError(null);
    setNotice(null);
    setCopyNotice(null);
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
      setRevealed({
        tokenId: result.token.id,
        value: result.access_token,
      });
      setCreateOpen(false);
      setName("");
      setNotice(`已创建“${result.token.name}”，请复制并妥善保存。`);
    } catch (requestError) {
      if (sessionGeneration.current === generation) {
        setError(messageOf(requestError, "无法创建访问令牌。"));
      }
    } finally {
      if (sessionGeneration.current === generation) setCreating(false);
    }
  };

  const toggleReveal = async (tokenId: string) => {
    if (revealed?.tokenId === tokenId) {
      revealGeneration.current += 1;
      setRevealed(null);
      setRevealingID(null);
      setCopyNotice(null);
      return;
    }
    if (!isReady) return;

    const generation = revealGeneration.current + 1;
    const session = sessionGeneration.current;
    revealGeneration.current = generation;
    setRevealed(null);
    setRevealingID(tokenId);
    setError(null);
    setCopyNotice(null);
    try {
      const result = await revealAccessToken(tokenId);
      if (
        sessionGeneration.current === session &&
        revealGeneration.current === generation
      ) {
        setRevealed({ tokenId, value: result.access_token });
      }
    } catch (requestError) {
      if (
        sessionGeneration.current === session &&
        revealGeneration.current === generation
      ) {
        setError(messageOf(requestError, "无法显示访问令牌。"));
      }
    } finally {
      if (
        sessionGeneration.current === session &&
        revealGeneration.current === generation
      ) {
        setRevealingID(null);
      }
    }
  };

  const copyRevealed = async () => {
    if (!revealed) return;
    try {
      await navigator.clipboard.writeText(revealed.value);
      setCopyNotice("令牌已复制");
    } catch {
      setCopyNotice(null);
      setError("无法自动复制，请手动选择令牌。");
    }
  };

  const refresh = () => {
    revealGeneration.current += 1;
    setRevealed(null);
    setRevealingID(null);
    setCopyNotice(null);
    onRefresh();
  };

  const remove = async () => {
    if (pendingDelete === null || deletingID !== null) return;
    const token = pendingDelete;
    const generation = sessionGeneration.current;
    revealGeneration.current += 1;
    setRevealed(null);
    setRevealingID(null);
    setCopyNotice(null);
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
    deletingID !== null ||
    revealingID !== null;

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
              创建令牌
            </Button>
            <Button
              variant="outline"
              disabled={!isReady || catalogBusy}
              onClick={refresh}
              type="button"
            >
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

      <Card className="min-h-0 flex-1 gap-0 overflow-hidden py-0 shadow-[var(--shadow-card)]">
        <div className="flex items-center justify-between border-b px-[18px] py-3.5">
          <div>
            <strong className="text-[12.5px]">访问令牌</strong>
            <span className="ml-2 text-[10px] text-muted-foreground">
              {catalog.status === "blocked" && catalog.items.length === 0
                ? "—"
                : `${catalog.items.length} 个`}
            </span>
          </div>
          <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">统计待接入</Badge>
        </div>

        <ScrollArea
          aria-busy={catalog.status === "loading"}
          aria-label="访问令牌列表"
          className="min-h-0 flex-1"
        >
          <div className="grid content-start">
          {catalog.status === "blocked" && catalog.items.length === 0 ? (
            <div className="grid min-h-[250px] place-items-center p-8 text-center text-[11px] text-muted-foreground">Core 就绪后将读取访问令牌。</div>
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="grid gap-2 p-4" aria-label="正在加载访问令牌">
              <span className="h-16 animate-pulse rounded-xl bg-muted" />
              <span className="h-16 animate-pulse rounded-xl bg-muted" />
              <span className="h-16 animate-pulse rounded-xl bg-muted" />
            </div>
          ) : catalog.status === "error" && catalog.items.length === 0 ? (
            <div className="flex min-h-[250px] flex-col items-center justify-center gap-3 p-8 text-center text-[11px] text-muted-foreground">
              <p className="text-xs text-foreground">暂时无法显示访问令牌。</p>
              <Button
                variant="outline"
                disabled={!isReady}
                onClick={refresh}
                type="button"
              >
                重试
              </Button>
            </div>
          ) : catalog.items.length === 0 ? (
            <div className="flex min-h-[250px] flex-col items-center justify-center gap-2 p-8 text-center text-[11px] text-muted-foreground">
              <p className="text-xs font-semibold text-foreground">还没有访问令牌。</p>
              <span>创建一个令牌即可连接本机客户端。</span>
              <Button
                className="mt-2"
                disabled={!isReady}
                onClick={() => setCreateOpen(true)}
                type="button"
              >
                创建令牌
              </Button>
            </div>
          ) : (
            catalog.items.map((token) => {
              const isRevealed = revealed?.tokenId === token.id;
              const isRevealing = revealingID === token.id;
              return (
                <article
                  className="grid grid-cols-[minmax(160px,1fr)_minmax(260px,1.5fr)_auto] items-center gap-4 border-b px-[18px] py-3.5 last:border-b-0 max-[900px]:grid-cols-1"
                  data-testid="access-token-row"
                  key={token.id}
                >
                  <div className="grid min-w-0 gap-1.5">
                    <span>
                      <strong className="text-xs">{token.name}</strong>
                      {token.source === "system_default" ? (
                        <Badge className="ml-2 px-1.5 py-0 text-[8px]" variant="secondary">默认</Badge>
                      ) : null}
                    </span>
                    <code className="overflow-hidden text-[10px] text-text-secondary text-ellipsis whitespace-nowrap">{token.hint}</code>
                  </div>
                  <dl className="grid grid-cols-3 gap-3 max-[600px]:grid-cols-1">
                    <div className="grid gap-1">
                      <dt className="text-[9px] text-muted-foreground">今日 Token</dt>
                      <dd className="text-[10.5px] font-semibold">—</dd>
                    </div>
                    <div className="grid gap-1">
                      <dt className="text-[9px] text-muted-foreground">累计 Token</dt>
                      <dd className="text-[10.5px] font-semibold">—</dd>
                    </div>
                    <div className="grid gap-1">
                      <dt className="text-[9px] text-muted-foreground">创建时间</dt>
                      <dd className="text-[10.5px] font-semibold">{createdAtLabel(token.created_at)}</dd>
                    </div>
                  </dl>
                  <div className="flex items-center justify-end gap-1 max-[900px]:justify-start">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={!isReady || deletingID !== null}
                      onClick={() => void toggleReveal(token.id)}
                      type="button"
                    >
                      {isRevealing ? "读取中…" : isRevealed ? "隐藏" : "显示"}
                    </Button>
                    <Button
                      className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
                      disabled={!isReady || deletingID !== null}
                      onClick={() => {
                        revealGeneration.current += 1;
                        setRevealed(null);
                        setRevealingID(null);
                        setCopyNotice(null);
                        setPendingDelete(token);
                        setError(null);
                        setNotice(null);
                      }}
                      type="button"
                      size="sm"
                      variant="ghost"
                    >
                      {deletingID === token.id ? "删除中…" : "删除"}
                    </Button>
                  </div>
                  {isRevealed ? (
                    <div className="col-span-full flex min-w-0 items-center gap-2 rounded-lg bg-accent px-3 py-2 max-[600px]:flex-wrap" data-testid="revealed-access-token">
                      <code className="min-w-0 flex-1 overflow-auto text-[10px] text-accent-foreground select-all">{revealed.value}</code>
                      <Button size="sm" variant="outline" onClick={() => void copyRevealed()} type="button">
                        复制
                      </Button>
                      {copyNotice ? (
                        <span className="text-[9px] text-success-foreground" role="status">{copyNotice}</span>
                      ) : null}
                    </div>
                  ) : null}
                </article>
              );
            })
          )}
          </div>
        </ScrollArea>
      </Card>

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
            <DialogTitle>创建访问令牌</DialogTitle>
            <DialogDescription>用客户端名称标记用途，创建后可随时在列表中显示。</DialogDescription>
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
