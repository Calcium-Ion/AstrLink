import { motion, useReducedMotion } from "motion/react";

import { GuideDialog, useFirstVisitGuide } from "./components/GuideDialog";
import { PackageLogo } from "./components/PackageCard";
import { ServiceKindIcon } from "./components/ServiceKindIcon";
import { useT } from "./i18n";
import { cn } from "./lib/utils";
import { useLoopStep } from "./use-loop-step";

export const SERVICE_ORDER_GUIDE_KEY = "astrlink.service-order-guide.v1";

type Strategy = "soonest_reset" | "most_remaining";

// One loop: lift New API, drop it in front, then open the Codex Plus card and
// show which account each strategy sends a request to.
const frames: readonly {
  order: readonly string[];
  lifted: string | null;
  strategy: Strategy | null;
  step: "grab" | "raise" | "soonest" | "most";
}[] = [
  {
    order: ["codex", "openai", "newapi"],
    lifted: null,
    strategy: null,
    step: "grab",
  },
  {
    order: ["codex", "openai", "newapi"],
    lifted: "newapi",
    strategy: null,
    step: "grab",
  },
  {
    order: ["newapi", "codex", "openai"],
    lifted: "newapi",
    strategy: null,
    step: "raise",
  },
  {
    order: ["newapi", "codex", "openai"],
    lifted: null,
    strategy: null,
    step: "raise",
  },
  {
    order: ["newapi", "codex", "openai"],
    lifted: null,
    strategy: "soonest_reset",
    step: "soonest",
  },
  {
    order: ["newapi", "codex", "openai"],
    lifted: null,
    strategy: "most_remaining",
    step: "most",
  },
];
const times = [0, 1000, 1900, 3000, 4000, 6800] as const;
const hold = 3200;

const cards = {
  codex: { kind: "codex_subscription", name: "Codex Plus", count: 2, left: 60 },
  openai: { kind: "openai", name: "OpenAI", count: 1, left: null },
  newapi: { kind: "newapi", name: "New API", count: 1, left: 88 },
} as const;

// Account A has more left; account B resets sooner.
const accounts = [
  { id: "a", name: "A", left: 62, resetHours: 3 },
  { id: "b", name: "B", left: 35, resetHours: 1 },
] as const;
const picked: Record<Strategy, string> = {
  soonest_reset: "b",
  most_remaining: "a",
};

const ease = [0.22, 1, 0.36, 1] as const;

function DemoBar({ left }: { left: number | null }) {
  return left === null ? (
    <span className="block h-1.5 rounded-full bg-[repeating-linear-gradient(135deg,var(--muted)_0_4px,var(--background)_4px_8px)] ring-1 ring-border/60 ring-inset" />
  ) : (
    <span className="block h-1.5 overflow-hidden rounded-full bg-muted">
      <span
        className="block h-full rounded-full bg-success"
        style={{ width: `${left}%` }}
      />
    </span>
  );
}

function OrderAnimation() {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const { step } = useLoopStep(times, hold, true);
  const current = frames[step]!;
  const duration = reducedMotion ? 0 : 0.6;
  return (
    <div className="min-w-0" data-order-guide-step={current.step}>
      <div
        aria-hidden="true"
        className="relative grid gap-2 rounded-lg border bg-muted/30 p-3"
      >
        <span className="text-micro text-muted-foreground">
          {t("services.orderGuide.first")} →
        </span>
        <ol
          aria-label={t("services.orderGuide.examples")}
          className="m-0 grid list-none grid-cols-3 gap-2 p-0"
        >
          {current.order.map((id, index) => {
            const card = cards[id as keyof typeof cards];
            const lifted = current.lifted === id;
            const open = id === "codex" && current.strategy !== null;
            return (
              <motion.li
                key={id}
                layout={!reducedMotion}
                animate={{ scale: lifted ? 1.05 : 1 }}
                transition={{ duration, ease }}
                data-preview-item={id}
                data-lifted={lifted || undefined}
                className={cn(
                  "relative grid min-w-0 gap-2 rounded-lg border bg-card p-2",
                  index === 0 && "border-primary/50",
                  lifted && "z-10 shadow-lg ring-1 ring-primary/50",
                  open && "ring-2 ring-primary/40",
                )}
              >
                <span className="flex min-w-0 items-center gap-1.5">
                  <PackageLogo count={card.count}>
                    <ServiceKindIcon kind={card.kind} />
                  </PackageLogo>
                  <span className="min-w-0 truncate text-xs font-medium">
                    {card.name}
                  </span>
                </span>
                <DemoBar left={card.left} />
              </motion.li>
            );
          })}
        </ol>
        <motion.div
          animate={{ opacity: current.strategy ? 1 : 0 }}
          className="grid gap-2 rounded-lg border bg-card p-2"
          data-demo-tray=""
          initial={false}
          transition={{ duration }}
        >
          <span className="flex flex-wrap gap-1 text-micro">
            {(["soonest_reset", "most_remaining"] as const).map((name) => (
              <span
                className={cn(
                  "rounded-md px-1.5 py-0.5 transition-colors",
                  current.strategy === name
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-muted-foreground",
                )}
                data-demo-strategy={name}
                data-active={current.strategy === name || undefined}
                key={name}
              >
                {t(`packages.strategies.${name}`)}
              </span>
            ))}
          </span>
          <span className="grid grid-cols-2 gap-2">
            {accounts.map((account) => {
              const first =
                current.strategy !== null &&
                picked[current.strategy] === account.id;
              return (
                <span
                  className={cn(
                    "grid min-w-0 gap-1 rounded-md border p-1.5 text-micro transition-colors",
                    first && "border-primary bg-primary/5",
                  )}
                  data-demo-account={account.id}
                  data-first={first || undefined}
                  key={account.id}
                >
                  <span className="flex items-center justify-between gap-1">
                    <span className="font-medium">
                      {t("services.orderGuide.account", {
                        name: account.name,
                      })}
                    </span>
                    {first ? (
                      <span className="font-medium text-primary">
                        {t("services.orderGuide.first")}
                      </span>
                    ) : null}
                  </span>
                  <DemoBar left={account.left} />
                  <span className="text-muted-foreground">
                    {t("services.orderGuide.accountState", {
                      left: account.left,
                      hours: account.resetHours,
                    })}
                  </span>
                </span>
              );
            })}
          </span>
        </motion.div>
      </div>
      <p className="mt-3 min-h-15 text-sm leading-5" role="status">
        {t(`services.orderGuide.${current.step}`)}
      </p>
    </div>
  );
}

export function ServiceOrderHelp({
  ready,
  providerCount,
}: {
  ready: boolean;
  providerCount: number;
}) {
  const t = useT();
  // The demo explains the order between providers and which account in a card
  // goes first, so it first opens once there are two providers. A provider is
  // each configured service, not a package card: cards group asynchronously,
  // which would make the first open racy.
  const [guideOpen, setGuideOpen] = useFirstVisitGuide(
    SERVICE_ORDER_GUIDE_KEY,
    ready && providerCount > 1,
  );

  return (
    <GuideDialog
      description={t("services.orderGuide.description")}
      dismissLabel={t("services.orderGuide.dismiss")}
      note={t("services.orderGuide.note")}
      onOpenChange={setGuideOpen}
      open={guideOpen}
      replayLabel={t("services.orderGuide.replay")}
      title={t("services.orderGuide.title")}
      triggerLabel={t("services.orderLabel")}
      triggerSize="icon"
    >
      <OrderAnimation />
    </GuideDialog>
  );
}
