import { useEffect, useRef, useState, type RefObject } from "react";

import { ClientTypeIcons } from "./components/ClientTypeIcon";
import { useFirstVisitGuide } from "./components/GuideDialog";
import { IconButton } from "./components/IconButton";
import { SpotlightTour } from "./components/SpotlightTour";
import { Check, CircleHelp } from "./components/icons";
import { useT } from "./i18n";
import { protocolClientTypes, protocolLabels } from "./service-presets";

export const SERVICE_EDITOR_TOUR_KEY = "astrlink.service-editor-tour.v1";

/** Let the editor's reveal settle before the first visit measures its tabs. */
const firstVisitDelay = 400;

// Clients are named first; the protocol stays beside them for advanced users.
const clientProtocols: readonly { protocol: string; name?: string }[] = [
  { protocol: "openai.responses", name: "Codex" },
  { protocol: "anthropic.messages", name: "Claude Code" },
  { protocol: "openai.chat" },
  { protocol: "google.generate_content", name: "Gemini CLI" },
];

function ClientSupport({ protocols }: { protocols: readonly string[] }) {
  const t = useT();
  return (
    <ul className="m-0 mt-2 grid list-none gap-0.5 p-0">
      {clientProtocols.map((item) => {
        const supported = protocols.includes(item.protocol);
        return (
          <li
            className="flex min-w-0 items-center gap-2.5 py-1"
            data-client-protocol={item.protocol}
            data-supported={supported || undefined}
            key={item.protocol}
          >
            <ClientTypeIcons
              className="w-10"
              clientTypes={protocolClientTypes[item.protocol]}
              size={18}
            />
            <span className="grid min-w-0 flex-1">
              <span className="truncate font-medium text-foreground">
                {item.name ?? t("services.editorTour.chatClients")}
              </span>
              <span className="truncate text-micro">
                {protocolLabels[item.protocol]}
              </span>
            </span>
            {supported ? (
              <span className="flex shrink-0 items-center gap-1 font-medium text-success">
                <Check
                  animateOnHover={false}
                  aria-hidden="true"
                  className="size-3.5"
                />
                {t("services.editorTour.supported")}
              </span>
            ) : (
              <span className="shrink-0">
                {t("services.editorTour.notEnabled")}
              </span>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/**
 * Tours the editor tabs in `root`, each marked with `data-tour-target`. Mounted
 * with the editor form, so the first visit starts it once.
 */
export function ServiceEditorTour({
  root,
  modelCount,
  protocols,
}: {
  root: RefObject<HTMLElement | null>;
  modelCount: number;
  /** Enabled entry protocols, shown as the clients they let in. */
  protocols: readonly string[];
}) {
  const t = useT();
  const [settled, setSettled] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(true), firstVisitDelay);
    return () => window.clearTimeout(timer);
  }, []);
  const [open, setOpen] = useFirstVisitGuide(SERVICE_EDITOR_TOUR_KEY, settled);
  // A replay restarts from the first step, even while the tour is open.
  const [run, setRun] = useState(0);
  const trigger = useRef<HTMLButtonElement>(null);
  const steps = [
    {
      id: "connection",
      title: t("services.tabConnection"),
      body: t("services.editorTour.connection"),
    },
    {
      id: "models",
      title: t("services.tabModels"),
      body: (
        <>
          {t("services.editorTour.models")}
          {modelCount === 0 ? (
            <span className="mt-1.5 block font-medium text-foreground">
              {t("services.editorTour.modelsEmpty")}
            </span>
          ) : null}
        </>
      ),
    },
    {
      id: "protocols",
      title: t("services.tabProtocols"),
      body: (
        <>
          {t("services.editorTour.protocols")}
          <ClientSupport protocols={protocols} />
        </>
      ),
    },
    {
      id: "failure",
      title: t("failure.title"),
      body: t("services.editorTour.failure"),
    },
  ];
  return (
    <>
      <IconButton
        ref={trigger}
        label={t("services.editorTour.help")}
        onClick={() => {
          setRun((value) => value + 1);
          setOpen(true);
        }}
        type="button"
      >
        <CircleHelp aria-hidden="true" className="text-muted-foreground" />
      </IconButton>
      <SpotlightTour
        labels={{
          next: t("services.editorTour.next"),
          back: t("services.editorTour.back"),
          skip: t("services.editorTour.skip"),
          done: t("services.editorTour.done"),
          progress: (current, total) =>
            t("services.editorTour.progress", { current, total }),
        }}
        onOpenChange={setOpen}
        open={open}
        returnFocus={trigger}
        root={root}
        run={run}
        steps={steps}
      />
    </>
  );
}
