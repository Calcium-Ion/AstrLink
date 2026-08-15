export const DEV_BUILD_ID_PATH = "/__astrlink_build";

export function isTauriRuntime(
  target: Window & { __TAURI_INTERNALS__?: unknown } = window,
): boolean {
  return "__TAURI_INTERNALS__" in target;
}

export function startDevWebviewReload({
  enabled = true,
  fetchImpl = fetch,
  intervalMs = 750,
  reload = () => window.location.reload(),
  target = window,
}: {
  enabled?: boolean;
  fetchImpl?: typeof fetch;
  intervalMs?: number;
  reload?: () => void;
  target?: Window;
} = {}): () => void {
  if (!enabled) return () => undefined;

  let stopped = false;
  let seen: string | null = null;
  let inFlight = false;

  const onKeyDown = (event: KeyboardEvent) => {
    if (
      !(event.metaKey || event.ctrlKey) ||
      event.altKey ||
      event.shiftKey ||
      (event.key !== "r" && event.key !== "R")
    ) {
      return;
    }
    event.preventDefault();
    reload();
  };
  target.addEventListener("keydown", onKeyDown);

  const tick = async () => {
    if (stopped || inFlight) return;
    inFlight = true;
    try {
      const response = await fetchImpl(DEV_BUILD_ID_PATH, { cache: "no-store" });
      if (!response.ok) return;
      const id = (await response.text()).trim();
      if (!id) return;
      if (seen === null) {
        seen = id;
        return;
      }
      if (id !== seen) {
        seen = id;
        reload();
      }
    } catch {
      // The dev server can drop briefly while Rsbuild restarts.
    } finally {
      inFlight = false;
    }
  };

  void tick();
  const timer = target.setInterval(() => void tick(), intervalMs);

  return () => {
    stopped = true;
    target.removeEventListener("keydown", onKeyDown);
    target.clearInterval(timer);
  };
}
