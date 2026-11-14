import { i18n } from "./i18n";

/** The host data that failed to parse; the message names it. */
export type DataSubject =
  | "rawSealing"
  | "rawAccess"
  | "localData"
  | "requestRecord"
  | "auditSettings"
  | "plugin"
  | "metadata";

/** Why a value was rejected. */
export type DataProblem =
  | "object"
  | "boolean"
  | "string"
  | "array"
  | "timestamp"
  | "timestampOrNull"
  | "integerAtLeast"
  | "positiveInteger"
  | "nonNegativeInteger"
  | "tooLong"
  | "badFormat"
  | "duplicate"
  | "unknownType"
  | "unknownStatus"
  | "unknownDecision"
  | "unknownScope"
  | "unknownOutcome"
  | "unsupportedField"
  | "belowMinimumLength"
  | "integer"
  | "integerBetween"
  | "number"
  | "nonNegativeNumber"
  | "exceedsTotal"
  | "tooMany"
  | "empty"
  | "missing"
  | "unknownReason"
  | "unexpectedValue";

/**
 * Throws for host data that failed validation. The UI shows these messages
 * as they are, so they follow the operator's language.
 */
export function invalidData(
  subject: DataSubject,
  path: string,
  problem: DataProblem,
  values: Record<string, number> = {},
): never {
  throw new Error(
    i18n.t(`dataErrors.${subject}`, {
      path,
      detail: i18n.t(`dataErrors.problems.${problem}`, values),
    }),
  );
}
