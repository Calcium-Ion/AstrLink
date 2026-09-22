import { useCallback, useEffect, useRef, useState } from "react";

import { i18n } from "./i18n";

export type CopyState = "idle" | "copied" | "failed";

export interface CopyFeedback {
  /** Key of the copy target currently showing feedback, or null. */
  activeKey: string | null;
  state: CopyState;
  copy: (key: string, text: string) => void;
}

const FEEDBACK_DURATION_MS = 1600;

async function writeClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  // WKWebView has shown silent no-op clipboard behavior; a visible
  // fallback beats a button that appears to do nothing.
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    if (!document.execCommand("copy")) {
      throw new Error("execCommand copy failed");
    }
  } finally {
    area.remove();
  }
}

export function useCopyFeedback(): CopyFeedback {
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [state, setState] = useState<CopyState>("idle");
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timerRef.current !== null) clearTimeout(timerRef.current);
    },
    [],
  );

  const copy = useCallback((key: string, text: string) => {
    void writeClipboard(text)
      .then(() => {
        setActiveKey(key);
        setState("copied");
      })
      .catch(() => {
        setActiveKey(key);
        setState("failed");
      })
      .finally(() => {
        if (timerRef.current !== null) clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => {
          setActiveKey(null);
          setState("idle");
        }, FEEDBACK_DURATION_MS);
      });
  }, []);

  return { activeKey, state, copy };
}

export function copyButtonLabel(
  feedback: CopyFeedback,
  key: string,
  idleLabel = i18n.t("common.copy"),
  copiedLabel = i18n.t("common.copied"),
): string {
  if (feedback.activeKey !== key) return idleLabel;
  return feedback.state === "failed"
    ? i18n.t("common.copyFailed")
    : copiedLabel;
}
