import {
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  createAccessToken,
  deleteAccessToken,
  revealAccessToken,
} from "./bridge";
import type { AccessTokenSummary } from "./access-token-model";

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

  const remove = async (token: AccessTokenSummary) => {
    const lastToken = catalog.items.length === 1;
    const warning = lastToken
      ? `“${token.name}”是最后一个访问令牌。删除后，所有客户端都将无法连接，直到创建新令牌。\n\n确定永久删除吗？`
      : `删除“${token.name}”后，使用它的客户端将立即无法连接。此操作无法撤销。\n\n确定删除吗？`;
    if (!window.confirm(warning)) return;

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
    <section className="token-manager" aria-labelledby="token-manager-heading">
      <div className="token-manager__header">
        <div>
          <span className="section-kicker">本地接入</span>
          <h2 id="token-manager-heading">管理访问令牌</h2>
          <p>为 IDE、CLI 或其他本机客户端分配独立令牌。</p>
        </div>
        <div className="token-manager__actions">
          <button
            className="btn-primary"
            disabled={!isReady || catalogBusy}
            onClick={() => {
              setCreateOpen(true);
              setError(null);
            }}
            type="button"
          >
            创建令牌
          </button>
          <button
            className="btn-secondary"
            disabled={!isReady || catalogBusy}
            onClick={refresh}
            type="button"
          >
            {catalog.status === "loading" ? "刷新中…" : "刷新"}
          </button>
        </div>
      </div>

      {(!isReady || catalog.status === "blocked") && (
        <div className="endpoint-manager__unavailable">
          {catalog.items.length
            ? "Core 尚未就绪，当前显示上次读取的令牌。"
            : "Core 就绪后才能管理访问令牌。"}
        </div>
      )}
      {catalog.status === "error" && catalog.error ? (
        <div className="form-message form-message--error" role="alert">
          {catalog.error}
        </div>
      ) : null}
      {error ? (
        <div className="form-message form-message--error" role="alert">
          {error}
        </div>
      ) : null}
      {notice ? (
        <div className="form-message form-message--notice" role="status">
          {notice}
        </div>
      ) : null}

      <div className="token-list">
        <div className="token-list__title">
          <div>
            <strong>访问令牌</strong>
            <span>
              {catalog.status === "blocked" && catalog.items.length === 0
                ? "—"
                : `${catalog.items.length} 个`}
            </span>
          </div>
          <span className="usage-pending-badge">统计待接入</span>
        </div>

        <div
          aria-busy={catalog.status === "loading"}
          aria-label="访问令牌列表"
          className="token-list__scroll"
        >
          {catalog.status === "blocked" && catalog.items.length === 0 ? (
            <div className="token-list__empty">Core 就绪后将读取访问令牌。</div>
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="token-list__skeleton" aria-label="正在加载访问令牌">
              <span />
              <span />
              <span />
            </div>
          ) : catalog.status === "error" && catalog.items.length === 0 ? (
            <div className="token-list__empty">
              <p>暂时无法显示访问令牌。</p>
              <button
                className="btn-secondary"
                disabled={!isReady}
                onClick={refresh}
                type="button"
              >
                重试
              </button>
            </div>
          ) : catalog.items.length === 0 ? (
            <div className="token-list__empty">
              <p>还没有访问令牌。</p>
              <span>创建一个令牌即可连接本机客户端。</span>
              <button
                className="btn-primary"
                disabled={!isReady}
                onClick={() => setCreateOpen(true)}
                type="button"
              >
                创建令牌
              </button>
            </div>
          ) : (
            catalog.items.map((token) => {
              const isRevealed = revealed?.tokenId === token.id;
              const isRevealing = revealingID === token.id;
              return (
                <article className="token-row" key={token.id}>
                  <div className="token-row__identity">
                    <span>
                      <strong>{token.name}</strong>
                      {token.source === "system_default" ? (
                        <small>默认</small>
                      ) : null}
                    </span>
                    <code>{token.hint}</code>
                  </div>
                  <dl className="token-row__metrics">
                    <div>
                      <dt>今日 Token</dt>
                      <dd>—</dd>
                    </div>
                    <div>
                      <dt>累计 Token</dt>
                      <dd>—</dd>
                    </div>
                    <div>
                      <dt>创建时间</dt>
                      <dd>{createdAtLabel(token.created_at)}</dd>
                    </div>
                  </dl>
                  <div className="token-row__actions">
                    <button
                      disabled={!isReady || deletingID !== null}
                      onClick={() => void toggleReveal(token.id)}
                      type="button"
                    >
                      {isRevealing ? "读取中…" : isRevealed ? "隐藏" : "显示"}
                    </button>
                    <button
                      className="danger-link"
                      disabled={!isReady || deletingID !== null}
                      onClick={() => void remove(token)}
                      type="button"
                    >
                      {deletingID === token.id ? "删除中…" : "删除"}
                    </button>
                  </div>
                  {isRevealed ? (
                    <div className="token-row__secret">
                      <code>{revealed.value}</code>
                      <button onClick={() => void copyRevealed()} type="button">
                        复制
                      </button>
                      {copyNotice ? (
                        <span role="status">{copyNotice}</span>
                      ) : null}
                    </div>
                  ) : null}
                </article>
              );
            })
          )}
        </div>
      </div>

      {createOpen ? (
        <div
          className="token-dialog-backdrop"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target && !creating) {
              setCreateOpen(false);
              setName("");
            }
          }}
        >
          <section
            aria-labelledby="create-token-heading"
            aria-modal="true"
            className="token-dialog"
            role="dialog"
          >
            <h3 id="create-token-heading">创建访问令牌</h3>
            <p>用客户端名称标记用途，创建后可随时在列表中显示。</p>
            <form onSubmit={(event) => void submitCreate(event)}>
              <label htmlFor="access-token-name">令牌名称</label>
              <input
                autoComplete="off"
                id="access-token-name"
                maxLength={64}
                onChange={(event) => setName(event.currentTarget.value)}
                placeholder="例如：VS Code"
                ref={nameInput}
                value={name}
              />
              <div className="token-dialog__actions">
                <button
                  className="btn-secondary"
                  disabled={creating}
                  onClick={() => {
                    setCreateOpen(false);
                    setName("");
                  }}
                  type="button"
                >
                  取消
                </button>
                <button className="btn-primary" disabled={creating} type="submit">
                  {creating ? "创建中…" : "创建"}
                </button>
              </div>
            </form>
          </section>
        </div>
      ) : null}
    </section>
  );
}
