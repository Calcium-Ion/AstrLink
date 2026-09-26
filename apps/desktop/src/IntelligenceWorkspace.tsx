import { useEffect, useState, type ReactNode } from "react";
import type { Service } from "./service-model";
import {
  intelligence,
  intelligenceStatus,
  runSummary,
  type IntelligenceSettings,
  type IntelligenceChannel,
  type IntelligenceQuestion,
  type IntelligenceRun,
} from "./intelligence-bridge";
import { IntelligenceQuestionEditor } from "./IntelligenceQuestionManager";
import {
  subscribeIntelligence,
  watchIntelligenceRun,
} from "./intelligence-background";
import { IconButton } from "./components/IconButton";
import { Brain } from "./components/icons";
import { notify } from "./notify";
import { Button } from "./components/ui/button";
import { Checkbox } from "./components/ui/checkbox";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import { Field } from "./components/Field";
import { FilterSelect } from "./components/FilterSelect";
import { ModelSelect } from "./components/ModelSelect";
import { ConfirmDialog } from "./components/ConfirmDialog";

function QuestionChoice({
  question,
  checked,
  onChange,
}: {
  question: IntelligenceQuestion;
  checked: boolean;
  onChange: () => void;
}) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <Checkbox checked={checked} onCheckedChange={onChange} />
      {question.name}
    </label>
  );
}
const toggle = (ids: string[], id: string) =>
  ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id];

function useLatestIntelligence(serviceId: string) {
  const [latest, setLatest] = useState<IntelligenceRun>();
  useEffect(() => subscribeIntelligence(serviceId, setLatest), [serviceId]);
  useEffect(() => {
    let alive = true;
    void intelligence<IntelligenceRun[]>("history", serviceId)
      .then((history) => {
        if (alive) setLatest(history[0]);
        for (const run of history) watchIntelligenceRun(run);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [serviceId]);
  return [latest, setLatest] as const;
}

export function IntelligenceStartButton({
  service,
  disabled,
}: {
  service: Service;
  disabled: boolean;
}) {
  const [latest, setLatest] = useLatestIntelligence(service.id);
  const [starting, setStarting] = useState(false);
  const start = async () => {
    setStarting(true);
    try {
      const run = await intelligence<IntelligenceRun>("start", service.id, {
        mode: "all",
        count: 1,
      });
      setLatest(run);
      watchIntelligenceRun(run);
      notify.success(`${service.name}：智力测试已在后台开始`);
    } catch (error) {
      notify.error(String(error));
    } finally {
      setStarting(false);
    }
  };
  return (
    <IconButton
      label={`智力测试 · ${service.name}`}
      disabled={disabled || starting || latest?.status === "running"}
      onClick={() => void start()}
    >
      <Brain aria-hidden="true" />
    </IconButton>
  );
}

export function IntelligenceResult({
  service,
  services,
}: {
  service: Service;
  services: Service[];
}) {
  const [latest, setLatest] = useLatestIntelligence(service.id);
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="link"
        className="h-auto p-0 text-xs"
        onClick={() => setOpen(true)}
        aria-label={`智力结果 · ${service.name}`}
      >
        {latest ? runSummary(latest) : "未测试"}
      </Button>
      {open && (
        <IntelligenceWorkspace
          service={service}
          services={services}
          onClose={() => setOpen(false)}
          onResult={setLatest}
          resultsOnly
        />
      )}
    </>
  );
}

function IntelligenceFrame({
  embedded,
  serviceName,
  onClose,
  children,
}: {
  embedded: boolean;
  serviceName: string;
  onClose: () => void;
  children: ReactNode;
}) {
  if (embedded)
    return (
      <section
        className="flex min-h-0 flex-1 flex-col overflow-hidden"
        aria-label="智力测试设置"
      >
        {children}
      </section>
    );
  return (
    <Dialog
      open
      onOpenChange={(value) => {
        if (!value) onClose();
      }}
    >
      <DialogContent
        variant="workspace"
        className="max-h-[calc(100dvh-4rem)]"
        aria-describedby="intelligence-description"
      >
        <div className="flex shrink-0 items-center gap-3 border-b px-4 py-3 pr-12">
          <DialogTitle>智力检测 · {serviceName}</DialogTitle>
          <DialogDescription id="intelligence-description" className="sr-only">
            查看后台测试进度、答题结果与图片判定
          </DialogDescription>
        </div>
        {children}
      </DialogContent>
    </Dialog>
  );
}
export function IntelligenceWorkspace({
  service,
  services,
  onClose,
  onResult,
  embedded = false,
  resultsOnly = false,
}: {
  service: Service;
  services: Service[];
  onClose: () => void;
  onResult: (run: IntelligenceRun) => void;
  embedded?: boolean;
  resultsOnly?: boolean;
}) {
  const [settings, setSettings] = useState<IntelligenceSettings>();
  const [channel, setChannel] = useState<IntelligenceChannel>();
  const [history, setHistory] = useState<IntelligenceRun[]>([]);
  const [run, setRun] = useState<IntelligenceRun>();
  const [tab, setTab] = useState(embedded ? "channel" : "results");
  const [mode, setMode] = useState("all");
  const [count, setCount] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [confirm, setConfirm] = useState<"" | "cancel">("");
  const [now, setNow] = useState(Date.now);
  const active = run?.status === "running";
  const locked = busy || active;
  const action = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    let alive = true;
    void Promise.all([
      intelligence<IntelligenceSettings>("settings"),
      intelligence<IntelligenceChannel>("channel", service.id),
      intelligence<IntelligenceRun[]>("history", service.id),
    ])
      .then(([s, c, h]) => {
        if (!alive) return;
        setSettings(s);
        setChannel(c);
        setHistory(h);
        if (h[0])
          void intelligence<IntelligenceRun>(
            "run",
            undefined,
            undefined,
            h[0].id,
          )
            .then((r) => {
              if (alive) setRun(r);
              watchIntelligenceRun(r);
            })
            .catch((e) => {
              if (alive) setError(String(e));
            });
      })
      .catch((e) => {
        if (alive) setError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [service.id]);
  useEffect(() => {
    let alive = true;
    const unsubscribe = subscribeIntelligence(service.id, (next) => {
      setRun(next);
      onResult(next);
      if (next.status !== "running") {
        void intelligence<IntelligenceRun[]>("history", service.id)
          .then((h) => {
            if (alive) setHistory(h);
          })
          .catch((e) => {
            if (alive) setError(String(e));
          });
      }
    });
    return () => {
      alive = false;
      unsubscribe();
    };
  }, [service.id, onResult]);
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 100);
    return () => clearInterval(timer);
  }, [active]);
  const close = onClose;
  const start = () =>
    action(async () => {
      if (!channel) return;
      await intelligence("save_channel", service.id, channel);
      const next = await intelligence<IntelligenceRun>("start", service.id, {
        mode,
        count,
      });
      setRun(next);
      watchIntelligenceRun(next);
      onResult(next);
      setTab("results");
    });
  return (
    <>
      <IntelligenceFrame
        embedded={embedded}
        serviceName={service.name}
        onClose={close}
      >
        <Tabs
          value={tab}
          onValueChange={setTab}
          className="flex min-h-0 flex-1 flex-col gap-0 overflow-hidden"
        >
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b px-4 py-2">
            <TabsList>
              <TabsTrigger value="results">测试结果</TabsTrigger>
              {!resultsOnly && (
                <>
                  <TabsTrigger value="channel">当前渠道</TabsTrigger>
                  <TabsTrigger value="questions">题库与默认设置</TabsTrigger>
                </>
              )}
            </TabsList>
            <div className="flex items-center gap-2">
              {active && !embedded && (
                <Button size="sm" variant="ghost" onClick={onClose}>
                  后台运行
                </Button>
              )}
              <FilterSelect
                ariaLabel="选题方式"
                label=""
                value={mode}
                onChange={setMode}
                disabled={locked}
                options={[
                  { value: "all", label: "全部测试" },
                  { value: "random", label: "随机选题" },
                ]}
              />
              {mode === "random" && (
                <Input
                  aria-label="随机题数"
                  type="number"
                  min={1}
                  max={30}
                  className="w-16"
                  value={count}
                  onChange={(e) => setCount(Number(e.target.value))}
                  disabled={locked}
                />
              )}
              {active ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setConfirm("cancel")}
                >
                  停止
                </Button>
              ) : (
                <Button
                  type="button"
                  size="sm"
                  disabled={busy || !channel}
                  onClick={() => void start()}
                >
                  开始测试
                </Button>
              )}
            </div>
          </div>
          {error && (
            <p
              role="alert"
              className="shrink-0 px-4 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
          {message && (
            <p
              role="status"
              className="shrink-0 px-4 py-2 text-sm text-muted-foreground"
            >
              {message}
            </p>
          )}
          <TabsContent
            value="results"
            className="m-0 min-h-0 flex-1 overflow-y-auto p-4"
            data-intelligence-primary
          >
            <div className="mb-3 flex flex-wrap items-center gap-3">
              <FilterSelect
                ariaLabel="测试历史"
                label=""
                value={run?.id ?? ""}
                disabled={!!active}
                options={[
                  { value: "", label: "选择历史记录" },
                  ...history.map((r) => ({
                    value: r.id,
                    label: `${new Date(r.started_at).toLocaleString()} · ${runSummary(r)}`,
                  })),
                ]}
                onChange={(id) => {
                  if (id)
                    void action(async () =>
                      setRun(
                        await intelligence<IntelligenceRun>(
                          "run",
                          undefined,
                          undefined,
                          id,
                        ),
                      ),
                    );
                }}
              />
              {run && (
                <span className="text-sm text-muted-foreground">
                  {runSummary(run)}
                </span>
              )}
            </div>
            {!run && (
              <p className="text-sm text-muted-foreground">
                点击开始测试。渠道未选题时，使用题库中的默认题目。
              </p>
            )}
            <div className="space-y-3">
              {run?.items.map((item) => (
                <article
                  key={item.question.id}
                  className="rounded-lg border p-3"
                >
                  <div className="flex flex-wrap justify-between gap-2 text-sm font-medium">
                    <span>{item.question.name}</span>
                    <span>
                      {intelligenceStatus[item.status] ?? item.status} ·{" "}
                      {(
                        (item.started_at &&
                        ["generating", "rendering", "judging"].includes(
                          item.status,
                        )
                          ? Math.max(0, now - Date.parse(item.started_at))
                          : item.duration_ms) / 1000
                      ).toFixed(1)}
                      s
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    答题模型：{item.model}
                    {item.question.kind === "svg" && run.judge_model
                      ? ` · 判图模型：${run.judge_model}`
                      : ""}
                  </p>
                  {item.reason && <p className="mt-2 text-sm">{item.reason}</p>}
                  {item.png && (
                    <img
                      src={item.png}
                      alt={item.question.name}
                      className="mt-3 max-h-80 max-w-full rounded border"
                    />
                  )}
                  {item.output && (
                    <details className="mt-2 text-sm">
                      <summary className="cursor-pointer">模型原始回答</summary>
                      <pre className="mt-2 whitespace-pre-wrap break-words font-mono text-xs">
                        {item.output}
                      </pre>
                    </details>
                  )}
                  {item.judge_output && (
                    <details className="mt-2 text-sm">
                      <summary className="cursor-pointer">图片判定原文</summary>
                      <pre className="mt-2 whitespace-pre-wrap break-words text-xs">
                        {item.judge_output}
                      </pre>
                    </details>
                  )}
                </article>
              ))}
            </div>
          </TabsContent>
          <TabsContent
            value="channel"
            className="m-0 min-h-0 flex-1 overflow-y-auto p-4"
            data-intelligence-primary
          >
            {channel && settings && (
              <fieldset disabled={locked} className="grid max-w-2xl gap-4">
                <Field
                  label="本渠道测试模型"
                  hint="选中的模型用于本渠道所有题目；留空时按下面的默认设置处理。"
                >
                  <ModelSelect
                    aria-label="本渠道测试模型"
                    value={channel.model}
                    options={service.models}
                    placeholder="没有指定模型"
                    onValueChange={(model) => setChannel({ ...channel, model })}
                  />
                </Field>
                <label className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={channel.use_default}
                    onCheckedChange={(v) =>
                      setChannel({ ...channel, use_default: v === true })
                    }
                  />
                  未指定时，使用题目模型或默认模型（
                  {settings.default_model || "未设置"}）
                </label>
                <p className="text-xs text-muted-foreground">
                  默认模型在本渠道不可用时，提示“没有指定模型”。
                </p>
                <Field label="本渠道指定题目" hint="不勾选时使用默认题目。">
                  <div className="grid gap-3">
                    {settings.questions.map((q) => (
                      <QuestionChoice
                        key={q.id}
                        question={q}
                        checked={channel.question_ids.includes(q.id)}
                        onChange={() =>
                          setChannel({
                            ...channel,
                            question_ids: toggle(channel.question_ids, q.id),
                          })
                        }
                      />
                    ))}
                  </div>
                </Field>
                <Button
                  type="button"
                  className="w-fit"
                  size="sm"
                  onClick={() =>
                    void action(async () => {
                      await intelligence("save_channel", service.id, channel);
                      setMessage("渠道设置已保存");
                    })
                  }
                >
                  保存渠道设置
                </Button>
              </fieldset>
            )}
          </TabsContent>
          <TabsContent
            value="questions"
            className="m-0 min-h-0 flex-1 overflow-y-auto p-4"
            data-intelligence-primary
          >
            {settings && (
              <IntelligenceQuestionEditor
                settings={settings}
                setSettings={setSettings}
                services={services}
                busy={locked}
                action={action}
                setMessage={setMessage}
              />
            )}
          </TabsContent>
        </Tabs>
      </IntelligenceFrame>
      <ConfirmDialog
        open={!!confirm}
        title="停止当前检测？"
        description="已完成的回答会保留在历史记录中。"
        confirmLabel="停止检测"
        onCancel={() => setConfirm("")}
        onConfirm={() =>
          void action(async () => {
            if (run) await intelligence("cancel", undefined, undefined, run.id);
            setConfirm("");
          })
        }
      />
    </>
  );
}
