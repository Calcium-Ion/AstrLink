import type { useT } from "./i18n";
import type { PluginTaskProtocol, PluginTaskRoute } from "./plugin-model";

type Translate = ReturnType<typeof useT>;

/** The gateway endpoints a protocol of a task plugin answers on. */
export const TASK_PROTOCOL_ENDPOINTS: Record<string, string[]> = {
  openai_video: ["/v1/videos"],
  openai_image: ["/v1/images/generations", "/v1/images/edits"],
  openai_responses: ["/v1/responses"],
};

/** A protocol of a task plugin in the interface language: 视频, 图片, Responses. */
export function taskProtocolLabel(name: string, t: Translate): string {
  return t(`plugins.task.protocols.${name}`, { defaultValue: name });
}

/** The forms of Responses request a claim takes, in words. */
export function taskModesLabel(
  protocol: PluginTaskProtocol,
  t: Translate,
): string {
  return (protocol.supports ?? [])
    .map((mode) => t(`plugins.task.modes.${mode}`, { defaultValue: mode }))
    .join(" · ");
}

/**
 * The short labels of what a task plugin serves, for a row or a card: its
 * protocols, then its native routes when it has any.
 */
export function taskSurfaceLabels(
  protocols: PluginTaskProtocol[],
  routes: PluginTaskRoute[],
  t: Translate,
): string[] {
  const labels = protocols.map((protocol) =>
    taskProtocolLabel(protocol.name, t),
  );
  if (routes.length > 0) labels.push(t("plugins.task.nativeRoutes"));
  return labels;
}

/** How a task plugin's provider authenticates, in words. */
export function taskAuthLabel(auth: string, t: Translate): string {
  return t(`plugins.task.auth.${auth}`, { defaultValue: auth });
}

/** A task's status in words; the tone its badge takes. */
export function taskStatusLabel(status: string, t: Translate): string {
  return t(`plugins.task.status.${status}`, { defaultValue: status });
}

export function taskStatusTone(
  status: string,
): "positive" | "negative" | "pending" | "neutral" {
  switch (status) {
    case "SUCCESS":
      return "positive";
    case "FAILURE":
      return "negative";
    case "UNKNOWN":
      return "neutral";
  }
  return "pending";
}

/** Where a task came from: a protocol, or a native route. */
export function taskSurfaceLabel(surface: string, t: Translate): string {
  return surface === "native"
    ? t("plugins.task.nativeRoutes")
    : taskProtocolLabel(surface, t);
}
