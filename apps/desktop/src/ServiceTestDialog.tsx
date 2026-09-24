import { BatchModelTest } from "./BatchModelTest";
import { SegmentedControl } from "./components/SegmentedControl";
import { ServiceTestResultView } from "./ServiceTestResultView";
import { useId, useRef, useState } from "react";
import { testService } from "./bridge";
import type { Service } from "./service-model";
import {
  testableCapabilities,
  type ServiceTestProtocol,
  type ServiceTestResult,
} from "./service-test-model";
import { useT } from "./i18n";
import { Field } from "./components/Field";
import { FilterSelect } from "./components/FilterSelect";
import { ServiceKindIcon } from "./components/ServiceKindIcon";
import {
  Flask,
  LoaderCircle,
  RefreshCw,
  SlidersHorizontal,
} from "./components/icons";
import { Button } from "./components/ui/button";
import { ModelSelect } from "./components/ModelSelect";
import { Label } from "./components/ui/label";
import { Textarea } from "./components/ui/textarea";
import { Switch } from "./components/ui/switch";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/ui/popover";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";

interface TestSettingsProps {
  showModel?: boolean;
  service: Service;
  protocol: ServiceTestProtocol;
  model: string;
  prompt: string;
  stream: boolean;
  running: boolean;
  onModel: (value: string) => void;
  onProtocol: (value: string) => void;
  onPrompt: (value: string) => void;
  onStream: (value: boolean) => void;
}

function TestSettings({
  showModel = true,
  service,
  protocol,
  model,
  prompt,
  stream,
  running,
  onModel,
  onProtocol,
  onPrompt,
  onStream,
}: TestSettingsProps) {
  const t = useT();
  const id = useId();
  const capabilities = testableCapabilities(service);
  const capability = capabilities.find((item) => item.protocol === protocol);
  return (
    <div className="grid gap-4">
      {showModel ? (
        <Field label={t("serviceTest.model")} htmlFor={`${id}-model`}>
          <ModelSelect
            id={`${id}-model`}
            aria-label={t("serviceTest.model")}
            value={model}
            options={service.models}
            maxLength={256}
            placeholder={t("serviceTest.modelPlaceholder")}
            emptyMessage={t("serviceTest.noMatchingModels")}
            disabled={running}
            onValueChange={onModel}
          />
        </Field>
      ) : null}
      <Field label={t("serviceTest.protocol")}>
        <FilterSelect
          ariaLabel={t("serviceTest.protocol")}
          label=""
          className="w-full"
          disabled={running || capabilities.length === 0}
          value={protocol}
          options={capabilities.map((item) => ({
            value: item.protocol,
            label: item.protocol,
          }))}
          onChange={onProtocol}
        />
      </Field>
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={`${id}-stream`} className="text-xs text-text-secondary">
          {t("serviceTest.stream")}
        </Label>
        <Switch
          id={`${id}-stream`}
          checked={stream}
          disabled={
            running ||
            !capability?.streaming ||
            service.kind === "codex_subscription"
          }
          onCheckedChange={onStream}
          aria-label={t("serviceTest.stream")}
          size="sm"
        />
      </div>
      {service.kind === "codex_subscription" ? (
        <p className="-mt-2 text-xs text-muted-foreground">
          {t("serviceTest.streamRequired")}
        </p>
      ) : null}
      <Field
        label={t("serviceTest.prompt")}
        htmlFor={`${id}-prompt`}
        hint={t("serviceTest.defaultPrompt")}
      >
        <Textarea
          id={`${id}-prompt`}
          className="min-h-24 resize-none"
          rows={4}
          maxLength={2000}
          value={prompt}
          placeholder="Reply with OK."
          disabled={running}
          onChange={(event) => onPrompt(event.target.value)}
        />
      </Field>
    </div>
  );
}

export function ServiceTestDialog({
  service,
  onClose,
}: {
  service: Service;
  onClose: () => void;
}) {
  const t = useT();
  const capabilities = testableCapabilities(service);
  const [protocol, setProtocol] = useState<ServiceTestProtocol>(
    (capabilities[0]?.protocol ?? "openai.responses") as ServiceTestProtocol,
  );
  const [model, setModel] = useState(service.models[0] ?? "");
  const [stream, setStream] = useState(capabilities[0]?.streaming ?? false);
  const [prompt, setPrompt] = useState("");
  const [running, setRunning] = useState(false);
  const [mode, setMode] = useState<"single" | "batch">("single");
  const [batchRunning, setBatchRunning] = useState(false);
  const batchLocked = useRef(false);
  const busy = running || batchRunning;
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [result, setResult] = useState<ServiceTestResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);
  const capability = capabilities.find((item) => item.protocol === protocol);
  const connected =
    !service.subscription || service.subscription.status === "connected";
  const canTest = connected && capability && model.trim() && !running;
  const blocked = !connected
    ? t("serviceTest.notConnected")
    : !capability
      ? t("serviceTest.unsupported")
      : null;
  const settings: TestSettingsProps = {
    service,
    protocol,
    model,
    prompt,
    stream,
    running: busy,
    onModel: (value) => {
      setModel(value);
      setResult(null);
      setError(null);
    },
    onPrompt: (value) => {
      setPrompt(value);
      setResult(null);
      setError(null);
    },
    onStream: (value) => {
      setStream(value);
      setResult(null);
      setError(null);
    },
    onProtocol: (value) => {
      setProtocol(value as ServiceTestProtocol);
      setStream(
        capabilities.find((item) => item.protocol === value)?.streaming ??
          false,
      );
      setResult(null);
      setError(null);
    },
  };

  async function runTest() {
    if (!canTest || inFlight.current) return;
    inFlight.current = true;
    setSettingsOpen(false);
    setRunning(true);
    setResult(null);
    setError(null);
    try {
      setResult(
        await testService(service.id, {
          protocol,
          model: model.trim(),
          stream,
          ...(prompt.trim() ? { prompt: prompt.trim() } : {}),
        }),
      );
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("serviceTest.requestFailed"),
      );
    } finally {
      inFlight.current = false;
      setRunning(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !inFlight.current && !batchLocked.current) onClose();
      }}
    >
      <DialogContent
        variant="workspace"
        showCloseButton={!busy}
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => event.preventDefault()}
      >
        <DialogHeader className="flex-row items-center gap-3 border-b px-5 py-4 pr-12 text-left">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted">
            <ServiceKindIcon kind={service.kind} size={24} />
          </span>
          <div className="grid min-w-0 flex-1 gap-1.5">
            <DialogTitle className="truncate" title={service.name}>
              {t("serviceTest.title", { name: service.name })}
            </DialogTitle>
            <DialogDescription className="truncate text-xs">
              {t("serviceTest.subtitle")}
            </DialogDescription>
          </div>
          <SegmentedControl
            label={t("batchTest.mode")}
            disabled={busy}
            value={mode}
            options={[
              { value: "single", label: t("batchTest.single") },
              { value: "batch", label: t("batchTest.batch") },
            ]}
            onValueChange={(value) => {
              if (!inFlight.current && !batchLocked.current) setMode(value);
            }}
          />
        </DialogHeader>
        <div
          className={
            mode === "single" ? "flex min-h-0 flex-1 flex-col" : "hidden"
          }
        >
          <div className="flex min-h-0 flex-1">
            <aside
              aria-label={t("serviceTest.settings")}
              className="hidden w-60 shrink-0 flex-col gap-4 overflow-y-auto border-r bg-muted/30 p-5 min-[800px]:flex"
            >
              <span className="text-xs font-semibold">
                {t("serviceTest.settings")}
              </span>
              <TestSettings {...settings} />
              <p className="mt-auto pt-4 text-xs leading-relaxed text-muted-foreground">
                {t("serviceTest.savedConnection")}
              </p>
            </aside>
            <section
              aria-label={t("serviceTest.results")}
              data-testid="service-test-results"
              className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-4 min-[800px]:p-5"
            >
              <div className="flex shrink-0 items-center justify-between gap-3 min-[800px]:hidden">
                <div className="min-w-0 text-xs">
                  <p className="truncate font-medium" title={model}>
                    {model || t("serviceTest.modelPlaceholder")}
                  </p>
                  <p className="mt-0.5 truncate text-muted-foreground">
                    {protocol} ·{" "}
                    {t(stream ? "serviceTest.stream" : "serviceTest.nonStream")}
                  </p>
                </div>
                <Popover open={settingsOpen} onOpenChange={setSettingsOpen}>
                  <PopoverTrigger asChild>
                    <Button size="sm" variant="outline" disabled={running}>
                      <SlidersHorizontal aria-hidden="true" />
                      {t("serviceTest.settings")}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent className="z-110 w-80">
                    <TestSettings {...settings} />
                    <Button
                      className="mt-4 w-full"
                      onClick={() => setSettingsOpen(false)}
                    >
                      {t("serviceTest.doneSettings")}
                    </Button>
                  </PopoverContent>
                </Popover>
              </div>
              <ServiceTestResultView
                result={result}
                error={error}
                running={running}
                blocked={blocked}
                stream={stream}
              />
            </section>
          </div>
          <DialogFooter className="flex-row items-center justify-between border-t bg-muted/30 px-4 py-3 sm:justify-between min-[800px]:px-5">
            <span className="min-w-0 text-xs text-muted-foreground">
              {t("serviceTest.quotaNote")}
            </span>
            <div className="flex shrink-0 gap-2">
              <Button variant="outline" onClick={onClose} disabled={running}>
                {t("common.close")}
              </Button>
              <Button onClick={() => void runTest()} disabled={!canTest}>
                {running ? (
                  <LoaderCircle
                    aria-hidden="true"
                    className="animate-spin motion-reduce:animate-none"
                  />
                ) : result || error ? (
                  <RefreshCw aria-hidden="true" />
                ) : (
                  <Flask aria-hidden="true" />
                )}
                {t(
                  running
                    ? "serviceTest.testing"
                    : result || error
                      ? "serviceTest.rerun"
                      : "serviceTest.run",
                )}
              </Button>
            </div>
          </DialogFooter>
        </div>
        <div
          className={
            mode === "batch" ? "flex min-h-0 flex-1 flex-col" : "hidden"
          }
        >
          <BatchModelTest
            service={service}
            input={{
              protocol,
              stream,
              ...(prompt.trim() ? { prompt: prompt.trim() } : {}),
            }}
            blocked={blocked}
            settings={<TestSettings {...settings} showModel={false} />}
            onClose={onClose}
            onBusyChange={(value) => {
              batchLocked.current = value;
              setBatchRunning(value);
            }}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}
