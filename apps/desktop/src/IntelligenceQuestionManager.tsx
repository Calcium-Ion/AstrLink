import { useEffect, useState } from "react";
import { listServices } from "./bridge";
import type { Service } from "./service-model";
import {
  intelligence,
  type IntelligenceSettings,
  type IntelligenceQuestion,
} from "./intelligence-bridge";
import { readReferenceImage } from "./lib/question-image";
import { Button } from "./components/ui/button";
import { Checkbox } from "./components/ui/checkbox";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Field } from "./components/Field";
import { FilterSelect } from "./components/FilterSelect";
import { ModelSelect } from "./components/ModelSelect";

export function IntelligenceQuestionManager({
  onDirtyChange,
}: {
  onDirtyChange: (dirty: boolean) => void;
}) {
  const [settings, setSettings] = useState<IntelligenceSettings>();
  const [saved, setSaved] = useState<IntelligenceSettings>();
  const dirty = JSON.stringify(settings) !== JSON.stringify(saved);
  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);
  const [services, setServices] = useState<Service[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  useEffect(() => {
    let alive = true;
    void Promise.all([
      intelligence<IntelligenceSettings>("settings"),
      listServices(),
    ])
      .then(([next, page]) => {
        if (alive) {
          setSettings(next);
          setSaved(next);
          setServices(page.items);
        }
      })
      .catch((error) => {
        if (alive) setError(String(error));
      });
    return () => {
      alive = false;
    };
  }, []);
  const action = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await fn();
    } catch (error) {
      setError(String(error));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section
      aria-label="智力题管理"
      className="min-h-0 flex-1 overflow-y-auto p-1"
      data-intelligence-primary
    >
      {error && (
        <p role="alert" className="mb-3 text-sm text-destructive">
          {error}
        </p>
      )}
      {message && (
        <p role="status" className="mb-3 text-sm text-muted-foreground">
          {message}
        </p>
      )}
      {settings ? (
        <IntelligenceQuestionEditor
          settings={settings}
          setSettings={setSettings}
          services={services}
          busy={busy}
          action={action}
          setMessage={setMessage}
          onSaved={setSaved}
        />
      ) : (
        !error && <p className="text-sm text-muted-foreground">正在加载题库…</p>
      )}
    </section>
  );
}

export function IntelligenceQuestionEditor({
  settings,
  setSettings,
  services,
  busy,
  action,
  setMessage,
  onSaved,
}: {
  settings: IntelligenceSettings;
  setSettings: (settings: IntelligenceSettings) => void;
  services: Service[];
  busy: boolean;
  action: (fn: () => Promise<void>) => Promise<void>;
  setMessage: (message: string) => void;
  onSaved?: (settings: IntelligenceSettings) => void;
}) {
  const [selected, setSelected] = useState(settings.questions[0]?.id ?? "");
  const question = settings.questions.find((q) => q.id === selected);
  const judge = services.find((s) => s.id === settings.judge_service_id);
  const toggle = (ids: string[], id: string) =>
    ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id];
  const updateQuestion = (patch: Partial<IntelligenceQuestion>) => {
    if (question)
      setSettings({
        ...settings,
        questions: settings.questions.map((q) =>
          q.id === question.id ? { ...q, ...patch } : q,
        ),
      });
  };
  return (
    <fieldset disabled={busy} className="@container grid min-w-0 gap-3">
      <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b bg-background pb-3">
        <FilterSelect
          ariaLabel="编辑题目"
          className="h-8 w-full @min-[480px]:w-64"
          label=""
          value={selected}
          onChange={setSelected}
          options={settings.questions.map((q) => ({
            value: q.id,
            label: q.name,
          }))}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={settings.questions.length >= 30}
          onClick={() => {
            const id = crypto.randomUUID().replaceAll("-", "");
            setSettings({
              ...settings,
              questions: [
                ...settings.questions,
                {
                  id,
                  name: "新题目",
                  kind: "text",
                  prompt: "",
                  answer: "",
                  model: "",
                },
              ],
            });
            setSelected(id);
          }}
        >
          新增题目
        </Button>
        <Button
          type="button"
          size="sm"
          className="ml-auto"
          onClick={() =>
            void action(async () => {
              await intelligence("save_settings", undefined, settings);
              onSaved?.(settings);
              setMessage("题库和默认设置已保存");
            })
          }
        >
          保存题库设置
        </Button>
      </div>
      <details className="rounded-lg border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          默认模型与图片判定设置
        </summary>
        <div className="mt-3 grid items-start gap-4 @min-[640px]:grid-cols-2">
          <div className="grid min-w-0 gap-3">
            <Field label="默认答题模型">
              <Input
                aria-label="默认答题模型"
                value={settings.default_model}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    default_model: e.target.value,
                  })
                }
              />
            </Field>
            <Field label="每次调用超时（秒）">
              <Input
                aria-label="调用超时"
                type="number"
                min={1}
                max={300}
                value={settings.timeout_seconds}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    timeout_seconds: Number(e.target.value),
                  })
                }
              />
            </Field>
          </div>
          <div className="grid min-w-0 gap-3">
            <Field label="图片判分渠道">
              <FilterSelect
                ariaLabel="图片判分渠道"
                className="h-8 w-full"
                label=""
                value={settings.judge_service_id}
                options={[
                  { value: "", label: "未指定" },
                  ...services.map((s) => ({
                    value: s.id,
                    label: s.name,
                  })),
                ]}
                onChange={(id) =>
                  setSettings({
                    ...settings,
                    judge_service_id: id,
                    judge_model: "",
                  })
                }
              />
            </Field>
            <Field label="图片判分模型">
              <ModelSelect
                aria-label="图片判分模型"
                value={settings.judge_model}
                options={judge?.models ?? []}
                onValueChange={(model) =>
                  setSettings({ ...settings, judge_model: model })
                }
              />
            </Field>
          </div>
        </div>
      </details>
      {question && (
        <div className="grid gap-3">
          <div className="grid gap-3 @min-[640px]:grid-cols-2">
            <Field label="题目名称">
              <Input
                aria-label="题目名称"
                value={question.name}
                onChange={(e) => updateQuestion({ name: e.target.value })}
              />
            </Field>
            <Field label="题型">
              <FilterSelect
                ariaLabel="题型"
                className="h-8 w-full"
                label=""
                value={question.kind}
                onChange={(kind) =>
                  updateQuestion({
                    kind: kind as IntelligenceQuestion["kind"],
                  })
                }
                options={[
                  { value: "text", label: "问答（文本答案）" },
                  { value: "number", label: "问答（数字答案）" },
                  { value: "svg", label: "SVG 画图" },
                ]}
              />
            </Field>
          </div>
          {question.kind === "svg" && (
            <Field
              label="对比参考图（可选）"
              hint="上传正常示例供判分模型对比；不上传则由模型直接判断。支持 PNG、JPEG、WebP，最大 8 MB。"
            >
              <Input
                aria-label="上传参考图"
                type="file"
                accept="image/png,image/jpeg,image/webp"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file)
                    void action(async () =>
                      updateQuestion({
                        reference_png: await readReferenceImage(file),
                      }),
                    );
                }}
              />
              {question.reference_png && (
                <div className="mt-2 flex items-start gap-2">
                  <img
                    src={question.reference_png}
                    alt="正常参考图"
                    className="max-h-32 max-w-48 rounded border"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => updateQuestion({ reference_png: "" })}
                  >
                    移除参考图
                  </Button>
                </div>
              )}
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={settings.default_question_ids.includes(question.id)}
              onCheckedChange={() =>
                setSettings({
                  ...settings,
                  default_question_ids: toggle(
                    settings.default_question_ids,
                    question.id,
                  ),
                })
              }
            />
            作为默认题目
          </label>
          <Field label="发给模型的题目">
            <Textarea
              aria-label="题目内容"
              rows={5}
              maxLength={1800}
              value={question.prompt}
              onChange={(e) => updateQuestion({ prompt: e.target.value })}
            />
          </Field>
          <Field label={question.kind === "svg" ? "画面判定要求" : "标准答案"}>
            <Textarea
              aria-label="标准答案"
              rows={2}
              maxLength={600}
              value={question.answer}
              onChange={(e) => updateQuestion({ answer: e.target.value })}
            />
          </Field>
          <Field
            label="题目专用模型"
            hint="没有渠道专用模型时生效；留空使用默认答题模型。"
          >
            <Input
              aria-label="题目专用模型"
              value={question.model}
              onChange={(e) => updateQuestion({ model: e.target.value })}
            />
          </Field>
        </div>
      )}
    </fieldset>
  );
}
