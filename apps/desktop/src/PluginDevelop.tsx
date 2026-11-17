import { type ReactNode, useEffect, useRef, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import {
  CodeBlock,
  CodeBlockActions,
  CodeBlockContent,
  CodeBlockCopyButton,
  CodeBlockHeader,
  CodeBlockLine,
  CodeBlockTitle,
} from "@/components/ui/code-block";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { dryRunPlugin, getPluginJournal } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { formatClockTime, formatDateTime } from "./format-time";
import { useT } from "./i18n";
import { hookLabel } from "./plugin-labels";
import type {
  PluginDetail,
  PluginDryRunResult,
  PluginHookTiming,
  PluginJournal,
  PluginJournalEntry,
  PluginStatus,
} from "./plugin-model";

/** How often the open tab reads the plugin's new log lines. */
const JOURNAL_INTERVAL_MS = 2000;
/** How many log lines the tab keeps. */
const MAX_LINES = 400;

/**
 * Hooks a hand run makes sense for, the ones that change nothing first; the
 * rest need a request around them.
 */
const DRY_RUN_HOOKS = [
  "describeStatus",
  "testConnection",
  "prepareSession",
  "prepareTurn",
  "observeTurn",
  "onAction",
  "callTool",
];

/** A first argument to edit, by hook. */
const SAMPLE_SESSION = {
  protocol: "openai.chat",
  model: "gpt-4o",
  client: "unknown",
  meta: {},
  session: { id: "demo", turn: 1 },
  language: "zh-CN",
};
const SAMPLE_ARGS: Record<string, unknown[]> = {
  describeStatus: [{ language: "zh-CN" }],
  testConnection: [{ language: "zh-CN" }],
  prepareSession: [{ ...SAMPLE_SESSION, reason: "start" }],
  prepareTurn: [
    {
      protocol: "openai.chat",
      model: "gpt-4o",
      client: "unknown",
      stream: false,
      meta: {},
      session: { id: "demo", turn: 1 },
      language: "zh-CN",
      user: { text: "你好", typed: "你好" },
    },
  ],
};

const levelTone: Record<string, StatusTone> = {
  error: "negative",
  warn: "pending",
  info: "neutral",
  debug: "neutral",
};

/**
 * What a plugin did: its calls since the gateway started, then its log, the
 * newest first. Its days and, for whoever builds it, the timings of its
 * calls and a hand run open on demand.
 */
export function PluginRunLog({
  detail,
  status,
  statusError,
  days,
}: {
  detail: PluginDetail;
  status: PluginStatus | null;
  statusError: string | null;
  /** The plugin's own account of its days, when it keeps one. */
  days?: ReactNode;
}) {
  const t = useT();
  const [entries, setEntries] = useState<PluginJournalEntry[]>([]);
  const [timings, setTimings] = useState<PluginHookTiming[]>([]);
  const [breakers, setBreakers] = useState<PluginJournal["breakers"]>([]);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<"log" | "days" | "tools">("log");
  const since = useRef(0);

  useEffect(() => {
    let stopped = false;
    since.current = 0;
    setEntries([]);
    setTimings([]);
    const poll = async () => {
      try {
        const journal = await getPluginJournal(detail.key, since.current);
        if (stopped) return;
        since.current = journal.latest;
        setTimings(journal.timings);
        setBreakers(journal.breakers ?? []);
        if (journal.entries.length > 0) {
          setEntries((previous) =>
            [...previous, ...journal.entries].slice(-MAX_LINES),
          );
        }
        setError(null);
      } catch (cause) {
        if (!stopped) {
          setError(controlErrorMessage(cause, t("plugins.develop.loadFailed")));
        }
      }
    };
    void poll();
    const timer = window.setInterval(() => void poll(), JOURNAL_INTERVAL_MS);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [detail.key, t]);

  const views: Array<{ value: "log" | "days" | "tools"; label: string }> = [
    { value: "log", label: t("plugins.develop.log") },
    ...(days
      ? [{ value: "days" as const, label: t("plugins.runLog.days") }]
      : []),
    { value: "tools", label: t("plugins.runLog.tools") },
  ];
  return (
    <div className="grid min-w-0 gap-4" data-testid="plugin-develop">
      {detail.development ? <DevelopmentBuild detail={detail} /> : null}
      <RunNumbers status={status} statusError={statusError} />
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {breakers.map((breaker) => (
        <FormMessage
          data-testid="plugin-breaker"
          key={breaker.hook}
          tone="notice"
        >
          {t("plugins.develop.breaker", {
            point: hookLabel(breaker.hook, t),
            time: formatClockTime(breaker.until),
          })}
        </FormMessage>
      ))}
      <div className="flex min-h-7 min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
        <SegmentedControl
          label={t("plugins.runLog.views")}
          onValueChange={setView}
          options={views}
          value={view}
          variant="line"
        />
        {view === "log" ? (
          <HelpPopover label={t("plugins.develop.logHelpLabel")}>
            {t("plugins.develop.logHelp")}
          </HelpPopover>
        ) : null}
        <span className="flex-1" />
        {view === "log" && entries.length > 0 ? (
          <Button
            onClick={() => setEntries([])}
            size="xs"
            type="button"
            variant="ghost"
          >
            {t("plugins.develop.clear")}
          </Button>
        ) : null}
      </div>
      {view === "log" ? (
        <Log entries={entries} />
      ) : view === "days" ? (
        days
      ) : (
        <div className="grid min-w-0 gap-5">
          <Timings timings={timings} />
          <DryRun detail={detail} />
        </div>
      )}
    </div>
  );
}

/** The plugin's calls since the gateway started, and what went wrong. */
function RunNumbers({
  status,
  statusError,
}: {
  status: PluginStatus | null;
  statusError: string | null;
}) {
  const t = useT();
  if (statusError) return <FormMessage tone="error">{statusError}</FormMessage>;
  if (status === null) return <LoadingState label={t("plugins.loading")} />;
  const failed = Object.values(status.failures).reduce(
    (sum, count) => sum + count,
    0,
  );
  const failures = Object.entries(status.failures).filter(
    ([, count]) => count > 0,
  );
  const warn = (count: number) => (
    <span className={count > 0 ? "text-warning-foreground" : undefined}>
      {count}
    </span>
  );
  return (
    <Panel data-testid="plugin-status">
      <MetricGroup className="grid-cols-2 @min-[640px]/workspace-surface:grid-cols-4">
        <Metric
          detail={t("plugins.runLog.sinceStart")}
          label={t("plugins.status.calls")}
          size="sm"
          value={status.calls}
        />
        <Metric
          detail={
            failures.length > 0
              ? failures
                  .map(
                    ([reason, count]) =>
                      `${t(`plugins.status.reasons.${reason}`, {
                        defaultValue: reason,
                      })} ${count}`,
                  )
                  .join(" · ")
              : undefined
          }
          label={t("plugins.status.failures")}
          size="sm"
          value={warn(failed)}
        />
        <Metric
          detail={
            status.memory_limits > 0
              ? t("plugins.runLog.memoryLimits", {
                  count: status.memory_limits,
                })
              : undefined
          }
          label={t("plugins.status.timeouts")}
          size="sm"
          value={warn(status.timeouts)}
        />
        <Metric
          detail={t("plugins.status.networkDetail", {
            denied: status.network.denied,
            errors: status.network.errors,
          })}
          label={t("plugins.status.networkRequests")}
          size="sm"
          value={status.network.requests}
        />
      </MetricGroup>
    </Panel>
  );
}

/** Where a development build comes from and when it was built. */
function DevelopmentBuild({ detail }: { detail: PluginDetail }) {
  const t = useT();
  const development = detail.development!;
  const folder =
    development.project.split(/[\\/]/).filter(Boolean).pop() ??
    development.project;
  const builtAt = development.built_at
    ? formatClockTime(development.built_at)
    : null;
  return (
    <div
      className="flex flex-wrap items-center gap-2 text-xs"
      data-testid="plugin-development"
    >
      <StatusBadge tone="pending">{t("plugins.develop.build")}</StatusBadge>
      <span className="font-medium" title={development.project}>
        {folder}
      </span>
      {builtAt ? (
        <span className="text-muted-foreground">
          {t("plugins.develop.builtAt", { time: builtAt })}
        </span>
      ) : null}
    </div>
  );
}

function milliseconds(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(2)} s`;
  if (value >= 10) return `${Math.round(value)} ms`;
  return `${value.toFixed(1)} ms`;
}

function Timings({ timings }: { timings: PluginHookTiming[] }) {
  const t = useT();
  return (
    <section className="grid gap-2" data-testid="plugin-timings">
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.develop.timings")}
        </h3>
        <HelpPopover label={t("plugins.develop.timingsHelpLabel")}>
          {t("plugins.develop.timingsHelp")}
        </HelpPopover>
      </div>
      {timings.length === 0 ? (
        <EmptyState className="py-6" title={t("plugins.develop.noCalls")} />
      ) : (
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead>{t("plugins.develop.hook")}</TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.calls")}
              </TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.failures")}
              </TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.p50")}
              </TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.p95")}
              </TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.max")}
              </TableHead>
              <TableHead className="text-right">
                {t("plugins.develop.script")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {timings.map((timing) => (
              <TableRow key={timing.hook}>
                <TableCell className="font-mono">{timing.hook}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {timing.calls}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {timing.failures > 0 ? (
                    <span className="text-danger-foreground">
                      {timing.failures}
                    </span>
                  ) : (
                    0
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {milliseconds(timing.p50_ms)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {milliseconds(timing.p95_ms)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {milliseconds(timing.max_ms)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {milliseconds(timing.script_ms)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function sampleArgs(hook: string): string {
  return JSON.stringify(SAMPLE_ARGS[hook] ?? [], null, 2);
}

function DryRun({ detail }: { detail: PluginDetail }) {
  const t = useT();
  const hooks = DRY_RUN_HOOKS.filter((hook) => detail.hooks.includes(hook));
  const [hook, setHook] = useState(hooks[0] ?? "");
  const [args, setArgs] = useState(() => sampleArgs(hooks[0] ?? ""));
  const [running, setRunning] = useState(false);
  const [outcome, setOutcome] = useState<PluginDryRunResult | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  if (hooks.length === 0) return null;

  const run = async () => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(args);
    } catch {
      setProblem(t("plugins.develop.argsInvalid"));
      return;
    }
    if (!Array.isArray(parsed)) {
      setProblem(t("plugins.develop.argsInvalid"));
      return;
    }
    setProblem(null);
    setRunning(true);
    try {
      setOutcome(await dryRunPlugin(detail.key, hook, parsed));
    } catch (cause) {
      setOutcome(null);
      setProblem(controlErrorMessage(cause, t("plugins.develop.runFailed")));
    } finally {
      setRunning(false);
    }
  };

  return (
    <section className="grid gap-2" data-testid="plugin-dry-run">
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.develop.dryRun")}
        </h3>
        <HelpPopover label={t("plugins.develop.dryRunHelpLabel")}>
          {t("plugins.develop.dryRunHelp")}
        </HelpPopover>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Select
          onValueChange={(value) => {
            setHook(value);
            setArgs(sampleArgs(value));
            setOutcome(null);
          }}
          value={hook}
        >
          <SelectTrigger
            aria-label={t("plugins.develop.hook")}
            className="w-48 font-mono"
            size="sm"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {hooks.map((item) => (
              <SelectItem className="font-mono" key={item} value={item}>
                {item}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          disabled={running}
          onClick={() => void run()}
          size="sm"
          type="button"
        >
          {running ? t("plugins.develop.running") : t("plugins.develop.run")}
        </Button>
      </div>
      <Textarea
        aria-label={t("plugins.develop.args")}
        className="min-h-24 font-mono text-xs"
        onChange={(event) => setArgs(event.target.value)}
        spellCheck={false}
        value={args}
      />
      {problem ? <FormMessage tone="error">{problem}</FormMessage> : null}
      {outcome ? <DryRunOutcome outcome={outcome} /> : null}
    </section>
  );
}

function DryRunOutcome({ outcome }: { outcome: PluginDryRunResult }) {
  const t = useT();
  const timing = t("plugins.develop.took", {
    elapsed: milliseconds(outcome.elapsed_ms),
    script: milliseconds(outcome.script_ms),
  });
  const text = outcome.error
    ? [outcome.error.message, outcome.error.stack].filter(Boolean).join("\n\n")
    : outcome.result === undefined
      ? t("plugins.develop.noResult")
      : JSON.stringify(outcome.result, null, 2);
  return (
    <div className="grid gap-2" data-testid="plugin-dry-run-outcome">
      <CodeBlock size="sm" variant={outcome.error ? "destructive" : "default"}>
        <CodeBlockHeader>
          <CodeBlockTitle>
            {outcome.error
              ? t(`plugins.status.reasons.${outcome.error.reason}`, {
                  defaultValue: outcome.error.reason,
                })
              : t("plugins.develop.result")}
          </CodeBlockTitle>
          <CodeBlockActions>
            <span className="text-micro text-muted-foreground">{timing}</span>
            <CodeBlockCopyButton value={text} />
          </CodeBlockActions>
        </CodeBlockHeader>
        <CodeBlockContent wrap>
          {text.split("\n").map((line, index) => (
            <CodeBlockLine key={index}>{line}</CodeBlockLine>
          ))}
        </CodeBlockContent>
      </CodeBlock>
      {outcome.logs.length > 0 ? (
        <ul className="divide-y border-y">
          {outcome.logs.map((entry, index) => (
            <LogLine entry={entry} key={index} />
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function Log({ entries }: { entries: PluginJournalEntry[] }) {
  const t = useT();
  return entries.length === 0 ? (
    <div data-testid="plugin-log">
      <EmptyState className="py-6" title={t("plugins.develop.noLog")} />
    </div>
  ) : (
    <Panel asChild>
      <ul className="divide-y" data-testid="plugin-log">
        {[...entries].reverse().map((entry) => (
          <LogLine entry={entry} key={entry.seq} />
        ))}
      </ul>
    </Panel>
  );
}

function LogLine({ entry }: { entry: PluginJournalEntry }) {
  const t = useT();
  return (
    <li className="grid gap-1 px-3 py-2 text-xs">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <time
          className="text-muted-foreground tabular-nums"
          dateTime={entry.time}
          title={formatDateTime(entry.time)}
        >
          {formatClockTime(entry.time)}
        </time>
        <StatusBadge tone={levelTone[entry.level] ?? "neutral"}>
          {t(`plugins.develop.levels.${entry.level}`, {
            defaultValue: entry.level,
          })}
        </StatusBadge>
        <span className="font-mono text-muted-foreground">{entry.hook}</span>
        {entry.dry_run ? (
          <span className="text-muted-foreground">
            {t("plugins.develop.dryRunTag")}
          </span>
        ) : null}
      </div>
      <p className="break-words whitespace-pre-wrap">{entry.message}</p>
      {entry.stack ? (
        <details>
          <summary className="cursor-pointer text-muted-foreground">
            {t("plugins.develop.stack")}
          </summary>
          <pre className="mt-1 overflow-x-auto font-mono text-micro whitespace-pre-wrap">
            {entry.stack}
          </pre>
        </details>
      ) : null}
    </li>
  );
}
