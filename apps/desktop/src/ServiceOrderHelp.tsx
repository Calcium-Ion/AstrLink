import { useEffect, useRef, useState, type ReactNode } from "react";
import { useReducedMotion } from "motion/react";

import { HelpPopover } from "./components/HelpPopover";
import { IconButton } from "./components/IconButton";
import { ReorderPreview } from "./components/ReorderPreview";
import { ServiceKindIcon } from "./components/ServiceKindIcon";
import { RotateCcw } from "./components/icons";
import { Button } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { useT } from "./i18n";

export const SERVICE_ORDER_GUIDE_KEY = "astrlink.service-order-guide.v1";

const frames = [
  { at: 0, order: ["codex", "openai", "newapi"], lifted: null, step: "grab" },
  {
    at: 900,
    order: ["codex", "openai", "newapi"],
    lifted: "newapi",
    step: "grab",
  },
  {
    at: 1600,
    order: ["newapi", "codex", "openai"],
    lifted: "newapi",
    step: "raise",
  },
  {
    at: 2700,
    order: ["newapi", "codex", "openai"],
    lifted: null,
    step: "raise",
  },
  {
    at: 3700,
    order: ["newapi", "codex", "openai"],
    lifted: "codex",
    step: "lower",
  },
  {
    at: 4400,
    order: ["newapi", "openai", "codex"],
    lifted: "codex",
    step: "lower",
  },
  {
    at: 5500,
    order: ["newapi", "openai", "codex"],
    lifted: null,
    step: "done",
  },
] as const;

function OrderAnimation() {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (reducedMotion) return;
    const timers = frames
      .slice(1)
      .map((item, index) =>
        window.setTimeout(() => setFrame(index + 1), item.at),
      );
    return () => timers.forEach(window.clearTimeout);
  }, [reducedMotion]);
  const current = frames[reducedMotion ? frames.length - 1 : frame];
  const items = [
    {
      id: "codex",
      name: t("services.codexName"),
      icon: <ServiceKindIcon kind="codex_subscription" />,
    },
    { id: "openai", name: "OpenAI", icon: <ServiceKindIcon kind="openai" /> },
    { id: "newapi", name: "New API", icon: <ServiceKindIcon kind="newapi" /> },
  ];
  return (
    <div className="min-w-0" data-order-guide-step={current.step}>
      <ReorderPreview
        items={items}
        order={[...current.order]}
        lifted={current.lifted}
        firstLabel={t("services.orderGuide.first")}
        label={t("services.orderGuide.examples")}
      />
      <p className="mt-3 min-h-15 text-sm leading-5" role="status">
        {t(`services.orderGuide.${current.step}`)}
      </p>
    </div>
  );
}

export function ServiceOrderHelp({
  ready,
  children,
}: {
  ready: boolean;
  children: ReactNode;
}) {
  const t = useT();
  const [helpOpen, setHelpOpen] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const [playback, setPlayback] = useState(0);
  const clicks = useRef(0);
  const checked = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const dismiss = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!ready || checked.current) return;
    checked.current = true;
    try {
      if (localStorage.getItem(SERVICE_ORDER_GUIDE_KEY) === "seen") return;
      localStorage.setItem(SERVICE_ORDER_GUIDE_KEY, "seen");
    } catch {
      // Storage can be unavailable in a WebView; the guide must still work.
    }
    setGuideOpen(true);
  }, [ready]);

  return (
    <>
      <HelpPopover
        label={t("services.orderLabel")}
        open={helpOpen}
        onOpenChange={setHelpOpen}
        triggerRef={trigger}
        onTriggerClick={(event) => {
          clicks.current += 1;
          if (clicks.current < 5) return;
          event.preventDefault();
          clicks.current = 0;
          setHelpOpen(false);
          setGuideOpen(true);
        }}
      >
        {children}
      </HelpPopover>
      <Dialog open={guideOpen} onOpenChange={setGuideOpen}>
        <DialogContent
          className="gap-3 sm:max-w-md"
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            dismiss.current?.focus({ preventScroll: true });
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            trigger.current?.focus({ preventScroll: true });
          }}
        >
          <DialogHeader className="pr-6 text-left">
            <DialogTitle>{t("services.orderGuide.title")}</DialogTitle>
            <DialogDescription>
              {t("services.orderGuide.description")}
            </DialogDescription>
          </DialogHeader>
          {guideOpen ? <OrderAnimation key={playback} /> : null}
          <p className="text-xs leading-5 text-muted-foreground">
            {t("services.orderGuide.note")}
          </p>
          <DialogFooter className="flex-row items-center justify-between sm:justify-between">
            <IconButton
              label={t("services.orderGuide.replay")}
              onClick={() => setPlayback((value) => value + 1)}
              variant="ghost"
            >
              <RotateCcw aria-hidden="true" />
            </IconButton>
            <Button ref={dismiss} onClick={() => setGuideOpen(false)}>
              {t("services.orderGuide.dismiss")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
