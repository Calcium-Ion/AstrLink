import type { AppSnapshot } from "../core-model";
import { useT } from "../i18n";
import { FormMessage } from "./FormMessage";

export function InferencePortNotice({
  snapshot,
}: {
  snapshot: AppSnapshot | null;
}) {
  const t = useT();
  const fallback =
    snapshot?.phase === "ready" ? snapshot.inference_port_fallback : null;
  if (!fallback) return null;
  return (
    <FormMessage tone="warning">
      {t("core.portFallback", {
        port: fallback.requested_port,
        address: snapshot?.ready?.inference_url,
      })}
    </FormMessage>
  );
}
