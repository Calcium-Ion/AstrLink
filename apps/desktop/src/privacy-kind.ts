import { i18n } from "./i18n";

/**
 * The privacy kinds Core records, in the order the UI lists them. A hit or a
 * finding carries one of these; anything else is a newer Core label.
 */
export const PRIVACY_KIND_ORDER = [
  "email",
  "phone",
  "account",
  "payment_card",
  "ip_address",
  "url",
  "common_secret",
  "private_address",
  "private_date",
  "private_person",
  "unknown",
] as const;

export type PrivacyKind = (typeof PRIVACY_KIND_ORDER)[number];

const knownKinds = new Set<string>(PRIVACY_KIND_ORDER);

const CUSTOM_KIND_PREFIX = "custom_";

/**
 * Display names of the policy's custom kinds, by kind. Whoever reads the
 * policy refreshes it, so records and statistics name a custom kind the way
 * the user called it.
 */
let customKindLabels = new Map<string, string>();

export function rememberCustomPrivacyKindLabels(
  rules: readonly { kind: string; label?: string }[],
): void {
  const next = new Map<string, string>();
  for (const rule of rules) {
    if (rule.kind.startsWith(CUSTOM_KIND_PREFIX) && rule.label) {
      next.set(rule.kind, rule.label);
    }
  }
  customKindLabels = next;
}

/**
 * A kind's own name, so an unrecognized label still reads as itself. A custom
 * kind reads as its display name, or by its ID once the policy no longer
 * declares it.
 */
export function privacyKindLabel(kind: string): string {
  if (knownKinds.has(kind)) return i18n.t(`privacy.${kind}`);
  if (kind.startsWith(CUSTOM_KIND_PREFIX)) {
    return (
      customKindLabels.get(kind) ??
      i18n.t("privacy.customKind", {
        id: kind.slice(CUSTOM_KIND_PREFIX.length),
      })
    );
  }
  return kind;
}

/**
 * The built-in kinds the UI knows about first, then custom kinds, then the
 * unknown stand-in, then unrecognized labels; each group alphabetically
 * beyond the built-in order.
 */
function kindRank(kind: string): number {
  if (kind === "unknown") return PRIVACY_KIND_ORDER.length + 1;
  const index = PRIVACY_KIND_ORDER.indexOf(kind as PrivacyKind);
  if (index !== -1) return index;
  if (kind.startsWith(CUSTOM_KIND_PREFIX)) return PRIVACY_KIND_ORDER.length;
  return PRIVACY_KIND_ORDER.length + 2;
}

export function sortPrivacyKinds(kinds: string[]): string[] {
  return [...kinds].sort(
    (left, right) =>
      kindRank(left) - kindRank(right) || left.localeCompare(right),
  );
}
