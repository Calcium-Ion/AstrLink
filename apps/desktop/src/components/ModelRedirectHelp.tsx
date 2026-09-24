import { useEffect, useId, useState } from "react";
import { motion, useReducedMotion } from "motion/react";

import { astrlinkAutoModelId } from "@/failure-policy-model";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

import { GuideDialog } from "./GuideDialog";
import { HelpDisclosure } from "./HelpDisclosure";
import { ArrowLeft, ArrowRight } from "./icons";
import { Switch } from "./ui/switch";

const from = "gpt-4o";
const to = "gpt-5.5";
const timings = [900, 1900, 3300, 4300, 5400];

/** Keep both names visible after a replacement, so the change can be compared. */
function ModelField({
  label,
  model,
  previous,
  target = false,
}: {
  label: string;
  model: string | null;
  previous?: string;
  target?: boolean;
}) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  return (
    <div
      className={cn(
        "flex min-h-24 min-w-0 flex-col justify-center gap-1 rounded-md border px-3 py-2 transition-colors motion-reduce:transition-none",
        target && model
          ? "border-warning/40 bg-warning-wash"
          : "border-primary/20 bg-primary/5",
      )}
    >
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="min-h-4 font-mono text-xs text-muted-foreground">
        {previous ? <s>{previous}</s> : null}
      </span>
      <motion.span
        key={model ?? "pending"}
        initial={reducedMotion ? false : { opacity: 0, y: 6 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: reducedMotion ? 0 : 0.35 }}
        className={cn(
          "break-all font-mono text-lg font-semibold",
          !model && "font-sans text-sm font-normal text-muted-foreground",
        )}
      >
        {model ?? t("modelRedirect.guide.pending")}
      </motion.span>
    </div>
  );
}

function TransferArrow({
  returning = false,
  active,
  label,
}: {
  returning?: boolean;
  active: boolean;
  label: string;
}) {
  const reducedMotion = useReducedMotion();
  const Icon = returning ? ArrowLeft : ArrowRight;
  return (
    <div className="flex min-w-0 flex-col items-center justify-center gap-2 px-1 text-center">
      <span className="text-xs font-medium">{label}</span>
      <div
        aria-hidden="true"
        className="relative flex w-full items-center justify-center"
      >
        <span className="absolute inset-x-0 border-t border-dashed border-input" />
        <motion.span
          initial={false}
          animate={{ scaleX: active ? 1 : 0 }}
          transition={{ duration: reducedMotion ? 0 : 0.65 }}
          className={cn(
            "absolute inset-x-0 border-t-2 border-primary",
            returning ? "origin-right" : "origin-left",
          )}
        />
        <Icon
          animateOnHover={false}
          className={cn(
            "relative size-5 bg-card",
            active ? "text-primary" : "text-muted-foreground",
          )}
        />
      </div>
    </div>
  );
}

/** A local switch compares the same request with and without redirection. */
function RedirectAnimation({
  enabled,
  onEnabledChange,
}: {
  enabled: boolean;
  onEnabledChange: (enabled: boolean) => void;
}) {
  const t = useT();
  const id = useId();
  const reducedMotion = useReducedMotion();
  const [playing, setPlaying] = useState(true);
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (reducedMotion || !playing) return;
    const timers = timings.map((at, index) =>
      window.setTimeout(() => setFrame(index + 1), at),
    );
    return () => timers.forEach(window.clearTimeout);
  }, [reducedMotion, playing]);
  const step = reducedMotion || !playing ? 5 : frame;
  const actual = enabled ? to : from;

  return (
    <div className="grid min-w-0 gap-4" data-redirect-guide-step={step}>
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border px-3 py-2.5">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">
            {t("modelRedirect.guide.example")}
          </p>
          <p className="mt-1 flex flex-wrap items-center gap-2 font-mono text-sm font-medium">
            {from}
            <ArrowRight
              animateOnHover={false}
              aria-hidden="true"
              className="size-3.5"
            />
            {to}
          </p>
        </div>
        <label
          htmlFor={id}
          className="flex cursor-pointer items-center gap-2 text-xs"
        >
          {t(
            enabled
              ? "modelRedirect.guide.enabled"
              : "modelRedirect.guide.disabled",
          )}
          <Switch
            id={id}
            aria-label={t("modelRedirect.guide.toggle")}
            checked={enabled}
            onCheckedChange={(next) => {
              setPlaying(false);
              onEnabledChange(next);
            }}
          />
        </label>
      </div>
      <figure
        aria-label={t("modelRedirect.guide.flow")}
        className="m-0 grid min-w-0 grid-cols-[minmax(0,1fr)_4.5rem_minmax(0,1fr)] gap-y-3 sm:grid-cols-[minmax(0,1fr)_7rem_minmax(0,1fr)]"
      >
        <h3 className="text-center text-sm font-semibold">
          {t("modelRedirect.guide.client")}
        </h3>
        <span className="text-center text-xs text-muted-foreground">
          AstrLink
        </span>
        <h3 className="text-center text-sm font-semibold">
          {t("modelRedirect.guide.upstream")}
        </h3>
        <ModelField label={t("modelRedirect.guide.sent")} model={from} />
        <TransferArrow
          label={t(
            enabled
              ? "modelRedirect.guide.replace"
              : "modelRedirect.guide.unchanged",
          )}
          active={step >= 1}
        />
        <div data-redirect-model="upstream">
          <ModelField
            label={t("modelRedirect.guide.received")}
            model={step < 2 ? null : actual}
            previous={enabled && step >= 2 ? from : undefined}
            target={enabled && step >= 2}
          />
        </div>
        <div data-redirect-model="client">
          <ModelField
            label={t("modelRedirect.guide.returned")}
            model={step < 5 ? null : from}
            previous={enabled && step >= 5 ? to : undefined}
          />
        </div>
        <TransferArrow
          returning
          label={t(
            enabled
              ? "modelRedirect.guide.restore"
              : "modelRedirect.guide.unchanged",
          )}
          active={step >= 4}
        />
        <ModelField
          label={t("modelRedirect.guide.result")}
          model={step < 3 ? null : actual}
          target={enabled}
        />
      </figure>
      <div className="grid gap-1 border-l-2 border-primary pl-3" role="status">
        <p className="text-sm font-medium">
          {t(
            enabled
              ? "modelRedirect.guide.outcome"
              : "modelRedirect.guide.outcomeDisabled",
            { from, to },
          )}
        </p>
        <p className="text-xs text-muted-foreground">
          {t("modelRedirect.guide.tryToggle")}
        </p>
      </div>
    </div>
  );
}

export function ModelRedirectHelp() {
  const t = useT();
  const [enabled, setEnabled] = useState(true);
  return (
    <GuideDialog
      className="sm:max-w-xl"
      description={t("modelRedirect.guide.description")}
      dismissLabel={t("modelRedirect.guide.dismiss")}
      replayLabel={t("modelRedirect.guide.replay")}
      title={t("modelRedirect.guide.title")}
      triggerLabel={t("modelRedirect.help")}
    >
      <RedirectAnimation enabled={enabled} onEnabledChange={setEnabled} />
      <HelpDisclosure title={t("modelRedirect.guide.details")}>
        <ul className="m-0 grid list-disc gap-1.5 pl-4">
          <li>{t("modelRedirect.helpMatch")}</li>
          <li>{t("modelRedirect.helpSingleHop")}</li>
          <li>{t("modelRedirect.helpResponse")}</li>
          <li>{t("modelRedirect.helpDisabled")}</li>
          <li>{t("modelRedirect.helpAuto", { model: astrlinkAutoModelId })}</li>
        </ul>
      </HelpDisclosure>
    </GuideDialog>
  );
}
