import { useEffect, useRef, useState, type ReactNode } from "react";
import { testService } from "./bridge";
import { useT } from "./i18n";
import type { Service } from "./service-model";
import type { ServiceTestInput } from "./service-test-model";
import { MAX_BATCH_MODELS, runModelTestBatch, type ModelTestRow } from "./service-test-batch";
import { ServiceTestResultView } from "./ServiceTestResultView";
import { EmptyState } from "./components/EmptyState";
import { FilterSelect } from "./components/FilterSelect";
import { FormMessage } from "./components/FormMessage";
import { ListToolbar } from "./components/ListToolbar";
import { Panel } from "./components/Panel";
import { StatusBadge } from "./components/StatusBadge";
import { ArrowLeft, Plus, SlidersHorizontal } from "./components/icons";
import { Button } from "./components/ui/button";
import { Checkbox } from "./components/ui/checkbox";
import { DialogFooter } from "./components/ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "./components/ui/popover";
import { Progress } from "./components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./components/ui/table";

export function BatchModelTest({ service, input, blocked, settings, onClose, onBusyChange }: {
  service: Service;
  input: Omit<ServiceTestInput, "model">;
  blocked: string | null;
  settings: ReactNode;
  onClose: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const t = useT();
  const [customModels, setCustomModels] = useState<string[]>([]);
  const [selected, setSelected] = useState(() => new Set(service.models.slice(0, MAX_BATCH_MODELS)));
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<Record<string, ModelTestRow>>({});
  const [running, setRunning] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [concurrency, setConcurrency] = useState(2);
  const [detail, setDetail] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const stopRequested = useRef(false);
  const locked = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; stopRequested.current = true; };
  }, []);
  const models = [...new Set([...service.models, ...customModels])];
  const visible = models.filter(model => model.toLowerCase().includes(query.toLowerCase().trim()));
  const entries = Object.values(rows);
  const completed = entries.filter(row => row.state === "success" || row.state === "failed");
  const failed = entries.filter(row => row.state === "failed").map(row => row.model);
  const succeeded = entries.filter(row => row.state === "success").length;
  const stopped = entries.filter(row => row.state === "stopped").length;
  const progress = entries.length ? 100 * (completed.length + stopped) / entries.length : 0;
  const current = detail && Object.hasOwn(rows, detail) ? rows[detail] : undefined;
  const formatTime = (value?: number | null) => value == null ? "—" : `${(value / 1000).toFixed(2)} s`;
  const allVisibleSelected = visible.length > 0 && visible.every(model => selected.has(model));
  const anyVisibleSelected = visible.some(model => selected.has(model));
  const newModel = query.trim();
  const canAdd = newModel !== "" && new TextEncoder().encode(newModel).length <= 256 && !/\p{Cc}/u.test(newModel) && !models.includes(newModel);

  function toggleModel(model: string, checked: boolean) {
    setSelected(previous => {
      const next = new Set(previous);
      if (checked && next.size < MAX_BATCH_MODELS) next.add(model);
      if (!checked) next.delete(model);
      return next;
    });
  }

  async function start(targets: string[], retry = false) {
    if (locked.current || blocked || targets.length === 0) return;
    locked.current = true;
    stopRequested.current = false;
    setStopping(false);
    setError(null);
    setDetail(null);
    setSettingsOpen(false);
    setRunning(true);
    onBusyChange(true);
    const queued = Object.fromEntries(targets.map(model => [model, { model, state: "queued" as const }]));
    setRows(previous => retry ? { ...previous, ...queued } : queued);
    try {
      await runModelTestBatch({ models: targets, input, concurrency,
        shouldStop: () => stopRequested.current,
        onUpdate: row => { if (mounted.current) setRows(previous => ({ ...previous, [row.model]: row })); },
        test: payload => testService(service.id, payload),
      });
    } catch (cause) {
      if (mounted.current) setError(cause instanceof Error ? cause.message : t("serviceTest.requestFailed"));
    } finally {
      locked.current = false;
      if (mounted.current) { setRunning(false); setStopping(false); onBusyChange(false); }
    }
  }

  return <>
    <div className="@container flex min-h-0 flex-1 flex-col gap-2 p-4 min-[800px]:p-5" data-testid="batch-model-tests">
      {detail ? <>
        <ServiceTestResultView result={current?.result} error={current?.error} running={current?.state === "running"} stream={current?.result?.stream ?? input.stream}
          leading={<>
            <Button size="icon-sm" variant="ghost" aria-label={t("batchTest.back")} title={t("batchTest.back")} onClick={() => setDetail(null)}><ArrowLeft aria-hidden="true" /></Button>
            <span className="truncate text-sm font-medium" title={detail}>{detail}</span>
          </>} />
      </> : <>
        <ListToolbar title={t("batchTest.models")} count={models.length} query={query} onQueryChange={setQuery}
          searchLabel={t("batchTest.search")} placeholder={t("batchTest.search")} clearLabel={t("common.clearSearch")}
          filters={<span className="truncate text-xs font-medium" title={t("batchTest.selected", { count: selected.size, limit: MAX_BATCH_MODELS })}>{t("batchTest.selectedShort", { count: selected.size, total: models.length })}</span>}
          actions={<>
            {canAdd ? <Button size="sm" variant="outline" disabled={running || selected.size >= MAX_BATCH_MODELS} onClick={() => { setCustomModels(previous => [...previous, newModel]); toggleModel(newModel, true); }}><Plus aria-hidden="true" />{t("batchTest.add")}</Button> : null}
            <Button size="sm" variant="ghost" disabled={running || selected.size === 0} onClick={() => setSelected(new Set())}>{t("batchTest.clear")}</Button>
            <Popover open={settingsOpen} onOpenChange={setSettingsOpen}>
            <PopoverTrigger asChild><Button size="sm" variant="outline" disabled={running}><SlidersHorizontal aria-hidden="true" />{t("serviceTest.settings")}</Button></PopoverTrigger>
            <PopoverContent className="z-110 grid max-h-[var(--radix-popover-content-available-height)] w-80 gap-4 overflow-y-auto">
              <p className="text-xs text-muted-foreground">{t("batchTest.selected", { count: selected.size, limit: MAX_BATCH_MODELS })}</p>
              {settings}
              <FilterSelect ariaLabel={t("batchTest.concurrency")} label={t("batchTest.concurrency")} value={String(concurrency)} disabled={running}
                options={[1, 2, 3].map(value => ({ value: String(value), label: String(value) }))} onChange={value => setConcurrency(Number(value))} />
              <Button onClick={() => setSettingsOpen(false)}>{t("serviceTest.doneSettings")}</Button>
            </PopoverContent>
          </Popover></>} />
        {blocked || error ? <FormMessage tone="error">{blocked ?? error}</FormMessage> : null}
        {entries.length ? <div className="grid shrink-0 gap-2" aria-live="polite">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span>{t(stopping ? "batchTest.stopping" : running ? "batchTest.progress" : "batchTest.finished", { done: completed.length, total: entries.length })}</span>
            <span className="text-success-foreground">{t("batchTest.successCount", { count: succeeded })}</span>
            <span className="text-danger-foreground">{t("batchTest.failedCount", { count: failed.length })}</span>
            {stopped > 0 ? <span className="text-muted-foreground">{t("batchTest.stoppedCount", { count: stopped })}</span> : null}
          </div>
          <Progress className="h-1" value={progress} aria-label={t("batchTest.progressLabel")} />
        </div> : null}
        <Panel className="flex min-h-0 flex-1 flex-col">
          {visible.length ? <Table containerClassName="min-h-0 flex-1 overflow-auto overscroll-contain" aria-label={t("batchTest.models")}>
            <TableHeader className="sticky top-0 z-10 bg-card"><TableRow>
              <TableHead className="w-10"><Checkbox aria-label={t("batchTest.selectAll")} disabled={running}
                checked={allVisibleSelected ? true : anyVisibleSelected ? "indeterminate" : false}
                onCheckedChange={checked => setSelected(previous => {
                  const next = new Set(previous);
                  for (const model of visible) {
                    if (!checked) next.delete(model);
                    else if (next.size < MAX_BATCH_MODELS) next.add(model);
                  }
                  return next;
                })} /></TableHead>
              <TableHead>{t("serviceTest.model")}</TableHead><TableHead>{t("batchTest.status")}</TableHead>
              <TableHead>{t("serviceTest.firstToken")}</TableHead><TableHead>{t("serviceTest.total")}</TableHead><TableHead className="w-16">{t("batchTest.response")}</TableHead>
            </TableRow></TableHeader>
            <TableBody>{visible.map(model => {
              const row = Object.hasOwn(rows, model) ? rows[model] : undefined;
              const tone = row?.state === "success" ? "positive" : row?.state === "failed" ? "negative" : row?.state === "running" ? "pending" : "neutral";
              return <TableRow key={model}>
                <TableCell><Checkbox aria-label={t("batchTest.selectModel", { model })} checked={selected.has(model)} disabled={running || (!selected.has(model) && selected.size >= MAX_BATCH_MODELS)} onCheckedChange={checked => toggleModel(model, checked === true)} /></TableCell>
                <TableCell className="max-w-64"><span className="block truncate text-xs font-medium" title={model}>{model}</span></TableCell>
                <TableCell><StatusBadge tone={tone}>{t(`batchTest.state.${row?.state ?? "ready"}`)}</StatusBadge></TableCell>
                <TableCell className="text-xs tabular-nums">{formatTime(row?.result?.first_token_ms)}</TableCell>
                <TableCell className="text-xs tabular-nums">{formatTime(row?.result?.duration_ms)}</TableCell>
                <TableCell><Button size="sm" variant="ghost" disabled={!row?.result && !row?.error} aria-label={t("batchTest.detailsNamed", { model })} onClick={() => setDetail(model)}>{t("batchTest.details")}</Button></TableCell>
              </TableRow>;
            })}</TableBody>
          </Table> : <EmptyState className="h-full border-0" title={t("common.noSearchResults")} description={t("batchTest.empty")} />}
        </Panel>
      </>}
    </div>
    <DialogFooter className="flex-row flex-wrap items-center justify-between border-t bg-muted/30 px-4 py-3 sm:justify-between">
      <span className="text-xs text-muted-foreground">{t("serviceTest.quotaNote")}</span>
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="outline" onClick={onClose} disabled={running}>{t("common.close")}</Button>
        {running ? <Button variant="outline" disabled={stopping} onClick={() => { stopRequested.current = true; setStopping(true); }}>{t(stopping ? "batchTest.stoppingShort" : "batchTest.stop")}</Button> : <>
          {failed.length ? <Button variant="outline" disabled={!!blocked} onClick={() => void start(failed, true)}>{t("batchTest.retry", { count: failed.length })}</Button> : null}
          <Button disabled={!!blocked || selected.size === 0} onClick={() => void start(models.filter(model => selected.has(model)))}>{t("batchTest.start", { count: selected.size })}</Button>
        </>}
      </div>
    </DialogFooter>
  </>;
}
