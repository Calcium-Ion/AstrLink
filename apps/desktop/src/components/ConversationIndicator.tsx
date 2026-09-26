import { useCallback, useId, useRef, useState } from "react";
import { Link } from "./icons";
import { AnchoredTooltip } from "./ui/tooltip";
import { useT } from "../i18n";

/** The accessible status label also has a hover/click hint inside request rows. */
export function ConversationIndicator({
  kind,
}: {
  kind: "continuation" | "stickiness";
}) {
  const t = useT();
  const id = useId();
  const [anchor, setAnchor] = useState<HTMLSpanElement | null>(null);
  const pinned = useRef(false);
  const dismiss = useCallback(() => {
    pinned.current = false;
    setAnchor(null);
  }, []);
  const label = t(
    kind === "continuation"
      ? "trajectory.continuation"
      : "routingDecision.selections.session_binding",
  );
  return (
    <>
      <span
        role="img"
        aria-label={label}
        aria-describedby={anchor ? id : undefined}
        title=""
        data-conversation-indicator={kind}
        className="inline-flex size-4 shrink-0 cursor-help items-center justify-center text-muted-foreground"
        onPointerEnter={(event) => {
          if (event.pointerType !== "touch") setAnchor(event.currentTarget);
        }}
        onPointerLeave={() => {
          if (!pinned.current) dismiss();
        }}
        onPointerDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          pinned.current = true;
          setAnchor(event.currentTarget);
        }}
      >
        <Link aria-hidden="true" animateOnHover={false} className="size-3.5" />
      </span>
      {anchor ? (
        <AnchoredTooltip anchor={anchor} id={id} onDismiss={dismiss}>
          {label}
        </AnchoredTooltip>
      ) : null}
    </>
  );
}
