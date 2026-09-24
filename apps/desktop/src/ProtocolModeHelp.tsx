import { useEffect, useState } from "react";
import { useReducedMotion } from "motion/react";

import astrlinkLogo from "./assets/astrlink-logo.svg";
import { AgentToolIcon } from "./components/AgentToolIcon";
import { GuideDialog, useFirstVisitGuide } from "./components/GuideDialog";
import { RequestFlowPreview } from "./components/RequestFlowPreview";
import { Server } from "./components/icons";
import { useT } from "./i18n";

export const PROTOCOL_MODE_GUIDE_KEY = "astrlink.protocol-mode-guide.v1";

// Both lanes share one clock so the same request can be compared side by side.
const frames = [
  { at: 0, position: 0, rewritten: false },
  { at: 900, position: 1, rewritten: false },
  { at: 1800, position: 1, rewritten: true },
  { at: 2800, position: 2, rewritten: true },
] as const;

const lanes = ["passthrough", "convert"] as const;
type Lane = (typeof lanes)[number];

function ProtocolModeLane({
  lane,
  frame,
}: {
  lane: Lane;
  frame: (typeof frames)[number];
}) {
  const t = useT();
  const passthrough = lane === "passthrough";
  const rewritten = !passthrough && frame.rewritten;
  const mode = t(
    passthrough
      ? "services.protocolModes.passthroughMode"
      : "services.protocolModes.convertMode",
  );
  return (
    <section
      className="flex min-w-0 flex-col rounded-md border p-3"
      data-protocol-lane={lane}
    >
      <h3 className="text-sm font-medium">{mode}</h3>
      <RequestFlowPreview
        label={t("services.protocolModes.flow", { mode })}
        nodes={[
          {
            id: "client",
            name: "Codex",
            caption: t("services.protocolModes.client"),
            icon: <AgentToolIcon id="codex" size={22} />,
          },
          {
            id: "astrlink",
            name: "AstrLink",
            caption: t(
              passthrough
                ? "services.protocolModes.unchanged"
                : "services.protocolModes.rewrite",
            ),
            icon: (
              <img
                alt=""
                className="size-7"
                height={28}
                src={astrlinkLogo}
                width={28}
              />
            ),
          },
          {
            id: "upstream",
            name: t("services.protocolModes.upstream"),
            caption: t(
              passthrough
                ? "services.protocolModes.upstreamNative"
                : "services.protocolModes.upstreamChatOnly",
            ),
            icon: (
              <Server
                animateOnHover={false}
                aria-hidden="true"
                className="size-5"
              />
            ),
          },
        ]}
        packet={{
          id: lane,
          protocol: rewritten ? "Chat" : "Responses",
          rewritten,
          parts: [
            {
              id: "messages",
              label: t("services.protocolModes.messages"),
              lost: false,
            },
            {
              id: "functions",
              label: t("services.protocolModes.functions"),
              lost: false,
            },
            {
              id: "codexTools",
              label: t("services.protocolModes.codexTools"),
              lost: rewritten,
            },
          ],
        }}
        position={frame.position}
      />
      <p className="mt-2 text-xs leading-5 text-muted-foreground">
        {t(
          passthrough
            ? "services.protocolModes.passthroughResult"
            : "services.protocolModes.convertResult",
        )}
      </p>
    </section>
  );
}

function ProtocolModeAnimation() {
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
  return (
    <div className="grid min-w-0 gap-3 sm:grid-cols-2">
      {lanes.map((lane) => (
        <ProtocolModeLane frame={current} key={lane} lane={lane} />
      ))}
    </div>
  );
}

/** Mounted with the 入口协议 tab, so the first visit plays the demo once. */
export function ProtocolModeHelp() {
  const t = useT();
  const [guideOpen, setGuideOpen] = useFirstVisitGuide(PROTOCOL_MODE_GUIDE_KEY);
  return (
    <GuideDialog
      className="sm:max-w-3xl"
      description={t("services.protocolModes.description")}
      dismissLabel={t("services.protocolModes.dismiss")}
      note={t("services.protocolModes.tip")}
      onOpenChange={setGuideOpen}
      open={guideOpen}
      replayLabel={t("services.protocolModes.replay")}
      title={t("services.protocolModes.title")}
      triggerLabel={t("services.protocolModes.help")}
      triggerVariant="button"
    >
      <ProtocolModeAnimation />
    </GuideDialog>
  );
}
