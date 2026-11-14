import type { useT } from "./i18n";

type Translate = ReturnType<typeof useT>;

/** A capability in the user's words: request.write.raw reads plugins.capabilities.requestWriteRaw. */
export function capabilityLabel(capability: string, t: Translate): string {
  const key = capability.replace(/\.([a-z])/g, (_, letter: string) =>
    letter.toUpperCase(),
  );
  return t(`plugins.capabilities.${key}`, { defaultValue: capability });
}

/** An extension point in the user's words, by the hook that serves it. */
export function hookLabel(hook: string, t: Translate): string {
  return t(`plugins.hooks.${hook}`, { defaultValue: hook });
}
